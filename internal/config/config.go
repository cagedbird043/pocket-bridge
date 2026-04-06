package config

import (
	"encoding/json"
	"fmt"
	"os"
)

type RelayConfig struct {
	ListenAddr string                 `json:"listen_addr"`
	Devices    map[string]RelayDevice `json:"devices"`
	FCM        *RelayFCMConfig        `json:"fcm,omitempty"`
}

type RelayDevice struct {
	PublicKeyBase64 string `json:"public_key_base64"`
}

type RelayFCMConfig struct {
	ProjectID       string `json:"project_id"`
	CredentialsFile string `json:"credentials_file,omitempty"`
	TokenStorePath  string `json:"token_store_path"`
}

type AgentConfig struct {
	DeviceID              string            `json:"device_id"`
	PrivateKeyBase64      string            `json:"private_key_base64"`
	RelayURL              string            `json:"relay_url"`
	UnixSocket            string            `json:"unix_socket"`
	Targets               map[string]string `json:"targets"`
	IncomingNotifyCommand []string          `json:"incoming_notify_command"`
	LogIncoming           bool              `json:"log_incoming"`
}

func LoadRelayConfig(path string) (*RelayConfig, error) {
	var cfg RelayConfig
	if err := loadJSON(path, &cfg); err != nil {
		return nil, err
	}
	if cfg.ListenAddr == "" {
		return nil, fmt.Errorf("relay config missing listen_addr")
	}
	if len(cfg.Devices) == 0 {
		return nil, fmt.Errorf("relay config missing devices")
	}
	for deviceID, device := range cfg.Devices {
		if device.PublicKeyBase64 == "" {
			return nil, fmt.Errorf("relay config missing public_key_base64 for device %s", deviceID)
		}
	}
	if cfg.FCM != nil {
		if cfg.FCM.ProjectID == "" {
			return nil, fmt.Errorf("relay config missing fcm.project_id")
		}
		if cfg.FCM.TokenStorePath == "" {
			return nil, fmt.Errorf("relay config missing fcm.token_store_path")
		}
	}
	return &cfg, nil
}

func LoadAgentConfig(path string) (*AgentConfig, error) {
	var cfg AgentConfig
	if err := loadJSON(path, &cfg); err != nil {
		return nil, err
	}
	if cfg.DeviceID == "" {
		return nil, fmt.Errorf("agent config missing device_id")
	}
	if cfg.PrivateKeyBase64 == "" {
		return nil, fmt.Errorf("agent config missing private_key_base64")
	}
	if cfg.RelayURL == "" {
		return nil, fmt.Errorf("agent config missing relay_url")
	}
	if cfg.UnixSocket == "" {
		return nil, fmt.Errorf("agent config missing unix_socket")
	}
	if cfg.Targets == nil {
		cfg.Targets = map[string]string{}
	}
	return &cfg, nil
}

func loadJSON(path string, dst any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read %s: %w", path, err)
	}
	if err := json.Unmarshal(data, dst); err != nil {
		return fmt.Errorf("decode %s: %w", path, err)
	}
	return nil
}
