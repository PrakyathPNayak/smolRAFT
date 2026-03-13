// Package main is the entry point for a smolRAFT replica node.
// It reads configuration from environment variables, wires up the
// RAFT node and HTTP server, and handles graceful shutdown on SIGTERM/SIGINT.
package main

import (
	"context"
	"log"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/smolRAFT/replica/internal/raft"
	"github.com/smolRAFT/replica/internal/server"
	"github.com/smolRAFT/types"
)

func main() {
	nodeID := os.Getenv("NODE_ID")
	port := os.Getenv("PORT")
	peerAddrs := os.Getenv("PEER_ADDRS")
	gatewayAddr := os.Getenv("GATEWAY_ADDR")

	if port == "" {
		port = "9001"
	}

	cfg := raft.Config{
		NodeID:      nodeID,
		PeerAddrs:   splitAddrs(peerAddrs),
		GatewayAddr: gatewayAddr,
	}

	if err := cfg.Validate(); err != nil {
		log.Fatalf("invalid configuration: %v", err)
	}

	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelDebug,
	})).With("nodeId", nodeID)

	rpcClient := raft.NewHTTPRPCClient()
	node := raft.NewNode(cfg, rpcClient)

	handler := server.NewHandler(node, logger)
	srv := server.NewServer(port, handler, logger)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	node.Start(ctx)

	go runCommitNotifier(ctx, node, rpcClient, gatewayAddr, logger)

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)

	go func() {
		<-sigCh
		logger.Info("received shutdown signal")
		cancel()
		node.Stop()
		if err := srv.Shutdown(); err != nil {
			logger.Error("server shutdown error", "error", err)
		}
	}()

	if err := srv.Start(); err != nil {
		log.Fatalf("server error: %v", err)
	}
}

func runCommitNotifier(ctx context.Context, node *raft.Node, rpc raft.RPCClient, gatewayAddr string, logger *slog.Logger) {
	for {
		select {
		case <-ctx.Done():
			return
		case entry, ok := <-node.CommitCh():
			if !ok {
				return
			}
			notification := types.CommitNotification{
				Stroke: entry.Stroke,
				Index:  entry.Index,
				Term:   entry.Term,
			}
			if err := rpc.NotifyCommit(ctx, gatewayAddr, notification); err != nil {
				logger.Warn("failed to notify gateway of commit",
					"error", err,
					"index", entry.Index,
					"strokeId", entry.Stroke.ID,
				)
			}
		}
	}
}

func splitAddrs(s string) []string {
	if s == "" {
		return nil
	}
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
