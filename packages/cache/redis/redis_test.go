package redis_test

import (
	"errors"
	"testing"

	"github.com/sudosylabs/proctor/packages/cache"
	rediscache "github.com/sudosylabs/proctor/packages/cache/redis"
)

func TestNewRejectsInvalidArguments(t *testing.T) {
	t.Parallel()

	if _, err := rediscache.New[string](nil, cache.StringCodec(), rediscache.Config{Namespace: "test"}); err == nil {
		t.Fatal("New() accepted a nil client")
	}
	if _, err := rediscache.New[string](nil, cache.StringCodec(), rediscache.Config{}); !errors.Is(err, cache.ErrInvalidNamespace) {
		t.Fatalf("New() namespace error = %v, want ErrInvalidNamespace", err)
	}
}
