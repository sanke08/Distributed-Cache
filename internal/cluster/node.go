package cluster

// NodeInfo represents a cluster node identity.
type NodeInfo struct {
	ID   string `json:"id"`   // unique node id
	Addr string `json:"addr"` // HTTP address, e.g. "127.0.0.1:8080"
}
