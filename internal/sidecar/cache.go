package sidecar

import (
	"container/list"
	"crypto/sha256"
	"encoding/hex"
	"sync"
)

// cacheKey builds a stable, collision-resistant key for the LRU and
// singleflight. We hash because outputs can be megabytes; we never compare
// keys for equality outside of map lookups.
func cacheKey(parts ...string) string {
	h := sha256.New()
	for _, p := range parts {
		h.Write([]byte(p))
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))
}

// lruCache is a tiny bounded map+list LRU. Goroutine-safe. Capacity <= 0
// disables caching entirely (every get misses).
type lruCache struct {
	mu    sync.Mutex
	cap   int
	ll    *list.List
	index map[string]*list.Element
}

type lruEntry struct {
	key string
	val string
}

func newLRU(capacity int) *lruCache {
	if capacity < 0 {
		capacity = 0
	}
	return &lruCache{
		cap:   capacity,
		ll:    list.New(),
		index: make(map[string]*list.Element),
	}
}

func (c *lruCache) get(key string) (string, bool) {
	if c.cap == 0 {
		return "", false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if el, ok := c.index[key]; ok {
		c.ll.MoveToFront(el)
		return el.Value.(*lruEntry).val, true
	}
	return "", false
}

func (c *lruCache) put(key, val string) {
	if c.cap == 0 {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if el, ok := c.index[key]; ok {
		el.Value.(*lruEntry).val = val
		c.ll.MoveToFront(el)
		return
	}
	el := c.ll.PushFront(&lruEntry{key: key, val: val})
	c.index[key] = el
	for c.ll.Len() > c.cap {
		old := c.ll.Back()
		if old == nil {
			break
		}
		c.ll.Remove(old)
		delete(c.index, old.Value.(*lruEntry).key)
	}
}
