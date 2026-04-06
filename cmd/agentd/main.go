package main

import (
	"context"
	"flag"
	"log"
	"os/signal"
	"syscall"

	"github.com/cagedbird043/pocket-bridge/internal/agent"
	"github.com/cagedbird043/pocket-bridge/internal/config"
)

func main() {
	var configPath string
	flag.StringVar(&configPath, "config", "configs/agent.laptop.example.json", "path to agent config")
	flag.Parse()

	cfg, err := config.LoadAgentConfig(configPath)
	if err != nil {
		log.Fatalf("load agent config: %v", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	svc, err := agent.New(cfg)
	if err != nil {
		log.Fatalf("init agent: %v", err)
	}
	if err := svc.Run(ctx); err != nil {
		log.Fatal(err)
	}
}
