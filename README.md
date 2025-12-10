# Distributed Cache

A production-grade distributed in-memory cache system built in Go with consistent hashing, multi-tenancy, and cluster coordination.

## Features

- 🚀 **Distributed Architecture**: Consistent hashing with virtual nodes for even data distribution
- 👥 **Multi-Tenant**: User-based cache isolation
- ⚡ **Dual Interface**: HTTP REST API and TCP protocol support
- 🔄 **LRU Eviction**: Automatic eviction of least recently used items
- ⏱️ **TTL Support**: Time-to-live for cache entries
- 💾 **Persistence**: Snapshot and restore capabilities
- 🔀 **Request Forwarding**: Automatic routing to the correct node (HTTP only)
- 🎯 **Leader-Follower**: Simple leader election based on lexicographic node ID
- 🔁 **Asynchronous Replication**: Background worker pool for data replication across nodes
- ⏰ **Conflict Resolution**: Timestamp-based Last-Write-Wins (LWW) for eventual consistency

---

## Table of Contents

- [Architecture](#architecture)
- [Folder Structure](#folder-structure)
- [Data Flow](#data-flow)
- [How It Works](#how-it-works)
- [Installation](#installation)
- [Usage](#usage)
- [API Reference](#api-reference)
- [Configuration](#configuration)
- [Limitations](#limitations)

---

## Architecture

```
┌─────────────────────────────────────────────────────────────────┐
│                         Client Layer                             │
│                    (HTTP / TCP Clients)                          │
└────────────┬────────────────────────────────────────────────────┘
             │
             ▼
┌─────────────────────────────────────────────────────────────────┐
│                      Server Layer                                │
│  ┌──────────────┐              ┌──────────────┐                 │
│  │  HTTP Server │              │  TCP Server  │                 │
│  │  (Port 8080) │              │ (Port 9000)  │                 │
│  └──────┬───────┘              └──────┬───────┘                 │
│         │                             │                         │
│         ▼                              ▼                         │
│  ┌─────────────────────────────────────────────┐                │
│  │         Request Handler Layer                │                │
│  │  • KV Handlers (http_handlers_kv.go)        │                │
│  │  • User Handlers (http_handlers_user.go)    │                │
│  │  • Cluster Handlers (http_handlers_cluster) │                │
│  │  • TCP Handlers (tcp.go)                    │                │
│  └─────────────┬───────────────────────────────┘                │
└────────────────┼────────────────────────────────────────────────┘
                 │
                 ▼
┌─────────────────────────────────────────────────────────────────┐
│                   Cluster Coordination                           │
│  ┌──────────────────────────────────────────────────────┐       │
│  │              ClusterState                             │       │
│  │  • Membership Management (AddNode/RemoveNode)         │       │
│  │  • Leader Election (IsLeader)                         │       │
│  │  • Request Routing (LookupOwner)                      │       │
│  │  • Replica Selection (GetSuccessorNodes)              │       │
│  │  • State Synchronization (PollLeader)                 │       │
│  └──────────────────┬───────────────────────────────────┘       │
│                     │                                            │
│                     ▼                                            │
│  ┌──────────────────────────────────────────────────────┐       │
│  │              HashRing                                 │       │
│  │  • Consistent Hashing                                 │       │
│  │  • Virtual Nodes (Replicas)                           │       │
│  │  • Key → Node Mapping                                 │       │
│  └──────────────────────────────────────────────────────┘       │
└─────────────────────────────────────────────────────────────────┘
                 │
                 ▼
┌─────────────────────────────────────────────────────────────────┐
│                    Cache Storage Layer                           │
│  ┌──────────────────────────────────────────────────────┐       │
│  │                  Cache                                │       │
│  │  • Multi-tenant user management                       │       │
│  │  • Snapshot/Restore                                   │       │
│  └──────────────────┬───────────────────────────────────┘       │
│                     │                                            │
│                     ▼                                            │
│  ┌──────────────────────────────────────────────────────┐       │
│  │              UserCache (per user)                     │       │
│  │  • In-memory key-value store                         │       │
│  │  • LRU eviction policy                                │       │
│  │  • TTL-based expiration                               │       │
│  │  • Background janitor (cleanup)                       │       │
│  └──────────────────────────────────────────────────────┘       │
└─────────────────────────────────────────────────────────────────┘
```

---

## Folder Structure

```
distributed-cache/
├── go.mod                          # Go module definition
├── README.md                       # This file
│
├── internal/
│   ├── cache/                      # Cache storage layer
│   │   ├── cache.go                # Multi-tenant cache manager
│   │   ├── user_cache.go           # Per-user cache with LRU & TTL
│   │   ├── config.go               # Cache configuration
│   │   └── errors.go               # Cache-specific errors
│   │
│   ├── cluster/                    # Cluster coordination
│   │   ├── cluster.go              # Cluster state & membership
│   │   ├── hashring.go             # Consistent hashing implementation
│   │   └── node.go                 # Node information structure
│   │
│   ├── server/                     # Server layer
│   │   ├── server.go               # Server lifecycle management
│   │   ├── http.go                 # HTTP routing and utilities
│   │   ├── http_handlers_kv.go     # KV operation handlers (distributed)
│   │   ├── http_handlers_user.go   # User management handlers
│   │   ├── http_handlers_cluster.go# Cluster API handlers
│   │   ├── replication.go          # Async replication worker pool
│   │   └── tcp.go                  # TCP protocol implementation
│   │
│   └── cmd/                        # Application entry point
│       └── main.go                 # Main application with CLI flags
│
└── data/                           # (Created at runtime) Snapshot storage
    └── user_*.json                 # Per-user snapshot files
```

### File Responsibilities

#### `internal/cache/`

- **`cache.go`**: Manages multiple user caches, provides snapshot/restore for all users, timestamp-aware Set()
- **`user_cache.go`**: Individual user's cache with LRU tracking, TTL expiration, timestamp-based conflict resolution, and janitor goroutine
- **`config.go`**: Configuration for cache (eviction limits, janitor intervals, etc.)
- **`errors.go`**: Domain-specific errors (`ErrUserNotFound`, `ErrKeyNotFound`, etc.)

#### `internal/cluster/`

- **`cluster.go`**: Manages cluster membership, leader election, state synchronization, and replica selection
- **`hashring.go`**: Implements consistent hashing with virtual nodes and successor node lookup
- **`node.go`**: Simple struct for node identification (ID + Address)

#### `internal/server/`

- **`server.go`**: Coordinates HTTP/TCP server lifecycle, handles cluster join logic, manages replication workers
- **`http_handlers_kv.go`**: GET/SET/DELETE/KEYS handlers with cluster-aware forwarding and replication
- **`http_handlers_user.go`**: User creation/deletion and snapshot/restore handlers
- **`http_handlers_cluster.go`**: Join and state endpoints for cluster coordination
- **`replication.go`**: Asynchronous replication manager with worker pool and retry logic
- **`tcp.go`**: Text-based TCP protocol (local-only, no distributed forwarding or replication)

#### `internal/cmd/`

- **`main.go`**: Entry point with CLI flag parsing, server initialization, and graceful shutdown

---

## Data Flow

### 1. Cluster Formation

```
Leader Node (Node A)                    Follower Node (Node B)
─────────────                           ───────────────────────
Start without -join flag                Start with -join=http://nodeA:8080
        │                                         │
        ▼                                         ▼
Initialize ClusterState                 Initialize ClusterState
(self = only member)                    (self = only member)
        │                                         │
        │                           ┌─────────────┘
        │                           │
        │                           │ POST /v1/cluster/join
        │                           │ {id: "nodeB", addr: ":8081"}
        │◄──────────────────────────┘
        │
        ▼
Add nodeB to cluster
Update HashRing
        │
        │ 200 OK + Snapshot
        │ {replicas: 10, nodes: [...], ring: {...}}
        └────────────────────────────►
                                      │
                                      ▼
                                Replace local state
                                Start polling leader every 2s
```

### 2. Distributed Write (HTTP)

```
Client                    Node A (Receiver)              Node B (Owner)
──────                    ─────────────────              ──────────────
POST /v1/set
X-User-Id: user1
{key: "foo", value: "bar"}
        │
        └──────────►  1. Receive request
                      2. Hash key: "user1:|:foo"
                      3. Lookup owner via HashRing
                      4. Owner = Node B ≠ self
                            │
                            │ Forward request
                            │ POST http://nodeB:8081/v1/set
                            └─────────────────────►
                                                   │
                                                   ▼
                                            5. Receive forwarded request
                                            6. Hash matches (I'm owner)
                                            7. Store locally
                                            8. Return 200 OK
                            ◄──────────────────────┘
                            │
        ◄───────────────────┘
        200 OK
```

### 3. Asynchronous Replication (Background)

After a successful write to the primary owner, replication happens asynchronously:

```
Node B (Primary)          Workers          Replicas (C & A)
────────────────          ───────          ────────────────
Write succeeds locally
Generate timestamp: ts
        │
        ▼
Get replicas:
GetSuccessorNodes(key, 3)
→ [B, C, A]
        │
        ▼
Skip self, enqueue for C & A
        │
        ├─────────────────► Worker picks task
        │                   POST /v1/internal/replicate
        │                   {user, key, val, ts}
        │                          │
        │                          ├───────────► Node C
        │                          │             Apply if ts >= existing
        │                          │             200 OK
        │                          │
        │                          └───────────► Node A
        │                                        Apply if ts >= existing
        │                                        200 OK
        ▼
Return to client
(don't wait for replicas)

Retries: 3 attempts with 2s backoff on failure
Queue: Bounded at 10,000 tasks (drops on overflow)
```

### 4. Local Write (TCP)

```
TCP Client                Node A
──────────                ──────
CONNECT localhost:9000
        │
        └──────────► Accept connection
                            │
AUTH user1                  ▼
        │              Store authUser = "user1"
        ◄──────────── OK
        │
SET foo bar                 │
        │                   ▼
        │              1. Parse command
        │              2. Set(user1, "foo", "bar") ← Direct to local cache
        │              3. No cluster lookup!
        ◄──────────── OK
```

**⚠️ Note**: TCP interface does NOT support distributed forwarding. It only operates on the local node's cache.

---

## How It Works

### Consistent Hashing

The system uses consistent hashing to distribute keys across nodes:

1. **Virtual Nodes**: Each physical node is mapped to 10 virtual nodes on the hash ring
2. **Key Mapping**: Keys are hashed using FNV-64a: `hash("userID:|:key")`
3. **Owner Lookup**: Binary search finds the first virtual node with `hash >= keyHash`
4. **Rebalancing**: When nodes join/leave, only ~1/N keys need to be redistributed

```go
// Example: 3 nodes with 10 replicas each = 30 points on ring
Ring: [hash1→NodeA, hash2→NodeC, hash3→NodeB, ..., hash30→NodeA]

Key "user1:|:foo" → hash=12345
↓
Find first hash >= 12345 → hash3 → NodeB
↓
NodeB is the owner
```

### Leader Election

- **Rule**: Node with the smallest lexicographic ID is the leader
- **Example**: Nodes `["nodeA", "nodeB", "nodeC"]` → `nodeA` is leader
- **Dynamic**: If leader leaves, next smallest becomes new leader
- **Join Endpoint**: Only the leader accepts `/v1/cluster/join` requests

### State Synchronization

Follower nodes poll the leader every 2 seconds:

```go
GET /v1/cluster/state
↓
{
  "replicas": 10,
  "nodes": [{id: "nodeA", addr: ":8080"}, ...],
  "ring": {"12345": {id: "nodeB", addr: ":8081"}, ...}
}
↓
Follower replaces local state
```

### LRU Eviction

Each `UserCache` maintains:

- `items`: `map[string]Item` (the actual data)
- `lruList`: Doubly-linked list (front = most recent, back = least recent)
- `lruMap`: `map[string]*list.Element` (fast lookup)

On `Get`:

1. Move key to front of `lruList`

On `Set`:

1. If key exists: update + move to front
2. If new key:
   - Add to front
   - If `len(items) > MaxEntries`: evict from back

### TTL Expiration

- **Lazy**: On `Get`, check if expired → delete + return not found
- **Active**: Background janitor runs every 30 seconds:
  1. Acquire RLock, collect expired keys
  2. Release RLock
  3. Acquire Lock, re-check expiration, delete

---

## Installation

### Prerequisites

- Go 1.23.2 or higher
- Git

### Clone the Repository

```bash
git clone https://github.com/sanke08/Distributed-Cache.git
cd Distributed-Cache
```

---

## Usage

### Starting a Single Node

```bash
go run internal/cmd/main.go -addr :8080 -tcp :9000 -id node1 -data data/node1
```

### Starting a Cluster

**1. Start Leader (Node 1)**

```bash
go run internal/cmd/main.go -addr :8080 -tcp :9000 -id node1 -data data/node1
```

**2. Start Follower (Node 2)** _(In a new terminal)_

```bash
go run internal/cmd/main.go -addr :8081 -tcp :9001 -id node2 -join http://localhost:8080 -data data/node2
```

**3. Start Another Follower (Node 3)** _(In a new terminal)_

```bash
go run internal/cmd/main.go -addr :8082 -tcp :9002 -id node3 -join http://localhost:8080 -data data/node3
```

### CLI Flags

| Flag    | Default | Description                                                 |
| ------- | ------- | ----------------------------------------------------------- |
| `-addr` | `:8080` | HTTP listen address                                         |
| `-tcp`  | `:9000` | TCP listen address                                          |
| `-id`   | `""`    | Node ID (defaults to HTTP addr if not set)                  |
| `-join` | `""`    | Leader HTTP address to join (e.g., `http://localhost:8080`) |
| `-data` | `data`  | Directory for snapshot files                                |

---

## API Reference

### HTTP API

#### Base URL: `http://localhost:8080/v1`

### User Management

**Create User**

```http
POST /v1/user
Content-Type: application/json

{
  "user_id": "alice"
}
```

**Delete User**

```http
DELETE /v1/user/{userID}
```

### Key-Value Operations

All KV operations require `X-User-Id` header.

**Set Key**

```http
POST /v1/set
X-User-Id: alice
Content-Type: application/json

{
  "key": "session_token",
  "value": "abc123xyz",
  "ttl_seconds": 3600
}
```

**Get Key**

```http
GET /v1/get?key=session_token
X-User-Id: alice
```

**Delete Key**

```http
DELETE /v1/delete?key=session_token
X-User-Id: alice
```

**List Keys**

```http
GET /v1/keys
X-User-Id: alice
```

### Persistence

**Save Snapshot**

```http
POST /v1/user/snapshot
X-User-Id: alice
```

Saves to `data/user_alice.json`

**Restore Snapshot**

```http
POST /v1/user/restore
X-User-Id: alice
```

Loads from `data/user_alice.json`

### Cluster Management

**Join Cluster** (Leader only)

```http
POST /v1/cluster/join
Content-Type: application/json

{
  "id": "node2",
  "addr": ":8081"
}
```

**Get Cluster State**

```http
GET /v1/cluster/state
```

**Health Check**

```http
GET /v1/ping
```

### Internal Endpoints

> **⚠️ Warning**: These endpoints are for internal cluster communication only. Do NOT expose to public clients.

**Replicate Data** (Internal use only)

```http
POST /v1/internal/replicate
Content-Type: application/json

{
  "user_id": "alice",
  "key": "session",
  "value": "YWJjMTIz",  // base64 encoded []byte
  "ttl_secs": 3600,
  "timestamp": 1733860453724578300
}
```

Used by replication workers to propagate writes from primary to replicas. Timestamp ensures Last-Write-Wins conflict resolution.

---

### TCP Protocol

Connect to `localhost:9000` and use text commands.

#### Commands

```
AUTH <userID>
CREATEUSER <userID>
DELETEUSER <userID>
SET <key> <value> [ttl_seconds]    (requires AUTH)
SET <userID> <key> <value> [ttl_seconds]
GET <key>                          (requires AUTH)
GET <userID> <key>
DELETE <key>                       (requires AUTH)
DELETE <userID> <key>
KEYS                               (requires AUTH)
KEYS <userID>
SNAPSHOT                           (requires AUTH)
SNAPSHOT <userID>
RESTORE                            (requires AUTH)
RESTORE <userID>
PING
QUIT
```

#### Example Session

```
$ telnet localhost 9000

> AUTH alice
OK

> SET session abc123
OK

> GET session
VALUE abc123

> KEYS
KEYS session

> QUIT
BYE
```

---

## Configuration

### Cache Configuration (in code)

```go
type Config struct {
    InitialCapacity int           // Initial map capacity
    MaxEntries      int           // Max keys before LRU eviction (0 = unlimited)
    JanitorInterval time.Duration // How often to clean expired keys
    DataDir         string        // Where to save snapshots
}
```

Default:

```go
{
    InitialCapacity: 100,
    MaxEntries:      10000,
    JanitorInterval: 30 * time.Second,
    DataDir:         "data",
}
```

### Server Configuration

```go
type ServerConfig struct {
    HTTPAddr        string
    TCPAddr         string
    CmdTimeout      time.Duration // 5s
    ReadTimeout     time.Duration // 10s
    WriteTimeout    time.Duration // 10s
    IdealTimeout    time.Duration // 120s
    NodeID          string
    JoinAddr        string
    ClusterReplicas int           // Virtual nodes per physical node (default: 10)
    PollInterval    time.Duration // How often followers poll leader (default: 2s)

    // Replication Settings
    ReplicationWorkers    int           // Concurrent worker goroutines (default: 4)
    ReplicationQueueSize  int           // Task buffer size (default: 10,000)
    ReplicationTimeout    time.Duration // HTTP client timeout (default: 300ms)
    ReplicationMaxRetries int           // Retry attempts per task (default: 3)
}
```

---

## Limitations

### 1. ⚠️ TCP Interface is Local-Only

The TCP protocol does **not** support distributed request forwarding. When a TCP client connects to Node A and requests a key owned by Node B:

- **HTTP**: Node A forwards the request to Node B ✅
- **TCP**: Node A looks in its local cache only ❌

**Workaround**: Use HTTP API for distributed operations, or implement client-side sharding for TCP.

### 2. Asynchronous Replication Only

**Current Implementation**: Data is replicated asynchronously to N successor nodes via a background worker pool.

**Limitations**:

- Writes return success before replicas confirm receipt
- If primary crashes before replication completes, replicas may miss the write
- No synchronous replication option for critical data

**Tradeoff**: Prioritizes low latency over durability. Acceptable for cache use cases.

### 3. DELETE Operations Not Replicated

**Issue**: Only SET operations trigger replication. DELETE requests are local-only.

**Impact**: Deleting a key on the primary does NOT delete it on replicas, leading to stale data.

**Future Enhancement**: Add replication logic to `handleDelete()`.

### 4. No Read Repair or Anti-Entropy

**Issue**: If replication fails (network partition, queue overflow), replicas become stale. There's no mechanism to detect/fix inconsistencies.

**Future Enhancement**: Implement periodic gossip protocol or merkle tree comparison.

### 5. No Node Failure Detection

If a node crashes, it remains in the cluster state until manually removed.

**Future Enhancement**: Implement heartbeat-based failure detection.

### 6. Leader is a Single Point of Failure

If the leader crashes, followers can't add new nodes (join requests fail).

**Workaround**: Followers will automatically recognize the next-smallest node as leader, but join functionality requires a full cluster restart.

### 7. No Authentication/Authorization

The system has no built-in auth.

---

## License

MIT License - feel free to use this code commercially or personally.

---

## Support and Guidance

If you have suggestions on how to improve this project, write code more professionally, or implement features in a more production-ready way, please open a GitHub
issue: https://github.com/sanke08/Distributed-Cache/issues.

I’m looking to learn best practices, improve code quality, and explore ways to make this project more robust, so any advice or insights from developers are highly appreciated.
