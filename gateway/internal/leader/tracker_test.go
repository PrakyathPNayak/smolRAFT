package leader

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/smolRAFT/types"
)

func TestTrackerPollOnce_DiscoversLeader(t *testing.T) {
	leaderSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r
		w.Header().Set("Content-Type", "application/json")
		_ = jsonEncode(w, types.StatusResponse{NodeID: "node-1", State: types.Leader, LeaderID: "node-1"})
	}))
	defer leaderSrv.Close()

	followerSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r
		w.Header().Set("Content-Type", "application/json")
		_ = jsonEncode(w, types.StatusResponse{NodeID: "node-2", State: types.Follower, LeaderID: "node-1"})
	}))
	defer followerSrv.Close()

	tkr := NewTracker([]string{
		strings.TrimPrefix(followerSrv.URL, "http://"),
		strings.TrimPrefix(leaderSrv.URL, "http://"),
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))

	tkr.PollOnce(context.Background())
	if got := tkr.LeaderAddr(); got == "" {
		t.Fatal("expected discovered leader address, got empty")
	}
}

func TestTrackerPollOnce_NoLeader(t *testing.T) {
	followerSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r
		w.Header().Set("Content-Type", "application/json")
		_ = jsonEncode(w, types.StatusResponse{NodeID: "node-2", State: types.Follower, LeaderID: ""})
	}))
	defer followerSrv.Close()

	tkr := NewTracker([]string{
		strings.TrimPrefix(followerSrv.URL, "http://"),
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))

	tkr.PollOnce(context.Background())
	if got := tkr.LeaderAddr(); got != "" {
		t.Fatalf("expected empty leader address, got %q", got)
	}
}

func jsonEncode(w http.ResponseWriter, v interface{}) error {
	return json.NewEncoder(w).Encode(v)
}
