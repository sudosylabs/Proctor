package cache

import (
	"context"
	"fmt"
	"math"
	"time"
)

// Capabilities describes optional operations and guarantees. Consumers should
// inspect it instead of selecting behavior from a concrete backend type.
type Capabilities struct {
	TTL            bool
	ConditionalSet bool
	AtomicCounter  bool
	Purge          bool
}

// Condition controls when Set may replace data.
type Condition uint8

const (
	// SetAlways writes regardless of whether the key exists.
	SetAlways Condition = iota
	// SetIfAbsent writes only when the key does not exist or has expired.
	SetIfAbsent
	// SetIfPresent writes only when the key currently exists.
	SetIfPresent
)

// SetOptions controls expiration and atomic conditional writes. A zero TTL
// means that the entry does not expire. Negative TTL values are invalid.
type SetOptions struct {
	TTL       time.Duration
	Condition Condition
}

// Validate checks whether the options have portable semantics.
func (o SetOptions) Validate() error {
	if o.TTL < 0 {
		return ErrInvalidTTL
	}
	switch o.Condition {
	case SetAlways, SetIfAbsent, SetIfPresent:
		return nil
	default:
		return fmt.Errorf("unknown set condition %d", o.Condition)
	}
}

// NormalizedTTL rounds a positive TTL up to a whole millisecond. The package
// applies this rule across backends because Redis expirations are
// millisecond-granular.
func NormalizedTTL(ttl time.Duration) (time.Duration, error) {
	if ttl < 0 {
		return 0, ErrInvalidTTL
	}
	if ttl == 0 {
		return 0, nil
	}
	const quantum = time.Millisecond
	if remainder := ttl % quantum; remainder != 0 {
		increment := quantum - remainder
		if ttl > time.Duration(math.MaxInt64)-increment {
			return 0, ErrInvalidTTL
		}
		ttl += increment
	}
	return ttl, nil
}

// CounterOptions controls the lifetime of the result of Add. Each successful
// Add refreshes the TTL. A zero TTL makes the counter persistent.
type CounterOptions struct {
	TTL time.Duration
}

// Validate checks the counter options.
func (o CounterOptions) Validate() error {
	if o.TTL < 0 {
		return ErrInvalidTTL
	}
	return nil
}

// Store is the minimal backend-independent cache contract.
type Store[V any] interface {
	Capabilities() Capabilities
	Get(ctx context.Context, key string) (V, error)
	Set(ctx context.Context, key string, value V, options SetOptions) error
	Delete(ctx context.Context, key string) error
}

// Counter is an optional atomic integer interface. Counter keys should use a
// separate namespace from values encoded by Store unless the codec also uses
// Redis-compatible base-10 integers.
type Counter interface {
	Add(ctx context.Context, key string, delta int64, options CounterOptions) (int64, error)
}

// Purger is an optional administrative interface that removes every key owned
// by a store instance.
type Purger interface {
	Purge(ctx context.Context) error
}
