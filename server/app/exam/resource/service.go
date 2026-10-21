// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package resource

import (
	"context"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"io"
	"strings"
	"time"

	"github.com/sudosylabs/proctor/server/app/apperror"
	"github.com/sudosylabs/proctor/server/app/exam/manageraccess"
	"github.com/sudosylabs/proctor/server/app/invocation"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

type AccessStore interface {
	Access(context.Context, model.ExamID, model.UserID) (*store.ExamAccessSnapshot, error)
	Get(context.Context, model.ExamID, model.UserID) (*store.ExamAuthoringSnapshot, error)
}
type Authorizer interface {
	Authorize(context.Context, invocation.Invocation, model.Action, model.Resource) error
}
type Auditor interface {
	BeginAtScope(context.Context, invocation.Invocation, model.Action, model.Resource, model.RoleScopeType, string, string, map[string]any, map[string]any) (model.AuditEventID, error)
	Fail(context.Context, model.AuditEventID, string) error
}
type Effects interface {
	Changed(context.Context, model.ExamID, model.ExamResourceID, int64, string) error
}
type EffectFailures interface {
	Report(context.Context, string, error)
}
type FileContent interface {
	StoreExamResource(context.Context, model.FileRevisionID, model.ExamResourceMediaType, io.Reader, int64, time.Time) (model.FileRendition, error)
	OpenExamResource(context.Context, model.FileRevisionID, model.FileRenditionID) (io.ReadCloser, error)
	RemoveExamResource(context.Context, model.FileRevisionID, model.FileRenditionID) error
}

type invalidContentError interface {
	InvalidExamResourceContent()
}

type workCapacityError interface{ WorkCapacityExceeded() }

type CreateCommand struct {
	ExamID                           model.ExamID
	ExpectedDraftRevision            int64
	DisplayName, DescriptionMarkdown string
	MediaType                        model.ExamResourceMediaType
	Body                             io.Reader
	Size                             int64
	ExpectedSHA256                   string
	IdempotencyKey                   string
}
type ReplaceContentCommand struct {
	ExamID                model.ExamID
	ResourceID            model.ExamResourceID
	ExpectedDraftRevision int64
	MediaType             model.ExamResourceMediaType
	Body                  io.Reader
	Size                  int64
	ExpectedSHA256        string
	IdempotencyKey        string
}
type EditMetadataCommand struct {
	ExamID                model.ExamID
	ResourceID            model.ExamResourceID
	ExpectedDraftRevision int64
	DisplayName           *string
	DescriptionMarkdown   *string
	IdempotencyKey        string
}
type ReorderCommand struct {
	ExamID                model.ExamID
	ExpectedDraftRevision int64
	ResourceIDs           []model.ExamResourceID
	IdempotencyKey        string
}
type RemoveCommand struct {
	ExamID                model.ExamID
	ResourceID            model.ExamResourceID
	ExpectedDraftRevision int64
	IdempotencyKey        string
}
type Opened struct {
	Record store.ExamResourceRecord
	Body   io.ReadCloser
}

func cloneStringPointer(value *string) *string {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

// Service manages the Exam Resources of an Exam Draft.
type Service struct{ deps Dependencies }

// Dependencies are the collaborators of Service; New validates them once and the
// service retains them unchanged.
type Dependencies struct {
	Persistence    store.ExamResourceStore
	Access         AccessStore
	Memberships    manageraccess.Memberships
	Authorizer     Authorizer
	Auditor        Auditor
	Effects        Effects
	EffectFailures EffectFailures
	Content        FileContent
	Now            func() time.Time
	NewResourceID  func() model.ExamResourceID
	NewEntryID     func() model.FileEntryID
	NewRevisionID  func() model.FileRevisionID
	NewLeaseID     func() model.UploadLeaseID
}

// New returns an Exam Resource Service, or an error when a required
// dependency is missing.
func New(deps Dependencies) (*Service, error) {
	if deps.Persistence == nil || deps.Access == nil || deps.Memberships == nil || deps.Authorizer == nil || deps.Auditor == nil || deps.Effects == nil || deps.EffectFailures == nil || deps.Content == nil || deps.Now == nil || deps.NewResourceID == nil || deps.NewEntryID == nil || deps.NewRevisionID == nil || deps.NewLeaseID == nil {
		return nil, errors.New("exam resource dependencies are required")
	}
	return &Service{deps: deps}, nil
}

// ListQuery names the Exam whose Draft Resources are listed.
type ListQuery struct{ ExamID model.ExamID }

// ListExamResources returns the Exam Draft's Exam Resources to a caller
// authorized to view the Exam.
func (s *Service) ListExamResources(ctx context.Context, invocation invocation.Invocation, query ListQuery) (_ []store.ExamResourceRecord, resultErr error) {
	defer func() { resultErr = publicFailures.Conceal(resultErr) }()
	if !query.ExamID.IsValid() {
		return nil, apperror.Invalid("exam.resource.invalid", "exam_id")
	}
	if _, err := s.authorize(ctx, invocation, query.ExamID, false); err != nil {
		return nil, err
	}
	items, err := s.deps.Persistence.List(ctx, query.ExamID)
	if err != nil {
		return nil, storeErrors.Translate(err)
	}
	return items, nil
}

// OpenQuery names one Draft Resource of an Exam.
type OpenQuery struct {
	ExamID     model.ExamID
	ResourceID model.ExamResourceID
}

// OpenExamResource opens the selected content of one Exam Draft Exam Resource
// for a caller authorized to view the Exam. The caller must close the body.
func (s *Service) OpenExamResource(ctx context.Context, invocation invocation.Invocation, query OpenQuery) (_ Opened, resultErr error) {
	defer func() { resultErr = publicFailures.Conceal(resultErr) }()
	if !query.ExamID.IsValid() || !query.ResourceID.IsValid() {
		return Opened{}, apperror.Invalid("exam.resource.invalid", "identity")
	}
	if _, err := s.authorize(ctx, invocation, query.ExamID, false); err != nil {
		return Opened{}, err
	}
	record, err := s.deps.Persistence.Get(ctx, query.ExamID, query.ResourceID)
	if err != nil {
		return Opened{}, storeErrors.Translate(err)
	}
	if record == nil || record.Resource == nil || record.Rendition == nil {
		return Opened{}, apperror.New("exam.resource.unavailable").Wrap(errors.New("incomplete resource record"))
	}
	body, err := s.deps.Content.OpenExamResource(ctx, record.Resource.SelectedFileRevisionID, record.Rendition.ID)
	if err != nil {
		return Opened{}, apperror.New("exam.resource.unavailable").Wrap(err)
	}
	return Opened{Record: *record, Body: body}, nil
}

// CreateExamResource idempotently uploads, verifies, and adds an Exam
// Resource to the Exam Draft, fenced by ExpectedDraftRevision.
func (s *Service) CreateExamResource(ctx context.Context, invocation invocation.Invocation, command CreateCommand) (_ store.ExamResourceRecord, resultErr error) {
	defer func() { resultErr = publicFailures.Conceal(resultErr) }()
	if !command.ExamID.IsValid() || command.ExpectedDraftRevision < 1 || command.Body == nil || command.Size < 0 || command.Size > model.ExamResourceMaximumBytes || !command.MediaType.IsValid() || !validSHA256(command.ExpectedSHA256) {
		return store.ExamResourceRecord{}, apperror.Invalid("exam.resource.invalid", "upload")
	}
	command.DisplayName = strings.TrimSpace(command.DisplayName)
	idempotency, err := prepareResourceIdempotency(invocation, idempotencyOperationAddResource, command.IdempotencyKey, command.ExamID, command.ExpectedDraftRevision, "", command.DisplayName, command.DescriptionMarkdown, command.MediaType, command.Size, command.ExpectedSHA256, nil)
	if err != nil {
		return store.ExamResourceRecord{}, err
	}
	authorization, err := s.authorize(ctx, invocation, command.ExamID, true)
	if err != nil {
		return store.ExamResourceRecord{}, err
	}
	items, err := s.deps.Persistence.List(ctx, command.ExamID)
	if err != nil {
		return store.ExamResourceRecord{}, storeErrors.Translate(err)
	}
	position := min(len(items), model.ExamResourceMaximumCount-1)
	at := model.TimeUTC(s.deps.Now())
	principal := invocation.Principal()
	resourceID, entryID, revisionID, leaseID := s.deps.NewResourceID(), s.deps.NewEntryID(), s.deps.NewRevisionID(), s.deps.NewLeaseID()
	entry, err := model.NewFileEntryForPurpose(entryID, model.FilePurposeExamResource, model.FileIndexingNone, at)
	if err != nil {
		return store.ExamResourceRecord{}, apperror.Invalid("exam.resource.invalid", "file_entry").Wrap(err)
	}
	revision, err := model.NewFileRevision(revisionID, entryID, model.FileAvailabilityPending, model.FileIndexingNotRequired, at)
	if err != nil {
		return store.ExamResourceRecord{}, apperror.Invalid("exam.resource.invalid", "file_revision").Wrap(err)
	}
	lease, err := model.NewUploadLease(leaseID, revisionID, principal.UserID, at, at.Add(time.Hour))
	if err != nil {
		return store.ExamResourceRecord{}, apperror.Invalid("exam.resource.invalid", "upload_lease").Wrap(err)
	}
	resource, err := model.NewExamResource(resourceID, command.ExamID, entryID, revisionID, command.DisplayName, command.DescriptionMarkdown, position, at)
	if err != nil {
		return store.ExamResourceRecord{}, apperror.Invalid("exam.resource.invalid", "metadata").Wrap(err)
	}
	if _, err = s.deps.Persistence.ReserveUpload(ctx, &store.ExamResourceUploadReservation{ExamID: command.ExamID, ActorUserID: principal.UserID, ManagerOverride: authorization.override, ExpectedDraftRevision: command.ExpectedDraftRevision, ResourceID: resourceID, Entry: entry, Revision: revision, Lease: lease}); err != nil {
		return store.ExamResourceRecord{}, storeErrors.Translate(err)
	}
	rendition, err := s.deps.Content.StoreExamResource(ctx, revisionID, command.MediaType, command.Body, command.Size, at)
	if err != nil {
		return store.ExamResourceRecord{}, mapContent(err)
	}
	if subtle.ConstantTimeCompare([]byte(rendition.SHA256), []byte(command.ExpectedSHA256)) != 1 {
		_ = s.deps.Content.RemoveExamResource(ctx, rendition.RevisionID, rendition.ID)
		return store.ExamResourceRecord{}, apperror.New("exam.resource.content_invalid").Wrap(errors.New("content checksum mismatch"))
	}
	return s.finalize(ctx, invocation, authorization, resource, leaseID, &rendition, command.ExpectedDraftRevision, idempotency, "add")
}

// ReplaceExamResourceContent idempotently uploads and selects new content for
// an existing Exam Resource, fenced by ExpectedDraftRevision.
func (s *Service) ReplaceExamResourceContent(ctx context.Context, invocation invocation.Invocation, command ReplaceContentCommand) (_ store.ExamResourceRecord, resultErr error) {
	defer func() { resultErr = publicFailures.Conceal(resultErr) }()
	if !command.ExamID.IsValid() || !command.ResourceID.IsValid() || command.ExpectedDraftRevision < 1 || command.Body == nil || command.Size < 0 || command.Size > model.ExamResourceMaximumBytes || !command.MediaType.IsValid() || !validSHA256(command.ExpectedSHA256) {
		return store.ExamResourceRecord{}, apperror.Invalid("exam.resource.invalid", "upload")
	}
	idempotency, err := prepareResourceIdempotency(invocation, idempotencyOperationReplaceResourceContent, command.IdempotencyKey, command.ExamID, command.ExpectedDraftRevision, command.ResourceID.String(), "", "", command.MediaType, command.Size, command.ExpectedSHA256, nil)
	if err != nil {
		return store.ExamResourceRecord{}, err
	}
	authorization, err := s.authorize(ctx, invocation, command.ExamID, true)
	if err != nil {
		return store.ExamResourceRecord{}, err
	}
	current, err := s.deps.Persistence.Get(ctx, command.ExamID, command.ResourceID)
	if err != nil {
		return store.ExamResourceRecord{}, storeErrors.Translate(err)
	}
	if current == nil || current.Resource == nil || current.Rendition == nil {
		return store.ExamResourceRecord{}, apperror.New("exam.resource.unavailable").Wrap(errors.New("incomplete resource record"))
	}
	at := model.TimeUTC(s.deps.Now())
	principal := invocation.Principal()
	revisionID, leaseID := s.deps.NewRevisionID(), s.deps.NewLeaseID()
	revision, err := model.NewFileRevision(revisionID, current.Resource.FileEntryID, model.FileAvailabilityPending, model.FileIndexingNotRequired, at)
	if err != nil {
		return store.ExamResourceRecord{}, apperror.Invalid("exam.resource.invalid", "file_revision").Wrap(err)
	}
	lease, err := model.NewUploadLease(leaseID, revisionID, principal.UserID, at, at.Add(time.Hour))
	if err != nil {
		return store.ExamResourceRecord{}, apperror.Invalid("exam.resource.invalid", "upload_lease").Wrap(err)
	}
	if _, err = s.deps.Persistence.ReserveUpload(ctx, &store.ExamResourceUploadReservation{ExamID: command.ExamID, ActorUserID: principal.UserID, ManagerOverride: authorization.override, ExpectedDraftRevision: command.ExpectedDraftRevision, ResourceID: command.ResourceID, EntryID: current.Resource.FileEntryID, Revision: revision, Lease: lease, Replacement: true}); err != nil {
		return store.ExamResourceRecord{}, storeErrors.Translate(err)
	}
	rendition, err := s.deps.Content.StoreExamResource(ctx, revisionID, command.MediaType, command.Body, command.Size, at)
	if err != nil {
		return store.ExamResourceRecord{}, mapContent(err)
	}
	if subtle.ConstantTimeCompare([]byte(rendition.SHA256), []byte(command.ExpectedSHA256)) != 1 {
		_ = s.deps.Content.RemoveExamResource(ctx, rendition.RevisionID, rendition.ID)
		return store.ExamResourceRecord{}, apperror.New("exam.resource.content_invalid").Wrap(errors.New("content checksum mismatch"))
	}
	updated := *current.Resource
	if _, err = updated.ReplaceContent(revisionID, at); err != nil {
		return store.ExamResourceRecord{}, apperror.Invalid("exam.resource.invalid", "content").Wrap(err)
	}
	return s.finalize(ctx, invocation, authorization, &updated, leaseID, &rendition, command.ExpectedDraftRevision, idempotency, "replace_content")
}

type authorizationDecision struct {
	override       bool
	academicUnitID model.AcademicUnitID
}

func (s *Service) finalize(ctx context.Context, invocation invocation.Invocation, authorization authorizationDecision, resource *model.ExamResource, leaseID model.UploadLeaseID, rendition *model.FileRendition, expected int64, idempotency *store.CommandIdempotency, operation string) (store.ExamResourceRecord, error) {
	action := model.ActionExamManage
	if authorization.override {
		action = model.ActionExamManageOverride
	}
	auditID, err := s.deps.Auditor.BeginAtScope(ctx, invocation, action, model.Resource{Type: model.ResourceExam, ID: resource.ExamID.String()}, model.RoleScopeAcademicUnit, authorization.academicUnitID.String(), operation, map[string]any{"exam_id": resource.ExamID.String(), "exam_resource_id": resource.ID.String(), "file_revision_id": resource.SelectedFileRevisionID.String()}, nil)
	if err != nil {
		_ = s.deps.Content.RemoveExamResource(ctx, rendition.RevisionID, rendition.ID)
		return store.ExamResourceRecord{}, err
	}
	at := model.TimeUTC(s.deps.Now())
	result, err := s.deps.Persistence.FinalizeUpload(ctx, &store.ExamResourceUploadFinalization{ExamID: resource.ExamID, ActorUserID: invocation.Principal().UserID, ManagerOverride: authorization.override, ExpectedDraftRevision: expected, Resource: resource, LeaseID: leaseID, Rendition: rendition, ChangedAt: at, AuditEventID: auditID, AuditAt: model.MillisFromTime(at)}, idempotency)
	if err != nil {
		return store.ExamResourceRecord{}, s.failAudit(ctx, auditID, err)
	}
	if result == nil || result.Value == nil {
		return store.ExamResourceRecord{}, apperror.New("exam.resource.unavailable").Wrap(errors.New("missing resource outcome"))
	}
	s.effect(ctx, result, operation)
	return *result.Value, nil
}

// EditExamResourceMetadata idempotently changes an Exam Resource's display
// name, description, or both, fenced by ExpectedDraftRevision. A nil field is
// left unchanged, but at least one must be set.
func (s *Service) EditExamResourceMetadata(ctx context.Context, invocation invocation.Invocation, command EditMetadataCommand) (_ store.ExamResourceRecord, resultErr error) {
	defer func() { resultErr = publicFailures.Conceal(resultErr) }()
	if !command.ExamID.IsValid() || !command.ResourceID.IsValid() || command.ExpectedDraftRevision < 1 || command.DisplayName == nil && command.DescriptionMarkdown == nil {
		return store.ExamResourceRecord{}, apperror.Invalid("exam.resource.invalid", "identity")
	}
	command.DisplayName = cloneStringPointer(command.DisplayName)
	if command.DisplayName != nil {
		*command.DisplayName = strings.TrimSpace(*command.DisplayName)
	}
	command.DescriptionMarkdown = cloneStringPointer(command.DescriptionMarkdown)
	idempotency, err := prepareMetadataIdempotency(invocation, command)
	if err != nil {
		return store.ExamResourceRecord{}, err
	}
	authorization, err := s.authorize(ctx, invocation, command.ExamID, true)
	if err != nil {
		return store.ExamResourceRecord{}, err
	}
	current, err := s.deps.Persistence.Get(ctx, command.ExamID, command.ResourceID)
	if err != nil {
		return store.ExamResourceRecord{}, storeErrors.Translate(err)
	}
	if current == nil || current.Resource == nil {
		return store.ExamResourceRecord{}, apperror.New("exam.resource.unavailable").Wrap(errors.New("incomplete resource record"))
	}
	candidate := *current.Resource
	displayName, descriptionMarkdown := candidate.DisplayName, candidate.DescriptionMarkdown
	if command.DisplayName != nil {
		displayName = *command.DisplayName
	}
	if command.DescriptionMarkdown != nil {
		descriptionMarkdown = *command.DescriptionMarkdown
	}
	changed, validationErr := candidate.ApplyMetadata(displayName, descriptionMarkdown, model.TimeUTC(s.deps.Now()))
	if validationErr != nil {
		return store.ExamResourceRecord{}, apperror.Invalid("exam.resource.invalid", "metadata").Wrap(validationErr)
	}
	if !changed && s.currentActiveDraft(ctx, invocation, command.ExamID, command.ExpectedDraftRevision) {
		return store.ExamResourceRecord{}, apperror.New("exam.resource.no_changes")
	}
	return s.metadataMutation(ctx, invocation, authorization, command, idempotency)
}
func (s *Service) metadataMutation(ctx context.Context, invocation invocation.Invocation, authorization authorizationDecision, c EditMetadataCommand, idempotency *store.CommandIdempotency) (store.ExamResourceRecord, error) {
	at := model.TimeUTC(s.deps.Now())
	action := model.ActionExamManage
	if authorization.override {
		action = model.ActionExamManageOverride
	}
	auditID, err := s.deps.Auditor.BeginAtScope(ctx, invocation, action, model.Resource{Type: model.ResourceExam, ID: c.ExamID.String()}, model.RoleScopeAcademicUnit, authorization.academicUnitID.String(), "edit_resource_metadata", map[string]any{"exam_id": c.ExamID.String(), "exam_resource_id": c.ResourceID.String()}, nil)
	if err != nil {
		return store.ExamResourceRecord{}, err
	}
	displayName, descriptionMarkdown := "", ""
	current, err := s.deps.Persistence.Get(ctx, c.ExamID, c.ResourceID)
	if err != nil {
		return store.ExamResourceRecord{}, s.failAudit(ctx, auditID, err)
	}
	if current == nil || current.Resource == nil {
		return store.ExamResourceRecord{}, s.failAudit(ctx, auditID, errors.New("missing resource record"))
	}
	displayName, descriptionMarkdown = current.Resource.DisplayName, current.Resource.DescriptionMarkdown
	if c.DisplayName != nil {
		displayName = *c.DisplayName
	}
	if c.DescriptionMarkdown != nil {
		descriptionMarkdown = *c.DescriptionMarkdown
	}
	result, err := s.deps.Persistence.UpdateMetadata(ctx, &store.ExamResourceMetadataUpdate{ExamID: c.ExamID, ActorUserID: invocation.Principal().UserID, ManagerOverride: authorization.override, ExpectedDraftRevision: c.ExpectedDraftRevision, ResourceID: c.ResourceID, DisplayName: displayName, DescriptionMarkdown: descriptionMarkdown, ChangedAt: at, AuditEventID: auditID, AuditAt: model.MillisFromTime(at)}, idempotency)
	if err != nil {
		return store.ExamResourceRecord{}, s.failAudit(ctx, auditID, err)
	}
	if result == nil || result.Value == nil {
		return store.ExamResourceRecord{}, apperror.New("exam.resource.unavailable").Wrap(errors.New("missing metadata outcome"))
	}
	s.effect(ctx, result, "edit_metadata")
	return *result.Value, nil
}

// ReorderExamResources idempotently sets the order of the Exam Draft's Exam
// Resources, fenced by ExpectedDraftRevision. ResourceIDs must not repeat.
func (s *Service) ReorderExamResources(ctx context.Context, invocation invocation.Invocation, c ReorderCommand) (_ []store.ExamResourceRecord, resultErr error) {
	defer func() { resultErr = publicFailures.Conceal(resultErr) }()
	c.ResourceIDs = append([]model.ExamResourceID(nil), c.ResourceIDs...)
	if !c.ExamID.IsValid() || c.ExpectedDraftRevision < 1 {
		return nil, apperror.Invalid("exam.resource.invalid", "identity")
	}
	if len(c.ResourceIDs) > model.ExamResourceMaximumCount {
		return nil, apperror.Invalid("exam.resource.invalid", "resource_ids")
	}
	seen := make(map[model.ExamResourceID]struct{}, len(c.ResourceIDs))
	for _, id := range c.ResourceIDs {
		if !id.IsValid() {
			return nil, apperror.Invalid("exam.resource.invalid", "resource_ids")
		}
		if _, exists := seen[id]; exists {
			return nil, apperror.Invalid("exam.resource.invalid", "resource_ids")
		}
		seen[id] = struct{}{}
	}
	c.ResourceIDs = append([]model.ExamResourceID(nil), c.ResourceIDs...)
	ids := make([]string, len(c.ResourceIDs))
	for index, id := range c.ResourceIDs {
		ids[index] = id.String()
	}
	idempotency, err := prepareResourceIdempotency(invocation, idempotencyOperationReorderResources, c.IdempotencyKey, c.ExamID, c.ExpectedDraftRevision, "", "", "", "", 0, "", ids)
	if err != nil {
		return nil, err
	}
	authorization, err := s.authorize(ctx, invocation, c.ExamID, true)
	if err != nil {
		return nil, err
	}
	current, err := s.deps.Persistence.List(ctx, c.ExamID)
	if err != nil {
		return nil, storeErrors.Translate(err)
	}
	if len(current) == len(c.ResourceIDs) {
		same := true
		for index := range current {
			if current[index].Resource == nil || current[index].Resource.ID != c.ResourceIDs[index] {
				same = false
				break
			}
		}
		if same && s.currentActiveDraft(ctx, invocation, c.ExamID, c.ExpectedDraftRevision) {
			return nil, apperror.New("exam.resource.no_changes")
		}
	}
	at := model.TimeUTC(s.deps.Now())
	action := model.ActionExamManage
	if authorization.override {
		action = model.ActionExamManageOverride
	}
	auditID, err := s.deps.Auditor.BeginAtScope(ctx, invocation, action, model.Resource{Type: model.ResourceExam, ID: c.ExamID.String()}, model.RoleScopeAcademicUnit, authorization.academicUnitID.String(), "reorder_resources", map[string]any{"exam_id": c.ExamID.String(), "resource_count": len(c.ResourceIDs)}, nil)
	if err != nil {
		return nil, err
	}
	result, err := s.deps.Persistence.Reorder(ctx, &store.ExamResourceReorder{ExamID: c.ExamID, ActorUserID: invocation.Principal().UserID, ManagerOverride: authorization.override, ExpectedDraftRevision: c.ExpectedDraftRevision, ResourceIDs: append([]model.ExamResourceID(nil), c.ResourceIDs...), ChangedAt: at, AuditEventID: auditID, AuditAt: model.MillisFromTime(at)}, idempotency)
	if err != nil {
		return nil, s.failAudit(ctx, auditID, err)
	}
	if result == nil {
		return nil, apperror.New("exam.resource.unavailable").Wrap(errors.New("missing reorder outcome"))
	}
	if !result.Replayed {
		if result.DraftRevision < 1 {
			s.deps.EffectFailures.Report(ctx, "exam_resource_changed", errors.New("missing committed Draft revision"))
		} else if effectErr := s.deps.Effects.Changed(ctx, c.ExamID, "", result.DraftRevision, "reorder"); effectErr != nil {
			s.deps.EffectFailures.Report(ctx, "exam_resource_changed", effectErr)
		}
	}
	return result.Items, nil
}

// RemoveExamResource idempotently removes an Exam Resource from the Exam
// Draft, fenced by ExpectedDraftRevision.
func (s *Service) RemoveExamResource(ctx context.Context, invocation invocation.Invocation, c RemoveCommand) (_ store.ExamResourceRecord, resultErr error) {
	defer func() { resultErr = publicFailures.Conceal(resultErr) }()
	if !c.ExamID.IsValid() || !c.ResourceID.IsValid() || c.ExpectedDraftRevision < 1 {
		return store.ExamResourceRecord{}, apperror.Invalid("exam.resource.invalid", "identity")
	}
	idempotency, err := prepareResourceIdempotency(invocation, idempotencyOperationRemoveResource, c.IdempotencyKey, c.ExamID, c.ExpectedDraftRevision, c.ResourceID.String(), "", "", "", 0, "", nil)
	if err != nil {
		return store.ExamResourceRecord{}, err
	}
	authorization, err := s.authorize(ctx, invocation, c.ExamID, true)
	if err != nil {
		return store.ExamResourceRecord{}, err
	}
	at := model.TimeUTC(s.deps.Now())
	action := model.ActionExamManage
	if authorization.override {
		action = model.ActionExamManageOverride
	}
	auditID, err := s.deps.Auditor.BeginAtScope(ctx, invocation, action, model.Resource{Type: model.ResourceExam, ID: c.ExamID.String()}, model.RoleScopeAcademicUnit, authorization.academicUnitID.String(), "remove_resource", map[string]any{"exam_id": c.ExamID.String(), "exam_resource_id": c.ResourceID.String()}, nil)
	if err != nil {
		return store.ExamResourceRecord{}, err
	}
	result, err := s.deps.Persistence.Remove(ctx, &store.ExamResourceRemoval{ExamID: c.ExamID, ActorUserID: invocation.Principal().UserID, ManagerOverride: authorization.override, ExpectedDraftRevision: c.ExpectedDraftRevision, ResourceID: c.ResourceID, ChangedAt: at, AuditEventID: auditID, AuditAt: model.MillisFromTime(at)}, idempotency)
	if err != nil {
		return store.ExamResourceRecord{}, s.failAudit(ctx, auditID, err)
	}
	if result == nil || result.Value == nil {
		return store.ExamResourceRecord{}, apperror.New("exam.resource.unavailable").Wrap(errors.New("missing removal outcome"))
	}
	s.effect(ctx, result, "remove")
	return *result.Value, nil
}

func (s *Service) authorize(ctx context.Context, invocation invocation.Invocation, examID model.ExamID, manage bool) (authorizationDecision, error) {
	principal := invocation.Principal()
	if principal.Validate() != nil {
		return authorizationDecision{}, apperror.Invalid("exam.resource.invalid", "principal")
	}
	access, err := s.deps.Access.Access(ctx, examID, principal.UserID)
	if err != nil {
		return authorizationDecision{}, storeErrors.Translate(err)
	}
	if access == nil || access.Exam == nil {
		return authorizationDecision{}, apperror.New("exam.resource.unavailable").Wrap(errors.New("missing access"))
	}
	var at time.Time
	if access.ActorIsManager {
		at = s.deps.Now()
	}
	ordinaryAction, overrideAction := model.ActionExamView, model.ActionExamViewOverride
	if manage {
		ordinaryAction, overrideAction = model.ActionExamManage, model.ActionExamManageOverride
	}
	action, err := manageraccess.SelectAction(ctx, s.deps.Memberships, principal.UserID, access, at, ordinaryAction, overrideAction)
	if err != nil {
		return authorizationDecision{}, apperror.New("exam.resource.unavailable").Wrap(err)
	}
	if err = s.deps.Authorizer.Authorize(ctx, invocation, action, model.Resource{Type: model.ResourceExam, ID: examID.String()}); err != nil {
		return authorizationDecision{}, err
	}
	return authorizationDecision{override: action == overrideAction, academicUnitID: access.Exam.AcademicUnitID}, nil
}

// currentActiveDraft permits an unaudited no-op only after a bounded
// authoritative read confirms the exact active Draft generation. Apparent
// no-ops against archived or stale state must reach the audited named Store
// command so its locked guards remain authoritative.
func (s *Service) currentActiveDraft(ctx context.Context, invocation invocation.Invocation, examID model.ExamID, expectedRevision int64) bool {
	snapshot, err := s.deps.Access.Get(ctx, examID, invocation.Principal().UserID)
	return err == nil && snapshot != nil && snapshot.Exam != nil && snapshot.Draft != nil &&
		!snapshot.Exam.IsArchived() && snapshot.Draft.Revision == expectedRevision
}

func (s *Service) failAudit(ctx context.Context, auditID model.AuditEventID, err error) error {
	mapped := storeErrors.Translate(err)
	code := apperror.CodeOf(mapped, "exam.resource.unavailable")
	if auditErr := s.deps.Auditor.Fail(ctx, auditID, code); auditErr != nil {
		return errors.Join(mapped, auditErr)
	}
	return mapped
}
func (s *Service) effect(ctx context.Context, result *store.ExamResourceCommandResult, operation string) {
	if result.Replayed || result.Value == nil {
		return
	}
	if err := s.deps.Effects.Changed(ctx, result.Value.Resource.ExamID, result.Value.Resource.ID, result.Value.DraftRevision, operation); err != nil {
		s.deps.EffectFailures.Report(ctx, "exam_resource_changed", err)
	}
}
func mapContent(err error) error {
	var capacity workCapacityError
	if errors.As(err, &capacity) {
		return apperror.New("service.busy").Wrap(err)
	}
	var invalidContent invalidContentError
	if errors.As(err, &invalidContent) {
		return apperror.New("exam.resource.content_invalid").Wrap(err)
	}
	return apperror.New("exam.resource.unavailable").Wrap(err)
}

// publicFailures presents every Exam Resource use-case failure: unexpected errors
// become exam.resource.unavailable, and protected targets conceal denial and absence.
var publicFailures = apperror.Concealment{
	Unavailable: "exam.resource.unavailable",
	Concealed:   []apperror.Code{"exam.resource.not_found"},
}

var storeErrors = apperror.StoreTable{
	Constraints: map[string]apperror.Code{
		"exam_archived":            "exam.archived",
		"exam_draft_revision":      "exam.draft.revision_conflict",
		"exam_resource_limit":      "exam.resource.limit",
		"exam_resource_no_changes": "exam.resource.no_changes",
		"exam_resource_order":      "exam.resource.order_invalid",
		"exam_resource_upload":     "exam.resource.upload_invalid",
		"exam_resource_changed":    "exam.resource.revision_conflict",
	},
	NotFound:     "exam.resource.not_found",
	Conflict:     "exam.resource.conflict",
	Invalid:      "exam.resource.invalid",
	InvalidField: "value",
	Unavailable:  "exam.resource.unavailable",
}

func validSHA256(value string) bool {
	if len(value) != 64 || value != strings.ToLower(value) {
		return false
	}
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == 32
}
