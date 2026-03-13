package raft

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"sync"
	"time"

	"github.com/smolRAFT/types"
)

// Timing constants for the RAFT protocol.
const (
	// ElectionTimeoutMin is the minimum election timeout duration.
	ElectionTimeoutMin = 500 * time.Millisecond
	// ElectionTimeoutMax is the maximum election timeout duration.
	ElectionTimeoutMax = 800 * time.Millisecond
	// HeartbeatInterval is the interval between leader heartbeats.
	HeartbeatInterval = 150 * time.Millisecond
	// RPCTimeout is the timeout for individual RPC calls to peers.
	RPCTimeout = 100 * time.Millisecond
)

// Config holds all configuration needed to initialize a RAFT node.
type Config struct {
	// NodeID is the unique identifier for this node in the cluster.
	NodeID string
	// PeerAddrs is the list of peer addresses (host:port) in the cluster.
	PeerAddrs []string
	// GatewayAddr is the address of the gateway service for commit callbacks.
	GatewayAddr string
}

// Validate checks that all required configuration fields are set.
// Returns an error describing the first missing field, or nil if valid.
func (c Config) Validate() error {
	if c.NodeID == "" {
		return fmt.Errorf("NODE_ID is required")
	}
	if len(c.PeerAddrs) == 0 {
		return fmt.Errorf("PEER_ADDRS is required")
	}
	if c.GatewayAddr == "" {
		return fmt.Errorf("GATEWAY_ADDR is required")
	}
	return nil
}

// Node represents a single participant in the Mini-RAFT cluster.
// It implements the Follower, Candidate, and Leader roles as described
// in the Ongaro & Ousterhout RAFT paper, simplified for a 3-node cluster.
//
// Thread safety: All exported methods are safe for concurrent use.
// Internal state is protected by mu (sync.RWMutex).
type Node struct {
	mu sync.RWMutex

	// Persistent state (would be on disk in production)
	id          string
	currentTerm int
	votedFor    string
	log         *Log

	// Volatile state
	state       types.NodeState
	commitIndex int
	lastApplied int
	leaderID    string

	// Leader-only volatile state (reinitialized on election)
	nextIndex  map[string]int
	matchIndex map[string]int

	// Configuration
	peers       []string
	gatewayAddr string

	// RPC client for communicating with peers and gateway
	rpc RPCClient

	// Channels for goroutine coordination
	resetElectionCh chan struct{} // signals election timer reset
	newEntryCh      chan struct{} // signals replication goroutines
	commitCh        chan types.LogEntry
	stopCh          chan struct{} // signals all goroutines to stop

	// Logger with node context
	logger *slog.Logger
}

// NewNode creates a new RAFT node with the given configuration and RPC client.
// The node starts in the FOLLOWER state with term 0 and an empty log.
// Call Start() to begin the election timer and participate in the cluster.
func NewNode(cfg Config, rpc RPCClient) *Node {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelDebug,
	})).With("nodeId", cfg.NodeID)

	n := &Node{
		id:          cfg.NodeID,
		currentTerm: 0,
		votedFor:    "",
		log:         NewLog(),

		state:       types.Follower,
		commitIndex: 0,
		lastApplied: 0,
		leaderID:    "",

		nextIndex:  make(map[string]int),
		matchIndex: make(map[string]int),

		peers:       cfg.PeerAddrs,
		gatewayAddr: cfg.GatewayAddr,

		rpc: rpc,

		resetElectionCh: make(chan struct{}, 1),
		newEntryCh:      make(chan struct{}, 1),
		commitCh:        make(chan types.LogEntry, 256),
		stopCh:          make(chan struct{}),

		logger: logger,
	}

	logger.Info("node created",
		"term", 0,
		"state", types.Follower,
		"peers", cfg.PeerAddrs,
	)

	return n
}

// Start begins the RAFT node's participation in the cluster.
// It launches the election timer goroutine and the commit applier.
// This method is non-blocking; goroutines run in the background.
func (n *Node) Start(ctx context.Context) {
	n.logger.Info("starting node", "term", n.currentTerm, "state", n.state)
	go n.runElectionTimer(ctx)
	go n.runCommitApplier(ctx)
}

// Stop signals all goroutines to shut down gracefully.
func (n *Node) Stop() {
	n.logger.Info("stopping node", "term", n.currentTerm, "state", n.state)
	close(n.stopCh)
}

// CommitCh returns the channel that emits committed log entries.
// The server layer reads from this channel to notify the gateway.
func (n *Node) CommitCh() <-chan types.LogEntry {
	return n.commitCh
}

// Status returns a snapshot of the node's current RAFT state.
func (n *Node) Status() types.StatusResponse {
	n.mu.RLock()
	defer n.mu.RUnlock()
	return types.StatusResponse{
		NodeID:      n.id,
		State:       n.state,
		Term:        n.currentTerm,
		LogLength:   n.log.Length(),
		CommitIndex: n.commitIndex,
		LeaderID:    n.leaderID,
	}
}

// ID returns the node's unique identifier.
func (n *Node) ID() string {
	return n.id
}

// IsLeader returns true if this node is currently the RAFT leader.
func (n *Node) IsLeader() bool {
	n.mu.RLock()
	defer n.mu.RUnlock()
	return n.state == types.Leader
}

// becomeFollower transitions the node to FOLLOWER state.
// Must be called with mu held.
func (n *Node) becomeFollower(term int, leaderID string) {
	prevState := n.state
	prevTerm := n.currentTerm
	n.state = types.Follower
	n.currentTerm = term
	n.votedFor = ""
	n.leaderID = leaderID
	if prevState != types.Follower || prevTerm != term {
		n.logger.Info("became follower",
			"term", term,
			"prevState", prevState,
			"prevTerm", prevTerm,
			"leaderId", leaderID,
		)
	}
}

// becomeCandidate transitions the node to CANDIDATE state,
// increments the term, and votes for itself.
// Must be called with mu held.
func (n *Node) becomeCandidate() {
	n.state = types.Candidate
	n.currentTerm++
	n.votedFor = n.id
	n.leaderID = ""
	n.logger.Info("became candidate",
		"term", n.currentTerm,
		"state", n.state,
	)
}

// becomeLeader transitions the node to LEADER state and initializes
// the nextIndex and matchIndex maps for each peer.
// Must be called with mu held.
func (n *Node) becomeLeader() {
	n.state = types.Leader
	n.leaderID = n.id

	// Initialize leader volatile state (§5.3 of RAFT paper)
	lastLogIndex := n.log.LastIndex()
	for _, peer := range n.peers {
		n.nextIndex[peer] = lastLogIndex + 1
		n.matchIndex[peer] = 0
	}

	n.logger.Info("became leader",
		"term", n.currentTerm,
		"state", n.state,
		"logLength", n.log.Length(),
	)
}

// resetElectionTimer sends a non-blocking signal to reset the election timer.
// Called when receiving valid heartbeats or granting votes.
func (n *Node) resetElectionTimer() {
	select {
	case n.resetElectionCh <- struct{}{}:
	default:
		// Channel already has a pending reset signal — that's fine
	}
}

// signalNewEntry sends a non-blocking signal to replication goroutines
// that new entries are available.
func (n *Node) signalNewEntry() {
	select {
	case n.newEntryCh <- struct{}{}:
	default:
	}
}

// runCommitApplier watches for advances in commitIndex and sends
// newly committed entries to the commitCh channel.
func (n *Node) runCommitApplier(ctx context.Context) {
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-n.stopCh:
			return
		case <-ticker.C:
			n.applyCommitted()
		}
	}
}

// applyCommitted sends any entries between lastApplied and commitIndex
// to the commit channel.
func (n *Node) applyCommitted() {
	n.mu.Lock()
	commitIndex := n.commitIndex
	lastApplied := n.lastApplied
	n.mu.Unlock()

	for i := lastApplied + 1; i <= commitIndex; i++ {
		entry, ok := n.log.GetEntry(i)
		if !ok {
			break
		}
		select {
		case n.commitCh <- entry:
		default:
			// Channel full — drop oldest and retry
			<-n.commitCh
			n.commitCh <- entry
		}
		n.mu.Lock()
		n.lastApplied = i
		n.mu.Unlock()
	}
}

// stepDown checks the incoming term and reverts to FOLLOWER if it's higher.
// Returns true if the node stepped down. Must be called with mu held.
func (n *Node) stepDown(remoteTerm int) bool {
	if remoteTerm > n.currentTerm {
		n.becomeFollower(remoteTerm, "")
		return true
	}
	return false
}
