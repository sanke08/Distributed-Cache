package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/sanke08/Distributed-Cache/internal/cache"
	"github.com/sanke08/Distributed-Cache/internal/cluster"
)

type ServerConfig struct {
	HTTPAddr        string
	TCPAddr         string
	ReadTimeout     time.Duration // http server read timeout
	WriteTimeout    time.Duration
	IdealTimeout    time.Duration
	CmdTimeout      time.Duration // per-command timeout for TCP (and used for HTTP handlers)
	ShutdownTimeout time.Duration

	// ClusterState
	NodeID          string // optional node id
	ClusterReplicas int    // number of virtual nodes per actual node
	JoinAddr        string // leader address to join, e.g., "http://leader:8080"
	PollInterval    time.Duration
}

type Server struct {
	cache *cache.Cache
	cfg   ServerConfig

	httpSrv *http.Server
	tcpLn   net.Listener

	wg sync.WaitGroup

	started bool
	mu      sync.Mutex

	cluster *cluster.ClusterState

	shutdownOnce sync.Once
	shutdownCh   chan struct{}
}

func NewServer(c *cache.Cache, cfg ServerConfig) *Server {
	if cfg.CmdTimeout == 0 {
		cfg.CmdTimeout = 5 * time.Second
	}

	return &Server{
		cache:      c,
		cfg:        cfg,
		shutdownCh: make(chan struct{}),
	}
}

func (s *Server) Start() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.started {
		return errors.New("server: already started")
	}

	// create NodeInfo for current server
	id := s.cfg.NodeID
	if id == "" {
		// derive id from addr (simple); in real system use UUID
		id = s.cfg.HTTPAddr
	}

	self := cluster.NodeInfo{ID: id, Addr: s.cfg.HTTPAddr}

	// initialize cluster state
	cs := cluster.NewClusterState(self, s.cfg.ClusterReplicas)
	s.cluster = cs

	// If join addr provided, join leader and start polling
	if s.cfg.JoinAddr != "" {
		if err := s.joinLeader(s.cfg.JoinAddr, self); err != nil {
			log.Printf("[server] join leader failed: %v", err)
			// proceed as standalone node (optionally error out)
		} else {
			// start poller to keep state updated
			stop := make(chan struct{})
			go cs.PollLeader(s.cfg.JoinAddr, s.cfg.PollInterval, stop)
			// when server shutdown close stop via s.shutdownCh handling later (not shown)
			// we'll close stop in Shutdown
		}
	} else {
		// current node is leader
	}

	// setup HTTP mux and handlers with cluster-aware routing
	mux := http.NewServeMux()
	s.httpSrv = &http.Server{
		Addr:         s.cfg.HTTPAddr,
		Handler:      mux,
		ReadTimeout:  s.cfg.ReadTimeout,
		WriteTimeout: s.cfg.WriteTimeout,
		IdleTimeout:  s.cfg.IdealTimeout,
	}

	// register handlers (http.go uses s.cluster)
	registerHTTPHandlers(mux, s)
	s.wg.Add(1)

	// Start HTTP in a goroutine
	go func() {
		defer s.wg.Done()
		log.Printf("[server] HTTP listening on %s", s.cfg.HTTPAddr)
		err := s.httpSrv.ListenAndServe()
		if err != nil {
			log.Printf("[server] HTTP error: %v", err)
		}
	}()

	// start TCP
	ln, err := net.Listen("tcp", s.cfg.TCPAddr)
	if err != nil {
		s.httpSrv.Shutdown(context.Background())
		return err
	}

	s.tcpLn = ln

	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		log.Printf("[server] TCP listening on %s", s.cfg.TCPAddr)
		s.acceptLoop()
	}()

	s.started = true
	return nil
}

// joinLeader posts /v1/cluster/join to leader and updates local cluster state from response.
func (s *Server) joinLeader(leaderAddr string, self cluster.NodeInfo) error {
	// leaderAddr example: "http://127.0.0.1:8080"
	client := &http.Client{Timeout: 3 * time.Second}
	bodyBytes, _ := json.Marshal(self)
	resp, err := client.Post(leaderAddr+"/v1/cluster/join", "application/json", bytes.NewReader(bodyBytes))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("join failed: %s", resp.Status)
	}
	// parse payload with same structure as cluster.Snapshot (replicas, nodes, ring)
	var payload struct {
		Replicas int                         `json:"replicas"`
		Nodes    []cluster.NodeInfo          `json:"nodes"`
		Ring     map[string]cluster.NodeInfo `json:"ring"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return err
	}
	// replace local cluster state
	s.cluster.ReplaceFromPayload(payload.Replicas, payload.Nodes, payload.Ring)
	return nil
}

// Shutdown Gracefully stops servers
func (s *Server) Shutdown(ctx context.Context) error {
	s.shutdownOnce.Do(func() {
		close(s.shutdownCh)
	})

	// General  HTTP shutdown
	httpDone := make(chan struct{})

	go func() {
		_ = s.httpSrv.Shutdown(ctx)
		close(httpDone)
	}()

	// Close TCP listener to stop accept loop
	if s.tcpLn != nil {
		_ = s.tcpLn.Close()
	}

	// wait for goroutines
	done := make(chan struct{})
	go func() {
		s.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		// all goroutines finished
	case <-ctx.Done():
		// times out
	}
	// allow remaining cleanup
	return nil
}
