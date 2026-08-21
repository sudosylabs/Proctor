// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package memberlist

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/sudosylabs/proctor/server/cluster"
)

type recordingDiscoveryStore struct {
	operations        []string
	events            chan string
	upserts           []cluster.DiscoveryNode
	listTimes         []time.Time
	cleanupAt         []time.Time
	deletes           []string
	deleteCtx         []error
	deleteHasDeadline []bool
	live              []cluster.DiscoveryNode
	upsertErr         error
	listErr           error
	deleteErr         error
	cleanupErr        error
}

func (s *recordingDiscoveryStore) Upsert(_ context.Context, node cluster.DiscoveryNode) error {
	s.record("upsert")
	s.upserts = append(s.upserts, node)
	return s.upsertErr
}

func (s *recordingDiscoveryStore) ListLive(_ context.Context, now time.Time) ([]cluster.DiscoveryNode, error) {
	s.record("list")
	s.listTimes = append(s.listTimes, now)
	return append([]cluster.DiscoveryNode(nil), s.live...), s.listErr
}

func (s *recordingDiscoveryStore) Delete(ctx context.Context, nodeID string) error {
	s.record("delete")
	s.deletes = append(s.deletes, nodeID)
	s.deleteCtx = append(s.deleteCtx, ctx.Err())
	_, hasDeadline := ctx.Deadline()
	s.deleteHasDeadline = append(s.deleteHasDeadline, hasDeadline)
	return s.deleteErr
}

func (s *recordingDiscoveryStore) DeleteExpired(_ context.Context, now time.Time) (int64, error) {
	s.record("cleanup")
	s.cleanupAt = append(s.cleanupAt, now)
	return 0, s.cleanupErr
}

func (s *recordingDiscoveryStore) record(operation string) {
	s.operations = append(s.operations, operation)
	if s.events != nil {
		s.events <- operation
	}
}

type recordingDiscoveryDiagnostics struct {
	messages []string
	errors   []error
}

func (d *recordingDiscoveryDiagnostics) ErrorContext(_ context.Context, message string, err error) {
	d.messages = append(d.messages, message)
	d.errors = append(d.errors, err)
}

func newDiscoveryMaintenanceForTest(
	store cluster.DiscoveryStore,
	diagnostics cluster.Logger,
	now time.Time,
	seeds []string,
) (*discoveryMaintenance, *int) {
	tickerCreations := 0
	maintenance := newDiscoveryMaintenance(discoveryMaintenanceConfig{
		nodeID:             "node-local",
		advertiseAddress:   "127.0.0.1:7946",
		serverVersion:      "test-version",
		seedAddresses:      seeds,
		discovery:          store,
		discoveryTTL:       30 * time.Second,
		discoveryHeartbeat: 10 * time.Second,
		protocolMin:        2,
		protocolMax:        4,
		diagnostics:        diagnostics,
		now:                func() time.Time { return now },
		newTicker: func(time.Duration) discoveryTicker {
			tickerCreations++
			panic("ticker must not be created during discovery preparation")
		},
	})
	return maintenance, &tickerCreations
}

func TestDiscoveryMaintenanceConstructionIsInertAndCopiesConfiguration(t *testing.T) {
	t.Parallel()

	store := &recordingDiscoveryStore{}
	diagnostics := &recordingDiscoveryDiagnostics{}
	seeds := []string{"127.0.0.1:7001"}
	maintenance, tickerCreations := newDiscoveryMaintenanceForTest(store, diagnostics, time.Now(), seeds)
	seeds[0] = "127.0.0.1:7999"

	if len(store.operations) != 0 {
		t.Fatalf("construction operations = %v, want none", store.operations)
	}
	if *tickerCreations != 0 {
		t.Fatalf("construction created %d tickers, want none", *tickerCreations)
	}
	if got := maintenance.cfg.seedAddresses; !reflect.DeepEqual(got, []string{"127.0.0.1:7001"}) {
		t.Fatalf("copied seed addresses = %v", got)
	}
}

func TestDiscoveryMaintenancePrepareAdvertisesThenSelectsDeterministicSeeds(t *testing.T) {
	t.Parallel()

	location := time.FixedZone("test-offset", 2*60*60)
	now := time.Date(2026, time.August, 13, 9, 10, 11, 12, location)
	store := &recordingDiscoveryStore{live: []cluster.DiscoveryNode{
		{NodeID: "node-local", AdvertiseAddress: "127.0.0.1:7946", ProtocolMin: 2, ProtocolMax: 4},
		{NodeID: "node-boundary-high", AdvertiseAddress: " 127.0.0.1:7003 ", ProtocolMin: 4, ProtocolMax: 6},
		{NodeID: "node-boundary-low", AdvertiseAddress: "127.0.0.1:7004", ProtocolMin: 1, ProtocolMax: 2},
		{NodeID: "node-incompatible", AdvertiseAddress: "127.0.0.1:7005", ProtocolMin: 5, ProtocolMax: 6},
		{NodeID: "node-static-duplicate", AdvertiseAddress: "127.0.0.1:7001", ProtocolMin: 2, ProtocolMax: 4},
		{NodeID: "node-dynamic-duplicate", AdvertiseAddress: "127.0.0.1:7003", ProtocolMin: 2, ProtocolMax: 4},
		{NodeID: "node-blank", AdvertiseAddress: "   ", ProtocolMin: 2, ProtocolMax: 4},
	}}
	diagnostics := &recordingDiscoveryDiagnostics{}
	seeds := []string{" 127.0.0.1:7001 ", "", "127.0.0.1:7002", "127.0.0.1:7001"}
	maintenance, tickerCreations := newDiscoveryMaintenanceForTest(store, diagnostics, now, seeds)

	got, err := maintenance.prepare(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"127.0.0.1:7001", "127.0.0.1:7002", "127.0.0.1:7003", "127.0.0.1:7004"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("seeds = %v, want %v", got, want)
	}
	if !reflect.DeepEqual(store.operations, []string{"upsert", "list"}) {
		t.Fatalf("operations = %v", store.operations)
	}
	if len(store.upserts) != 1 {
		t.Fatalf("upserts = %d, want 1", len(store.upserts))
	}
	wantNow := now.UTC()
	wantNode := cluster.DiscoveryNode{
		NodeID:           "node-local",
		AdvertiseAddress: "127.0.0.1:7946",
		ServerVersion:    "test-version",
		ProtocolMin:      2,
		ProtocolMax:      4,
		UpdatedAt:        wantNow,
		ExpiresAt:        wantNow.Add(30 * time.Second),
	}
	if gotNode := store.upserts[0]; gotNode != wantNode {
		t.Fatalf("advertisement = %#v, want %#v", gotNode, wantNode)
	}
	if !reflect.DeepEqual(store.listTimes, []time.Time{wantNow}) {
		t.Fatalf("list instants = %v, want %v", store.listTimes, wantNow)
	}
	if len(diagnostics.messages) != 0 {
		t.Fatalf("unexpected diagnostics = %v", diagnostics.messages)
	}
	if *tickerCreations != 0 {
		t.Fatalf("prepare created %d tickers, want none", *tickerCreations)
	}
}

func TestDiscoveryMaintenancePreparePreservesAdvertisementFailure(t *testing.T) {
	t.Parallel()

	advertiseErr := errors.New("advertisement unavailable")
	store := &recordingDiscoveryStore{upsertErr: advertiseErr}
	maintenance, tickerCreations := newDiscoveryMaintenanceForTest(
		store,
		&recordingDiscoveryDiagnostics{},
		time.Date(2026, time.August, 13, 9, 0, 0, 0, time.UTC),
		nil,
	)

	_, err := maintenance.prepare(context.Background())
	if !errors.Is(err, advertiseErr) {
		t.Fatalf("prepare error = %v, want advertisement error", err)
	}
	if !reflect.DeepEqual(store.operations, []string{"upsert"}) {
		t.Fatalf("operations = %v, want only upsert", store.operations)
	}
	if *tickerCreations != 0 {
		t.Fatalf("failed prepare created %d tickers, want none", *tickerCreations)
	}
}

func TestDiscoveryMaintenancePrepareRollsBackListingFailureAndRemainsRetryable(t *testing.T) {
	t.Parallel()

	listErr := errors.New("listing unavailable")
	rollbackErr := errors.New("withdrawal unavailable")
	store := &recordingDiscoveryStore{listErr: listErr, deleteErr: rollbackErr}
	diagnostics := &recordingDiscoveryDiagnostics{}
	maintenance, tickerCreations := newDiscoveryMaintenanceForTest(
		store,
		diagnostics,
		time.Date(2026, time.August, 13, 9, 0, 0, 0, time.UTC),
		[]string{"127.0.0.1:7001"},
	)

	_, err := maintenance.prepare(context.Background())
	if !errors.Is(err, listErr) || err.Error() != "list discovery peers: listing unavailable" {
		t.Fatalf("prepare error = %v, want wrapped listing error", err)
	}
	if !reflect.DeepEqual(store.operations, []string{"upsert", "list", "delete"}) {
		t.Fatalf("failure operations = %v", store.operations)
	}
	if !reflect.DeepEqual(store.deletes, []string{"node-local"}) {
		t.Fatalf("rollback node IDs = %v", store.deletes)
	}
	if !reflect.DeepEqual(diagnostics.errors, []error{rollbackErr}) {
		t.Fatalf("diagnostic errors = %v", diagnostics.errors)
	}
	if !reflect.DeepEqual(diagnostics.messages, []string{"cluster discovery startup rollback failed"}) {
		t.Fatalf("diagnostic messages = %v", diagnostics.messages)
	}
	message := diagnostics.messages[0]
	if strings.Contains(message, "node-local") || strings.Contains(message, "127.0.0.1") {
		t.Fatalf("rollback diagnostic exposed discovery data: %q", message)
	}

	store.listErr = nil
	store.deleteErr = nil
	got, err := maintenance.prepare(context.Background())
	if err != nil {
		t.Fatalf("retry prepare: %v", err)
	}
	if !reflect.DeepEqual(got, []string{"127.0.0.1:7001"}) {
		t.Fatalf("retry seeds = %v", got)
	}
	wantOperations := []string{"upsert", "list", "delete", "upsert", "list"}
	if !reflect.DeepEqual(store.operations, wantOperations) {
		t.Fatalf("retry operations = %v, want %v", store.operations, wantOperations)
	}
	if *tickerCreations != 0 {
		t.Fatalf("failed and retried prepare created %d tickers, want none", *tickerCreations)
	}
}

func TestDiscoveryMaintenanceRollbackUsesBoundedCleanupAfterStartupCancellation(t *testing.T) {
	t.Parallel()

	listErr := errors.New("listing failed")
	rollbackErr := errors.New("rollback failed")
	store := &recordingDiscoveryStore{listErr: listErr, deleteErr: rollbackErr}
	diagnostics := &recordingDiscoveryDiagnostics{}
	maintenance, _ := newDiscoveryMaintenanceForTest(
		store,
		diagnostics,
		time.Date(2026, time.August, 13, 9, 0, 0, 0, time.UTC),
		nil,
	)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := maintenance.prepare(ctx)
	if !errors.Is(err, listErr) {
		t.Fatalf("prepare error = %v, want primary listing error", err)
	}
	if !reflect.DeepEqual(store.deleteCtx, []error{nil}) {
		t.Fatalf("rollback context errors = %v, want usable bounded cleanup context", store.deleteCtx)
	}
	if !reflect.DeepEqual(store.deleteHasDeadline, []bool{true}) {
		t.Fatalf("rollback deadline presence = %v, want bounded cleanup context", store.deleteHasDeadline)
	}
	if !reflect.DeepEqual(diagnostics.errors, []error{rollbackErr}) {
		t.Fatalf("rollback diagnostic errors = %v", diagnostics.errors)
	}
}

func TestDiscoveryMaintenancePrepareRejectsMissingClockInstantWithoutStoreWork(t *testing.T) {
	t.Parallel()

	store := &recordingDiscoveryStore{}
	maintenance, tickerCreations := newDiscoveryMaintenanceForTest(
		store,
		&recordingDiscoveryDiagnostics{},
		time.Time{},
		nil,
	)

	_, err := maintenance.prepare(context.Background())
	if err == nil || err.Error() != "discovery now is required" {
		t.Fatalf("prepare error = %v, want missing-now error", err)
	}
	if len(store.operations) != 0 {
		t.Fatalf("operations = %v, want none", store.operations)
	}
	if *tickerCreations != 0 {
		t.Fatalf("invalid prepare created %d tickers, want none", *tickerCreations)
	}
}

func TestPrivateLeaseRejectsNonPositiveTTL(t *testing.T) {
	t.Parallel()

	_, _, err := discoveryLease(time.Date(2026, time.August, 13, 9, 0, 0, 0, time.UTC), 0)
	if err == nil || err.Error() != "discovery ttl must be positive" {
		t.Fatalf("discoveryLease error = %v", err)
	}
}

func TestDiscoveryMaintenanceTickRenewsThenCleansWithOneInstantAndIndependentFailures(t *testing.T) {
	t.Parallel()

	renewErr := errors.New("renew unavailable")
	cleanupErr := errors.New("cleanup unavailable")
	store := &recordingDiscoveryStore{upsertErr: renewErr, cleanupErr: cleanupErr}
	diagnostics := &recordingDiscoveryDiagnostics{}
	maintenance, tickerCreations := newDiscoveryMaintenanceForTest(
		store,
		diagnostics,
		time.Time{},
		nil,
	)
	first := time.Date(2026, time.August, 13, 7, 8, 9, 10, time.UTC)

	maintenance.maintainAt(context.Background(), first)
	if !reflect.DeepEqual(store.operations, []string{"upsert", "cleanup"}) {
		t.Fatalf("failed tick operations = %v", store.operations)
	}
	if got := store.upserts[0].UpdatedAt; !got.Equal(first) {
		t.Fatalf("renewal instant = %v, want %v", got, first)
	}
	if !reflect.DeepEqual(store.cleanupAt, []time.Time{first}) {
		t.Fatalf("cleanup instants = %v, want %v", store.cleanupAt, first)
	}
	wantMessages := []string{"cluster discovery heartbeat failed", "cluster discovery cleanup failed"}
	if !reflect.DeepEqual(diagnostics.messages, wantMessages) {
		t.Fatalf("diagnostic messages = %v, want %v", diagnostics.messages, wantMessages)
	}
	if !reflect.DeepEqual(diagnostics.errors, []error{renewErr, cleanupErr}) {
		t.Fatalf("diagnostic errors = %v", diagnostics.errors)
	}

	store.upsertErr = nil
	store.cleanupErr = nil
	second := first.Add(10 * time.Second)
	maintenance.maintainAt(context.Background(), second)
	wantOperations := []string{"upsert", "cleanup", "upsert", "cleanup"}
	if !reflect.DeepEqual(store.operations, wantOperations) {
		t.Fatalf("continued tick operations = %v, want %v", store.operations, wantOperations)
	}
	if got := store.upserts[1].UpdatedAt; !got.Equal(second) {
		t.Fatalf("second renewal instant = %v, want %v", got, second)
	}
	if got := store.cleanupAt[1]; !got.Equal(second) {
		t.Fatalf("second cleanup instant = %v, want %v", got, second)
	}
	if *tickerCreations != 0 {
		t.Fatalf("direct maintenance created %d tickers, want none", *tickerCreations)
	}
}

type manualDiscoveryTicker struct {
	ticks chan time.Time
	stops int
}

func (t *manualDiscoveryTicker) C() <-chan time.Time {
	return t.ticks
}

func (t *manualDiscoveryTicker) Stop() {
	t.stops++
}

type cancellationBlockingDiscoveryStore struct {
	upsertEntered  chan struct{}
	upsertReleased chan struct{}
	operations     []string
}

func (s *cancellationBlockingDiscoveryStore) Upsert(ctx context.Context, _ cluster.DiscoveryNode) error {
	s.operations = append(s.operations, "upsert")
	close(s.upsertEntered)
	<-ctx.Done()
	close(s.upsertReleased)
	return ctx.Err()
}

func (s *cancellationBlockingDiscoveryStore) ListLive(ctx context.Context, _ time.Time) ([]cluster.DiscoveryNode, error) {
	s.operations = append(s.operations, "list")
	return nil, ctx.Err()
}

func (s *cancellationBlockingDiscoveryStore) Delete(_ context.Context, _ string) error {
	s.operations = append(s.operations, "delete")
	return nil
}

func (s *cancellationBlockingDiscoveryStore) DeleteExpired(ctx context.Context, _ time.Time) (int64, error) {
	s.operations = append(s.operations, "cleanup")
	return 0, ctx.Err()
}

func TestDiscoveryMaintenanceRunOwnsScheduleUntilCancellation(t *testing.T) {
	t.Parallel()

	events := make(chan string, 3)
	store := &recordingDiscoveryStore{events: events}
	diagnostics := &recordingDiscoveryDiagnostics{}
	ticker := &manualDiscoveryTicker{ticks: make(chan time.Time)}
	scheduled := make(chan time.Duration, 1)
	now := time.Date(2026, time.August, 13, 10, 11, 12, 13, time.UTC)
	maintenance, _ := newDiscoveryMaintenanceForTest(store, diagnostics, now, nil)
	maintenance.cfg.newTicker = func(interval time.Duration) discoveryTicker {
		scheduled <- interval
		return ticker
	}

	runCtx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		maintenance.run(runCtx, func(context.Context, []string) error { return nil })
	}()

	if interval := <-scheduled; interval != 10*time.Second {
		t.Fatalf("schedule interval = %v, want 10s", interval)
	}
	ticker.ticks <- time.Time{}
	if operation := <-events; operation != "upsert" {
		t.Fatalf("first tick operation = %q, want upsert", operation)
	}
	if operation := <-events; operation != "cleanup" {
		t.Fatalf("second tick operation = %q, want cleanup", operation)
	}
	if operation := <-events; operation != "list" {
		t.Fatalf("third tick operation = %q, want list", operation)
	}

	cancel()
	<-done
	if ticker.stops != 1 {
		t.Fatalf("ticker stops = %d, want 1", ticker.stops)
	}
	if !reflect.DeepEqual(store.operations, []string{"upsert", "cleanup", "list"}) {
		t.Fatalf("loop operations = %v", store.operations)
	}
	if got := store.upserts[0].UpdatedAt; !got.Equal(now) {
		t.Fatalf("loop renewal instant = %v, want %v", got, now)
	}
	if got := store.cleanupAt[0]; !got.Equal(now) {
		t.Fatalf("loop cleanup instant = %v, want %v", got, now)
	}
	if len(diagnostics.messages) != 0 {
		t.Fatalf("unexpected diagnostics = %v", diagnostics.messages)
	}
}

func TestDiscoveryMaintenanceRunRetriesRejoinAfterFailure(t *testing.T) {
	t.Parallel()

	store := &recordingDiscoveryStore{live: []cluster.DiscoveryNode{{
		NodeID:           "node-peer",
		AdvertiseAddress: "127.0.0.1:7001",
		ProtocolMin:      2,
		ProtocolMax:      4,
	}}}
	diagnostics := &recordingDiscoveryDiagnostics{}
	ticker := &manualDiscoveryTicker{ticks: make(chan time.Time)}
	scheduled := make(chan time.Duration, 1)
	maintenance, _ := newDiscoveryMaintenanceForTest(store, diagnostics, time.Now(), nil)
	maintenance.cfg.newTicker = func(interval time.Duration) discoveryTicker {
		scheduled <- interval
		return ticker
	}

	rejoinErr := errors.New("join unavailable")
	attempts := make(chan []string, 2)
	callCount := 0
	runCtx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		maintenance.run(runCtx, func(_ context.Context, seeds []string) error {
			callCount++
			attempts <- append([]string(nil), seeds...)
			if callCount == 1 {
				return rejoinErr
			}
			return nil
		})
	}()
	<-scheduled

	ticker.ticks <- time.Time{}
	if got := <-attempts; !reflect.DeepEqual(got, []string{"127.0.0.1:7001"}) {
		t.Fatalf("first retry seeds = %v", got)
	}
	ticker.ticks <- time.Time{}
	if got := <-attempts; !reflect.DeepEqual(got, []string{"127.0.0.1:7001"}) {
		t.Fatalf("second retry seeds = %v", got)
	}
	cancel()
	<-done

	if !reflect.DeepEqual(diagnostics.messages, []string{"memberlist rejoin incomplete"}) ||
		!reflect.DeepEqual(diagnostics.errors, []error{rejoinErr}) {
		t.Fatalf("retry diagnostics = %v / %v", diagnostics.messages, diagnostics.errors)
	}
}

func TestTransportStopOwnsMaintenanceTerminationAndWithdrawal(t *testing.T) {
	t.Parallel()

	store := &recordingDiscoveryStore{}
	diagnostics := &recordingDiscoveryDiagnostics{}
	ticker := &manualDiscoveryTicker{ticks: make(chan time.Time)}
	scheduled := make(chan time.Duration, 1)
	maintenance, _ := newDiscoveryMaintenanceForTest(store, diagnostics, time.Now(), nil)
	maintenance.cfg.newTicker = func(interval time.Duration) discoveryTicker {
		scheduled <- interval
		return ticker
	}
	runCtx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	transport := &Transport{
		state:     stateStarted,
		cancel:    cancel,
		done:      done,
		discovery: maintenance,
	}
	go func() {
		defer close(done)
		maintenance.run(runCtx, func(context.Context, []string) error { return nil })
	}()
	<-scheduled

	if err := transport.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	default:
		t.Fatal("Stop returned before owned maintenance terminated")
	}
	if ticker.stops != 1 {
		t.Fatalf("ticker stops = %d, want 1", ticker.stops)
	}
	if !reflect.DeepEqual(store.operations, []string{"delete"}) {
		t.Fatalf("stop operations = %v, want withdrawal only", store.operations)
	}
	if err := transport.Stop(context.Background()); err != nil {
		t.Fatalf("repeated Stop: %v", err)
	}
	if !reflect.DeepEqual(store.operations, []string{"delete"}) {
		t.Fatalf("repeated Stop operations = %v", store.operations)
	}
	if err := transport.Ping(context.Background()); !errors.Is(err, cluster.ErrStopped) {
		t.Fatalf("Ping after Stop error = %v", err)
	}
}

func TestTransportStopReportsExpiredDeadlineAfterTerminatingOwnedMaintenance(t *testing.T) {
	t.Parallel()

	store := &recordingDiscoveryStore{}
	ticker := &manualDiscoveryTicker{ticks: make(chan time.Time)}
	scheduled := make(chan time.Duration, 1)
	maintenance, _ := newDiscoveryMaintenanceForTest(
		store,
		&recordingDiscoveryDiagnostics{},
		time.Now(),
		nil,
	)
	maintenance.cfg.newTicker = func(interval time.Duration) discoveryTicker {
		scheduled <- interval
		return ticker
	}
	runCtx, cancelRun := context.WithCancel(context.Background())
	done := make(chan struct{})
	transport := &Transport{
		state:     stateStarted,
		cancel:    cancelRun,
		done:      done,
		discovery: maintenance,
	}
	go func() {
		defer close(done)
		maintenance.run(runCtx, func(context.Context, []string) error { return nil })
	}()
	<-scheduled

	stopCtx, cancelStop := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancelStop()
	if err := transport.Stop(stopCtx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Stop error = %v, want deadline exceeded", err)
	}
	if len(store.operations) != 0 {
		t.Fatalf("expired-deadline operations = %v, want no withdrawal", store.operations)
	}
	select {
	case <-done:
	default:
		t.Fatal("deadline Stop returned before owned maintenance terminated")
	}
	if ticker.stops != 1 {
		t.Fatalf("ticker stops = %d, want 1", ticker.stops)
	}
	if err := transport.Stop(context.Background()); err != nil {
		t.Fatalf("repeated completed Stop: %v", err)
	}
	if len(store.operations) != 0 {
		t.Fatalf("repeated completed Stop operations = %v", store.operations)
	}
}

func TestTransportStopCancellationReleasesInFlightDiscoveryWithoutAbandonment(t *testing.T) {
	t.Parallel()

	store := &cancellationBlockingDiscoveryStore{
		upsertEntered:  make(chan struct{}),
		upsertReleased: make(chan struct{}),
	}
	diagnostics := &recordingDiscoveryDiagnostics{}
	ticker := &manualDiscoveryTicker{ticks: make(chan time.Time)}
	scheduled := make(chan time.Duration, 1)
	maintenance, _ := newDiscoveryMaintenanceForTest(store, diagnostics, time.Now(), nil)
	maintenance.cfg.newTicker = func(interval time.Duration) discoveryTicker {
		scheduled <- interval
		return ticker
	}
	runCtx, cancelRun := context.WithCancel(context.Background())
	done := make(chan struct{})
	transport := &Transport{
		state:     stateStarted,
		cancel:    cancelRun,
		done:      done,
		discovery: maintenance,
	}
	go func() {
		defer close(done)
		maintenance.run(runCtx, func(context.Context, []string) error { return nil })
	}()
	<-scheduled
	ticker.ticks <- time.Time{}
	<-store.upsertEntered

	stopCtx, cancelStop := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancelStop()
	if err := transport.Stop(stopCtx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("deadline Stop error = %v, want deadline exceeded", err)
	}
	<-store.upsertReleased
	if err := transport.Ping(context.Background()); !errors.Is(err, cluster.ErrStopped) {
		t.Fatalf("Ping after deadline shutdown error = %v", err)
	}
	select {
	case <-done:
	default:
		t.Fatal("deadline Stop returned before in-flight discovery maintenance terminated")
	}
	if ticker.stops != 1 {
		t.Fatalf("ticker stops = %d, want 1", ticker.stops)
	}
	wantOperations := []string{"upsert", "cleanup", "list"}
	if !reflect.DeepEqual(store.operations, wantOperations) {
		t.Fatalf("shutdown operations = %v, want %v", store.operations, wantOperations)
	}
	wantDiagnostics := []string{"cluster discovery heartbeat failed", "cluster discovery cleanup failed", "cluster discovery peer listing failed"}
	if !reflect.DeepEqual(diagnostics.messages, wantDiagnostics) {
		t.Fatalf("maintenance diagnostics = %v, want %v", diagnostics.messages, wantDiagnostics)
	}
	if len(diagnostics.errors) != 3 ||
		!errors.Is(diagnostics.errors[0], context.Canceled) ||
		!errors.Is(diagnostics.errors[1], context.Canceled) ||
		!errors.Is(diagnostics.errors[2], context.Canceled) {
		t.Fatalf("maintenance diagnostic errors = %v", diagnostics.errors)
	}
	if err := transport.Stop(context.Background()); err != nil {
		t.Fatalf("repeated completed Stop: %v", err)
	}
	if !reflect.DeepEqual(store.operations, wantOperations) {
		t.Fatalf("repeated completed Stop operations = %v", store.operations)
	}
}

func TestTransportStopDiagnosesWithdrawalFailureAndRemainsIdempotent(t *testing.T) {
	t.Parallel()

	withdrawErr := errors.New("withdraw unavailable")
	store := &recordingDiscoveryStore{deleteErr: withdrawErr}
	diagnostics := &recordingDiscoveryDiagnostics{}
	maintenance, _ := newDiscoveryMaintenanceForTest(store, diagnostics, time.Now(), nil)
	transport := &Transport{state: stateStarted, discovery: maintenance}

	if err := transport.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(store.operations, []string{"delete"}) {
		t.Fatalf("withdrawal operations = %v", store.operations)
	}
	if !reflect.DeepEqual(diagnostics.messages, []string{"cluster discovery delete failed"}) {
		t.Fatalf("diagnostic messages = %v", diagnostics.messages)
	}
	if !reflect.DeepEqual(diagnostics.errors, []error{withdrawErr}) {
		t.Fatalf("diagnostic errors = %v", diagnostics.errors)
	}
	if err := transport.Stop(context.Background()); err != nil {
		t.Fatalf("repeated Stop: %v", err)
	}
	if len(store.operations) != 1 {
		t.Fatalf("repeated Stop operations = %v", store.operations)
	}
}

func TestTransportStopBeforeStartPerformsNoDiscoveryWork(t *testing.T) {
	t.Parallel()

	store := &recordingDiscoveryStore{}
	maintenance, tickerCreations := newDiscoveryMaintenanceForTest(
		store,
		&recordingDiscoveryDiagnostics{},
		time.Now(),
		nil,
	)
	transport := &Transport{state: stateCreated, discovery: maintenance}

	if err := transport.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(store.operations) != 0 {
		t.Fatalf("Stop before Start operations = %v", store.operations)
	}
	if *tickerCreations != 0 {
		t.Fatalf("Stop before Start created %d tickers", *tickerCreations)
	}
	if err := transport.Stop(context.Background()); err != nil {
		t.Fatalf("repeated Stop before Start: %v", err)
	}
}

func TestMemberlistLeaveTimeoutUsesRemainingStopDeadline(t *testing.T) {
	t.Parallel()

	if got, ok := memberlistLeaveTimeout(context.Background()); !ok || got != time.Second {
		t.Fatalf("background leave timeout = %v, %t; want 1s, true", got, ok)
	}
	shortCtx, cancelShort := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancelShort()
	got, ok := memberlistLeaveTimeout(shortCtx)
	if !ok || got <= 0 || got > 100*time.Millisecond {
		t.Fatalf("short-deadline leave timeout = %v, %t", got, ok)
	}
	expiredCtx, cancelExpired := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancelExpired()
	if got, ok := memberlistLeaveTimeout(expiredCtx); ok || got != 0 {
		t.Fatalf("expired leave timeout = %v, %t; want 0, false", got, ok)
	}
}

var _ cluster.DiscoveryStore = (*recordingDiscoveryStore)(nil)
