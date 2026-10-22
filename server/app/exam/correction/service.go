// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package correction

import (
	"context"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"io"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/sudosylabs/proctor/server/app/apperror"
	"github.com/sudosylabs/proctor/server/app/exam/manageraccess"
	"github.com/sudosylabs/proctor/server/app/invocation"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

type AccessStore interface {
	Access(context.Context, model.ExamID, model.UserID) (*store.ExamAccessSnapshot, error)
}
type Revisions interface {
	GetSnapshot(context.Context, model.ExamID, model.ExamRevisionID) (*model.ExamRevision, error)
}
type Authorizer interface {
	Authorize(context.Context, invocation.Invocation, model.Action, model.Resource) error
}
type Auditor interface {
	BeginAtScope(context.Context, invocation.Invocation, model.Action, model.Resource, model.RoleScopeType, string, string, map[string]any, map[string]any) (model.AuditEventID, error)
	Fail(context.Context, model.AuditEventID, string) error
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
type workCapacityError interface{ WorkCapacityExceeded() }

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
type OptionalBrowserPolicy struct {
	Present bool
	Policy  model.BrowserPolicy
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
	BrowserPolicy             OptionalBrowserPolicy
	Resources                 []ResourceManifestItem
	CandidateSummary          string
	AcknowledgementRequired   bool
	AffectedCapabilities      []model.CandidateCapability
	PrivateReason             string
	IdempotencyKey            string
}
type Result struct {
	ExamID                  model.ExamID
	SittingID               model.ExamSittingID
	PreviousRevisionID      model.ExamRevisionID
	RevisionID              model.ExamRevisionID
	RevisionNumber          int64
	SittingState            model.ExamSittingState
	SittingRevision         int64
	EffectiveAt             time.Time
	AcknowledgementRequired bool
	AffectedCapabilities    []model.CandidateCapability
	Replayed                bool
}

// AppliedCorrection is the public result of applying a live correction. It
// deliberately omits the replay and acknowledgement facts that only the
// after-commit effects consume.
type AppliedCorrection struct {
	ExamID               model.ExamID
	SittingID            model.ExamSittingID
	PreviousRevisionID   model.ExamRevisionID
	RevisionID           model.ExamRevisionID
	RevisionNumber       int64
	SittingState         model.ExamSittingState
	SittingRevision      int64
	EffectiveAt          time.Time
	AffectedCapabilities []model.CandidateCapability
}

func (r Result) applied() AppliedCorrection {
	return AppliedCorrection{ExamID: r.ExamID, SittingID: r.SittingID, PreviousRevisionID: r.PreviousRevisionID,
		RevisionID: r.RevisionID, RevisionNumber: r.RevisionNumber, SittingState: r.SittingState,
		SittingRevision: r.SittingRevision, EffectiveAt: r.EffectiveAt, AffectedCapabilities: slices.Clone(r.AffectedCapabilities)}
}

// Service stages Exam Resource content for, and applies, live corrections to
// an Exam Sitting.
type Service struct{ deps Dependencies }

// Dependencies are the collaborators of Service; New validates them once and the
// service retains them unchanged.
type Dependencies struct {
	Persistence       store.ExamCorrectionStore
	Revisions         Revisions
	Access            AccessStore
	Memberships       manageraccess.Memberships
	Authorizer        Authorizer
	Auditor           Auditor
	Effects           Effects
	EffectFailures    EffectFailures
	Content           Content
	Now               func() time.Time
	NewStageID        func() model.ExamCorrectionResourceStageID
	NewResourceID     func() model.ExamResourceID
	NewEntryID        func() model.FileEntryID
	NewFileRevisionID func() model.FileRevisionID
	NewLeaseID        func() model.UploadLeaseID
	NewRenditionID    func() model.FileRenditionID
	NewExamRevisionID func() model.ExamRevisionID
	InstitutionOrigin string
}

// New returns a correction Service, or an error when a required dependency is
// missing.
func New(deps Dependencies) (*Service, error) {
	if deps.Persistence == nil || deps.Revisions == nil || deps.Access == nil || deps.Memberships == nil || deps.Authorizer == nil || deps.Auditor == nil || deps.Effects == nil || deps.EffectFailures == nil || deps.Content == nil || deps.Now == nil || deps.NewStageID == nil || deps.NewResourceID == nil || deps.NewEntryID == nil || deps.NewFileRevisionID == nil || deps.NewLeaseID == nil || deps.NewRenditionID == nil || deps.NewExamRevisionID == nil {
		return nil, errors.New("exam correction dependencies are required")
	}
	return &Service{deps: deps}, nil
}

// StageExamSittingCorrectionResourceContent idempotently uploads and verifies
// Exam Resource content against its expected SHA-256 for a later correction
// of the Sitting based on BaseRevisionID. The returned stage expires at
// ExpiresAt.
func (s *Service) StageExamSittingCorrectionResourceContent(ctx context.Context, invocation invocation.Invocation, c StageResourceContentCommand) (_ ResourceStage, resultErr error) {
	defer func() { resultErr = publicFailures.Conceal(resultErr) }()
	if !c.ExamID.IsValid() || !c.SittingID.IsValid() || !c.BaseRevisionID.IsValid() || !c.Target.IsValid() || !c.MediaType.IsValid() || c.Body == nil || c.Size < 0 || c.Size > model.ExamResourceMaximumBytes || !validSHA256(c.ExpectedSHA256) || (c.Target == store.ExamCorrectionResourceAddition && !c.ResourceID.IsZero()) || (c.Target == store.ExamCorrectionResourceReplacement && !c.ResourceID.IsValid()) {
		return ResourceStage{}, apperror.Invalid("exam.sitting.correction.invalid", "upload")
	}
	idempotency, err := prepareStageIdempotency(invocation, c)
	if err != nil {
		return ResourceStage{}, err
	}
	auth, err := s.authorize(ctx, invocation, c.ExamID, c.SittingID)
	if err != nil {
		return ResourceStage{}, err
	}
	at := model.TimeUTC(s.deps.Now())
	principal := invocation.Principal()
	resourceID := c.ResourceID
	entryID := model.FileEntryID("")
	var entry *model.FileEntry
	if c.Target == store.ExamCorrectionResourceAddition {
		resourceID = s.deps.NewResourceID()
		entryID = s.deps.NewEntryID()
		entry, err = model.NewFileEntryForPurpose(entryID, model.FilePurposeExamResource, model.FileIndexingNone, at)
		if err != nil {
			return ResourceStage{}, apperror.Invalid("exam.sitting.correction.invalid", "file_entry").Wrap(err)
		}
	} else {
		var base *model.ExamRevision
		base, err = s.deps.Revisions.GetSnapshot(ctx, c.ExamID, c.BaseRevisionID)
		if err != nil {
			return ResourceStage{}, storeErrors.Translate(err)
		}
		if base == nil || base.ID != c.BaseRevisionID || base.ExamID != c.ExamID {
			return ResourceStage{}, apperror.New("exam.sitting.correction.unavailable").Wrap(errors.New("missing base revision"))
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
			return ResourceStage{}, apperror.New("exam.sitting.correction.not_found")
		}
	}
	revisionID, leaseID, renditionID := s.deps.NewFileRevisionID(), s.deps.NewLeaseID(), s.deps.NewRenditionID()
	revision, e := model.NewFileRevision(revisionID, entryID, model.FileAvailabilityPending, model.FileIndexingNotRequired, at)
	if e != nil {
		return ResourceStage{}, apperror.Invalid("exam.sitting.correction.invalid", "file_revision").Wrap(e)
	}
	lease, e := model.NewUploadLease(leaseID, revisionID, principal.UserID, at, at.Add(model.UploadLeaseMaximumLifetime))
	if e != nil {
		return ResourceStage{}, apperror.Invalid("exam.sitting.correction.invalid", "upload_lease").Wrap(e)
	}
	auditID, err := s.deps.Auditor.BeginAtScope(ctx, invocation, auth.action, model.Resource{Type: model.ResourceExamSitting, ID: c.SittingID.String()}, model.RoleScopeAcademicUnit, auth.academicUnitID.String(), "exam_sitting_correction_resource_stage_reserve", map[string]any{
		"exam_id": c.ExamID.String(), "exam_sitting_id": c.SittingID.String(), "base_revision_id": c.BaseRevisionID.String(), "target": string(c.Target),
	}, nil)
	if err != nil {
		return ResourceStage{}, err
	}
	stage, err := s.deps.Persistence.ReserveResourceStage(ctx, &store.ExamCorrectionResourceStageReservation{StageID: s.deps.NewStageID(), ExamID: c.ExamID, SittingID: c.SittingID, BaseRevisionID: c.BaseRevisionID, Target: c.Target, ResourceID: resourceID, Entry: entry, FileEntryID: entryID, Revision: revision, Lease: lease, RenditionID: renditionID, ActorUserID: principal.UserID, ManagerOverride: auth.override, CreatedAt: at, AuditEventID: auditID, AuditAt: at}, idempotency)
	if err != nil {
		return ResourceStage{}, s.failAudit(ctx, auditID, err)
	}
	if stage == nil {
		return ResourceStage{}, apperror.New("exam.sitting.correction.unavailable").Wrap(errors.New("missing stage outcome"))
	}
	if err = validateStage(stage, c, principal.UserID); err != nil {
		return ResourceStage{}, err
	}
	if stage.State == store.ExamCorrectionResourceStagePending {
		rendition, writeErr := s.deps.Content.StoreExamResourceRendition(ctx, stage.FileRevisionID, stage.RenditionID, c.MediaType, c.Body, c.Size, stage.CreatedAt)
		if writeErr != nil {
			return ResourceStage{}, mapContent(writeErr)
		}
		if subtle.ConstantTimeCompare([]byte(rendition.SHA256), []byte(c.ExpectedSHA256)) != 1 {
			return ResourceStage{}, apperror.New("exam.sitting.correction.content_invalid").Wrap(errors.New("content checksum mismatch"))
		}
		stage, err = s.deps.Persistence.MarkResourceStageReady(ctx, &store.ExamCorrectionResourceStageReadiness{StageID: stage.ID, ActorUserID: principal.UserID, Rendition: &rendition, ReadyAt: model.TimeUTC(s.deps.Now())})
		if err != nil {
			return ResourceStage{}, storeErrors.Translate(err)
		}
		if err = validateStage(stage, c, principal.UserID); err != nil {
			return ResourceStage{}, err
		}
	}
	if stage.State != store.ExamCorrectionResourceStageReady && stage.State != store.ExamCorrectionResourceStageConsumed {
		return ResourceStage{}, apperror.New("exam.sitting.correction.unavailable").Wrap(errors.New("stage did not become ready"))
	}
	return projectStage(stage), nil
}

func validateStage(stage *store.ExamCorrectionResourceStage, c StageResourceContentCommand, actor model.UserID) error {
	if stage == nil || !stage.ID.IsValid() || stage.ExamID != c.ExamID || stage.SittingID != c.SittingID || stage.BaseRevisionID != c.BaseRevisionID || stage.Target != c.Target || !stage.ResourceID.IsValid() || !stage.FileEntryID.IsValid() || !stage.FileRevisionID.IsValid() || !stage.UploadLeaseID.IsValid() || !stage.RenditionID.IsValid() || stage.CreatedByUserID != actor || stage.ExpiresAt.IsZero() || (c.Target == store.ExamCorrectionResourceReplacement && stage.ResourceID != c.ResourceID) {
		return apperror.New("exam.sitting.correction.unavailable").Wrap(errors.New("inconsistent stage outcome"))
	}
	if stage.State != store.ExamCorrectionResourceStagePending && stage.State != store.ExamCorrectionResourceStageReady && stage.State != store.ExamCorrectionResourceStageConsumed {
		return apperror.New("exam.sitting.correction.unavailable").Wrap(errors.New("invalid stage state"))
	}
	if stage.State != store.ExamCorrectionResourceStagePending && (stage.Rendition == nil || stage.Rendition.ID != stage.RenditionID || stage.Rendition.RevisionID != stage.FileRevisionID || stage.Rendition.Validate() != nil) {
		return apperror.New("exam.sitting.correction.unavailable").Wrap(errors.New("incomplete ready stage"))
	}
	if stage.Rendition != nil && (model.ExamResourceMediaType(stage.Rendition.MediaType) != c.MediaType || stage.Rendition.Size != c.Size || subtle.ConstantTimeCompare([]byte(stage.Rendition.SHA256), []byte(c.ExpectedSHA256)) != 1) {
		return apperror.New("exam.sitting.correction.unavailable").Wrap(errors.New("stage metadata differs from command"))
	}
	return nil
}

func projectStage(stage *store.ExamCorrectionResourceStage) ResourceStage {
	return ResourceStage{StageID: stage.ID, ResourceID: stage.ResourceID, MediaType: model.ExamResourceMediaType(stage.Rendition.MediaType), Size: stage.Rendition.Size, SHA256: stage.Rendition.SHA256, ExpiresAt: stage.ExpiresAt}
}

// ApplyExamSittingCorrection idempotently applies a live correction to a
// Sitting, publishing a new Exam Revision from changed Exam Instructions,
// browser policy, or staged Exam Resources. It is fenced by
// ExpectedSittingRevision and ExpectedCurrentRevisionID.
func (s *Service) ApplyExamSittingCorrection(ctx context.Context, invocation invocation.Invocation, c ApplyCommand) (_ AppliedCorrection, resultErr error) {
	defer func() { resultErr = publicFailures.Conceal(resultErr) }()
	manifest := make([]ResourceManifestItem, len(c.Resources))
	copy(manifest, c.Resources)
	c.Resources = manifest
	c.AffectedCapabilities = slices.Clone(c.AffectedCapabilities)
	if err := validateApply(c); err != nil {
		return AppliedCorrection{}, err
	}
	c.Resources = append([]ResourceManifestItem(nil), c.Resources...)
	if c.BrowserPolicy.Present {
		if err := c.BrowserPolicy.Policy.ValidateInstitutionOrigin(s.deps.InstitutionOrigin); err != nil {
			return AppliedCorrection{}, apperror.Invalid("exam.sitting.correction.invalid", "browser_policy").Wrap(err)
		}
	}
	idempotency, err := prepareApplyIdempotency(invocation, c)
	if err != nil {
		return AppliedCorrection{}, err
	}
	auth, err := s.authorize(ctx, invocation, c.ExamID, c.SittingID)
	if err != nil {
		return AppliedCorrection{}, err
	}
	at := model.TimeUTC(s.deps.Now())
	value := map[string]any{"exam_id": c.ExamID.String(), "exam_sitting_id": c.SittingID.String(), "expected_sitting_revision": c.ExpectedSittingRevision, "expected_current_revision_id": c.ExpectedCurrentRevisionID.String(), "instructions_present": c.Instructions.Present, "browser_policy_present": c.BrowserPolicy.Present, "resource_count": len(c.Resources), "acknowledgement_required": c.AcknowledgementRequired}
	auditID, err := s.deps.Auditor.BeginAtScope(ctx, invocation, auth.action, model.Resource{Type: model.ResourceExamSitting, ID: c.SittingID.String()}, model.RoleScopeAcademicUnit, auth.academicUnitID.String(), "exam_sitting_content_correct", value, nil)
	if err != nil {
		return AppliedCorrection{}, err
	}
	var instructions *string
	if c.Instructions.Present {
		v := c.Instructions.Markdown
		instructions = &v
	}
	var browserPolicy *model.BrowserPolicy
	if c.BrowserPolicy.Present {
		value := c.BrowserPolicy.Policy.Clone()
		browserPolicy = &value
	}
	resources := make([]store.ExamCorrectionResourceManifestItem, len(c.Resources))
	for i, r := range c.Resources {
		resources[i] = store.ExamCorrectionResourceManifestItem{ResourceID: r.ResourceID, DisplayName: strings.TrimSpace(r.DisplayName), DescriptionMarkdown: r.DescriptionMarkdown, StageID: r.StageID}
	}
	stored, err := s.deps.Persistence.Apply(ctx, &store.ExamCorrectionApplication{InstitutionOrigin: s.deps.InstitutionOrigin, RevisionID: s.deps.NewExamRevisionID(), ExamID: c.ExamID, SittingID: c.SittingID, CurrentRevisionID: c.ExpectedCurrentRevisionID, ExpectedSittingRevision: c.ExpectedSittingRevision, ActorUserID: invocation.Principal().UserID, ManagerOverride: auth.override, InstructionsMarkdown: instructions, BrowserPolicy: browserPolicy, Resources: resources, CandidateSummary: c.CandidateSummary, AcknowledgementRequired: c.AcknowledgementRequired, AffectedCapabilities: append([]model.CandidateCapability{}, c.AffectedCapabilities...), PrivateReason: c.PrivateReason, AppliedAt: at, AuditEventID: auditID, AuditAt: at}, idempotency)
	if err != nil {
		return AppliedCorrection{}, s.failAudit(ctx, auditID, err)
	}
	result, err := projectResult(stored, c)
	if err != nil {
		return AppliedCorrection{}, err
	}
	if !result.Replayed {
		if effectErr := s.deps.Effects.Corrected(ctx, result); effectErr != nil {
			s.deps.EffectFailures.Report(ctx, "exam_sitting_content_corrected", effectErr)
		}
	}
	return result.applied(), nil
}

func validateApply(c ApplyCommand) error {
	if !c.ExamID.IsValid() || !c.SittingID.IsValid() || !c.ExpectedCurrentRevisionID.IsValid() || c.ExpectedSittingRevision < 1 || len(c.Resources) > model.ExamResourceMaximumCount || !utf8.ValidString(c.PrivateReason) || strings.TrimSpace(c.PrivateReason) == "" || strings.TrimSpace(c.PrivateReason) != c.PrivateReason || utf8.RuneCountInString(c.PrivateReason) > 1000 || len(c.PrivateReason) > 4000 {
		return apperror.Invalid("exam.sitting.correction.invalid", "command")
	}
	if !c.Instructions.Present && c.Instructions.Markdown != "" {
		return apperror.Invalid("exam.sitting.correction.invalid", "instructions_markdown")
	}
	if c.Instructions.Present && (!utf8.ValidString(c.Instructions.Markdown) || len(c.Instructions.Markdown) > model.ExamInstructionsMarkdownMaxBytes) {
		return apperror.Invalid("exam.sitting.correction.invalid", "instructions_markdown")
	}
	if !c.BrowserPolicy.Present && (c.BrowserPolicy.Policy.Enabled || c.BrowserPolicy.Policy.StartRuleID != "" || len(c.BrowserPolicy.Policy.Rules) != 0) || c.BrowserPolicy.Present && c.BrowserPolicy.Policy.Validate() != nil {
		return apperror.Invalid("exam.sitting.correction.invalid", "browser_policy")
	}
	if err := model.ValidateCandidateCorrectionSelection(c.CandidateSummary, c.AffectedCapabilities); err != nil {
		return apperror.Invalid("exam.sitting.correction.invalid", "candidate_summary")
	}
	resourceIDs := map[model.ExamResourceID]struct{}{}
	stageIDs := map[model.ExamCorrectionResourceStageID]struct{}{}
	for _, r := range c.Resources {
		if !r.ResourceID.IsValid() || strings.TrimSpace(r.DisplayName) != r.DisplayName || r.DisplayName == "" || !utf8.ValidString(r.DisplayName) || utf8.RuneCountInString(r.DisplayName) > model.ExamResourceDisplayNameMaxRunes || !utf8.ValidString(r.DescriptionMarkdown) || len(r.DescriptionMarkdown) > model.ExamResourceDescriptionMaxBytes {
			return apperror.Invalid("exam.sitting.correction.invalid", "resources")
		}
		if _, ok := resourceIDs[r.ResourceID]; ok {
			return apperror.Invalid("exam.sitting.correction.invalid", "resources")
		}
		resourceIDs[r.ResourceID] = struct{}{}
		if !r.StageID.IsZero() && !r.StageID.IsValid() {
			return apperror.Invalid("exam.sitting.correction.invalid", "resources")
		}
		if r.StageID.IsValid() {
			if _, ok := stageIDs[r.StageID]; ok {
				return apperror.Invalid("exam.sitting.correction.invalid", "resources")
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

func (s *Service) authorize(ctx context.Context, invocation invocation.Invocation, examID model.ExamID, sittingID model.ExamSittingID) (authorizationDecision, error) {
	p := invocation.Principal()
	if p.Validate() != nil {
		return authorizationDecision{}, apperror.Invalid("exam.sitting.correction.invalid", "principal")
	}
	access, err := s.deps.Access.Access(ctx, examID, p.UserID)
	if err != nil {
		return authorizationDecision{}, storeErrors.Translate(err)
	}
	if access == nil || access.Exam == nil || access.Exam.ID != examID {
		return authorizationDecision{}, apperror.New("exam.sitting.correction.unavailable").Wrap(errors.New("missing access"))
	}
	var at time.Time
	if access.ActorIsManager {
		at = s.deps.Now()
	}
	action, err := manageraccess.SelectAction(ctx, s.deps.Memberships, p.UserID, access, at, model.ActionExamSittingManage, model.ActionExamSittingManageOverride)
	if err != nil {
		return authorizationDecision{}, apperror.New("exam.sitting.correction.unavailable").Wrap(err)
	}
	if err = s.deps.Authorizer.Authorize(ctx, invocation, action, model.Resource{Type: model.ResourceExamSitting, ID: sittingID.String()}); err != nil {
		return authorizationDecision{}, err
	}
	return authorizationDecision{action == model.ActionExamSittingManageOverride, access.Exam.AcademicUnitID, action}, nil
}
func (s *Service) failAudit(ctx context.Context, id model.AuditEventID, err error) error {
	mapped := storeErrors.Translate(err)
	code := apperror.CodeOf(mapped, "exam.sitting.correction.unavailable")
	if auditErr := s.deps.Auditor.Fail(ctx, id, code); auditErr != nil {
		return errors.Join(mapped, auditErr)
	}
	return mapped
}
func projectResult(stored *store.ExamCorrectionResult, c ApplyCommand) (Result, error) {
	if stored == nil || stored.Revision == nil || stored.Sitting == nil || stored.Sitting.Sitting == nil {
		return Result{}, apperror.New("exam.sitting.correction.unavailable").Wrap(errors.New("missing correction outcome"))
	}
	s := stored.Sitting.Sitting
	if stored.Revision.ExamID != c.ExamID || stored.Revision.ID == c.ExpectedCurrentRevisionID || stored.Revision.Kind != model.ExamRevisionPublicationLiveCorrection || stored.Revision.Number < 1 ||
		stored.PreviousRevisionID != c.ExpectedCurrentRevisionID || s.ID != c.SittingID || s.ExamID != c.ExamID ||
		s.ExamRevisionID != stored.Revision.ID || s.Revision != c.ExpectedSittingRevision+1 ||
		(s.State != model.ExamSittingOpen && s.State != model.ExamSittingPaused) || s.UpdatedAt.IsZero() ||
		stored.EffectiveAt.IsZero() || !model.TimeUTC(stored.EffectiveAt).Equal(model.TimeUTC(stored.Revision.PublishedAt)) {
		return Result{}, apperror.New("exam.sitting.correction.unavailable").Wrap(errors.New("inconsistent correction outcome"))
	}
	return Result{ExamID: c.ExamID, SittingID: c.SittingID, PreviousRevisionID: stored.PreviousRevisionID,
		RevisionID: stored.Revision.ID, RevisionNumber: stored.Revision.Number, SittingState: s.State,
		SittingRevision: s.Revision, EffectiveAt: model.TimeUTC(stored.EffectiveAt),
		AcknowledgementRequired: c.AcknowledgementRequired, AffectedCapabilities: append([]model.CandidateCapability{}, c.AffectedCapabilities...), Replayed: stored.Replayed}, nil
}
func mapContent(err error) error {
	var capacity workCapacityError
	if errors.As(err, &capacity) {
		return apperror.New("service.busy").Wrap(err)
	}
	var invalidContent invalidContentError
	if errors.As(err, &invalidContent) {
		return apperror.New("exam.sitting.correction.content_invalid").Wrap(err)
	}
	return apperror.New("exam.sitting.correction.unavailable").Wrap(err)
}

// publicFailures presents every live-correction use-case failure: unexpected errors
// become exam.sitting.correction.unavailable, and protected targets conceal denial and absence.
var publicFailures = apperror.Concealment{
	Unavailable: "exam.sitting.correction.unavailable",
	Concealed:   []apperror.Code{"exam.sitting.correction.not_found"},
}

var storeErrors = apperror.StoreTable{
	Constraints: map[string]apperror.Code{
		"exam_archived":                     "exam.archived",
		"exam_sitting_revision":             "exam.sitting.revision_conflict",
		"exam_sitting_state":                "exam.sitting.state_conflict",
		"exam_sitting_revision_selection":   "exam.sitting.revision_conflict",
		"exam_sitting_deadline_reached":     "exam.sitting.deadline_reached",
		"exam_correction_base_revision":     "exam.sitting.revision_conflict",
		"exam_correction_no_changes":        "exam.sitting.correction.no_changes",
		"exam_correction_limit":             "exam.sitting.correction.limit",
		"exam_correction_resource_limit":    "exam.resource.limit",
		"exam_correction_resource_manifest": "exam.sitting.correction.manifest_conflict",
		"exam_correction_resource_stage":    "exam.sitting.correction.stage_conflict",
	},
	NotFound:     "exam.sitting.correction.not_found",
	Conflict:     "exam.sitting.correction.conflict",
	Invalid:      "exam.sitting.correction.invalid",
	InvalidField: "value",
	Unavailable:  "exam.sitting.correction.unavailable",
}

func validSHA256(value string) bool {
	if len(value) != 64 || value != strings.ToLower(value) {
		return false
	}
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == 32
}
