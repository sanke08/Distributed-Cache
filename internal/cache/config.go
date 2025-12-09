package cache

import "time"

type Config struct {
	// JanitorInterval is the TTL cleanup interval for each user.
	JanitorInterval time.Duration

	// InitialCapacity for user maps (optional hint).
	InitialCapacity int

	MaxEntries int    // per-user LRU capacity; 0 means unlimited
	DataDir    string // directory for per-user persistence
}

func DefaultConfig() Config {
	return Config{
		JanitorInterval: 5 * time.Second,
		InitialCapacity: 64,
		MaxEntries:      100,    // unlimited by default
		DataDir:         "data", // default data dir
	}
}
