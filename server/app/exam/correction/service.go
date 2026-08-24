// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package correction

import (
	"context"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"io"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

type Call struct {
	principal model.Principal
	metadata  model.RequestMetadata
}

func NewCall(p model.Principal, m model.RequestMetadata) Call {
	p.CredentialScopes = append([]string(nil), p.CredentialScopes...)
	return Call{p, m}
}
func (c Call) Principal() model.Principal {
	p := c.principal
	p.CredentialScopes = append([]string(nil), p.CredentialScopes...)
	return p
}
func (c Call) RequestMetadata() model.RequestMetadata { return c.metadata }

type Fault struct {
	Code       string
	SafeFields map[string]any
	Cause      error
}

func (f *Fault) Error() string {
	if f == nil {
		return "exam correction fault"
	}
	return f.Code
}
func (f *Fault) Unwrap() error {
	if f == nil {
		return nil
	}
	return f.Cause
}

type AccessStore interface {
	Access(context.Context, model.ExamID, model.UserID) (*store.ExamAccessSnapshot, error)
}
type Revisions interface {
	GetSnapshot(context.Context, model.ExamID, model.ExamRevisionID) (*model.ExamRevision, error)
}
type Memberships interface {
	ListActiveByUser(context.Context, string, int64) ([]*model.AcademicUnitMember, error)
}
type Authorizer interface {
	Authorize(context.Context, Call, model.Action, model.Resource) error
}
type Auditor interface {
	Begin(context.Context, Call, model.Action, model.Resource, model.RoleScopeType, string, string, map[string]any, map[string]any) (string, error)
	Fail(context.Context, string, string) error
}
type Effects interface {
	Corrected(context.Context, Result) error
}
type EffectFailures interface {
	Report(context.Context, string, error)
}
type Content interface {
	StoreExamResourceRendition(context.Context, model.FileRevisionID, model.FileRenditionID, model.ExamResourceMediaType, io.Reader, int64, time.Time) (model.FileRendition, error)
}
type invalidContentError interface{ InvalidExamResourceContent() }

type StageResourceContentCommand struct {
	ExamID         model.ExamID
	SittingID      model.ExamSittingID
	BaseRevisionID model.ExamRevisionID
	Target         store.ExamCorrectionResourceStageTarget
	ResourceID     model.ExamResourceID
	MediaType      model.ExamResourceMediaType
	Body           io.Reader
	Size           int64
	ExpectedSHA256 string
	IdempotencyKey string
}
type ResourceStage struct {
	StageID    model.ExamCorrectionResourceStageID
	ResourceID model.ExamResourceID
	MediaType  model.ExamResourceMediaType
	Size       int64
	SHA256     string
	ExpiresAt  time.Time
}
type OptionalInstructions struct {
	Present  bool
	Markdown string
}
type ResourceManifestItem struct {
	ResourceID          model.ExamResourceID
	DisplayName         string
	DescriptionMarkdown string
	StageID             model.ExamCorrectionResourceStageID
}
type ApplyCommand struct {
	ExamID                    model.ExamID
	SittingID                 model.ExamSittingID
	ExpectedSittingRevision   int64
	ExpectedCurrentRevisionID model.ExamRevisionID
	Instructions              OptionalInstructions
	Resources                 []ResourceManifestItem
	PrivateReason             string
	IdempotencyKey            string
}
type Result struct {
	ExamID             model.ExamID
	SittingID          model.ExamSittingID
	PreviousRevisionID model.ExamRevisionID
	RevisionID         model.ExamRevisionID
	RevisionNumber     int64
	SittingState       model.ExamSittingState
	SittingRevision    int64
	EffectiveAt        time.Time
	Replayed           bool
}

type Service struct {
	persistence       store.ExamCorrectionStore
	revisions         Revisions
	access            AccessStore
	memberships       Memberships
	authorizer        Authorizer
	auditor           Auditor
	effects           Effects
	failures          EffectFailures
	content           Content
	now               func() time.Time
	newStageID        func() model.ExamCorrectionResourceStageID
	newResourceID     func() model.ExamResourceID
	newEntryID        func() model.FileEntryID
	newFileRevisionID func() model.FileRevisionID
	newLeaseID        func() model.UploadLeaseID
	newRenditionID    func() model.FileRenditionID
	newExamRevisionID func() model.ExamRevisionID
}

func New(p store.ExamCorrectionStore, revisions Revisions, access AccessStore, memberships Memberships, authorizer Authorizer, auditor Auditor, effects Effects, failures EffectFailures, content Content, now func() time.Time, newStageID func() model.ExamCorrectionResourceStageID, newResourceID func() model.ExamResourceID, newEntryID func() model.FileEntryID, newFileRevisionID func() model.FileRevisionID, newLeaseID func() model.UploadLeaseID, newRenditionID func() model.FileRenditionID, newExamRevisionID func() model.ExamRevisionID) (*Service, error) {
	if p == nil || revisions == nil || access == nil || memberships == nil || authorizer == nil || auditor == nil || effects == nil || failures == nil || content == nil || now == nil || newStageID == nil || newResourceID == nil || newEntryID == nil || newFileRevisionID == nil || newLeaseID == nil || newRenditionID == nil || newExamRevisionID == nil {
		return nil, errors.New("exam correction dependencies are required")
	}
	return &Service{
		persistence: p, revisions: revisions, access: access, memberships: memberships,
		authorizer: authorizer, auditor: auditor, effects: effects, failures: failures, content: content, now: now,
		newStageID: newStageID, newResourceID: newResourceID, newEntryID: newEntryID,
		newFileRevisionID: newFileRevisionID, newLeaseID: newLeaseID, newRenditionID: newRenditionID,
		newExamRevisionID: newExamRevisionID,
	}, nil
}

func (s *Service) StageResourceContent(ctx context.Context, call Call, c StageResourceContentCommand) (ResourceStage, error) {
	if !c.ExamID.IsValid() || !c.SittingID.IsValid() || !c.BaseRevisionID.IsValid() || !c.Target.IsValid() || !c.MediaType.IsValid() || c.Body == nil || c.Size < 0 || c.Size > model.ExamResourceMaximumBytes || !validSHA256(c.ExpectedSHA256) || (c.Target == store.ExamCorrectionResourceAddition && !c.ResourceID.IsZero()) || (c.Target == store.ExamCorrectionResourceReplacement && !c.ResourceID.IsValid()) {
		return ResourceStage{}, invalid("upload")
	}
	idempotency, err := prepareStageIdempotency(call, c)
	if err != nil {
		return ResourceStage{}, err
	}
	auth, err := s.authorize(ctx, call, c.ExamID, c.SittingID)
	if err != nil {
		return ResourceStage{}, err
	}
	at := model.TimeUTC(s.now())
	principal := call.Principal()
	resourceID := c.ResourceID
	entryID := model.FileEntryID("")
	var entry *model.FileEntry
	if c.Target == store.ExamCorrectionResourceAddition {
		resourceID = s.newResourceID()
		entryID = s.newEntryID()
		entry, err = model.NewFileEntryForPurpose(entryID, model.FilePurposeExamResource, model.FileIndexingNone, at)
		if err != nil {
			return ResourceStage{}, invalidCause("file_entry", err)
		}
	} else {
		var base *model.ExamRevision
		base, err = s.revisions.GetSnapshot(ctx, c.ExamID, c.BaseRevisionID)
		if err != nil {
			return ResourceStage{}, mapStore(err)
		}
		if base == nil || base.ID != c.BaseRevisionID || base.ExamID != c.ExamID {
			return ResourceStage{}, unavailable(errors.New("missing base revision"))
		}
		found := false
		for _, r := range base.Resources {
			if r.ResourceID == resourceID {
				entryID = r.FileEntryID
				found = true
				break
			}
		}
		if !found {
			return ResourceStage{}, &Fault{Code: "exam.sitting.correction.not_found"}
		}
	}
	revisionID, leaseID, renditionID := s.newFileRevisionID(), s.newLeaseID(), s.newRenditionID()
	revision, e := model.NewFileRevision(revisionID, entryID, model.FileAvailabilityPending, model.FileIndexingNotRequired, at)
	if e != nil {
		return ResourceStage{}, invalidCause("file_revision", e)
	}
	lease, e := model.NewUploadLease(leaseID, revisionID, principal.UserID, at, at.Add(model.UploadLeaseMaximumLifetime))
	if e != nil {
		return ResourceStage{}, invalidCause("upload_lease", e)
	}
	auditID, err := s.auditor.Begin(ctx, call, auth.action, model.Resource{Type: model.ResourceExamSitting, ID: c.SittingID.String()}, model.RoleScopeAcademicUnit, auth.academicUnitID.String(), "exam_sitting_correction_resource_stage_reserve", map[string]any{
		"exam_id": c.ExamID.String(), "exam_sitting_id": c.SittingID.String(), "base_revision_id": c.BaseRevisionID.String(), "target": string(c.Target),
	}, nil)
	if err != nil {
		return ResourceStage{}, err
	}
	stage, err := s.persistence.ReserveResourceStage(ctx, &store.ExamCorrectionResourceStageReservation{StageID: s.newStageID(), ExamID: c.ExamID, SittingID: c.SittingID, BaseRevisionID: c.BaseRevisionID, Target: c.Target, ResourceID: resourceID, Entry: entry, FileEntryID: entryID, Revision: revision, Lease: lease, RenditionID: renditionID, ActorUserID: principal.UserID, ManagerOverride: auth.override, CreatedAt: at, AuditEventID: auditID, AuditAt: model.MillisFromTime(at)}, idempotency)
	if err != nil {
		return ResourceStage{}, s.failAudit(ctx, auditID, err)
	}
	if stage == nil {
		return ResourceStage{}, unavailable(errors.New("missing stage outcome"))
	}
	if err = validateStage(stage, c, principal.UserID); err != nil {
		return ResourceStage{}, err
	}
	if stage.State == store.ExamCorrectionResourceStagePending {
		rendition, writeErr := s.content.StoreExamResourceRendition(ctx, stage.FileRevisionID, stage.RenditionID, c.MediaType, c.Body, c.Size, stage.CreatedAt)
		if writeErr != nil {
			return ResourceStage{}, mapContent(writeErr)
		}
		if subtle.ConstantTimeCompare([]byte(rendition.SHA256), []byte(c.ExpectedSHA256)) != 1 {
			return ResourceStage{}, &Fault{Code: "exam.sitting.correction.invalid_content", Cause: errors.New("content checksum mismatch")}
		}
		stage, err = s.persistence.MarkResourceStageReady(ctx, &store.ExamCorrectionResourceStageReadyInput{StageID: stage.ID, ActorUserID: principal.UserID, Rendition: &rendition, ReadyAt: model.TimeUTC(s.now())})
		if err != nil {
			return ResourceStage{}, mapStore(err)
		}
		if err = validateStage(stage, c, principal.UserID); err != nil {
			return ResourceStage{}, err
		}
	}
	if stage.State != store.ExamCorrectionResourceStageReady && stage.State != store.ExamCorrectionResourceStageConsumed {
		return ResourceStage{}, unavailable(errors.New("stage did not become ready"))
	}
	return projectStage(stage), nil
}

func validateStage(stage *store.ExamCorrectionResourceStage, c StageResourceContentCommand, actor model.UserID) error {
	if stage == nil || !stage.ID.IsValid() || stage.ExamID != c.ExamID || stage.SittingID != c.SittingID || stage.BaseRevisionID != c.BaseRevisionID || stage.Target != c.Target || !stage.ResourceID.IsValid() || !stage.FileEntryID.IsValid() || !stage.FileRevisionID.IsValid() || !stage.UploadLeaseID.IsValid() || !stage.RenditionID.IsValid() || stage.CreatedByUserID != actor || stage.ExpiresAt.IsZero() || (c.Target == store.ExamCorrectionResourceReplacement && stage.ResourceID != c.ResourceID) {
		return unavailable(errors.New("inconsistent stage outcome"))
	}
	if stage.State != store.ExamCorrectionResourceStagePending && stage.State != store.ExamCorrectionResourceStageReady && stage.State != store.ExamCorrectionResourceStageConsumed {
		return unavailable(errors.New("invalid stage state"))
	}
	if stage.State != store.ExamCorrectionResourceStagePending && (stage.Rendition == nil || stage.Rendition.ID != stage.RenditionID || stage.Rendition.RevisionID != stage.FileRevisionID || stage.Rendition.Validate() != nil) {
		return unavailable(errors.New("incomplete ready stage"))
	}
	if stage.Rendition != nil && (model.ExamResourceMediaType(stage.Rendition.MediaType) != c.MediaType || stage.Rendition.Size != c.Size || subtle.ConstantTimeCompare([]byte(stage.Rendition.SHA256), []byte(c.ExpectedSHA256)) != 1) {
		return unavailable(errors.New("stage metadata differs from command"))
	}
	return nil
}

func projectStage(stage *store.ExamCorrectionResourceStage) ResourceStage {
	return ResourceStage{StageID: stage.ID, ResourceID: stage.ResourceID, MediaType: model.ExamResourceMediaType(stage.Rendition.MediaType), Size: stage.Rendition.Size, SHA256: stage.Rendition.SHA256, ExpiresAt: stage.ExpiresAt}
}

func (s *Service) Apply(ctx context.Context, call Call, c ApplyCommand) (Result, error) {
	if err := validateApply(c); err != nil {
		return Result{}, err
	}
	c.Resources = append([]ResourceManifestItem(nil), c.Resources...)
	idempotency, err := prepareApplyIdempotency(call, c)
	if err != nil {
		return Result{}, err
	}
	auth, err := s.authorize(ctx, call, c.ExamID, c.SittingID)
	if err != nil {
		return Result{}, err
	}
	at := model.TimeUTC(s.now())
	value := map[string]any{"exam_id": c.ExamID.String(), "exam_sitting_id": c.SittingID.String(), "expected_sitting_revision": c.ExpectedSittingRevision, "expected_current_revision_id": c.ExpectedCurrentRevisionID.String(), "instructions_present": c.Instructions.Present, "resource_count": len(c.Resources)}
	auditID, err := s.auditor.Begin(ctx, call, auth.action, model.Resource{Type: model.ResourceExamSitting, ID: c.SittingID.String()}, model.RoleScopeAcademicUnit, auth.academicUnitID.String(), "exam_sitting_content_correct", value, nil)
	if err != nil {
		return Result{}, err
	}
	var instructions *string
	if c.Instructions.Present {
		v := c.Instructions.Markdown
		instructions = &v
	}
	resources := make([]store.ExamCorrectionResourceManifestItem, len(c.Resources))
	for i, r := range c.Resources {
		resources[i] = store.ExamCorrectionResourceManifestItem{ResourceID: r.ResourceID, DisplayName: strings.TrimSpace(r.DisplayName), DescriptionMarkdown: r.DescriptionMarkdown, StageID: r.StageID}
	}
	stored, err := s.persistence.Apply(ctx, &store.ExamCorrectionApplication{RevisionID: s.newExamRevisionID(), ExamID: c.ExamID, SittingID: c.SittingID, CurrentRevisionID: c.ExpectedCurrentRevisionID, ExpectedSittingRevision: c.ExpectedSittingRevision, ActorUserID: call.Principal().UserID, ManagerOverride: auth.override, InstructionsMarkdown: instructions, Resources: resources, PrivateReason: c.PrivateReason, AppliedAt: at, AuditEventID: auditID, AuditAt: model.MillisFromTime(at)}, idempotency)
	if err != nil {
		return Result{}, s.failAudit(ctx, auditID, err)
	}
	result, err := projectResult(stored, c)
	if err != nil {
		return Result{}, err
	}
	if !result.Replayed {
		if effectErr := s.effects.Corrected(ctx, result); effectErr != nil {
			s.failures.Report(ctx, "exam_sitting_content_corrected", effectErr)
		}
	}
	return result, nil
}

func validateApply(c ApplyCommand) error {
	if !c.ExamID.IsValid() || !c.SittingID.IsValid() || !c.ExpectedCurrentRevisionID.IsValid() || c.ExpectedSittingRevision < 1 || len(c.Resources) > model.ExamResourceMaximumCount || !utf8.ValidString(c.PrivateReason) || strings.TrimSpace(c.PrivateReason) == "" || strings.TrimSpace(c.PrivateReason) != c.PrivateReason || utf8.RuneCountInString(c.PrivateReason) > 1000 || len(c.PrivateReason) > 4000 {
		return invalid("command")
	}
	if !c.Instructions.Present && c.Instructions.Markdown != "" {
		return invalid("instructions_markdown")
	}
	if c.Instructions.Present && (!utf8.ValidString(c.Instructions.Markdown) || len(c.Instructions.Markdown) > model.ExamInstructionsMarkdownMaxBytes) {
		return invalid("instructions_markdown")
	}
	resourceIDs := map[model.ExamResourceID]struct{}{}
	stageIDs := map[model.ExamCorrectionResourceStageID]struct{}{}
	for _, r := range c.Resources {
		if !r.ResourceID.IsValid() || strings.TrimSpace(r.DisplayName) != r.DisplayName || r.DisplayName == "" || !utf8.ValidString(r.DisplayName) || utf8.RuneCountInString(r.DisplayName) > model.ExamResourceDisplayNameMaxRunes || !utf8.ValidString(r.DescriptionMarkdown) || len(r.DescriptionMarkdown) > model.ExamResourceDescriptionMaxBytes {
			return invalid("resources")
		}
		if _, ok := resourceIDs[r.ResourceID]; ok {
			return invalid("resources")
		}
		resourceIDs[r.ResourceID] = struct{}{}
		if !r.StageID.IsZero() && !r.StageID.IsValid() {
			return invalid("resources")
		}
		if r.StageID.IsValid() {
			if _, ok := stageIDs[r.StageID]; ok {
				return invalid("resources")
			}
			stageIDs[r.StageID] = struct{}{}
		}
	}
	return nil
}

type authorizationDecision struct {
	override       bool
	academicUnitID model.AcademicUnitID
	action         model.Action
}

func (s *Service) authorize(ctx context.Context, call Call, examID model.ExamID, sittingID model.ExamSittingID) (authorizationDecision, error) {
	p := call.Principal()
	if p.Validate() != nil {
		return authorizationDecision{}, invalid("principal")
	}
	access, err := s.access.Access(ctx, examID, p.UserID)
	if err != nil {
		return authorizationDecision{}, mapStore(err)
	}
	if access == nil || access.Exam == nil || access.Exam.ID != examID {
		return authorizationDecision{}, unavailable(errors.New("missing access"))
	}
	ordinary := false
	if access.ActorIsManager {
		members, e := s.memberships.ListActiveByUser(ctx, p.UserID.String(), model.MillisFromTime(s.now()))
		if e != nil {
			return authorizationDecision{}, unavailable(e)
		}
		for _, m := range members {
			if m != nil && m.AcademicUnitID == access.Exam.AcademicUnitID {
				ordinary = true
				break
			}
		}
	}
	action := model.ActionExamSittingManage
	if !ordinary {
		action = model.ActionExamSittingManageOverride
	}
	if err = s.authorizer.Authorize(ctx, call, action, model.Resource{Type: model.ResourceExamSitting, ID: sittingID.String()}); err != nil {
		return authorizationDecision{}, err
	}
	return authorizationDecision{!ordinary, access.Exam.AcademicUnitID, action}, nil
}
func (s *Service) failAudit(ctx context.Context, id string, err error) error {
	mapped := mapStore(err)
	code := "exam.sitting.correction.unavailable"
	var f *Fault
	if errors.As(mapped, &f) {
		code = f.Code
	}
	if auditErr := s.auditor.Fail(ctx, id, code); auditErr != nil {
		return auditErr
	}
	return mapped
}
func projectResult(stored *store.ExamCorrectionResult, c ApplyCommand) (Result, error) {
	if stored == nil || stored.Revision == nil || stored.Sitting == nil || stored.Sitting.Sitting == nil {
		return Result{}, unavailable(errors.New("missing correction outcome"))
	}
	s := stored.Sitting.Sitting
	if stored.Revision.ExamID != c.ExamID || stored.Revision.ID == c.ExpectedCurrentRevisionID || stored.Revision.Kind != model.ExamRevisionPublicationLiveCorrection || stored.Revision.Number < 1 ||
		stored.PreviousRevisionID != c.ExpectedCurrentRevisionID || s.ID != c.SittingID || s.ExamID != c.ExamID ||
		s.ExamRevisionID != stored.Revision.ID || s.Revision != c.ExpectedSittingRevision+1 ||
		(s.State != model.ExamSittingOpen && s.State != model.ExamSittingPaused) || s.UpdatedAt.IsZero() ||
		stored.EffectiveAt.IsZero() || !model.TimeUTC(stored.EffectiveAt).Equal(model.TimeUTC(stored.Revision.PublishedAt)) {
		return Result{}, unavailable(errors.New("inconsistent correction outcome"))
	}
	return Result{ExamID: c.ExamID, SittingID: c.SittingID, PreviousRevisionID: stored.PreviousRevisionID, RevisionID: stored.Revision.ID, RevisionNumber: stored.Revision.Number, SittingState: s.State, SittingRevision: s.Revision, EffectiveAt: model.TimeUTC(stored.EffectiveAt), Replayed: stored.Replayed}, nil
}
func invalid(field string) error {
	return &Fault{Code: "exam.sitting.correction.invalid", SafeFields: map[string]any{"field": field}}
}
func invalidCause(field string, cause error) error {
	return &Fault{Code: "exam.sitting.correction.invalid", SafeFields: map[string]any{"field": field}, Cause: cause}
}
func unavailable(cause error) error {
	return &Fault{Code: "exam.sitting.correction.unavailable", Cause: cause}
}
func mapContent(err error) error {
	var invalidContent invalidContentError
	if errors.As(err, &invalidContent) {
		return &Fault{Code: "exam.sitting.correction.invalid_content", Cause: err}
	}
	return unavailable(err)
}
func mapStore(err error) error {
	var conflict *store.ErrConflict
	var invalidInput *store.ErrInvalidInput
	var idemConflict *store.ErrIdempotencyConflict
	var idemProgress *store.ErrIdempotencyInProgress
	switch {
	case errors.As(err, &idemConflict):
		return &Fault{Code: "idempotency.conflict", Cause: err}
	case errors.As(err, &idemProgress):
		return &Fault{Code: "idempotency.in_progress", Cause: err}
	case store.IsNotFound(err):
		return &Fault{Code: "exam.sitting.correction.not_found", Cause: err}
	case errors.As(err, &conflict):
		codes := map[string]string{
			"exam_archived":                     "exam.archived",
			"exam_sitting_revision":             "exam.sitting.revision_conflict",
			"exam_sitting_state":                "exam.sitting.state_conflict",
			"exam_sitting_revision_selection":   "exam.sitting.revision_conflict",
			"exam_sitting_deadline_reached":     "exam.sitting.deadline_reached",
			"exam_correction_base_revision":     "exam.sitting.revision_conflict",
			"exam_correction_no_changes":        "exam.sitting.correction.no_changes",
			"exam_correction_resource_limit":    "exam.resource.limit",
			"exam_correction_resource_manifest": "exam.sitting.correction.manifest_invalid",
			"exam_correction_resource_stage":    "exam.sitting.correction.stage_invalid",
		}
		if code := codes[conflict.Constraint]; code != "" {
			return &Fault{Code: code, Cause: err}
		}
		return &Fault{Code: "exam.sitting.correction.conflict", Cause: err}
	case errors.As(err, &invalidInput):
		return invalidCause("value", err)
	default:
		return unavailable(err)
	}
}
func validSHA256(value string) bool {
	if len(value) != 64 || value != strings.ToLower(value) {
		return false
	}
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == 32
}
