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

_pb() {
  local -a commands kinds clip_actions shells
  local cmd=""
  local cmd_index=0
  local arg_index=0
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

  _arguments -C \
    '--socket[agent unix socket path]:socket path:_files' \
    '1:command:->command' \
    '*::arg:->args'

  case $state in
    command)
      _describe -t commands 'pb command' commands
      return
      ;;
    args)
      for (( i = 2; i < CURRENT; i++ )); do
        case "${words[i]}" in
          --socket)
            (( i++ ))
            ;;
          -*)
            ;;
          *)
            cmd="${words[i]}"
            cmd_index=$i
            break
            ;;
        esac
      done
      (( arg_index = CURRENT - cmd_index ))

      case "$cmd" in
        keygen|status)
          return
          ;;
        notify)
          case $arg_index in
            1) _message 'target device' ;;
            2) _message 'notification title' ;;
            *) _message 'notification body' ;;
          esac
          return
          ;;
        clip)
          if (( arg_index == 1 )); then
            _describe -t clip_actions 'clipboard action' clip_actions
            return
          fi
          case "${words[cmd_index + 1]}" in
            push)
              case $arg_index in
                2) _message 'target device' ;;
                *) _message 'clipboard text' ;;
              esac
              ;;
            pull)
              if (( arg_index == 2 )); then
                _message 'target device'
              fi
              ;;
          esac
          return
          ;;
        task)
          if (( arg_index == 1 )); then
            _describe -t task_kinds 'task status' kinds
            return
          fi
          case $arg_index in
            2) _message 'task title' ;;
            *) _message 'task summary' ;;
          esac
          return
          ;;
        completion)
          if (( arg_index == 1 )); then
            _describe -t shells 'shell' shells
          fi
          return
          ;;
      esac
      ;;
  esac
}

_pb "$@"
`
}
