// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package correction

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/server/app/apperror"
	"github.com/sudosylabs/proctor/server/app/invocation"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

func TestStageResourceContentReservesBeforeDeterministicWriteAndReturnsAuthoritativeMetadata(t *testing.T) {
	t.Parallel()
	f := newCorrectionFixture(t)
	sha := strings.Repeat("a", 64)
	got, err := f.service.StageExamSittingCorrectionResourceContent(context.Background(), f.invocation, StageResourceContentCommand{
		ExamID: f.examID, SittingID: f.sittingID, BaseRevisionID: f.baseRevisionID,
		Target: store.ExamCorrectionResourceAddition, MediaType: model.ExamResourceMediaText,
		Body: strings.NewReader("notes"), Size: 5, ExpectedSHA256: sha, IdempotencyKey: "test-key",
	})
	require.NoError(t, err)
	require.False(t, got.StageID.IsZero())
	require.False(t, got.ResourceID.IsZero())
	require.Equal(t, model.ExamResourceMediaText, got.MediaType)
	require.Equal(t, int64(5), got.Size)
	require.Equal(t, sha, got.SHA256)
	require.False(t, got.ExpiresAt.IsZero())
	// Contract (guardrail 8): authorization precedes the audit, the audit begins
	// before the durable reservation, and bytes are stored only inside the
	// reservation before it becomes ready.
	require.Equal(t, []string{"access", "membership", "authorize", "audit.begin", "reserve", "content.store", "ready"}, f.order)
	require.NotEmpty(t, f.persistence.reservation.AuditEventID)
	require.GreaterOrEqual(t, f.persistence.reservation.AuditAt, int64(1))
	require.Equal(t, model.ActionExamSittingManage, f.authorizer.action, "authorization")
	require.Equal(t, model.Resource{Type: model.ResourceExamSitting, ID: f.sittingID.String()}, f.authorizer.resource, "authorization")
	require.False(t, f.persistence.reservation.ManagerOverride, "authorization")
	for _, forbidden := range []string{"media_type", "size", "sha256", "stage_id", "private_reason", "display_name"} {
		_, ok := f.auditor.value[forbidden]
		require.False(t, ok, "unsafe audit field %q in %#v", forbidden, f.auditor.value)
	}
	wantIdempotency, prepareErr := prepareStageIdempotency(f.invocation, StageResourceContentCommand{
		ExamID: f.examID, SittingID: f.sittingID, BaseRevisionID: f.baseRevisionID,
		Target: store.ExamCorrectionResourceAddition, MediaType: model.ExamResourceMediaText,
		Size: 5, ExpectedSHA256: sha, IdempotencyKey: "test-key",
	})
	require.NoError(t, prepareErr)
	require.Equal(t, wantIdempotency, f.persistence.idempotency)
}

func TestStageResourceContentReadyReplaySkipsContentWrite(t *testing.T) {
	t.Parallel()
	f := newCorrectionFixture(t)
	f.persistence.returnReady = true
	_, err := f.service.StageExamSittingCorrectionResourceContent(context.Background(), f.invocation, StageResourceContentCommand{ExamID: f.examID, SittingID: f.sittingID, BaseRevisionID: f.baseRevisionID, Target: store.ExamCorrectionResourceAddition, MediaType: model.ExamResourceMediaText, Body: strings.NewReader("notes"), Size: 5, ExpectedSHA256: strings.Repeat("a", 64), IdempotencyKey: "test-key"})
	require.NoError(t, err)
	require.Equal(t, 0, f.content.calls, "content")
	require.Equal(t, 0, f.persistence.readyCalls, "content")
}

type correctionCapacityError struct{}

func (correctionCapacityError) Error() string         { return "content capacity is unavailable" }
func (correctionCapacityError) WorkCapacityExceeded() {}

func TestStageResourceContentCapacityRefusalLeavesOnlyPendingReservation(t *testing.T) {
	t.Parallel()
	f := newCorrectionFixture(t)
	f.content.err = correctionCapacityError{}
	_, err := f.service.StageExamSittingCorrectionResourceContent(context.Background(), f.invocation, StageResourceContentCommand{
		ExamID: f.examID, SittingID: f.sittingID, BaseRevisionID: f.baseRevisionID,
		Target: store.ExamCorrectionResourceAddition, MediaType: model.ExamResourceMediaText,
		Body: strings.NewReader("notes"), Size: 5, ExpectedSHA256: strings.Repeat("a", 64), IdempotencyKey: "test-key",
	})
	var fault *apperror.Error
	require.ErrorAs(t, err, &fault)
	require.Equal(t, "service.busy", fault.Code())
	require.ErrorIs(t, err, f.content.err)
	require.NotNil(t, f.persistence.reservation, "refused content became ready or published: store=%#v effects", f.persistence)
	require.Equal(t, 0, f.persistence.readyCalls, "refused content became ready or published: store=%#v effects", f.persistence)
	require.Nil(t, f.persistence.application, "refused content became ready or published: store=%#v effects", f.persistence)
	require.Equal(t, 0, f.effects.calls, "refused content became ready or published: store=%#v effects", f.persistence)
	// Contract (guardrail 8): the audit begins before the durable reservation that
	// the refused bytes were stored under.
	require.Equal(t, []string{"access", "membership", "authorize", "audit.begin", "reserve", "content.store"}, f.order)
}

func TestStageResourceContentUsesReservedTimeForConcurrentReplay(t *testing.T) {
	t.Parallel()
	f := newCorrectionFixture(t)
	f.persistence.stageCreatedAt = f.at.Add(-time.Minute)
	_, err := f.service.StageExamSittingCorrectionResourceContent(context.Background(), f.invocation, StageResourceContentCommand{
		ExamID: f.examID, SittingID: f.sittingID, BaseRevisionID: f.baseRevisionID,
		Target: store.ExamCorrectionResourceAddition, MediaType: model.ExamResourceMediaText,
		Body: strings.NewReader("notes"), Size: 5, ExpectedSHA256: strings.Repeat("a", 64), IdempotencyKey: "test-key",
	})
	require.NoError(t, err)
	require.True(t, f.content.createdAt.Equal(f.persistence.stageCreatedAt), "rendition created_at=%s want reserved", f.content.createdAt)
}

func TestStageResourceContentUsesExplicitOverrideWithoutCurrentUnitMembership(t *testing.T) {
	t.Parallel()
	f := newCorrectionFixture(t)
	f.memberships.empty = true
	_, err := f.service.StageExamSittingCorrectionResourceContent(context.Background(), f.invocation, StageResourceContentCommand{ExamID: f.examID, SittingID: f.sittingID, BaseRevisionID: f.baseRevisionID, Target: store.ExamCorrectionResourceAddition, MediaType: model.ExamResourceMediaText, Body: strings.NewReader("notes"), Size: 5, ExpectedSHA256: strings.Repeat("a", 64), IdempotencyKey: "test-key"})
	require.NoError(t, err)
	require.Equal(t, model.ActionExamSittingManageOverride, f.authorizer.action, "action=%s reservation=%#v", f.authorizer.action, f.persistence.reservation)
	require.True(t, f.persistence.reservation.ManagerOverride, "action=%s reservation=%#v", f.authorizer.action, f.persistence.reservation)
}

func TestStageResourceContentStopsWhenCurrentManagerMembershipCannotBeRead(t *testing.T) {
	t.Parallel()
	f := newCorrectionFixture(t)
	failure := errors.New("membership unavailable")
	f.memberships.err = failure
	_, err := f.service.StageExamSittingCorrectionResourceContent(context.Background(), f.invocation, StageResourceContentCommand{ExamID: f.examID, SittingID: f.sittingID, BaseRevisionID: f.baseRevisionID, Target: store.ExamCorrectionResourceAddition, MediaType: model.ExamResourceMediaText, Body: strings.NewReader("notes"), Size: 5, ExpectedSHA256: strings.Repeat("a", 64), IdempotencyKey: "test-key"})
	var fault *apperror.Error
	require.ErrorAs(t, err, &fault)
	require.Equal(t, "exam.sitting.correction.unavailable", fault.Code())
	require.ErrorIs(t, err, failure)
	// Without current membership nothing is authorized or reserved.
	require.Empty(t, f.authorizer.action, "authorized without current membership")
	require.Nil(t, f.persistence.reservation, "reserved without current membership")
}

func TestStageReplacementPinsFileEntryFromExactBaseRevision(t *testing.T) {
	t.Parallel()
	f := newCorrectionFixture(t)
	resourceID, entryID := model.NewExamResourceID(), model.NewFileEntryID()
	f.revisions.snapshot = &model.ExamRevision{ID: f.baseRevisionID, ExamID: f.examID, Resources: []model.ExamRevisionResource{{ResourceID: resourceID, FileEntryID: entryID}}}
	_, err := f.service.StageExamSittingCorrectionResourceContent(context.Background(), f.invocation, StageResourceContentCommand{ExamID: f.examID, SittingID: f.sittingID, BaseRevisionID: f.baseRevisionID, Target: store.ExamCorrectionResourceReplacement, ResourceID: resourceID, MediaType: model.ExamResourceMediaText, Body: strings.NewReader("notes"), Size: 5, ExpectedSHA256: strings.Repeat("a", 64), IdempotencyKey: "test-key"})
	require.NoError(t, err)
	require.Nil(t, f.persistence.reservation.Entry)
	require.Equal(t, entryID, f.persistence.reservation.FileEntryID)
	require.Equal(t, resourceID, f.persistence.reservation.ResourceID)
}

func TestApplyUsesOneAtomicStoreCommandAndSuppressesReplayEffects(t *testing.T) {
	t.Parallel()
	f := newCorrectionFixture(t)
	f.persistence.applyReplayed = true
	result, err := f.service.ApplyExamSittingCorrection(context.Background(), f.invocation, ApplyCommand{ExamID: f.examID, SittingID: f.sittingID, ExpectedSittingRevision: 3, ExpectedCurrentRevisionID: f.baseRevisionID, Instructions: OptionalInstructions{Present: true, Markdown: "Updated"}, Resources: []ResourceManifestItem{}, AffectedCapabilities: []model.CandidateCapability{model.CandidateCapabilityBrowser, model.CandidateCapabilitySubmission, model.CandidateCapabilityWorkspace}, CandidateSummary: "The instructions were corrected.", AcknowledgementRequired: true, PrivateReason: "Correct a discovered ambiguity", IdempotencyKey: "test-key"})
	require.NoError(t, err)
	require.Equal(t, f.baseRevisionID, result.PreviousRevisionID)
	require.False(t, result.RevisionID.IsZero())
	require.Equal(t, int64(4), result.SittingRevision)
	require.True(t, result.EffectiveAt.Equal(f.at.Add(-time.Minute)))
	require.Equal(t, 0, f.effects.calls)
	require.NotNil(t, f.persistence.application)
	require.NotNil(t, f.persistence.application.InstructionsMarkdown)
	require.Equal(t, "Updated", *f.persistence.application.InstructionsMarkdown)
	require.NotEmpty(t, f.persistence.application.PrivateReason)
	require.Equal(t, 0, f.effects.calls, "replay effects")
	for _, forbidden := range []string{"instructions_markdown", "resources", "private_reason", "stage_id", "sha256"} {
		_, ok := f.auditor.value[forbidden]
		require.False(t, ok, "unsafe audit field %q", forbidden)
	}
	wantIdempotency, prepareErr := prepareApplyIdempotency(f.invocation, ApplyCommand{ExamID: f.examID, SittingID: f.sittingID,
		ExpectedSittingRevision: 3, ExpectedCurrentRevisionID: f.baseRevisionID,
		Instructions: OptionalInstructions{Present: true, Markdown: "Updated"}, Resources: []ResourceManifestItem{},
		AffectedCapabilities: []model.CandidateCapability{model.CandidateCapabilityBrowser, model.CandidateCapabilitySubmission, model.CandidateCapabilityWorkspace}, CandidateSummary: "The instructions were corrected.", AcknowledgementRequired: true,
		PrivateReason: "Correct a discovered ambiguity", IdempotencyKey: "test-key"})
	require.NoError(t, prepareErr)
	require.Equal(t, wantIdempotency, f.persistence.idempotency)
}

func TestApplyPublishesOnlyAfterCommitAndReportsTransientFailure(t *testing.T) {
	t.Parallel()
	f := newCorrectionFixture(t)
	f.effects.err = errors.New("realtime unavailable")
	_, err := f.service.ApplyExamSittingCorrection(context.Background(), f.invocation, ApplyCommand{ExamID: f.examID, SittingID: f.sittingID, ExpectedSittingRevision: 3, ExpectedCurrentRevisionID: f.baseRevisionID, Instructions: OptionalInstructions{Present: true, Markdown: "Updated"}, Resources: []ResourceManifestItem{}, AffectedCapabilities: []model.CandidateCapability{model.CandidateCapabilityBrowser, model.CandidateCapabilitySubmission, model.CandidateCapabilityWorkspace}, CandidateSummary: "The instructions were corrected.", PrivateReason: "Correct a discovered ambiguity", IdempotencyKey: "test-key"})
	require.NoError(t, err)
	require.NotNil(t, f.persistence.application, "application=%#v effects=%#v order=%v", f.persistence.application, f.effects, f.order)
	require.Equal(t, 1, f.effects.calls, "application=%#v effects=%#v order=%v", f.persistence.application, f.effects, f.order)
	require.Equal(t, 1, f.effects.reportCalls, "application=%#v effects=%#v order=%v", f.persistence.application, f.effects, f.order)
	// Contract (guardrails 8 and 14): authorization precedes the audit, the audit
	// begins before the durable write, and the write commits before its effect.
	require.Equal(t, []string{"access", "membership", "authorize", "audit.begin", "apply", "effect"}, f.order)
}

func TestApplyRejectsNonCanonicalPrivateReasonBeforeAuthorization(t *testing.T) {
	t.Parallel()
	f := newCorrectionFixture(t)
	_, err := f.service.ApplyExamSittingCorrection(context.Background(), f.invocation, ApplyCommand{ExamID: f.examID, SittingID: f.sittingID, ExpectedSittingRevision: 3, ExpectedCurrentRevisionID: f.baseRevisionID, PrivateReason: " padded ", IdempotencyKey: "test-key"})
	var fault *apperror.Error
	require.ErrorAs(t, err, &fault)
	require.Equal(t, "exam.sitting.correction.invalid", fault.Code())
	require.Empty(t, f.order)
}

type correctionFixture struct {
	service        *Service
	invocation     invocation.Invocation
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
	service, err := New(Dependencies{
		Persistence:       f.persistence,
		Revisions:         f.revisions,
		Access:            f.access,
		Memberships:       f.memberships,
		Authorizer:        f.authorizer,
		Auditor:           f.auditor,
		Effects:           f.effects,
		EffectFailures:    f.effects,
		Content:           f.content,
		Now:               func() time.Time { return f.at },
		NewStageID:        model.NewExamCorrectionResourceStageID,
		NewResourceID:     model.NewExamResourceID,
		NewEntryID:        model.NewFileEntryID,
		NewFileRevisionID: model.NewFileRevisionID,
		NewLeaseID:        model.NewUploadLeaseID,
		NewRenditionID:    model.NewFileRenditionID,
		NewExamRevisionID: model.NewExamRevisionID,
		InstitutionOrigin: "https://institution.example",
	})
	require.NoError(t, err)
	f.service = service
	f.invocation = invocation.New(model.Principal{UserID: f.userID, SessionID: model.NewSessionID(), CredentialID: model.PrincipalCredentialID(model.NewID()), CredentialType: model.CredentialSessionAccess, AuthenticationMethod: "password", AuthenticationStrength: model.AuthenticationSingleFactor, ClientType: model.SessionClientWeb, AuthenticatedAt: f.at}, model.RequestMetadata{})
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
func (s *correctionStoreFake) MarkResourceStageReady(_ context.Context, in *store.ExamCorrectionResourceStageReadiness) (*store.ExamCorrectionResourceStage, error) {
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
	err   error
}

func (m *correctionMembershipFake) ListActiveByUser(context.Context, model.UserID, time.Time) ([]*model.AcademicUnitMember, error) {
	m.f.order = append(m.f.order, "membership")
	if m.empty {
		return nil, m.err
	}
	return []*model.AcademicUnitMember{{AcademicUnitID: m.f.unitID, UserID: m.f.userID}}, m.err
}

type correctionAuthorizerFake struct {
	f        *correctionFixture
	action   model.Action
	resource model.Resource
}

func (a *correctionAuthorizerFake) Authorize(_ context.Context, _ invocation.Invocation, action model.Action, resource model.Resource) error {
	a.f.order = append(a.f.order, "authorize")
	a.action, a.resource = action, resource
	return nil
}

type correctionAuditFake struct {
	f     *correctionFixture
	value map[string]any
}

func (a *correctionAuditFake) BeginAtScope(_ context.Context, _ invocation.Invocation, _ model.Action, _ model.Resource, _ model.RoleScopeType, _, _ string, value, _ map[string]any) (model.AuditEventID, error) {
	a.f.order = append(a.f.order, "audit.begin")
	a.value = value
	return model.NewAuditEventID(), nil
}
func (a *correctionAuditFake) Fail(context.Context, model.AuditEventID, string) error {
	a.f.order = append(a.f.order, "audit.fail")
	return nil
}

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
	err       error
}

func (c *correctionContentFake) StoreExamResourceRendition(_ context.Context, revisionID model.FileRevisionID, renditionID model.FileRenditionID, _ model.ExamResourceMediaType, _ io.Reader, _ int64, at time.Time) (model.FileRendition, error) {
	c.f.order = append(c.f.order, "content.store")
	c.calls++
	c.createdAt = at
	if c.err != nil {
		return model.FileRendition{}, c.err
	}
	return *correctionRendition(renditionID, revisionID, at), nil
}
