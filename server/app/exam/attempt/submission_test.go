// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package attempt

import (
	"context"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

func TestSubmitSealsExactAcknowledgedStateBeforePublishingEffects(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	credential := model.NewCredentialToken()
	participationID := model.NewAttemptParticipationID()
	workspaceID := model.NewExamAttemptWorkspaceID()
	submissionID := model.NewSubmissionID()
	f.submissionID = submissionID
	digest := strings.Repeat("d", 64)
	f.submissions.target = &store.ExamSubmissionSealTarget{ExamID: f.sitting.ExamID, SittingID: f.sitting.ID,
		ClassID: f.sitting.ClassID, CandidateUserID: f.userID, WorkspaceID: workspaceID}
	f.submissions.sealResult = &store.ExamSubmissionSealResult{Receipt: store.ExamSubmissionReceipt{
		SubmissionID: submissionID, AttemptID: f.attemptID, State: model.ExamAttemptSubmitted,
		WorkspaceCursor: 11, ManifestDigest: digest, SubmittedAt: f.at}, ExamID: f.sitting.ExamID,
		SittingID: f.sitting.ID, ClassID: f.sitting.ClassID, CandidateUserID: f.userID,
		ParticipationID: participationID, Generation: 3, ConnectionID: f.connectionID}

	result, err := f.service.Submit(context.Background(), f.call, SubmitCommand{
		Access: WorkspaceMutationAccess{CandidateAccess: CandidateAccess{AttemptID: f.attemptID,
			ConnectionID: f.connectionID, ContinuityCredential: credential}, ParticipationID: participationID, Generation: 3},
		ExpectedWorkspaceCursor: 11, FinalFocusLossSequence: 7, Idempotency: &store.CommandIdempotency{},
	})
	if err != nil {
		t.Fatal(err)
	}
	principal := f.call.Principal()
	access := f.submissions.resolvedAccess
	if access.AttemptID != f.attemptID || access.ParticipationID != participationID || access.Generation != 3 ||
		access.ConnectionID != f.connectionID || access.CandidateUserID != principal.UserID || access.SessionID != principal.SessionID ||
		access.ContinuityCredentialHash != model.HashToken(credential) || access.ExpectedWorkspaceCursor != 11 ||
		access.FinalFocusLossSequence != 7 || f.submissions.seal == nil || !f.submissions.seal.SubmissionID.IsValid() ||
		!model.IsValidId(f.submissions.seal.AuditEventID) || result.Receipt.SubmissionID != submissionID || f.effects.submitted != 1 {
		t.Fatalf("access=%#v seal=%#v result=%#v effects=%#v", access, f.submissions.seal, result, f.effects)
	}
	audit := fmt.Sprintf("%#v", f.audit.values)
	for _, private := range []string{credential, model.HashToken(credential), participationID.String()} {
		if strings.Contains(audit, private) {
			t.Fatalf("private Submission material entered audit: %s", audit)
		}
	}
	if len(f.audit.values) != 1 || f.audit.values["exam_attempt_id"] != f.attemptID.String() {
		t.Fatalf("Submission audit fields=%#v", f.audit.values)
	}
	if got := strings.Join(f.order, ","); got != "submission.resolve,audit,submission.seal,effect.submit" {
		t.Fatalf("order=%s", got)
	}
}

func TestSubmitReplayReturnsRetainedReceiptAndSuppressesEffects(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	proposedID, retainedID := model.NewSubmissionID(), model.NewSubmissionID()
	f.submissionID = proposedID
	access := validWorkspaceMutationAccess(f)
	f.submissions.target = &store.ExamSubmissionSealTarget{ExamID: f.sitting.ExamID, SittingID: f.sitting.ID,
		ClassID: f.sitting.ClassID, CandidateUserID: f.userID, WorkspaceID: model.NewExamAttemptWorkspaceID()}
	f.submissions.sealResult = &store.ExamSubmissionSealResult{Receipt: store.ExamSubmissionReceipt{SubmissionID: retainedID,
		AttemptID: f.attemptID, State: model.ExamAttemptSubmitted, WorkspaceCursor: 0,
		ManifestDigest: strings.Repeat("e", 64), SubmittedAt: f.at}, ExamID: f.sitting.ExamID,
		SittingID: f.sitting.ID, ClassID: f.sitting.ClassID, CandidateUserID: f.userID,
		ParticipationID: access.ParticipationID, Generation: access.Generation, ConnectionID: access.ConnectionID, Replayed: true}

	result, err := f.service.Submit(context.Background(), f.call, SubmitCommand{Access: access,
		ExpectedWorkspaceCursor: 0, FinalFocusLossSequence: 0, Idempotency: &store.CommandIdempotency{}})
	if err != nil {
		t.Fatal(err)
	}
	if result.Receipt.SubmissionID != retainedID || f.submissions.seal.SubmissionID != proposedID ||
		!result.Replayed || f.effects.submitted != 0 {
		t.Fatalf("result=%#v seal=%#v effects=%#v", result, f.submissions.seal, f.effects)
	}
}

func TestManagedSubmissionNestedOwnershipMismatchIsConcealed(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	submissionID := model.NewSubmissionID()
	f.submissions.authorization = &store.ExamSubmissionAuthorization{SubmissionID: submissionID,
		ExamID: f.sitting.ExamID, SittingID: f.sitting.ID, AttemptID: f.attemptID, AcademicUnitID: model.NewAcademicUnitID()}
	_, err := f.service.GetSubmission(context.Background(), f.call, GetSubmissionQuery{ExamID: model.NewExamID(),
		SittingID: f.sitting.ID, AttemptID: f.attemptID, SubmissionID: submissionID})
	var fault *Fault
	if !errors.As(err, &fault) || fault.Code != "exam.attempt.not_found" ||
		strings.Join(f.order, ",") != "submission.authorize,submission.resolve_owner" {
		t.Fatalf("error=%v order=%v", err, f.order)
	}
}

func TestOpenSubmissionFileAuthorizesThenStreamsRetainedStarterBytes(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	submission := submissionFixture(t, f, 5)
	entryID, version, starterID := model.NewAttemptWorkspaceEntryID(), model.NewWorkspaceContentVersion(), model.NewStarterWorkspaceObjectID()
	f.submissions.authorization = &store.ExamSubmissionAuthorization{SubmissionID: submission.ID,
		ExamID: f.sitting.ExamID, SittingID: f.sitting.ID, AttemptID: f.attemptID, AcademicUnitID: model.NewAcademicUnitID()}
	f.submissions.file = &store.ExamSubmissionFileSelector{Entry: store.ExamSubmissionManifestItem{EntryID: entryID,
		Kind: model.StarterWorkspaceEntryFile, Path: "main.go", ContentVersion: version, MediaType: "text/x-go",
		SizeBytes: 4, SHA256: strings.Repeat("a", 64)}, StorageOrigin: model.AttemptWorkspaceStorageStarter,
		StarterObjectID: starterID, ContentVersion: version}

	opened, err := f.service.OpenSubmissionFile(context.Background(), f.call, OpenSubmissionFileQuery{
		GetSubmissionQuery: GetSubmissionQuery{ExamID: f.sitting.ExamID, SittingID: f.sitting.ID,
			AttemptID: f.attemptID, SubmissionID: submission.ID}, EntryID: entryID})
	if err != nil {
		t.Fatal(err)
	}
	defer opened.Body.Close()
	body, _ := io.ReadAll(opened.Body)
	if string(body) != "workspace" || opened.ContentVersion != version || opened.SHA256 != strings.Repeat("a", 64) ||
		f.content.starterID != starterID || f.content.openAttemptID.IsValid() {
		t.Fatalf("opened=%#v starter=%s attempt=%s body=%q", opened, f.content.starterID, f.content.openAttemptID, body)
	}
}

func TestOpenSubmissionFileStreamsRetainedAttemptOriginBytes(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	submission := submissionFixture(t, f, 5)
	entryID, version, objectID := model.NewAttemptWorkspaceEntryID(), model.NewWorkspaceContentVersion(), model.NewAttemptWorkspaceObjectID()
	f.submissions.authorization = &store.ExamSubmissionAuthorization{SubmissionID: submission.ID,
		ExamID: f.sitting.ExamID, SittingID: f.sitting.ID, AttemptID: f.attemptID, AcademicUnitID: model.NewAcademicUnitID()}
	f.submissions.file = &store.ExamSubmissionFileSelector{Entry: store.ExamSubmissionManifestItem{EntryID: entryID,
		Kind: model.StarterWorkspaceEntryFile, Path: "answer.txt", ContentVersion: version, MediaType: "text/plain",
		SizeBytes: 4, SHA256: strings.Repeat("b", 64)}, StorageOrigin: model.AttemptWorkspaceStorageAttempt,
		AttemptObjectID: objectID, ContentVersion: version}
	f.content.openAttemptBody = "work"

	opened, err := f.service.OpenSubmissionFile(context.Background(), f.call, OpenSubmissionFileQuery{
		GetSubmissionQuery: GetSubmissionQuery{ExamID: f.sitting.ExamID, SittingID: f.sitting.ID,
			AttemptID: f.attemptID, SubmissionID: submission.ID}, EntryID: entryID})
	if err != nil {
		t.Fatal(err)
	}
	defer opened.Body.Close()
	body, _ := io.ReadAll(opened.Body)
	if string(body) != "work" || opened.ContentVersion != version || opened.SHA256 != strings.Repeat("b", 64) ||
		f.content.openAttemptID != objectID || f.content.starterID.IsValid() {
		t.Fatalf("opened=%#v starter=%s attempt=%s body=%q", opened, f.content.starterID, f.content.openAttemptID, body)
	}
}

func TestOpenSubmissionFileFailsClosedOnRetainedSelectorMismatch(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	submission := submissionFixture(t, f, 5)
	entryID := model.NewAttemptWorkspaceEntryID()
	f.submissions.authorization = &store.ExamSubmissionAuthorization{SubmissionID: submission.ID,
		ExamID: f.sitting.ExamID, SittingID: f.sitting.ID, AttemptID: f.attemptID, AcademicUnitID: model.NewAcademicUnitID()}
	f.submissions.file = &store.ExamSubmissionFileSelector{Entry: store.ExamSubmissionManifestItem{
		EntryID: model.NewAttemptWorkspaceEntryID(), Kind: model.StarterWorkspaceEntryFile, Path: "answer.txt",
		ContentVersion: model.NewWorkspaceContentVersion(), MediaType: "text/plain", SizeBytes: 4,
		SHA256: strings.Repeat("b", 64)}, StorageOrigin: model.AttemptWorkspaceStorageAttempt,
		AttemptObjectID: model.NewAttemptWorkspaceObjectID(), ContentVersion: model.NewWorkspaceContentVersion()}

	_, err := f.service.OpenSubmissionFile(context.Background(), f.call, OpenSubmissionFileQuery{
		GetSubmissionQuery: GetSubmissionQuery{ExamID: f.sitting.ExamID, SittingID: f.sitting.ID,
			AttemptID: f.attemptID, SubmissionID: submission.ID}, EntryID: entryID})
	var fault *Fault
	if !errors.As(err, &fault) || fault.Code != "exam.attempt.unavailable" ||
		f.content.openAttemptID.IsValid() || f.content.starterID.IsValid() {
		t.Fatalf("error=%v starter=%s attempt=%s", err, f.content.starterID, f.content.openAttemptID)
	}
}

func TestListSubmissionManifestReturnsBoundedEntryIDKeysetAfterAuthorization(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	submission := submissionFixture(t, f, 5)
	f.submissions.authorization = &store.ExamSubmissionAuthorization{SubmissionID: submission.ID,
		ExamID: f.sitting.ExamID, SittingID: f.sitting.ID, AttemptID: f.attemptID, AcademicUnitID: model.NewAcademicUnitID()}
	ids := []model.AttemptWorkspaceEntryID{model.NewAttemptWorkspaceEntryID(), model.NewAttemptWorkspaceEntryID(), model.NewAttemptWorkspaceEntryID()}
	slices.SortFunc(ids, func(left, right model.AttemptWorkspaceEntryID) int {
		return strings.Compare(left.String(), right.String())
	})
	f.submissions.manifest = &store.ExamSubmissionManifestPage{SubmissionID: submission.ID, WorkspaceCursor: 5,
		ManifestDigest: submission.ManifestDigest, Items: []store.ExamSubmissionManifestItem{
			{EntryID: ids[1], Kind: model.StarterWorkspaceEntryDirectory, Path: "src"},
			{EntryID: ids[2], Kind: model.StarterWorkspaceEntryFile, Path: "src/main.go",
				ContentVersion: model.NewWorkspaceContentVersion(), MediaType: "text/x-go", SizeBytes: 4, SHA256: strings.Repeat("a", 64)},
		}}

	page, err := f.service.ListSubmissionManifest(context.Background(), f.call, ListSubmissionManifestQuery{
		GetSubmissionQuery: GetSubmissionQuery{ExamID: f.sitting.ExamID, SittingID: f.sitting.ID,
			AttemptID: f.attemptID, SubmissionID: submission.ID}, AfterEntryID: ids[0], Limit: 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	if page.SubmissionID != submission.ID || len(page.Items) != 2 || page.Items[1].Path != "src/main.go" ||
		f.submissions.manifestOptions.AfterEntryID != ids[0] || f.submissions.manifestOptions.Limit != 2 {
		t.Fatalf("page=%#v options=%#v", page, f.submissions.manifestOptions)
	}
}

func TestGetSubmissionAuthorizesCanonicalResourceBeforeProtectedRead(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	submission := submissionFixture(t, f, 5)
	f.submissions.authorization = &store.ExamSubmissionAuthorization{SubmissionID: submission.ID,
		ExamID: f.sitting.ExamID, SittingID: f.sitting.ID, AttemptID: f.attemptID, AcademicUnitID: model.NewAcademicUnitID()}
	f.submissions.submission = submission

	got, err := f.service.GetSubmission(context.Background(), f.call, GetSubmissionQuery{ExamID: f.sitting.ExamID,
		SittingID: f.sitting.ID, AttemptID: f.attemptID, SubmissionID: submission.ID})
	if err != nil {
		t.Fatal(err)
	}
	if got == nil || got.Authorization != *f.submissions.authorization || got.Submission.ID != submission.ID ||
		f.submissionAuthorizationID != submission.ID {
		t.Fatalf("got=%#v authorized=%s", got, f.submissionAuthorizationID)
	}
	if order := strings.Join(f.order, ","); order != "submission.authorize,submission.resolve_owner,submission.get" {
		t.Fatalf("order=%s", order)
	}
}

func submissionFixture(t *testing.T, f *fixture, cursor int64) *model.ExamSubmission {
	t.Helper()
	manifest, err := model.NewExamSubmissionManifest(cursor, nil)
	if err != nil {
		t.Fatal(err)
	}
	submission, err := model.NewExamSubmission(model.ExamSubmissionSpecification{ID: model.NewSubmissionID(),
		AttemptID: f.attemptID, WorkspaceID: model.NewExamAttemptWorkspaceID(), Manifest: manifest,
		SubmittedAt: f.at.Add(time.Minute)})
	if err != nil {
		t.Fatal(err)
	}
	return submission
}

type submissionStoreFake struct {
	f               *fixture
	resolvedAccess  store.ExamSubmissionSealAccess
	target          *store.ExamSubmissionSealTarget
	seal            *store.ExamSubmissionSeal
	sealResult      *store.ExamSubmissionSealResult
	err             error
	authorization   *store.ExamSubmissionAuthorization
	submission      *model.ExamSubmission
	manifest        *store.ExamSubmissionManifestPage
	manifestOptions store.ExamSubmissionManifestListOptions
	file            *store.ExamSubmissionFileSelector
}

func (fake *submissionStoreFake) ResolveSealTarget(_ context.Context, access store.ExamSubmissionSealAccess) (*store.ExamSubmissionSealTarget, error) {
	fake.f.order = append(fake.f.order, "submission.resolve")
	fake.resolvedAccess = access
	return fake.target, fake.err
}

func (fake *submissionStoreFake) Seal(_ context.Context, input *store.ExamSubmissionSeal, _ *store.CommandIdempotency) (*store.ExamSubmissionSealResult, error) {
	fake.f.order = append(fake.f.order, "submission.seal")
	fake.seal = input
	return fake.sealResult, fake.err
}

func (fake *submissionStoreFake) Resolve(context.Context, model.SubmissionID) (*store.ExamSubmissionAuthorization, error) {
	fake.f.order = append(fake.f.order, "submission.resolve_owner")
	return fake.authorization, fake.err
}

func (fake *submissionStoreFake) Get(context.Context, model.SubmissionID) (*model.ExamSubmission, error) {
	fake.f.order = append(fake.f.order, "submission.get")
	return fake.submission, fake.err
}

func (fake *submissionStoreFake) ListManifest(_ context.Context, options store.ExamSubmissionManifestListOptions) (*store.ExamSubmissionManifestPage, error) {
	fake.manifestOptions = options
	return fake.manifest, fake.err
}

func (fake *submissionStoreFake) ResolveFile(context.Context, model.SubmissionID, model.AttemptWorkspaceEntryID) (*store.ExamSubmissionFileSelector, error) {
	fake.f.order = append(fake.f.order, "submission.resolve_file")
	return fake.file, fake.err
}
