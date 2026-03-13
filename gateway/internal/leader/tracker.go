// Package leader implements leader discovery for the gateway.
// It periodically polls all replicas to find the current RAFT leader
// and caches the result for fast routing of write requests.
package leader

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/smolRAFT/types"
)

const (
	// PollInterval is how often we poll replicas for leader status.
	PollInterval = 2 * time.Second
	// PollTimeout is the HTTP timeout for each status poll.
	PollTimeout = 500 * time.Millisecond
)

// Tracker discovers and caches the current RAFT leader address.
// It polls all known replicas at a fixed interval and updates the
// cached leader whenever a change is detected.
type Tracker struct {
	mu           sync.RWMutex
	leaderAddr   string
	replicaAddrs []string
	client       *http.Client
	logger       *slog.Logger
}

// NewTracker creates a new Tracker that polls the given replica addresses.
func NewTracker(replicaAddrs []string, logger *slog.Logger) *Tracker {
	return &Tracker{
		replicaAddrs: replicaAddrs,
		client: &http.Client{
			Timeout: PollTimeout,
		},
		logger: logger.With("component", "tracker"),
	}
}

// Start begins the leader polling loop. Blocks until ctx is cancelled.
func (t *Tracker) Start(ctx context.Context) {
	t.logger.Info("starting leader tracker", "replicas", t.replicaAddrs)
	// Do an immediate poll
	t.PollOnce(ctx)

	ticker := time.NewTicker(PollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			t.PollOnce(ctx)
		}
	}
}

// PollOnce performs one leader discovery cycle immediately.
// This is useful when a request fails due to stale leader information.
func (t *Tracker) PollOnce(ctx context.Context) {
	t.poll(ctx)
}

// LeaderAddr returns the cached leader address, or "" if unknown.
func (t *Tracker) LeaderAddr() string {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.leaderAddr
}

// poll queries all replicas and updates the cached leader address.
func (t *Tracker) poll(ctx context.Context) {
	for _, addr := range t.replicaAddrs {
		status, err := t.fetchStatus(ctx, addr)
		if err != nil {
			continue
		}
		if status.State == types.Leader {
			t.setLeader(addr)
			return
		}
		// A follower that knows its leader can tell us
		if status.LeaderID != "" {
			// Find the address of the leader by matching node IDs
			for _, rAddr := range t.replicaAddrs {
				rStatus, err := t.fetchStatus(ctx, rAddr)
				if err != nil {
					continue
				}
				if rStatus.NodeID == status.LeaderID && rStatus.State == types.Leader {
					t.setLeader(rAddr)
					return
				}
			}
		}
	}
	// No leader found — clear the cache
	t.setLeader("")
}

func (t *Tracker) setLeader(addr string) {
	t.mu.Lock()
	prev := t.leaderAddr
	t.leaderAddr = addr
	t.mu.Unlock()
	if prev != addr {
		if addr == "" {
			t.logger.Warn("leader unknown")
		} else {
			t.logger.Info("leader discovered", "addr", addr)
		}
	}
}

func (t *Tracker) fetchStatus(ctx context.Context, addr string) (types.StatusResponse, error) {
	url := fmt.Sprintf("http://%s/status", addr)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return types.StatusResponse{}, err
	}
	resp, err := t.client.Do(req)
	if err != nil {
		return types.StatusResponse{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return types.StatusResponse{}, fmt.Errorf("status %d from %s", resp.StatusCode, addr)
	}
	var status types.StatusResponse
	if err := json.NewDecoder(resp.Body).Decode(&status); err != nil {
		return types.StatusResponse{}, err
	}
	return status, nil
}
