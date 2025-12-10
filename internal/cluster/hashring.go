package cluster

import (
	"fmt"
	"hash/fnv"
	"slices"
	"sort"
	"strconv"
	"sync"
)

// HashRing implements a consistent hashing ring with virtual nodes.
// This structure is safe for concurrent access.
type HashRing struct {
	replicas int // number of virtual nodes per node

	nodes  map[int64]NodeInfo //hash => node
	hashes []int64            //sorted

	mu sync.RWMutex
}

func NewHashRing(replicas int) *HashRing {
	if replicas <= 0 {
		replicas = 10
	}
	return &HashRing{
		replicas: replicas,
		nodes:    make(map[int64]NodeInfo),
		hashes:   make([]int64, 0),
	}
}

func hashStr(s string) int64 {
	h := fnv.New64a()
	h.Write([]byte(s))
	return int64(h.Sum64())
}

// AddNode inserts an actual node with virtual nodes.
func (hr *HashRing) AddNode(node NodeInfo) {
	hr.mu.Lock()
	defer hr.mu.Unlock()
	for i := 0; i < hr.replicas; i++ {
		key := fmt.Sprintf("%s#%d", node.Addr, i)
		hash := hashStr(key)
		hr.nodes[hash] = node
		hr.hashes = append(hr.hashes, hash)
	}
	slices.Sort(hr.hashes)
}

// RemoveNode removes an node and its virtual nodes.
func (hr *HashRing) RemoveNode(nodeID string) {
	hr.mu.Lock()
	defer hr.mu.Unlock()

	// old := []int{1,2,3,4,5}
	// new := old[:0]

	// new = append(new, 9, 8)
	// fmt.Println(old) // Output: [9 8 3 4 5]
	// fmt.Println(new) // Output: [9 8]

	// new = append(new, 7, 6, 5)
	// fmt.Println(old) // Output: [9 8 7 6 5]

	newHashes := hr.hashes[:0]
	for _, hash := range hr.hashes {
		node, ok := hr.nodes[hash]
		if !ok || node.ID != nodeID {
			newHashes = append(newHashes, hash)
			continue
		}
		delete(hr.nodes, hash)
	}
	hr.hashes = newHashes
}

// Lookup returns the NodeInfo responsible for the given key.
func (hr *HashRing) Lookup(key string) (NodeInfo, bool) {
	hr.mu.RLock()
	defer hr.mu.RUnlock()

	if len(hr.hashes) == 0 {
		return NodeInfo{}, false
	}
	h := hashStr(key)

	// first node whose hash is greater than or equal to the key’s hash.
	idx := sort.Search(len(hr.hashes), func(i int) bool {
		return hr.hashes[i] >= h
	})

	// if idx == len(hr.hashes), it means the key is greater than all hashes.
	// In that case, we wrap around to the first node.
	if idx == len(hr.hashes) {
		// wrap
		idx = 0
	}

	node := hr.nodes[hr.hashes[idx]]
	return node, true
}

// Snapshot returns a simple serializable representation: map replica->nodeInfo.
// For convenience we return slice of hashes as strings -> nodeInfo.
func (hr *HashRing) Snapshot() map[string]NodeInfo {
	hr.mu.RLock()
	defer hr.mu.RUnlock()

	out := make(map[string]NodeInfo, len(hr.hashes))
	for _, hash := range hr.hashes {
		out[strconv.FormatInt(hash, 10)] = hr.nodes[hash]
	}
	return out
}

// ReplaceFromSnapshot replaces ring with provided snapshot and replicas.
func (hr *HashRing) ReplaceFromSnapshot(snapshot map[string]NodeInfo, replicas int) {
	hr.mu.Lock()
	defer hr.mu.Unlock()

	hr.replicas = replicas
	hr.hashes = hr.hashes[:0]
	hr.nodes = make(map[int64]NodeInfo, len(snapshot))

	for hashStr, node := range snapshot {
		// parse hash
		// var h uint64
		// fmt.Sscan(hashStr, &h)
		h, err := strconv.ParseInt(hashStr, 10, 64)
		if err != nil {
			panic(err)
		}
		hr.hashes = append(hr.hashes, int64(h))
		hr.nodes[int64(h)] = node
	}
	slices.Sort(hr.hashes)
}

// GetSuccessorNodes returns up to 'count' unique real nodes starting from the primary for the given key.
// It walks the virtual nodes clockwise, skipping duplicate real nodes.
func (hr *HashRing) GetSuccessorNodes(key string, count int) []NodeInfo {
	hr.mu.RLock()
	defer hr.mu.RUnlock()

	res := make([]NodeInfo, 0, count)

	if len(hr.hashes) == 0 || count <= 0 {
		return res
	}

	// get the hash for the given key
	h := hashStr(key)

	// find the start index *[it return initial index of owner of (key)]
	idx := sort.Search(len(hr.hashes), func(i int) bool {
		return hr.hashes[i] >= h // internally reduce J till it is greater than or equal to h
	})

	// if idx == len(hr.hashes), it means the key is greater than all hashes.
	// In that case, we wrap around to the first node.
	if idx == len(hr.hashes) {
		// wrap
		idx = 0
	}

	seen := make(map[string]struct{})

	for i := 0; (len(res) < count) && (i < len(hr.hashes)); i++ {
		pos := (idx + i) % len(hr.hashes)
		hash := hr.hashes[pos]
		node := hr.nodes[hash]
		if _, ok := seen[node.ID]; ok {
			continue
		}
		seen[node.ID] = struct{}{}
		res = append(res, node)
	}
	return res
}
