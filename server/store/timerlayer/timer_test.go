// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package timerlayer

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

type recordedMetric struct {
	operation Operation
	outcome   Outcome
	duration  time.Duration
}

type recordingMetrics struct {
	events []recordedMetric
}

func (m *recordingMetrics) Observe(operation Operation, outcome Outcome, duration time.Duration) {
	m.events = append(m.events, recordedMetric{operation: operation, outcome: outcome, duration: duration})
}

type institutionStub struct {
	store.InstitutionStore
	result *model.Institution
	err    error
	id     string
}

func (s *institutionStub) Get(_ context.Context, id model.InstitutionID) (*model.Institution, error) {
	s.id = id.String()
	return s.result, s.err
}

type rootStub struct {
	store.Store
	institution store.InstitutionStore
	pingErr     error
}

func (s *rootStub) Institution() store.InstitutionStore { return s.institution }
func (s *rootStub) Ping(context.Context) error          { return s.pingErr }

func TestTimerPreservesResultsErrorsAndUsesBoundedMetrics(t *testing.T) {
	t.Parallel()

	startedAt := time.Date(2026, time.August, 8, 9, 0, 0, 0, time.UTC)
	now := sequenceClock(startedAt, startedAt.Add(25*time.Millisecond))
	want := &model.Institution{ID: model.NewInstitutionID(), DisplayName: "Northbridge"}
	wrapped := &institutionStub{result: want}
	metrics := &recordingMetrics{}
	layer := newLayer(&rootStub{institution: wrapped}, metrics, Options{}, now)
	institution := layer.Institution()
	require.Same(t, institution, layer.Institution(), "Institution() returned a different wrapper for the same store")

	const sensitiveID = "student-secret-must-not-be-a-label"
	got, err := layer.Institution().Get(context.Background(), sensitiveID)
	require.NoError(t, err)
	require.Same(t, want, got, "Get() result")
	require.Equal(t, sensitiveID, wrapped.id, "wrapped Get() id")
	require.Len(t, metrics.events, 1, "metrics")
	event := metrics.events[0]
	require.Equal(t, "institution.get", event.operation.String())
	require.Equal(t, OutcomeSuccess, event.outcome)
	require.Equal(t, 25*time.Millisecond, event.duration)
	require.NotEqual(t, sensitiveID, event.operation.String(), "sensitive argument leaked into operation label")
}

func TestTimerPreservesExactErrorAndRecordsFailure(t *testing.T) {
	t.Parallel()

	wantErr := errors.New("persistence failed")
	startedAt := time.Date(2026, time.August, 8, 9, 0, 0, 0, time.UTC)
	metrics := &recordingMetrics{}
	layer := newLayer(
		&rootStub{institution: &institutionStub{err: wantErr}},
		metrics,
		Options{},
		sequenceClock(startedAt, startedAt.Add(time.Millisecond)),
	)

	got, err := layer.Institution().Get(context.Background(), model.NewInstitutionID())
	require.Nil(t, got, "Get() result")
	require.Equal(t, wantErr, err, "Get() error")
	require.Len(t, metrics.events, 1, "metrics")
	require.Equal(t, OutcomeError, metrics.events[0].outcome, "metrics")
}

func TestRecorderPanicCannotChangeStoreSemantics(t *testing.T) {
	t.Parallel()

	want := &model.Institution{ID: model.NewInstitutionID()}
	layer := newLayer(
		&rootStub{institution: &institutionStub{result: want}},
		RecorderFunc(func(Operation, Outcome, time.Duration) { panic("metrics unavailable") }),
		Options{},
		time.Now,
	)

	got, err := layer.Institution().Get(context.Background(), model.NewInstitutionID())
	require.NoError(t, err, "Get()")
	require.Same(t, want, got, "Get()")
}

func TestTimerMeasuresRootOperations(t *testing.T) {
	t.Parallel()

	wantErr := errors.New("database unavailable")
	metrics := &recordingMetrics{}
	layer := newLayer(&rootStub{pingErr: wantErr}, metrics, Options{}, time.Now)
	require.Equal(t, wantErr, layer.Ping(context.Background()), "Ping() error")
	require.Len(t, metrics.events, 1)
	require.Equal(t, "store.ping", metrics.events[0].operation.String())
}

func TestNewRejectsMissingDependencies(t *testing.T) {
	t.Parallel()

	_, err := New(nil, NopRecorder{}, Options{})
	require.Error(t, err, "New() accepted a nil store")
	_, err = New(&rootStub{}, nil, Options{})
	require.Error(t, err, "New() accepted a nil recorder")
	_, err = New(&rootStub{}, NopRecorder{}, Options{SlowThreshold: time.Second})
	require.Error(t, err, "New() accepted a slow threshold without a recorder")
	_, err = New(&rootStub{}, NopRecorder{}, Options{SlowThreshold: -time.Second, Slow: &recordingSlow{}})
	require.Error(t, err, "New() accepted a negative slow threshold")
}

type recordedSlow struct {
	ctx       context.Context
	operation string
	duration  time.Duration
}

type recordingSlow struct {
	events []recordedSlow
}

func (r *recordingSlow) ObserveSlow(ctx context.Context, operation Operation, duration time.Duration) {
	r.events = append(r.events, recordedSlow{ctx: ctx, operation: operation.String(), duration: duration})
}

type correlationKey struct{}

func TestSlowOperationsAtOrAboveTheThresholdAreReported(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name     string
		elapsed  time.Duration
		reported bool
	}{
		{name: "below threshold", elapsed: 99 * time.Millisecond, reported: false},
		{name: "at threshold", elapsed: 100 * time.Millisecond, reported: true},
		{name: "above threshold", elapsed: time.Second, reported: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			startedAt := time.Date(2026, time.August, 8, 9, 0, 0, 0, time.UTC)
			slow := &recordingSlow{}
			layer := newLayer(
				&rootStub{institution: &institutionStub{err: errors.New("student-secret-must-not-leak")}},
				NopRecorder{},
				Options{SlowThreshold: 100 * time.Millisecond, Slow: slow},
				sequenceClock(startedAt, startedAt.Add(test.elapsed)),
			)
			ctx := context.WithValue(context.Background(), correlationKey{}, "request")
			_, err := layer.Institution().Get(ctx, model.NewInstitutionID())
			require.Error(t, err)
			if !test.reported {
				require.Empty(t, slow.events)
				return
			}
			require.Len(t, slow.events, 1)
			require.Equal(t, "institution.get", slow.events[0].operation)
			require.Equal(t, test.elapsed, slow.events[0].duration)
			require.Equal(t, "request", slow.events[0].ctx.Value(correlationKey{}), "caller context for correlation")
		})
	}
}

func TestSlowRecorderPanicCannotChangeStoreSemantics(t *testing.T) {
	t.Parallel()

	want := &model.Institution{ID: model.NewInstitutionID()}
	layer := newLayer(
		&rootStub{institution: &institutionStub{result: want}},
		NopRecorder{},
		Options{SlowThreshold: time.Nanosecond, Slow: slowFunc(func() { panic("logging unavailable") })},
		sequenceClock(time.Unix(0, 0), time.Unix(1, 0)),
	)
	got, err := layer.Institution().Get(context.Background(), model.NewInstitutionID())
	require.NoError(t, err)
	require.Same(t, want, got)
}

type spanKey struct{}

type recordingTracer struct {
	started  []string
	outcomes []Outcome
}

func (r *recordingTracer) StartOperation(ctx context.Context, operation Operation) (context.Context, func(Outcome)) {
	r.started = append(r.started, operation.String())
	return context.WithValue(ctx, spanKey{}, operation.String()), func(outcome Outcome) {
		r.outcomes = append(r.outcomes, outcome)
	}
}

type contextCapturingInstitutionStore struct {
	store.InstitutionStore
	ctx context.Context
	err error
}

func (s *contextCapturingInstitutionStore) Get(ctx context.Context, _ model.InstitutionID) (*model.Institution, error) {
	s.ctx = ctx
	return nil, s.err
}

func TestTracerEnclosesTheForwardedCallAndReceivesTheOutcome(t *testing.T) {
	t.Parallel()

	wantErr := errors.New("student-secret-must-not-leak")
	wrapped := &contextCapturingInstitutionStore{err: wantErr}
	tracer := &recordingTracer{}
	slow := &recordingSlow{}
	layer := newLayer(
		&rootStub{institution: wrapped}, NopRecorder{},
		Options{Tracer: tracer, SlowThreshold: time.Nanosecond, Slow: slow},
		sequenceClock(time.Unix(0, 0), time.Unix(1, 0)),
	)
	_, err := layer.Institution().Get(context.Background(), model.NewInstitutionID())
	require.Equal(t, wantErr, err)
	require.Equal(t, []string{"institution.get"}, tracer.started)
	require.Equal(t, []Outcome{OutcomeError}, tracer.outcomes)
	require.Equal(t, "institution.get", wrapped.ctx.Value(spanKey{}), "forwarded call runs inside the span")
	require.Len(t, slow.events, 1)
	require.Equal(t, "institution.get", slow.events[0].ctx.Value(spanKey{}), "slow log correlates with the span")
}

type panickingTracer struct{}

func (panickingTracer) StartOperation(context.Context, Operation) (context.Context, func(Outcome)) {
	panic("tracing unavailable")
}

func TestTracerPanicCannotChangeStoreSemantics(t *testing.T) {
	t.Parallel()

	want := &model.Institution{ID: model.NewInstitutionID()}
	layer := newLayer(
		&rootStub{institution: &institutionStub{result: want}}, NopRecorder{},
		Options{Tracer: panickingTracer{}}, time.Now,
	)
	got, err := layer.Institution().Get(context.Background(), model.NewInstitutionID())
	require.NoError(t, err)
	require.Same(t, want, got)
}

type slowFunc func()

func (f slowFunc) ObserveSlow(context.Context, Operation, time.Duration) { f() }

func TestTimerPreservesMissingPerModelStores(t *testing.T) {
	t.Parallel()

	layer := newLayer(&rootStub{}, NopRecorder{}, Options{}, time.Now)
	require.Nil(t, layer.Institution(), "Institution() wrapped a nil underlying store")
}

func TestGeneratedForwardingIsCurrent(t *testing.T) {
	t.Parallel()

	temporary := t.TempDir()
	generatedPath := filepath.Join(temporary, "forwarding_gen.go")
	command := exec.Command(
		"go", "run", "../storetest/layergen", "-layer", "timer", "-source", "..", "-output", generatedPath,
	)
	command.Env = append(os.Environ(), "GOCACHE="+filepath.Join(temporary, "go-cache"))
	output, err := command.CombinedOutput()
	require.NoError(t, err, "regenerate forwarding: %v\n%s", err, output)
	want, err := os.ReadFile("forwarding_gen.go")
	require.NoError(t, err)
	got, err := os.ReadFile(generatedPath)
	require.NoError(t, err)
	require.True(t, bytes.Equal(got, want), "forwarding_gen.go is stale; run go generate ./store/timerlayer")
}

func sequenceClock(values ...time.Time) func() time.Time {
	index := 0
	return func() time.Time {
		value := values[index]
		if index < len(values)-1 {
			index++
		}
		return value
	}
}
