package raft

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/smolRAFT/types"
)

// ErrNotLeader is returned when a write operation is attempted on a non-leader node.
var ErrNotLeader = errors.New("not the current leader")

// RPCClient defines the interface for RAFT inter-node communication.
// This interface enables unit testing with mock RPC clients.
type RPCClient interface {
	// RequestVote sends a RequestVote RPC to the given peer address.
	RequestVote(ctx context.Context, addr string, req types.VoteRequest) (types.VoteResponse, error)

	// AppendEntries sends an AppendEntries RPC to the given peer address.
	AppendEntries(ctx context.Context, addr string, req types.AppendEntriesRequest) (types.AppendEntriesResponse, error)

	// NotifyCommit sends a commit notification to the gateway.
	NotifyCommit(ctx context.Context, addr string, notification types.CommitNotification) error

	// FetchSyncLog retrieves committed log entries from the given index on a peer.
	// Used by the leader to bulk catch-up a lagging follower.
	FetchSyncLog(ctx context.Context, addr string, fromIndex int) ([]types.LogEntry, error)
}

// HTTPRPCClient implements RPCClient using HTTP/JSON.
// It provides the production transport for RAFT inter-node communication.
type HTTPRPCClient struct {
	client *http.Client
}

// NewHTTPRPCClient creates a new HTTP-based RPC client.
// The client does not set a global timeout; per-call timeouts are
// managed via context.Context.
func NewHTTPRPCClient() *HTTPRPCClient {
	return &HTTPRPCClient{
		client: &http.Client{},
	}
}

// RequestVote sends a RequestVote RPC to the given peer via HTTP POST.
func (c *HTTPRPCClient) RequestVote(ctx context.Context, addr string, req types.VoteRequest) (types.VoteResponse, error) {
	var resp types.VoteResponse
	err := c.doPost(ctx, fmt.Sprintf("http://%s/request-vote", addr), req, &resp)
	return resp, err
}

// AppendEntries sends an AppendEntries RPC to the given peer via HTTP POST.
func (c *HTTPRPCClient) AppendEntries(ctx context.Context, addr string, req types.AppendEntriesRequest) (types.AppendEntriesResponse, error) {
	var resp types.AppendEntriesResponse
	err := c.doPost(ctx, fmt.Sprintf("http://%s/append-entries", addr), req, &resp)
	return resp, err
}

// NotifyCommit sends a commit notification to the gateway via HTTP POST.
func (c *HTTPRPCClient) NotifyCommit(ctx context.Context, addr string, notification types.CommitNotification) error {
	return c.doPost(ctx, fmt.Sprintf("http://%s/commit", addr), notification, nil)
}

// FetchSyncLog retrieves committed log entries from addr starting at fromIndex.
func (c *HTTPRPCClient) FetchSyncLog(ctx context.Context, addr string, fromIndex int) ([]types.LogEntry, error) {
	url := fmt.Sprintf("http://%s/sync-log?from=%d", addr, fromIndex)
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("create request to %s: %w", url, err)
	}
	httpResp, err := c.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("send request to %s: %w", url, err)
	}
	defer httpResp.Body.Close()
	if httpResp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status from %s: %d", url, httpResp.StatusCode)
	}
	var entries []types.LogEntry
	if err := json.NewDecoder(httpResp.Body).Decode(&entries); err != nil {
		return nil, fmt.Errorf("decode response from %s: %w", url, err)
	}
	return entries, nil
}

// doPost performs an HTTP POST with JSON request/response bodies.
// The context controls the request lifetime (deadline/cancellation).
func (c *HTTPRPCClient) doPost(ctx context.Context, url string, reqBody interface{}, respBody interface{}) error {
	body, err := json.Marshal(reqBody)
	if err != nil {
		return fmt.Errorf("marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create request to %s: %w", url, err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	httpResp, err := c.client.Do(httpReq)
	if err != nil {
		return fmt.Errorf("send request to %s: %w", url, err)
	}
	defer httpResp.Body.Close()

	if httpResp.StatusCode != http.StatusOK {
		return fmt.Errorf("unexpected status from %s: %d", url, httpResp.StatusCode)
	}

	if respBody != nil {
		if err := json.NewDecoder(httpResp.Body).Decode(respBody); err != nil {
			return fmt.Errorf("decode response from %s: %w", url, err)
		}
	}

	return nil
}
