// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package localcachelayer_test

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/packages/cache"
	"github.com/sudosylabs/proctor/packages/cache/memory"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
	"github.com/sudosylabs/proctor/server/store/localcachelayer"
)

type controlledCacheEntry struct {
	value     []byte
	expiresAt time.Time
}

type controlledCache struct {
	now     time.Time
	entries map[string]controlledCacheEntry
}

type failingCache struct{}

func (failingCache) Get(context.Context, string) ([]byte, bool, error) {
	return nil, false, errors.New("cache unavailable")
}
func (failingCache) Set(context.Context, string, []byte, time.Duration) error {
	return errors.New("cache unavailable")
}
func (failingCache) Delete(context.Context, string) error { return errors.New("cache unavailable") }

type invalidationNetwork struct {
	handlers [2]func(context.Context, string) error
	drop     bool
}

type invalidationEndpoint struct {
	network *invalidationNetwork
	index   int
}

func (e invalidationEndpoint) RegisterAcademicPeriod(handler func(context.Context, string) error) error {
	e.network.handlers[e.index] = handler
	return nil
}

func (e invalidationEndpoint) BroadcastAcademicPeriod(ctx context.Context, id string) error {
	if e.network.drop {
		return nil
	}
	peer := e.network.handlers[1-e.index]
	if peer == nil {
		return errors.New("peer handler is not registered")
	}
	// Duplicate delivery proves the peer handler is idempotent.
	if err := peer(ctx, id); err != nil {
		return err
	}
	return peer(ctx, id)
}

func newControlledCache() *controlledCache {
	return &controlledCache{
		now:     time.Date(2026, time.August, 8, 12, 0, 0, 0, time.UTC),
		entries: map[string]controlledCacheEntry{},
	}
}

func (c *controlledCache) Get(_ context.Context, key string) ([]byte, bool, error) {
	entry, ok := c.entries[key]
	if !ok || !c.now.Before(entry.expiresAt) {
		delete(c.entries, key)
		return nil, false, nil
	}
	return append([]byte(nil), entry.value...), true, nil
}

func (c *controlledCache) Set(_ context.Context, key string, value []byte, ttl time.Duration) error {
	c.entries[key] = controlledCacheEntry{
		value:     append([]byte(nil), value...),
		expiresAt: c.now.Add(ttl),
	}
	return nil
}

func (c *controlledCache) Delete(_ context.Context, key string) error {
	delete(c.entries, key)
	return nil
}

func (c *controlledCache) advance(duration time.Duration) { c.now = c.now.Add(duration) }

type academicPeriodStub struct {
	store.AcademicPeriodStore
	period      *model.AcademicPeriod
	getAttempts int
	saveErr     error
}

func (s *academicPeriodStub) Get(context.Context, model.AcademicPeriodID) (*model.AcademicPeriod, error) {
	s.getAttempts++
	return s.period, nil
}

func (s *academicPeriodStub) Update(_ context.Context, input *store.AcademicPeriodUpdate) (*model.AcademicPeriod, error) {
	if s.saveErr != nil {
		return nil, s.saveErr
	}
	period := input.Period
	s.period = period
	return period, nil
}

type rootStub struct {
	store.Store
	academicPeriod store.AcademicPeriodStore
	roleBinding    store.RoleBindingStore
}

func (s *rootStub) AcademicPeriod() store.AcademicPeriodStore { return s.academicPeriod }
func (s *rootStub) RoleBinding() store.RoleBindingStore       { return s.roleBinding }

type roleBindingStub struct {
	store.RoleBindingStore
	attempts int
}

func (s *roleBindingStub) ListActiveByUser(context.Context, model.UserID, time.Time) ([]*model.RoleBinding, error) {
	s.attempts++
	return nil, nil
}

func TestLocalCacheReturnsDefensiveAcademicPeriodCopies(t *testing.T) {
	t.Parallel()

	wantName := "2026-2027"
	period := &model.AcademicPeriod{
		ID:          model.NewAcademicPeriodID(),
		Owner:       model.NewInstitutionAcademicPeriodOwner(model.NewInstitutionID()),
		Name:        "2026-2027",
		DisplayName: wantName,
		CreatedAt:   time.Now().UTC(),
		UpdatedAt:   time.Now().UTC(),
		StartsAt:    time.Now().UTC(),
		EndsAt:      time.Now().UTC().Add(time.Hour),
		Revision:    1,
	}
	underlying := &academicPeriodStub{period: period}
	cache, err := newMemoryCache(8)
	require.NoError(t, err)
	layer, err := localcachelayer.New(
		&rootStub{academicPeriod: underlying},
		cache,
		localcachelayer.Policy{TTL: time.Minute},
		localcachelayer.NopRecorder{},
		localcachelayer.NopInvalidationFanout{},
	)
	require.NoError(t, err)

	first, err := layer.AcademicPeriod().Get(context.Background(), period.ID)
	require.NoError(t, err)
	first.DisplayName = "consumer mutation"
	second, err := layer.AcademicPeriod().Get(context.Background(), period.ID)
	require.NoError(t, err)
	require.NotSame(t, first, second, "Get() returned a shared mutable AcademicPeriod pointer")
	require.NotSame(t, period, second, "Get() returned a shared mutable AcademicPeriod pointer")
	require.Equal(t, wantName, second.DisplayName, "cached DisplayName")
	require.Equal(t, 1, underlying.getAttempts, "underlying Get() attempts")
}

func TestLocalCacheInvalidatesAfterSuccessfulAcademicPeriodMutation(t *testing.T) {
	t.Parallel()

	period := validAcademicPeriod()
	underlying := &academicPeriodStub{period: period}
	layer := newLayer(t, underlying, time.Minute)
	ctx := context.Background()
	_, err := layer.AcademicPeriod().Get(ctx, period.ID)
	require.NoError(t, err)
	updated := *period
	updated.DisplayName = "Updated period"
	_, err = layer.AcademicPeriod().Update(ctx, academicPeriodUpdate(&updated))
	require.NoError(t, err)
	got, err := layer.AcademicPeriod().Get(ctx, period.ID)
	require.NoError(t, err)
	require.Equal(t, updated.DisplayName, got.DisplayName, "DisplayName")
	require.Equal(t, 2, underlying.getAttempts, "underlying Get() attempts")
}

func TestLocalCacheRejectsInvalidCachedAcademicPeriod(t *testing.T) {
	t.Parallel()

	period := validAcademicPeriod()
	underlying := &academicPeriodStub{period: period}
	cache := newControlledCache()
	require.NoError(t, cache.Set(
		context.Background(),
		"store/academic_period/id/"+period.ID.String(),
		[]byte(`{}`),
		time.Minute,
	))
	layer := newLayerWithCache(t, underlying, cache, time.Minute)
	got, err := layer.AcademicPeriod().Get(context.Background(), period.ID)
	require.NoError(t, err)
	require.Equal(t, period.ID, got.ID, "Get() = %#v after %d authoritative reads, want valid period after 1 read", got, underlying.getAttempts)
	require.Equal(t, 1, underlying.getAttempts, "Get() = %#v after %d authoritative reads, want valid period after 1 read", got, underlying.getAttempts)
}

func TestLocalCacheRecoversAfterMissedPeerInvalidationAtTTL(t *testing.T) {
	t.Parallel()

	const ttl = time.Minute
	period := validAcademicPeriod()
	underlying := &academicPeriodStub{period: period}
	cacheA := newControlledCache()
	cacheB := newControlledCache()
	network := &invalidationNetwork{}
	layerA := newLayerWithCacheAndFanout(t, underlying, cacheA, ttl, invalidationEndpoint{network, 0})
	layerB := newLayerWithCacheAndFanout(t, underlying, cacheB, ttl, invalidationEndpoint{network, 1})
	ctx := context.Background()

	_, err := layerB.AcademicPeriod().Get(ctx, period.ID)
	require.NoError(t, err)
	delivered := *period
	delivered.DisplayName = "Delivered from node A"
	_, err = layerA.AcademicPeriod().Update(ctx, academicPeriodUpdate(&delivered))
	require.NoError(t, err)
	peerFresh, err := layerB.AcademicPeriod().Get(ctx, period.ID)
	require.NoError(t, err)
	require.Equal(t, delivered.DisplayName, peerFresh.DisplayName, "delivered invalidation DisplayName")

	network.drop = true
	stale, err := layerB.AcademicPeriod().Get(ctx, period.ID)
	require.NoError(t, err)
	updated := *period
	updated.DisplayName = "Missed on node B"
	_, err = layerA.AcademicPeriod().Update(ctx, academicPeriodUpdate(&updated))
	require.NoError(t, err)

	stillStale, err := layerB.AcademicPeriod().Get(ctx, period.ID)
	require.NoError(t, err)
	require.Equal(t, stale.DisplayName, stillStale.DisplayName, "pre-TTL DisplayName")
	cacheB.advance(ttl)
	recovered, err := layerB.AcademicPeriod().Get(ctx, period.ID)
	require.NoError(t, err)
	require.Equal(t, updated.DisplayName, recovered.DisplayName, "post-TTL DisplayName")
	require.Equal(t, 3, underlying.getAttempts, "underlying Get() attempts")
}

type coordinatedAcademicPeriodStore struct {
	store.AcademicPeriodStore
	mu          sync.Mutex
	period      *model.AcademicPeriod
	getAttempts int
	readStarted chan struct{}
	allowRead   chan struct{}
}

func (s *coordinatedAcademicPeriodStore) Get(context.Context, model.AcademicPeriodID) (*model.AcademicPeriod, error) {
	s.mu.Lock()
	s.getAttempts++
	period := *s.period
	first := s.getAttempts == 1
	s.mu.Unlock()
	if first {
		close(s.readStarted)
		<-s.allowRead
	}
	return &period, nil
}

func (s *coordinatedAcademicPeriodStore) Update(_ context.Context, input *store.AcademicPeriodUpdate) (*model.AcademicPeriod, error) {
	period := input.Period
	s.mu.Lock()
	s.period = period
	s.mu.Unlock()
	return period, nil
}

func TestLocalCacheConcurrentFillCannotUndoMutationInvalidation(t *testing.T) {
	t.Parallel()

	period := validAcademicPeriod()
	underlying := &coordinatedAcademicPeriodStore{
		period: period, readStarted: make(chan struct{}), allowRead: make(chan struct{}),
	}
	layer := newLayer(t, underlying, time.Minute)
	readDone := make(chan error, 1)
	go func() {
		_, err := layer.AcademicPeriod().Get(context.Background(), period.ID)
		readDone <- err
	}()
	<-underlying.readStarted
	updated := *period
	updated.DisplayName = "Committed while cache fill was in flight"
	_, err := layer.AcademicPeriod().Update(context.Background(), academicPeriodUpdate(&updated))
	require.NoError(t, err)
	close(underlying.allowRead)
	require.NoError(t, (<-readDone))
	got, err := layer.AcademicPeriod().Get(context.Background(), period.ID)
	require.NoError(t, err)
	require.Equal(t, updated.DisplayName, got.DisplayName, "DisplayName")
	underlying.mu.Lock()
	attempts := underlying.getAttempts
	underlying.mu.Unlock()
	require.Equal(t, 2, attempts, "authoritative reads")
}

func TestLocalCacheDoesNotServeHitForCanceledContext(t *testing.T) {
	t.Parallel()

	period := validAcademicPeriod()
	underlying := &academicPeriodStub{period: period}
	layer := newLayer(t, underlying, time.Minute)
	_, err := layer.AcademicPeriod().Get(context.Background(), period.ID)
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = layer.AcademicPeriod().Get(ctx, period.ID)
	require.ErrorIs(t, err, context.Canceled, "Get() error")
}

func TestLocalCacheBypassesAuthoritativeSecurityReads(t *testing.T) {
	t.Parallel()

	bindings := &roleBindingStub{}
	cache, err := newMemoryCache(8)
	require.NoError(t, err)
	layer, err := localcachelayer.New(
		&rootStub{roleBinding: bindings},
		cache,
		localcachelayer.DefaultPolicy(),
		localcachelayer.NopRecorder{},
		localcachelayer.NopInvalidationFanout{},
	)
	require.NoError(t, err)
	for range 2 {
		_, err := layer.RoleBinding().ListActiveByUser(
			context.Background(),
			model.NewUserID(),
			model.NowUTC(),
		)
		require.NoError(t, err)
	}
	require.Equal(t, 2, bindings.attempts, "authoritative role-binding reads")
}

func TestLocalCacheFailureFallsBackAndRecorderCannotBreakReads(t *testing.T) {
	t.Parallel()

	period := validAcademicPeriod()
	underlying := &academicPeriodStub{period: period}
	layer, err := localcachelayer.New(
		&rootStub{academicPeriod: underlying},
		failingCache{},
		localcachelayer.DefaultPolicy(),
		localcachelayer.RecorderFunc(func(localcachelayer.Operation, localcachelayer.Outcome) {
			panic("metrics backend failure")
		}),
		localcachelayer.NopInvalidationFanout{},
	)
	require.NoError(t, err)
	for range 2 {
		_, err := layer.AcademicPeriod().Get(context.Background(), period.ID)
		require.NoError(t, err)
	}
	require.Equal(t, 2, underlying.getAttempts, "authoritative reads")
}

func TestLocalCacheRecordsClosedHitAndMissOutcomes(t *testing.T) {
	t.Parallel()

	period := validAcademicPeriod()
	underlying := &academicPeriodStub{period: period}
	cache, err := newMemoryCache(8)
	require.NoError(t, err)
	var observations []string
	layer, err := localcachelayer.New(
		&rootStub{academicPeriod: underlying},
		cache,
		localcachelayer.DefaultPolicy(),
		localcachelayer.RecorderFunc(func(operation localcachelayer.Operation, outcome localcachelayer.Outcome) {
			observations = append(observations, operation.String()+":"+string(outcome))
		}),
		localcachelayer.NopInvalidationFanout{},
	)
	require.NoError(t, err)
	for range 2 {
		_, err := layer.AcademicPeriod().Get(context.Background(), period.ID)
		require.NoError(t, err)
	}
	want := []string{"academic_period.get:miss", "academic_period.get:hit"}
	require.Len(t, observations, len(want), "observations")
	require.Equal(t, want[0], observations[0], "observations")
	require.Equal(t, want[1], observations[1], "observations")
}

func TestMemoryCacheBoundsSizeTTLAndByteAliasing(t *testing.T) {
	t.Parallel()

	cache, err := newMemoryCache(2)
	require.NoError(t, err)
	ctx := context.Background()
	require.Error(t, cache.Set(ctx, "invalid", []byte("value"), 0), "Set() accepted an unbounded TTL")
	first := []byte("first")
	require.NoError(t, cache.Set(ctx, "a", first, time.Minute))
	first[0] = 'X'
	require.NoError(t, cache.Set(ctx, "b", []byte("second"), time.Minute))
	require.NoError(t, cache.Set(ctx, "c", []byte("third"), time.Minute))
	_, found, err := cache.Get(ctx, "a")
	require.NoError(t, err, "evicted key a = found")
	require.False(t, found, "evicted key a = found")
	value, found, err := cache.Get(ctx, "b")
	require.NoError(t, err, "key b")
	require.True(t, found, "key b")
	require.Equal(t, "second", string(value), "key b")
	value[0] = 'X'
	again, found, err := cache.Get(ctx, "b")
	require.NoError(t, err, "aliased key b")
	require.True(t, found, "aliased key b")
	require.Equal(t, "second", string(again), "aliased key b")

	require.NoError(t, cache.Set(ctx, "expires", []byte("soon"), time.Millisecond))
	deadline := time.Now().Add(time.Second)
	for {
		_, found, err = cache.Get(ctx, "expires")
		require.NoError(t, err)
		if !found {
			break
		}
		require.False(t, time.Now().After(deadline), "cache entry did not expire within one second")
		time.Sleep(time.Millisecond)
	}
}

func TestMemoryCacheEvictsLeastRecentlyUsedEntry(t *testing.T) {
	t.Parallel()

	cache, err := newMemoryCache(2)
	require.NoError(t, err)
	ctx := context.Background()
	for _, key := range []string{"a", "b"} {
		require.NoError(t, cache.Set(ctx, key, []byte(key), time.Minute))
	}
	_, found, err := cache.Get(ctx, "a")
	require.NoError(t, err, "refresh key a = found")
	require.True(t, found, "refresh key a = found")
	require.NoError(t, cache.Set(ctx, "c", []byte("c"), time.Minute))
	_, found, err = cache.Get(ctx, "b")
	require.NoError(t, err, "least-recent key b = found")
	require.False(t, found, "least-recent key b = found")
	_, found, err = cache.Get(ctx, "a")
	require.NoError(t, err, "recent key a = found")
	require.True(t, found, "recent key a = found")
}

func TestLocalCacheRejectsUnboundedConstruction(t *testing.T) {
	t.Parallel()

	_, err := newMemoryCache(0)
	require.Error(t, err, "newMemoryCache() accepted zero entries")
	cache, err := newMemoryCache(1)
	require.NoError(t, err)
	validRoot := &rootStub{academicPeriod: &academicPeriodStub{period: validAcademicPeriod()}}
	tests := []localcachelayer.Policy{
		{TTL: 0},
		{TTL: 6 * time.Minute},
	}
	for _, policy := range tests {
		_, err = localcachelayer.New(validRoot, cache, policy, localcachelayer.NopRecorder{}, localcachelayer.NopInvalidationFanout{})
		require.Error(t, err, "New() accepted policy %#v", policy)
	}
	_, err = localcachelayer.New(nil, cache, localcachelayer.DefaultPolicy(), localcachelayer.NopRecorder{}, localcachelayer.NopInvalidationFanout{})
	require.Error(t, err, "New() accepted nil store")
	_, err = localcachelayer.New(validRoot, nil, localcachelayer.DefaultPolicy(), localcachelayer.NopRecorder{}, localcachelayer.NopInvalidationFanout{})
	require.Error(t, err, "New() accepted nil cache")
	_, err = localcachelayer.New(validRoot, cache, localcachelayer.DefaultPolicy(), nil, localcachelayer.NopInvalidationFanout{})
	require.Error(t, err, "New() accepted nil recorder")
	_, err = localcachelayer.New(validRoot, cache, localcachelayer.DefaultPolicy(), localcachelayer.NopRecorder{}, nil)
	require.Error(t, err, "New() accepted nil invalidation fan-out")
}

func TestLocalCacheGeneratedForwardingIsCurrent(t *testing.T) {
	t.Parallel()

	temporary := t.TempDir()
	generatedPath := filepath.Join(temporary, "forwarding_gen.go")
	command := exec.Command(
		"go", "run", "../storetest/layergen", "-layer", "localcache", "-source", "..", "-output", generatedPath,
	)
	output, err := command.CombinedOutput()
	require.NoError(t, err, "regenerate forwarding: %v\n%s", err, output)
	want, err := os.ReadFile("forwarding_gen.go")
	require.NoError(t, err)
	got, err := os.ReadFile(generatedPath)
	require.NoError(t, err)
	require.Equal(t, string(want), string(got), "forwarding_gen.go is stale; run go generate ./store/localcachelayer")
}

func validAcademicPeriod() *model.AcademicPeriod {
	now := time.Now().UTC()
	return &model.AcademicPeriod{
		ID:          model.NewAcademicPeriodID(),
		Owner:       model.NewInstitutionAcademicPeriodOwner(model.NewInstitutionID()),
		Name:        "2026-2027",
		DisplayName: "2026-2027",
		CreatedAt:   now,
		UpdatedAt:   now,
		StartsAt:    now,
		EndsAt:      now.Add(time.Hour),
		Revision:    1,
	}
}

func newMemoryCache(maxEntries int) (localcachelayer.Cache, error) {
	store, err := memory.New(cache.BytesCodec(), memory.Config{
		MaxEntries: maxEntries,
		MaxBytes:   64 << 20,
	})
	if err != nil {
		return nil, err
	}
	return localcachelayer.NewCacheAdapter(store)
}

func newLayer(t *testing.T, periods store.AcademicPeriodStore, ttl time.Duration) *localcachelayer.Layer {
	t.Helper()
	cache, err := newMemoryCache(8)
	require.NoError(t, err)
	return newLayerWithCache(t, periods, cache, ttl)
}

func newLayerWithCache(
	t *testing.T,
	periods store.AcademicPeriodStore,
	cache localcachelayer.Cache,
	ttl time.Duration,
) *localcachelayer.Layer {
	return newLayerWithCacheAndFanout(t, periods, cache, ttl, localcachelayer.NopInvalidationFanout{})
}

func newLayerWithCacheAndFanout(
	t *testing.T,
	periods store.AcademicPeriodStore,
	cache localcachelayer.Cache,
	ttl time.Duration,
	invalidation localcachelayer.InvalidationFanout,
) *localcachelayer.Layer {
	t.Helper()
	layer, err := localcachelayer.New(
		&rootStub{academicPeriod: periods},
		cache,
		localcachelayer.Policy{TTL: ttl},
		localcachelayer.NopRecorder{},
		invalidation,
	)
	require.NoError(t, err)
	return layer
}

func academicPeriodUpdate(period *model.AcademicPeriod) *store.AcademicPeriodUpdate {
	return &store.AcademicPeriodUpdate{Period: period, AuditEventID: model.NewAuditEventID(), AuditAt: 1}
}
