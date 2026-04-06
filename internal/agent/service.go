package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	pocketbridgev1 "github.com/cagedbird043/pocket-bridge/gen/go/pocketbridge/v1"
	"github.com/cagedbird043/pocket-bridge/internal/auth"
	"github.com/cagedbird043/pocket-bridge/internal/config"
	"github.com/cagedbird043/pocket-bridge/internal/pbwire"
	"github.com/cagedbird043/pocket-bridge/internal/push"
	"github.com/gorilla/websocket"
)

type Service struct {
	cfg *config.AgentConfig

	connMu    sync.RWMutex
	conn      *websocket.Conn
	writeMu   sync.Mutex
	connected bool
	lastError string

	clipboardMu         sync.Mutex
	pendingClipboardMap map[string]chan clipboardValueResult
	requestMu           sync.Mutex
	pendingRequestMap   map[string]chan requestResult
	push                push.Sender
	pushes              *push.Registry
}

type Status struct {
	DeviceID  string `json:"device_id"`
	Connected bool   `json:"connected"`
	RelayURL  string `json:"relay_url"`
	LastError string `json:"last_error,omitempty"`
}

type NotifyRequest struct {
	Target   string `json:"target"`
	Title    string `json:"title"`
	Body     string `json:"body"`
	Topic    string `json:"topic"`
	Priority string `json:"priority"`
}

type TaskRequest struct {
	Target  string `json:"target"`
	Kind    string `json:"kind"`
	Title   string `json:"title"`
	Summary string `json:"summary"`
}

type ClipboardPushRequest struct {
	Target             string `json:"target"`
	MimeType           string `json:"mime_type"`
	Text               string `json:"text"`
	ReadLocalClipboard bool   `json:"read_local_clipboard"`
}

type ClipboardPullRequest struct {
	Target            string `json:"target"`
	PreferredMimeType string `json:"preferred_mime_type"`
	TimeoutMs         int    `json:"timeout_ms"`
}

type ClipboardValueResponse struct {
	FromDeviceID string `json:"from_device_id"`
	MimeType     string `json:"mime_type"`
	Text         string `json:"text"`
	LocalApplied bool   `json:"local_applied,omitempty"`
	LocalError   string `json:"local_error,omitempty"`
}

type clipboardValueResult struct {
	response ClipboardValueResponse
}

type requestResult struct {
	ack   *pocketbridgev1.Ack
	err   *pocketbridgev1.Error
	refID string
}

func New(cfg *config.AgentConfig) (*Service, error) {
	svc := &Service{
		cfg:                 cfg,
		pendingClipboardMap: make(map[string]chan clipboardValueResult),
		pendingRequestMap:   make(map[string]chan requestResult),
	}
	if cfg.FCM != nil {
		pushes, err := push.NewRegistry(cfg.FCM.TokenStorePath)
		if err != nil {
			return nil, err
		}
		sender, err := push.NewFCMSender(cfg.FCM)
		if err != nil {
			return nil, err
		}
		svc.pushes = pushes
		svc.push = sender
	}
	return svc, nil
}

func (s *Service) Run(ctx context.Context) error {
	errCh := make(chan error, 2)

	go func() {
		errCh <- s.runUnixSocketServer(ctx)
	}()
	go func() {
		errCh <- s.runRelayLoop(ctx)
	}()

	select {
	case <-ctx.Done():
		return nil
	case err := <-errCh:
		if err == nil || err == http.ErrServerClosed {
			return nil
		}
		return err
	}
}

func (s *Service) runUnixSocketServer(ctx context.Context) error {
	socketPath := expandPath(s.cfg.UnixSocket)
	if err := os.MkdirAll(filepath.Dir(socketPath), 0o755); err != nil {
		return fmt.Errorf("create socket dir: %w", err)
	}
	_ = os.Remove(socketPath)

	ln, err := net.Listen("unix", socketPath)
	if err != nil {
		return fmt.Errorf("listen unix socket: %w", err)
	}
	defer func() {
		_ = ln.Close()
		_ = os.Remove(socketPath)
	}()
	_ = os.Chmod(socketPath, 0o600)

	mux := http.NewServeMux()
	mux.HandleFunc("/v1/status", s.handleStatus)
	mux.HandleFunc("/v1/notify", s.handleNotify)
	mux.HandleFunc("/v1/clip/push", s.handleClipboardPush)
	mux.HandleFunc("/v1/clip/pull", s.handleClipboardPull)
	mux.HandleFunc("/v1/task", s.handleTask)

	srv := &http.Server{Handler: mux}
	go func() {
		<-ctx.Done()
		_ = srv.Shutdown(context.Background())
	}()

	log.Printf("agent unix socket ready: %s", socketPath)
	return srv.Serve(ln)
}

func (s *Service) runRelayLoop(ctx context.Context) error {
	for {
		if ctx.Err() != nil {
			return nil
		}
		if err := s.connectAndServe(ctx); err != nil {
			s.setState(false, err.Error())
			log.Printf("agent relay loop: %v", err)
		}
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(2 * time.Second):
		}
	}
}

func (s *Service) connectAndServe(ctx context.Context) error {
	conn, _, err := websocket.DefaultDialer.DialContext(ctx, s.cfg.RelayURL, nil)
	if err != nil {
		return fmt.Errorf("dial relay: %w", err)
	}
	defer conn.Close()

	if err := s.authenticateRelay(conn); err != nil {
		return err
	}
	s.setConn(conn)
	defer s.clearConn(conn)
	log.Printf("agent connected to relay as %s", s.cfg.DeviceID)

	for {
		env, err := readEnvelope(conn)
		if err != nil {
			return err
		}
		s.handleEnvelope(env)
	}
}

func (s *Service) authenticateRelay(conn *websocket.Conn) error {
	privateKey, err := auth.ParsePrivateKeyBase64(s.cfg.PrivateKeyBase64)
	if err != nil {
		return fmt.Errorf("parse device private key: %w", err)
	}

	hello := &pocketbridgev1.Envelope{
		Id:           nextID(),
		FromDeviceId: s.cfg.DeviceID,
		UnixMs:       time.Now().UnixMilli(),
		Payload: &pocketbridgev1.Envelope_DeviceHello{
			DeviceHello: &pocketbridgev1.DeviceHello{
				DeviceId:        s.cfg.DeviceID,
				DeviceType:      "agent",
				ProtocolVersion: "v1",
			},
		},
	}
	if err := writeEnvelope(conn, hello); err != nil {
		return fmt.Errorf("send device hello: %w", err)
	}

	challengeEnv, err := readEnvelope(conn)
	if err != nil {
		return fmt.Errorf("read auth challenge: %w", err)
	}
	challenge, err := expectAuthChallenge(challengeEnv)
	if err != nil {
		return err
	}
	if challenge.Algorithm != auth.AlgorithmEd25519 {
		return fmt.Errorf("unsupported auth algorithm: %s", challenge.Algorithm)
	}

	response := &pocketbridgev1.Envelope{
		Id:           nextID(),
		FromDeviceId: s.cfg.DeviceID,
		ToDeviceId:   "relay",
		UnixMs:       time.Now().UnixMilli(),
		Payload: &pocketbridgev1.Envelope_AuthResponse{
			AuthResponse: &pocketbridgev1.AuthResponse{
				Signature: auth.SignChallenge(privateKey, challenge.ChallengeData),
			},
		},
	}
	if err := writeEnvelope(conn, response); err != nil {
		return fmt.Errorf("send auth response: %w", err)
	}

	ackEnv, err := readEnvelope(conn)
	if err != nil {
		return fmt.Errorf("read auth ack: %w", err)
	}
	if err := expectHelloAck(ackEnv, s.cfg.DeviceID); err != nil {
		return err
	}
	return nil
}

func (s *Service) handleEnvelope(env *pocketbridgev1.Envelope) {
	switch msg := env.Payload.(type) {
	case *pocketbridgev1.Envelope_Ack:
		if !s.resolveRequest(msg.Ack.AckId, requestResult{ack: msg.Ack, refID: msg.Ack.AckId}) {
			log.Printf("agent ack id=%s", msg.Ack.AckId)
		}
	case *pocketbridgev1.Envelope_Error:
		if refID, ok := parseRefID(msg.Error.Message); ok && s.resolveRequest(refID, requestResult{err: msg.Error, refID: refID}) {
			return
		}
		log.Printf("agent error code=%s msg=%s", msg.Error.Code, msg.Error.Message)
	case *pocketbridgev1.Envelope_AuthChallenge, *pocketbridgev1.Envelope_AuthResponse:
		log.Printf("agent ignored unexpected auth payload after handshake")
	case *pocketbridgev1.Envelope_PushTokenUpdate:
		if s.pushes == nil {
			log.Printf("agent ignored push token from=%s: local FCM sender not configured", env.FromDeviceId)
			return
		}
		if err := s.pushes.Upsert(env.FromDeviceId, msg.PushTokenUpdate); err != nil {
			log.Printf("push token update from=%s failed: %v", env.FromDeviceId, err)
			return
		}
		log.Printf("agent stored push token: device=%s provider=%s platform=%s", env.FromDeviceId, msg.PushTokenUpdate.Provider, msg.PushTokenUpdate.Platform)
	case *pocketbridgev1.Envelope_NotifyPush:
		if s.cfg.LogIncoming {
			log.Printf("notify from=%s topic=%s title=%q body=%q", env.FromDeviceId, msg.NotifyPush.Topic, msg.NotifyPush.Title, msg.NotifyPush.Body)
		}
		if err := s.runNotifyCommand(msg.NotifyPush.Title, msg.NotifyPush.Body); err != nil {
			log.Printf("notify command failed: %v", err)
		}
	case *pocketbridgev1.Envelope_TaskStatus:
		if s.cfg.LogIncoming {
			log.Printf("task status from=%s kind=%s title=%q summary=%q", env.FromDeviceId, msg.TaskStatus.Kind, msg.TaskStatus.Title, msg.TaskStatus.Summary)
		}
		if err := s.runNotifyCommand(msg.TaskStatus.Title, msg.TaskStatus.Summary); err != nil {
			log.Printf("task notify command failed: %v", err)
		}
	case *pocketbridgev1.Envelope_ClipboardPush:
		if s.cfg.LogIncoming {
			log.Printf("clipboard push from=%s mime=%s text=%q", env.FromDeviceId, msg.ClipboardPush.MimeType, previewText(msg.ClipboardPush.Text))
		}
		s.applyClipboardTextAsync("clipboard push", msg.ClipboardPush.Text)
	case *pocketbridgev1.Envelope_ClipboardPull:
		if s.cfg.LogIncoming {
			log.Printf("clipboard pull from=%s preferred=%q", env.FromDeviceId, msg.ClipboardPull.PreferredMimeType)
		}
		text, err := s.readClipboardText()
		if err != nil {
			log.Printf("clipboard read failed: %v", err)
			if sendErr := s.sendAgentError(env.FromDeviceId, "clipboard_read_failed", err.Error()); sendErr != nil {
				log.Printf("clipboard read error reply failed: %v", sendErr)
			}
			return
		}
		value := &pocketbridgev1.Envelope{
			Id:           nextID(),
			FromDeviceId: s.cfg.DeviceID,
			ToDeviceId:   env.FromDeviceId,
			UnixMs:       time.Now().UnixMilli(),
			Payload: &pocketbridgev1.Envelope_ClipboardValue{
				ClipboardValue: &pocketbridgev1.ClipboardValue{
					MimeType: "text/plain;charset=utf-8",
					Text:     text,
				},
			},
		}
		if err := s.sendEnvelope(value); err != nil {
			log.Printf("clipboard value reply failed: %v", err)
		}
	case *pocketbridgev1.Envelope_ClipboardValue:
		if s.cfg.LogIncoming {
			log.Printf("clipboard value from=%s mime=%s text=%q", env.FromDeviceId, msg.ClipboardValue.MimeType, previewText(msg.ClipboardValue.Text))
		}
		result := ClipboardValueResponse{
			FromDeviceID: env.FromDeviceId,
			MimeType:     msg.ClipboardValue.MimeType,
			Text:         msg.ClipboardValue.Text,
		}
		s.resolveClipboardWaiter(env.FromDeviceId, result)
		s.applyClipboardTextAsync("clipboard value", msg.ClipboardValue.Text)
	default:
		if s.cfg.LogIncoming {
			log.Printf("agent ignored payload type %T", env.Payload)
		}
	}
}

func (s *Service) handleStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(s.Status())
}

func (s *Service) handleNotify(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req NotifyRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad json", http.StatusBadRequest)
		return
	}
	if req.Target == "" || req.Title == "" {
		http.Error(w, "target and title are required", http.StatusBadRequest)
		return
	}
	target := s.resolveTarget(req.Target)
	env := &pocketbridgev1.Envelope{
		Id:           nextID(),
		FromDeviceId: s.cfg.DeviceID,
		ToDeviceId:   target,
		UnixMs:       time.Now().UnixMilli(),
		Payload: &pocketbridgev1.Envelope_NotifyPush{
			NotifyPush: &pocketbridgev1.NotifyPush{
				Title:    req.Title,
				Body:     req.Body,
				Topic:    req.Topic,
				Priority: req.Priority,
			},
		},
	}
	if err := s.sendNotifyEnvelope(env); err != nil {
		http.Error(w, err.Error(), http.StatusServiceUnavailable)
		return
	}
	w.WriteHeader(http.StatusAccepted)
	_, _ = w.Write([]byte("queued\n"))
}

func (s *Service) handleClipboardPush(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req ClipboardPushRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad json", http.StatusBadRequest)
		return
	}
	if req.Target == "" {
		http.Error(w, "target is required", http.StatusBadRequest)
		return
	}
	text := req.Text
	if req.ReadLocalClipboard {
		var err error
		text, err = s.readClipboardText()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	}
	mimeType := req.MimeType
	if mimeType == "" {
		mimeType = "text/plain;charset=utf-8"
	}
	env := &pocketbridgev1.Envelope{
		Id:           nextID(),
		FromDeviceId: s.cfg.DeviceID,
		ToDeviceId:   s.resolveTarget(req.Target),
		UnixMs:       time.Now().UnixMilli(),
		Payload: &pocketbridgev1.Envelope_ClipboardPush{
			ClipboardPush: &pocketbridgev1.ClipboardPush{
				MimeType: mimeType,
				Text:     text,
			},
		},
	}
	if err := s.sendNotifyEnvelope(env); err != nil {
		http.Error(w, err.Error(), http.StatusServiceUnavailable)
		return
	}
	w.WriteHeader(http.StatusAccepted)
	_, _ = w.Write([]byte("queued\n"))
}

func (s *Service) handleClipboardPull(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req ClipboardPullRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad json", http.StatusBadRequest)
		return
	}
	if req.Target == "" {
		http.Error(w, "target is required", http.StatusBadRequest)
		return
	}
	target := s.resolveTarget(req.Target)
	waitCh, err := s.registerClipboardWaiter(target)
	if err != nil {
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}
	defer s.unregisterClipboardWaiter(target, waitCh)

	env := &pocketbridgev1.Envelope{
		Id:           nextID(),
		FromDeviceId: s.cfg.DeviceID,
		ToDeviceId:   target,
		UnixMs:       time.Now().UnixMilli(),
		Payload: &pocketbridgev1.Envelope_ClipboardPull{
			ClipboardPull: &pocketbridgev1.ClipboardPull{
				PreferredMimeType: req.PreferredMimeType,
			},
		},
	}
	if err := s.sendNotifyEnvelope(env); err != nil {
		http.Error(w, err.Error(), http.StatusServiceUnavailable)
		return
	}

	timeout := 15 * time.Second
	if req.TimeoutMs > 0 {
		timeout = time.Duration(req.TimeoutMs) * time.Millisecond
	}

	select {
	case result := <-waitCh:
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(result.response)
	case <-time.After(timeout):
		http.Error(w, "timed out waiting for clipboard value", http.StatusGatewayTimeout)
	}
}

func (s *Service) handleTask(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req TaskRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad json", http.StatusBadRequest)
		return
	}
	if req.Target == "" || req.Kind == "" || req.Title == "" {
		http.Error(w, "target, kind, and title are required", http.StatusBadRequest)
		return
	}
	target := s.resolveTarget(req.Target)
	env := &pocketbridgev1.Envelope{
		Id:           nextID(),
		FromDeviceId: s.cfg.DeviceID,
		ToDeviceId:   target,
		UnixMs:       time.Now().UnixMilli(),
		Payload: &pocketbridgev1.Envelope_TaskStatus{
			TaskStatus: &pocketbridgev1.TaskStatus{
				Kind:    req.Kind,
				Title:   req.Title,
				Summary: req.Summary,
			},
		},
	}
	if err := s.sendEnvelope(env); err != nil {
		http.Error(w, err.Error(), http.StatusServiceUnavailable)
		return
	}
	w.WriteHeader(http.StatusAccepted)
	_, _ = w.Write([]byte("queued\n"))
}

func (s *Service) Status() Status {
	s.connMu.RLock()
	defer s.connMu.RUnlock()
	return Status{
		DeviceID:  s.cfg.DeviceID,
		Connected: s.connected,
		RelayURL:  s.cfg.RelayURL,
		LastError: s.lastError,
	}
}

func (s *Service) resolveTarget(name string) string {
	if target, ok := s.cfg.Targets[name]; ok {
		return target
	}
	return name
}

func (s *Service) sendEnvelope(env *pocketbridgev1.Envelope) error {
	s.connMu.RLock()
	conn := s.conn
	connected := s.connected
	s.connMu.RUnlock()
	if !connected || conn == nil {
		return fmt.Errorf("relay is not connected")
	}
	data, err := pbwire.MarshalEnvelope(env)
	if err != nil {
		return err
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	return conn.WriteMessage(websocket.BinaryMessage, data)
}

func (s *Service) sendNotifyEnvelope(env *pocketbridgev1.Envelope) error {
	result, err := s.sendEnvelopeAwaitResult(env, 5*time.Second)
	if err != nil {
		return err
	}
	if result.err == nil {
		return nil
	}
	if result.err.Code != "target_offline" {
		return fmt.Errorf("relay error: %s %s", result.err.Code, trimRefSuffix(result.err.Message))
	}
	return s.sendOfflinePush(env.ToDeviceId, env)
}

func (s *Service) sendEnvelopeAwaitResult(env *pocketbridgev1.Envelope, timeout time.Duration) (requestResult, error) {
	waitCh, err := s.registerRequestWaiter(env.Id)
	if err != nil {
		return requestResult{}, err
	}
	defer s.unregisterRequestWaiter(env.Id, waitCh)

	if err := s.sendEnvelope(env); err != nil {
		return requestResult{}, err
	}

	select {
	case result := <-waitCh:
		return result, nil
	case <-time.After(timeout):
		return requestResult{}, fmt.Errorf("timed out waiting for relay response")
	}
}

func (s *Service) sendOfflinePush(targetDeviceID string, env *pocketbridgev1.Envelope) error {
	if s.push == nil || s.pushes == nil {
		return fmt.Errorf("target offline and local FCM sender is not configured")
	}
	target, ok := s.pushes.Lookup(targetDeviceID)
	if !ok {
		return fmt.Errorf("target offline and no cached push token for %s", targetDeviceID)
	}
	msg, ok := push.MessageFromEnvelope(env)
	if !ok {
		return fmt.Errorf("target offline and payload does not support FCM fallback")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := s.push.Send(ctx, target, msg); err != nil {
		return err
	}
	log.Printf("agent delivered offline push: target=%s provider=%s kind=%s", targetDeviceID, target.Provider, msg.Kind)
	return nil
}

func (s *Service) sendAgentError(target, code, message string) error {
	return s.sendEnvelope(&pocketbridgev1.Envelope{
		Id:           nextID(),
		FromDeviceId: s.cfg.DeviceID,
		ToDeviceId:   target,
		UnixMs:       time.Now().UnixMilli(),
		Payload: &pocketbridgev1.Envelope_Error{
			Error: &pocketbridgev1.Error{
				Code:    code,
				Message: message,
			},
		},
	})
}

func (s *Service) setConn(conn *websocket.Conn) {
	s.connMu.Lock()
	defer s.connMu.Unlock()
	s.conn = conn
	s.connected = true
	s.lastError = ""
}

func (s *Service) clearConn(conn *websocket.Conn) {
	s.connMu.Lock()
	defer s.connMu.Unlock()
	if s.conn == conn {
		s.conn = nil
		s.connected = false
	}
	s.failAllPendingRequests("relay disconnected")
}

func (s *Service) setState(connected bool, lastErr string) {
	s.connMu.Lock()
	defer s.connMu.Unlock()
	s.connected = connected
	s.lastError = lastErr
	if !connected {
		s.failAllPendingRequests(lastErr)
	}
}

func (s *Service) runNotifyCommand(title, body string) error {
	if len(s.cfg.IncomingNotifyCommand) == 0 {
		return nil
	}
	args := append([]string{}, s.cfg.IncomingNotifyCommand[1:]...)
	args = append(args, title)
	if body != "" {
		args = append(args, body)
	}
	cmd := exec.Command(s.cfg.IncomingNotifyCommand[0], args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return nil
}

func (s *Service) readClipboardText() (string, error) {
	cmd := exec.Command("wl-paste", "-n")
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("wl-paste failed: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return stdout.String(), nil
}

func (s *Service) writeClipboardText(text string) error {
	cmd := exec.Command("wl-copy", "-t", "text/plain;charset=utf-8")
	cmd.Stdin = strings.NewReader(text)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("wl-copy failed: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return nil
}

func (s *Service) applyClipboardTextAsync(source, text string) {
	go func() {
		if err := s.writeClipboardText(text); err != nil {
			log.Printf("%s apply failed: %v", source, err)
		}
	}()
}

func (s *Service) registerClipboardWaiter(target string) (chan clipboardValueResult, error) {
	s.clipboardMu.Lock()
	defer s.clipboardMu.Unlock()
	if _, exists := s.pendingClipboardMap[target]; exists {
		return nil, fmt.Errorf("clipboard pull already pending for %s", target)
	}
	ch := make(chan clipboardValueResult, 1)
	s.pendingClipboardMap[target] = ch
	return ch, nil
}

func (s *Service) unregisterClipboardWaiter(target string, ch chan clipboardValueResult) {
	s.clipboardMu.Lock()
	defer s.clipboardMu.Unlock()
	if current, exists := s.pendingClipboardMap[target]; exists && current == ch {
		delete(s.pendingClipboardMap, target)
	}
}

func (s *Service) resolveClipboardWaiter(target string, response ClipboardValueResponse) {
	s.clipboardMu.Lock()
	ch, exists := s.pendingClipboardMap[target]
	if exists {
		delete(s.pendingClipboardMap, target)
	}
	s.clipboardMu.Unlock()
	if !exists {
		return
	}
	ch <- clipboardValueResult{response: response}
	close(ch)
}

func previewText(text string) string {
	normalized := strings.ReplaceAll(text, "\n", "\\n")
	if len(normalized) <= 80 {
		return normalized
	}
	return normalized[:80] + "..."
}

func expandPath(path string) string {
	if strings.HasPrefix(path, "~/") {
		home, err := os.UserHomeDir()
		if err == nil {
			return filepath.Join(home, path[2:])
		}
	}
	return path
}

func nextID() string {
	return time.Now().UTC().Format("20060102T150405.000000000")
}

func (s *Service) registerRequestWaiter(id string) (chan requestResult, error) {
	s.requestMu.Lock()
	defer s.requestMu.Unlock()
	if _, exists := s.pendingRequestMap[id]; exists {
		return nil, fmt.Errorf("request already pending for %s", id)
	}
	ch := make(chan requestResult, 1)
	s.pendingRequestMap[id] = ch
	return ch, nil
}

func (s *Service) unregisterRequestWaiter(id string, ch chan requestResult) {
	s.requestMu.Lock()
	defer s.requestMu.Unlock()
	if current, exists := s.pendingRequestMap[id]; exists && current == ch {
		delete(s.pendingRequestMap, id)
	}
}

func (s *Service) resolveRequest(id string, result requestResult) bool {
	s.requestMu.Lock()
	ch, exists := s.pendingRequestMap[id]
	if exists {
		delete(s.pendingRequestMap, id)
	}
	s.requestMu.Unlock()
	if !exists {
		return false
	}
	ch <- result
	close(ch)
	return true
}

func (s *Service) failAllPendingRequests(message string) {
	s.requestMu.Lock()
	defer s.requestMu.Unlock()
	for id, ch := range s.pendingRequestMap {
		ch <- requestResult{
			err: &pocketbridgev1.Error{
				Code:    "relay_disconnected",
				Message: message,
			},
			refID: id,
		}
		close(ch)
		delete(s.pendingRequestMap, id)
	}
}

func parseRefID(message string) (string, bool) {
	const prefix = " (ref="
	start := strings.LastIndex(message, prefix)
	if start < 0 || !strings.HasSuffix(message, ")") {
		return "", false
	}
	return message[start+len(prefix) : len(message)-1], true
}

func trimRefSuffix(message string) string {
	if _, ok := parseRefID(message); !ok {
		return message
	}
	start := strings.LastIndex(message, " (ref=")
	return message[:start]
}

func readEnvelope(conn *websocket.Conn) (*pocketbridgev1.Envelope, error) {
	msgType, payload, err := conn.ReadMessage()
	if err != nil {
		return nil, err
	}
	if msgType != websocket.BinaryMessage {
		return nil, fmt.Errorf("relay expects binary protobuf messages")
	}
	env, err := pbwire.UnmarshalEnvelope(payload)
	if err != nil {
		return nil, fmt.Errorf("bad envelope: %w", err)
	}
	return env, nil
}

func writeEnvelope(conn *websocket.Conn, env *pocketbridgev1.Envelope) error {
	data, err := pbwire.MarshalEnvelope(env)
	if err != nil {
		return err
	}
	return conn.WriteMessage(websocket.BinaryMessage, data)
}

func expectAuthChallenge(env *pocketbridgev1.Envelope) (*pocketbridgev1.AuthChallenge, error) {
	switch payload := env.Payload.(type) {
	case *pocketbridgev1.Envelope_AuthChallenge:
		return payload.AuthChallenge, nil
	case *pocketbridgev1.Envelope_Error:
		return nil, fmt.Errorf("relay auth error: %s %s", payload.Error.Code, payload.Error.Message)
	default:
		return nil, fmt.Errorf("expected auth challenge, got %T", env.Payload)
	}
}

func expectHelloAck(env *pocketbridgev1.Envelope, deviceID string) error {
	switch payload := env.Payload.(type) {
	case *pocketbridgev1.Envelope_Ack:
		expected := helloAckID(deviceID)
		if payload.Ack.AckId != expected {
			return fmt.Errorf("unexpected auth ack: %s", payload.Ack.AckId)
		}
		return nil
	case *pocketbridgev1.Envelope_Error:
		return fmt.Errorf("relay auth error: %s %s", payload.Error.Code, payload.Error.Message)
	default:
		return fmt.Errorf("expected auth ack, got %T", env.Payload)
	}
}

func helloAckID(deviceID string) string {
	return "hello:" + deviceID
}
