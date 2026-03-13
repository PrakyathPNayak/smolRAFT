# Failure Scenarios & Expected Behavior

This document describes all failure modes the smolRAFT cluster must handle, the expected system behavior, and how to reproduce each scenario.

## Scenario 1: Leader Crash

**Trigger**: `docker-compose stop replica1` (while replica1 is leader)

**Expected Behavior**:
1. Remaining followers (replica2, replica3) stop receiving heartbeats
2. After election timeout (500-800ms), one starts an election with incremented term
3. The candidate requests votes from the remaining peer
4. With 2/3 nodes alive, majority (2) is achievable → new leader elected
5. Gateway detects leader change via `/status` polling within 1 second
6. Gateway re-routes new strokes to the new leader
7. Client WebSocket connections remain open; brief pause in commits (~1s), then normal operation
8. Drawing continues without clients needing to reconnect

**Verification**:
```bash
# Check cluster status before kill
make status
# Kill the current leader
docker-compose stop replica1
# Watch logs for election
docker-compose logs -f replica2 replica3
# Verify new leader elected
make status
# Draw in browser — strokes should still commit
```

## Scenario 2: Follower Crash

**Trigger**: `docker-compose stop replica3` (while replica3 is follower)

**Expected Behavior**:
1. Leader notices AppendEntries to replica3 times out (100ms)
2. Leader logs warning but continues normal operation
3. Leader still has quorum (2/3: itself + remaining follower)
4. New strokes are committed with 2/3 acknowledgment
5. No impact on clients or drawing experience
6. Gateway never notices (it only talks to the leader)

**Verification**:
```bash
docker-compose stop replica3
# Draw strokes — should commit normally
make status  # Shows 2 nodes, leader still active
```

## Scenario 3: Follower Restart (Catch-Up)

**Trigger**: Stop a follower, draw N strokes, restart the follower

**Expected Behavior**:
1. Follower starts with empty in-memory log
2. Receives AppendEntries from leader; prevLogIndex check fails (follower has no entries)
3. Follower responds `{success: false, conflictIndex: 0}`
4. Leader decrements nextIndex for this follower, retransmits from the beginning
5. May take multiple rounds if leader decrements one at a time, or leader can use conflictIndex hint
6. Eventually follower has all committed entries and participates normally
7. No client impact during catch-up

**Verification**:
```bash
docker-compose stop replica3
# Draw 10 strokes in the browser
docker-compose start replica3
# Wait 2-3 seconds
curl http://localhost:9003/status  # Should show logLength matching leader
```

## Scenario 4: Network Partition (Minority Isolation)

**Trigger**: `scripts/partition.sh replica1` (isolates one node)

**Expected Behavior**:
1. If partitioned node is the leader:
   - It can no longer reach a majority → cannot commit entries
   - Remaining 2 nodes elect a new leader
   - Old leader eventually receives RPC with higher term → steps down to FOLLOWER
2. If partitioned node is a follower:
   - It stops receiving heartbeats → starts elections
   - Cannot win (no majority) → increments term repeatedly
   - When partition heals, it may have a very high term
   - On reconnection, its high term forces current leader to step down
   - New election occurs with the high term; the node with the most up-to-date log wins
3. Gateway discovers new leader and re-routes

**Note**: Without pre-vote protocol, the isolated node's term inflation can be disruptive. This is a known limitation.

## Scenario 5: Split Vote

**Trigger**: Two followers start elections simultaneously (rare, but possible)

**Expected Behavior**:
1. Both followers increment term and become CANDIDATE
2. Each votes for itself
3. Neither gets a majority (each has 1 vote, need 2)
4. Election timeout fires again with different random delays
5. One candidate starts next election first, gets the other's vote
6. Leader is elected in the subsequent term
7. Typical resolution: 1-2 extra election rounds (~1-2 seconds total delay)

**Verification**: Hard to reproduce deterministically. Observe via logs:
```
INFO became candidate  nodeId=node2  term=5
INFO became candidate  nodeId=node3  term=5
WARN election failed: no majority  nodeId=node2  term=5
INFO became candidate  nodeId=node3  term=6
INFO vote granted  nodeId=node2  term=6  votedFor=node3
INFO became leader  nodeId=node3  term=6
```

## Scenario 6: Rapid Successive Failures

**Trigger**: Kill leader, wait 2s, kill new leader

**Expected Behavior**:
1. First kill → new leader elected (same as Scenario 1)
2. Second kill → only 1 node remains
3. Remaining node has no quorum (1/3 < majority)
4. Remaining node starts elections but cannot win
5. System is read-only: Gateway cannot commit new strokes
6. Gateway reports no leader available
7. Client sees "No leader — waiting for quorum" status
8. When any killed node restarts → quorum restored → election succeeds → writes resume

**Verification**:
```bash
docker-compose stop replica1
sleep 3
docker-compose stop replica2  # Assuming replica2 became leader
make status  # Only replica3, no leader
# Try to draw — should see error/pending state
docker-compose start replica1
sleep 2
make status  # Leader elected, system recovered
```

## Scenario 7: Gateway Restart

**Trigger**: `docker-compose restart gateway`

**Expected Behavior**:
1. All WebSocket connections drop
2. Clients auto-reconnect with exponential backoff
3. Gateway re-discovers leader by polling `/status` on all replicas
4. Clients re-establish WebSocket connections
5. Canvas state is preserved in browser; new strokes continue
6. RAFT cluster is completely unaffected (it doesn't depend on gateway)

## Scenario 8: Slow Follower

**Trigger**: Add artificial latency to one replica's network

**Expected Behavior**:
1. Leader's AppendEntries to slow follower may time out (100ms)
2. Leader still commits with fast follower's ACK (2/3)
3. Slow follower eventually receives entries (on next heartbeat/retry)
4. Slow follower's log falls behind temporarily but catches up
5. No impact on commit latency (only need majority)

## Scenario 9: All Nodes Restart Simultaneously

**Trigger**: `docker-compose restart replica1 replica2 replica3`

**Expected Behavior**:
1. All logs are lost (in-memory only)
2. All nodes start as FOLLOWER with term=0, empty log
3. Election timers fire; one node becomes leader
4. System is functional but all previous drawing data is gone
5. Clients see empty canvas (if canvas state was server-authoritative)
6. With client-side canvas preservation, clients keep their local view

**Note**: This is expected behavior for an in-memory system. Production systems use WAL.

## Test Matrix

| # | Scenario                   | Client Impact    | Recovery Time | Automated Test |
|---|---------------------------|------------------|---------------|----------------|
| 1 | Leader crash              | ~1s pause        | < 2s          | Yes            |
| 2 | Follower crash            | None             | Immediate     | Yes            |
| 3 | Follower restart          | None             | < 3s catch-up | Yes            |
| 4 | Network partition         | ~1s if leader    | < 2s          | Manual         |
| 5 | Split vote                | ~1-2s pause      | < 3s          | Probabilistic  |
| 6 | Double failure            | Write unavailable| Until restart  | Yes            |
| 7 | Gateway restart           | WS reconnect     | < 5s          | Yes            |
| 8 | Slow follower             | None             | Gradual       | Manual         |
| 9 | Full cluster restart      | Data loss        | < 2s          | Yes            |
