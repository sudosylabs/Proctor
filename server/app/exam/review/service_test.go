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
	if err != nil {
		t.Fatalf("SaveDecision() error = %v", err)
	}
	if result.Review == nil || result.Decision == nil || f.persistence.decision == nil || f.effects.changed != 1 {
		t.Fatalf("result=%#v mutation=%#v effects=%d", result, f.persistence.decision, f.effects.changed)
	}
	if f.authorizer.reviewSubmission != f.submissionID || f.persistence.decision.ManagerOverride ||
		f.persistence.decision.ActorUserID != f.userID || f.persistence.decision.AuditEventID == "" {
		t.Fatalf("authorization=%#v mutation=%#v", f.authorizer, f.persistence.decision)
	}
	for _, forbidden := range []string{"private_rationale", "manager_notes", "student_remarks_markdown"} {
		if _, exists := f.auditor.value[forbidden]; exists {
			t.Fatalf("private field %q in audit %#v", forbidden, f.auditor.value)
		}
	}
	wantIdempotency, prepareErr := prepareDecisionIdempotency(f.invocation, SaveDecisionCommand{
		SubmissionID: f.submissionID, FlagID: f.flagID, ExpectedReviewRevision: 0,
		ExpectedDecisionRevision: 0, Outcome: model.IntegrityReviewConfirmed,
		PrivateRationale: "The retained continuity evidence was verified.", IdempotencyKey: "test-key",
	})
	if prepareErr != nil {
		t.Fatal(prepareErr)
	}
	assertStoreBoundaryCommand(t, f.persistence.idempotency, wantIdempotency)
}

func TestUpdateDraftPassesOwnedIdempotencyToStore(t *testing.T) {
	t.Parallel()
	f := newReviewFixture(t)
	command := UpdateDraftCommand{SubmissionID: f.submissionID, ReviewID: f.reviewID, ExpectedReviewRevision: 1,
		ManagerNotes: "private", StudentRemarksMarkdown: "Visible", IdempotencyKey: "draft-key"}
	if _, err := f.service.UpdateExamIntegrityReview(context.Background(), f.invocation, command); err != nil {
		t.Fatal(err)
	}
	want, err := prepareDraftIdempotency(f.invocation, command)
	if err != nil {
		t.Fatal(err)
	}
	assertStoreBoundaryCommand(t, f.persistence.idempotency, want)
}

func TestExactDecisionReplayReturnsRetainedResultWithoutEffect(t *testing.T) {
	t.Parallel()
	f := newReviewFixture(t)
	f.persistence.replayed = true
	result, err := f.service.SaveExamIntegrityDecision(context.Background(), f.invocation, SaveDecisionCommand{
		SubmissionID: f.submissionID, FlagID: f.flagID, Outcome: model.IntegrityReviewConfirmed,
		PrivateRationale: "The retained continuity evidence was verified.", IdempotencyKey: "test-key",
	})
	if err != nil || !result.Replayed || f.effects.changed != 0 {
		t.Fatalf("result=%#v error=%v effects=%d", result, err, f.effects.changed)
	}
}

func TestFinalizeAndReleaseUseDistinctAuthorizationAndEffects(t *testing.T) {
	t.Parallel()
	f := newReviewFixture(t)
	f.persistence.mode = "finalize"
	finalized, err := f.service.FinalizeExamIntegrityReview(context.Background(), f.invocation, FinalizeCommand{SubmissionID: f.submissionID,
		ReviewID: f.reviewID, ExpectedReviewRevision: 2, IdempotencyKey: "test-key"})
	if err != nil || finalized.Review.State != model.SubmissionReviewFinalized || f.authorizer.reviewCalls != 1 || f.effects.finalized != 1 {
		t.Fatalf("finalized=%#v error=%v auth=%#v effects=%#v", finalized, err, f.authorizer, f.effects)
	}
	wantFinalize, prepareErr := prepareTerminalIdempotency(f.invocation, store.ExamIntegrityReviewFinalizeOperation,
		"test-key", f.submissionID, f.reviewID, 2)
	if prepareErr != nil {
		t.Fatal(prepareErr)
	}
	assertStoreBoundaryCommand(t, f.persistence.idempotency, wantFinalize)
	f.persistence.mode = "release"
	released, err := f.service.ReleaseStudentExamResult(context.Background(), f.invocation, ReleaseCommand{SubmissionID: f.submissionID,
		ReviewID: f.reviewID, ExpectedReviewRevision: 3, IdempotencyKey: "test-key"})
	if err != nil || released.Review.ReleaseState != model.SubmissionReviewReleased || f.authorizer.releaseCalls != 1 || f.effects.released != 1 {
		t.Fatalf("released=%#v error=%v auth=%#v effects=%#v", released, err, f.authorizer, f.effects)
	}
	wantRelease, prepareErr := prepareTerminalIdempotency(f.invocation, store.ExamIntegrityReviewReleaseOperation,
		"test-key", f.submissionID, f.reviewID, 3)
	if prepareErr != nil {
		t.Fatal(prepareErr)
	}
	assertStoreBoundaryCommand(t, f.persistence.idempotency, wantRelease)
	if f.mail.request.CandidateUserID != f.userID || f.mail.request.ReviewID != f.reviewID || f.mail.request.ExpectedReviewRevision != 3 ||
		!f.mail.request.ReleasedAt.Equal(f.at.Add(time.Minute)) || f.persistence.release == nil ||
		f.persistence.release.Notice == nil || f.persistence.release.ExpectedRecipientRevision != 2 {
		t.Fatalf("mail=%#v release=%#v", f.mail.request, f.persistence.release)
	}
}

func TestReleasePreparationFailureIsAuditedAsAttempt(t *testing.T) {
	t.Parallel()
	f := newReviewFixture(t)
	f.persistence.mode = "release"
	f.persistence.prepareErr = store.NewErrConflict("submission_review", "integrity_review_state", nil)
	_, err := f.service.ReleaseStudentExamResult(context.Background(), f.invocation, ReleaseCommand{SubmissionID: f.submissionID,
		ReviewID: f.reviewID, ExpectedReviewRevision: 3, IdempotencyKey: "test-key"})
	var fault *apperror.Error
	if !errors.As(err, &fault) || fault.Code() != "exam.integrity_review.state_conflict" {
		t.Fatalf("Release() error = %v", err)
	}
	// The release-time reservation is a durable write, so its attempt must be
	// audited before it runs and completed as failed when it is refused.
	if f.auditor.begun != 1 || f.auditor.failedCode != fault.Code() || f.mail.calls != 0 || f.persistence.release != nil {
		t.Fatalf("audit=%#v mail=%d release=%#v", f.auditor, f.mail.calls, f.persistence.release)
	}
}

func TestReleaseReplayReturnsRetainedResultWithoutPreparingMail(t *testing.T) {
	t.Parallel()
	f := newReviewFixture(t)
	f.persistence.mode, f.persistence.replayed = "release", true
	released, err := f.service.ReleaseStudentExamResult(context.Background(), f.invocation, ReleaseCommand{SubmissionID: f.submissionID,
		ReviewID: f.reviewID, ExpectedReviewRevision: 3, IdempotencyKey: "test-key"})
	if err != nil || !released.Replayed || f.effects.released != 0 || f.mail.calls != 0 ||
		f.persistence.release == nil || f.persistence.release.Notice != nil {
		t.Fatalf("released=%#v error=%v effects=%#v mail=%#v release=%#v", released, err, f.effects,
			f.mail, f.persistence.release)
	}
}

func TestCandidateResultRequiresSessionOwnershipAndSanitizesRemarks(t *testing.T) {
	t.Parallel()
	f := newReviewFixture(t)
	f.persistence.student = &model.StudentResult{ReviewID: f.reviewID, SubmissionID: f.submissionID,
		AttemptID: f.attemptID, CandidateUserID: f.userID,
		StudentRemarksMarkdown: "Safe **text** <script>alert(1)</script> ![remote](https://tracker.example/x)", ReleasedAt: f.at}
	result, err := f.service.GetStudentExamResult(context.Background(), f.invocation, f.attemptID)
	if err != nil {
		t.Fatalf("GetStudentResult() error = %v", err)
	}
	if result.StudentRemarksMarkdown != "Safe **text**  remote" {
		t.Fatalf("sanitized remarks = %q", result.StudentRemarksMarkdown)
	}
	wrongPrincipal := f.invocation.Principal()
	wrongPrincipal.UserID = model.NewUserID()
	wrong := invocation.New(wrongPrincipal, f.invocation.RequestMetadata())
	if _, err = f.service.GetStudentExamResult(context.Background(), wrong, f.attemptID); err == nil {
		t.Fatal("GetStudentResult() accepted another candidate")
	}
	patPrincipal := f.invocation.Principal()
	patPrincipal.CredentialType = model.CredentialPersonalAccessToken
	patPrincipal.SessionID = ""
	pat := invocation.New(patPrincipal, f.invocation.RequestMetadata())
	if _, err = f.service.GetStudentExamResult(context.Background(), pat, f.attemptID); err == nil {
		t.Fatal("GetStudentResult() accepted non-Session principal")
	}
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
	if err != nil {
		t.Fatal(err)
	}
	f.service = service
	f.invocation = invocation.New(model.Principal{UserID: f.userID, SessionID: model.NewSessionID(), CredentialID: model.PrincipalCredentialID(model.NewId()),
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

func (f *reviewStoreFake) PrepareRelease(context.Context, model.SubmissionID, model.SubmissionReviewID,
	int64,
) (*store.ExamIntegrityReviewReleasePreparation, error) {
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
	_ = review.UpdateDraft(input.ExpectedReviewRevision, input.ManagerNotes, input.StudentRemarksMarkdown, f.f.at)
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

func (*reviewAuthorizerFake) AuthorizeView(context.Context, invocation.Invocation, model.SubmissionID) error {
	return nil
}
func (f *reviewAuthorizerFake) AuthorizeReview(_ context.Context, _ invocation.Invocation, id model.SubmissionID) (bool, error) {
	f.reviewSubmission, f.reviewCalls = id, f.reviewCalls+1
	return false, nil
}
func (f *reviewAuthorizerFake) AuthorizeRelease(_ context.Context, _ invocation.Invocation, _ model.SubmissionID) (bool, error) {
	f.releaseCalls++
	return false, nil
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
