# smolRAFT — Distributed Real-Time Drawing Board with Mini-RAFT Consensus

A fault-tolerant, real-time collaborative whiteboard backed by a 3-node RAFT consensus cluster. Users draw in a browser; strokes are replicated across all replicas via a simplified RAFT protocol. The system survives any single node failure with zero downtime.

## Architecture

```
┌─────────────────────────────────────────────────────────────────┐
│                        Browser Clients                          │
│   ┌──────────┐   ┌──────────┐   ┌──────────┐                   │
│   │ Canvas 1 │   │ Canvas 2 │   │ Canvas 3 │   ...             │
│   └────┬─────┘   └────┬─────┘   └────┬─────┘                   │
│        │              │              │                          │
│        └──────────────┼──────────────┘                          │
│                       │ WebSocket                               │
└───────────────────────┼─────────────────────────────────────────┘
                        │
              ┌─────────▼──────────┐
              │      Gateway       │
              │  :8080 (WS + HTTP) │
              │                    │
              │  • WS Hub          │
              │  • Leader Tracker  │
              │  • Stroke Router   │
              └───┬────┬────┬──────┘
                  │    │    │  HTTP/JSON RPCs
        ┌─────────┘    │    └──────────┐
        ▼              ▼               ▼
┌──────────────┐ ┌──────────────┐ ┌──────────────┐
│  Replica 1   │ │  Replica 2   │ │  Replica 3   │
│  :9001       │ │  :9002       │ │  :9003       │
│              │ │              │ │              │
│  Mini-RAFT   │ │  Mini-RAFT   │ │  Mini-RAFT   │
│  Node        │◄┼─────────────►│  Node        │
│              │ │  AppendEntries│ │              │
│  State:      │ │  RequestVote │ │  State:      │
│  FOLLOWER /  │ │  Heartbeat   │ │  FOLLOWER /  │
│  CANDIDATE / │ │              │ │  CANDIDATE / │
│  LEADER      │ │              │ │  LEADER      │
└──────────────┘ └──────────────┘ └──────────────┘
       ◄─────── RAFT Consensus ────────►
```

## Quick Start

### Prerequisites
- Docker & docker-compose (v2.20+)
- Go 1.22+ (for local development)
- A modern browser with canvas support

### Run the System
```bash
# Clone and start all services
git clone <repo-url> && cd smolRAFT
docker-compose up --build

# Open the drawing board
open http://localhost:3000
```

### Development (Hot-Reload)
```bash
# Start with live-reload via Air
docker-compose up --build

# Edit any Go file — Air rebuilds automatically
# Edit frontend files — served directly via bind mount
```

## Components

| Service    | Port  | Description                                      |
|------------|-------|--------------------------------------------------|
| `gateway`  | 8080  | WebSocket hub, leader tracking, stroke routing   |
| `replica1` | 9001  | RAFT node 1                                      |
| `replica2` | 9002  | RAFT node 2                                      |
| `replica3` | 9003  | RAFT node 3                                      |
| `frontend` | 3000  | Static HTML/JS/CSS drawing board                 |

## RAFT Protocol Summary

### Node States
- **FOLLOWER** — Passive; replicates entries from leader; resets election timer on valid heartbeat
- **CANDIDATE** — Initiates election; requests votes from peers; promotes to LEADER on majority
- **LEADER** — Handles all writes; sends AppendEntries + heartbeats; commits on majority ACK

### State Transitions
```
                    ┌─────────────────────────────┐
                    │                             │
                    ▼     election timeout         │
              ┌──────────┐ ──────────────► ┌──────────┐
   start ───► │ FOLLOWER │                 │CANDIDATE │◄─┐
              └──────────┘ ◄────────────── └──────────┘  │
                    ▲      discovers leader     │    │    │
                    │      or higher term       │    └────┘
                    │                           │ wins election
                    │   discovers higher term   │ (majority votes)
                    │ ◄──────────────────  ┌────▼─────┐
                    └───────────────────── │  LEADER   │
                                           └──────────┘
```

### Timing Constants
| Parameter            | Value        |
|----------------------|-------------|
| Election timeout     | 500–800 ms  |
| Heartbeat interval   | 150 ms      |
| AppendEntries timeout| 100 ms      |
| Vote request timeout | 100 ms      |

### Replication Flow
1. Gateway sends stroke to leader via `POST /stroke`
2. Leader appends to local log with next index
3. Leader sends `AppendEntries` to both followers concurrently
4. On majority acknowledgment (2+/3), leader marks entry committed
5. Leader notifies Gateway of committed stroke
6. Gateway pushes stroke to all WebSocket clients

### Safety Invariants
- Committed entries are **never** overwritten
- A node only votes once per term
- A node only votes for candidates with logs at least as up-to-date
- Higher term always supersedes: revert to FOLLOWER immediately
- Split vote → election timeout fires again with new term

## API Reference

### Gateway Endpoints
| Method | Path      | Description                |
|--------|-----------|----------------------------|
| GET    | /health   | Liveness check             |
| GET    | /status   | Gateway status + clients   |
| GET    | /leader   | Current leader info        |
| GET    | /metrics  | Demo metrics               |
| GET    | /ws       | WebSocket upgrade endpoint |
| POST   | /commit   | Leader commit callback     |

### Replica Endpoints
| Method | Path             | Description                              |
|--------|-----------------|------------------------------------------|
| POST   | /request-vote   | Handle VoteRequest RPC                   |
| POST   | /append-entries  | Handle AppendEntries RPC                 |
| POST   | /heartbeat      | Heartbeat (empty AppendEntries)          |
| GET    | /sync-log?from=N| Return committed entries from index N    |
| POST   | /stroke         | Accept new stroke (leader only)          |
| GET    | /status         | Node status (id, state, term, log info)  |
| GET    | /health         | Liveness check                           |
| GET    | /metrics        | Demo metrics                             |

## Configuration

All configuration is via environment variables:

### Replica Environment Variables
| Variable       | Example                                        | Description                |
|---------------|------------------------------------------------|----------------------------|
| `NODE_ID`     | `node1`                                        | Unique node identifier     |
| `PEER_ADDRS`  | `replica2:9002,replica3:9003`                  | Comma-separated peer addrs |
| `GATEWAY_ADDR`| `gateway:8080`                                 | Gateway callback address   |
| `PORT`        | `9001`                                         | HTTP listen port           |

### Gateway Environment Variables
| Variable        | Example                                               | Description              |
|----------------|-------------------------------------------------------|--------------------------|
| `PORT`         | `8080`                                                | HTTP/WS listen port      |
| `REPLICA_ADDRS`| `replica1:9001,replica2:9002,replica3:9003`           | All replica addresses    |

## Testing

```bash
# Run all unit tests
make test

# Run with race detector
go test -race ./...

# Run integration tests (requires docker-compose up)
make test-integration
```

### Failure Scenarios
See [FAILURE_SCENARIOS.md](FAILURE_SCENARIOS.md) for detailed test scenarios including:
- Leader crash and automatic failover
- Follower crash and catch-up on restart
- Network partition simulation
- Rapid successive failures

## Development Guide

### Hot-Reload
Each service uses [Air](https://github.com/cosmtrek/air) for hot-reload. Edit any `.go` file and the container rebuilds automatically within seconds.

### Adding a New Replica
1. Copy environment variable block in `docker-compose.yml`
2. Add the new address to `PEER_ADDRS` of all existing replicas
3. Add to `REPLICA_ADDRS` in the gateway config
4. `docker-compose up --build`

## Known Limitations
- **No persistent storage**: Log is in-memory only; full restart loses all state
- **No log compaction/snapshots**: Log grows unbounded (adequate for demo workloads)
- **No membership changes**: Cluster size is fixed at startup
- **No pre-vote protocol**: Could see disruption from partitioned nodes
- **Simplified catch-up**: Leader retransmits full suffix rather than using snapshots
- **No TLS**: Inter-node communication is plaintext (not for production deployment)

## References
- [The Raft Consensus Algorithm (Ongaro & Ousterhout)](https://raft.github.io/raft.pdf)
- [Gorilla WebSocket](https://pkg.go.dev/github.com/gorilla/websocket)
- [Air — Live Reload for Go](https://github.com/cosmtrek/air)
- [etcd Raft Implementation](https://github.com/etcd-io/raft)
