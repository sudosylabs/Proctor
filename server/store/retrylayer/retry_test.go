// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package retrylayer_test

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
	"github.com/sudosylabs/proctor/server/store/retrylayer"
)

type institutionStub struct {
	store.InstitutionStore
	getAttempts  int
	saveAttempts int
	get          func() (*model.Institution, error)
	saveErr      error
}

func (s *institutionStub) Get(context.Context, string) (*model.Institution, error) {
	s.getAttempts++
	return s.get()
}

func (s *institutionStub) Save(context.Context, *model.Institution) (*model.Institution, error) {
	s.saveAttempts++
	return nil, s.saveErr
}

type rootStub struct {
	store.Store
	institution         store.InstitutionStore
	examAuthoring       store.ExamAuthoringStore
	personalAccessToken store.PersonalAccessTokenStore
}

func (s *rootStub) Institution() store.InstitutionStore   { return s.institution }
func (s *rootStub) AcademicUnit() store.AcademicUnitStore { return nil }
func (s *rootStub) ExamAuthoring() store.ExamAuthoringStore {
	return s.examAuthoring
}
func (s *rootStub) PersonalAccessToken() store.PersonalAccessTokenStore {
	return s.personalAccessToken
}

type examAuthoringStub struct {
	store.ExamAuthoringStore
	createAttempts  int
	accessAttempts  int
	getAttempts     int
	resolveAttempts int
	err             error
}

func (s *examAuthoringStub) Create(context.Context, *store.ExamAuthoringCreation, *store.CommandIdempotency) (*store.ExamAuthoringCommandResult, error) {
	s.createAttempts++
	return nil, s.err
}

func (s *examAuthoringStub) Access(context.Context, model.ExamID, model.UserID) (*store.ExamAccessSnapshot, error) {
	s.accessAttempts++
	return nil, s.err
}

func (s *examAuthoringStub) Get(context.Context, model.ExamID, model.UserID) (*store.ExamAuthoringSnapshot, error) {
	s.getAttempts++
	return nil, s.err
}

func (s *examAuthoringStub) Resolve(context.Context, model.ExamID) (*model.Exam, error) {
	s.resolveAttempts++
	return nil, s.err
}

type personalAccessTokenStub struct {
	store.PersonalAccessTokenStore
	attempts int
	err      error
}

func (s *personalAccessTokenStub) Resolve(context.Context, string, int64, int64) (*store.PersonalAccessTokenResolution, error) {
	s.attempts++
	return nil, s.err
}

func TestRetryAllowlistedReadPreservesEventualResult(t *testing.T) {
	t.Parallel()

	transientErr := errors.New("serialization failure")
	want := &model.Institution{ID: model.NewInstitutionID(), DisplayName: "Northbridge"}
	stub := &institutionStub{}
	stub.get = func() (*model.Institution, error) {
		if stub.getAttempts < 3 {
			return nil, transientErr
		}
		return want, nil
	}
	layer, err := retrylayer.New(&rootStub{institution: stub}, retrylayer.Policy{
		MaxAttempts:    3,
		InitialBackoff: time.Nanosecond,
		MaxBackoff:     time.Nanosecond,
		IsTransient:    func(err error) bool { return err == transientErr },
	})
	if err != nil {
		t.Fatal(err)
	}

	got, err := layer.Institution().Get(context.Background(), model.NewId())
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("Get() = %#v, want original pointer %#v", got, want)
	}
	if stub.getAttempts != 3 {
		t.Fatalf("Get() attempts = %d, want 3", stub.getAttempts)
	}
}

func TestRetryNeverRetriesUnsafeMutation(t *testing.T) {
	t.Parallel()

	transientErr := errors.New("serialization failure")
	stub := &institutionStub{saveErr: transientErr, get: func() (*model.Institution, error) { return nil, nil }}
	layer, err := retrylayer.New(&rootStub{institution: stub}, retrylayer.Policy{
		MaxAttempts:    3,
		InitialBackoff: time.Nanosecond,
		MaxBackoff:     time.Nanosecond,
		IsTransient:    func(error) bool { return true },
	})
	if err != nil {
		t.Fatal(err)
	}

	_, gotErr := layer.Institution().Save(context.Background(), &model.Institution{})
	if gotErr != transientErr {
		t.Fatalf("Save() error = %v, want exact error %v", gotErr, transientErr)
	}
	if stub.saveAttempts != 1 {
		t.Fatalf("Save() attempts = %d, want 1", stub.saveAttempts)
	}
}

func TestRetryDoesNotTreatMutatingResolveAsARead(t *testing.T) {
	t.Parallel()

	transientErr := errors.New("serialization failure")
	stub := &personalAccessTokenStub{err: transientErr}
	policy := retrylayer.Policy{
		MaxAttempts:    3,
		InitialBackoff: time.Nanosecond,
		MaxBackoff:     time.Nanosecond,
		IsTransient:    func(error) bool { return true },
	}
	layer, err := retrylayer.New(&rootStub{personalAccessToken: stub}, policy)
	if err != nil {
		t.Fatal(err)
	}

	_, gotErr := layer.PersonalAccessToken().Resolve(context.Background(), "hash", 1, 2)
	if gotErr != transientErr {
		t.Fatalf("Resolve() error = %v, want exact error %v", gotErr, transientErr)
	}
	if stub.attempts != 1 {
		t.Fatalf("Resolve() attempts = %d, want 1", stub.attempts)
	}
}

func TestRetryExamAuthoringIdempotentCreateAndReads(t *testing.T) {
	t.Parallel()

	transientErr := errors.New("serialization failure")
	stub := &examAuthoringStub{err: transientErr}
	layer, err := retrylayer.New(&rootStub{examAuthoring: stub}, retrylayer.Policy{
		MaxAttempts: 3, InitialBackoff: time.Nanosecond, MaxBackoff: time.Nanosecond,
		IsTransient: func(err error) bool { return err == transientErr },
	})
	if err != nil {
		t.Fatal(err)
	}

	_, _ = layer.ExamAuthoring().Create(context.Background(), &store.ExamAuthoringCreation{}, &store.CommandIdempotency{})
	_, _ = layer.ExamAuthoring().Access(context.Background(), model.NewExamID(), model.NewUserID())
	_, _ = layer.ExamAuthoring().Get(context.Background(), model.NewExamID(), model.NewUserID())
	_, _ = layer.ExamAuthoring().Resolve(context.Background(), model.NewExamID())
	if stub.createAttempts != 3 || stub.accessAttempts != 3 || stub.getAttempts != 3 || stub.resolveAttempts != 3 {
		t.Fatalf("attempts create/access/get/resolve = %d/%d/%d/%d, want 3/3/3/3",
			stub.createAttempts, stub.accessAttempts, stub.getAttempts, stub.resolveAttempts)
	}
}

func TestRetryExamAuthoringDoesNotRetryCreateWithoutIdempotency(t *testing.T) {
	t.Parallel()

	stub := &examAuthoringStub{err: errors.New("serialization failure")}
	layer, err := retrylayer.New(&rootStub{examAuthoring: stub}, retrylayer.Policy{
		MaxAttempts: 3, InitialBackoff: time.Nanosecond, MaxBackoff: time.Nanosecond,
		IsTransient: func(error) bool { return true },
	})
	if err != nil {
		t.Fatal(err)
	}
	_, _ = layer.ExamAuthoring().Create(context.Background(), &store.ExamAuthoringCreation{}, nil)
	if stub.createAttempts != 1 {
		t.Fatalf("Create() attempts = %d, want 1 without idempotency", stub.createAttempts)
	}
}

func TestRetryPassesUnclassifiedErrorThroughUnchanged(t *testing.T) {
	t.Parallel()

	domainErr := store.NewErrNotFound("institution", "missing")
	stub := &institutionStub{get: func() (*model.Institution, error) { return nil, domainErr }}
	layer := newLayer(t, stub, func(error) bool { return false }, 3, time.Nanosecond)

	_, gotErr := layer.Institution().Get(context.Background(), "missing")
	if gotErr != domainErr {
		t.Fatalf("Get() error = %v, want exact domain error %v", gotErr, domainErr)
	}
	if stub.getAttempts != 1 {
		t.Fatalf("Get() attempts = %d, want 1", stub.getAttempts)
	}
}

func TestRetryReturnsExactTransientErrorAfterBoundedAttempts(t *testing.T) {
	t.Parallel()

	transientErr := errors.New("deadlock detected")
	stub := &institutionStub{get: func() (*model.Institution, error) { return nil, transientErr }}
	layer := newLayer(t, stub, func(err error) bool { return err == transientErr }, 3, time.Nanosecond)

	_, gotErr := layer.Institution().Get(context.Background(), model.NewId())
	if gotErr != transientErr {
		t.Fatalf("Get() error = %v, want exact transient error %v", gotErr, transientErr)
	}
	if stub.getAttempts != 3 {
		t.Fatalf("Get() attempts = %d, want 3", stub.getAttempts)
	}
}

func TestRetryCancellationStopsBeforeAnotherAttempt(t *testing.T) {
	t.Parallel()

	transientErr := errors.New("serialization failure")
	stub := &institutionStub{get: func() (*model.Institution, error) { return nil, transientErr }}
	layer := newLayer(t, stub, func(error) bool { return true }, 3, time.Hour)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	startedAt := time.Now()
	_, gotErr := layer.Institution().Get(ctx, model.NewId())
	if gotErr != context.Canceled {
		t.Fatalf("Get() error = %v, want context.Canceled", gotErr)
	}
	if elapsed := time.Since(startedAt); elapsed > time.Second {
		t.Fatalf("cancellation took %s", elapsed)
	}
	if stub.getAttempts != 1 {
		t.Fatalf("Get() attempts = %d, want 1", stub.getAttempts)
	}
}

func TestRetryDeadlineStopsBeforeAnotherAttempt(t *testing.T) {
	t.Parallel()

	transientErr := errors.New("serialization failure")
	stub := &institutionStub{get: func() (*model.Institution, error) { return nil, transientErr }}
	layer := newLayer(t, stub, func(error) bool { return true }, 3, time.Hour)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()

	startedAt := time.Now()
	_, gotErr := layer.Institution().Get(ctx, model.NewId())
	if gotErr != context.DeadlineExceeded {
		t.Fatalf("Get() error = %v, want context.DeadlineExceeded", gotErr)
	}
	if elapsed := time.Since(startedAt); elapsed > time.Second {
		t.Fatalf("deadline took %s", elapsed)
	}
	if stub.getAttempts != 1 {
		t.Fatalf("Get() attempts = %d, want 1", stub.getAttempts)
	}
}

func TestRetryPreservesStableAndMissingAccessors(t *testing.T) {
	t.Parallel()

	stub := &institutionStub{get: func() (*model.Institution, error) { return nil, nil }}
	layer := newLayer(t, stub, func(error) bool { return false }, 3, time.Nanosecond)
	if layer.Institution() != layer.Institution() {
		t.Fatal("Institution() returned different wrappers")
	}
	if layer.AcademicUnit() != nil {
		t.Fatal("AcademicUnit() wrapped a nil underlying store")
	}
}

func TestNewRejectsUnboundedOrIncompletePolicy(t *testing.T) {
	t.Parallel()

	valid := retrylayer.DefaultPolicy(func(error) bool { return false })
	tests := []struct {
		name   string
		policy retrylayer.Policy
	}{
		{name: "zero attempts", policy: withAttempts(valid, 0)},
		{name: "too many attempts", policy: withAttempts(valid, 11)},
		{name: "nonpositive initial backoff", policy: withInitialBackoff(valid, 0)},
		{name: "maximum below initial", policy: withMaxBackoff(valid, time.Nanosecond)},
		{name: "missing classifier", policy: withClassifier(valid, nil)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if _, err := retrylayer.New(&rootStub{}, test.policy); err == nil {
				t.Fatal("New() accepted invalid policy")
			}
		})
	}
	if _, err := retrylayer.New(nil, valid); err == nil {
		t.Fatal("New() accepted nil store")
	}
}

func TestRetryGeneratedForwardingIsCurrent(t *testing.T) {
	t.Parallel()

	temporary := t.TempDir()
	generatedPath := filepath.Join(temporary, "forwarding_gen.go")
	command := exec.Command(
		"go", "run", "../storetest/layergen", "-layer", "retry", "-source", "..", "-output", generatedPath,
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
		t.Fatal("forwarding_gen.go is stale; run go generate ./store/retrylayer")
	}
}

func withAttempts(policy retrylayer.Policy, attempts int) retrylayer.Policy {
	policy.MaxAttempts = attempts
	return policy
}

func withInitialBackoff(policy retrylayer.Policy, backoff time.Duration) retrylayer.Policy {
	policy.InitialBackoff = backoff
	return policy
}

func withMaxBackoff(policy retrylayer.Policy, backoff time.Duration) retrylayer.Policy {
	policy.MaxBackoff = backoff
	return policy
}

func withClassifier(policy retrylayer.Policy, classifier func(error) bool) retrylayer.Policy {
	policy.IsTransient = classifier
	return policy
}

func newLayer(
	t *testing.T,
	institution store.InstitutionStore,
	classifier func(error) bool,
	attempts int,
	backoff time.Duration,
) *retrylayer.Layer {
	t.Helper()
	layer, err := retrylayer.New(&rootStub{institution: institution}, retrylayer.Policy{
		MaxAttempts:    attempts,
		InitialBackoff: backoff,
		MaxBackoff:     backoff,
		IsTransient:    classifier,
	})
	if err != nil {
		t.Fatal(err)
	}
	return layer
}
