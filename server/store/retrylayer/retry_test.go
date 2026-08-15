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
	institution          store.InstitutionStore
	examAuthoring        store.ExamAuthoringStore
	examSitting          store.ExamSittingStore
	examAttempt          store.ExamAttemptStore
	examAttemptWorkspace store.ExamAttemptWorkspaceStore
	personalAccessToken  store.PersonalAccessTokenStore
}

func (s *rootStub) Institution() store.InstitutionStore   { return s.institution }
func (s *rootStub) AcademicUnit() store.AcademicUnitStore { return nil }
func (s *rootStub) ExamAuthoring() store.ExamAuthoringStore {
	return s.examAuthoring
}
func (s *rootStub) ExamSitting() store.ExamSittingStore { return s.examSitting }
func (s *rootStub) ExamAttempt() store.ExamAttemptStore { return s.examAttempt }
func (s *rootStub) ExamAttemptWorkspace() store.ExamAttemptWorkspaceStore {
	return s.examAttemptWorkspace
}
func (s *rootStub) PersonalAccessToken() store.PersonalAccessTokenStore {
	return s.personalAccessToken
}

type examAuthoringStub struct {
	store.ExamAuthoringStore
	createAttempts  int
	updateAttempts  int
	focusAttempts   int
	listAttempts    int
	archiveAttempts int
	accessAttempts  int
	getAttempts     int
	resolveAttempts int
	err             error
}

func (s *examAuthoringStub) Create(context.Context, *store.ExamAuthoringCreation, *store.CommandIdempotency) (*store.ExamAuthoringCommandResult, error) {
	s.createAttempts++
	return nil, s.err
}

func (s *examAuthoringStub) UpdateDraftText(context.Context, *store.ExamDraftTextUpdate, *store.CommandIdempotency) (*store.ExamAuthoringCommandResult, error) {
	s.updateAttempts++
	return nil, s.err
}

func (s *examAuthoringStub) UpdateDraftFocusLoss(context.Context, *store.ExamDraftFocusLossUpdate, *store.CommandIdempotency) (*store.ExamAuthoringCommandResult, error) {
	s.focusAttempts++
	return nil, s.err
}

func (s *examAuthoringStub) List(context.Context, store.ExamListOptions) ([]store.ExamSummary, error) {
	s.listAttempts++
	return nil, s.err
}

func (s *examAuthoringStub) Archive(context.Context, *store.ExamArchive, *store.CommandIdempotency) (*store.ExamArchiveCommandResult, error) {
	s.archiveAttempts++
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

type examSittingUnsafeMutationStub struct {
	store.ExamSittingStore
	advanceAttempts int
	closeAttempts   int
	err             error
}

type examAttemptRetryStub struct {
	store.ExamAttemptStore
	connectAttempts       int
	renewAttempts         int
	resolveExpiryAttempts int
	listExpiryAttempts    int
	expireAttempts        int
	reallowAttempts       int
	closeAttempts         int
	err                   error
}

type examAttemptWorkspaceRetryStub struct {
	store.ExamAttemptWorkspaceStore
	applyAttempts    int
	markAttempts     int
	claimAttempts    int
	completeAttempts int
	releaseAttempts  int
	err              error
}

func (stub *examAttemptWorkspaceRetryStub) ApplyMutation(context.Context, *store.ExamAttemptWorkspaceMutation, *store.CommandIdempotency) (*store.ExamAttemptWorkspaceMutationResult, error) {
	stub.applyAttempts++
	return nil, stub.err
}

func (stub *examAttemptWorkspaceRetryStub) MarkObjectReclaimable(context.Context, model.AttemptWorkspaceObjectID) error {
	stub.markAttempts++
	return stub.err
}

func (stub *examAttemptWorkspaceRetryStub) ClaimObjectsForCleanup(context.Context, int, string) ([]model.AttemptWorkspaceObject, error) {
	stub.claimAttempts++
	return nil, stub.err
}

func (stub *examAttemptWorkspaceRetryStub) CompleteObjectCleanup(context.Context, model.AttemptWorkspaceObjectID, string) error {
	stub.completeAttempts++
	return stub.err
}

func (stub *examAttemptWorkspaceRetryStub) ReleaseObjectCleanup(context.Context, model.AttemptWorkspaceObjectID, string) error {
	stub.releaseAttempts++
	return stub.err
}

func (stub *examAttemptRetryStub) Connect(context.Context, *store.ExamAttemptConnect, *store.CommandIdempotency) (*store.ExamAttemptConnectResult, error) {
	stub.connectAttempts++
	return nil, stub.err
}

func (stub *examAttemptRetryStub) CloseConnection(context.Context, *store.ExamAttemptConnectionClose) (*store.ExamAttemptConnectionCloseResult, error) {
	stub.closeAttempts++
	return nil, stub.err
}

func (stub *examAttemptRetryStub) RenewParticipation(context.Context, *store.ExamAttemptParticipationRenewal) (*store.ExamAttemptParticipationRenewalResult, error) {
	stub.renewAttempts++
	return nil, stub.err
}

func (stub *examAttemptRetryStub) ResolveParticipationExpiry(context.Context, model.ExamAttemptID, model.AttemptParticipationID, int64) (*store.ExamAttemptParticipationExpiryDue, error) {
	stub.resolveExpiryAttempts++
	return nil, stub.err
}

func (stub *examAttemptRetryStub) ListExpiredParticipations(context.Context, int) ([]store.ExamAttemptParticipationExpiryDue, error) {
	stub.listExpiryAttempts++
	return nil, stub.err
}

func (stub *examAttemptRetryStub) ExpireParticipation(context.Context, *store.ExamAttemptParticipationExpiry) (*store.ExamAttemptParticipationExpiryResult, error) {
	stub.expireAttempts++
	return nil, stub.err
}

func (stub *examAttemptRetryStub) ReallowAttempt(context.Context, *store.ExamAttemptReallow, *store.CommandIdempotency) (*store.ExamAttemptReallowResult, error) {
	stub.reallowAttempts++
	return nil, stub.err
}

func (stub *examSittingUnsafeMutationStub) AdvanceDue(context.Context, *store.ExamSittingDueAdvance) (*store.ExamSittingLifecycleResult, error) {
	stub.advanceAttempts++
	return nil, stub.err
}

func (stub *examSittingUnsafeMutationStub) CloseIfNoAttempts(context.Context, *store.ExamSittingCloseIfNoAttempts) (*store.ExamSittingLifecycleResult, error) {
	stub.closeAttempts++
	return nil, stub.err
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

func TestRetryNeverRetriesSystemSittingMutationsWithoutCommandOutcomes(t *testing.T) {
	t.Parallel()
	transientErr := errors.New("unknown commit outcome")
	stub := &examSittingUnsafeMutationStub{err: transientErr}
	layer, err := retrylayer.New(&rootStub{examSitting: stub}, retrylayer.Policy{
		MaxAttempts: 3, InitialBackoff: time.Nanosecond, MaxBackoff: time.Nanosecond,
		IsTransient: func(error) bool { return true },
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, got := layer.ExamSitting().AdvanceDue(context.Background(), &store.ExamSittingDueAdvance{}); got != transientErr {
		t.Fatalf("AdvanceDue() error = %v", got)
	}
	if _, got := layer.ExamSitting().CloseIfNoAttempts(context.Background(), &store.ExamSittingCloseIfNoAttempts{}); got != transientErr {
		t.Fatalf("CloseIfNoAttempts() error = %v", got)
	}
	if stub.advanceAttempts != 1 || stub.closeAttempts != 1 {
		t.Fatalf("system mutation attempts = advance %d close %d", stub.advanceAttempts, stub.closeAttempts)
	}
}

func TestRetryOnlyRetriesExamAttemptMutationWithDurableCommandOutcome(t *testing.T) {
	t.Parallel()
	transientErr := errors.New("unknown commit outcome")
	stub := &examAttemptRetryStub{err: transientErr}
	layer, err := retrylayer.New(&rootStub{examAttempt: stub}, retrylayer.Policy{
		MaxAttempts: 3, InitialBackoff: time.Nanosecond, MaxBackoff: time.Nanosecond,
		IsTransient: func(error) bool { return true },
	})
	if err != nil {
		t.Fatal(err)
	}
	command := &store.CommandIdempotency{UserID: model.NewUserID()}
	if _, got := layer.ExamAttempt().Connect(context.Background(), &store.ExamAttemptConnect{}, command); got != transientErr {
		t.Fatalf("Connect() error = %v", got)
	}
	if _, got := layer.ExamAttempt().CloseConnection(context.Background(), &store.ExamAttemptConnectionClose{}); got != transientErr {
		t.Fatalf("CloseConnection() error = %v", got)
	}
	if stub.connectAttempts != 3 || stub.closeAttempts != 1 {
		t.Fatalf("Exam Attempt mutation attempts = connect %d close %d", stub.connectAttempts, stub.closeAttempts)
	}
}

func TestRetryExamAttemptRenewalExpiryAndReallowUseTheirDurableFences(t *testing.T) {
	t.Parallel()
	transientErr := errors.New("unknown commit outcome")
	stub := &examAttemptRetryStub{err: transientErr}
	layer, err := retrylayer.New(&rootStub{examAttempt: stub}, retrylayer.Policy{
		MaxAttempts: 3, InitialBackoff: time.Nanosecond, MaxBackoff: time.Nanosecond,
		IsTransient: func(error) bool { return true },
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	_, _ = layer.ExamAttempt().RenewParticipation(ctx, &store.ExamAttemptParticipationRenewal{})
	_, _ = layer.ExamAttempt().ResolveParticipationExpiry(ctx, model.NewExamAttemptID(), model.NewAttemptParticipationID(), 1)
	_, _ = layer.ExamAttempt().ListExpiredParticipations(ctx, 10)
	_, _ = layer.ExamAttempt().ExpireParticipation(ctx, &store.ExamAttemptParticipationExpiry{})
	_, _ = layer.ExamAttempt().ReallowAttempt(ctx, &store.ExamAttemptReallow{}, &store.CommandIdempotency{})
	if stub.renewAttempts != 3 || stub.resolveExpiryAttempts != 3 || stub.listExpiryAttempts != 3 ||
		stub.expireAttempts != 1 || stub.reallowAttempts != 3 {
		t.Fatalf("attempts renew/resolve/list/expire/reallow = %d/%d/%d/%d/%d, want 3/3/3/1/3",
			stub.renewAttempts, stub.resolveExpiryAttempts, stub.listExpiryAttempts, stub.expireAttempts, stub.reallowAttempts)
	}
	_, _ = layer.ExamAttempt().ReallowAttempt(ctx, &store.ExamAttemptReallow{}, nil)
	if stub.reallowAttempts != 4 {
		t.Fatalf("ReallowAttempt() attempts without command = %d, want one additional call", stub.reallowAttempts)
	}
}

func TestRetryOnlyRetriesExamAttemptWorkspaceMutationWithDurableCommandOutcome(t *testing.T) {
	t.Parallel()

	transientErr := errors.New("unknown commit outcome")
	stub := &examAttemptWorkspaceRetryStub{err: transientErr}
	layer, err := retrylayer.New(&rootStub{examAttemptWorkspace: stub}, retrylayer.Policy{
		MaxAttempts: 3, InitialBackoff: time.Nanosecond, MaxBackoff: time.Nanosecond,
		IsTransient: func(error) bool { return true },
	})
	if err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	if _, got := layer.ExamAttemptWorkspace().ApplyMutation(ctx, &store.ExamAttemptWorkspaceMutation{}, &store.CommandIdempotency{}); got != transientErr {
		t.Fatalf("ApplyMutation() error = %v", got)
	}
	if stub.applyAttempts != 3 {
		t.Fatalf("ApplyMutation() attempts with command = %d, want 3", stub.applyAttempts)
	}
	if _, got := layer.ExamAttemptWorkspace().ApplyMutation(ctx, &store.ExamAttemptWorkspaceMutation{}, nil); got != transientErr {
		t.Fatalf("ApplyMutation() without command error = %v", got)
	}
	if stub.applyAttempts != 4 {
		t.Fatalf("ApplyMutation() attempts without command = %d, want one additional call", stub.applyAttempts)
	}
}

func TestRetryForwardsExamAttemptWorkspaceCleanupMutationsOnce(t *testing.T) {
	t.Parallel()

	transientErr := errors.New("unknown cleanup outcome")
	stub := &examAttemptWorkspaceRetryStub{err: transientErr}
	layer, err := retrylayer.New(&rootStub{examAttemptWorkspace: stub}, retrylayer.Policy{
		MaxAttempts: 3, InitialBackoff: time.Nanosecond, MaxBackoff: time.Nanosecond,
		IsTransient: func(error) bool { return true },
	})
	if err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	objectID := model.NewAttemptWorkspaceObjectID()
	_ = layer.ExamAttemptWorkspace().MarkObjectReclaimable(ctx, objectID)
	_, _ = layer.ExamAttemptWorkspace().ClaimObjectsForCleanup(ctx, 1, "claim")
	_ = layer.ExamAttemptWorkspace().CompleteObjectCleanup(ctx, objectID, "claim")
	_ = layer.ExamAttemptWorkspace().ReleaseObjectCleanup(ctx, objectID, "claim")
	if stub.markAttempts != 1 || stub.claimAttempts != 1 || stub.completeAttempts != 1 || stub.releaseAttempts != 1 {
		t.Fatalf("cleanup attempts = mark %d, claim %d, complete %d, release %d; want one each",
			stub.markAttempts, stub.claimAttempts, stub.completeAttempts, stub.releaseAttempts)
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
	_, _ = layer.ExamAuthoring().UpdateDraftText(context.Background(), &store.ExamDraftTextUpdate{}, &store.CommandIdempotency{})
	_, _ = layer.ExamAuthoring().UpdateDraftFocusLoss(context.Background(), &store.ExamDraftFocusLossUpdate{}, &store.CommandIdempotency{})
	_, _ = layer.ExamAuthoring().List(context.Background(), store.ExamListOptions{})
	_, _ = layer.ExamAuthoring().Archive(context.Background(), &store.ExamArchive{}, &store.CommandIdempotency{})
	_, _ = layer.ExamAuthoring().Access(context.Background(), model.NewExamID(), model.NewUserID())
	_, _ = layer.ExamAuthoring().Get(context.Background(), model.NewExamID(), model.NewUserID())
	_, _ = layer.ExamAuthoring().Resolve(context.Background(), model.NewExamID())
	if stub.createAttempts != 3 || stub.updateAttempts != 3 || stub.focusAttempts != 3 || stub.listAttempts != 3 || stub.archiveAttempts != 3 || stub.accessAttempts != 3 || stub.getAttempts != 3 || stub.resolveAttempts != 3 {
		t.Fatalf("attempts create/update/focus/list/archive/access/get/resolve = %d/%d/%d/%d/%d/%d/%d/%d, want all 3",
			stub.createAttempts, stub.updateAttempts, stub.focusAttempts, stub.listAttempts, stub.archiveAttempts, stub.accessAttempts, stub.getAttempts, stub.resolveAttempts)
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
	_, _ = layer.ExamAuthoring().UpdateDraftText(context.Background(), &store.ExamDraftTextUpdate{}, nil)
	_, _ = layer.ExamAuthoring().UpdateDraftFocusLoss(context.Background(), &store.ExamDraftFocusLossUpdate{}, nil)
	_, _ = layer.ExamAuthoring().Archive(context.Background(), &store.ExamArchive{}, nil)
	if stub.createAttempts != 1 {
		t.Fatalf("Create() attempts = %d, want 1 without idempotency", stub.createAttempts)
	}
	if stub.updateAttempts != 1 {
		t.Fatalf("UpdateDraftText() attempts = %d, want 1 without idempotency", stub.updateAttempts)
	}
	if stub.focusAttempts != 1 {
		t.Fatalf("UpdateDraftFocusLoss() attempts = %d, want 1 without idempotency", stub.focusAttempts)
	}
	if stub.archiveAttempts != 1 {
		t.Fatalf("Archive() attempts = %d, want 1 without idempotency", stub.archiveAttempts)
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
