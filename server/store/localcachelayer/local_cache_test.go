// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

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

func (s *academicPeriodStub) Get(context.Context, string) (*model.AcademicPeriod, error) {
	s.getAttempts++
	return s.period, nil
}

func (s *academicPeriodStub) Save(_ context.Context, period *model.AcademicPeriod) (*model.AcademicPeriod, error) {
	if s.saveErr != nil {
		return nil, s.saveErr
	}
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

func (s *roleBindingStub) ListActiveByUser(context.Context, string, int64) ([]*model.RoleBinding, error) {
	s.attempts++
	return nil, nil
}

func TestLocalCacheReturnsDefensiveAcademicPeriodCopies(t *testing.T) {
	t.Parallel()

	wantName := "2026-2027"
	period := &model.AcademicPeriod{
		ID:            model.NewAcademicPeriodID(),
		InstitutionID: model.NewInstitutionID(),
		Name:          "2026-2027",
		DisplayName:   wantName,
		CreatedAt:     time.Now().UTC(),
		UpdatedAt:     time.Now().UTC(),
		StartsAt:      time.Now().UTC(),
		EndsAt:        time.Now().UTC().Add(time.Hour),
		Revision:      1,
	}
	underlying := &academicPeriodStub{period: period}
	cache, err := localcachelayer.NewMemoryCache(8)
	if err != nil {
		t.Fatal(err)
	}
	layer, err := localcachelayer.New(
		&rootStub{academicPeriod: underlying},
		cache,
		localcachelayer.Policy{TTL: time.Minute},
		localcachelayer.NopRecorder{},
		localcachelayer.NopInvalidationFanout{},
	)
	if err != nil {
		t.Fatal(err)
	}

	first, err := layer.AcademicPeriod().Get(context.Background(), period.ID.String())
	if err != nil {
		t.Fatal(err)
	}
	first.DisplayName = "consumer mutation"
	second, err := layer.AcademicPeriod().Get(context.Background(), period.ID.String())
	if err != nil {
		t.Fatal(err)
	}
	if second == first || second == period {
		t.Fatal("Get() returned a shared mutable AcademicPeriod pointer")
	}
	if second.DisplayName != wantName {
		t.Fatalf("cached DisplayName = %q, want %q", second.DisplayName, wantName)
	}
	if underlying.getAttempts != 1 {
		t.Fatalf("underlying Get() attempts = %d, want 1", underlying.getAttempts)
	}
}

func TestLocalCacheInvalidatesAfterSuccessfulAcademicPeriodMutation(t *testing.T) {
	t.Parallel()

	period := validAcademicPeriod()
	underlying := &academicPeriodStub{period: period}
	layer := newLayer(t, underlying, time.Minute)
	ctx := context.Background()
	if _, err := layer.AcademicPeriod().Get(ctx, period.ID.String()); err != nil {
		t.Fatal(err)
	}
	updated := *period
	updated.DisplayName = "Updated period"
	if _, err := layer.AcademicPeriod().Save(ctx, &updated); err != nil {
		t.Fatal(err)
	}
	got, err := layer.AcademicPeriod().Get(ctx, period.ID.String())
	if err != nil {
		t.Fatal(err)
	}
	if got.DisplayName != updated.DisplayName {
		t.Fatalf("DisplayName = %q, want %q", got.DisplayName, updated.DisplayName)
	}
	if underlying.getAttempts != 2 {
		t.Fatalf("underlying Get() attempts = %d, want 2 after invalidation", underlying.getAttempts)
	}
}

func TestLocalCacheRejectsInvalidCachedAcademicPeriod(t *testing.T) {
	t.Parallel()

	period := validAcademicPeriod()
	underlying := &academicPeriodStub{period: period}
	cache := newControlledCache()
	if err := cache.Set(
		context.Background(),
		"store/academic_period/id/"+period.ID.String(),
		[]byte(`{}`),
		time.Minute,
	); err != nil {
		t.Fatal(err)
	}
	layer := newLayerWithCache(t, underlying, cache, time.Minute)
	got, err := layer.AcademicPeriod().Get(context.Background(), period.ID.String())
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != period.ID || underlying.getAttempts != 1 {
		t.Fatalf("Get() = %#v after %d authoritative reads, want valid period after 1 read", got, underlying.getAttempts)
	}
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

	if _, err := layerB.AcademicPeriod().Get(ctx, period.ID.String()); err != nil {
		t.Fatal(err)
	}
	delivered := *period
	delivered.DisplayName = "Delivered from node A"
	if _, err := layerA.AcademicPeriod().Save(ctx, &delivered); err != nil {
		t.Fatal(err)
	}
	peerFresh, err := layerB.AcademicPeriod().Get(ctx, period.ID.String())
	if err != nil {
		t.Fatal(err)
	}
	if peerFresh.DisplayName != delivered.DisplayName {
		t.Fatalf("delivered invalidation DisplayName = %q, want %q", peerFresh.DisplayName, delivered.DisplayName)
	}

	network.drop = true
	stale, err := layerB.AcademicPeriod().Get(ctx, period.ID.String())
	if err != nil {
		t.Fatal(err)
	}
	updated := *period
	updated.DisplayName = "Missed on node B"
	if _, err := layerA.AcademicPeriod().Save(ctx, &updated); err != nil {
		t.Fatal(err)
	}

	stillStale, err := layerB.AcademicPeriod().Get(ctx, period.ID.String())
	if err != nil {
		t.Fatal(err)
	}
	if stillStale.DisplayName != stale.DisplayName {
		t.Fatalf("pre-TTL DisplayName = %q, want bounded stale value %q", stillStale.DisplayName, stale.DisplayName)
	}
	cacheB.advance(ttl)
	recovered, err := layerB.AcademicPeriod().Get(ctx, period.ID.String())
	if err != nil {
		t.Fatal(err)
	}
	if recovered.DisplayName != updated.DisplayName {
		t.Fatalf("post-TTL DisplayName = %q, want %q", recovered.DisplayName, updated.DisplayName)
	}
	if underlying.getAttempts != 3 {
		t.Fatalf("underlying Get() attempts = %d, want 3 across delivery and expiry", underlying.getAttempts)
	}
}

type coordinatedAcademicPeriodStore struct {
	store.AcademicPeriodStore
	mu          sync.Mutex
	period      *model.AcademicPeriod
	getAttempts int
	readStarted chan struct{}
	allowRead   chan struct{}
}

func (s *coordinatedAcademicPeriodStore) Get(context.Context, string) (*model.AcademicPeriod, error) {
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

func (s *coordinatedAcademicPeriodStore) Save(_ context.Context, period *model.AcademicPeriod) (*model.AcademicPeriod, error) {
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
		_, err := layer.AcademicPeriod().Get(context.Background(), period.ID.String())
		readDone <- err
	}()
	<-underlying.readStarted
	updated := *period
	updated.DisplayName = "Committed while cache fill was in flight"
	if _, err := layer.AcademicPeriod().Save(context.Background(), &updated); err != nil {
		t.Fatal(err)
	}
	close(underlying.allowRead)
	if err := <-readDone; err != nil {
		t.Fatal(err)
	}
	got, err := layer.AcademicPeriod().Get(context.Background(), period.ID.String())
	if err != nil {
		t.Fatal(err)
	}
	if got.DisplayName != updated.DisplayName {
		t.Fatalf("DisplayName = %q, want %q", got.DisplayName, updated.DisplayName)
	}
	underlying.mu.Lock()
	attempts := underlying.getAttempts
	underlying.mu.Unlock()
	if attempts != 2 {
		t.Fatalf("authoritative reads = %d, want 2 after invalidated in-flight fill", attempts)
	}
}

func TestLocalCacheDoesNotServeHitForCanceledContext(t *testing.T) {
	t.Parallel()

	period := validAcademicPeriod()
	underlying := &academicPeriodStub{period: period}
	layer := newLayer(t, underlying, time.Minute)
	if _, err := layer.AcademicPeriod().Get(context.Background(), period.ID.String()); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := layer.AcademicPeriod().Get(ctx, period.ID.String()); !errors.Is(err, context.Canceled) {
		t.Fatalf("Get() error = %v, want context.Canceled", err)
	}
}

func TestLocalCacheBypassesAuthoritativeSecurityReads(t *testing.T) {
	t.Parallel()

	bindings := &roleBindingStub{}
	cache, err := localcachelayer.NewMemoryCache(8)
	if err != nil {
		t.Fatal(err)
	}
	layer, err := localcachelayer.New(
		&rootStub{roleBinding: bindings},
		cache,
		localcachelayer.DefaultPolicy(),
		localcachelayer.NopRecorder{},
		localcachelayer.NopInvalidationFanout{},
	)
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if _, err := layer.RoleBinding().ListActiveByUser(
			context.Background(),
			model.NewUserID().String(),
			time.Now().UnixMilli(),
		); err != nil {
			t.Fatal(err)
		}
	}
	if bindings.attempts != 2 {
		t.Fatalf("authoritative role-binding reads = %d, want 2", bindings.attempts)
	}
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
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if _, err := layer.AcademicPeriod().Get(context.Background(), period.ID.String()); err != nil {
			t.Fatal(err)
		}
	}
	if underlying.getAttempts != 2 {
		t.Fatalf("authoritative reads = %d, want 2 when cache fails", underlying.getAttempts)
	}
}

func TestLocalCacheRecordsClosedHitAndMissOutcomes(t *testing.T) {
	t.Parallel()

	period := validAcademicPeriod()
	underlying := &academicPeriodStub{period: period}
	cache, err := localcachelayer.NewMemoryCache(8)
	if err != nil {
		t.Fatal(err)
	}
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
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if _, err := layer.AcademicPeriod().Get(context.Background(), period.ID.String()); err != nil {
			t.Fatal(err)
		}
	}
	want := []string{"academic_period.get:miss", "academic_period.get:hit"}
	if len(observations) != len(want) || observations[0] != want[0] || observations[1] != want[1] {
		t.Fatalf("observations = %v, want %v", observations, want)
	}
}

func TestMemoryCacheBoundsSizeTTLAndByteAliasing(t *testing.T) {
	t.Parallel()

	cache, err := localcachelayer.NewMemoryCache(2)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := cache.Set(ctx, "invalid", []byte("value"), 0); err == nil {
		t.Fatal("Set() accepted an unbounded TTL")
	}
	first := []byte("first")
	if err := cache.Set(ctx, "a", first, time.Minute); err != nil {
		t.Fatal(err)
	}
	first[0] = 'X'
	if err := cache.Set(ctx, "b", []byte("second"), time.Minute); err != nil {
		t.Fatal(err)
	}
	if err := cache.Set(ctx, "c", []byte("third"), time.Minute); err != nil {
		t.Fatal(err)
	}
	if _, found, err := cache.Get(ctx, "a"); err != nil || found {
		t.Fatalf("evicted key a = found %v, error %v", found, err)
	}
	value, found, err := cache.Get(ctx, "b")
	if err != nil || !found || string(value) != "second" {
		t.Fatalf("key b = %q, found %v, error %v", value, found, err)
	}
	value[0] = 'X'
	again, found, err := cache.Get(ctx, "b")
	if err != nil || !found || string(again) != "second" {
		t.Fatalf("aliased key b = %q, found %v, error %v", again, found, err)
	}

	if err := cache.Set(ctx, "expires", []byte("soon"), time.Millisecond); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(time.Second)
	for {
		_, found, err = cache.Get(ctx, "expires")
		if err != nil {
			t.Fatal(err)
		}
		if !found {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("cache entry did not expire within one second")
		}
		time.Sleep(time.Millisecond)
	}
}

func TestLocalCacheRejectsUnboundedConstruction(t *testing.T) {
	t.Parallel()

	if _, err := localcachelayer.NewMemoryCache(0); err == nil {
		t.Fatal("NewMemoryCache() accepted zero entries")
	}
	cache, err := localcachelayer.NewMemoryCache(1)
	if err != nil {
		t.Fatal(err)
	}
	validRoot := &rootStub{academicPeriod: &academicPeriodStub{period: validAcademicPeriod()}}
	tests := []localcachelayer.Policy{
		{TTL: 0},
		{TTL: 6 * time.Minute},
	}
	for _, policy := range tests {
		if _, err := localcachelayer.New(validRoot, cache, policy, localcachelayer.NopRecorder{}, localcachelayer.NopInvalidationFanout{}); err == nil {
			t.Fatalf("New() accepted policy %#v", policy)
		}
	}
	if _, err := localcachelayer.New(nil, cache, localcachelayer.DefaultPolicy(), localcachelayer.NopRecorder{}, localcachelayer.NopInvalidationFanout{}); err == nil {
		t.Fatal("New() accepted nil store")
	}
	if _, err := localcachelayer.New(validRoot, nil, localcachelayer.DefaultPolicy(), localcachelayer.NopRecorder{}, localcachelayer.NopInvalidationFanout{}); err == nil {
		t.Fatal("New() accepted nil cache")
	}
	if _, err := localcachelayer.New(validRoot, cache, localcachelayer.DefaultPolicy(), nil, localcachelayer.NopInvalidationFanout{}); err == nil {
		t.Fatal("New() accepted nil recorder")
	}
	if _, err := localcachelayer.New(validRoot, cache, localcachelayer.DefaultPolicy(), localcachelayer.NopRecorder{}, nil); err == nil {
		t.Fatal("New() accepted nil invalidation fan-out")
	}
}

func TestLocalCacheGeneratedForwardingIsCurrent(t *testing.T) {
	t.Parallel()

	temporary := t.TempDir()
	generatedPath := filepath.Join(temporary, "forwarding_gen.go")
	command := exec.Command(
		"go", "run", "../storetest/layergen", "-layer", "localcache", "-source", "..", "-output", generatedPath,
	)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("regenerate forwarding: %v\n%s", err, output)
	}
	want, err := os.ReadFile("forwarding_gen.go")
	if err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(generatedPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(want) {
		t.Fatal("forwarding_gen.go is stale; run go generate ./store/localcachelayer")
	}
}

func validAcademicPeriod() *model.AcademicPeriod {
	now := time.Now().UTC()
	return &model.AcademicPeriod{
		ID:            model.NewAcademicPeriodID(),
		InstitutionID: model.NewInstitutionID(),
		Name:          "2026-2027",
		DisplayName:   "2026-2027",
		CreatedAt:     now,
		UpdatedAt:     now,
		StartsAt:      now,
		EndsAt:        now.Add(time.Hour),
		Revision:      1,
	}
}

func newLayer(t *testing.T, periods store.AcademicPeriodStore, ttl time.Duration) *localcachelayer.Layer {
	t.Helper()
	cache, err := localcachelayer.NewMemoryCache(8)
	if err != nil {
		t.Fatal(err)
	}
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
	if err != nil {
		t.Fatal(err)
	}
	return layer
}
