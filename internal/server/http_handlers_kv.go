package server

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"time"

	"github.com/sanke08/Distributed-Cache/internal/cache"
)

type setRequest struct {
	Key       string `json:"key"`
	Value     string `json:"value"`
	TTLSecond int64  `json:"ttl_second,omitempty"`
}

type internalReplicationRequest struct {
	UserID    string `json:"user_id"`
	Key       string `json:"key"`
	Value     []byte `json:"value"`
	TTL       int64  `json:"ttl_secs,"`
	Timestamp int64  `json:"timestamp"`
}

func (s *Server) handleSet(w http.ResponseWriter, r *http.Request) {

	uid, err := userIDFromHeader(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	var req setRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
	}

	if req.Key == "" {
		http.Error(w, "missing key", http.StatusBadRequest)
		return
	}

	// determine owner
	keyForHash := uid + ":|:" + req.Key
	owner, ok := s.cluster.LookupOwner(keyForHash)
	if !ok {
		http.Error(w, "no cluster nodes", http.StatusServiceUnavailable)
		return
	}

	// If not owner, forward the original request (body) to owner
	if owner.Addr != s.cfg.HTTPAddr {
		// fforward original body as-is
		s.forwardToOwner(owner, w, r)
		return
	}

	// owner is self -> do fast local write and enqueue replication tasks
	ttl := time.Duration(0)
	if req.TTLSecond > 0 {
		ttl = time.Duration(req.TTLSecond) * time.Second
	}

	_, cancel := context.WithTimeout(r.Context(), s.cfg.CmdTimeout)
	defer cancel()

	// Ensure user exists
	if err := s.cache.CreateUser(uid); err != nil {
		if err != cache.ErrUserExists {
			log.Printf("[http] create user err: %v", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
	}

	timestamp := time.Now().UnixNano()

	// Local fast write (Set inside ReplicateSet handles timestamp logic)
	if err := s.cache.Set(uid, req.Key, []byte(req.Value), ttl, timestamp); err != nil {
		if err == cache.ErrUserNotFound {
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		}
		log.Printf("[http] set err: %v", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	// enqueue replication to other replicas (non-blocking)
	s.enqueueReplication(uid, req.Key, []byte(req.Value), req.TTLSecond, timestamp)

	// immediate success response
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`{"status":"ok"}`))
}

func (s *Server) handleGet(w http.ResponseWriter, r *http.Request) {
	uid, err := userIDFromHeader(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	key := r.URL.Query().Get("key")
	if key == "" {
		http.Error(w, "missing key", http.StatusBadRequest)
		return
	}

	// determine owner
	keyForHash := uid + ":|:" + key
	owner, ok := s.cluster.LookupOwner(keyForHash)
	if !ok {
		http.Error(w, "no cluster nodes", http.StatusServiceUnavailable)
		return
	}

	if owner.Addr != s.cfg.HTTPAddr {
		// forward
		s.forwardToOwner(owner, w, r)
		return
	}

	_, cancel := context.WithTimeout(r.Context(), s.cfg.CmdTimeout)
	defer cancel()

	val, err := s.cache.Get(uid, key)
	if err != nil {
		if err == cache.ErrUserNotFound || err == cache.ErrKeyNotFound {
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		}
		log.Printf("[http] get err: %v", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	resp := valueResponse{Value: string(val)}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

func (s *Server) handleDelete(w http.ResponseWriter, r *http.Request) {
	uid, err := userIDFromHeader(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	key := r.URL.Query().Get("key")
	if key == "" {
		http.Error(w, "missing key", http.StatusBadRequest)
		return
	}

	// determine owner
	keyForHash := uid + ":|:" + key
	owner, ok := s.cluster.LookupOwner(keyForHash)
	if !ok {
		http.Error(w, "no cluster nodes", http.StatusServiceUnavailable)
		return
	}

	if owner.Addr != s.cfg.HTTPAddr {
		// forward
		s.forwardToOwner(owner, w, r)
		return
	}

	_, cancel := context.WithTimeout(r.Context(), s.cfg.CmdTimeout)
	defer cancel()
	if err := s.cache.Delete(uid, key); err != nil {
		if err == cache.ErrUserNotFound {
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		}
		log.Printf("[http] delete err: %v", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`{"status":"deleted"}`))

}

func (s *Server) handleKeys(w http.ResponseWriter, r *http.Request) {
	uid, err := userIDFromHeader(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	// For keys, we might need distributed gathering,
	// but for now, we only list local keys or we need a way to aggregate.
	// Current implementation: only local keys for that user.
	// Production Grade Recommendation: this should probably aggregate from all nodes.
	// But per "make functionality does not break" (it was local before), we keep it local or modify?
	// The user added cluster logic to SET/GET but not KEYS in the diff.
	// Checking the diffs... The user did NOT modify handleKeys in his last edit.
	// So I will keep it as is (local).

	_, cancel := context.WithTimeout(r.Context(), s.cfg.CmdTimeout)
	defer cancel()
	keys, err := s.cache.ListKeys(uid)
	if err != nil {
		if err == cache.ErrUserNotFound {
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		}
		log.Printf("[http] keys err: %v", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	resp := keyResponse{Keys: keys}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

// Internal replication endpoint - replicas accept these writes from primary.
func (s *Server) handleInternalReplicate(w http.ResponseWriter, r *http.Request) {
	var req internalReplicationRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
	}

	ttl := time.Duration(0)
	if req.TTL > 0 {
		ttl = time.Duration(req.TTL) * time.Second
	}

	// ensure user exists (create if necessary)
	if err := s.cache.CreateUser(req.UserID); err != nil && err != cache.ErrUserExists {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	// apply replicated write - replicateSet ensures timestamp ordering
	if err := s.cache.Set(req.UserID, req.Key, req.Value, ttl, req.Timestamp); err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`{"status":"ok"}`))

}
