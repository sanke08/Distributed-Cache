package server

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"time"

	"github.com/sanke08/Distributed-Cache/internal/cache"
)

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

	if owner.Addr != s.cfg.HTTPAddr {
		// forward
		s.forwardToOwner(owner, w, r)
		return
	}

	// owner is self -> operate locally
	ttl := time.Duration(0)
	if req.TTLSecond > 0 {
		ttl = time.Duration(req.TTLSecond) * time.Second
	}

	_, cancel := context.WithTimeout(r.Context(), s.cfg.CmdTimeout)
	defer cancel()

	if err := s.cache.Set(uid, req.Key, []byte(req.Value), ttl); err != nil {
		if err == cache.ErrUserNotFound {
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		}
		log.Printf("[http] set err: %v", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

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
