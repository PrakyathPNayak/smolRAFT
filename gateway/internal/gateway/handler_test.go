package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/smolRAFT/gateway/internal/leader"
	"github.com/smolRAFT/types"
)

func newHandlerForTest(t *testing.T) (*Handler, *Hub) {
	t.Helper()

	leaderSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(types.StatusResponse{NodeID: "node-1", State: types.Leader, LeaderID: "node-1"})
	}))
	t.Cleanup(leaderSrv.Close)

	tkr := leader.NewTracker([]string{strings.TrimPrefix(leaderSrv.URL, "http://")}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	tkr.PollOnce(context.Background())

	hub := NewHub(slog.New(slog.NewTextHandler(io.Discard, nil)))
	go hub.Run()

	h := NewHandler(hub, tkr, slog.New(slog.NewTextHandler(io.Discard, nil)))
	return h, hub
}

func TestHandleHealth(t *testing.T) {
	h, _ := newHandlerForTest(t)
	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	rec := httptest.NewRecorder()

	h.HandleHealth(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rec.Code)
	}
}

func TestHandleLeader(t *testing.T) {
	h, _ := newHandlerForTest(t)
	req := httptest.NewRequest(http.MethodGet, "/leader", nil)
	rec := httptest.NewRecorder()

	h.HandleLeader(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rec.Code)
	}
}

func TestHandleMetrics(t *testing.T) {
	h, _ := newHandlerForTest(t)
	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	rec := httptest.NewRecorder()

	h.HandleMetrics(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "smolraft_gateway_connected_clients") {
		t.Fatalf("expected metrics payload, got %q", rec.Body.String())
	}
}

func TestHandleCommit_BroadcastsStroke(t *testing.T) {
	h, hub := newHandlerForTest(t)

	client := &Client{send: make(chan []byte, 1)}
	hub.register <- client
	deadline := time.After(500 * time.Millisecond)
	for hub.ClientCount() != 1 {
		select {
		case <-deadline:
			t.Fatal("timed out waiting for client registration")
		default:
			time.Sleep(10 * time.Millisecond)
		}
	}

	body, _ := json.Marshal(types.CommitNotification{
		Stroke: types.StrokeEvent{ID: "s1", UserID: "u1"},
		Index:  1,
		Term:   1,
	})
	req := httptest.NewRequest(http.MethodPost, "/commit", bytes.NewReader(body))
	rec := httptest.NewRecorder()

	h.HandleCommit(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rec.Code)
	}
	select {
	case msg := <-client.send:
		var wsMsg types.WSMessage
		if err := json.Unmarshal(msg, &wsMsg); err != nil {
			t.Fatalf("unmarshal ws message: %v", err)
		}
		if wsMsg.Type != "stroke" {
			t.Fatalf("expected stroke message, got %q", wsMsg.Type)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("timed out waiting for broadcast")
	}
}

func TestHandleCommit_MethodNotAllowed(t *testing.T) {
	h, _ := newHandlerForTest(t)
	req := httptest.NewRequest(http.MethodGet, "/commit", nil)
	rec := httptest.NewRecorder()

	h.HandleCommit(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected status 405, got %d", rec.Code)
	}
}
