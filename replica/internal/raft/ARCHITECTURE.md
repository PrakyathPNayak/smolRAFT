# RAFT Package Architecture

## Purpose
The `internal/raft` package implements a simplified RAFT consensus protocol for a 3-node cluster. It handles leader election, log replication, and commit tracking. This is the core algorithmic component of smolRAFT.

## File Map
| File             | Responsibility                                        |
|-----------------|-------------------------------------------------------|
| `node.go`       | Node struct, state machine, lifecycle, status          |
| `election.go`   | Election timer, vote requests, vote handling           |
| `replication.go`| AppendEntries, stroke handling, commit advancement     |
| `log.go`        | Append-only log data structure, thread-safe operations |
| `rpc.go`        | RPCClient interface, HTTP client implementation        |

## Concurrency Model
```
Node.Start()
    |
    +---> runElectionTimer goroutine
    |         - Fires election on timeout
    |         - Reset via resetElectionCh
    |
    +---> runCommitApplier goroutine
              - Polls commitIndex vs lastApplied
              - Sends committed entries to commitCh

On becoming LEADER:
    +---> runHeartbeatLoop goroutine
    |         - Sends heartbeats every 150ms
    |
    +---> runReplicationLoop goroutine (per peer)
              - Triggered by newEntryCh
              - Sends AppendEntries to assigned peer
```

## Synchronization
- `sync.RWMutex` on Node protects all mutable state
- `resetElectionCh` (chan struct{}, buffered 1) — non-blocking timer reset signal
- `newEntryCh` (chan struct{}, buffered 1) — non-blocking replication trigger
- `commitCh` (chan LogEntry, buffered 256) — committed entry output
- `stopCh` (chan struct{}, unbuffered) — lifecycle termination via close()

## Safety Invariants Enforced
1. **One vote per term**: `votedFor` cleared on term change, checked before granting
2. **Log up-to-dateness**: Votes only granted if candidate's log >= voter's log
3. **Committed entries never overwritten**: `Log.AppendEntries` panics if truncation hits committed range
4. **Higher term supersedes**: `stepDown()` called on every incoming RPC with higher term
5. **Commit only current term**: `advanceCommitIndex` skips entries from previous terms
