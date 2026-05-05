package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/cagedbird043/pocket-bridge/internal/auth"
)

type statusResponse struct {
	DeviceID  string `json:"device_id"`
	Connected bool   `json:"connected"`
	RelayURL  string `json:"relay_url"`
	LastError string `json:"last_error,omitempty"`
}

type clipboardPullResponse struct {
	FromDeviceID string `json:"from_device_id"`
	MimeType     string `json:"mime_type"`
	Text         string `json:"text"`
	LocalApplied bool   `json:"local_applied"`
	LocalError   string `json:"local_error,omitempty"`
}

func main() {
	socketPath := flag.String("socket", defaultSocketPath(), "agent unix socket path")
	flag.Parse()

	if flag.NArg() == 0 {
		usage()
	}

	client := newUnixHTTPClient(*socketPath)
	cmd := flag.Arg(0)
	args := flag.Args()[1:]

	switch cmd {
	case "keygen":
		handleKeygen()
	case "completion":
		handleCompletion(args)
	case "status":
		handleStatus(client)
	case "notify":
		if len(args) < 3 {
			usage()
		}
		payload := map[string]string{
			"target": args[0],
			"title":  args[1],
			"body":   strings.Join(args[2:], " "),
		}
		postJSON(client, "/v1/notify", payload)
		fmt.Println("notify queued")
	case "clip":
		handleClip(client, args)
	case "task":
		if len(args) < 3 {
			usage()
		}
		payload := map[string]string{
			"target":  "phone",
			"kind":    args[0],
			"title":   args[1],
			"summary": strings.Join(args[2:], " "),
		}
		postJSON(client, "/v1/task", payload)
		fmt.Println("task queued")
	default:
		usage()
	}
}

func handleCompletion(args []string) {
	if len(args) != 1 || args[0] != "zsh" {
		usage()
	}
	fmt.Print(zshCompletionScript())
}

func handleKeygen() {
	publicKeyBase64, privateKeyBase64, err := auth.GenerateKeyPairBase64()
	if err != nil {
		exitErr(err)
	}
	out := map[string]string{
		"public_key_base64":  publicKeyBase64,
		"private_key_base64": privateKeyBase64,
	}
	data, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		exitErr(err)
	}
	fmt.Println(string(data))
}

func handleClip(client *http.Client, args []string) {
	if len(args) < 2 {
		usage()
	}

	switch args[0] {
	case "push":
		payload := map[string]any{
			"target":    args[1],
			"mime_type": "text/plain;charset=utf-8",
		}
		if len(args) == 2 {
			payload["read_local_clipboard"] = true
		} else {
			payload["text"] = strings.Join(args[2:], " ")
		}
		postJSON(client, "/v1/clip/push", payload)
		fmt.Println("clipboard push queued")
	case "pull":
		if len(args) != 2 {
			usage()
		}
		body := postJSON(client, "/v1/clip/pull", map[string]any{
			"target": args[1],
		})
		var out clipboardPullResponse
		if err := json.Unmarshal(body, &out); err != nil {
			exitErr(err)
		}
		_, _ = io.WriteString(os.Stdout, out.Text)
		if !strings.HasSuffix(out.Text, "\n") {
			fmt.Println()
		}
		if out.LocalError != "" {
			fmt.Fprintln(os.Stderr, "警告:", out.LocalError)
		}
	default:
		usage()
	}
}

func handleStatus(client *http.Client) {
	resp, err := client.Get("http://unix/v1/status")
	if err != nil {
		exitErr(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		exitErr(fmt.Errorf("status %s: %s", resp.Status, strings.TrimSpace(string(body))))
	}
	var out statusResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		exitErr(err)
	}
	fmt.Printf("device=%s connected=%t relay=%s", out.DeviceID, out.Connected, out.RelayURL)
	if out.LastError != "" {
		fmt.Printf(" last_error=%q", out.LastError)
	}
	fmt.Println()
}

func postJSON(client *http.Client, path string, payload any) []byte {
	data, err := json.Marshal(payload)
	if err != nil {
		exitErr(err)
	}
	resp, err := client.Post("http://unix"+path, "application/json", bytes.NewReader(data))
	if err != nil {
		exitErr(err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		exitErr(err)
	}
	if resp.StatusCode/100 != 2 {
		exitErr(fmt.Errorf("status %s: %s", resp.Status, strings.TrimSpace(string(body))))
	}
	return body
}

func newUnixHTTPClient(socket string) *http.Client {
	transport := &http.Transport{
		DialContext: func(_ context.Context, _, _ string) (net.Conn, error) {
			return net.Dial("unix", socket)
		},
	}
	return &http.Client{
		Transport: transport,
		Timeout:   20 * time.Second,
	}
}

func defaultSocketPath() string {
	stateDir := os.Getenv("XDG_STATE_HOME")
	if stateDir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "/tmp/pocket-bridge.sock"
		}
		stateDir = filepath.Join(home, ".local", "state")
	}
	return filepath.Join(stateDir, "pocket-bridge", "agent.sock")
}

func usage() {
	fmt.Fprintln(os.Stderr, "用法:")
	fmt.Fprintln(os.Stderr, "  pb [--socket PATH] status")
	fmt.Fprintln(os.Stderr, "  pb keygen")
	fmt.Fprintln(os.Stderr, "  pb completion zsh")
	fmt.Fprintln(os.Stderr, "  pb [--socket PATH] notify <target> <title> <body>")
	fmt.Fprintln(os.Stderr, "  pb [--socket PATH] clip push <target> [text]")
	fmt.Fprintln(os.Stderr, "  pb [--socket PATH] clip pull <target>")
	fmt.Fprintln(os.Stderr, "  pb [--socket PATH] task <started|blocked|done|failed> <title> <summary>")
	os.Exit(1)
}

func exitErr(err error) {
	fmt.Fprintln(os.Stderr, "错误:", err)
	os.Exit(1)
}

func zshCompletionScript() string {
	return `#compdef pb

_pb_target_candidates() {
  local config token_store
  local -a candidates
  local -a config_paths token_stores

  config_paths=(
    "/etc/pocket-bridge/agentd-${USER}.json"
    "${XDG_CONFIG_HOME:-${HOME}/.config}/pocket-bridge/agentd.json"
    /etc/pocket-bridge/agentd-*.json(N)
  )

  for config in ${config_paths}; do
    [[ -r "$config" ]] || continue
    candidates+=(${(f)"$(python3 - "$config" <<'PY'
import json, sys
path = sys.argv[1]
try:
    data = json.load(open(path, 'r', encoding='utf-8'))
except Exception:
    raise SystemExit(0)
out = []
targets = data.get('targets') or {}
for key, value in targets.items():
    if isinstance(key, str) and key.strip():
        out.append(key.strip())
    if isinstance(value, str) and value.strip():
        out.append(value.strip())
for item in out:
    print(item)
PY
)"})
    token_store="$(python3 - "$config" <<'PY'
import json, sys
path = sys.argv[1]
try:
    data = json.load(open(path, 'r', encoding='utf-8'))
except Exception:
    raise SystemExit(0)
fcm = data.get('fcm') or {}
path = fcm.get('token_store_path')
if isinstance(path, str) and path.strip():
    print(path.strip())
PY
)"
    [[ -n "$token_store" ]] && token_stores+=("${~token_store}")
  done

  token_stores+=("${XDG_STATE_HOME:-${HOME}/.local/state}/pocket-bridge/fcm-tokens.json")
  for token_store in ${(u)token_stores}; do
    [[ -r "$token_store" ]] || continue
    candidates+=(${(f)"$(python3 - "$token_store" <<'PY'
import json, sys
path = sys.argv[1]
try:
    data = json.load(open(path, 'r', encoding='utf-8'))
except Exception:
    raise SystemExit(0)
for key in data.keys():
    if isinstance(key, str) and key.strip():
        print(key.strip())
PY
)"})
  done

  candidates+=(phone laptop phone_avd)
  print -l ${(u)candidates}
}

_pb_complete_targets() {
  local -a targets
  targets=(${(f)"$(_pb_target_candidates)"})
  if (( ${#targets} == 0 )); then
    _message 'target device'
    return
  fi
  _values 'target device' ${targets}
}

_pb() {
  local -a commands kinds clip_actions shells positionals
  local cmd="" clip_action=""
  local i

  commands=(
    'keygen:generate a device key pair'
    'status:show agent status'
    'notify:send a notification to a target device'
    'clip:push or pull clipboard text'
    'task:send a task status event'
    'completion:print shell completion script'
  )
  kinds=(
    'started:task started'
    'blocked:task blocked'
    'done:task completed'
    'failed:task failed'
  )
  clip_actions=(
    'push:send clipboard text'
    'pull:pull clipboard text'
  )
  shells=(
    'zsh:zsh completion'
  )

  if [[ "${words[CURRENT-1]}" == "--socket" ]]; then
    _files
    return
  fi

  if [[ "${words[CURRENT]}" == --* ]]; then
    _values 'pb option' --socket
    return
  fi

  for (( i = 2; i < CURRENT; i++ )); do
    case "${words[i]}" in
      --socket)
        (( i++ ))
        ;;
      -*)
        ;;
      *)
        positionals+=("${words[i]}")
        ;;
    esac
  done

  if (( ${#positionals} == 0 )); then
    _describe -t commands 'pb command' commands
    return
  fi

  cmd="${positionals[1]}"

  case "$cmd" in
    keygen|status)
      return
      ;;
    notify)
      case ${#positionals} in
        1) _pb_complete_targets ;;
        2) _message 'notification title' ;;
        *) _message 'notification body' ;;
      esac
      return
      ;;
    clip)
      if (( ${#positionals} == 1 )); then
        _describe -t clip_actions 'clipboard action' clip_actions
        return
      fi
      clip_action="${positionals[2]}"
      case "$clip_action" in
        push)
          case ${#positionals} in
            2) _pb_complete_targets ;;
            *) _message 'clipboard text' ;;
          esac
          ;;
        pull)
          if (( ${#positionals} == 2 )); then
            _pb_complete_targets
          fi
          ;;
      esac
      return
      ;;
    task)
      if (( ${#positionals} == 1 )); then
        _describe -t task_kinds 'task status' kinds
        return
      fi
      case ${#positionals} in
        2) _message 'task title' ;;
        *) _message 'task summary' ;;
      esac
      return
      ;;
    completion)
      if (( ${#positionals} == 1 )); then
        _describe -t shells 'shell' shells
      fi
      return
      ;;
  esac
}

_pb "$@"
`
}
