package gateway

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/smolRAFT/gateway/internal/leader"
	"github.com/smolRAFT/types"
)

func TestServerRoutes(t *testing.T) {
	leaderSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r
		_ = json.NewEncoder(w).Encode(types.StatusResponse{NodeID: "n1", State: types.Leader, LeaderID: "n1"})
	}))
	defer leaderSrv.Close()

	tkr := leader.NewTracker([]string{strings.TrimPrefix(leaderSrv.URL, "http://")}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	tkr.PollOnce(context.Background())

	hub := NewHub(slog.New(slog.NewTextHandler(io.Discard, nil)))
	h := NewHandler(hub, tkr, slog.New(slog.NewTextHandler(io.Discard, nil)))
	s := NewServer("0", h, slog.New(slog.NewTextHandler(io.Discard, nil)))

	ts := httptest.NewServer(s.httpServer.Handler)
	defer ts.Close()

	for _, path := range []string{"/health", "/status", "/leader", "/metrics"} {
		resp, err := http.Get(ts.URL + path)
		if err != nil {
			t.Fatalf("GET %s: %v", path, err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("GET %s status = %d, want 200", path, resp.StatusCode)
		}
	}
}
