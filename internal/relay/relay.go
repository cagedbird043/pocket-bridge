package relay

import (
	"context"
	"log"
	"net/http"
	"sync"
	"time"

	pocketbridgev1 "github.com/cagedbird043/pocket-bridge/gen/go/pocketbridge/v1"
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
	deviceID := r.URL.Query().Get("device_id")
	token := r.Header.Get("Authorization")
	token = trimBearer(token)

	if !s.authorized(deviceID, token) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	conn, err := s.upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("relay upgrade failed for %s: %v", deviceID, err)
		return
	}

	cc := &clientConn{
		deviceID: deviceID,
		conn:     conn,
	}
	s.register(cc)
	defer s.unregister(deviceID, cc)

	log.Printf("relay device online: %s", deviceID)
	defer log.Printf("relay device offline: %s", deviceID)

	if err := s.sendAck(cc, helloAckID(deviceID)); err != nil {
		log.Printf("relay hello ack failed for %s: %v", deviceID, err)
		return
	}

	for {
		msgType, payload, err := conn.ReadMessage()
		if err != nil {
			return
		}
		if msgType != websocket.BinaryMessage {
			_ = s.sendError(cc, "", "unsupported_message_type", "relay expects binary protobuf messages")
			continue
		}

		env, err := pbwire.UnmarshalEnvelope(payload)
		if err != nil {
			_ = s.sendError(cc, "", "bad_envelope", err.Error())
			continue
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
		case *pocketbridgev1.Envelope_DeviceHello:
			if err := s.sendAck(cc, env.Id); err != nil {
				return
			}
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

func (s *Server) authorized(deviceID, token string) bool {
	if deviceID == "" || token == "" {
		return false
	}
	device, ok := s.cfg.Devices[deviceID]
	if !ok {
		return false
	}
	return device.Token == token
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
	return writeEnvelope(cc, &pocketbridgev1.Envelope{
		Id:           nextID(),
		FromDeviceId: "relay",
		ToDeviceId:   cc.deviceID,
		UnixMs:       time.Now().UnixMilli(),
		Payload: &pocketbridgev1.Envelope_Ack{
			Ack: &pocketbridgev1.Ack{AckId: ackID},
		},
	})
}

func (s *Server) sendError(cc *clientConn, refID, code, message string) error {
	return writeEnvelope(cc, &pocketbridgev1.Envelope{
		Id:           nextID(),
		FromDeviceId: "relay",
		ToDeviceId:   cc.deviceID,
		UnixMs:       time.Now().UnixMilli(),
		Payload: &pocketbridgev1.Envelope_Error{
			Error: &pocketbridgev1.Error{
				Code:    code,
				Message: message + refSuffix(refID),
			},
		},
	})
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

func trimBearer(v string) string {
	const prefix = "Bearer "
	if len(v) >= len(prefix) && v[:len(prefix)] == prefix {
		return v[len(prefix):]
	}
	return v
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
