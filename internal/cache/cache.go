package cache

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type Cache struct {
	users map[string]*UserCache
	mu    sync.RWMutex
	cfg   Config
}

func NewCache(cfg Config) *Cache {
	if cfg.JanitorInterval == 0 {
		cfg = DefaultConfig()
	}

	return &Cache{
		users: make(map[string]*UserCache),
		cfg:   cfg,
	}
}

func (c *Cache) CreateUser(userID string) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	_, ok := c.users[userID]
	if ok {
		return ErrUserExists
	}
	c.users[userID] = newUserCache(c.cfg)
	return nil
}

func (c *Cache) DeleteUser(userID string) error {
	c.mu.Lock()
	user, ok := c.users[userID]

	if !ok {
		c.mu.Unlock()
		return ErrUserNotFound
	}

	delete(c.users, userID)
	c.mu.Unlock()

	user.stop()
	return nil
}

func (c *Cache) Set(userID, key string, value []byte, ttl time.Duration) error {
	uc := c.getUser(userID)
	if uc == nil {
		return ErrUserNotFound
	}
	uc.set(key, value, ttl)
	return nil
}

func (c *Cache) Get(userID, key string) ([]byte, error) {
	uc := c.getUser(userID)
	if uc == nil {
		return nil, ErrUserNotFound
	}

	item, ok := uc.get(key)
	if !ok {
		return nil, ErrKeyNotFound
	}

	return item.Value, nil
}

func (c *Cache) Delete(userID, key string) error {
	uc := c.getUser(userID)
	if uc == nil {
		return ErrUserNotFound
	}
	uc.delete(key)
	return nil
}

func (c *Cache) ListKeys(userID string) ([]string, error) {
	uc := c.getUser(userID)
	if uc == nil {
		return nil, ErrUserNotFound
	}
	return uc.keys(), nil
}

func (c *Cache) getUser(userID string) *UserCache {
	c.mu.RLock()
	defer c.mu.RUnlock()
	uc, ok := c.users[userID]
	if !ok {
		return nil
	}
	return uc
}

// PersistedItem represents a single key/value + expiry in snapshot.
type PersistedItem struct {
	Key       string    `json:"key"`
	Value     []byte    `json:"value"`      // JSON handles base64 for []byte
	ExpiresAt time.Time `json:"expires_at"` // zero => no expiry
}

// UserSnapshot is a serializable representation of a user's items.
type UserSnapshot struct {
	UserID string          `json:"user_id"`
	Items  []PersistedItem `json:"items"`
}

// SnapshotUser returns a snapshot for the given userID.
// Caller can then SaveUserToFile(snapshot).
func (c *Cache) SnapshotUser(userID string) (*UserSnapshot, error) {
	uc := c.getUser(userID)
	if uc == nil {
		return nil, ErrUserNotFound
	}

	items, err := uc.Snapshot()
	if err != nil {
		return nil, err
	}

	snap := &UserSnapshot{UserID: userID, Items: make([]PersistedItem, 0, len(items))}

	for k, v := range items {
		snap.Items = append(snap.Items, PersistedItem{
			Key:       k,
			Value:     v.Value,
			ExpiresAt: v.ExpiresAt,
		})
	}
	return snap, nil
}

// SaveUserToFile writes snapshot to a JSON file under c.cfg.DataDir using atomic rename.
// Path: <DataDir>/user_<userID>.json
func (c *Cache) SaveUserToFile(snap *UserSnapshot) (string, error) {
	dir := c.cfg.DataDir
	if dir == "" {
		dir = "data"
	}

	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}

	filename := getUserFilePath(dir, snap.UserID)

	tmpFile, err := os.CreateTemp(dir, snap.UserID)
	if err != nil {
		return "", err
	}

	enc := json.NewEncoder(tmpFile)
	enc.SetIndent("", "  ")

	if err := enc.Encode(snap); err != nil {
		_ = tmpFile.Close()
		_ = os.Remove(tmpFile.Name())
		return "", err
	}

	if err := tmpFile.Close(); err != nil {
		_ = os.Remove(tmpFile.Name())
		return "", err
	}

	if err := os.Rename(tmpFile.Name(), filename); err != nil {
		_ = os.Remove(tmpFile.Name())
		return "", err
	}
	return filename, nil
}

// LoadUserFromFile loads snapshot for userID from file and returns snapshot.
func (c *Cache) LoadUserFromFile(userID string) (*UserSnapshot, error) {
	dir := c.cfg.DataDir
	if dir == "" {
		dir = "data"
	}

	filename := getUserFilePath(dir, userID)

	f, err := os.Open(filename)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var snap UserSnapshot
	dec := json.NewDecoder(f)
	if err := dec.Decode(&snap); err != nil {
		return nil, err
	}

	return &snap, nil
}

// RestoreUserFromSnapshot overwrites the user's existing cache with the provided snapshot.
// If the user does not exist, it will create it.
func (c *Cache) RestoreUserFromSnapshot(snap *UserSnapshot) error {

	// ensure user exists
	c.mu.Lock()

	uc, ok := c.users[snap.UserID]
	if !ok {
		uc = newUserCache(c.cfg)
		c.users[snap.UserID] = uc
	}
	c.mu.Unlock()

	// build map of key->item
	items := make(map[string]Item, len(snap.Items))
	now := time.Now()

	for _, item := range snap.Items {

		if !item.ExpiresAt.IsZero() && now.After(item.ExpiresAt) {
			continue
		}

		// copy value
		vCopy := make([]byte, len(item.Value))
		copy(vCopy, item.Value)

		items[item.Key] = Item{
			Value:     vCopy,
			ExpiresAt: item.ExpiresAt,
		}
	}

	// replace user cache contents with snapshot
	return uc.RestoreFromSnapshot(items)

}

// LoadAllUsersFromDir loads all snapshot files in DataDir and restores them into cache.
// It will skip invalid files and continue. Returns number of loaded snapshots and error if fatal.
func (c *Cache) LoadAllUsersFromDir() (int, error) {
	dir := c.cfg.DataDir
	if dir == "" {
		dir = "data"
	}

	if err := os.MkdirAll(dir, 0o755); err != nil {
		return 0, err
	}

	entries, err := os.ReadDir(dir)

	if err != nil {
		return 0, err
	}

	loaded := 0

	for _, e := range entries {
		if e.IsDir() {
			continue
		}

		filename := e.Name()
		if !isUserSnapshotFile(filename) {
			continue
		}

		userID := getUserIDFromFilename(filename)
		if userID == "" {
			continue
		}

		snap, err := c.LoadUserFromFile(userID)
		if err != nil {
			// skip invalid file
			continue
		}

		// restore snapshot
		c.RestoreUserFromSnapshot(snap)

		loaded++
	}

	return loaded, nil

}

func isUserSnapshotFile(filename string) bool {
	return strings.HasPrefix(filename, "user_") && strings.HasSuffix(filename, ".json") && len(filename) != len("user_.json")
}

func getUserIDFromFilename(filename string) string {
	if !isUserSnapshotFile(filename) {
		return ""
	}

	return strings.TrimSuffix(strings.TrimPrefix(filename, "user_"), ".json")
}

// helpers for filenames
func getUserFilePath(dir, userID string) string {
	// safe filename pattern: user_<userID>.json
	return filepath.Join(dir, fmt.Sprintf("user_%s.json", userID))
}
