package redis_test

import (
	"context"
	"os"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/redis/rueidis"
	"github.com/sudosylabs/proctor/packages/cache"
	"github.com/sudosylabs/proctor/packages/cache/cachetest"
	rediscache "github.com/sudosylabs/proctor/packages/cache/redis"
)

func TestIntegrationConformance(t *testing.T) {
	address := os.Getenv("CACHE_REDIS_ADDRESS")
	if address == "" {
		t.Skip("set CACHE_REDIS_ADDRESS to run Redis integration tests")
	}

	database := 0
	if raw := os.Getenv("CACHE_REDIS_DATABASE"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil {
			t.Fatalf("invalid CACHE_REDIS_DATABASE: %v", err)
		}
		database = parsed
	}
	client, err := rueidis.NewClient(rueidis.ClientOption{
		InitAddress: []string{address},
		Password:    os.Getenv("CACHE_REDIS_PASSWORD"),
		SelectDB:    database,
	})
	if err != nil {
		t.Fatalf("create Redis client: %v", err)
	}
	t.Cleanup(client.Close)
	if err := client.Do(t.Context(), client.B().Ping().Build()).Error(); err != nil {
		t.Fatalf("ping Redis: %v", err)
	}

	base := "proctor-cache-integration-" + strconv.FormatInt(time.Now().UnixNano(), 10)
	var sequence atomic.Uint64
	cachetest.Run(t, func(t *testing.T) cache.Store[string] {
		t.Helper()
		namespace := base + "-" + strconv.FormatUint(sequence.Add(1), 10)
		store, newErr := rediscache.New(client, cache.StringCodec(), rediscache.Config{Namespace: namespace})
		if newErr != nil {
			t.Fatalf("New() error = %v", newErr)
		}
		t.Cleanup(func() {
			deleteNamespace(t, client, namespace+":*")
		})
		return store
	})
}

func deleteNamespace(t *testing.T, client rueidis.Client, pattern string) {
	t.Helper()
	var cursor uint64
	for {
		entry, err := client.Do(context.Background(), client.B().Scan().
			Cursor(cursor).
			Match(pattern).
			Count(100).
			Build()).AsScanEntry()
		if err != nil {
			t.Errorf("cleanup scan: %v", err)
			return
		}
		if len(entry.Elements) > 0 {
			if err := client.Do(context.Background(), client.B().Del().Key(entry.Elements...).Build()).Error(); err != nil {
				t.Errorf("cleanup delete: %v", err)
				return
			}
		}
		cursor = entry.Cursor
		if cursor == 0 {
			return
		}
	}
}
