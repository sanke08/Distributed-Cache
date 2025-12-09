package cache

import (
	"container/list"
	"sync"
	"sync/atomic"
	"time"
)

type Item struct {
	Value     []byte
	ExpiresAt time.Time
}

type lruEntry struct {
	key string
}

func (item Item) isExpired(now time.Time) bool {
	if item.ExpiresAt.IsZero() {
		return false
	}
	return now.After(item.ExpiresAt)
}

type UserCache struct {
	mu    sync.RWMutex
	items map[string]Item
	cfg   Config

	stopOnce  sync.Once
	stopCh    chan struct{}
	stoppedCH chan struct{}

	// LRU data structures
	lruList *list.List               // front = most recent, back = least recent
	lruMap  map[string]*list.Element // key -> element in lruList

	// stats (simple)
	hits   int64
	misses int64
}

func newUserCache(cfg Config) *UserCache {
	userCache := &UserCache{
		items:     make(map[string]Item, cfg.InitialCapacity),
		cfg:       cfg,
		stopCh:    make(chan struct{}),
		stoppedCH: make(chan struct{}),
		lruList:   list.New(),
		lruMap:    make(map[string]*list.Element, cfg.InitialCapacity),
	}
	go userCache.janitor()
	return userCache
}

func (uc *UserCache) stop() {
	uc.stopOnce.Do(func() {
		close(uc.stopCh)
		<-uc.stoppedCH
	})
}

func (uc *UserCache) get(key string) (Item, bool) {
	uc.mu.RLock()
	item, ok := uc.items[key]

	if !ok {
		atomic.AddInt64(&uc.misses, 1)
		uc.mu.RUnlock()
		return Item{}, false
	}

	//  If expired, remove and return not found
	if item.isExpired(time.Now()) {
		uc.mu.RUnlock()
		uc.mu.Lock()
		delete(uc.items, key)
		uc.removeFromLRU(key)
		uc.mu.Unlock()
		atomic.AddInt64(&uc.misses, 1)
		return Item{}, false
	}

	uc.mu.RUnlock()

	// move to front in LRU
	uc.mu.Lock()
	uc.moveToFront(key)
	uc.mu.Unlock()

	atomic.AddInt64(&uc.hits, 1)

	valueCopy := make([]byte, len(item.Value))
	copy(valueCopy, item.Value)
	item.Value = valueCopy

	return item, true

}

func (uc *UserCache) set(key string, value []byte, ttl time.Duration) {
	var expires time.Time
	if ttl > 0 {
		expires = time.Now().Add(ttl)
	}

	// input := []byte("secret")
	// cache.set("k", input, 5*time.Minute)
	// input[0] = 'X' // update cache value if not copies

	// copy value to avoid caller mutating
	vCopy := make([]byte, len(value))
	copy(vCopy, value)

	uc.mu.Lock()
	defer uc.mu.Unlock()

	if _, ok := uc.items[key]; ok {
		uc.items[key] = Item{Value: vCopy, ExpiresAt: expires}
		uc.moveToFront(key)
		return
	}

	// Insert new
	uc.items[key] = Item{Value: vCopy, ExpiresAt: expires}
	uc.addToLRU(key)

	// Evict if necessary
	if uc.cfg.MaxEntries > 0 {
		for len(uc.items) > uc.cfg.MaxEntries {
			// evict back item
			back := uc.lruList.Back()
			if back == nil {
				break
			}

			entry := back.Value.(*lruEntry)
			delete(uc.items, entry.key)
			uc.removeFromLRUElement(back)
		}
	}
}

func (uc *UserCache) delete(key string) {
	uc.mu.Lock()
	defer uc.mu.Unlock()
	delete(uc.items, key)
	uc.removeFromLRU(key)
}

func (uc *UserCache) keys() []string {
	now := time.Now()

	uc.mu.RLock()
	defer uc.mu.RUnlock()

	ks := make([]string, 0, len(uc.items))

	for k, v := range uc.items {
		if !v.isExpired(now) {
			ks = append(ks, k)
		}
	}
	return ks
}

// ---------- LRU helper methods (must be called with lock) ----------

// addToLRU inserts key at front. Caller must hold uc.mu lock.
func (uc *UserCache) addToLRU(key string) {
	if el, ok := uc.lruMap[key]; ok {
		uc.lruList.MoveToFront(el)
		return
	}

	// Add new entry - wrap key in lruEntry struct
	el := uc.lruList.PushFront(&lruEntry{key: key})
	uc.lruMap[key] = el
}

// moveToFront moves an existing key's element to front. Caller must hold uc.mu lock.
func (uc *UserCache) moveToFront(key string) {
	if el, ok := uc.lruMap[key]; ok {
		uc.lruList.MoveToFront(el)
	}
}

// removeFromLRU removes a key from LRU structures if present. Caller must hold uc.mu lock.
func (uc *UserCache) removeFromLRU(key string) {
	if el, ok := uc.lruMap[key]; ok {
		uc.removeFromLRUElement(el)
	}
}

// removeFromLRUElement removes the provided element from list and map. Caller must hold uc.mu lock.
func (uc *UserCache) removeFromLRUElement(el *list.Element) {
	ent := el.Value.(*lruEntry)
	delete(uc.lruMap, ent.key)
	uc.lruList.Remove(el)
}

// janitor periodically scans for expired keys.
// Strategy: on each tick, gather expired keys under RLock, then obtain Lock and delete.
func (uc *UserCache) janitor() {
	ticker := time.NewTicker(uc.cfg.JanitorInterval)
	defer func() {
		ticker.Stop()
		close(uc.stoppedCH)
	}()

	for {
		select {
		case <-uc.stopCh:
			return
		case <-ticker.C:
			now := time.Now()
			var expiredKeys []string

			// gather expired under RLock
			uc.mu.RLock()
			for k, v := range uc.items {
				if v.isExpired(now) {
					expiredKeys = append(expiredKeys, k)
				}
			}
			uc.mu.RUnlock()

			if len(expiredKeys) == 0 {
				continue
			}

			uc.mu.Lock()
			now = time.Now()
			for _, key := range expiredKeys {
				v, ok := uc.items[key]

				if ok && v.isExpired(now) {
					delete(uc.items, key)
					uc.removeFromLRU(key)
				}
			}
			uc.mu.Unlock()
		}
	}
}

// Snapshot returns a snapshot of current items for persistence.
// It copies items to avoid holding locks during I/O.
func (uc *UserCache) Snapshot() (map[string]Item, error) {
	uc.mu.RLock()
	defer uc.mu.RUnlock()

	out := make(map[string]Item, len(uc.items))

	for k, v := range uc.items {
		vCopy := make([]byte, len(v.Value))
		copy(vCopy, v.Value)
		out[k] = Item{Value: vCopy, ExpiresAt: v.ExpiresAt}
	}

	return out, nil
}

// RestoreFromSnapshot replaces the user cache contents with provided items.
// Caller should ensure this is used carefully; this will overwrite existing items.
func (uc *UserCache) RestoreFromSnapshot(items map[string]Item) error {
	uc.mu.Lock()
	defer uc.mu.Unlock()

	uc.items = make(map[string]Item, len(items))
	uc.lruList = list.New()
	uc.lruMap = make(map[string]*list.Element, len(items))

	for k, v := range items {
		// copy value
		vCopy := make([]byte, len(v.Value))
		copy(vCopy, v.Value)
		uc.items[k] = Item{Value: vCopy, ExpiresAt: v.ExpiresAt}
		// add to LRU (treat snapshot insertion as most-recent)
		el := uc.lruList.PushFront(&lruEntry{key: k})
		uc.lruMap[k] = el
	}
	return nil
}
