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

func postJSON(client *http.Client, path string, payload any) {
	data, err := json.Marshal(payload)
	if err != nil {
		exitErr(err)
	}
	resp, err := client.Post("http://unix"+path, "application/json", bytes.NewReader(data))
	if err != nil {
		exitErr(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		body, _ := io.ReadAll(resp.Body)
		exitErr(fmt.Errorf("status %s: %s", resp.Status, strings.TrimSpace(string(body))))
	}
}

func newUnixHTTPClient(socket string) *http.Client {
	transport := &http.Transport{
		DialContext: func(_ context.Context, _, _ string) (net.Conn, error) {
			return net.Dial("unix", socket)
		},
	}
	return &http.Client{
		Transport: transport,
		Timeout:   3 * time.Second,
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
	fmt.Fprintln(os.Stderr, "  pb [--socket PATH] task <started|blocked|done|failed> <title> <summary>")
	os.Exit(1)
}

func exitErr(err error) {
	fmt.Fprintln(os.Stderr, "错误:", err)
	os.Exit(1)
}
