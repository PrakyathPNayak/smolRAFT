package raft

import (
	"context"
	"time"

	"github.com/smolRAFT/types"
)

// HandleStroke processes a new drawing stroke from the gateway.
// Only the LEADER can accept strokes. The stroke is appended to the log
// and replication is triggered to all followers.
//
// Returns an error if this node is not the leader.
func (n *Node) HandleStroke(stroke types.StrokeEvent) error {
	n.mu.Lock()
	defer n.mu.Unlock()

	if n.state != types.Leader {
		return ErrNotLeader
	}

	entry := types.LogEntry{
		Term:   n.currentTerm,
		Stroke: stroke,
	}

	index := n.log.Append(entry)
	n.logger.Info("stroke appended to log",
		"term", n.currentTerm,
		"state", n.state,
		"index", index,
		"strokeId", stroke.ID,
	)

	// Signal replication goroutines that new entries are available
	n.signalNewEntry()

	return nil
}

// runHeartbeatLoop sends periodic heartbeats to all followers.
// Runs while the node is LEADER in the given term.
func (n *Node) runHeartbeatLoop(ctx context.Context) {
	ticker := time.NewTicker(HeartbeatInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-n.stopCh:
			return
		case <-ticker.C:
			n.mu.RLock()
			isLeader := n.state == types.Leader
			n.mu.RUnlock()

			if !isLeader {
				return
			}

			n.sendHeartbeats(ctx)
		}
	}
}

// sendHeartbeats sends AppendEntries RPCs (possibly with entries) to all peers.
func (n *Node) sendHeartbeats(ctx context.Context) {
	n.mu.RLock()
	if n.state != types.Leader {
		n.mu.RUnlock()
		return
	}
	peers := make([]string, len(n.peers))
	copy(peers, n.peers)
	n.mu.RUnlock()

	for _, peer := range peers {
		go func(peerAddr string) {
			n.sendAppendEntries(ctx, peerAddr)
		}(peer)
	}
}

// runReplicationLoop is a per-peer goroutine that watches for new log entries
// and sends them to the peer. It exits when the node is no longer the leader
// for the given term.
func (n *Node) runReplicationLoop(ctx context.Context, peerAddr string, leaderTerm int) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-n.stopCh:
			return
		case <-n.newEntryCh:
			n.mu.RLock()
			stillLeader := n.state == types.Leader && n.currentTerm == leaderTerm
			n.mu.RUnlock()

			if !stillLeader {
				return
			}

			n.sendAppendEntries(ctx, peerAddr)
		}
	}
}

// sendAppendEntries sends an AppendEntries RPC to a single peer.
// It reads the peer's nextIndex to determine which entries to send,
// and handles the response (updating nextIndex/matchIndex).
func (n *Node) sendAppendEntries(ctx context.Context, peerAddr string) {
	n.mu.RLock()
	if n.state != types.Leader {
		n.mu.RUnlock()
		return
	}

	nextIdx, ok := n.nextIndex[peerAddr]
	if !ok {
		n.mu.RUnlock()
		return
	}

	prevLogIndex := nextIdx - 1
	prevLogTerm := n.log.Term(prevLogIndex)
	entries := n.log.GetFrom(nextIdx)
	term := n.currentTerm
	leaderID := n.id
	commitIndex := n.commitIndex
	n.mu.RUnlock()

	req := types.AppendEntriesRequest{
		Term:         term,
		LeaderID:     leaderID,
		PrevLogIndex: prevLogIndex,
		PrevLogTerm:  prevLogTerm,
		Entries:      entries,
		LeaderCommit: commitIndex,
	}

	rpcCtx, cancel := context.WithTimeout(ctx, RPCTimeout)
	defer cancel()

	resp, err := n.rpc.AppendEntries(rpcCtx, peerAddr, req)
	if err != nil {
		n.logger.Debug("append entries failed",
			"term", term,
			"state", types.Leader,
			"peer", peerAddr,
			"error", err,
		)
		return
	}

	n.mu.Lock()
	defer n.mu.Unlock()

	// Check if we received a higher term
	if n.stepDown(resp.Term) {
		return
	}

	// Verify we're still leader in the same term
	if n.state != types.Leader || n.currentTerm != term {
		return
	}

	if resp.Success {
		// Update nextIndex and matchIndex for this peer
		if len(entries) > 0 {
			newMatchIndex := entries[len(entries)-1].Index
			n.nextIndex[peerAddr] = newMatchIndex + 1
			n.matchIndex[peerAddr] = newMatchIndex

			n.logger.Debug("replication success",
				"term", n.currentTerm,
				"state", n.state,
				"peer", peerAddr,
				"matchIndex", newMatchIndex,
			)
		}

		// Try to advance commit index
		n.advanceCommitIndex()
	} else {
		// Log inconsistency — decrement nextIndex using conflict hint
		if resp.ConflictIndex > 0 {
			n.nextIndex[peerAddr] = resp.ConflictIndex
		} else if n.nextIndex[peerAddr] > 1 {
			n.nextIndex[peerAddr]--
		}

		n.logger.Debug("replication conflict, backing up",
			"term", n.currentTerm,
			"state", n.state,
			"peer", peerAddr,
			"newNextIndex", n.nextIndex[peerAddr],
			"conflictIndex", resp.ConflictIndex,
		)
	}
}

// advanceCommitIndex checks if any un-committed entries have been replicated
// to a majority. If so, it advances the commit index.
// Must be called with mu held.
func (n *Node) advanceCommitIndex() {
	// Only commit entries from the current term (§5.4.2 of RAFT paper)
	for idx := n.commitIndex + 1; idx <= n.log.LastIndex(); idx++ {
		if n.log.Term(idx) != n.currentTerm {
			continue
		}

		// Count replicas that have this entry (including self)
		replicaCount := 1 // self
		for _, peer := range n.peers {
			if n.matchIndex[peer] >= idx {
				replicaCount++
			}
		}

		majority := (len(n.peers)+1)/2 + 1
		if replicaCount >= majority {
			n.commitIndex = idx
			n.logger.Info("entry committed",
				"term", n.currentTerm,
				"state", n.state,
				"commitIndex", n.commitIndex,
			)
		}
	}
}

// HandleAppendEntries processes an incoming AppendEntries RPC from the leader.
// It validates the leader's term, checks log consistency, appends new entries,
// and advances the commit index.
//
// Returns the response to send back to the leader.
func (n *Node) HandleAppendEntries(req types.AppendEntriesRequest) types.AppendEntriesResponse {
	n.mu.Lock()
	defer n.mu.Unlock()

	resp := types.AppendEntriesResponse{
		Term:    n.currentTerm,
		Success: false,
	}

	// Rule: higher term always supersedes
	n.stepDown(req.Term)

	// Reject if leader's term is behind
	if req.Term < n.currentTerm {
		resp.Term = n.currentTerm
		return resp
	}

	// Valid leader for this term — reset election timer
	n.resetElectionTimer()
	n.leaderID = req.LeaderID

	// If we're a candidate and receive a valid AppendEntries, step down
	if n.state == types.Candidate {
		n.becomeFollower(req.Term, req.LeaderID)
	}

	// Ensure we're a follower with matching term
	if n.state != types.Follower {
		n.becomeFollower(req.Term, req.LeaderID)
	}
	n.currentTerm = req.Term

	// Log consistency check (§5.3 of RAFT paper)
	if req.PrevLogIndex > 0 {
		prevEntry, exists := n.log.GetEntry(req.PrevLogIndex)
		if !exists {
			// We don't have the entry at prevLogIndex
			resp.ConflictIndex = n.log.LastIndex() + 1
			n.logger.Debug("append entries rejected: missing prev entry",
				"term", n.currentTerm,
				"state", n.state,
				"prevLogIndex", req.PrevLogIndex,
				"ourLastIndex", n.log.LastIndex(),
			)
			return resp
		}
		if prevEntry.Term != req.PrevLogTerm {
			// Term mismatch at prevLogIndex
			resp.ConflictIndex = req.PrevLogIndex
			n.logger.Debug("append entries rejected: term mismatch",
				"term", n.currentTerm,
				"state", n.state,
				"prevLogIndex", req.PrevLogIndex,
				"prevLogTerm", req.PrevLogTerm,
				"ourTerm", prevEntry.Term,
			)
			return resp
		}
	}

	// Append new entries (handles conflicts internally)
	if len(req.Entries) > 0 {
		n.log.AppendEntries(req.Entries, n.commitIndex)
		n.logger.Debug("entries appended",
			"term", n.currentTerm,
			"state", n.state,
			"count", len(req.Entries),
			"lastIndex", n.log.LastIndex(),
		)
	}

	// Advance commit index
	if req.LeaderCommit > n.commitIndex {
		lastNewIndex := n.log.LastIndex()
		if req.LeaderCommit < lastNewIndex {
			n.commitIndex = req.LeaderCommit
		} else {
			n.commitIndex = lastNewIndex
		}
	}

	resp.Success = true
	resp.Term = n.currentTerm
	return resp
}

// GetSyncLog returns all committed entries from the given index onward.
// Used by the catch-up protocol.
func (n *Node) GetSyncLog(fromIndex int) []types.LogEntry {
	n.mu.RLock()
	commitIndex := n.commitIndex
	n.mu.RUnlock()

	entries := n.log.GetFrom(fromIndex)
	// Only return committed entries
	result := make([]types.LogEntry, 0, len(entries))
	for _, entry := range entries {
		if entry.Index <= commitIndex {
			result = append(result, entry)
		}
	}
	return result
}
