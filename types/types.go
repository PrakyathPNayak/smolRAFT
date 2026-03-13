// Package types defines shared data structures used across all smolRAFT
// services. These types represent the wire format for inter-node RAFT RPCs,
// gateway-to-replica communication, and WebSocket messages to browser clients.
package types

// NodeState represents the current role of a RAFT node in the cluster.
type NodeState string

const (
	// Follower is the passive state; the node replicates entries from the leader.
	Follower NodeState = "FOLLOWER"
	// Candidate is the election state; the node requests votes from peers.
	Candidate NodeState = "CANDIDATE"
	// Leader is the active state; the node handles all writes and sends heartbeats.
	Leader NodeState = "LEADER"
)

// LogEntry represents a single entry in the RAFT replicated log.
// Each entry contains a drawing stroke and the term in which it was created.
type LogEntry struct {
	// Index is the 1-based position of this entry in the log.
	Index int `json:"index"`
	// Term is the leader's term when this entry was created.
	Term int `json:"term"`
	// Stroke is the drawing stroke data associated with this log entry.
	Stroke StrokeEvent `json:"stroke"`
}

// StrokeEvent represents a single drawing stroke from a client.
// It contains the coordinates and visual properties needed to render the stroke.
type StrokeEvent struct {
	// ID is a unique identifier for this stroke (client-generated UUID).
	ID string `json:"id"`
	// Points contains the ordered sequence of coordinates in this stroke.
	Points []Point `json:"points"`
	// Color is the stroke color as a CSS color string (e.g., "#ff0000").
	Color string `json:"color"`
	// Width is the stroke width in pixels.
	Width float64 `json:"width"`
	// UserID identifies the client that created this stroke.
	UserID string `json:"userId"`
}

// Point represents a 2D coordinate on the drawing canvas.
type Point struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
}

// VoteRequest is the RequestVote RPC request as defined in the RAFT paper.
// Sent by candidates to all peers during an election.
type VoteRequest struct {
	// Term is the candidate's current term.
	Term int `json:"term"`
	// CandidateID is the ID of the candidate requesting the vote.
	CandidateID string `json:"candidateId"`
	// LastLogIndex is the index of the candidate's last log entry.
	LastLogIndex int `json:"lastLogIndex"`
	// LastLogTerm is the term of the candidate's last log entry.
	LastLogTerm int `json:"lastLogTerm"`
}

// VoteResponse is the response to a RequestVote RPC.
type VoteResponse struct {
	// Term is the current term of the responding node (for candidate to update itself).
	Term int `json:"term"`
	// VoteGranted is true if the responding node voted for the candidate.
	VoteGranted bool `json:"voteGranted"`
}

// AppendEntriesRequest is the AppendEntries RPC request as defined in the RAFT paper.
// Used by the leader to replicate log entries and as heartbeats (with empty Entries).
type AppendEntriesRequest struct {
	// Term is the leader's current term.
	Term int `json:"term"`
	// LeaderID is the ID of the sending leader (so followers can redirect clients).
	LeaderID string `json:"leaderId"`
	// PrevLogIndex is the index of the log entry immediately preceding the new entries.
	PrevLogIndex int `json:"prevLogIndex"`
	// PrevLogTerm is the term of the entry at PrevLogIndex.
	PrevLogTerm int `json:"prevLogTerm"`
	// Entries are the log entries to append (empty for heartbeats).
	Entries []LogEntry `json:"entries"`
	// LeaderCommit is the leader's current commit index.
	LeaderCommit int `json:"leaderCommit"`
}

// AppendEntriesResponse is the response to an AppendEntries RPC.
type AppendEntriesResponse struct {
	// Term is the current term of the responding node.
	Term int `json:"term"`
	// Success is true if the follower's log matched PrevLogIndex and PrevLogTerm.
	Success bool `json:"success"`
	// ConflictIndex is set when Success is false; it tells the leader where the
	// follower's log diverges, enabling efficient log catch-up.
	ConflictIndex int `json:"conflictIndex,omitempty"`
}

// StatusResponse is returned by GET /status on each replica.
// It provides a snapshot of the node's current RAFT state.
type StatusResponse struct {
	// NodeID is the unique identifier of this node.
	NodeID string `json:"nodeId"`
	// State is the current role: FOLLOWER, CANDIDATE, or LEADER.
	State NodeState `json:"state"`
	// Term is the current RAFT term.
	Term int `json:"term"`
	// LogLength is the total number of entries in the log.
	LogLength int `json:"logLength"`
	// CommitIndex is the index of the highest committed log entry.
	CommitIndex int `json:"commitIndex"`
	// LeaderID is the ID of the current known leader (empty if unknown).
	LeaderID string `json:"leaderId"`
}

// CommitNotification is sent from a replica leader to the gateway when
// a stroke has been committed (replicated to a majority).
type CommitNotification struct {
	// Stroke is the committed drawing stroke.
	Stroke StrokeEvent `json:"stroke"`
	// Index is the log index of the committed entry.
	Index int `json:"index"`
	// Term is the term in which the entry was committed.
	Term int `json:"term"`
}

// WSMessage is the envelope for all WebSocket messages between the gateway
// and browser clients.
type WSMessage struct {
	// Type identifies the message kind: "stroke", "status", "error".
	Type string `json:"type"`
	// Payload is the JSON-encoded message body (type-dependent).
	Payload interface{} `json:"payload"`
}
