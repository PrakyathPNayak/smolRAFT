// Package server implements the HTTP server layer for a RAFT replica node.
// It bridges incoming HTTP requests to the underlying RaftNode and serializes
// responses as JSON.
package server

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/smolRAFT/replica/internal/raft"
	"github.com/smolRAFT/types"
)

// Handler holds HTTP handlers for all replica endpoints.
// It delegates to the RaftNode for all protocol logic.
type Handler struct {
	node   *raft.Node
	logger *slog.Logger
}

// NewHandler creates a new Handler backed by the given RAFT node.
func NewHandler(node *raft.Node, logger *slog.Logger) *Handler {
	return &Handler{
		node:   node,
		logger: logger,
	}
}

// HandleHealth responds to liveness checks with a 200 OK.
func (h *Handler) HandleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

// HandleMetrics exposes a small plaintext metrics surface for demos.
func (h *Handler) HandleMetrics(w http.ResponseWriter, r *http.Request) {
	status := h.node.Status()
	w.Header().Set("Content-Type", "text/plain; version=0.0.4")
	fmt.Fprintf(w, "smolraft_replica_term{node=\"%s\"} %d\n", status.NodeID, status.Term)
	fmt.Fprintf(w, "smolraft_replica_log_length{node=\"%s\"} %d\n", status.NodeID, status.LogLength)
	fmt.Fprintf(w, "smolraft_replica_commit_index{node=\"%s\"} %d\n", status.NodeID, status.CommitIndex)
}

// HandleStatus returns the current RAFT state of this node.
func (h *Handler) HandleStatus(w http.ResponseWriter, r *http.Request) {
	status := h.node.Status()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(status)
}

// HandleRequestVote processes an incoming RequestVote RPC.
func (h *Handler) HandleRequestVote(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req types.VoteRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.logger.Warn("invalid vote request body", "error", err)
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	resp := h.node.HandleVoteRequest(req)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

// HandleAppendEntries processes an incoming AppendEntries RPC.
func (h *Handler) HandleAppendEntries(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req types.AppendEntriesRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.logger.Warn("invalid append entries request body", "error", err)
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	resp := h.node.HandleAppendEntries(req)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

// HandleHeartbeat processes a heartbeat (empty AppendEntries).
func (h *Handler) HandleHeartbeat(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req types.AppendEntriesRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.logger.Warn("invalid heartbeat request body", "error", err)
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	// Heartbeat is just an AppendEntries with no entries
	req.Entries = nil
	resp := h.node.HandleAppendEntries(req)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

// HandleStroke accepts a new drawing stroke from the gateway.
// Only succeeds if this node is the current RAFT leader.
func (h *Handler) HandleStroke(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var stroke types.StrokeEvent
	if err := json.NewDecoder(r.Body).Decode(&stroke); err != nil {
		h.logger.Warn("invalid stroke request body", "error", err)
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	if err := h.node.HandleStroke(stroke); err != nil {
		if err == raft.ErrNotLeader {
			status := h.node.Status()
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusServiceUnavailable)
			json.NewEncoder(w).Encode(map[string]string{
				"error":    "not the leader",
				"leaderId": status.LeaderID,
			})
			return
		}
		h.logger.Error("stroke handling failed", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	json.NewEncoder(w).Encode(map[string]string{"status": "accepted"})
}

// HandleSyncLog returns committed log entries from a given index.
// Used by the catch-up protocol for restarted nodes.
func (h *Handler) HandleSyncLog(w http.ResponseWriter, r *http.Request) {
	fromStr := r.URL.Query().Get("from")
	from, err := strconv.Atoi(fromStr)
	if err != nil {
		from = 0
	}

	entries := h.node.GetSyncLog(from)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(entries)
}
