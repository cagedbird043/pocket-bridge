package push

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	firebase "firebase.google.com/go/v4"
	"firebase.google.com/go/v4/messaging"
	pocketbridgev1 "github.com/cagedbird043/pocket-bridge/gen/go/pocketbridge/v1"
	"github.com/cagedbird043/pocket-bridge/internal/config"
	"google.golang.org/api/option"
)

const (
	ProviderFCM = "fcm"

	pushDataKind         = "pb_kind"
	pushDataFromDeviceID = "pb_from_device_id"
	pushDataTitle        = "pb_title"
	pushDataBody         = "pb_body"
)

type Sender interface {
	Send(ctx context.Context, target TargetState, msg Message) error
}

type TargetState struct {
	Provider    string    `json:"provider"`
	Token       string    `json:"token"`
	Platform    string    `json:"platform,omitempty"`
	PackageName string    `json:"package_name,omitempty"`
	UpdatedAt   time.Time `json:"updated_at"`
}

type Registry struct {
	path    string
	mu      sync.RWMutex
	entries map[string]TargetState
}

type Message struct {
	Kind         string
	FromDeviceID string
	Title        string
	Body         string
}

type fcmSender struct {
	client *messaging.Client
}

func NewRegistry(path string) (*Registry, error) {
	path = expandPath(path)
	r := &Registry{
		path:    path,
		entries: make(map[string]TargetState),
	}
	if path == "" {
		return r, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return r, nil
		}
		return nil, fmt.Errorf("read push token store: %w", err)
	}
	if len(data) == 0 {
		return r, nil
	}
	if err := json.Unmarshal(data, &r.entries); err != nil {
		return nil, fmt.Errorf("decode push token store: %w", err)
	}
	return r, nil
}

func (r *Registry) Lookup(deviceID string) (TargetState, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	entry, ok := r.entries[deviceID]
	return entry, ok
}

func (r *Registry) Upsert(deviceID string, update *pocketbridgev1.PushTokenUpdate) error {
	if update == nil {
		return fmt.Errorf("push token update is nil")
	}
	if update.Provider == "" {
		return fmt.Errorf("push token update missing provider")
	}
	if update.Token == "" {
		return fmt.Errorf("push token update missing token")
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	r.entries[deviceID] = TargetState{
		Provider:    update.Provider,
		Token:       update.Token,
		Platform:    update.Platform,
		PackageName: update.PackageName,
		UpdatedAt:   time.Now().UTC(),
	}
	return r.saveLocked()
}

func (r *Registry) Delete(deviceID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.entries, deviceID)
	return r.saveLocked()
}

func (r *Registry) saveLocked() error {
	if r.path == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(r.path), 0o755); err != nil {
		return fmt.Errorf("create push token dir: %w", err)
	}
	data, err := json.MarshalIndent(r.entries, "", "  ")
	if err != nil {
		return fmt.Errorf("encode push token store: %w", err)
	}
	tmpPath := r.path + ".tmp"
	if err := os.WriteFile(tmpPath, data, 0o600); err != nil {
		return fmt.Errorf("write push token temp file: %w", err)
	}
	if err := os.Rename(tmpPath, r.path); err != nil {
		return fmt.Errorf("replace push token store: %w", err)
	}
	return nil
}

func NewFCMSender(cfg *config.FCMConfig) (Sender, error) {
	ctx := context.Background()
	opts := []option.ClientOption{}
	if cfg.CredentialsFile != "" {
		opts = append(opts, option.WithCredentialsFile(expandPath(cfg.CredentialsFile)))
	}
	app, err := firebase.NewApp(ctx, &firebase.Config{
		ProjectID: cfg.ProjectID,
	}, opts...)
	if err != nil {
		return nil, fmt.Errorf("init firebase app: %w", err)
	}
	client, err := app.Messaging(ctx)
	if err != nil {
		return nil, fmt.Errorf("init firebase messaging client: %w", err)
	}
	return &fcmSender{client: client}, nil
}

func (s *fcmSender) Send(ctx context.Context, target TargetState, msg Message) error {
	if target.Provider != ProviderFCM {
		return fmt.Errorf("unsupported push provider: %s", target.Provider)
	}
	if target.Token == "" {
		return fmt.Errorf("missing push token")
	}

	_, err := s.client.Send(ctx, &messaging.Message{
		Token: target.Token,
		Data: map[string]string{
			pushDataKind:         msg.Kind,
			pushDataFromDeviceID: msg.FromDeviceID,
			pushDataTitle:        msg.Title,
			pushDataBody:         msg.Body,
		},
		Android: &messaging.AndroidConfig{
			Priority: "high",
		},
	})
	if err != nil {
		return fmt.Errorf("send FCM message: %w", err)
	}
	return nil
}

func MessageFromEnvelope(env *pocketbridgev1.Envelope) (Message, bool) {
	switch payload := env.Payload.(type) {
	case *pocketbridgev1.Envelope_NotifyPush:
		return Message{
			Kind:         "notify",
			FromDeviceID: env.FromDeviceId,
			Title:        payload.NotifyPush.Title,
			Body:         payload.NotifyPush.Body,
		}, true
	case *pocketbridgev1.Envelope_TaskStatus:
		return Message{
			Kind:         "task",
			FromDeviceID: env.FromDeviceId,
			Title:        fmt.Sprintf("Codex %s: %s", payload.TaskStatus.Kind, payload.TaskStatus.Title),
			Body:         payload.TaskStatus.Summary,
		}, true
	default:
		return Message{}, false
	}
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
