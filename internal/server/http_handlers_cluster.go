package server

import (
	"encoding/json"
	"net/http"

	"github.com/sanke08/Distributed-Cache/internal/cluster"
)

func (s *Server) handleClusterJoin(w http.ResponseWriter, r *http.Request) {
	if s.cluster.IsLeader() {
		// handle join directly
		var n cluster.NodeInfo
		if err := json.NewDecoder(r.Body).Decode(&n); err != nil {
			http.Error(w, "invalid json", http.StatusBadRequest)
			return
		}

		// add node
		s.cluster.AddNode(n)
		// return full snapshot
		data, err := s.cluster.Snapshot()
		if err != nil {
			http.Error(w, "internal", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(data)

	} else {
		// if not leader, respond with 307, redirect to leader address
		// find leader (smallest id)
		nodes := s.cluster.Nodes()

		if len(nodes) == 0 {
			http.Error(w, "no leader", http.StatusServiceUnavailable)
			return
		}
		leader := nodes[0] // smallest ID due to sorted order

		http.Redirect(w, r, "http://"+leader.Addr+"/v1/cluster/join", http.StatusTemporaryRedirect)
	}
}

func (s *Server) handleStat(w http.ResponseWriter, r *http.Request) {
	data, err := s.cluster.Snapshot()
	if err != nil {
		http.Error(w, "internal", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(data)
}
