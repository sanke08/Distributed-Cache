package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/sanke08/Distributed-Cache/internal/cluster"
)

type replicationTask struct {
	To        cluster.NodeInfo
	UserID    string
	Key       string
	Value     []byte
	TTLSec    int64
	Timestamp int64
	Attempts  int
}

type replicationManager struct {
	// queue and workers
	queue      chan replicationTask
	workers    int
	client     *http.Client
	wg         sync.WaitGroup
	stopCh     chan struct{}
	maxRetries int
	timeout    time.Duration
}

func newReplicationManager(workers int, queueSize int, timeout time.Duration, maxRetries int) *replicationManager {
	transport := &http.Transport{
		MaxIdleConns:        100,
		MaxIdleConnsPerHost: 100,
		IdleConnTimeout:     90 * time.Second,
	}
	return &replicationManager{
		queue:   make(chan replicationTask, queueSize),
		workers: workers,
		client: &http.Client{
			Transport: transport,
			Timeout:   timeout,
		},
		stopCh:     make(chan struct{}),
		maxRetries: maxRetries,
		timeout:    timeout,
	}
}

func (rm *replicationManager) start() {
	for i := 0; i < rm.workers; i++ {
		rm.wg.Add(1)
		go func() {
			defer rm.wg.Done()
			rm.workerLoop()
		}()
	}
}

func (rm *replicationManager) Stop(ctx context.Context) {
	// signal stop, then wait for workers
	close(rm.stopCh)
	done := make(chan struct{})

	go func() {
		rm.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
	case <-ctx.Done():
	}
}

// enqueue adds a task to the queue; non-blocking when full (drops task and logs)
func (rm *replicationManager) enqueue(t replicationTask) error {
	select {
	case rm.queue <- t:
		return nil
	default:
		// queue full
		log.Printf("[replication] queue full; dropping task for %s/%s -> %s", t.UserID, t.Key, t.To.Addr)
		return errors.New("replication queue full")
	}
}

func (rm *replicationManager) workerLoop() {
	for {
		select {
		case <-rm.stopCh:
			return
		case t := <-rm.queue:
			rm.processTask(t)
		}
	}
}

func (rm *replicationManager) processTask(t replicationTask) {
	// attempt with retries and exponential backoff
	attempt := t.Attempts
	for {
		if attempt > rm.maxRetries {
			log.Printf("[replication] max retries reached for %s/%s -> %s", t.UserID, t.Key, t.To.Addr)
			return
		}

		err := rm.doReplicateOnce(t)

		if err == nil {
			return
		}

		attempt++
		time.Sleep(2 * time.Second)
	}
}

type replicatePayload struct {
	UserID    string `json:"user_id"`
	Key       string `json:"key"`
	Value     []byte `json:"value"`
	TTLSec    int64  `json:"ttl_secs"`
	Timestamp int64  `json:"timestamp"`
}

func (rm *replicationManager) doReplicateOnce(t replicationTask) error {
	payload := replicatePayload{
		UserID:    t.UserID,
		Key:       t.Key,
		Value:     t.Value,
		TTLSec:    t.TTLSec,
		Timestamp: t.Timestamp,
	}

	jsonPayload, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	url := "http://" + t.To.Addr + "/v1/internal/replicate"

	ctx, cancel := context.WithTimeout(context.Background(), rm.timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(jsonPayload))
	if err != nil {
		return err
	}

	req.Header.Set("Content-Type", "application/json")

	resp, err := rm.client.Do(req)
	if err != nil {
		return err
	}

	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("replication failed with status code %d", resp.StatusCode)
	}

	return nil
}
