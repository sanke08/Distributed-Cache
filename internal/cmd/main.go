package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/sanke08/Distributed-Cache/internal/cache"
	"github.com/sanke08/Distributed-Cache/internal/server"
)

func main() {
	httpAddr := flag.String("addr", ":8080", "http listen addr")
	tcpAddr := flag.String("tcp", ":9000", "tcp listen addr")
	nodeID := flag.String("id", "", "node id (optional)")
	join := flag.String("join", "", "leader http addr to join, e.g. http://127.0.0.1:8080")
	dataDir := flag.String("data", "data", "data directory for snapshots")
	flag.Parse()

	cfg := cache.DefaultConfig()
	cfg.DataDir = *dataDir

	c := cache.NewCache(cfg)

	// restore any saved users on startup (best-effort)
	if _, err := c.LoadAllUsersFromDir(); err != nil {
		fmt.Println("warning: unable to load snapshots:", err)
	}

	srvConfig := server.ServerConfig{
		HTTPAddr:              *httpAddr,
		TCPAddr:               *tcpAddr,
		CmdTimeout:            5 * time.Second,
		ReadTimeout:           10 * time.Second,
		WriteTimeout:          10 * time.Second,
		IdealTimeout:          120 * time.Second,
		ShutdownTimeout:       5 * time.Second,
		NodeID:                *nodeID,
		JoinAddr:              *join,
		ClusterReplicas:       10,
		PollInterval:          2 * time.Second,
		ReplicationWorkers:    4,
		ReplicationQueueSize:  100,
		ReplicationTimeout:    300 * time.Millisecond,
		ReplicationMaxRetries: 3,
	}

	s := server.NewServer(c, srvConfig)

	if err := s.Start(); err != nil {
		log.Fatalf("start server: %v", err)
	}

	fmt.Printf("server started at %s (tcp %s). join=%s\n", *httpAddr, *tcpAddr, *join)

	// wait for signal
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	<-sig
	fmt.Println("shutting down...")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := s.Shutdown(ctx); err != nil {
		log.Fatalf("shutdown: %v", err)
	}
	fmt.Println("stopped")

}
