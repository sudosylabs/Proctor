// Package memory provides a concurrency-safe in-process cache backend.
package memory

import (
	"container/heap"
	"container/list"
	"context"
	"fmt"
	"math"
	"strconv"
	"sync"
	"time"

	"github.com/sudosylabs/proctor/packages/cache"
)

// entry is one stored key. A value entry sits on the recency list and is
// evictable; a counter entry (counter set) is not, and while it expires it
// sits on the expiry heap at heapIndex instead (-1 when it never expires).
type entry struct {
	key       string
	data      []byte
	expiresAt time.Time
	recency   *list.Element
	counter   bool
	heapIndex int
}

func (e entry) expired(now time.Time) bool {
	return !e.expiresAt.IsZero() && !now.Before(e.expiresAt)
}

// Store is an encoded in-process cache. Encoding values before storing them
// keeps its copy/isolation behavior aligned with remote backends.
//
// Values and counters share one key space but not one budget. Values are
// evicted least recently used first. Counters usually carry security state
// such as rate-limit windows, so a flood of new keys must never push a live
// counter out: they occupy their own MaxCounters slots, leave only by
// expiring or being deleted, and Add refuses a new counter with
// cache.ErrCapacity while every slot is live.
type Store[V any] struct {
	mu            sync.Mutex
	values        map[string]*entry
	recency       *list.List
	retainedBytes int64
	counters      int
	expiries      expiryHeap
	config        Config
	codec         cache.Codec[V]
	now           func() time.Time
}

// Config bounds retained values and encoded key/value bytes, and separately
// the number of atomic counters. Go map, list, and object overhead are
// deliberately excluded from MaxBytes, and counters from both value bounds.
// A zero MaxCounters disables counters: the store then does not advertise
// AtomicCounter and Add returns cache.ErrUnsupported.
type Config struct {
	MaxEntries  int
	MaxBytes    int64
	MaxCounters int
}

// DefaultConfig provides conservative bounds for callers that do not need to
// tune the in-process adapter.
func DefaultConfig() Config {
	return Config{MaxEntries: 100_000, MaxBytes: 64 << 20, MaxCounters: 100_000}
}

func (c Config) validate() error {
	if c.MaxEntries < 1 {
		return fmt.Errorf("memory cache: max entries must be greater than zero")
	}
	if c.MaxBytes < 1 {
		return fmt.Errorf("memory cache: max bytes must be greater than zero")
	}
	if c.MaxCounters < 0 {
		return fmt.Errorf("memory cache: max counters must not be negative")
	}
	return nil
}

// New constructs an empty bounded in-process LRU store. Supplying no Config
// selects DefaultConfig; more than one Config is rejected.
func New[V any](codec cache.Codec[V], configurations ...Config) (*Store[V], error) {
	if codec == nil {
		return nil, fmt.Errorf("memory cache: codec must not be nil")
	}
	configuration := DefaultConfig()
	if len(configurations) > 1 {
		return nil, fmt.Errorf("memory cache: at most one configuration is allowed")
	}
	if len(configurations) == 1 {
		configuration = configurations[0]
	}
	if err := configuration.validate(); err != nil {
		return nil, err
	}
	return &Store[V]{
		values:  make(map[string]*entry),
		recency: list.New(),
		config:  configuration,
		codec:   codec,
		now:     time.Now,
	}, nil
}

func (s *Store[V]) Capabilities() cache.Capabilities {
	return cache.Capabilities{
		TTL:            true,
		ConditionalSet: true,
		AtomicCounter:  s.config.MaxCounters > 0,
		Purge:          true,
	}
}

func (s *Store[V]) Get(ctx context.Context, key string) (V, error) {
	var zero V
	if err := ctx.Err(); err != nil {
		return zero, cache.Error("get", key, err)
	}
	if err := cache.ValidateKey(key); err != nil {
		return zero, cache.Error("get", key, err)
	}

	s.mu.Lock()
	item, found := s.values[key]
	if found && item.expired(s.now()) {
		s.removeLocked(item)
		found = false
	}
	var data []byte
	if found {
		if !item.counter {
			s.recency.MoveToFront(item.recency)
		}
		data = append([]byte(nil), item.data...)
	}
	s.mu.Unlock()

	if !found {
		return zero, cache.Error("get", key, cache.ErrNotFound)
	}
	value, err := s.codec.Decode(data)
	if err != nil {
		return zero, cache.Error("get", key, err)
	}
	return value, nil
}

func (s *Store[V]) Set(ctx context.Context, key string, value V, options cache.SetOptions) error {
	if err := ctx.Err(); err != nil {
		return cache.Error("set", key, err)
	}
	if err := cache.ValidateKey(key); err != nil {
		return cache.Error("set", key, err)
	}
	if err := options.Validate(); err != nil {
		return cache.Error("set", key, err)
	}
	ttl, err := cache.NormalizedTTL(options.TTL)
	if err != nil {
		return cache.Error("set", key, err)
	}
	data, err := s.codec.Encode(value)
	if err != nil {
		return cache.Error("set", key, err)
	}
	data = append([]byte(nil), data...)

	now := s.now()
	s.mu.Lock()
	defer s.mu.Unlock()

	current, found := s.values[key]
	if found && current.expired(now) {
		s.removeLocked(current)
		found = false
	}
	if options.Condition == cache.SetIfAbsent && found {
		return cache.Error("set", key, cache.ErrNotStored)
	}
	if options.Condition == cache.SetIfPresent && !found {
		return cache.Error("set", key, cache.ErrNotStored)
	}

	var expiresAt time.Time
	if ttl > 0 {
		expiresAt = now.Add(ttl)
	}
	if int64(len(key)+len(data)) > s.config.MaxBytes {
		return cache.Error("set", key, fmt.Errorf("%w: encoded entry exceeds memory cache byte limit", cache.ErrInvalidValue))
	}
	if found && current.counter {
		s.removeLocked(current)
		found = false
	}
	if found {
		s.retainedBytes -= int64(len(current.data))
		current.data = data
		current.expiresAt = expiresAt
		s.retainedBytes += int64(len(data))
		s.recency.MoveToFront(current.recency)
	} else {
		item := &entry{key: key, data: data, expiresAt: expiresAt}
		item.recency = s.recency.PushFront(item)
		s.values[key] = item
		s.retainedBytes += int64(len(key) + len(data))
	}
	s.evictLocked()
	return nil
}

func (s *Store[V]) Delete(ctx context.Context, key string) error {
	if err := ctx.Err(); err != nil {
		return cache.Error("delete", key, err)
	}
	if err := cache.ValidateKey(key); err != nil {
		return cache.Error("delete", key, err)
	}

	s.mu.Lock()
	if item, found := s.values[key]; found {
		s.removeLocked(item)
	}
	s.mu.Unlock()
	return nil
}

// Add atomically changes an integer stored as base-10 text. Its expiration is
// a fixed window: options set it only when the counter is created or has none.
// A counter is never evicted; when every MaxCounters slot holds a live counter,
// creating another fails with cache.ErrCapacity so callers fail closed.
func (s *Store[V]) Add(ctx context.Context, key string, delta int64, options cache.CounterOptions) (int64, error) {
	if err := ctx.Err(); err != nil {
		return 0, cache.Error("add", key, err)
	}
	if s.config.MaxCounters == 0 {
		return 0, cache.Error("add", key, cache.ErrUnsupported)
	}
	if err := cache.ValidateKey(key); err != nil {
		return 0, cache.Error("add", key, err)
	}
	if err := options.Validate(); err != nil {
		return 0, cache.Error("add", key, err)
	}
	ttl, err := cache.NormalizedTTL(options.TTL)
	if err != nil {
		return 0, cache.Error("add", key, err)
	}

	now := s.now()
	s.mu.Lock()
	defer s.mu.Unlock()

	item, found := s.values[key]
	if found && item.expired(now) {
		s.removeLocked(item)
		found = false
	}

	var current int64
	if found {
		current, err = strconv.ParseInt(string(item.data), 10, 64)
		if err != nil {
			return 0, cache.Error("add", key, fmt.Errorf("%w: counter is not an integer", cache.ErrInvalidValue))
		}
	}
	if (delta > 0 && current > math.MaxInt64-delta) ||
		(delta < 0 && current < math.MinInt64-delta) {
		return 0, cache.Error("add", key, fmt.Errorf("%w: integer overflow", cache.ErrInvalidValue))
	}
	current += delta

	var expiresAt time.Time
	if found && !item.expiresAt.IsZero() {
		expiresAt = item.expiresAt
	} else if ttl > 0 {
		expiresAt = now.Add(ttl)
	}
	if !found || !item.counter {
		s.expireCountersLocked(now)
		if s.counters >= s.config.MaxCounters {
			return 0, cache.Error("add", key, fmt.Errorf("%w: every counter slot holds a live counter", cache.ErrCapacity))
		}
	}
	data := []byte(strconv.FormatInt(current, 10))
	if found && !item.counter {
		// An integer value becomes a counter and leaves the evictable budget.
		s.removeLocked(item)
		found = false
	}
	if found {
		item.data = data
		if !item.expiresAt.Equal(expiresAt) {
			item.expiresAt = expiresAt
			heap.Push(&s.expiries, item)
		}
		return current, nil
	}
	item = &entry{key: key, data: data, expiresAt: expiresAt, counter: true, heapIndex: -1}
	s.values[key] = item
	s.counters++
	if !expiresAt.IsZero() {
		heap.Push(&s.expiries, item)
	}
	return current, nil
}

// Purge atomically removes every item from this store instance.
func (s *Store[V]) Purge(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return cache.Error("purge", "", err)
	}
	s.mu.Lock()
	s.values = make(map[string]*entry)
	s.recency.Init()
	s.retainedBytes = 0
	s.counters = 0
	s.expiries = nil
	s.mu.Unlock()
	return nil
}

// evictLocked restores the value bounds. It never removes a counter.
func (s *Store[V]) evictLocked() {
	if len(s.values)-s.counters <= s.config.MaxEntries && s.retainedBytes <= s.config.MaxBytes {
		return
	}
	now := s.now()
	for element := s.recency.Back(); element != nil; {
		previous := element.Prev()
		item := element.Value.(*entry)
		if item.expired(now) {
			s.removeLocked(item)
		}
		element = previous
	}
	for len(s.values)-s.counters > s.config.MaxEntries || s.retainedBytes > s.config.MaxBytes {
		oldest := s.recency.Back()
		if oldest == nil {
			return
		}
		s.removeLocked(oldest.Value.(*entry))
	}
}

// expireCountersLocked removes every counter whose window has closed.
func (s *Store[V]) expireCountersLocked(now time.Time) {
	for len(s.expiries) > 0 && s.expiries[0].expired(now) {
		s.removeLocked(s.expiries[0])
	}
}

func (s *Store[V]) removeLocked(item *entry) {
	delete(s.values, item.key)
	if item.counter {
		s.counters--
		if item.heapIndex >= 0 {
			heap.Remove(&s.expiries, item.heapIndex)
		}
		return
	}
	s.recency.Remove(item.recency)
	s.retainedBytes -= int64(len(item.key) + len(item.data))
}

// expiryHeap orders expiring counters by deadline, earliest first.
type expiryHeap []*entry

func (h expiryHeap) Len() int           { return len(h) }
func (h expiryHeap) Less(i, j int) bool { return h[i].expiresAt.Before(h[j].expiresAt) }
func (h expiryHeap) Swap(i, j int) {
	h[i], h[j] = h[j], h[i]
	h[i].heapIndex = i
	h[j].heapIndex = j
}

func (h *expiryHeap) Push(value any) {
	item := value.(*entry)
	item.heapIndex = len(*h)
	*h = append(*h, item)
}

func (h *expiryHeap) Pop() any {
	old := *h
	item := old[len(old)-1]
	old[len(old)-1] = nil
	item.heapIndex = -1
	*h = old[:len(old)-1]
	return item
}

var (
	_ cache.Store[any] = (*Store[any])(nil)
	_ cache.Counter    = (*Store[any])(nil)
	_ cache.Purger     = (*Store[any])(nil)
)
