# smolRAFT — System Architecture

## Purpose

smolRAFT is a distributed real-time collaborative drawing board that demonstrates the RAFT consensus algorithm in a tangible, visual way. Users draw strokes in their browsers; those strokes are replicated across a 3-node cluster via a simplified RAFT protocol before being broadcast to all connected clients. The system tolerates any single node failure with zero downtime.

## System Overview

```
                    ┌─────────────────────────┐
                    │     Browser Clients      │
                    │  (Canvas + WebSocket)    │
                    └───────────┬──────────────┘
                                │ ws://gateway:8080/ws
                    ┌───────────▼──────────────┐
                    │        Gateway            │
                    │                           │
                    │  ┌─────────┐ ┌─────────┐ │
                    │  │   Hub   │ │ Leader  │ │
                    │  │(WS mgr) │ │ Tracker │ │
                    │  └─────────┘ └─────────┘ │
                    └──┬────────┬────────┬─────┘
                       │        │        │
          POST /stroke │        │        │ POST /stroke
         (to leader)   │        │        │ (to leader)
                       ▼        ▼        ▼
              ┌─────────┐ ┌─────────┐ ┌─────────┐
              │Replica 1│ │Replica 2│ │Replica 3│
              │  :9001  │ │  :9002  │ │  :9003  │
              │         │ │         │ │         │
              │ RaftNode│ │ RaftNode│ │ RaftNode│
              │  ┌────┐ │ │  ┌────┐ │ │  ┌────┐ │
              │  │Log │ │ │  │Log │ │ │  │Log │ │
              │  └────┘ │ │  └────┘ │ │  └────┘ │
              └────┬────┘ └────┬────┘ └────┬────┘
                   │           │           │
                   └───────────┼───────────┘
                     RAFT RPCs │
                  (AppendEntries, RequestVote, Heartbeat)
```

## Component Map

### `/types` — Shared Type Definitions
- `types.go` — LogEntry, VoteRequest/Response, AppendEntriesRequest/Response, StrokeEvent, StatusResponse
- Used as a Go module dependency by both `gateway` and `replica`

### `/replica` — RAFT Consensus Node
- `cmd/main.go` — Entry point: config loading, wiring, startup
- `internal/raft/`
  - `node.go` — RaftNode struct, state machine, initialization
  - `election.go` — Election timer, candidate promotion, vote collection
  - `replication.go` — AppendEntries sender, log replication, commit advancement
  - `log.go` — Append-only log data structure, thread-safe operations
  - `rpc.go` — HTTP RPC client helpers with timeouts
- `internal/server/`
  - `server.go` — HTTP server setup, graceful shutdown
  - `handler.go` — HTTP handlers bridging HTTP ↔ RaftNode

### `/gateway` — WebSocket Hub & Leader Router
- `cmd/main.go` — Entry point: config loading, wiring, startup
- `internal/gateway/`
  - `hub.go` — WebSocket client registry, broadcast fan-out
  - `handler.go` — HTTP + WebSocket upgrade handlers
  - `server.go` — Server setup, graceful shutdown
- `internal/leader/`
  - `tracker.go` — Leader discovery via polling, re-routing on failover

### `/frontend` — Browser UI
- `index.html` — Canvas element, HUD overlay
- `style.css` — Industrial/terminal aesthetic
- `app.js` — WebSocket client, canvas rendering, reconnect logic

## RAFT State Machine

```
                          starts as
                    ┌───────────────────┐
                    │                   │
                    ▼                   │
              ┌──────────┐             │
              │ FOLLOWER │─────────────┘ (on startup)
              └──────────┘
                    │
                    │ election timeout
                    │ (no heartbeat received
                    │  for 500-800ms)
                    ▼
              ┌──────────┐
         ┌───►│CANDIDATE │◄───┐
         │    └──────────┘    │
         │         │          │
         │         │ wins     │ split vote /
         │         │ majority │ election timeout
         │         ▼          │
         │    ┌──────────┐    │
         │    │  LEADER  │────┘ (discovers higher term)
         │    └──────────┘
         │         │
         │         │ discovers higher term
         └─────────┘ (reverts to FOLLOWER)

  ANY STATE: on receiving RPC with term > currentTerm
             → set currentTerm = rpc.term
             → revert to FOLLOWER
             → clear votedFor
```

## Data Flow — Happy Path

### Stroke Commit Sequence
```
 Client        Gateway         Leader(R1)      Follower(R2)    Follower(R3)
   │              │               │                │               │
   │──stroke─────►│               │                │               │
   │              │──POST /stroke►│                │               │
   │              │               │──append to log │               │
   │              │               │                │               │
   │              │               │══AppendEntries═╪══════════════►│
   │              │               │══AppendEntries═╪►              │
   │              │               │                │               │
   │              │               │◄═══ACK════════╪════════════════│
   │              │               │◄═══ACK════════╪═               │
   │              │               │                │               │
   │              │               │──commit (2/3)  │               │
   │              │               │──callback─────►│               │
   │              │◄──committed───│  (or next AE)  │               │
   │              │               │                │               │
   │◄──broadcast──│               │                │               │
   │  (all WS     │               │                │               │
   │   clients)   │               │                │               │
```

## Concurrency Model

### Replica Goroutines
| Goroutine            | Purpose                                    | Lifecycle          |
|----------------------|--------------------------------------------|--------------------|
| `runElectionTimer`   | Fires election on timeout; reset by HBs    | Node lifetime      |
| `runHeartbeatLoop`   | Leader sends periodic heartbeats            | While LEADER       |
| `replicateToFollower`| Per-peer AppendEntries sender               | While LEADER       |
| HTTP server          | Handles incoming RPCs                       | Node lifetime      |

### Synchronization
- `sync.RWMutex` on `RaftNode` protects: `currentTerm`, `votedFor`, `state`, `log`, `commitIndex`, `lastApplied`, `nextIndex`, `matchIndex`
- Election timer reset via channel signal (avoids timer races)
- Replication triggers via channel (leader notifies replication goroutines of new entries)

### Gateway Goroutines
| Goroutine         | Purpose                                | Lifecycle          |
|-------------------|----------------------------------------|--------------------|
| `Hub.run`         | Central select loop for register/unregister/broadcast | Gateway lifetime |
| Per-client writer | Reads from client's send channel, writes to WS | Client connection  |
| Per-client reader | Reads from WS, forwards to gateway      | Client connection  |
| `Tracker.run`     | Polls replica /status to discover leader | Gateway lifetime   |

## Configuration

### Replica Environment Variables
| Variable         | Type   | Default | Required | Description                    |
|-----------------|--------|---------|----------|--------------------------------|
| `NODE_ID`       | string | —       | yes      | Unique node identifier         |
| `PORT`          | string | `9001`  | no       | HTTP server listen port        |
| `PEER_ADDRS`    | string | —       | yes      | Comma-separated peer addresses |
| `GATEWAY_ADDR`  | string | —       | yes      | Gateway callback address       |

### Gateway Environment Variables
| Variable         | Type   | Default | Required | Description                    |
|-----------------|--------|---------|----------|--------------------------------|
| `PORT`          | string | `8080`  | no       | HTTP/WS server listen port     |
| `REPLICA_ADDRS` | string | —       | yes      | Comma-separated replica addrs  |

## Failure Handling

| Failure                      | Detection                       | Recovery                                      |
|-----------------------------|---------------------------------|-----------------------------------------------|
| Leader crash                | Election timeout (500-800ms)    | New election; gateway re-discovers leader     |
| Follower crash              | AppendEntries timeout (100ms)   | Leader continues with remaining quorum        |
| Follower restart (stale)    | prevLogIndex mismatch           | Leader decrements nextIndex, retransmits      |
| Gateway → leader timeout    | HTTP timeout                    | Gateway polls /status, finds new leader       |
| Network partition (minority)| No heartbeats received          | Minority starts elections but cannot win       |
| Split vote                  | No majority in election         | Term increments, new election after timeout   |

## Known Limitations

1. **In-memory only** — No WAL or persistent storage; full cluster restart loses all state
2. **No log compaction** — Log grows unbounded; suitable for demo workloads only
3. **No snapshots** — Catch-up requires retransmitting full log suffix
4. **Fixed membership** — No dynamic cluster reconfiguration (no AddServer/RemoveServer)
5. **No pre-vote** — A partitioned node that rejoins with a high term can disrupt the cluster
6. **No read consistency** — Reads from followers may return stale data (acceptable for drawing board)
7. **No TLS** — All inter-node communication is plaintext
8. **Single gateway** — Gateway is a SPOF (could be addressed with multiple gateways + load balancer)
