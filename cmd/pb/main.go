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
