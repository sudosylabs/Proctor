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

func (s *institutionStub) Get(_ context.Context, id string) (*model.Institution, error) {
	s.id = id
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
	layer := newLayer(&rootStub{institution: wrapped}, metrics, now)
	if layer.Institution() != layer.Institution() {
		t.Fatal("Institution() returned a different wrapper for the same store")
	}

	const sensitiveID = "student-secret-must-not-be-a-label"
	got, err := layer.Institution().Get(context.Background(), sensitiveID)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("Get() result = %#v, want original pointer %#v", got, want)
	}
	if wrapped.id != sensitiveID {
		t.Fatalf("wrapped Get() id = %q, want %q", wrapped.id, sensitiveID)
	}
	if len(metrics.events) != 1 {
		t.Fatalf("metrics = %#v, want one event", metrics.events)
	}
	event := metrics.events[0]
	if event.operation.String() != "institution.get" {
		t.Fatalf("operation = %q", event.operation.String())
	}
	if event.outcome != OutcomeSuccess {
		t.Fatalf("outcome = %q", event.outcome)
	}
	if event.duration != 25*time.Millisecond {
		t.Fatalf("duration = %s", event.duration)
	}
	if event.operation.String() == sensitiveID {
		t.Fatal("sensitive argument leaked into operation label")
	}
}

func TestTimerPreservesExactErrorAndRecordsFailure(t *testing.T) {
	t.Parallel()

	wantErr := errors.New("persistence failed")
	startedAt := time.Date(2026, time.August, 8, 9, 0, 0, 0, time.UTC)
	metrics := &recordingMetrics{}
	layer := newLayer(
		&rootStub{institution: &institutionStub{err: wantErr}},
		metrics,
		sequenceClock(startedAt, startedAt.Add(time.Millisecond)),
	)

	got, err := layer.Institution().Get(context.Background(), model.NewId())
	if got != nil {
		t.Fatalf("Get() result = %#v, want nil", got)
	}
	if err != wantErr {
		t.Fatalf("Get() error = %v, want exact error %v", err, wantErr)
	}
	if len(metrics.events) != 1 || metrics.events[0].outcome != OutcomeError {
		t.Fatalf("metrics = %#v, want one error event", metrics.events)
	}
}

func TestRecorderPanicCannotChangeStoreSemantics(t *testing.T) {
	t.Parallel()

	want := &model.Institution{ID: model.NewInstitutionID()}
	layer := newLayer(
		&rootStub{institution: &institutionStub{result: want}},
		RecorderFunc(func(Operation, Outcome, time.Duration) { panic("metrics unavailable") }),
		time.Now,
	)

	got, err := layer.Institution().Get(context.Background(), model.NewId())
	if err != nil || got != want {
		t.Fatalf("Get() = %#v, %v", got, err)
	}
}

func TestTimerMeasuresRootOperations(t *testing.T) {
	t.Parallel()

	wantErr := errors.New("database unavailable")
	metrics := &recordingMetrics{}
	layer := newLayer(&rootStub{pingErr: wantErr}, metrics, time.Now)
	if err := layer.Ping(context.Background()); err != wantErr {
		t.Fatalf("Ping() error = %v, want %v", err, wantErr)
	}
	if len(metrics.events) != 1 || metrics.events[0].operation.String() != "store.ping" {
		t.Fatalf("metrics = %#v", metrics.events)
	}
}

func TestNewRejectsMissingDependencies(t *testing.T) {
	t.Parallel()

	if _, err := New(nil, NopRecorder{}); err == nil {
		t.Fatal("New() accepted a nil store")
	}
	if _, err := New(&rootStub{}, nil); err == nil {
		t.Fatal("New() accepted a nil recorder")
	}
}

func TestTimerPreservesMissingPerModelStores(t *testing.T) {
	t.Parallel()

	layer := newLayer(&rootStub{}, NopRecorder{}, time.Now)
	if layer.Institution() != nil {
		t.Fatal("Institution() wrapped a nil underlying store")
	}
}

func TestGeneratedForwardingIsCurrent(t *testing.T) {
	t.Parallel()

	temporary := t.TempDir()
	generatedPath := filepath.Join(temporary, "forwarding_gen.go")
	command := exec.Command(
		"go", "run", "../storetest/layergen", "-layer", "timer", "-source", "..", "-output", generatedPath,
	)
	command.Env = append(os.Environ(), "GOCACHE="+filepath.Join(temporary, "go-cache"))
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
	if !bytes.Equal(got, want) {
		t.Fatal("forwarding_gen.go is stale; run go generate ./store/timerlayer")
	}
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
