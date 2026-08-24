// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package correction

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

func TestStageResourceContentReservesBeforeDeterministicWriteAndReturnsAuthoritativeMetadata(t *testing.T) {
	t.Parallel()
	f := newCorrectionFixture(t)
	sha := strings.Repeat("a", 64)
	got, err := f.service.StageResourceContent(context.Background(), f.call, StageResourceContentCommand{
		ExamID: f.examID, SittingID: f.sittingID, BaseRevisionID: f.baseRevisionID,
		Target: store.ExamCorrectionResourceAddition, MediaType: model.ExamResourceMediaText,
		Body: strings.NewReader("notes"), Size: 5, ExpectedSHA256: sha, IdempotencyKey: "test-key",
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.StageID.IsZero() || got.ResourceID.IsZero() || got.MediaType != model.ExamResourceMediaText || got.Size != 5 || got.SHA256 != sha || got.ExpiresAt.IsZero() {
		t.Fatalf("stage=%#v", got)
	}
	if want := "access,membership,authorize,audit.begin,reserve,content.store,ready"; strings.Join(f.order, ",") != want {
		t.Fatalf("order=%v want=%s", f.order, want)
	}
	if f.persistence.reservation.AuditEventID == "" || f.persistence.reservation.AuditAt < 1 {
		t.Fatalf("reservation audit=%#v", f.persistence.reservation)
	}
	if f.authorizer.action != model.ActionExamSittingManage || f.authorizer.resource != (model.Resource{Type: model.ResourceExamSitting, ID: f.sittingID.String()}) || f.persistence.reservation.ManagerOverride {
		t.Fatalf("authorization=%s %#v override=%v", f.authorizer.action, f.authorizer.resource, f.persistence.reservation.ManagerOverride)
	}
	for _, forbidden := range []string{"media_type", "size", "sha256", "stage_id", "private_reason", "display_name"} {
		if _, ok := f.auditor.value[forbidden]; ok {
			t.Fatalf("unsafe audit field %q in %#v", forbidden, f.auditor.value)
		}
	}
	wantIdempotency, prepareErr := prepareStageIdempotency(f.call, StageResourceContentCommand{
		ExamID: f.examID, SittingID: f.sittingID, BaseRevisionID: f.baseRevisionID,
		Target: store.ExamCorrectionResourceAddition, MediaType: model.ExamResourceMediaText,
		Size: 5, ExpectedSHA256: sha, IdempotencyKey: "test-key",
	})
	if prepareErr != nil {
		t.Fatal(prepareErr)
	}
	assertStoreBoundaryCommand(t, f.persistence.idempotency, wantIdempotency)
}

func TestStageResourceContentReadyReplaySkipsContentWrite(t *testing.T) {
	t.Parallel()
	f := newCorrectionFixture(t)
	f.persistence.returnReady = true
	_, err := f.service.StageResourceContent(context.Background(), f.call, StageResourceContentCommand{ExamID: f.examID, SittingID: f.sittingID, BaseRevisionID: f.baseRevisionID, Target: store.ExamCorrectionResourceAddition, MediaType: model.ExamResourceMediaText, Body: strings.NewReader("notes"), Size: 5, ExpectedSHA256: strings.Repeat("a", 64), IdempotencyKey: "test-key"})
	if err != nil {
		t.Fatal(err)
	}
	if f.content.calls != 0 || f.persistence.readyCalls != 0 {
		t.Fatalf("content=%d ready=%d", f.content.calls, f.persistence.readyCalls)
	}
}

func TestStageResourceContentUsesReservedTimeForConcurrentReplay(t *testing.T) {
	t.Parallel()
	f := newCorrectionFixture(t)
	f.persistence.stageCreatedAt = f.at.Add(-time.Minute)
	_, err := f.service.StageResourceContent(context.Background(), f.call, StageResourceContentCommand{
		ExamID: f.examID, SittingID: f.sittingID, BaseRevisionID: f.baseRevisionID,
		Target: store.ExamCorrectionResourceAddition, MediaType: model.ExamResourceMediaText,
		Body: strings.NewReader("notes"), Size: 5, ExpectedSHA256: strings.Repeat("a", 64), IdempotencyKey: "test-key",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !f.content.createdAt.Equal(f.persistence.stageCreatedAt) {
		t.Fatalf("rendition created_at=%s want reserved %s", f.content.createdAt, f.persistence.stageCreatedAt)
	}
}

func TestStageResourceContentUsesExplicitOverrideWithoutCurrentUnitMembership(t *testing.T) {
	t.Parallel()
	f := newCorrectionFixture(t)
	f.memberships.empty = true
	_, err := f.service.StageResourceContent(context.Background(), f.call, StageResourceContentCommand{ExamID: f.examID, SittingID: f.sittingID, BaseRevisionID: f.baseRevisionID, Target: store.ExamCorrectionResourceAddition, MediaType: model.ExamResourceMediaText, Body: strings.NewReader("notes"), Size: 5, ExpectedSHA256: strings.Repeat("a", 64), IdempotencyKey: "test-key"})
	if err != nil {
		t.Fatal(err)
	}
	if f.authorizer.action != model.ActionExamSittingManageOverride || !f.persistence.reservation.ManagerOverride {
		t.Fatalf("action=%s reservation=%#v", f.authorizer.action, f.persistence.reservation)
	}
}

func TestStageReplacementPinsFileEntryFromExactBaseRevision(t *testing.T) {
	t.Parallel()
	f := newCorrectionFixture(t)
	resourceID, entryID := model.NewExamResourceID(), model.NewFileEntryID()
	f.revisions.snapshot = &model.ExamRevision{ID: f.baseRevisionID, ExamID: f.examID, Resources: []model.ExamRevisionResource{{ResourceID: resourceID, FileEntryID: entryID}}}
	_, err := f.service.StageResourceContent(context.Background(), f.call, StageResourceContentCommand{ExamID: f.examID, SittingID: f.sittingID, BaseRevisionID: f.baseRevisionID, Target: store.ExamCorrectionResourceReplacement, ResourceID: resourceID, MediaType: model.ExamResourceMediaText, Body: strings.NewReader("notes"), Size: 5, ExpectedSHA256: strings.Repeat("a", 64), IdempotencyKey: "test-key"})
	if err != nil {
		t.Fatal(err)
	}
	if f.persistence.reservation.Entry != nil || f.persistence.reservation.FileEntryID != entryID || f.persistence.reservation.ResourceID != resourceID {
		t.Fatalf("reservation=%#v", f.persistence.reservation)
	}
}

func TestApplyUsesOneAtomicStoreCommandAndSuppressesReplayEffects(t *testing.T) {
	t.Parallel()
	f := newCorrectionFixture(t)
	f.persistence.applyReplayed = true
	result, err := f.service.Apply(context.Background(), f.call, ApplyCommand{ExamID: f.examID, SittingID: f.sittingID, ExpectedSittingRevision: 3, ExpectedCurrentRevisionID: f.baseRevisionID, Instructions: OptionalInstructions{Present: true, Markdown: "Updated"}, Resources: []ResourceManifestItem{}, PrivateReason: "Correct a discovered ambiguity", IdempotencyKey: "test-key"})
	if err != nil {
		t.Fatal(err)
	}
	if result.PreviousRevisionID != f.baseRevisionID || result.RevisionID.IsZero() || result.SittingRevision != 4 ||
		!result.EffectiveAt.Equal(f.at.Add(-time.Minute)) || !result.Replayed {
		t.Fatalf("result=%#v", result)
	}
	if f.persistence.application == nil || f.persistence.application.InstructionsMarkdown == nil || *f.persistence.application.InstructionsMarkdown != "Updated" || f.persistence.application.PrivateReason == "" {
		t.Fatalf("application=%#v", f.persistence.application)
	}
	if f.effects.calls != 0 {
		t.Fatalf("replay effects=%d", f.effects.calls)
	}
	for _, forbidden := range []string{"instructions_markdown", "resources", "private_reason", "stage_id", "sha256"} {
		if _, ok := f.auditor.value[forbidden]; ok {
			t.Fatalf("unsafe audit field %q", forbidden)
		}
	}
	wantIdempotency, prepareErr := prepareApplyIdempotency(f.call, ApplyCommand{ExamID: f.examID, SittingID: f.sittingID,
		ExpectedSittingRevision: 3, ExpectedCurrentRevisionID: f.baseRevisionID,
		Instructions: OptionalInstructions{Present: true, Markdown: "Updated"}, Resources: []ResourceManifestItem{},
		PrivateReason: "Correct a discovered ambiguity", IdempotencyKey: "test-key"})
	if prepareErr != nil {
		t.Fatal(prepareErr)
	}
	assertStoreBoundaryCommand(t, f.persistence.idempotency, wantIdempotency)
}

func TestApplyPublishesOnlyAfterCommitAndReportsTransientFailure(t *testing.T) {
	t.Parallel()
	f := newCorrectionFixture(t)
	f.effects.err = errors.New("realtime unavailable")
	_, err := f.service.Apply(context.Background(), f.call, ApplyCommand{ExamID: f.examID, SittingID: f.sittingID, ExpectedSittingRevision: 3, ExpectedCurrentRevisionID: f.baseRevisionID, Resources: []ResourceManifestItem{}, PrivateReason: "Correct a discovered ambiguity", IdempotencyKey: "test-key"})
	if err != nil {
		t.Fatal(err)
	}
	if f.persistence.application == nil || f.effects.calls != 1 || f.effects.reportCalls != 1 || strings.Join(f.order, ",") != "access,membership,authorize,audit.begin,apply,effect" {
		t.Fatalf("application=%#v effects=%#v order=%v", f.persistence.application, f.effects, f.order)
	}
}

func TestApplyRejectsNonCanonicalPrivateReasonBeforeAuthorization(t *testing.T) {
	t.Parallel()
	f := newCorrectionFixture(t)
	_, err := f.service.Apply(context.Background(), f.call, ApplyCommand{ExamID: f.examID, SittingID: f.sittingID, ExpectedSittingRevision: 3, ExpectedCurrentRevisionID: f.baseRevisionID, PrivateReason: " padded ", IdempotencyKey: "test-key"})
	var fault *Fault
	if !errors.As(err, &fault) || fault.Code != "exam.sitting.correction.invalid" || len(f.order) != 0 {
		t.Fatalf("error=%v order=%v", err, f.order)
	}
}

type correctionFixture struct {
	service        *Service
	call           Call
	examID         model.ExamID
	sittingID      model.ExamSittingID
	baseRevisionID model.ExamRevisionID
	unitID         model.AcademicUnitID
	userID         model.UserID
	at             time.Time
	order          []string
	persistence    *correctionStoreFake
	access         *correctionAccessFake
	memberships    *correctionMembershipFake
	authorizer     *correctionAuthorizerFake
	auditor        *correctionAuditFake
	effects        *correctionEffectFake
	content        *correctionContentFake
	revisions      *correctionRevisionsFake
}

func newCorrectionFixture(t *testing.T) *correctionFixture {
	t.Helper()
	f := &correctionFixture{examID: model.NewExamID(), sittingID: model.NewExamSittingID(), baseRevisionID: model.NewExamRevisionID(), unitID: model.NewAcademicUnitID(), userID: model.NewUserID(), at: time.Date(2026, 8, 15, 12, 0, 0, 0, time.UTC)}
	f.persistence = &correctionStoreFake{f: f}
	f.access = &correctionAccessFake{f: f}
	f.memberships = &correctionMembershipFake{f: f}
	f.authorizer = &correctionAuthorizerFake{f: f}
	f.auditor = &correctionAuditFake{f: f}
	f.effects = &correctionEffectFake{f: f}
	f.content = &correctionContentFake{f: f}
	f.revisions = &correctionRevisionsFake{}
	service, err := New(f.persistence, f.revisions, f.access, f.memberships, f.authorizer, f.auditor, f.effects, f.effects, f.content, func() time.Time { return f.at }, model.NewExamCorrectionResourceStageID, model.NewExamResourceID, model.NewFileEntryID, model.NewFileRevisionID, model.NewUploadLeaseID, model.NewFileRenditionID, model.NewExamRevisionID)
	if err != nil {
		t.Fatal(err)
	}
	f.service = service
	f.call = NewCall(model.Principal{UserID: f.userID, SessionID: model.NewSessionID(), CredentialID: model.PrincipalCredentialID(model.NewId()), CredentialType: model.CredentialSessionAccess, AuthenticationMethod: "password", AuthenticationStrength: model.AuthenticationSingleFactor, ClientType: model.SessionClientWeb, AuthenticatedAt: f.at}, model.RequestMetadata{})
	return f
}

type correctionStoreFake struct {
	f              *correctionFixture
	reservation    *store.ExamCorrectionResourceStageReservation
	stageCreatedAt time.Time
	readyCalls     int
	returnReady    bool
	application    *store.ExamCorrectionApplication
	applyReplayed  bool
	idempotency    *store.CommandIdempotency
}

func (s *correctionStoreFake) ReserveResourceStage(_ context.Context, in *store.ExamCorrectionResourceStageReservation, command *store.CommandIdempotency) (*store.ExamCorrectionResourceStage, error) {
	s.f.order = append(s.f.order, "reserve")
	s.reservation, s.idempotency = in, command
	stage := &store.ExamCorrectionResourceStage{ID: in.StageID, ExamID: in.ExamID, SittingID: in.SittingID, BaseRevisionID: in.BaseRevisionID, Target: in.Target, ResourceID: in.ResourceID, FileEntryID: in.FileEntryID, FileRevisionID: in.Revision.ID, UploadLeaseID: in.Lease.ID, RenditionID: in.RenditionID, CreatedByUserID: in.ActorUserID, State: store.ExamCorrectionResourceStagePending, CreatedAt: in.CreatedAt, ExpiresAt: in.Lease.ExpiresAt}
	if !s.stageCreatedAt.IsZero() {
		stage.CreatedAt = s.stageCreatedAt
	}
	if s.returnReady {
		stage.State = store.ExamCorrectionResourceStageReady
		stage.Rendition = correctionRendition(in.RenditionID, in.Revision.ID, in.CreatedAt)
	}
	return stage, nil
}
func (s *correctionStoreFake) MarkResourceStageReady(_ context.Context, in *store.ExamCorrectionResourceStageReadyInput) (*store.ExamCorrectionResourceStage, error) {
	s.f.order = append(s.f.order, "ready")
	s.readyCalls++
	r := in.Rendition
	return &store.ExamCorrectionResourceStage{ID: s.reservation.StageID, ExamID: s.reservation.ExamID, SittingID: s.reservation.SittingID, BaseRevisionID: s.reservation.BaseRevisionID, Target: s.reservation.Target, ResourceID: s.reservation.ResourceID, FileEntryID: s.reservation.FileEntryID, FileRevisionID: s.reservation.Revision.ID, UploadLeaseID: s.reservation.Lease.ID, RenditionID: s.reservation.RenditionID, CreatedByUserID: s.reservation.ActorUserID, State: store.ExamCorrectionResourceStageReady, ExpiresAt: s.reservation.Lease.ExpiresAt, Rendition: r}, nil
}
func (s *correctionStoreFake) Apply(_ context.Context, in *store.ExamCorrectionApplication, command *store.CommandIdempotency) (*store.ExamCorrectionResult, error) {
	s.f.order = append(s.f.order, "apply")
	s.application, s.idempotency = in, command
	sitting := &model.ExamSitting{ID: in.SittingID, ExamID: in.ExamID, ExamRevisionID: in.RevisionID, State: model.ExamSittingOpen, Revision: in.ExpectedSittingRevision + 1, UpdatedAt: in.AppliedAt}
	effectiveAt := in.AppliedAt.Add(-time.Minute)
	return &store.ExamCorrectionResult{Revision: &store.ExamRevisionSummary{ID: in.RevisionID, ExamID: in.ExamID, Number: 2, Kind: model.ExamRevisionPublicationLiveCorrection, PublishedAt: effectiveAt}, Sitting: &store.ExamSittingSnapshot{Sitting: sitting}, PreviousRevisionID: in.CurrentRevisionID, EffectiveAt: effectiveAt, Replayed: s.applyReplayed}, nil
}
func correctionRendition(id model.FileRenditionID, revisionID model.FileRevisionID, at time.Time) *model.FileRendition {
	r, _ := model.NewFileRendition(id, revisionID, "original", string(model.ExamResourceMediaText), 5, 0, 0, strings.Repeat("a", 64), at)
	return r
}

type correctionRevisionsFake struct{ snapshot *model.ExamRevision }

func (r *correctionRevisionsFake) GetSnapshot(context.Context, model.ExamID, model.ExamRevisionID) (*model.ExamRevision, error) {
	if r.snapshot == nil {
		return nil, store.NewErrNotFound("exam_revision", "")
	}
	return r.snapshot, nil
}

type correctionAccessFake struct{ f *correctionFixture }

func (a *correctionAccessFake) Access(context.Context, model.ExamID, model.UserID) (*store.ExamAccessSnapshot, error) {
	a.f.order = append(a.f.order, "access")
	exam, _ := model.NewExam(a.f.examID, a.f.unitID, a.f.userID, a.f.at)
	return &store.ExamAccessSnapshot{Exam: exam, ActorIsManager: true}, nil
}

type correctionMembershipFake struct {
	f     *correctionFixture
	empty bool
}

func (m *correctionMembershipFake) ListActiveByUser(context.Context, string, int64) ([]*model.AcademicUnitMember, error) {
	m.f.order = append(m.f.order, "membership")
	if m.empty {
		return nil, nil
	}
	return []*model.AcademicUnitMember{{AcademicUnitID: m.f.unitID, UserID: m.f.userID}}, nil
}

type correctionAuthorizerFake struct {
	f        *correctionFixture
	action   model.Action
	resource model.Resource
}

func (a *correctionAuthorizerFake) Authorize(_ context.Context, _ Call, action model.Action, resource model.Resource) error {
	a.f.order = append(a.f.order, "authorize")
	a.action, a.resource = action, resource
	return nil
}

type correctionAuditFake struct {
	f     *correctionFixture
	value map[string]any
}

func (a *correctionAuditFake) Begin(_ context.Context, _ Call, _ model.Action, _ model.Resource, _ model.RoleScopeType, _, _ string, value, _ map[string]any) (string, error) {
	a.f.order = append(a.f.order, "audit.begin")
	a.value = value
	return model.NewId(), nil
}
func (a *correctionAuditFake) Fail(context.Context, string, string) error { return nil }

type correctionEffectFake struct {
	f           *correctionFixture
	calls       int
	reportCalls int
	err         error
}

func (e *correctionEffectFake) Corrected(context.Context, Result) error {
	e.f.order = append(e.f.order, "effect")
	e.calls++
	return e.err
}
func (e *correctionEffectFake) Report(context.Context, string, error) { e.reportCalls++ }

type correctionContentFake struct {
	f         *correctionFixture
	calls     int
	createdAt time.Time
}

func (c *correctionContentFake) StoreExamResourceRendition(_ context.Context, revisionID model.FileRevisionID, renditionID model.FileRenditionID, _ model.ExamResourceMediaType, _ io.Reader, _ int64, at time.Time) (model.FileRendition, error) {
	c.f.order = append(c.f.order, "content.store")
	c.calls++
	c.createdAt = at
	return *correctionRendition(renditionID, revisionID, at), nil
}
