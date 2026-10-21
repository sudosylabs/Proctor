// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package memberlist

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

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

	require.Empty(t, store.operations, "construction operations")
	require.Equal(t, 0, *tickerCreations, "construction created")
	require.Equal(t, []string{"127.0.0.1:7001"}, maintenance.cfg.seedAddresses)
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
	require.NoError(t, err)
	want := []string{"127.0.0.1:7001", "127.0.0.1:7002", "127.0.0.1:7003", "127.0.0.1:7004"}
	require.Equal(t, want, got, "seeds")
	require.Equal(t, []string{"upsert", "list"}, store.operations)
	require.Len(t, store.upserts, 1, "upserts")
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
	require.Equal(t, wantNode, store.upserts[0], "advertisement")
	require.Equal(t, []time.Time{wantNow}, store.listTimes, "list instants = %v, want %v", store.listTimes, wantNow)
	require.Empty(t, diagnostics.messages)
	require.Equal(t, 0, *tickerCreations, "prepare created")
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
	require.ErrorIs(t, err, advertiseErr, "prepare error")
	require.Equal(t, []string{"upsert"}, store.operations, "operations")
	require.Equal(t, 0, *tickerCreations, "failed prepare created")
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
	require.ErrorIs(t, err, listErr, "prepare error")
	require.Equal(t, "list discovery peers: listing unavailable", err.Error(), "prepare error")
	require.Equal(t, []string{"upsert", "list", "delete"}, store.operations)
	require.Equal(t, []string{"node-local"}, store.deletes)
	require.Equal(t, []error{rollbackErr}, diagnostics.errors)
	require.Equal(t, []string{"cluster discovery startup rollback failed"}, diagnostics.messages)
	message := diagnostics.messages[0]
	require.NotContains(t, message, "node-local")
	require.NotContains(t, message, "127.0.0.1")

	store.listErr = nil
	store.deleteErr = nil
	got, err := maintenance.prepare(context.Background())
	require.NoError(t, err)
	require.Equal(t, []string{"127.0.0.1:7001"}, got)
	wantOperations := []string{"upsert", "list", "delete", "upsert", "list"}
	require.Equal(t, wantOperations, store.operations, "retry operations")
	require.Equal(t, 0, *tickerCreations, "failed and retried prepare created")
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
	require.ErrorIs(t, err, listErr, "prepare error")
	require.Equal(t, []error{nil}, store.deleteCtx, "rollback context errors")
	require.Equal(t, []bool{true}, store.deleteHasDeadline, "rollback deadline presence")
	require.Equal(t, []error{rollbackErr}, diagnostics.errors)
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
	require.Error(t, err, "prepare error")
	require.Equal(t, "discovery now is required", err.Error(), "prepare error")
	require.Empty(t, store.operations, "operations")
	require.Equal(t, 0, *tickerCreations, "invalid prepare created")
}

func TestPrivateLeaseRejectsNonPositiveTTL(t *testing.T) {
	t.Parallel()

	_, _, err := discoveryLease(time.Date(2026, time.August, 13, 9, 0, 0, 0, time.UTC), 0)
	require.Error(t, err)
	require.Equal(t, "discovery ttl must be positive", err.Error())
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
	require.Equal(t, []string{"upsert", "cleanup"}, store.operations)
	got := store.upserts[0].UpdatedAt
	require.True(t, got.Equal(first), "renewal instant = %v", got)
	require.Equal(t, []time.Time{first}, store.cleanupAt, "cleanup instants = %v, want %v", store.cleanupAt, first)
	wantMessages := []string{"cluster discovery heartbeat failed", "cluster discovery cleanup failed"}
	require.Equal(t, wantMessages, diagnostics.messages, "diagnostic messages")
	require.Equal(t, []error{renewErr, cleanupErr}, diagnostics.errors)

	store.upsertErr = nil
	store.cleanupErr = nil
	second := first.Add(10 * time.Second)
	maintenance.maintainAt(context.Background(), second)
	wantOperations := []string{"upsert", "cleanup", "upsert", "cleanup"}
	require.Equal(t, wantOperations, store.operations, "continued tick operations")
	got = store.upserts[1].UpdatedAt
	require.True(t, got.Equal(second), "second renewal instant = %v", got)
	got = store.cleanupAt[1]
	require.True(t, got.Equal(second), "second cleanup instant = %v", got)
	require.Equal(t, 0, *tickerCreations, "direct maintenance created")
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

	interval := <-scheduled
	require.Equal(t, 10*time.Second, interval, "schedule interval")
	ticker.ticks <- time.Time{}
	operation := <-events
	require.Equal(t, "upsert", operation, "first tick operation")
	operation = <-events
	require.Equal(t, "cleanup", operation, "second tick operation")
	operation = <-events
	require.Equal(t, "list", operation, "third tick operation")

	cancel()
	<-done
	require.Equal(t, 1, ticker.stops, "ticker stops")
	require.Equal(t, []string{"upsert", "cleanup", "list"}, store.operations)
	got := store.upserts[0].UpdatedAt
	require.True(t, got.Equal(now), "loop renewal instant = %v", got)
	got = store.cleanupAt[0]
	require.True(t, got.Equal(now), "loop cleanup instant = %v", got)
	require.Empty(t, diagnostics.messages)
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
	require.Equal(t, []string{"127.0.0.1:7001"}, (<-attempts))
	ticker.ticks <- time.Time{}
	require.Equal(t, []string{"127.0.0.1:7001"}, (<-attempts))
	cancel()
	<-done

	require.Equal(t, []string{"memberlist rejoin incomplete"}, diagnostics.messages, "retry diagnostics")
	require.Equal(t, []error{rejoinErr}, diagnostics.errors, "retry diagnostics")
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

	require.NoError(t, transport.Stop(context.Background()))
	select {
	case <-done:
	default:
		require.FailNow(t, "Stop returned before owned maintenance terminated")
	}
	require.Equal(t, 1, ticker.stops, "ticker stops")
	require.Equal(t, []string{"delete"}, store.operations, "stop operations")
	require.NoError(t, transport.Stop(context.Background()))
	require.Equal(t, []string{"delete"}, store.operations)
	require.ErrorIs(t, transport.Ping(context.Background()), cluster.ErrStopped)
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
	require.ErrorIs(t, transport.Stop(stopCtx), context.DeadlineExceeded, "Stop error")
	require.Empty(t, store.operations, "expired-deadline operations")
	select {
	case <-done:
	default:
		require.FailNow(t, "deadline Stop returned before owned maintenance terminated")
	}
	require.Equal(t, 1, ticker.stops, "ticker stops")
	require.NoError(t, transport.Stop(context.Background()))
	require.Empty(t, store.operations)
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
	require.ErrorIs(t, transport.Stop(stopCtx), context.DeadlineExceeded, "deadline Stop error")
	<-store.upsertReleased
	require.ErrorIs(t, transport.Ping(context.Background()), cluster.ErrStopped)
	select {
	case <-done:
	default:
		require.FailNow(t, "deadline Stop returned before in-flight discovery maintenance terminated")
	}
	require.Equal(t, 1, ticker.stops, "ticker stops")
	wantOperations := []string{"upsert", "cleanup", "list"}
	require.Equal(t, wantOperations, store.operations, "shutdown operations")
	wantDiagnostics := []string{"cluster discovery heartbeat failed", "cluster discovery cleanup failed", "cluster discovery peer listing failed"}
	require.Equal(t, wantDiagnostics, diagnostics.messages, "maintenance diagnostics")
	require.Len(t, diagnostics.errors, 3)
	require.ErrorIs(t, diagnostics.errors[0], context.Canceled)
	require.ErrorIs(t, diagnostics.errors[1], context.Canceled)
	require.ErrorIs(t, diagnostics.errors[2], context.Canceled)
	require.NoError(t, transport.Stop(context.Background()))
	require.Equal(t, wantOperations, store.operations)
}

func TestTransportStopDiagnosesWithdrawalFailureAndRemainsIdempotent(t *testing.T) {
	t.Parallel()

	withdrawErr := errors.New("withdraw unavailable")
	store := &recordingDiscoveryStore{deleteErr: withdrawErr}
	diagnostics := &recordingDiscoveryDiagnostics{}
	maintenance, _ := newDiscoveryMaintenanceForTest(store, diagnostics, time.Now(), nil)
	transport := &Transport{state: stateStarted, discovery: maintenance}

	require.NoError(t, transport.Stop(context.Background()))
	require.Equal(t, []string{"delete"}, store.operations)
	require.Equal(t, []string{"cluster discovery delete failed"}, diagnostics.messages)
	require.Equal(t, []error{withdrawErr}, diagnostics.errors)
	require.NoError(t, transport.Stop(context.Background()))
	require.Len(t, store.operations, 1)
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

	require.NoError(t, transport.Stop(context.Background()))
	require.Empty(t, store.operations)
	require.Equal(t, 0, *tickerCreations, "Stop before Start created")
	require.NoError(t, transport.Stop(context.Background()))
}

func TestMemberlistLeaveTimeoutUsesRemainingStopDeadline(t *testing.T) {
	t.Parallel()

	got, ok := memberlistLeaveTimeout(context.Background())
	require.True(t, ok, "background leave timeout")
	require.Equal(t, time.Second, got, "background leave timeout")
	shortCtx, cancelShort := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancelShort()
	got, ok = memberlistLeaveTimeout(shortCtx)
	require.True(t, ok, "short-deadline leave timeout")
	require.Positive(t, int64(got), "short-deadline leave timeout")
	require.LessOrEqual(t, got, 100*time.Millisecond, "short-deadline leave timeout")
	expiredCtx, cancelExpired := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancelExpired()
	got, ok = memberlistLeaveTimeout(expiredCtx)
	require.False(t, ok, "expired leave timeout")
	require.Equal(t, int64(0), int64(got), "expired leave timeout")
}

var _ cluster.DiscoveryStore = (*recordingDiscoveryStore)(nil)
