package raft

import (
	"context"
	"math/rand/v2"
	"sync"
	"time"

	"github.com/smolRAFT/types"
)

// runElectionTimer is a long-running goroutine that manages the RAFT
// election timeout. When the timer fires without being reset (by a valid
// heartbeat or vote grant), the node transitions to CANDIDATE and starts
// an election. This goroutine runs for the lifetime of the node.
func (n *Node) runElectionTimer(ctx context.Context) {
	timer := time.NewTimer(randomElectionTimeout())
	defer timer.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-n.stopCh:
			return

		case <-n.resetElectionCh:
			// Reset the timer — a valid heartbeat or vote grant was received
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			timer.Reset(randomElectionTimeout())

		case <-timer.C:
			// Election timeout fired — check if we should start an election
			n.mu.RLock()
			state := n.state
			n.mu.RUnlock()

			if state == types.Leader {
				// Leaders don't start elections; reset and continue
				timer.Reset(randomElectionTimeout())
				continue
			}

			// Start election
			n.startElection(ctx)
			timer.Reset(randomElectionTimeout())
		}
	}
}

// startElection transitions to CANDIDATE, votes for self, and requests
// votes from all peers. If a majority is obtained, the node becomes LEADER
// and starts sending heartbeats.
func (n *Node) startElection(ctx context.Context) {
	n.mu.Lock()
	n.becomeCandidate()
	term := n.currentTerm
	candidateID := n.id
	lastLogIndex := n.log.LastIndex()
	lastLogTerm := n.log.LastTerm()
	peers := make([]string, len(n.peers))
	copy(peers, n.peers)
	n.mu.Unlock()

	req := types.VoteRequest{
		Term:         term,
		CandidateID:  candidateID,
		LastLogIndex: lastLogIndex,
		LastLogTerm:  lastLogTerm,
	}

	// We already voted for ourselves
	votes := 1
	total := len(peers) + 1 // total cluster size
	majority := total/2 + 1

	var mu sync.Mutex
	var wg sync.WaitGroup

	for _, peer := range peers {
		wg.Add(1)
		go func(peerAddr string) {
			defer wg.Done()
			n.requestVoteFromPeer(ctx, peerAddr, req, &votes, &mu, term)
		}(peer)
	}

	// Wait for all vote responses (with timeout via context)
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
	case <-ctx.Done():
		return
	case <-n.stopCh:
		return
	}

	mu.Lock()
	finalVotes := votes
	mu.Unlock()

	// Check if we won
	n.mu.Lock()
	defer n.mu.Unlock()

	// Verify we're still a candidate in the same term (could have stepped down)
	if n.state != types.Candidate || n.currentTerm != term {
		n.logger.Debug("election result ignored: state changed",
			"term", n.currentTerm,
			"state", n.state,
			"electionTerm", term,
		)
		return
	}

	if finalVotes >= majority {
		n.becomeLeader()
		// Start heartbeat and replication goroutines
		go n.runHeartbeatLoop(ctx)
		for _, peer := range n.peers {
			go n.runReplicationLoop(ctx, peer, term)
		}
		// Send immediate heartbeat to establish authority
		go n.sendHeartbeats(ctx)
	} else {
		n.logger.Warn("election failed: no majority",
			"term", term,
			"state", n.state,
			"votes", finalVotes,
			"needed", majority,
		)
	}
}

// requestVoteFromPeer sends a RequestVote RPC to a single peer and processes
// the response. Increments the vote counter on success.
func (n *Node) requestVoteFromPeer(
	ctx context.Context,
	peerAddr string,
	req types.VoteRequest,
	votes *int,
	mu *sync.Mutex,
	electionTerm int,
) {
	rpcCtx, cancel := context.WithTimeout(ctx, RPCTimeout)
	defer cancel()

	resp, err := n.rpc.RequestVote(rpcCtx, peerAddr, req)
	if err != nil {
		n.logger.Warn("vote request failed",
			"term", req.Term,
			"state", types.Candidate,
			"peer", peerAddr,
			"error", err,
		)
		return
	}

	// Check if remote has higher term
	n.mu.Lock()
	if resp.Term > n.currentTerm {
		n.becomeFollower(resp.Term, "")
		n.mu.Unlock()
		return
	}
	// Verify we're still in the same election
	if n.currentTerm != electionTerm || n.state != types.Candidate {
		n.mu.Unlock()
		return
	}
	n.mu.Unlock()

	if resp.VoteGranted {
		mu.Lock()
		*votes++
		n.logger.Debug("vote received",
			"term", req.Term,
			"state", types.Candidate,
			"peer", peerAddr,
			"totalVotes", *votes,
		)
		mu.Unlock()
	}
}

// HandleVoteRequest processes an incoming RequestVote RPC from a candidate.
// It grants the vote if:
// 1. The candidate's term is >= our current term
// 2. We haven't voted for someone else in this term
// 3. The candidate's log is at least as up-to-date as ours
//
// Returns the response to send back to the candidate.
func (n *Node) HandleVoteRequest(req types.VoteRequest) types.VoteResponse {
	n.mu.Lock()
	defer n.mu.Unlock()

	resp := types.VoteResponse{
		Term:        n.currentTerm,
		VoteGranted: false,
	}

	// Rule: higher term always supersedes
	n.stepDown(req.Term)

	// Reject if candidate's term is behind
	if req.Term < n.currentTerm {
		resp.Term = n.currentTerm
		n.logger.Debug("vote rejected: stale term",
			"term", n.currentTerm,
			"state", n.state,
			"candidateId", req.CandidateID,
			"candidateTerm", req.Term,
		)
		return resp
	}

	// Check if we can vote for this candidate
	canVote := n.votedFor == "" || n.votedFor == req.CandidateID

	// Check log up-to-dateness (§5.4.1 of RAFT paper):
	// A log is more up-to-date if its last entry has a higher term,
	// or if terms are equal and the log is longer.
	ourLastTerm := n.log.LastTerm()
	ourLastIndex := n.log.LastIndex()
	logOK := req.LastLogTerm > ourLastTerm ||
		(req.LastLogTerm == ourLastTerm && req.LastLogIndex >= ourLastIndex)

	if canVote && logOK {
		n.votedFor = req.CandidateID
		n.currentTerm = req.Term
		resp.VoteGranted = true
		resp.Term = n.currentTerm
		n.resetElectionTimer()

		n.logger.Info("vote granted",
			"term", n.currentTerm,
			"state", n.state,
			"votedFor", req.CandidateID,
		)
	} else {
		n.logger.Debug("vote rejected",
			"term", n.currentTerm,
			"state", n.state,
			"candidateId", req.CandidateID,
			"canVote", canVote,
			"logOK", logOK,
			"votedFor", n.votedFor,
		)
	}

	return resp
}

// randomElectionTimeout returns a random duration between
// ElectionTimeoutMin and ElectionTimeoutMax.
func randomElectionTimeout() time.Duration {
	spread := ElectionTimeoutMax - ElectionTimeoutMin
	return ElectionTimeoutMin + time.Duration(rand.Int64N(int64(spread)))
}
