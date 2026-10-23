package memory

import "time"

// SetClock replaces the store's clock so tests can close counter windows.
func SetClock[V any](store *Store[V], now func() time.Time) {
	store.mu.Lock()
	defer store.mu.Unlock()
	store.now = now
}
