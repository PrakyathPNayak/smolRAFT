# Gateway Architecture

## Purpose

The gateway is the entrypoint for browser clients. It accepts WebSocket connections,
forwards incoming strokes to the current RAFT leader, and broadcasts committed
strokes back to all connected clients.

## Components

- `internal/gateway/hub.go`
  - Manages WS client lifecycle (`register`, `unregister`) and fan-out broadcasts.
  - Backpressure handling: slow clients are dropped.
- `internal/gateway/handler.go`
  - `GET /ws`: upgrades to WebSocket and runs read/write pumps.
  - `POST /commit`: receives leader commit callbacks and broadcasts to clients.
  - `GET /health`, `GET /status`, `GET /leader`, `GET /metrics`.
  - Retries leader forward once after refreshing leader tracker.
- `internal/gateway/server.go`
  - HTTP server wiring and graceful shutdown.
  - Structured request logging middleware.
- `internal/leader/tracker.go`
  - Polls replica `/status` endpoints to discover and cache current leader.

## Data Flow

1. Client sends stroke JSON over WebSocket.
2. Handler forwards stroke to `http://<leader>/stroke`.
3. Leader commits stroke after majority replication.
4. Leader calls `POST /commit` on gateway.
5. Gateway hub broadcasts committed stroke to all WS clients.

## Failure Handling

- If leader is unknown, forward fails fast and client gets error message.
- If leader changes mid-flight (`503`), gateway refreshes leader once and retries.
- If a WS client cannot keep up, it is disconnected to protect overall throughput.

## Observability

- JSON structured logs via `slog`.
- Request logs include method, path, status, and duration.
- `/metrics` exposes plaintext demo metrics:
  - `smolraft_gateway_connected_clients`
