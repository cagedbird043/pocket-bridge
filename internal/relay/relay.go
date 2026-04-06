package relay

import (
	"context"
	"log"
	"net/http"
	"sync"
	"time"

	pocketbridgev1 "github.com/cagedbird043/pocket-bridge/gen/go/pocketbridge/v1"
	"github.com/cagedbird043/pocket-bridge/internal/auth"
	"github.com/cagedbird043/pocket-bridge/internal/config"
	"github.com/cagedbird043/pocket-bridge/internal/pbwire"
	"github.com/gorilla/websocket"
)

type Server struct {
	cfg      *config.RelayConfig
	upgrader websocket.Upgrader

	mu      sync.RWMutex
	clients map[string]*clientConn
}

type clientConn struct {
	deviceID string
	conn     *websocket.Conn
	writeMu  sync.Mutex
}

func New(cfg *config.RelayConfig) *Server {
	return &Server{
		cfg: cfg,
		upgrader: websocket.Upgrader{
			CheckOrigin: func(r *http.Request) bool { return true },
		},
		clients: make(map[string]*clientConn),
	}
}

func (s *Server) Run(ctx context.Context) error {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", s.handleHealthz)
	mux.HandleFunc("/ws", s.handleWS)

	srv := &http.Server{
		Addr:    s.cfg.ListenAddr,
		Handler: mux,
	}

	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
	}()

	log.Printf("relay listening on %s", s.cfg.ListenAddr)
	err := srv.ListenAndServe()
	if err == http.ErrServerClosed {
		return nil
	}
	return err
}

func (s *Server) handleHealthz(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok\n"))
}

func (s *Server) handleWS(w http.ResponseWriter, r *http.Request) {
	conn, err := s.upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("relay upgrade failed: %v", err)
		return
	}

	cc := &clientConn{conn: conn}
	deviceID, err := s.authenticate(cc)
	if err != nil {
		log.Printf("relay auth failed: %v", err)
		_ = cc.conn.Close()
		return
	}

	cc.deviceID = deviceID
	s.register(cc)
	defer s.unregister(deviceID, cc)

	log.Printf("relay device online: %s", deviceID)
	defer log.Printf("relay device offline: %s", deviceID)

	for {
		env, err := readEnvelope(conn)
		if err != nil {
			return
		}
		if env.FromDeviceId != "" && env.FromDeviceId != deviceID {
			_ = s.sendError(cc, env.Id, "from_device_mismatch", "from_device_id does not match authenticated device")
			continue
		}
		env.FromDeviceId = deviceID
		if env.UnixMs == 0 {
			env.UnixMs = time.Now().UnixMilli()
		}

		switch env.Payload.(type) {
		case *pocketbridgev1.Envelope_DeviceHello, *pocketbridgev1.Envelope_AuthChallenge, *pocketbridgev1.Envelope_AuthResponse:
			_ = s.sendError(cc, env.Id, "unexpected_auth_payload", "auth payload is only allowed during handshake")
			continue
		default:
			if env.ToDeviceId == "" {
				_ = s.sendError(cc, env.Id, "missing_target", "to_device_id is required")
				continue
			}
			target := s.lookup(env.ToDeviceId)
			if target == nil {
				_ = s.sendError(cc, env.Id, "target_offline", "target device is not connected")
				continue
			}
			if err := writeEnvelope(target, env); err != nil {
				_ = s.sendError(cc, env.Id, "target_write_failed", err.Error())
				continue
			}
			if err := s.sendAck(cc, env.Id); err != nil {
				return
			}
		}
	}
}

func (s *Server) authenticate(cc *clientConn) (string, error) {
	helloEnv, err := readEnvelope(cc.conn)
	if err != nil {
		return "", err
	}
	hello, ok := helloEnv.Payload.(*pocketbridgev1.Envelope_DeviceHello)
	if !ok {
		_ = s.sendErrorWithTarget(cc, "", helloEnv.Id, "missing_device_hello", "first message must be device_hello")
		return "", errAuthFailed
	}

	deviceID := hello.DeviceHello.DeviceId
	if deviceID == "" {
		_ = s.sendErrorWithTarget(cc, "", helloEnv.Id, "missing_device_id", "device_hello.device_id is required")
		return "", errAuthFailed
	}

	publicKey, err := s.lookupPublicKey(deviceID)
	if err != nil {
		_ = s.sendErrorWithTarget(cc, deviceID, helloEnv.Id, "unknown_device", err.Error())
		return "", errAuthFailed
	}

	challengeData, err := auth.BuildChallenge(deviceID)
	if err != nil {
		return "", err
	}
	if err := writeEnvelope(cc, &pocketbridgev1.Envelope{
		Id:           nextID(),
		FromDeviceId: "relay",
		ToDeviceId:   deviceID,
		UnixMs:       time.Now().UnixMilli(),
		Payload: &pocketbridgev1.Envelope_AuthChallenge{
			AuthChallenge: &pocketbridgev1.AuthChallenge{
				ChallengeData: challengeData,
				Algorithm:     auth.AlgorithmEd25519,
			},
		},
	}); err != nil {
		return "", err
	}

	responseEnv, err := readEnvelope(cc.conn)
	if err != nil {
		return "", err
	}
	response, ok := responseEnv.Payload.(*pocketbridgev1.Envelope_AuthResponse)
	if !ok {
		_ = s.sendErrorWithTarget(cc, deviceID, responseEnv.Id, "missing_auth_response", "second message must be auth_response")
		return "", errAuthFailed
	}
	if !auth.VerifyChallenge(publicKey, challengeData, response.AuthResponse.Signature) {
		_ = s.sendErrorWithTarget(cc, deviceID, responseEnv.Id, "invalid_signature", "auth signature verification failed")
		return "", errAuthFailed
	}
	if err := s.sendAckWithTarget(cc, deviceID, helloAckID(deviceID)); err != nil {
		return "", err
	}
	return deviceID, nil
}

func (s *Server) lookupPublicKey(deviceID string) ([]byte, error) {
	device, ok := s.cfg.Devices[deviceID]
	if !ok {
		return nil, errUnknownDevice(deviceID)
	}
	return auth.ParsePublicKeyBase64(device.PublicKeyBase64)
}

func (s *Server) register(cc *clientConn) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if prev, ok := s.clients[cc.deviceID]; ok {
		_ = prev.conn.Close()
	}
	s.clients[cc.deviceID] = cc
}

func (s *Server) unregister(deviceID string, cc *clientConn) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if current, ok := s.clients[deviceID]; ok && current == cc {
		delete(s.clients, deviceID)
	}
	_ = cc.conn.Close()
}

func (s *Server) lookup(deviceID string) *clientConn {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.clients[deviceID]
}

func (s *Server) sendAck(cc *clientConn, ackID string) error {
	return s.sendAckWithTarget(cc, cc.deviceID, ackID)
}

func (s *Server) sendAckWithTarget(cc *clientConn, targetDeviceID, ackID string) error {
	return writeEnvelope(cc, &pocketbridgev1.Envelope{
		Id:           nextID(),
		FromDeviceId: "relay",
		ToDeviceId:   targetDeviceID,
		UnixMs:       time.Now().UnixMilli(),
		Payload: &pocketbridgev1.Envelope_Ack{
			Ack: &pocketbridgev1.Ack{AckId: ackID},
		},
	})
}

func (s *Server) sendError(cc *clientConn, refID, code, message string) error {
	return s.sendErrorWithTarget(cc, cc.deviceID, refID, code, message)
}

func (s *Server) sendErrorWithTarget(cc *clientConn, targetDeviceID, refID, code, message string) error {
	return writeEnvelope(cc, &pocketbridgev1.Envelope{
		Id:           nextID(),
		FromDeviceId: "relay",
		ToDeviceId:   targetDeviceID,
		UnixMs:       time.Now().UnixMilli(),
		Payload: &pocketbridgev1.Envelope_Error{
			Error: &pocketbridgev1.Error{
				Code:    code,
				Message: message + refSuffix(refID),
			},
		},
	})
}

func readEnvelope(conn *websocket.Conn) (*pocketbridgev1.Envelope, error) {
	msgType, payload, err := conn.ReadMessage()
	if err != nil {
		return nil, err
	}
	if msgType != websocket.BinaryMessage {
		return nil, errUnsupportedMessageType
	}
	env, err := pbwire.UnmarshalEnvelope(payload)
	if err != nil {
		return nil, errMalformedEnvelope(err)
	}
	return env, nil
}

func writeEnvelope(cc *clientConn, env *pocketbridgev1.Envelope) error {
	data, err := pbwire.MarshalEnvelope(env)
	if err != nil {
		return err
	}
	cc.writeMu.Lock()
	defer cc.writeMu.Unlock()
	return cc.conn.WriteMessage(websocket.BinaryMessage, data)
}

func helloAckID(deviceID string) string {
	return "hello:" + deviceID
}

func refSuffix(ref string) string {
	if ref == "" {
		return ""
	}
	return " (ref=" + ref + ")"
}

func nextID() string {
	return time.Now().UTC().Format("20060102T150405.000000000")
}

var (
	errAuthFailed             = &relayError{message: "authentication failed"}
	errUnsupportedMessageType = &relayError{message: "relay expects binary protobuf messages"}
)

type relayError struct {
	message string
}

func (e *relayError) Error() string {
	return e.message
}

func errMalformedEnvelope(err error) error {
	return &relayError{message: "bad envelope: " + err.Error()}
}

func errUnknownDevice(deviceID string) error {
	return &relayError{message: "unknown device: " + deviceID}
}
