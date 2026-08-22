# Proctor Cache

`cache` is a small Go cache library with consistent behavior across in-process
and Redis deployments. It is an independent module and can be used outside the
Proctor server.

Module:

```text
github.com/sudosylabs/proctor/packages/cache
```

The package is intentionally for disposable data. A cached value must never be
the only copy of data that the application cannot reconstruct.

## Design

- typed values through explicit codecs;
- `context.Context` on every I/O operation;
- identical key validation and millisecond TTL normalization across backends;
- atomic `SetIfAbsent` and `SetIfPresent` writes;
- optional atomic integer counters;
- capability reporting instead of backend type checks;
- backend conformance tests reusable by future adapters;
- caller-owned Redis connections.

The initial backends are:

- `memory`: concurrency-safe, encoded, entry/byte-bounded in-process LRU;
- `redis`: Redis standalone, Sentinel, or Cluster storage through `rueidis`.

The core interface deliberately excludes scanning, bulk deletion, distributed
locking, and loading behavior. Those have different safety and consistency
properties and should be added as narrow optional interfaces only when their
contract is clear.

## Install

```bash
go get github.com/sudosylabs/proctor/packages/cache
```

Import a backend separately:

```go
import (
    "github.com/sudosylabs/proctor/packages/cache"
    "github.com/sudosylabs/proctor/packages/cache/memory"
)
```

## Memory example

```go
store, err := memory.New(
    cache.JSONCodec[Session](),
    memory.Config{MaxEntries: 10_000, MaxBytes: 32 << 20},
)
if err != nil {
    return err
}

err = store.Set(ctx, "session/123", session, cache.SetOptions{
    TTL:       10 * time.Minute,
    Condition: cache.SetIfAbsent,
})
if errors.Is(err, cache.ErrNotStored) {
    // The key already exists.
}

session, err = store.Get(ctx, "session/123")
if errors.Is(err, cache.ErrNotFound) {
    // Rebuild it from authoritative storage.
}
```

The memory backend stores encoded bytes rather than retaining the original Go
value. This prevents callers from mutating cached slices, maps, or pointers
without a subsequent `Set`, and keeps behavior closer to remote backends. It
evicts the least recently used entries until both configured limits are met;
omitting `memory.Config` selects conservative defaults of 100,000 entries and
64 MiB of retained key/value bytes. Runtime object overhead is intentionally
outside the byte accounting.

## Redis example

```go
client, err := rueidis.NewClient(rueidis.ClientOption{
    InitAddress: []string{"127.0.0.1:6379"},
})
if err != nil {
    return err
}
defer client.Close()

store, err := rediscache.New(
    client,
    cache.JSONCodec[Session](),
    rediscache.Config{Namespace: "school-api"},
)
if err != nil {
    return err
}
```

The Redis adapter does not close the client. One client can be shared by
multiple independently namespaced cache instances.

## TTL semantics

- `TTL == 0` means no expiration.
- `TTL < 0` returns `cache.ErrInvalidTTL`.
- Positive TTLs are rounded up to the next whole millisecond.
- An expired entry behaves exactly like an absent entry.
- A successful counter update refreshes its TTL; a zero counter TTL removes
  any existing expiration.

These rules avoid differences between the nanosecond precision available to an
in-memory implementation and Redis' millisecond expiration commands.

## Conditional writes

`SetOptions.Condition` supports:

- `SetAlways`: create or replace;
- `SetIfAbsent`: atomically write only when absent;
- `SetIfPresent`: atomically write only when present.

A condition that does not match returns `cache.ErrNotStored`. This is an
expected result, not a backend failure.

## Atomic counters

Backends advertising `Capabilities().AtomicCounter` implement `cache.Counter`:

```go
counter := store.(cache.Counter)
value, err := counter.Add(ctx, "login-attempts/123", 1, cache.CounterOptions{
    TTL: 15 * time.Minute,
})
```

Counter values are stored as base-10 signed integers. Keep counter keys
separate from ordinary typed values unless the selected codec is intentionally
compatible with that representation. Overflow and non-integer data return
`cache.ErrInvalidValue`.

## Capabilities

Check capabilities when application behavior depends on optional operations:

```go
caps := store.Capabilities()
if caps.AtomicCounter {
    counter := store.(cache.Counter)
    // ...
}
```

The memory backend supports `cache.Purger`. The Redis backend intentionally
does not: safely purging a prefix requires topology-aware iteration and is not
equivalent across standalone and clustered Redis. Applications should use
versioned namespaces for broad invalidation.

## Testing

Run the module suite:

```bash
go test ./...
go test -race ./...
go vet ./...
```

Redis integration tests are opt-in and currently target a standalone test
instance. Docker CLI with Compose provides the repeatable local workflow:

```bash
make conformance-redis
```

This starts an isolated Redis container, waits for its health check, runs the
shared conformance suite, and removes the container even when the test fails.
It binds only to `127.0.0.1:16379`, disables persistence, and stores temporary
data in a container-local `tmpfs`.

For debugging, keep the service running explicitly:

```bash
make redis-up
CACHE_REDIS_ADDRESS=127.0.0.1:16379 go test ./redis -run Integration -count=1
make redis-down
```

The defaults can be overridden without editing Compose:

```bash
CACHE_REDIS_PORT=26379 \
CACHE_REDIS_IMAGE=redis:7.2-alpine \
make conformance-redis
```

When testing an existing server without Docker, optional environment variables
are `CACHE_REDIS_PASSWORD` and `CACHE_REDIS_DATABASE`.

Backend authors can use `cachetest.Run` to validate the shared contract.

## Compatibility

The module follows semantic versioning once tagged. Before `v1.0.0`, exported
APIs may change as the Proctor server exercises the package in production-like
workloads.

## License

Apache License 2.0. See [LICENSE](LICENSE).
