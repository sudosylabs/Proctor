// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

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

	"github.com/stretchr/testify/require"

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

func (s *institutionStub) Get(context.Context, model.InstitutionID) (*model.Institution, error) {
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
	passwordCredential   store.PasswordCredentialStore
	invitation           store.InvitationStore
}

type retryRecorder struct{ outcomes []string }

func (r *retryRecorder) ObserveStoreRetry(outcome string) { r.outcomes = append(r.outcomes, outcome) }

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
func (s *rootStub) Invitation() store.InvitationStore                 { return s.invitation }
func (s *rootStub) PasswordCredential() store.PasswordCredentialStore { return s.passwordCredential }

type passwordCredentialStub struct {
	store.PasswordCredentialStore
	input    *store.PasswordCredentialRehash
	attempts int
	err      error
}

func (s *passwordCredentialStub) Rehash(_ context.Context, input *store.PasswordCredentialRehash) error {
	s.attempts++
	s.input = input
	return s.err
}

type invitationRetryStub struct {
	store.InvitationStore
	issueAttempts  int
	resendAttempts int
	err            error
}

func (s *invitationRetryStub) IssueStudentClassIdempotently(context.Context, *store.StudentClassInvitationIssue, *store.CommandIdempotency) (*store.InvitationCommandResult, error) {
	s.issueAttempts++
	return nil, s.err
}

func (s *invitationRetryStub) ResendIdempotently(context.Context, *store.InvitationResend, *store.CommandIdempotency) (*store.InvitationAdministrationCommandResult, error) {
	s.resendAttempts++
	return nil, s.err
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
	finishAttempts  int
	err             error
}

type examAttemptRetryStub struct {
	store.ExamAttemptStore
	connectAttempts       int
	renewAttempts         int
	resolveFocusAttempts  int
	recordFocusAttempts   int
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

func (s *examAttemptWorkspaceRetryStub) ApplyMutation(context.Context, *store.ExamAttemptWorkspaceMutation, *store.CommandIdempotency) (*store.ExamAttemptWorkspaceMutationResult, error) {
	s.applyAttempts++
	return nil, s.err
}

func (s *examAttemptWorkspaceRetryStub) MarkObjectReclaimable(context.Context, model.AttemptWorkspaceObjectID) error {
	s.markAttempts++
	return s.err
}

func (s *examAttemptWorkspaceRetryStub) ClaimObjectsForCleanup(ctx context.Context, input *store.AttemptWorkspaceCleanupClaim) ([]model.AttemptWorkspaceObject, error) {
	s.claimAttempts++
	return nil, s.err
}

func (s *examAttemptWorkspaceRetryStub) CompleteObjectCleanup(ctx context.Context, input *store.AttemptWorkspaceCleanupCompletion) error {
	s.completeAttempts++
	return s.err
}

func (s *examAttemptWorkspaceRetryStub) ReleaseObjectCleanup(ctx context.Context, input *store.AttemptWorkspaceCleanupRelease) error {
	s.releaseAttempts++
	return s.err
}

func (s *examAttemptRetryStub) Connect(context.Context, *store.ExamAttemptConnect, *store.CommandIdempotency) (*store.ExamAttemptConnectResult, error) {
	s.connectAttempts++
	return nil, s.err
}

func (s *examAttemptRetryStub) CloseConnection(context.Context, *store.ExamAttemptConnectionClose) (*store.ExamAttemptConnectionCloseResult, error) {
	s.closeAttempts++
	return nil, s.err
}

func (s *examAttemptRetryStub) RenewParticipation(context.Context, *store.ExamAttemptParticipationRenewal) (*store.ExamAttemptParticipationRenewalResult, error) {
	s.renewAttempts++
	return nil, s.err
}

func (s *examAttemptRetryStub) ResolveFocusLossTarget(context.Context, store.ExamAttemptFocusLossAccess) (*store.ExamAttemptFocusLossTarget, error) {
	s.resolveFocusAttempts++
	return nil, s.err
}

func (s *examAttemptRetryStub) RecordFocusLoss(context.Context, *store.ExamAttemptFocusLossSignal) (*store.ExamAttemptFocusLossResult, error) {
	s.recordFocusAttempts++
	return nil, s.err
}

func (s *examAttemptRetryStub) ResolveParticipationExpiry(context.Context, model.ExamAttemptID, model.AttemptParticipationID, int64) (*store.ExamAttemptParticipationExpiryDue, error) {
	s.resolveExpiryAttempts++
	return nil, s.err
}

func (s *examAttemptRetryStub) ListExpiredParticipations(context.Context, int) ([]store.ExamAttemptParticipationExpiryDue, error) {
	s.listExpiryAttempts++
	return nil, s.err
}

func (s *examAttemptRetryStub) ExpireParticipation(context.Context, *store.ExamAttemptParticipationExpiry) (*store.ExamAttemptParticipationExpiryResult, error) {
	s.expireAttempts++
	return nil, s.err
}

func (s *examAttemptRetryStub) ReallowAttempt(context.Context, *store.ExamAttemptReallow, *store.CommandIdempotency) (*store.ExamAttemptReallowResult, error) {
	s.reallowAttempts++
	return nil, s.err
}

func (s *examSittingUnsafeMutationStub) AdvanceDue(context.Context, *store.ExamSittingDueAdvance) (*store.ExamSittingLifecycleResult, error) {
	s.advanceAttempts++
	return nil, s.err
}

func (s *examSittingUnsafeMutationStub) FinishSealing(context.Context, *store.ExamSittingFinishSealing) (*store.ExamSittingLifecycleResult, error) {
	s.finishAttempts++
	return nil, s.err
}

func (s *personalAccessTokenStub) Resolve(context.Context, string, time.Time, time.Duration) (*store.PersonalAccessTokenResolution, error) {
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
	layer, err := retrylayer.NewWithRecorder(&rootStub{institution: stub}, retrylayer.Policy{
		MaxAttempts:    3,
		InitialBackoff: time.Nanosecond,
		MaxBackoff:     time.Nanosecond,
		IsTransient:    func(err error) bool { return err == transientErr },
	}, nil)
	require.NoError(t, err)

	got, err := layer.Institution().Get(context.Background(), model.NewInstitutionID())
	require.NoError(t, err)
	require.Same(t, want, got, "Get()")
	require.Equal(t, 3, stub.getAttempts, "Get() attempts")
}

func TestRetryNeverRetriesUnsafeMutation(t *testing.T) {
	t.Parallel()

	transientErr := errors.New("serialization failure")
	stub := &institutionStub{saveErr: transientErr, get: func() (*model.Institution, error) { return nil, nil }}
	layer, err := retrylayer.NewWithRecorder(&rootStub{institution: stub}, retrylayer.Policy{
		MaxAttempts:    3,
		InitialBackoff: time.Nanosecond,
		MaxBackoff:     time.Nanosecond,
		IsTransient:    func(error) bool { return true },
	}, nil)
	require.NoError(t, err)

	_, gotErr := layer.Institution().Save(context.Background(), &model.Institution{})
	require.Equal(t, transientErr, gotErr, "Save() error")
	require.Equal(t, 1, stub.saveAttempts, "Save() attempts")
}

func TestRetryNeverRetriesPasswordRehashWithUnknownCommitOutcome(t *testing.T) {
	t.Parallel()
	unknown := errors.New("unknown commit outcome")
	stub := &passwordCredentialStub{err: unknown}
	layer, err := retrylayer.NewWithRecorder(&rootStub{passwordCredential: stub}, retrylayer.Policy{
		MaxAttempts: 3, InitialBackoff: time.Nanosecond, MaxBackoff: time.Nanosecond,
		IsTransient: func(error) bool { return true },
	}, nil)
	require.NoError(t, err)
	input := &store.PasswordCredentialRehash{ID: model.NewPasswordCredentialID(), UserID: model.NewUserID(),
		ExpectedHash: "encoded-original", ExpectedRevision: 1, PasswordHash: "encoded-rehash"}
	// Rehash must return the original error instance unchanged.
	require.Same(t, unknown, layer.PasswordCredential().Rehash(context.Background(), input), "Rehash() error, want original unknown-outcome error")
	require.Equal(t, 1, stub.attempts, "Rehash() attempts")
	require.Same(t, input, stub.input, "Rehash() attempts")
}

func TestRetryNeverRetriesSystemSittingMutationsWithoutCommandOutcomes(t *testing.T) {
	t.Parallel()
	transientErr := errors.New("unknown commit outcome")
	stub := &examSittingUnsafeMutationStub{err: transientErr}
	layer, err := retrylayer.NewWithRecorder(&rootStub{examSitting: stub}, retrylayer.Policy{
		MaxAttempts: 3, InitialBackoff: time.Nanosecond, MaxBackoff: time.Nanosecond,
		IsTransient: func(error) bool { return true },
	}, nil)
	require.NoError(t, err)
	_, got := layer.ExamSitting().AdvanceDue(context.Background(), &store.ExamSittingDueAdvance{})
	require.Equal(t, transientErr, got)
	_, got = layer.ExamSitting().FinishSealing(context.Background(), &store.ExamSittingFinishSealing{})
	require.Equal(t, transientErr, got)
	require.Equal(t, 1, stub.advanceAttempts, "system mutation attempts = advance")
	require.Equal(t, 1, stub.finishAttempts, "system mutation attempts = advance")
}

func TestRetryOnlyRetriesExamAttemptMutationWithDurableCommandOutcome(t *testing.T) {
	t.Parallel()
	transientErr := errors.New("unknown commit outcome")
	stub := &examAttemptRetryStub{err: transientErr}
	layer, err := retrylayer.NewWithRecorder(&rootStub{examAttempt: stub}, retrylayer.Policy{
		MaxAttempts: 3, InitialBackoff: time.Nanosecond, MaxBackoff: time.Nanosecond,
		IsTransient: func(error) bool { return true },
	}, nil)
	require.NoError(t, err)
	command := &store.CommandIdempotency{UserID: model.NewUserID()}
	_, got := layer.ExamAttempt().Connect(context.Background(), &store.ExamAttemptConnect{}, command)
	require.Equal(t, transientErr, got)
	_, got = layer.ExamAttempt().CloseConnection(context.Background(), &store.ExamAttemptConnectionClose{})
	require.Equal(t, transientErr, got)
	require.Equal(t, 3, stub.connectAttempts, "Exam Attempt mutation attempts = connect")
	require.Equal(t, 1, stub.closeAttempts, "Exam Attempt mutation attempts = connect")
}

func TestRetryInvitationMutationsOnlyWithDurableCommandOutcomes(t *testing.T) {
	t.Parallel()
	transientErr := errors.New("unknown Invitation commit outcome")
	stub := &invitationRetryStub{err: transientErr}
	layer, err := retrylayer.NewWithRecorder(&rootStub{invitation: stub}, retrylayer.Policy{
		MaxAttempts: 3, InitialBackoff: time.Nanosecond, MaxBackoff: time.Nanosecond,
		IsTransient: func(error) bool { return true },
	}, nil)
	require.NoError(t, err)
	command := &store.CommandIdempotency{UserID: model.NewUserID()}
	_, _ = layer.Invitation().IssueStudentClassIdempotently(context.Background(), &store.StudentClassInvitationIssue{}, command)
	_, _ = layer.Invitation().ResendIdempotently(context.Background(), &store.InvitationResend{}, command)
	_, _ = layer.Invitation().IssueStudentClassIdempotently(context.Background(), &store.StudentClassInvitationIssue{}, nil)
	require.Equal(t, 4, stub.issueAttempts, "Invitation attempts issue/resend")
	require.Equal(t, 3, stub.resendAttempts, "Invitation attempts issue/resend")
}

func TestRetryExamAttemptRenewalExpiryAndReallowUseTheirDurableFences(t *testing.T) {
	t.Parallel()
	transientErr := errors.New("unknown commit outcome")
	stub := &examAttemptRetryStub{err: transientErr}
	layer, err := retrylayer.NewWithRecorder(&rootStub{examAttempt: stub}, retrylayer.Policy{
		MaxAttempts: 3, InitialBackoff: time.Nanosecond, MaxBackoff: time.Nanosecond,
		IsTransient: func(error) bool { return true },
	}, nil)
	require.NoError(t, err)
	ctx := context.Background()
	_, _ = layer.ExamAttempt().RenewParticipation(ctx, &store.ExamAttemptParticipationRenewal{DesktopCompatibilityPolicyRevision: 1})
	_, _ = layer.ExamAttempt().ResolveParticipationExpiry(ctx, model.NewExamAttemptID(), model.NewAttemptParticipationID(), 1)
	_, _ = layer.ExamAttempt().ListExpiredParticipations(ctx, 10)
	_, _ = layer.ExamAttempt().ExpireParticipation(ctx, &store.ExamAttemptParticipationExpiry{})
	_, _ = layer.ExamAttempt().ReallowAttempt(ctx, &store.ExamAttemptReallow{}, &store.CommandIdempotency{})
	require.Equal(t, 3, stub.renewAttempts, "attempts renew/resolve/list/expire/reallow")
	require.Equal(t, 3, stub.resolveExpiryAttempts, "attempts renew/resolve/list/expire/reallow")
	require.Equal(t, 3, stub.listExpiryAttempts, "attempts renew/resolve/list/expire/reallow")
	require.Equal(t, 1, stub.expireAttempts, "attempts renew/resolve/list/expire/reallow")
	require.Equal(t, 3, stub.reallowAttempts, "attempts renew/resolve/list/expire/reallow")
	_, _ = layer.ExamAttempt().ReallowAttempt(ctx, &store.ExamAttemptReallow{}, nil)
	require.Equal(t, 4, stub.reallowAttempts, "ReallowAttempt() attempts without command")
}

func TestRetryReadsButDoesNotAutomaticallyRetryAuditedFocusLossMutation(t *testing.T) {
	t.Parallel()
	transientErr := errors.New("unknown focus loss outcome")
	stub := &examAttemptRetryStub{err: transientErr}
	layer, err := retrylayer.NewWithRecorder(&rootStub{examAttempt: stub}, retrylayer.Policy{
		MaxAttempts: 3, InitialBackoff: time.Nanosecond, MaxBackoff: time.Nanosecond,
		IsTransient: func(error) bool { return true },
	}, nil)
	require.NoError(t, err)
	ctx := context.Background()
	_, _ = layer.ExamAttempt().ResolveFocusLossTarget(ctx, store.ExamAttemptFocusLossAccess{})
	_, _ = layer.ExamAttempt().RecordFocusLoss(ctx, &store.ExamAttemptFocusLossSignal{})
	require.Equal(t, 3, stub.resolveFocusAttempts, "Focus Loss attempts resolve/record")
	require.Equal(t, 1, stub.recordFocusAttempts, "Focus Loss attempts resolve/record")
}

func TestRetryOnlyRetriesExamAttemptWorkspaceMutationWithDurableCommandOutcome(t *testing.T) {
	t.Parallel()

	transientErr := errors.New("unknown commit outcome")
	stub := &examAttemptWorkspaceRetryStub{err: transientErr}
	layer, err := retrylayer.NewWithRecorder(&rootStub{examAttemptWorkspace: stub}, retrylayer.Policy{
		MaxAttempts: 3, InitialBackoff: time.Nanosecond, MaxBackoff: time.Nanosecond,
		IsTransient: func(error) bool { return true },
	}, nil)
	require.NoError(t, err)

	ctx := context.Background()
	_, got := layer.ExamAttemptWorkspace().ApplyMutation(ctx, &store.ExamAttemptWorkspaceMutation{}, &store.CommandIdempotency{})
	require.Equal(t, transientErr, got)
	require.Equal(t, 3, stub.applyAttempts, "ApplyMutation() attempts with command")
	_, got = layer.ExamAttemptWorkspace().ApplyMutation(ctx, &store.ExamAttemptWorkspaceMutation{}, nil)
	require.Equal(t, transientErr, got)
	require.Equal(t, 4, stub.applyAttempts, "ApplyMutation() attempts without command")
}

func TestRetryForwardsExamAttemptWorkspaceCleanupMutationsOnce(t *testing.T) {
	t.Parallel()

	transientErr := errors.New("unknown cleanup outcome")
	stub := &examAttemptWorkspaceRetryStub{err: transientErr}
	layer, err := retrylayer.NewWithRecorder(&rootStub{examAttemptWorkspace: stub}, retrylayer.Policy{
		MaxAttempts: 3, InitialBackoff: time.Nanosecond, MaxBackoff: time.Nanosecond,
		IsTransient: func(error) bool { return true },
	}, nil)
	require.NoError(t, err)

	ctx := context.Background()
	objectID := model.NewAttemptWorkspaceObjectID()
	_ = layer.ExamAttemptWorkspace().MarkObjectReclaimable(ctx, objectID)
	_, _ = layer.ExamAttemptWorkspace().ClaimObjectsForCleanup(ctx, &store.AttemptWorkspaceCleanupClaim{Limit: 1, ClaimToken: "claim"})
	_ = layer.ExamAttemptWorkspace().CompleteObjectCleanup(ctx, &store.AttemptWorkspaceCleanupCompletion{ObjectID: objectID, ClaimToken: "claim"})
	_ = layer.ExamAttemptWorkspace().ReleaseObjectCleanup(ctx, &store.AttemptWorkspaceCleanupRelease{ObjectID: objectID, ClaimToken: "claim"})
	require.Equal(t, 1, stub.markAttempts, "cleanup attempts = mark")
	require.Equal(t, 1, stub.claimAttempts, "cleanup attempts = mark")
	require.Equal(t, 1, stub.completeAttempts, "cleanup attempts = mark")
	require.Equal(t, 1, stub.releaseAttempts, "cleanup attempts = mark")
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
	layer, err := retrylayer.NewWithRecorder(&rootStub{personalAccessToken: stub}, policy, nil)
	require.NoError(t, err)

	_, gotErr := layer.PersonalAccessToken().Resolve(context.Background(), "hash", model.TimeFromMillis(1), 2*time.Millisecond)
	require.Equal(t, transientErr, gotErr, "Resolve() error")
	require.Equal(t, 1, stub.attempts, "Resolve() attempts")
}

func TestRetryExamAuthoringIdempotentCreateAndReads(t *testing.T) {
	t.Parallel()

	transientErr := errors.New("serialization failure")
	stub := &examAuthoringStub{err: transientErr}
	layer, err := retrylayer.NewWithRecorder(&rootStub{examAuthoring: stub}, retrylayer.Policy{
		MaxAttempts: 3, InitialBackoff: time.Nanosecond, MaxBackoff: time.Nanosecond,
		IsTransient: func(err error) bool { return err == transientErr },
	}, nil)
	require.NoError(t, err)

	_, _ = layer.ExamAuthoring().Create(context.Background(), &store.ExamAuthoringCreation{}, &store.CommandIdempotency{})
	_, _ = layer.ExamAuthoring().UpdateDraftText(context.Background(), &store.ExamDraftTextUpdate{}, &store.CommandIdempotency{})
	_, _ = layer.ExamAuthoring().UpdateDraftFocusLoss(context.Background(), &store.ExamDraftFocusLossUpdate{}, &store.CommandIdempotency{})
	_, _ = layer.ExamAuthoring().List(context.Background(), store.ExamListOptions{})
	_, _ = layer.ExamAuthoring().Archive(context.Background(), &store.ExamArchive{}, &store.CommandIdempotency{})
	_, _ = layer.ExamAuthoring().Access(context.Background(), model.NewExamID(), model.NewUserID())
	_, _ = layer.ExamAuthoring().Get(context.Background(), model.NewExamID(), model.NewUserID())
	_, _ = layer.ExamAuthoring().Resolve(context.Background(), model.NewExamID())
	require.Equal(t, 3, stub.createAttempts, "attempts create/update/focus/list/archive/access/get/resolve")
	require.Equal(t, 3, stub.updateAttempts, "attempts create/update/focus/list/archive/access/get/resolve")
	require.Equal(t, 3, stub.focusAttempts, "attempts create/update/focus/list/archive/access/get/resolve")
	require.Equal(t, 3, stub.listAttempts, "attempts create/update/focus/list/archive/access/get/resolve")
	require.Equal(t, 3, stub.archiveAttempts, "attempts create/update/focus/list/archive/access/get/resolve")
	require.Equal(t, 3, stub.accessAttempts, "attempts create/update/focus/list/archive/access/get/resolve")
	require.Equal(t, 3, stub.getAttempts, "attempts create/update/focus/list/archive/access/get/resolve")
	require.Equal(t, 3, stub.resolveAttempts, "attempts create/update/focus/list/archive/access/get/resolve")
}

func TestRetryExamAuthoringDoesNotRetryCreateWithoutIdempotency(t *testing.T) {
	t.Parallel()

	stub := &examAuthoringStub{err: errors.New("serialization failure")}
	layer, err := retrylayer.NewWithRecorder(&rootStub{examAuthoring: stub}, retrylayer.Policy{
		MaxAttempts: 3, InitialBackoff: time.Nanosecond, MaxBackoff: time.Nanosecond,
		IsTransient: func(error) bool { return true },
	}, nil)
	require.NoError(t, err)
	_, _ = layer.ExamAuthoring().Create(context.Background(), &store.ExamAuthoringCreation{}, nil)
	_, _ = layer.ExamAuthoring().UpdateDraftText(context.Background(), &store.ExamDraftTextUpdate{}, nil)
	_, _ = layer.ExamAuthoring().UpdateDraftFocusLoss(context.Background(), &store.ExamDraftFocusLossUpdate{}, nil)
	_, _ = layer.ExamAuthoring().Archive(context.Background(), &store.ExamArchive{}, nil)
	require.Equal(t, 1, stub.createAttempts, "Create() attempts")
	require.Equal(t, 1, stub.updateAttempts, "UpdateDraftText() attempts")
	require.Equal(t, 1, stub.focusAttempts, "UpdateDraftFocusLoss() attempts")
	require.Equal(t, 1, stub.archiveAttempts, "Archive() attempts")
}

func TestRetryPassesUnclassifiedErrorThroughUnchanged(t *testing.T) {
	t.Parallel()

	domainErr := store.NewErrNotFound("institution", "missing")
	stub := &institutionStub{get: func() (*model.Institution, error) { return nil, domainErr }}
	layer := newLayer(t, stub, func(error) bool { return false }, 3, time.Nanosecond)

	_, gotErr := layer.Institution().Get(context.Background(), "missing")
	require.Equal(t, gotErr, domainErr, "Get() error = %v, want exact domain error %v", gotErr, domainErr)
	require.Equal(t, 1, stub.getAttempts, "Get() attempts")
}

func TestRetryReturnsExactTransientErrorAfterBoundedAttempts(t *testing.T) {
	t.Parallel()

	transientErr := errors.New("deadlock detected")
	stub := &institutionStub{get: func() (*model.Institution, error) { return nil, transientErr }}
	layer := newLayer(t, stub, func(err error) bool { return err == transientErr }, 3, time.Nanosecond)

	_, gotErr := layer.Institution().Get(context.Background(), model.NewInstitutionID())
	require.Equal(t, transientErr, gotErr, "Get() error")
	require.Equal(t, 3, stub.getAttempts, "Get() attempts")
}

func TestRetryCancellationStopsBeforeAnotherAttempt(t *testing.T) {
	t.Parallel()

	transientErr := errors.New("serialization failure")
	stub := &institutionStub{get: func() (*model.Institution, error) { return nil, transientErr }}
	layer := newLayer(t, stub, func(error) bool { return true }, 3, time.Hour)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	startedAt := time.Now()
	_, gotErr := layer.Institution().Get(ctx, model.NewInstitutionID())
	require.Equal(t, context.Canceled, gotErr, "Get() error")
	require.LessOrEqual(t, time.Since(startedAt), time.Second)
	require.Equal(t, 1, stub.getAttempts, "Get() attempts")
}

func TestRetryDeadlineStopsBeforeAnotherAttempt(t *testing.T) {
	t.Parallel()

	transientErr := errors.New("serialization failure")
	stub := &institutionStub{get: func() (*model.Institution, error) { return nil, transientErr }}
	layer := newLayer(t, stub, func(error) bool { return true }, 3, time.Hour)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()

	startedAt := time.Now()
	_, gotErr := layer.Institution().Get(ctx, model.NewInstitutionID())
	require.Equal(t, context.DeadlineExceeded, gotErr, "Get() error")
	require.LessOrEqual(t, time.Since(startedAt), time.Second)
	require.Equal(t, 1, stub.getAttempts, "Get() attempts")
}

func TestRetryPreservesStableAndMissingAccessors(t *testing.T) {
	t.Parallel()

	stub := &institutionStub{get: func() (*model.Institution, error) { return nil, nil }}
	layer := newLayer(t, stub, func(error) bool { return false }, 3, time.Nanosecond)
	institution := layer.Institution()
	require.Same(t, institution, layer.Institution(), "Institution() returned different wrappers")
	require.Nil(t, layer.AcademicUnit(), "AcademicUnit() wrapped a nil underlying store")
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
			_, err := retrylayer.NewWithRecorder(&rootStub{}, test.policy, nil)
			require.Error(t, err, "New() accepted invalid policy")
		})
	}
	_, err := retrylayer.NewWithRecorder(nil, valid, nil)
	require.Error(t, err, "New() accepted nil store")
}

func TestRetryGeneratedForwardingIsCurrent(t *testing.T) {
	t.Parallel()

	temporary := t.TempDir()
	generatedPath := filepath.Join(temporary, "forwarding_gen.go")
	command := exec.Command(
		"go", "run", "../storetest/layergen", "-layer", "retry", "-source", "..", "-output", generatedPath,
	)
	command.Env = append(os.Environ(), "GOCACHE="+filepath.Join(temporary, "go-cache"))
	output, err := command.CombinedOutput()
	require.NoError(t, err, "regenerate forwarding: %v\n%s", err, output)
	want, err := os.ReadFile("forwarding_gen.go")
	require.NoError(t, err)
	got, err := os.ReadFile(generatedPath)
	require.NoError(t, err)
	require.True(t, bytes.Equal(got, want), "forwarding_gen.go is stale; run go generate ./store/retrylayer")
}

func TestRetryRecorderReportsAttemptsAndExhaustion(t *testing.T) {
	t.Parallel()

	transient := errors.New("transient")
	institution := &institutionStub{get: func() (*model.Institution, error) { return nil, transient }}
	recorder := &retryRecorder{}
	layer, err := retrylayer.NewWithRecorder(&rootStub{institution: institution}, retrylayer.Policy{
		MaxAttempts: 3, InitialBackoff: time.Nanosecond, MaxBackoff: time.Nanosecond,
		IsTransient: func(err error) bool { return errors.Is(err, transient) },
	}, recorder)
	require.NoError(t, err)
	_, err = layer.Institution().Get(context.Background(), "institution")
	require.ErrorIs(t, err, transient)
	want := []string{"retry", "retry", "exhausted"}
	require.Len(t, recorder.outcomes, len(want), "outcomes")
	for index := range want {
		require.Equal(t, want[index], recorder.outcomes[index], "outcomes = %#v, want %#v", recorder.outcomes, want)
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
	layer, err := retrylayer.NewWithRecorder(&rootStub{institution: institution}, retrylayer.Policy{
		MaxAttempts:    attempts,
		InitialBackoff: backoff,
		MaxBackoff:     backoff,
		IsTransient:    classifier,
	}, nil)
	require.NoError(t, err)
	return layer
}

func (s *examAuthoringStub) UpdateDraftNativePolicy(context.Context, *store.ExamDraftNativePolicyUpdate, *store.CommandIdempotency) (*store.ExamAuthoringCommandResult, error) {
	s.focusAttempts++
	return nil, s.err
}

type servingNodeLeaseRetryStub struct {
	store.ServingNodeLeaseStore
	upsertAttempts int
	deleteAttempts int
	err            error
	failures       int
}

func (s *servingNodeLeaseRetryStub) Upsert(_ context.Context, claim *store.ServingNodeLeaseClaim) (*store.ServingNodeLease, error) {
	s.upsertAttempts++
	if s.upsertAttempts <= s.failures {
		return nil, s.err
	}
	return &store.ServingNodeLease{NodeID: claim.NodeID, LeaseID: claim.LeaseID}, nil
}

func (s *servingNodeLeaseRetryStub) Delete(ctx context.Context, input *store.ServingNodeLeaseDeletion) error {
	s.deleteAttempts++
	if s.deleteAttempts <= s.failures {
		return s.err
	}
	return nil
}

type servingNodeLeaseRootStub struct {
	store.Store
	lease store.ServingNodeLeaseStore
}

func (s *servingNodeLeaseRootStub) ServingNodeLease() store.ServingNodeLeaseStore { return s.lease }

func TestRetryServingNodeLeaseIncarnationWritesAreIdempotent(t *testing.T) {
	t.Parallel()

	transientErr := errors.New("serialization failure")
	stub := &servingNodeLeaseRetryStub{err: transientErr, failures: 2}
	layer, err := retrylayer.NewWithRecorder(&servingNodeLeaseRootStub{lease: stub}, retrylayer.Policy{
		MaxAttempts:    3,
		InitialBackoff: time.Nanosecond,
		MaxBackoff:     time.Nanosecond,
		IsTransient:    func(err error) bool { return errors.Is(err, transientErr) },
	}, nil)
	require.NoError(t, err)
	leaseID := model.NewID()
	lease, err := layer.ServingNodeLease().Upsert(context.Background(), &store.ServingNodeLeaseClaim{NodeID: "node-a", LeaseID: leaseID, Lifetime: 30 * time.Second})
	require.NoError(t, err, "Upsert()")
	require.NotNil(t, lease, "Upsert()")
	require.Equal(t, leaseID, lease.LeaseID, "Upsert()")
	require.Equal(t, 3, stub.upsertAttempts, "Upsert()")
	require.NoError(t, layer.ServingNodeLease().Delete(context.Background(), &store.ServingNodeLeaseDeletion{NodeID: "node-a", LeaseID: leaseID}), "Delete()")
	require.Equal(t, 3, stub.deleteAttempts, "Delete()")
}
