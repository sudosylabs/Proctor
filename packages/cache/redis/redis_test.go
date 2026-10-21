package redis_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/packages/cache"
	rediscache "github.com/sudosylabs/proctor/packages/cache/redis"
)

func TestNewRejectsInvalidArguments(t *testing.T) {
	t.Parallel()

	_, err := rediscache.New[string](nil, cache.StringCodec(), rediscache.Config{Namespace: "test"})
	require.Error(t, err, "New() accepted a nil client")
	_, err = rediscache.New[string](nil, cache.StringCodec(), rediscache.Config{})
	require.ErrorIs(t, err, cache.ErrInvalidNamespace, "New() namespace error")
}
