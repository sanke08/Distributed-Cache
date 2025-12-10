package cluster

import (
	"encoding/json"
	"net/http"
	"sort"
	"sync"
	"time"
)

// ClusterState holds nodes and hashring. This is a leader-managed state.
type ClusterState struct {
	mu       sync.RWMutex
	ring     *HashRing
	nodesMap map[string]NodeInfo // id -> NodeInfo
	Replicas int
	self     NodeInfo
}

func NewClusterState(self NodeInfo, replicas int) *ClusterState {
	cs := &ClusterState{
		self:     self,
		Replicas: replicas,
		ring:     NewHashRing(replicas),
		nodesMap: make(map[string]NodeInfo),
		mu:       sync.RWMutex{},
	}

	cs.nodesMap[self.ID] = self
	cs.ring.AddNode(self)
	return cs
}

// AddNode adds a node to membership (leader action).
func (cs *ClusterState) AddNode(node NodeInfo) {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	if _, ok := cs.nodesMap[node.ID]; ok {
		return
	}
	cs.nodesMap[node.ID] = node
	cs.ring.AddNode(node)
}

// RemoveNode removes a node from membership (leader action).
func (cs *ClusterState) RemoveNode(nodeID string) {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	if _, ok := cs.nodesMap[nodeID]; !ok {
		return
	}
	delete(cs.nodesMap, nodeID)
	cs.ring.RemoveNode(nodeID)
}

// Nodes returns current members in stable order.
func (cs *ClusterState) Nodes() []NodeInfo {
	cs.mu.RLock()
	defer cs.mu.RUnlock()
	out := make([]NodeInfo, 0, len(cs.nodesMap))
	for _, node := range cs.nodesMap {
		out = append(out, node)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].ID < out[j].ID
	})
	return out
}

// LookupOwner returns the node responsible for the key.
func (cs *ClusterState) LookupOwner(key string) (NodeInfo, bool) {
	return cs.ring.Lookup(key)
}

// Snapshot returns JSON serializable snapshot of state.
func (cs *ClusterState) Snapshot() ([]byte, error) {
	cs.mu.RLock()
	defer cs.mu.RUnlock()

	type payload struct {
		Replicas int                 `json:"replicas"`
		Nodes    []NodeInfo          `json:"nodes"`
		Ring     map[string]NodeInfo `json:"ring"` // hash->node
	}
	p := payload{
		Replicas: cs.Replicas,
		Nodes:    cs.Nodes(),
		Ring:     cs.ring.Snapshot(),
	}
	return json.Marshal(p)
}

// ServeJoin and ServeState removed to decouple from HTTP.
// The server package should handle HTTP encoding/decoding and call AddNode/Snapshot.
// IsLeader determines if this node is leader by lowest ID rule.
func (cs *ClusterState) IsLeader() bool {
	cs.mu.RLock()
	defer cs.mu.RUnlock()
	// lowest lexicographic ID is leader
	min := cs.self.ID
	for id := range cs.nodesMap {
		if id < min {
			min = id
		}
	}
	return cs.self.ID == min
}

// ReplaceFromPayload replaces state from snapshot payload (used by follower to sync).
func (cs *ClusterState) ReplaceFromPayload(replicas int, nodes []NodeInfo, ring map[string]NodeInfo) {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	cs.Replicas = replicas
	cs.nodesMap = make(map[string]NodeInfo, len(nodes))
	for _, n := range nodes {
		cs.nodesMap[n.ID] = n
	}
	// replace hashring
	cs.ring.ReplaceFromSnapshot(ring, replicas)
}

// PollLeader polls leader state and updates local view periodically.
// caller should run in goroutine and stop when context canceled.
func (cs *ClusterState) PollLeader(leaderAddr string, interval time.Duration, stop <-chan struct{}) {
	// leaderAddr is full http address, e.g., "http://127.0.0.1:8080"
	t := time.NewTicker(interval)
	defer t.Stop()
	client := &http.Client{Timeout: 2 * time.Second}

	for {
		select {
		case <-stop:
			return
		case <-t.C:
			// fetch /v1/cluster/state
			resp, err := client.Get(leaderAddr + "/v1/cluster/state")
			if err != nil {
				continue
			}
			var payload struct {
				Replicas int                 `json:"replicas"`
				Nodes    []NodeInfo          `json:"nodes"`
				Ring     map[string]NodeInfo `json:"ring"`
			}

			if err := json.NewDecoder(resp.Body).Decode(&payload); err == nil {
				cs.ReplaceFromPayload(payload.Replicas, payload.Nodes, payload.Ring)
			}

			resp.Body.Close()
		}
	}
}

// GetReplicaNodes returns up to 'count' replica nodes (primary + successors).
func (cs *ClusterState) GetReplicaNodes(key string, count int) []NodeInfo {
	cs.mu.RLocker()
	defer cs.mu.RUnlock()
	return cs.ring.GetSuccessorNodes(key, count)
}
