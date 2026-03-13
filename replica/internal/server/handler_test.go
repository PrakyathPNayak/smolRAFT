package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"log/slog"
	"os"

	"github.com/smolRAFT/replica/internal/raft"
	"github.com/smolRAFT/types"
)

// testSetup creates a RAFT node backed by a mock RPC client and builds
// the HTTP handler on top of it. The node is NOT started (no
// election timer), so it stays a follower unless we poke it manually.
func testSetup(t *testing.T) (*Handler, *raft.Node) {
	t.Helper()
	cfg := raft.Config{
		NodeID:      "test-node",
		PeerAddrs:   []string{"peer1:9000", "peer2:9000"},
		GatewayAddr: "gateway:8000",
	}
	rpc := raft.NewHTTPRPCClient()
	node := raft.NewNode(cfg, rpc)
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelError}))
	h := NewHandler(node, logger)
	return h, node
}

func TestHandleHealth(t *testing.T) {
	h, _ := testSetup(t)
	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	rec := httptest.NewRecorder()
	h.HandleHealth(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	var body map[string]string
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body["status"] != "ok" {
		t.Fatalf("expected status ok, got %q", body["status"])
	}
}

func TestHandleStatus(t *testing.T) {
	h, _ := testSetup(t)
	req := httptest.NewRequest(http.MethodGet, "/status", nil)
	rec := httptest.NewRecorder()
	h.HandleStatus(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	var status types.StatusResponse
	if err := json.NewDecoder(rec.Body).Decode(&status); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if status.NodeID != "test-node" {
		t.Fatalf("expected nodeId test-node, got %q", status.NodeID)
	}
	if status.State != types.Follower {
		t.Fatalf("expected FOLLOWER, got %v", status.State)
	}
}

func TestHandleRequestVote(t *testing.T) {
	h, _ := testSetup(t)

	vr := types.VoteRequest{
		Term:         1,
		CandidateID:  "candidate-1",
		LastLogIndex: 0,
		LastLogTerm:  0,
	}
	body, _ := json.Marshal(vr)

	req := httptest.NewRequest(http.MethodPost, "/request-vote", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.HandleRequestVote(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	var resp types.VoteResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !resp.VoteGranted {
		t.Fatal("expected vote granted")
	}
	if resp.Term != 1 {
		t.Fatalf("expected term 1, got %d", resp.Term)
	}
}

func TestHandleRequestVote_MethodNotAllowed(t *testing.T) {
	h, _ := testSetup(t)
	req := httptest.NewRequest(http.MethodGet, "/request-vote", nil)
	rec := httptest.NewRecorder()
	h.HandleRequestVote(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d", rec.Code)
	}
}

func TestHandleAppendEntries(t *testing.T) {
	h, _ := testSetup(t)

	ae := types.AppendEntriesRequest{
		Term:         1,
		LeaderID:     "leader-1",
		PrevLogIndex: 0,
		PrevLogTerm:  0,
		Entries:      nil,
		LeaderCommit: 0,
	}
	body, _ := json.Marshal(ae)

	req := httptest.NewRequest(http.MethodPost, "/append-entries", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.HandleAppendEntries(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	var resp types.AppendEntriesResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !resp.Success {
		t.Fatal("expected success for valid heartbeat")
	}
}

func TestHandleStroke_NotLeader(t *testing.T) {
	h, _ := testSetup(t)

	stroke := types.StrokeEvent{
		ID:     "stroke-1",
		Points: []types.Point{{X: 10, Y: 20}},
		Color:  "#ff0000",
		Width:  2.0,
		UserID: "user-1",
	}
	body, _ := json.Marshal(stroke)

	req := httptest.NewRequest(http.MethodPost, "/stroke", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.HandleStroke(rec, req)

	// Follower node should return 503
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d", rec.Code)
	}
}

func TestHandleSyncLog_Empty(t *testing.T) {
	h, _ := testSetup(t)
	req := httptest.NewRequest(http.MethodGet, "/sync-log?from=0", nil)
	rec := httptest.NewRecorder()
	h.HandleSyncLog(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
}

func TestHandleHeartbeat(t *testing.T) {
	h, _ := testSetup(t)

	hb := types.AppendEntriesRequest{
		Term:         1,
		LeaderID:     "leader-1",
		PrevLogIndex: 0,
		PrevLogTerm:  0,
		Entries:      nil,
		LeaderCommit: 0,
	}
	body, _ := json.Marshal(hb)

	req := httptest.NewRequest(http.MethodPost, "/heartbeat", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.HandleHeartbeat(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
}

func TestHandleMetrics(t *testing.T) {
	h, _ := testSetup(t)
	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	rec := httptest.NewRecorder()

	h.HandleMetrics(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "smolraft_replica_term") {
		t.Fatalf("expected term metric, got: %s", body)
	}
}
