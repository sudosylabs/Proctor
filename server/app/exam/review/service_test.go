// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package review

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

func TestSaveDecisionAuthorizesAuditsAtomicallyAndSuppressesPrivateRationale(t *testing.T) {
	t.Parallel()
	f := newReviewFixture(t)
	result, err := f.service.SaveDecision(context.Background(), f.call, SaveDecisionCommand{
		SubmissionID: f.submissionID, FlagID: f.flagID, ExpectedReviewRevision: 0,
		ExpectedDecisionRevision: 0, Outcome: model.IntegrityReviewConfirmed,
		PrivateRationale: "The retained continuity evidence was verified.", Idempotency: &store.CommandIdempotency{},
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
}

func TestExactDecisionReplayReturnsRetainedResultWithoutEffect(t *testing.T) {
	t.Parallel()
	f := newReviewFixture(t)
	f.persistence.replayed = true
	result, err := f.service.SaveDecision(context.Background(), f.call, SaveDecisionCommand{
		SubmissionID: f.submissionID, FlagID: f.flagID, Outcome: model.IntegrityReviewConfirmed,
		PrivateRationale: "The retained continuity evidence was verified.", Idempotency: &store.CommandIdempotency{},
	})
	if err != nil || !result.Replayed || f.effects.changed != 0 {
		t.Fatalf("result=%#v error=%v effects=%d", result, err, f.effects.changed)
	}
}

func TestFinalizeAndReleaseUseDistinctAuthorizationAndEffects(t *testing.T) {
	t.Parallel()
	f := newReviewFixture(t)
	f.persistence.mode = "finalize"
	finalized, err := f.service.Finalize(context.Background(), f.call, FinalizeCommand{SubmissionID: f.submissionID,
		ReviewID: f.reviewID, ExpectedReviewRevision: 2, Idempotency: &store.CommandIdempotency{}})
	if err != nil || finalized.Review.State != model.SubmissionReviewFinalized || f.authorizer.reviewCalls != 1 || f.effects.finalized != 1 {
		t.Fatalf("finalized=%#v error=%v auth=%#v effects=%#v", finalized, err, f.authorizer, f.effects)
	}
	f.persistence.mode = "release"
	released, err := f.service.Release(context.Background(), f.call, ReleaseCommand{SubmissionID: f.submissionID,
		ReviewID: f.reviewID, ExpectedReviewRevision: 3, Idempotency: &store.CommandIdempotency{}})
	if err != nil || released.Review.ReleaseState != model.SubmissionReviewReleased || f.authorizer.releaseCalls != 1 || f.effects.released != 1 {
		t.Fatalf("released=%#v error=%v auth=%#v effects=%#v", released, err, f.authorizer, f.effects)
	}
}

func TestCandidateResultRequiresSessionOwnershipAndSanitizesRemarks(t *testing.T) {
	t.Parallel()
	f := newReviewFixture(t)
	f.persistence.student = &model.StudentResult{ReviewID: f.reviewID, SubmissionID: f.submissionID,
		AttemptID: f.attemptID, CandidateUserID: f.userID,
		StudentRemarksMarkdown: "Safe **text** <script>alert(1)</script> ![remote](https://tracker.example/x)", ReleasedAt: f.at}
	result, err := f.service.GetStudentResult(context.Background(), f.call, GetStudentResultQuery{AttemptID: f.attemptID})
	if err != nil {
		t.Fatalf("GetStudentResult() error = %v", err)
	}
	if result.StudentRemarksMarkdown != "Safe **text**  remote" {
		t.Fatalf("sanitized remarks = %q", result.StudentRemarksMarkdown)
	}
	wrong := f.call
	wrong.principal.UserID = model.NewUserID()
	if _, err = f.service.GetStudentResult(context.Background(), wrong, GetStudentResultQuery{AttemptID: f.attemptID}); err == nil {
		t.Fatal("GetStudentResult() accepted another candidate")
	}
	pat := f.call
	pat.principal.CredentialType = model.CredentialPersonalAccessToken
	pat.principal.SessionID = ""
	if _, err = f.service.GetStudentResult(context.Background(), pat, GetStudentResultQuery{AttemptID: f.attemptID}); err == nil {
		t.Fatal("GetStudentResult() accepted non-Session principal")
	}
}

type reviewFixture struct {
	service      *Service
	call         Call
	persistence  *reviewStoreFake
	authorizer   *reviewAuthorizerFake
	auditor      *reviewAuditorFake
	effects      *reviewEffectsFake
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
	service, err := New(Dependencies{Persistence: f.persistence, Authorizer: f.authorizer, Auditor: f.auditor,
		Effects: f.effects, EffectFailures: f.effects, Now: func() time.Time { return f.at },
		NewReviewID: model.NewSubmissionReviewID, NewDecisionID: model.NewIntegrityReviewDecisionID})
	if err != nil {
		t.Fatal(err)
	}
	f.service = service
	f.call = NewCall(model.Principal{UserID: f.userID, SessionID: model.NewSessionID(), CredentialID: model.PrincipalCredentialID(model.NewId()),
		CredentialType: model.CredentialSessionAccess, AuthenticationMethod: "password", AuthenticationStrength: model.AuthenticationSingleFactor,
		ClientType: model.SessionClientWeb, AuthenticatedAt: f.at}, model.RequestMetadata{})
	return f
}

type reviewStoreFake struct {
	f        *reviewFixture
	decision *store.ExamIntegrityReviewDecisionMutation
	replayed bool
	mode     string
	student  *model.StudentResult
}

func (fake *reviewStoreFake) Resolve(context.Context, model.SubmissionID) (*store.ExamIntegrityReviewAuthorization, error) {
	return &store.ExamIntegrityReviewAuthorization{SubmissionID: fake.f.submissionID, ExamID: model.NewExamID(), SittingID: model.NewExamSittingID(), AttemptID: fake.f.attemptID, CandidateUserID: fake.f.userID, AcademicUnitID: model.NewAcademicUnitID()}, nil
}
func (fake *reviewStoreFake) Get(context.Context, model.SubmissionID) (*store.ExamSubmissionReviewSnapshot, error) {
	return nil, errors.New("unused")
}
func (fake *reviewStoreFake) ListFlags(context.Context, store.ExamIntegrityFlagListOptions) (*store.ExamIntegrityFlagPage, error) {
	return nil, errors.New("unused")
}
func (fake *reviewStoreFake) ListEvidence(context.Context, store.ExamIntegrityEvidenceListOptions) (*store.ExamIntegrityEvidencePage, error) {
	return nil, errors.New("unused")
}
func (fake *reviewStoreFake) ListDiscrepancies(context.Context, store.ExamIntegrityDiscrepancyListOptions) (*store.ExamIntegrityDiscrepancyPage, error) {
	return nil, errors.New("unused")
}
func (fake *reviewStoreFake) SaveDecision(_ context.Context, input *store.ExamIntegrityReviewDecisionMutation, _ *store.CommandIdempotency) (*store.ExamIntegrityReviewMutationResult, error) {
	fake.decision = input
	review, _ := model.NewSubmissionReview(input.ReviewID, fake.f.submissionID, fake.f.userID, fake.f.at)
	decision, _ := model.NewIntegrityReviewDecision(input.DecisionID, review.ID, fake.f.flagID,
		model.IntegrityReviewConfirmed, fake.f.userID, "The retained continuity evidence was verified.", fake.f.at)
	return &store.ExamIntegrityReviewMutationResult{Authorization: *mustReviewAuthorization(fake), Review: review, Decision: decision, Replayed: fake.replayed}, nil
}
func (fake *reviewStoreFake) UpdateDraft(context.Context, *store.ExamIntegrityReviewDraftMutation, *store.CommandIdempotency) (*store.ExamIntegrityReviewMutationResult, error) {
	return nil, errors.New("unused")
}
func (fake *reviewStoreFake) Finalize(context.Context, *store.ExamIntegrityReviewFinalize, *store.CommandIdempotency) (*store.ExamIntegrityReviewMutationResult, error) {
	review, _ := model.NewSubmissionReview(fake.f.reviewID, fake.f.submissionID, fake.f.userID, fake.f.at.Add(-time.Minute))
	_ = review.UpdateDraft(1, "", "", fake.f.at.Add(-30*time.Second))
	_ = review.Finalize(2, fake.f.userID, 0, 0, 0, "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", fake.f.at)
	return &store.ExamIntegrityReviewMutationResult{Authorization: *mustReviewAuthorization(fake), Review: review}, nil
}
func (fake *reviewStoreFake) Release(context.Context, *store.ExamIntegrityReviewRelease, *store.CommandIdempotency) (*store.ExamIntegrityReviewMutationResult, error) {
	result, _ := fake.Finalize(context.Background(), nil, nil)
	_ = result.Review.Release(3, fake.f.userID, fake.f.at.Add(time.Minute))
	return result, nil
}
func (fake *reviewStoreFake) GetReleasedStudentResult(_ context.Context, attemptID model.ExamAttemptID, candidateID model.UserID) (*model.StudentResult, error) {
	if fake.student == nil || fake.student.AttemptID != attemptID || fake.student.CandidateUserID != candidateID {
		return nil, store.NewErrNotFound("student_result", attemptID.String())
	}
	clone := *fake.student
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

func (*reviewAuthorizerFake) AuthorizeView(context.Context, Call, model.SubmissionID) error {
	return nil
}
func (fake *reviewAuthorizerFake) AuthorizeReview(_ context.Context, _ Call, id model.SubmissionID) (bool, error) {
	fake.reviewSubmission, fake.reviewCalls = id, fake.reviewCalls+1
	return false, nil
}
func (fake *reviewAuthorizerFake) AuthorizeRelease(_ context.Context, _ Call, _ model.SubmissionID) (bool, error) {
	fake.releaseCalls++
	return false, nil
}

type reviewAuditorFake struct{ value map[string]any }

func (fake *reviewAuditorFake) Begin(_ context.Context, _ Call, _ model.Action, _ model.Resource, _ model.RoleScopeType, _, _ string, value map[string]any) (string, error) {
	fake.value = value
	return model.NewAuditEventID().String(), nil
}
func (*reviewAuditorFake) Fail(context.Context, string, string) error { return nil }

type reviewEffectsFake struct{ changed, finalized, released int }

func (fake *reviewEffectsFake) ReviewChanged(context.Context, Result) error {
	fake.changed++
	return nil
}
func (fake *reviewEffectsFake) ReviewFinalized(context.Context, Result) error {
	fake.finalized++
	return nil
}
func (fake *reviewEffectsFake) ResultReleased(context.Context, Result) error {
	fake.released++
	return nil
}
func (*reviewEffectsFake) Report(context.Context, string, error) {}
