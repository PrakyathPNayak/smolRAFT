package raft

import (
	"context"
	"sync"
	"testing"

	"github.com/smolRAFT/types"
)

// mockRPCClient is a test double for RPCClient that records calls and
// returns configurable responses.
type mockRPCClient struct {
	mu              sync.Mutex
	voteResponses   map[string]types.VoteResponse
	voteErrors      map[string]error
	appendResponses map[string]types.AppendEntriesResponse
	appendErrors    map[string]error
	voteCalls       []voteCall
	appendCalls     []appendCall
	commitCalls     []types.CommitNotification
}

type voteCall struct {
	Addr string
	Req  types.VoteRequest
}

type appendCall struct {
	Addr string
	Req  types.AppendEntriesRequest
}

func newMockRPC() *mockRPCClient {
	return &mockRPCClient{
		voteResponses:   make(map[string]types.VoteResponse),
		voteErrors:      make(map[string]error),
		appendResponses: make(map[string]types.AppendEntriesResponse),
		appendErrors:    make(map[string]error),
	}
}

func (m *mockRPCClient) RequestVote(_ context.Context, addr string, req types.VoteRequest) (types.VoteResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.voteCalls = append(m.voteCalls, voteCall{Addr: addr, Req: req})
	if err, ok := m.voteErrors[addr]; ok {
		return types.VoteResponse{}, err
	}
	if resp, ok := m.voteResponses[addr]; ok {
		return resp, nil
	}
	return types.VoteResponse{Term: req.Term, VoteGranted: false}, nil
}

func (m *mockRPCClient) AppendEntries(_ context.Context, addr string, req types.AppendEntriesRequest) (types.AppendEntriesResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.appendCalls = append(m.appendCalls, appendCall{Addr: addr, Req: req})
	if err, ok := m.appendErrors[addr]; ok {
		return types.AppendEntriesResponse{}, err
	}
	if resp, ok := m.appendResponses[addr]; ok {
		return resp, nil
	}
	return types.AppendEntriesResponse{Term: req.Term, Success: true}, nil
}

func (m *mockRPCClient) NotifyCommit(_ context.Context, _ string, notification types.CommitNotification) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.commitCalls = append(m.commitCalls, notification)
	return nil
}

func (m *mockRPCClient) FetchSyncLog(_ context.Context, _ string, _ int) ([]types.LogEntry, error) {
	return nil, nil
}

func makeTestNode(id string, peers []string) (*Node, *mockRPCClient) {
	rpc := newMockRPC()
	cfg := Config{
		NodeID:      id,
		PeerAddrs:   peers,
		GatewayAddr: "gateway:8080",
	}
	node := NewNode(cfg, rpc)
	return node, rpc
}

func TestNewNode(t *testing.T) {
	node, _ := makeTestNode("node1", []string{"peer1:9002", "peer2:9003"})

	status := node.Status()
	if status.NodeID != "node1" {
		t.Errorf("nodeID = %q, want %q", status.NodeID, "node1")
	}
	if status.State != types.Follower {
		t.Errorf("state = %v, want FOLLOWER", status.State)
	}
	if status.Term != 0 {
		t.Errorf("term = %d, want 0", status.Term)
	}
	if status.LogLength != 0 {
		t.Errorf("logLength = %d, want 0", status.LogLength)
	}
	if node.IsLeader() {
		t.Error("new node should not be leader")
	}
}

func TestConfigValidate(t *testing.T) {
	tests := []struct {
		name    string
		cfg     Config
		wantErr bool
	}{
		{
			name: "valid config",
			cfg: Config{
				NodeID:      "node1",
				PeerAddrs:   []string{"peer1:9002"},
				GatewayAddr: "gateway:8080",
			},
			wantErr: false,
		},
		{
			name: "missing node ID",
			cfg: Config{
				PeerAddrs:   []string{"peer1:9002"},
				GatewayAddr: "gateway:8080",
			},
			wantErr: true,
		},
		{
			name: "missing peer addrs",
			cfg: Config{
				NodeID:      "node1",
				GatewayAddr: "gateway:8080",
			},
			wantErr: true,
		},
		{
			name: "missing gateway addr",
			cfg: Config{
				NodeID:    "node1",
				PeerAddrs: []string{"peer1:9002"},
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.cfg.Validate()
			if (err != nil) != tt.wantErr {
				t.Errorf("Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestHandleVoteRequest_GrantVote(t *testing.T) {
	node, _ := makeTestNode("node1", []string{"node2:9002", "node3:9003"})

	req := types.VoteRequest{
		Term:         1,
		CandidateID:  "node2",
		LastLogIndex: 0,
		LastLogTerm:  0,
	}

	resp := node.HandleVoteRequest(req)

	if !resp.VoteGranted {
		t.Error("expected vote to be granted")
	}
	if resp.Term != 1 {
		t.Errorf("response term = %d, want 1", resp.Term)
	}
}

func TestHandleVoteRequest_RejectStaleTerm(t *testing.T) {
	node, _ := makeTestNode("node1", []string{"node2:9002"})

	// Set node to term 5
	node.mu.Lock()
	node.currentTerm = 5
	node.mu.Unlock()

	req := types.VoteRequest{
		Term:         3, // older term
		CandidateID:  "node2",
		LastLogIndex: 0,
		LastLogTerm:  0,
	}

	resp := node.HandleVoteRequest(req)

	if resp.VoteGranted {
		t.Error("should not grant vote for stale term")
	}
	if resp.Term != 5 {
		t.Errorf("response term = %d, want 5", resp.Term)
	}
}

func TestHandleVoteRequest_RejectAlreadyVoted(t *testing.T) {
	node, _ := makeTestNode("node1", []string{"node2:9002", "node3:9003"})

	// First vote - should succeed
	req1 := types.VoteRequest{
		Term:         1,
		CandidateID:  "node2",
		LastLogIndex: 0,
		LastLogTerm:  0,
	}
	resp1 := node.HandleVoteRequest(req1)
	if !resp1.VoteGranted {
		t.Fatal("first vote should be granted")
	}

	// Second vote for different candidate in same term - should fail
	req2 := types.VoteRequest{
		Term:         1,
		CandidateID:  "node3",
		LastLogIndex: 0,
		LastLogTerm:  0,
	}
	resp2 := node.HandleVoteRequest(req2)
	if resp2.VoteGranted {
		t.Error("should not grant second vote in same term")
	}
}

func TestHandleVoteRequest_RejectOutdatedLog(t *testing.T) {
	node, _ := makeTestNode("node1", []string{"node2:9002"})

	// Node has entries in its log
	node.log.Append(types.LogEntry{Term: 2})
	node.log.Append(types.LogEntry{Term: 3})

	// Candidate has shorter log with lower last term
	req := types.VoteRequest{
		Term:         4,
		CandidateID:  "node2",
		LastLogIndex: 1,
		LastLogTerm:  2,
	}

	resp := node.HandleVoteRequest(req)

	if resp.VoteGranted {
		t.Error("should not grant vote to candidate with outdated log")
	}
}

func TestHandleVoteRequest_HigherTermStepsDown(t *testing.T) {
	node, _ := makeTestNode("node1", []string{"node2:9002"})

	node.mu.Lock()
	node.currentTerm = 3
	node.state = types.Leader
	node.mu.Unlock()

	req := types.VoteRequest{
		Term:         5,
		CandidateID:  "node2",
		LastLogIndex: 0,
		LastLogTerm:  0,
	}

	resp := node.HandleVoteRequest(req)

	if !resp.VoteGranted {
		t.Error("should grant vote to higher term candidate")
	}

	status := node.Status()
	if status.State != types.Follower {
		t.Errorf("state = %v, want FOLLOWER after seeing higher term", status.State)
	}
	if status.Term != 5 {
		t.Errorf("term = %d, want 5", status.Term)
	}
}

func TestHandleAppendEntries_ValidHeartbeat(t *testing.T) {
	node, _ := makeTestNode("node1", []string{"node2:9002"})

	node.mu.Lock()
	node.currentTerm = 1
	node.mu.Unlock()

	req := types.AppendEntriesRequest{
		Term:         1,
		LeaderID:     "node2",
		PrevLogIndex: 0,
		PrevLogTerm:  0,
		Entries:      nil,
		LeaderCommit: 0,
	}

	resp := node.HandleAppendEntries(req)

	if !resp.Success {
		t.Error("heartbeat should succeed")
	}

	status := node.Status()
	if status.LeaderID != "node2" {
		t.Errorf("leaderID = %q, want %q", status.LeaderID, "node2")
	}
}

func TestHandleAppendEntries_RejectStaleTerm(t *testing.T) {
	node, _ := makeTestNode("node1", []string{"node2:9002"})

	node.mu.Lock()
	node.currentTerm = 5
	node.mu.Unlock()

	req := types.AppendEntriesRequest{
		Term:     3,
		LeaderID: "node2",
	}

	resp := node.HandleAppendEntries(req)

	if resp.Success {
		t.Error("should reject AppendEntries with stale term")
	}
	if resp.Term != 5 {
		t.Errorf("response term = %d, want 5", resp.Term)
	}
}

func TestHandleAppendEntries_WithEntries(t *testing.T) {
	node, _ := makeTestNode("node1", []string{"node2:9002"})

	req := types.AppendEntriesRequest{
		Term:         1,
		LeaderID:     "node2",
		PrevLogIndex: 0,
		PrevLogTerm:  0,
		Entries: []types.LogEntry{
			{Index: 1, Term: 1, Stroke: types.StrokeEvent{ID: "s1"}},
			{Index: 2, Term: 1, Stroke: types.StrokeEvent{ID: "s2"}},
		},
		LeaderCommit: 2,
	}

	resp := node.HandleAppendEntries(req)

	if !resp.Success {
		t.Error("AppendEntries with valid entries should succeed")
	}

	status := node.Status()
	if status.LogLength != 2 {
		t.Errorf("log length = %d, want 2", status.LogLength)
	}
	if status.CommitIndex != 2 {
		t.Errorf("commit index = %d, want 2", status.CommitIndex)
	}
}

func TestHandleAppendEntries_LogMismatch(t *testing.T) {
	node, _ := makeTestNode("node1", []string{"node2:9002"})

	// Node has entry at index 1 with term 1
	node.log.Append(types.LogEntry{Term: 1})

	// Leader sends entry expecting prevLogIndex=2 which doesn't exist
	req := types.AppendEntriesRequest{
		Term:         2,
		LeaderID:     "node2",
		PrevLogIndex: 2,
		PrevLogTerm:  1,
		Entries: []types.LogEntry{
			{Index: 3, Term: 2},
		},
		LeaderCommit: 0,
	}

	resp := node.HandleAppendEntries(req)

	if resp.Success {
		t.Error("should reject AppendEntries with missing prev entry")
	}
	if resp.ConflictIndex != 2 {
		t.Errorf("conflict index = %d, want 2", resp.ConflictIndex)
	}
}

func TestHandleAppendEntries_TermMismatch(t *testing.T) {
	node, _ := makeTestNode("node1", []string{"node2:9002"})

	// Node has entry at index 1 with term 1
	node.log.Append(types.LogEntry{Term: 1})

	// Leader expects term 2 at index 1
	req := types.AppendEntriesRequest{
		Term:         2,
		LeaderID:     "node2",
		PrevLogIndex: 1,
		PrevLogTerm:  2, // mismatch: we have term 1
		Entries: []types.LogEntry{
			{Index: 2, Term: 2},
		},
		LeaderCommit: 0,
	}

	resp := node.HandleAppendEntries(req)

	if resp.Success {
		t.Error("should reject AppendEntries with term mismatch")
	}
	if resp.ConflictIndex != 1 {
		t.Errorf("conflict index = %d, want 1", resp.ConflictIndex)
	}
}

func TestHandleAppendEntries_CandidateStepsDown(t *testing.T) {
	node, _ := makeTestNode("node1", []string{"node2:9002"})

	node.mu.Lock()
	node.state = types.Candidate
	node.currentTerm = 2
	node.mu.Unlock()

	req := types.AppendEntriesRequest{
		Term:         2,
		LeaderID:     "node2",
		PrevLogIndex: 0,
		PrevLogTerm:  0,
	}

	resp := node.HandleAppendEntries(req)

	if !resp.Success {
		t.Error("candidate should accept valid AppendEntries")
	}

	status := node.Status()
	if status.State != types.Follower {
		t.Errorf("state = %v, want FOLLOWER", status.State)
	}
}

func TestHandleStroke_LeaderOnly(t *testing.T) {
	node, _ := makeTestNode("node1", []string{"node2:9002"})

	stroke := types.StrokeEvent{ID: "s1", Color: "#ff0000"}

	// Node is follower, should reject
	err := node.HandleStroke(stroke)
	if err != ErrNotLeader {
		t.Errorf("follower stroke error = %v, want ErrNotLeader", err)
	}

	// Make node leader
	node.mu.Lock()
	node.state = types.Leader
	node.mu.Unlock()

	err = node.HandleStroke(stroke)
	if err != nil {
		t.Errorf("leader stroke error = %v, want nil", err)
	}

	if node.log.Length() != 1 {
		t.Errorf("log length = %d, want 1 after stroke", node.log.Length())
	}
}

func TestGetSyncLog(t *testing.T) {
	node, _ := makeTestNode("node1", []string{"node2:9002"})

	// Add some entries and set commit index
	node.log.Append(types.LogEntry{Term: 1, Stroke: types.StrokeEvent{ID: "s1"}})
	node.log.Append(types.LogEntry{Term: 1, Stroke: types.StrokeEvent{ID: "s2"}})
	node.log.Append(types.LogEntry{Term: 2, Stroke: types.StrokeEvent{ID: "s3"}})

	node.mu.Lock()
	node.commitIndex = 2 // only first 2 entries committed
	node.mu.Unlock()

	entries := node.GetSyncLog(1)
	if len(entries) != 2 {
		t.Errorf("sync log from 1 len = %d, want 2 (only committed)", len(entries))
	}
}

func TestRandomElectionTimeout(t *testing.T) {
	for i := 0; i < 100; i++ {
		d := randomElectionTimeout()
		if d < ElectionTimeoutMin || d > ElectionTimeoutMax {
			t.Errorf("timeout %v out of range [%v, %v]", d, ElectionTimeoutMin, ElectionTimeoutMax)
		}
	}
}
