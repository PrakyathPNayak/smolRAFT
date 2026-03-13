package main

import (
	"context"
	"log"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/smolRAFT/gateway/internal/gateway"
	"github.com/smolRAFT/gateway/internal/leader"
)

func main() {
	port := os.Getenv("PORT")
	replicaAddrs := os.Getenv("REPLICA_ADDRS")

	if port == "" {
		port = "8080"
	}
	if replicaAddrs == "" {
		log.Fatal("REPLICA_ADDRS is required")
	}

	addrs := splitAddrs(replicaAddrs)

	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelDebug,
	}))

	tracker := leader.NewTracker(addrs, logger)
	hub := gateway.NewHub(logger)
	handler := gateway.NewHandler(hub, tracker, logger)
	srv := gateway.NewServer(port, handler, logger)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go hub.Run()
	go tracker.Start(ctx)

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)

	go func() {
		<-sigCh
		logger.Info("received shutdown signal")
		cancel()
		if err := srv.Shutdown(); err != nil {
			logger.Error("server shutdown error", "error", err)
		}
	}()

	if err := srv.Start(); err != nil {
		log.Fatalf("server error: %v", err)
	}
}

func splitAddrs(s string) []string {
	parts := strings.Split(s, ",")
	addrs := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			addrs = append(addrs, p)
		}
	}
	return addrs
}
