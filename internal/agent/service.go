package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	pocketbridgev1 "github.com/cagedbird043/pocket-bridge/gen/go/pocketbridge/v1"
	"github.com/cagedbird043/pocket-bridge/internal/config"
	"github.com/cagedbird043/pocket-bridge/internal/pbwire"
	"github.com/gorilla/websocket"
)

type Service struct {
	cfg *config.AgentConfig

	connMu    sync.RWMutex
	conn      *websocket.Conn
	writeMu   sync.Mutex
	connected bool
	lastError string
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

func New(cfg *config.AgentConfig) *Service {
	return &Service{cfg: cfg}
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
	wsURL, err := url.Parse(s.cfg.RelayURL)
	if err != nil {
		return fmt.Errorf("parse relay url: %w", err)
	}
	query := wsURL.Query()
	query.Set("device_id", s.cfg.DeviceID)
	wsURL.RawQuery = query.Encode()

	header := http.Header{}
	header.Set("Authorization", "Bearer "+s.cfg.Token)

	conn, _, err := websocket.DefaultDialer.DialContext(ctx, wsURL.String(), header)
	if err != nil {
		return fmt.Errorf("dial relay: %w", err)
	}
	defer conn.Close()

	s.setConn(conn)
	defer s.clearConn(conn)

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
	if err := s.sendEnvelope(hello); err != nil {
		return err
	}
	s.setState(true, "")
	log.Printf("agent connected to relay as %s", s.cfg.DeviceID)

	for {
		msgType, payload, err := conn.ReadMessage()
		if err != nil {
			return err
		}
		if msgType != websocket.BinaryMessage {
			continue
		}
		env, err := pbwire.UnmarshalEnvelope(payload)
		if err != nil {
			log.Printf("agent bad envelope: %v", err)
			continue
		}
		s.handleEnvelope(env)
	}
}

func (s *Service) handleEnvelope(env *pocketbridgev1.Envelope) {
	switch msg := env.Payload.(type) {
	case *pocketbridgev1.Envelope_Ack:
		log.Printf("agent ack id=%s", msg.Ack.AckId)
	case *pocketbridgev1.Envelope_Error:
		log.Printf("agent error code=%s msg=%s", msg.Error.Code, msg.Error.Message)
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
	if err := s.sendEnvelope(env); err != nil {
		http.Error(w, err.Error(), http.StatusServiceUnavailable)
		return
	}
	w.WriteHeader(http.StatusAccepted)
	_, _ = w.Write([]byte("queued\n"))
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
}

func (s *Service) setState(connected bool, lastErr string) {
	s.connMu.Lock()
	defer s.connMu.Unlock()
	s.connected = connected
	s.lastError = lastErr
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
