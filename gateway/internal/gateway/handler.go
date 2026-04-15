package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/gorilla/websocket"
	"github.com/smolRAFT/gateway/internal/leader"
	"github.com/smolRAFT/types"
)

const (
	// writeWait is the time allowed to write a message to the client.
	writeWait = 10 * time.Second
	// pongWait is the time allowed to read the next pong message from the client.
	pongWait = 60 * time.Second
	// pingPeriod is the interval for sending pings to the client.
	pingPeriod = (pongWait * 9) / 10
	// maxMessageSize is the maximum message size allowed from the client.
	maxMessageSize = 8192
	// strokeForwardTimeout is the timeout for forwarding strokes to the leader.
	strokeForwardTimeout = 2 * time.Second
)

var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	CheckOrigin: func(r *http.Request) bool {
		return true // Allow all origins for demo
	},
}

// Handler holds HTTP/WS handlers for the gateway.
type Handler struct {
	hub     *Hub
	tracker *leader.Tracker
	logger  *slog.Logger
	client  *http.Client
}

// NewHandler creates a new gateway Handler.
func NewHandler(hub *Hub, tracker *leader.Tracker, logger *slog.Logger) *Handler {
	return &Handler{
		hub:     hub,
		tracker: tracker,
		logger:  logger.With("component", "handler"),
		client: &http.Client{
			Timeout: strokeForwardTimeout,
		},
	}
}

// HandleWS upgrades the HTTP connection to a WebSocket and serves a client.
func (h *Handler) HandleWS(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		h.logger.Error("websocket upgrade failed", "error", err)
		return
	}

	client := &Client{
		conn: conn,
		send: make(chan []byte, 256),
	}
	h.hub.register <- client

	go h.writePump(client)
	go h.readPump(client)

	// Replay committed canvas history so new clients see the current board state.
	go h.replayHistory(client)
}

// HandleCommit receives committed stroke notifications from replica leaders.
// It broadcasts the stroke to all connected WebSocket clients.
func (h *Handler) HandleCommit(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var notification types.CommitNotification
	if err := json.NewDecoder(r.Body).Decode(&notification); err != nil {
		h.logger.Warn("invalid commit notification", "error", err)
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	msg := types.WSMessage{
		Type:    "stroke",
		Payload: notification.Stroke,
	}
	data, err := json.Marshal(msg)
	if err != nil {
		h.logger.Error("marshal ws message", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	h.hub.Broadcast(data)
	w.WriteHeader(http.StatusOK)
}

// HandleHealth responds to liveness checks.
func (h *Handler) HandleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

// HandleStatus returns gateway status including leader addr and client count.
func (h *Handler) HandleStatus(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"leader":  h.tracker.LeaderAddr(),
		"clients": h.hub.ClientCount(),
	})
}

// HandleLeader returns the current known leader address.
func (h *Handler) HandleLeader(w http.ResponseWriter, r *http.Request) {
	leaderAddr := h.tracker.LeaderAddr()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{
		"leader":     leaderAddr,
		"leaderAddr": leaderAddr,
	})
}

// HandleMetrics exposes a small plaintext metrics surface for demos.
func (h *Handler) HandleMetrics(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; version=0.0.4")
	fmt.Fprintf(w, "smolraft_gateway_connected_clients %d\n", h.hub.ClientCount())
}

// readPump reads messages from the WebSocket client and forwards
// drawing strokes to the RAFT leader.
func (h *Handler) readPump(client *Client) {
	defer func() {
		h.hub.unregister <- client
		client.conn.Close()
	}()

	client.conn.SetReadLimit(maxMessageSize)
	client.conn.SetReadDeadline(time.Now().Add(pongWait))
	client.conn.SetPongHandler(func(string) error {
		client.conn.SetReadDeadline(time.Now().Add(pongWait))
		return nil
	})

	for {
		_, message, err := client.conn.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseNormalClosure) {
				h.logger.Warn("ws read error", "error", err)
			}
			return
		}

		// Parse the stroke and forward to the leader
		var stroke types.StrokeEvent
		if err := json.Unmarshal(message, &stroke); err != nil {
			h.logger.Warn("invalid stroke from client", "error", err)
			h.sendError(client, "invalid stroke data")
			continue
		}

		if err := h.forwardToLeader(stroke); err != nil {
			h.logger.Warn("failed to forward stroke", "error", err, "strokeId", stroke.ID)
			h.sendError(client, "failed to submit stroke")
		}
	}
}

// writePump pumps messages from the hub to the WebSocket connection.
func (h *Handler) writePump(client *Client) {
	ticker := time.NewTicker(pingPeriod)
	defer func() {
		ticker.Stop()
		client.conn.Close()
	}()

	for {
		select {
		case message, ok := <-client.send:
			client.conn.SetWriteDeadline(time.Now().Add(writeWait))
			if !ok {
				client.conn.WriteMessage(websocket.CloseMessage, []byte{})
				return
			}

			w, err := client.conn.NextWriter(websocket.TextMessage)
			if err != nil {
				return
			}
			w.Write(message)

			// Drain any queued messages into the same write
			n := len(client.send)
			for i := 0; i < n; i++ {
				w.Write([]byte("\n"))
				w.Write(<-client.send)
			}

			if err := w.Close(); err != nil {
				return
			}

		case <-ticker.C:
			client.conn.SetWriteDeadline(time.Now().Add(writeWait))
			if err := client.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}
}

// replayHistory fetches all committed strokes from the current leader via
// /sync-log and sends them to the newly connected client so it sees the
// full canvas state immediately.
func (h *Handler) replayHistory(client *Client) {
	leaderAddr := h.tracker.LeaderAddr()
	if leaderAddr == "" {
		h.tracker.PollOnce(context.Background())
		leaderAddr = h.tracker.LeaderAddr()
	}
	if leaderAddr == "" {
		return
	}

	url := fmt.Sprintf("http://%s/sync-log?from=1", leaderAddr)
	resp, err := h.client.Get(url)
	if err != nil {
		h.logger.Warn("sync-log fetch failed on client connect", "error", err)
		return
	}
	defer resp.Body.Close()

	var entries []types.LogEntry
	if err := json.NewDecoder(resp.Body).Decode(&entries); err != nil {
		h.logger.Warn("sync-log decode failed", "error", err)
		return
	}

	for _, entry := range entries {
		msg := types.WSMessage{
			Type:    "stroke",
			Payload: entry.Stroke,
		}
		data, err := json.Marshal(msg)
		if err != nil {
			continue
		}
		select {
		case client.send <- data:
		default:
			// Client buffer full; stop replaying to avoid blocking
			return
		}
	}
}

// forwardToLeader sends a stroke to the current RAFT leader via HTTP POST.
func (h *Handler) forwardToLeader(stroke types.StrokeEvent) error {
	if err := h.forwardToLeaderAddr(stroke, h.tracker.LeaderAddr()); err == nil {
		return nil
	}

	// Refresh leader once and retry to improve failover UX.
	h.tracker.PollOnce(context.Background())
	return h.forwardToLeaderAddr(stroke, h.tracker.LeaderAddr())
}

func (h *Handler) forwardToLeaderAddr(stroke types.StrokeEvent, leaderAddr string) error {
	if leaderAddr == "" {
		return fmt.Errorf("no known leader")
	}

	data, err := json.Marshal(stroke)
	if err != nil {
		return fmt.Errorf("marshal stroke: %w", err)
	}

	url := fmt.Sprintf("http://%s/stroke", leaderAddr)
	resp, err := h.client.Post(url, "application/json", bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("forward to leader: %w", err)
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body)

	if resp.StatusCode == http.StatusServiceUnavailable {
		return fmt.Errorf("leader rejected: not the leader anymore")
	}
	if resp.StatusCode != http.StatusAccepted {
		return fmt.Errorf("leader returned status %d", resp.StatusCode)
	}
	return nil
}

// sendError sends an error message to a single WebSocket client.
func (h *Handler) sendError(client *Client, msg string) {
	errMsg := types.WSMessage{
		Type:    "error",
		Payload: map[string]string{"message": msg},
	}
	data, err := json.Marshal(errMsg)
	if err != nil {
		return
	}
	select {
	case client.send <- data:
	default:
	}
}
