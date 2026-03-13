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
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/smolRAFT/gateway/internal/leader"
	"github.com/smolRAFT/types"
)

func TestGatewayEndToEnd_WSForwardAndCommitBroadcast(t *testing.T) {
	var strokePosts atomic.Int32

	leaderSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/status":
			_ = json.NewEncoder(w).Encode(types.StatusResponse{NodeID: "node-1", State: types.Leader, LeaderID: "node-1"})
		case "/stroke":
			if r.Method != http.MethodPost {
				http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
				return
			}
			strokePosts.Add(1)
			w.WriteHeader(http.StatusAccepted)
		default:
			http.NotFound(w, r)
		}
	}))
	defer leaderSrv.Close()

	tkr := leader.NewTracker(
		[]string{strings.TrimPrefix(leaderSrv.URL, "http://")},
		slog.New(slog.NewTextHandler(io.Discard, nil)),
	)
	tkr.PollOnce(context.Background())

	hub := NewHub(slog.New(slog.NewTextHandler(io.Discard, nil)))
	go hub.Run()

	h := NewHandler(hub, tkr, slog.New(slog.NewTextHandler(io.Discard, nil)))
	s := NewServer("0", h, slog.New(slog.NewTextHandler(io.Discard, nil)))

	api := httptest.NewServer(s.httpServer.Handler)
	defer api.Close()

	wsURL := "ws" + strings.TrimPrefix(api.URL, "http") + "/ws"
	wsConn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("dial websocket: %v", err)
	}
	defer wsConn.Close()

	stroke := types.StrokeEvent{ID: "stroke-1", UserID: "u1", Color: "#00ff41", Width: 2, Points: []types.Point{{X: 1, Y: 2}, {X: 5, Y: 8}}}
	strokePayload, _ := json.Marshal(stroke)
	if err := wsConn.WriteMessage(websocket.TextMessage, strokePayload); err != nil {
		t.Fatalf("ws write stroke: %v", err)
	}

	deadline := time.After(1 * time.Second)
	for strokePosts.Load() == 0 {
		select {
		case <-deadline:
			t.Fatal("expected stroke to be forwarded to leader")
		default:
			time.Sleep(10 * time.Millisecond)
		}
	}

	commitBody, _ := json.Marshal(types.CommitNotification{Stroke: stroke, Index: 1, Term: 1})
	resp, err := http.Post(api.URL+"/commit", "application/json", bytes.NewReader(commitBody))
	if err != nil {
		t.Fatalf("post /commit: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("commit status = %d, want 200", resp.StatusCode)
	}

	_ = wsConn.SetReadDeadline(time.Now().Add(1 * time.Second))
	_, msg, err := wsConn.ReadMessage()
	if err != nil {
		t.Fatalf("read ws broadcast: %v", err)
	}

	var wsMsg types.WSMessage
	if err := json.Unmarshal(msg, &wsMsg); err != nil {
		t.Fatalf("unmarshal ws message: %v", err)
	}
	if wsMsg.Type != "stroke" {
		t.Fatalf("ws type = %q, want stroke", wsMsg.Type)
	}
}
