package cache

import (
	"errors"
	"fmt"
)

var (
	// ErrNotFound indicates that a key is absent or has expired.
	ErrNotFound = errors.New("cache: not found")
	// ErrNotStored indicates that a conditional write was not applied.
	ErrNotStored = errors.New("cache: conditional write not applied")
	// ErrInvalidKey indicates that a key is empty, too large, or contains
	// characters excluded by the portable key contract.
	ErrInvalidKey = errors.New("cache: invalid key")
	// ErrInvalidNamespace indicates that a backend namespace is empty, too
	// large, or contains characters excluded by the portable contract.
	ErrInvalidNamespace = errors.New("cache: invalid namespace")
	// ErrInvalidTTL indicates that a negative expiration was supplied.
	ErrInvalidTTL = errors.New("cache: invalid ttl")
	// ErrInvalidValue indicates that an operation cannot interpret the stored
	// representation, for example when a counter key contains non-integer data.
	ErrInvalidValue = errors.New("cache: invalid value")
	// ErrUnsupported indicates that a backend cannot provide an optional
	// operation or guarantee.
	ErrUnsupported = errors.New("cache: unsupported operation")
)

// OpError adds operation and key context while preserving errors.Is and
// errors.As behavior for the underlying error.
type OpError struct {
	Op  string
	Key string
	Err error
}

func (e *OpError) Error() string {
	if e == nil {
		return "<nil>"
	}
	if e.Key == "" {
		return fmt.Sprintf("cache %s: %v", e.Op, e.Err)
	}
	return fmt.Sprintf("cache %s %q: %v", e.Op, e.Key, e.Err)
}

func (e *OpError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

// Error annotates err with a cache operation and key. A nil error remains nil.
func Error(op, key string, err error) error {
	if err == nil {
		return nil
	}
	return &OpError{Op: op, Key: key, Err: err}
}
