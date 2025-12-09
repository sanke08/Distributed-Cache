package server

import (
	"context"
	"encoding/json"
	"log"
	"net/http"

	"github.com/sanke08/Distributed-Cache/internal/cache"
)

func (s *Server) handleUserCreate(w http.ResponseWriter, r *http.Request) {
	var payload struct {
		UserID string `json:"user_id"`
	}

	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
	}

	if payload.UserID == "" {
		http.Error(w, "missing user_id", http.StatusBadRequest)
		return
	}

	_, cancel := context.WithTimeout(r.Context(), s.cfg.CmdTimeout)
	defer cancel()

	if err := s.cache.CreateUser(payload.UserID); err != nil {
		if err == cache.ErrUserExists {
			http.Error(w, err.Error(), http.StatusConflict)
			return
		}
		log.Printf("[http] create user err: %v", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`{"status":"created"}`))
}

func (s *Server) handleUserDelete(w http.ResponseWriter, r *http.Request) {

	userID := r.PathValue("userID")
	if userID == "" {
		http.Error(w, "missing user id in path", http.StatusBadRequest)
		return
	}
	_, cancel := context.WithTimeout(r.Context(), s.cfg.CmdTimeout)
	defer cancel()

	if err := s.cache.DeleteUser(userID); err != nil {
		if err == cache.ErrUserNotFound {
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		}
		log.Printf("[http] delete user err: %v", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`{"status":"deleted"}`))
}

// handleSaveSnapshot triggers saving a user's snapshot to disk.
func (s *Server) handleSaveSnapshot(w http.ResponseWriter, r *http.Request) {
	uid, err := userIDFromHeader(r)

	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	snap, err := s.cache.SnapshotUser(uid)

	if err != nil {
		if err == cache.ErrUserNotFound {
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		}
		log.Printf("[http] snapshot err: %v", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	if _, err := s.cache.SaveUserToFile(snap); err != nil {
		log.Printf("[http] save user to file err: %v", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	_, _ = w.Write([]byte(`{"status":"ok"}`))
}

// handleRestoreSnapshot triggers loading a user's snapshot from disk and restoring into cache.
func (s *Server) handleRestoreSnapshot(w http.ResponseWriter, r *http.Request) {
	uid, err := userIDFromHeader(r)

	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	snap, err := s.cache.LoadUserFromFile(uid)

	if err != nil {
		if err == cache.ErrUserNotFound {
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		}
		log.Printf("[http] load user from file err: %v", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	if snap == nil {
		http.Error(w, "snapshot not found", http.StatusNotFound)
		return
	}

	if err := s.cache.RestoreUserFromSnapshot(snap); err != nil {
		log.Printf("[http] restore user err: %v", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	_, _ = w.Write([]byte(`{"status":"ok"}`))
}
