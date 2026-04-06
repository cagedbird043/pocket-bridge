package main

import (
	"context"
	"flag"
	"log"
	"os/signal"
	"syscall"

	"github.com/cagedbird043/pocket-bridge/internal/config"
	"github.com/cagedbird043/pocket-bridge/internal/relay"
)

func main() {
	var configPath string
	flag.StringVar(&configPath, "config", "configs/relay.example.json", "path to relay config")
	flag.Parse()

	cfg, err := config.LoadRelayConfig(configPath)
	if err != nil {
		log.Fatalf("load relay config: %v", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	srv := relay.New(cfg)
	if err := srv.Run(ctx); err != nil {
		log.Fatal(err)
	}
}
