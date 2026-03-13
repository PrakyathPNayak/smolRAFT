package raft

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/smolRAFT/types"
)

// TestThreeNodeElection starts three RAFT nodes, waits for an election,
// and asserts exactly one leader emerges within a bounded time.
func TestThreeNodeElection(t *testing.T) {
	nodes, cleanup := startCluster(t, 3)
	defer cleanup()

	leader := waitForLeader(t, nodes, 5*time.Second)
	t.Logf("leader elected: %s (term %d)", leader.ID(), leader.Status().Term)

	followerCount := 0
	for _, n := range nodes {
		s := n.Status()
		if s.State == types.Follower {
			followerCount++
		}
	}
	if followerCount != 2 {
		t.Fatalf("expected 2 followers, got %d", followerCount)
	}
}

// TestLeaderReplicatesEntry verifies that a stroke submitted to the
// leader is replicated and committed across the cluster.
func TestLeaderReplicatesEntry(t *testing.T) {
	nodes, cleanup := startCluster(t, 3)
	defer cleanup()

	leader := waitForLeader(t, nodes, 5*time.Second)

	stroke := types.StrokeEvent{
		ID:     "stroke-1",
		Points: []types.Point{{X: 10, Y: 20}, {X: 30, Y: 40}},
		Color:  "#ff0000",
		Width:  3.0,
		UserID: "user-1",
	}
	if err := leader.HandleStroke(stroke); err != nil {
		t.Fatalf("HandleStroke: %v", err)
	}

	// Wait for commit to propagate
	deadline := time.After(5 * time.Second)
	for {
		allCommitted := true
		for _, n := range nodes {
			s := n.Status()
			if s.CommitIndex < 1 {
				allCommitted = false
				break
			}
		}
		if allCommitted {
			break
		}
		select {
		case <-deadline:
			for _, n := range nodes {
				s := n.Status()
				t.Logf("node %s: state=%s term=%d commit=%d logLen=%d",
					s.NodeID, s.State, s.Term, s.CommitIndex, s.LogLength)
			}
			t.Fatal("timed out waiting for commit to propagate to all nodes")
		case <-time.After(50 * time.Millisecond):
		}
	}

	// Verify all nodes have the entry
	for _, n := range nodes {
		s := n.Status()
		if s.LogLength != 1 {
			t.Errorf("node %s: expected logLength=1, got %d", s.NodeID, s.LogLength)
		}
		if s.CommitIndex != 1 {
			t.Errorf("node %s: expected commitIndex=1, got %d", s.NodeID, s.CommitIndex)
		}
	}
}

// TestNonLeaderRejectsStroke verifies that a follower returns ErrNotLeader.
func TestNonLeaderRejectsStroke(t *testing.T) {
	nodes, cleanup := startCluster(t, 3)
	defer cleanup()

	waitForLeader(t, nodes, 5*time.Second)

	var follower *Node
	for _, n := range nodes {
		if n.Status().State == types.Follower {
			follower = n
			break
		}
	}
	if follower == nil {
		t.Fatal("no follower found")
	}

	stroke := types.StrokeEvent{
		ID:     "stroke-1",
		Points: []types.Point{{X: 10, Y: 20}},
		Color:  "#00ff00",
		Width:  1.0,
		UserID: "user-2",
	}
	err := follower.HandleStroke(stroke)
	if err != ErrNotLeader {
		t.Fatalf("expected ErrNotLeader, got %v", err)
	}
}

// startCluster creates n RAFT nodes backed by real HTTP servers.
func startCluster(t *testing.T, n int) ([]*Node, func()) {
	t.Helper()

	type nodeInfo struct {
		server *httptest.Server
		node   *Node
	}

	infos := make([]nodeInfo, n)
	handlers := make([]*http.ServeMux, n)

	// Phase 1: create test servers to get addresses
	for i := 0; i < n; i++ {
		mux := http.NewServeMux()
		handlers[i] = mux
		srv := httptest.NewServer(mux)
		infos[i] = nodeInfo{server: srv}
	}

	// Phase 2: build addresses and create nodes
	addrs := make([]string, n)
	for i := 0; i < n; i++ {
		addrs[i] = strings.TrimPrefix(infos[i].server.URL, "http://")
	}

	// Mock gateway to collect commit notifications
	var gatewayMu sync.Mutex
	var committed []types.CommitNotification
	_ = committed
	gatewaySrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var notif types.CommitNotification
		if err := json.NewDecoder(r.Body).Decode(&notif); err == nil {
			gatewayMu.Lock()
			committed = append(committed, notif)
			gatewayMu.Unlock()
		}
		w.WriteHeader(http.StatusOK)
	}))
	gatewayAddr := strings.TrimPrefix(gatewaySrv.URL, "http://")

	ctx, cancel := context.WithCancel(context.Background())

	for i := 0; i < n; i++ {
		peers := make([]string, 0, n-1)
		for j := 0; j < n; j++ {
			if j != i {
				peers = append(peers, addrs[j])
			}
		}

		cfg := Config{
			NodeID:      fmt.Sprintf("node-%d", i),
			PeerAddrs:   peers,
			GatewayAddr: gatewayAddr,
		}

		rpc := NewHTTPRPCClient()
		node := NewNode(cfg, rpc)
		infos[i].node = node

		registerNodeRoutes(handlers[i], node)
		node.Start(ctx)
	}

	nodes := make([]*Node, n)
	for i := range infos {
		nodes[i] = infos[i].node
	}

	cleanup := func() {
		cancel()
		for _, info := range infos {
			info.node.Stop()
			info.server.Close()
		}
		gatewaySrv.Close()
	}

	return nodes, cleanup
}

// registerNodeRoutes wires RAFT RPC endpoints to a node on the given mux.
func registerNodeRoutes(mux *http.ServeMux, node *Node) {
	mux.HandleFunc("/request-vote", func(w http.ResponseWriter, r *http.Request) {
		var req types.VoteRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		resp := node.HandleVoteRequest(req)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	})

	mux.HandleFunc("/append-entries", func(w http.ResponseWriter, r *http.Request) {
		var req types.AppendEntriesRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		resp := node.HandleAppendEntries(req)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	})

	mux.HandleFunc("/commit", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
}

func waitForLeader(t *testing.T, nodes []*Node, timeout time.Duration) *Node {
	t.Helper()
	deadline := time.After(timeout)
	for {
		for _, n := range nodes {
			if n.Status().State == types.Leader {
				return n
			}
		}
		select {
		case <-deadline:
			for _, n := range nodes {
				s := n.Status()
				t.Logf("node %s: state=%s term=%d", s.NodeID, s.State, s.Term)
			}
			t.Fatal("timed out waiting for leader election")
			return nil
		case <-time.After(50 * time.Millisecond):
		}
	}
}
