package server

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"time"
)

// Server wraps an HTTP server with RAFT replica handlers and graceful shutdown.
type Server struct {
	httpServer *http.Server
	handler    *Handler
	logger     *slog.Logger
}

// NewServer creates a new HTTP server for a RAFT replica node.
// It registers all RAFT RPC and operational endpoints.
func NewServer(port string, handler *Handler, logger *slog.Logger) *Server {
	mux := http.NewServeMux()

	// RAFT RPC endpoints
	mux.HandleFunc("/request-vote", handler.HandleRequestVote)
	mux.HandleFunc("/append-entries", handler.HandleAppendEntries)
	mux.HandleFunc("/heartbeat", handler.HandleHeartbeat)

	// Operational endpoints
	mux.HandleFunc("/stroke", handler.HandleStroke)
	mux.HandleFunc("/status", handler.HandleStatus)
	mux.HandleFunc("/health", handler.HandleHealth)
	mux.HandleFunc("/metrics", handler.HandleMetrics)
	mux.HandleFunc("/sync-log", handler.HandleSyncLog)

	return &Server{
		httpServer: &http.Server{
			Addr:         fmt.Sprintf(":%s", port),
			Handler:      mux,
			ReadTimeout:  10 * time.Second,
			WriteTimeout: 10 * time.Second,
			IdleTimeout:  60 * time.Second,
		},
		handler: handler,
		logger:  logger,
	}
}

// Start begins listening for HTTP requests. This method blocks until the
// server is shut down. Returns an error if the server fails to start
// (address already in use, etc.), but returns nil on graceful shutdown.
func (s *Server) Start() error {
	s.logger.Info("starting HTTP server", "addr", s.httpServer.Addr)
	err := s.httpServer.ListenAndServe()
	if err == http.ErrServerClosed {
		return nil
	}
	return fmt.Errorf("http server: %w", err)
}

// Shutdown gracefully stops the HTTP server with a 10-second deadline.
// In-flight requests are allowed to complete before the deadline.
func (s *Server) Shutdown() error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	s.logger.Info("shutting down HTTP server")
	return s.httpServer.Shutdown(ctx)
}
