package server

import (
	"bytes"
	"errors"
	"io"
	"net/http"

	"github.com/sanke08/Distributed-Cache/internal/cluster"
)

var (
	errMissingUser = errors.New("missing user ID")
)

func registerHTTPHandlers(mux *http.ServeMux, s *Server) {
	mux.HandleFunc("POST /v1/user", s.handleUserCreate)
	mux.HandleFunc("DELETE /v1/user/{userID}", s.handleUserDelete)
	mux.HandleFunc("POST /v1/set", s.handleSet)
	mux.HandleFunc("GET /v1/get", s.handleGet)
	mux.HandleFunc("DELETE /v1/delete", s.handleDelete)
	mux.HandleFunc("GET /v1/keys", s.handleKeys)
	mux.HandleFunc("GET /v1/ping", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	})

	// persistence endpoint
	mux.HandleFunc("POST /v1/user/snapshot", s.handleSaveSnapshot)   // POST {user_id} or header
	mux.HandleFunc("POST /v1/user/restore", s.handleRestoreSnapshot) // POST {user_id} or header

	// cluster
	mux.HandleFunc("POST /v1/cluster/join", s.handleClusterJoin)
	mux.HandleFunc("GET /v1/cluster/state", s.handleStat)

	// replication
	mux.HandleFunc("/v1/internal/replicate", s.handleInternalReplicate)
}

type valueResponse struct {
	Value string `json:"value"`
}

type keyResponse struct {
	Keys []string `json:"keys"`
}

func userIDFromHeader(r *http.Request) (string, error) {
	userID := r.Header.Get("X-User-Id")
	if userID == "" {
		return "", errMissingUser
	}
	return userID, nil
}

// forwardToOwner forwards the incoming HTTP request to the owner node and copies response back.
func (s *Server) forwardToOwner(owner cluster.NodeInfo, w http.ResponseWriter, r *http.Request) {
	client := &http.Client{Timeout: s.cfg.CmdTimeout}
	// build URL to same path on owner
	url := "http://" + owner.Addr + r.URL.Path

	if r.URL.RawQuery != "" {
		url += "?" + r.URL.RawQuery
	}
	// create new request
	var body io.Reader
	if r.Body != nil {
		// we need to read body and recreate because r.Body is a stream; safe approach: read all
		data, _ := io.ReadAll(r.Body)
		r.Body.Close()
		body = bytes.NewReader(data)
		// also restore r.Body for potential re-use (not necessary here)
	}
	req, err := http.NewRequest(r.Method, url, body)
	if err != nil {
		http.Error(w, "forward error", http.StatusInternalServerError)
		return
	}
	// copy headers, especially X-User-ID
	req.Header = r.Header.Clone()
	resp, err := client.Do(req)
	if err != nil {
		http.Error(w, "forward error", http.StatusBadGateway)
		return
	}

	defer resp.Body.Close()
	// copy status and headers
	for k, vv := range resp.Header {
		for _, v := range vv {
			w.Header().Add(k, v)
		}
	}
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, resp.Body)
}
