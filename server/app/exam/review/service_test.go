// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package review

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/server/app/apperror"
	"github.com/sudosylabs/proctor/server/app/invocation"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

func TestSaveDecisionAuthorizesAuditsAtomicallyAndSuppressesPrivateRationale(t *testing.T) {
	t.Parallel()
	f := newReviewFixture(t)
	result, err := f.service.SaveExamIntegrityDecision(context.Background(), f.invocation, SaveDecisionCommand{
		SubmissionID: f.submissionID, FlagID: f.flagID, ExpectedReviewRevision: 0,
		ExpectedDecisionRevision: 0, Outcome: model.IntegrityReviewConfirmed,
		PrivateRationale: "The retained continuity evidence was verified.", IdempotencyKey: "test-key",
	})
	require.NoError(t, err)
	require.NotNil(t, result.Review, "result=%#v mutation=%#v effects", result, f.persistence.decision)
	require.NotNil(t, result.Decision, "result=%#v mutation=%#v effects", result, f.persistence.decision)
	require.NotNil(t, f.persistence.decision, "result=%#v mutation=%#v effects", result, f.persistence.decision)
	require.Equal(t, 1, f.effects.changed, "result=%#v mutation=%#v effects", result, f.persistence.decision)
	require.Equal(t, f.submissionID, f.authorizer.reviewSubmission, "authorization=%#v mutation=%#v", f.authorizer, f.persistence.decision)
	require.False(t, f.persistence.decision.ManagerOverride, "authorization=%#v mutation=%#v", f.authorizer, f.persistence.decision)
	require.Equal(t, f.userID, f.persistence.decision.ActorUserID, "authorization=%#v mutation=%#v", f.authorizer, f.persistence.decision)
	require.NotEmpty(t, f.persistence.decision.AuditEventID, "authorization=%#v mutation=%#v", f.authorizer, f.persistence.decision)
	for _, forbidden := range []string{"private_rationale", "manager_notes", "student_remarks_markdown"} {
		_, exists := f.auditor.value[forbidden]
		require.False(t, exists, "private field %q in audit %#v", forbidden, f.auditor.value)
	}
	wantIdempotency, prepareErr := prepareDecisionIdempotency(f.invocation, SaveDecisionCommand{
		SubmissionID: f.submissionID, FlagID: f.flagID, ExpectedReviewRevision: 0,
		ExpectedDecisionRevision: 0, Outcome: model.IntegrityReviewConfirmed,
		PrivateRationale: "The retained continuity evidence was verified.", IdempotencyKey: "test-key",
	})
	require.NoError(t, prepareErr)
	require.Equal(t, wantIdempotency, f.persistence.idempotency)
}

func TestUpdateDraftPassesOwnedIdempotencyToStore(t *testing.T) {
	t.Parallel()
	f := newReviewFixture(t)
	command := UpdateDraftCommand{SubmissionID: f.submissionID, ReviewID: f.reviewID, ExpectedReviewRevision: 1,
		ManagerNotes: "private", StudentRemarksMarkdown: "Visible", IdempotencyKey: "draft-key"}
	_, err := f.service.UpdateExamIntegrityReview(context.Background(), f.invocation, command)
	require.NoError(t, err)
	want, err := prepareDraftIdempotency(f.invocation, command)
	require.NoError(t, err)
	require.Equal(t, want, f.persistence.idempotency)
}

func TestExactDecisionReplayReturnsRetainedResultWithoutEffect(t *testing.T) {
	t.Parallel()
	f := newReviewFixture(t)
	f.persistence.replayed = true
	result, err := f.service.SaveExamIntegrityDecision(context.Background(), f.invocation, SaveDecisionCommand{
		SubmissionID: f.submissionID, FlagID: f.flagID, Outcome: model.IntegrityReviewConfirmed,
		PrivateRationale: "The retained continuity evidence was verified.", IdempotencyKey: "test-key",
	})
	require.NoError(t, err, "result=%#v error=%v effects", result, err)
	require.True(t, result.Replayed, "result=%#v error=%v effects", result, err)
	require.Equal(t, 0, f.effects.changed, "result=%#v error=%v effects", result, err)
}

func TestFinalizeAndReleaseUseDistinctAuthorizationAndEffects(t *testing.T) {
	t.Parallel()
	f := newReviewFixture(t)
	f.persistence.mode = "finalize"
	finalized, err := f.service.FinalizeExamIntegrityReview(context.Background(), f.invocation, FinalizeCommand{SubmissionID: f.submissionID,
		ReviewID: f.reviewID, ExpectedReviewRevision: 2, IdempotencyKey: "test-key"})
	require.NoError(t, err, "finalized=%#v error=%v auth=%#v effects=%#v", finalized, err, f.authorizer, f.effects)
	require.Equal(t, model.SubmissionReviewFinalized, finalized.Review.State, "finalized=%#v error=%v auth=%#v effects=%#v", finalized, err, f.authorizer, f.effects)
	require.Equal(t, 1, f.authorizer.reviewCalls, "finalized=%#v error=%v auth=%#v effects=%#v", finalized, err, f.authorizer, f.effects)
	require.Equal(t, 1, f.effects.finalized, "finalized=%#v error=%v auth=%#v effects=%#v", finalized, err, f.authorizer, f.effects)
	wantFinalize, prepareErr := prepareTerminalIdempotency(f.invocation, store.ExamIntegrityReviewFinalizeOperation,
		"test-key", f.submissionID, f.reviewID, 2)
	require.NoError(t, prepareErr)
	require.Equal(t, wantFinalize, f.persistence.idempotency)
	f.persistence.mode = "release"
	released, err := f.service.ReleaseStudentExamResult(context.Background(), f.invocation, ReleaseCommand{SubmissionID: f.submissionID,
		ReviewID: f.reviewID, ExpectedReviewRevision: 3, IdempotencyKey: "test-key"})
	require.NoError(t, err, "released=%#v error=%v auth=%#v effects=%#v", released, err, f.authorizer, f.effects)
	require.Equal(t, model.SubmissionReviewReleased, released.Review.ReleaseState, "released=%#v error=%v auth=%#v effects=%#v", released, err, f.authorizer, f.effects)
	require.Equal(t, 1, f.authorizer.releaseCalls, "released=%#v error=%v auth=%#v effects=%#v", released, err, f.authorizer, f.effects)
	require.Equal(t, 1, f.effects.released, "released=%#v error=%v auth=%#v effects=%#v", released, err, f.authorizer, f.effects)
	wantRelease, prepareErr := prepareTerminalIdempotency(f.invocation, store.ExamIntegrityReviewReleaseOperation,
		"test-key", f.submissionID, f.reviewID, 3)
	require.NoError(t, prepareErr)
	require.Equal(t, wantRelease, f.persistence.idempotency)
	require.Equal(t, f.userID, f.mail.request.CandidateUserID, "mail=%#v release", f.mail.request)
	require.Equal(t, f.reviewID, f.mail.request.ReviewID, "mail=%#v release", f.mail.request)
	require.Equal(t, int64(3), f.mail.request.ExpectedReviewRevision, "mail=%#v release", f.mail.request)
	require.True(t, f.mail.request.ReleasedAt.Equal(f.at.Add(time.Minute)), "mail=%#v release", f.mail.request)
	require.NotNil(t, f.persistence.release, "mail=%#v release", f.mail.request)
	require.NotNil(t, f.persistence.release.Notice, "mail=%#v release", f.mail.request)
	require.Equal(t, int64(2), f.persistence.release.ExpectedRecipientRevision, "mail=%#v release", f.mail.request)
}

func TestReleasePreparationFailureIsAuditedAsAttempt(t *testing.T) {
	t.Parallel()
	f := newReviewFixture(t)
	f.persistence.mode = "release"
	f.persistence.prepareErr = store.NewErrConflict("submission_review", "integrity_review_state", nil)
	_, err := f.service.ReleaseStudentExamResult(context.Background(), f.invocation, ReleaseCommand{SubmissionID: f.submissionID,
		ReviewID: f.reviewID, ExpectedReviewRevision: 3, IdempotencyKey: "test-key"})
	var fault *apperror.Error
	require.ErrorAs(t, err, &fault)
	require.Equal(t, "exam.integrity_review.state_conflict", fault.Code())
	// The release-time reservation is a durable write, so its attempt must be
	// audited before it runs and completed as failed when it is refused.
	require.Equal(t, 1, f.auditor.begun, "audit=%#v mail=%d release", f.auditor, f.mail.calls)
	require.Equal(t, fault.Code(), f.auditor.failedCode, "audit=%#v mail=%d release", f.auditor, f.mail.calls)
	require.Equal(t, 0, f.mail.calls, "audit=%#v mail=%d release", f.auditor, f.mail.calls)
	require.Nil(t, f.persistence.release, "audit=%#v mail=%d release", f.auditor, f.mail.calls)
}

func TestReleaseReplayReturnsRetainedResultWithoutPreparingMail(t *testing.T) {
	t.Parallel()
	f := newReviewFixture(t)
	f.persistence.mode, f.persistence.replayed = "release", true
	released, err := f.service.ReleaseStudentExamResult(context.Background(), f.invocation, ReleaseCommand{SubmissionID: f.submissionID,
		ReviewID: f.reviewID, ExpectedReviewRevision: 3, IdempotencyKey: "test-key"})
	require.NoError(t, err, "released=%#v error=%v effects=%#v mail=%#v release", released, err, f.effects, f.mail)
	require.True(t, released.Replayed, "released=%#v error=%v effects=%#v mail=%#v release", released, err, f.effects, f.mail)
	require.Equal(t, 0, f.effects.released, "released=%#v error=%v effects=%#v mail=%#v release", released, err, f.effects, f.mail)
	require.Equal(t, 0, f.mail.calls, "released=%#v error=%v effects=%#v mail=%#v release", released, err, f.effects, f.mail)
	require.NotNil(t, f.persistence.release, "released=%#v error=%v effects=%#v mail=%#v release", released, err, f.effects, f.mail)
	require.Nil(t, f.persistence.release.Notice, "released=%#v error=%v effects=%#v mail=%#v release", released, err, f.effects, f.mail)
}

func TestCandidateResultRequiresSessionOwnershipAndSanitizesRemarks(t *testing.T) {
	t.Parallel()
	f := newReviewFixture(t)
	f.persistence.student = &model.StudentResult{ReviewID: f.reviewID, SubmissionID: f.submissionID,
		AttemptID: f.attemptID, CandidateUserID: f.userID,
		StudentRemarksMarkdown: "Safe **text** <script>alert(1)</script> ![remote](https://tracker.example/x)", ReleasedAt: f.at}
	result, err := f.service.GetStudentExamResult(context.Background(), f.invocation, f.attemptID)
	require.NoError(t, err)
	require.Equal(t, "Safe **text**  remote", result.StudentRemarksMarkdown)
	wrongPrincipal := f.invocation.Principal()
	wrongPrincipal.UserID = model.NewUserID()
	wrong := invocation.New(wrongPrincipal, f.invocation.RequestMetadata())
	_, err = f.service.GetStudentExamResult(context.Background(), wrong, f.attemptID)
	require.Error(t, err, "GetStudentResult() accepted another candidate")
	patPrincipal := f.invocation.Principal()
	patPrincipal.CredentialType = model.CredentialPersonalAccessToken
	patPrincipal.SessionID = ""
	pat := invocation.New(patPrincipal, f.invocation.RequestMetadata())
	_, err = f.service.GetStudentExamResult(context.Background(), pat, f.attemptID)
	require.Error(t, err, "GetStudentResult() accepted non-Session principal")
}

type reviewFixture struct {
	service      *Service
	invocation   invocation.Invocation
	persistence  *reviewStoreFake
	authorizer   *reviewAuthorizerFake
	auditor      *reviewAuditorFake
	effects      *reviewEffectsFake
	mail         *reviewMailFake
	userID       model.UserID
	submissionID model.SubmissionID
	reviewID     model.SubmissionReviewID
	flagID       model.IntegrityFlagID
	attemptID    model.ExamAttemptID
	at           time.Time
}

func newReviewFixture(t *testing.T) *reviewFixture {
	t.Helper()
	f := &reviewFixture{userID: model.NewUserID(), submissionID: model.NewSubmissionID(), reviewID: model.NewSubmissionReviewID(),
		flagID: model.NewIntegrityFlagID(), attemptID: model.NewExamAttemptID(), at: time.Date(2026, 8, 15, 12, 0, 0, 0, time.UTC)}
	f.persistence = &reviewStoreFake{f: f}
	f.authorizer = &reviewAuthorizerFake{}
	f.auditor = &reviewAuditorFake{}
	f.effects = &reviewEffectsFake{}
	f.mail = &reviewMailFake{f: f}
	service, err := New(Dependencies{Persistence: f.persistence, Authorizer: f.authorizer, Auditor: f.auditor,
		Effects: f.effects, EffectFailures: f.effects, Mail: f.mail, Now: func() time.Time { return f.at },
		NewReviewID: model.NewSubmissionReviewID, NewDecisionID: model.NewIntegrityReviewDecisionID})
	require.NoError(t, err)
	f.service = service
	f.invocation = invocation.New(model.Principal{UserID: f.userID, SessionID: model.NewSessionID(), CredentialID: model.PrincipalCredentialID(model.NewID()),
		CredentialType: model.CredentialSessionAccess, AuthenticationMethod: "password", AuthenticationStrength: model.AuthenticationSingleFactor,
		ClientType: model.SessionClientWeb, AuthenticatedAt: f.at}, model.RequestMetadata{})
	return f
}

type reviewStoreFake struct {
	f           *reviewFixture
	decision    *store.ExamIntegrityReviewDecisionMutation
	replayed    bool
	mode        string
	student     *model.StudentResult
	release     *store.ExamIntegrityReviewRelease
	draft       *store.ExamIntegrityReviewDraftMutation
	idempotency *store.CommandIdempotency
	prepareErr  error
}

func (f *reviewStoreFake) PrepareRelease(ctx context.Context, input *store.ExamIntegrityReviewReleasePrepare) (*store.ExamIntegrityReviewReleasePreparation, error) {
	if f.prepareErr != nil {
		return nil, f.prepareErr
	}
	return &store.ExamIntegrityReviewReleasePreparation{Replayed: f.replayed, ReleaseAt: f.f.at.Add(time.Minute)}, nil
}

func (f *reviewStoreFake) Resolve(context.Context, model.SubmissionID) (*store.ExamIntegrityReviewAuthorization, error) {
	return &store.ExamIntegrityReviewAuthorization{SubmissionID: f.f.submissionID, ExamID: model.NewExamID(), SittingID: model.NewExamSittingID(), AttemptID: f.f.attemptID, CandidateUserID: f.f.userID, AcademicUnitID: model.NewAcademicUnitID()}, nil
}
func (f *reviewStoreFake) Get(context.Context, model.SubmissionID) (*store.ExamSubmissionReviewSnapshot, error) {
	return nil, errors.New("unused")
}
func (f *reviewStoreFake) ListFlags(context.Context, store.ExamIntegrityFlagListOptions) (*store.ExamIntegrityFlagPage, error) {
	return nil, errors.New("unused")
}
func (f *reviewStoreFake) ListEvidence(context.Context, store.ExamIntegrityEvidenceListOptions) (*store.ExamIntegrityEvidencePage, error) {
	return nil, errors.New("unused")
}
func (f *reviewStoreFake) ListDiscrepancies(context.Context, store.ExamIntegrityDiscrepancyListOptions) (*store.ExamIntegrityDiscrepancyPage, error) {
	return nil, errors.New("unused")
}
func (f *reviewStoreFake) SaveDecision(_ context.Context, input *store.ExamIntegrityReviewDecisionMutation, command *store.CommandIdempotency) (*store.ExamIntegrityReviewMutationResult, error) {
	f.decision, f.idempotency = input, command
	review, _ := model.NewSubmissionReview(input.ReviewID, f.f.submissionID, f.f.userID, f.f.at)
	decision, _ := model.NewIntegrityReviewDecision(input.DecisionID, review.ID, f.f.flagID,
		model.IntegrityReviewConfirmed, f.f.userID, "The retained continuity evidence was verified.", f.f.at)
	return &store.ExamIntegrityReviewMutationResult{Authorization: *mustReviewAuthorization(f), Review: review, Decision: decision, Replayed: f.replayed}, nil
}
func (f *reviewStoreFake) UpdateDraft(_ context.Context, input *store.ExamIntegrityReviewDraftMutation, command *store.CommandIdempotency) (*store.ExamIntegrityReviewMutationResult, error) {
	f.draft, f.idempotency = input, command
	review, _ := model.NewSubmissionReview(f.f.reviewID, f.f.submissionID, f.f.userID, f.f.at.Add(-time.Minute))
	_ = review.UpdateDraft(input.ExpectedRevision, input.ManagerNotes, input.StudentRemarksMarkdown, f.f.at)
	return &store.ExamIntegrityReviewMutationResult{Authorization: *mustReviewAuthorization(f), Review: review}, nil
}
func (f *reviewStoreFake) Finalize(_ context.Context, _ *store.ExamIntegrityReviewFinalize, command *store.CommandIdempotency) (*store.ExamIntegrityReviewMutationResult, error) {
	if command != nil {
		f.idempotency = command
	}
	review, _ := model.NewSubmissionReview(f.f.reviewID, f.f.submissionID, f.f.userID, f.f.at.Add(-time.Minute))
	_ = review.UpdateDraft(1, "", "", f.f.at.Add(-30*time.Second))
	_ = review.Finalize(2, f.f.userID, 0, 0, 0, "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", f.f.at)
	return &store.ExamIntegrityReviewMutationResult{Authorization: *mustReviewAuthorization(f), Review: review}, nil
}
func (f *reviewStoreFake) Release(_ context.Context, input *store.ExamIntegrityReviewRelease, command *store.CommandIdempotency) (*store.ExamIntegrityReviewMutationResult, error) {
	f.release, f.idempotency = input, command
	result, _ := f.Finalize(context.Background(), nil, nil)
	_ = result.Review.Release(3, f.f.userID, f.f.at.Add(time.Minute))
	result.Replayed = f.replayed
	return result, nil
}
func (f *reviewStoreFake) GetReleasedStudentResult(_ context.Context, attemptID model.ExamAttemptID, candidateID model.UserID) (*model.StudentResult, error) {
	if f.student == nil || f.student.AttemptID != attemptID || f.student.CandidateUserID != candidateID {
		return nil, store.NewErrNotFound("student_result", attemptID.String())
	}
	clone := *f.student
	return &clone, nil
}

func mustReviewAuthorization(fake *reviewStoreFake) *store.ExamIntegrityReviewAuthorization {
	authorization, _ := fake.Resolve(context.Background(), fake.f.submissionID)
	return authorization
}

type reviewAuthorizerFake struct {
	reviewSubmission model.SubmissionID
	reviewCalls      int
	releaseCalls     int
}

func (*reviewAuthorizerFake) AuthorizeSubmissionView(context.Context, invocation.Invocation, model.SubmissionID) error {
	return nil
}
func (f *reviewAuthorizerFake) AuthorizeSubmissionReview(_ context.Context, _ invocation.Invocation, id model.SubmissionID) (ManagerAuthorization, error) {
	f.reviewSubmission, f.reviewCalls = id, f.reviewCalls+1
	return ManagerAuthorization{}, nil
}
func (f *reviewAuthorizerFake) AuthorizeSubmissionRelease(_ context.Context, _ invocation.Invocation, _ model.SubmissionID) (ManagerAuthorization, error) {
	f.releaseCalls++
	return ManagerAuthorization{}, nil
}

type reviewAuditorFake struct {
	value      map[string]any
	begun      int
	failedCode string
}

func (f *reviewAuditorFake) BeginAtScope(_ context.Context, _ invocation.Invocation, _ model.Action, _ model.Resource, _ model.RoleScopeType, _, _ string, value, _ map[string]any) (model.AuditEventID, error) {
	f.value = value
	f.begun++
	return model.NewAuditEventID(), nil
}
func (f *reviewAuditorFake) Fail(_ context.Context, _ model.AuditEventID, code string) error {
	f.failedCode = code
	return nil
}

type reviewEffectsFake struct{ changed, finalized, released int }

type reviewMailFake struct {
	f       *reviewFixture
	request ResultReleaseMailPreparation
	calls   int
}

func (f *reviewMailFake) PrepareResultRelease(_ context.Context,
	request ResultReleaseMailPreparation,
) (*PreparedResultReleaseMail, error) {
	f.calls++
	f.request = request
	return &PreparedResultReleaseMail{Notice: &store.PreparedMail{Occurrence: &model.MailOccurrence{},
		Delivery: &model.MailDelivery{}, Job: &model.Job{}}, ExpectedRecipientRevision: 2}, nil
}

func (f *reviewEffectsFake) ReviewChanged(context.Context, Result) error {
	f.changed++
	return nil
}
func (f *reviewEffectsFake) ReviewFinalized(context.Context, Result) error {
	f.finalized++
	return nil
}
func (f *reviewEffectsFake) ResultReleased(context.Context, Result) error {
	f.released++
	return nil
}
func (*reviewEffectsFake) Report(context.Context, string, error) {}

func (f *reviewStoreFake) ListNativeConditions(context.Context, store.NativeConditionListOptions) (*store.NativeConditionPage, error) {
	return &store.NativeConditionPage{Items: []model.NativeConditionEvidence{}}, nil
}
