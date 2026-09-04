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

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

type Call struct {
	principal model.Principal
	metadata  model.RequestMetadata
}

func NewCall(principal model.Principal, metadata model.RequestMetadata) Call {
	principal.CredentialScopes = append([]string(nil), principal.CredentialScopes...)
	return Call{principal: principal, metadata: metadata}
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
		return "exam resource fault"
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
	Get(context.Context, model.ExamID, model.UserID) (*store.ExamAuthoringSnapshot, error)
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

type Service struct {
	persistence   store.ExamResourceStore
	access        AccessStore
	memberships   Memberships
	authorizer    Authorizer
	auditor       Auditor
	effects       Effects
	failures      EffectFailures
	content       FileContent
	now           func() time.Time
	newResourceID func() model.ExamResourceID
	newEntryID    func() model.FileEntryID
	newRevisionID func() model.FileRevisionID
	newLeaseID    func() model.UploadLeaseID
}

func New(persistence store.ExamResourceStore, access AccessStore, memberships Memberships, authorizer Authorizer, auditor Auditor, effects Effects, failures EffectFailures, content FileContent, now func() time.Time, newResourceID func() model.ExamResourceID, newEntryID func() model.FileEntryID, newRevisionID func() model.FileRevisionID, newLeaseID func() model.UploadLeaseID) (*Service, error) {
	if persistence == nil || access == nil || memberships == nil || authorizer == nil || auditor == nil || effects == nil || failures == nil || content == nil || now == nil || newResourceID == nil || newEntryID == nil || newRevisionID == nil || newLeaseID == nil {
		return nil, errors.New("exam resource dependencies are required")
	}
	return &Service{persistence: persistence, access: access, memberships: memberships, authorizer: authorizer, auditor: auditor, effects: effects, failures: failures, content: content, now: now, newResourceID: newResourceID, newEntryID: newEntryID, newRevisionID: newRevisionID, newLeaseID: newLeaseID}, nil
}

func (s *Service) List(ctx context.Context, call Call, examID model.ExamID) ([]store.ExamResourceRecord, error) {
	if !examID.IsValid() {
		return nil, invalid("exam_id")
	}
	if _, err := s.authorize(ctx, call, examID, false); err != nil {
		return nil, err
	}
	items, err := s.persistence.List(ctx, examID)
	if err != nil {
		return nil, mapStore(err)
	}
	return items, nil
}
func (s *Service) Open(ctx context.Context, call Call, examID model.ExamID, resourceID model.ExamResourceID) (Opened, error) {
	if !examID.IsValid() || !resourceID.IsValid() {
		return Opened{}, invalid("identity")
	}
	if _, err := s.authorize(ctx, call, examID, false); err != nil {
		return Opened{}, err
	}
	record, err := s.persistence.Get(ctx, examID, resourceID)
	if err != nil {
		return Opened{}, mapStore(err)
	}
	if record == nil || record.Resource == nil || record.Rendition == nil {
		return Opened{}, unavailable(errors.New("incomplete resource record"))
	}
	body, err := s.content.OpenExamResource(ctx, record.Resource.SelectedFileRevisionID, record.Rendition.ID)
	if err != nil {
		return Opened{}, unavailable(err)
	}
	return Opened{Record: *record, Body: body}, nil
}

func (s *Service) Create(ctx context.Context, call Call, command CreateCommand) (store.ExamResourceRecord, error) {
	if !command.ExamID.IsValid() || command.ExpectedDraftRevision < 1 || command.Body == nil || command.Size < 0 || command.Size > model.ExamResourceMaximumBytes || !command.MediaType.IsValid() || !validSHA256(command.ExpectedSHA256) {
		return store.ExamResourceRecord{}, invalid("upload")
	}
	command.DisplayName = strings.TrimSpace(command.DisplayName)
	idempotency, err := prepareResourceIdempotency(call, idempotencyOperationAddResource, command.IdempotencyKey, command.ExamID, command.ExpectedDraftRevision, "", command.DisplayName, command.DescriptionMarkdown, command.MediaType, command.Size, command.ExpectedSHA256, nil)
	if err != nil {
		return store.ExamResourceRecord{}, err
	}
	authorization, err := s.authorize(ctx, call, command.ExamID, true)
	if err != nil {
		return store.ExamResourceRecord{}, err
	}
	items, err := s.persistence.List(ctx, command.ExamID)
	if err != nil {
		return store.ExamResourceRecord{}, mapStore(err)
	}
	position := min(len(items), model.ExamResourceMaximumCount-1)
	at := model.TimeUTC(s.now())
	principal := call.Principal()
	resourceID, entryID, revisionID, leaseID := s.newResourceID(), s.newEntryID(), s.newRevisionID(), s.newLeaseID()
	entry, err := model.NewFileEntryForPurpose(entryID, model.FilePurposeExamResource, model.FileIndexingNone, at)
	if err != nil {
		return store.ExamResourceRecord{}, invalidCause("file_entry", err)
	}
	revision, err := model.NewFileRevision(revisionID, entryID, model.FileAvailabilityPending, model.FileIndexingNotRequired, at)
	if err != nil {
		return store.ExamResourceRecord{}, invalidCause("file_revision", err)
	}
	lease, err := model.NewUploadLease(leaseID, revisionID, principal.UserID, at, at.Add(time.Hour))
	if err != nil {
		return store.ExamResourceRecord{}, invalidCause("upload_lease", err)
	}
	resource, err := model.NewExamResource(resourceID, command.ExamID, entryID, revisionID, command.DisplayName, command.DescriptionMarkdown, position, at)
	if err != nil {
		return store.ExamResourceRecord{}, invalidCause("metadata", err)
	}
	if _, err = s.persistence.ReserveUpload(ctx, &store.ExamResourceUploadReservation{ExamID: command.ExamID, ActorUserID: principal.UserID, ManagerOverride: authorization.override, ExpectedDraftRevision: command.ExpectedDraftRevision, ResourceID: resourceID, Entry: entry, Revision: revision, Lease: lease}); err != nil {
		return store.ExamResourceRecord{}, mapStore(err)
	}
	rendition, err := s.content.StoreExamResource(ctx, revisionID, command.MediaType, command.Body, command.Size, at)
	if err != nil {
		return store.ExamResourceRecord{}, mapContent(err)
	}
	if subtle.ConstantTimeCompare([]byte(rendition.SHA256), []byte(command.ExpectedSHA256)) != 1 {
		_ = s.content.RemoveExamResource(ctx, rendition.RevisionID, rendition.ID)
		return store.ExamResourceRecord{}, &Fault{Code: "exam.resource.invalid_content", Cause: errors.New("content checksum mismatch")}
	}
	return s.finalize(ctx, call, authorization, resource, leaseID, &rendition, command.ExpectedDraftRevision, idempotency, "add")
}

func (s *Service) ReplaceContent(ctx context.Context, call Call, command ReplaceContentCommand) (store.ExamResourceRecord, error) {
	if !command.ExamID.IsValid() || !command.ResourceID.IsValid() || command.ExpectedDraftRevision < 1 || command.Body == nil || command.Size < 0 || command.Size > model.ExamResourceMaximumBytes || !command.MediaType.IsValid() || !validSHA256(command.ExpectedSHA256) {
		return store.ExamResourceRecord{}, invalid("upload")
	}
	idempotency, err := prepareResourceIdempotency(call, idempotencyOperationReplaceResourceContent, command.IdempotencyKey, command.ExamID, command.ExpectedDraftRevision, command.ResourceID.String(), "", "", command.MediaType, command.Size, command.ExpectedSHA256, nil)
	if err != nil {
		return store.ExamResourceRecord{}, err
	}
	authorization, err := s.authorize(ctx, call, command.ExamID, true)
	if err != nil {
		return store.ExamResourceRecord{}, err
	}
	current, err := s.persistence.Get(ctx, command.ExamID, command.ResourceID)
	if err != nil {
		return store.ExamResourceRecord{}, mapStore(err)
	}
	if current == nil || current.Resource == nil || current.Rendition == nil {
		return store.ExamResourceRecord{}, unavailable(errors.New("incomplete resource record"))
	}
	at := model.TimeUTC(s.now())
	principal := call.Principal()
	revisionID, leaseID := s.newRevisionID(), s.newLeaseID()
	revision, err := model.NewFileRevision(revisionID, current.Resource.FileEntryID, model.FileAvailabilityPending, model.FileIndexingNotRequired, at)
	if err != nil {
		return store.ExamResourceRecord{}, invalidCause("file_revision", err)
	}
	lease, err := model.NewUploadLease(leaseID, revisionID, principal.UserID, at, at.Add(time.Hour))
	if err != nil {
		return store.ExamResourceRecord{}, invalidCause("upload_lease", err)
	}
	if _, err = s.persistence.ReserveUpload(ctx, &store.ExamResourceUploadReservation{ExamID: command.ExamID, ActorUserID: principal.UserID, ManagerOverride: authorization.override, ExpectedDraftRevision: command.ExpectedDraftRevision, ResourceID: command.ResourceID, EntryID: current.Resource.FileEntryID, Revision: revision, Lease: lease, Replacement: true}); err != nil {
		return store.ExamResourceRecord{}, mapStore(err)
	}
	rendition, err := s.content.StoreExamResource(ctx, revisionID, command.MediaType, command.Body, command.Size, at)
	if err != nil {
		return store.ExamResourceRecord{}, mapContent(err)
	}
	if subtle.ConstantTimeCompare([]byte(rendition.SHA256), []byte(command.ExpectedSHA256)) != 1 {
		_ = s.content.RemoveExamResource(ctx, rendition.RevisionID, rendition.ID)
		return store.ExamResourceRecord{}, &Fault{Code: "exam.resource.invalid_content", Cause: errors.New("content checksum mismatch")}
	}
	updated := *current.Resource
	if _, err = updated.ReplaceContent(revisionID, at); err != nil {
		return store.ExamResourceRecord{}, invalidCause("content", err)
	}
	return s.finalize(ctx, call, authorization, &updated, leaseID, &rendition, command.ExpectedDraftRevision, idempotency, "replace_content")
}

type authorizationDecision struct {
	override       bool
	academicUnitID model.AcademicUnitID
}

func (s *Service) finalize(ctx context.Context, call Call, authorization authorizationDecision, resource *model.ExamResource, leaseID model.UploadLeaseID, rendition *model.FileRendition, expected int64, idempotency *store.CommandIdempotency, operation string) (store.ExamResourceRecord, error) {
	action := model.ActionExamManage
	if authorization.override {
		action = model.ActionExamManageOverride
	}
	auditID, err := s.auditor.Begin(ctx, call, action, model.Resource{Type: model.ResourceExam, ID: resource.ExamID.String()}, model.RoleScopeAcademicUnit, authorization.academicUnitID.String(), operation, map[string]any{"exam_id": resource.ExamID.String(), "exam_resource_id": resource.ID.String(), "file_revision_id": resource.SelectedFileRevisionID.String()}, nil)
	if err != nil {
		_ = s.content.RemoveExamResource(ctx, rendition.RevisionID, rendition.ID)
		return store.ExamResourceRecord{}, err
	}
	at := model.TimeUTC(s.now())
	result, err := s.persistence.FinalizeUpload(ctx, &store.ExamResourceUploadFinalization{ExamID: resource.ExamID, ActorUserID: call.Principal().UserID, ManagerOverride: authorization.override, ExpectedDraftRevision: expected, Resource: resource, LeaseID: leaseID, Rendition: rendition, ChangedAt: at, AuditEventID: auditID, AuditAt: model.MillisFromTime(at)}, idempotency)
	if err != nil {
		return store.ExamResourceRecord{}, s.failAudit(ctx, auditID, err)
	}
	if result == nil || result.Value == nil {
		return store.ExamResourceRecord{}, unavailable(errors.New("missing resource outcome"))
	}
	s.effect(ctx, result, operation)
	return *result.Value, nil
}

func (s *Service) EditMetadata(ctx context.Context, call Call, command EditMetadataCommand) (store.ExamResourceRecord, error) {
	if !command.ExamID.IsValid() || !command.ResourceID.IsValid() || command.ExpectedDraftRevision < 1 || command.DisplayName == nil && command.DescriptionMarkdown == nil {
		return store.ExamResourceRecord{}, invalid("identity")
	}
	command.DisplayName = cloneStringPointer(command.DisplayName)
	if command.DisplayName != nil {
		*command.DisplayName = strings.TrimSpace(*command.DisplayName)
	}
	command.DescriptionMarkdown = cloneStringPointer(command.DescriptionMarkdown)
	idempotency, err := prepareMetadataIdempotency(call, command)
	if err != nil {
		return store.ExamResourceRecord{}, err
	}
	authorization, err := s.authorize(ctx, call, command.ExamID, true)
	if err != nil {
		return store.ExamResourceRecord{}, err
	}
	current, err := s.persistence.Get(ctx, command.ExamID, command.ResourceID)
	if err != nil {
		return store.ExamResourceRecord{}, mapStore(err)
	}
	if current == nil || current.Resource == nil {
		return store.ExamResourceRecord{}, unavailable(errors.New("incomplete resource record"))
	}
	candidate := *current.Resource
	displayName, descriptionMarkdown := candidate.DisplayName, candidate.DescriptionMarkdown
	if command.DisplayName != nil {
		displayName = *command.DisplayName
	}
	if command.DescriptionMarkdown != nil {
		descriptionMarkdown = *command.DescriptionMarkdown
	}
	changed, validationErr := candidate.ApplyMetadata(displayName, descriptionMarkdown, model.TimeUTC(s.now()))
	if validationErr != nil {
		return store.ExamResourceRecord{}, invalidCause("metadata", validationErr)
	}
	if !changed && s.currentActiveDraft(ctx, call, command.ExamID, command.ExpectedDraftRevision) {
		return store.ExamResourceRecord{}, &Fault{Code: "exam.resource.no_changes"}
	}
	return s.metadataMutation(ctx, call, authorization, command, idempotency)
}
func (s *Service) metadataMutation(ctx context.Context, call Call, authorization authorizationDecision, c EditMetadataCommand, idempotency *store.CommandIdempotency) (store.ExamResourceRecord, error) {
	at := model.TimeUTC(s.now())
	action := model.ActionExamManage
	if authorization.override {
		action = model.ActionExamManageOverride
	}
	auditID, err := s.auditor.Begin(ctx, call, action, model.Resource{Type: model.ResourceExam, ID: c.ExamID.String()}, model.RoleScopeAcademicUnit, authorization.academicUnitID.String(), "edit_resource_metadata", map[string]any{"exam_id": c.ExamID.String(), "exam_resource_id": c.ResourceID.String()}, nil)
	if err != nil {
		return store.ExamResourceRecord{}, err
	}
	displayName, descriptionMarkdown := "", ""
	current, err := s.persistence.Get(ctx, c.ExamID, c.ResourceID)
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
	result, err := s.persistence.UpdateMetadata(ctx, &store.ExamResourceMetadataUpdate{ExamID: c.ExamID, ActorUserID: call.Principal().UserID, ManagerOverride: authorization.override, ExpectedDraftRevision: c.ExpectedDraftRevision, ResourceID: c.ResourceID, DisplayName: displayName, DescriptionMarkdown: descriptionMarkdown, ChangedAt: at, AuditEventID: auditID, AuditAt: model.MillisFromTime(at)}, idempotency)
	if err != nil {
		return store.ExamResourceRecord{}, s.failAudit(ctx, auditID, err)
	}
	if result == nil || result.Value == nil {
		return store.ExamResourceRecord{}, unavailable(errors.New("missing metadata outcome"))
	}
	s.effect(ctx, result, "edit_metadata")
	return *result.Value, nil
}

func (s *Service) Reorder(ctx context.Context, call Call, c ReorderCommand) ([]store.ExamResourceRecord, error) {
	if !c.ExamID.IsValid() || c.ExpectedDraftRevision < 1 {
		return nil, invalid("identity")
	}
	if len(c.ResourceIDs) > model.ExamResourceMaximumCount {
		return nil, invalid("resource_ids")
	}
	seen := make(map[model.ExamResourceID]struct{}, len(c.ResourceIDs))
	for _, id := range c.ResourceIDs {
		if !id.IsValid() {
			return nil, invalid("resource_ids")
		}
		if _, exists := seen[id]; exists {
			return nil, invalid("resource_ids")
		}
		seen[id] = struct{}{}
	}
	c.ResourceIDs = append([]model.ExamResourceID(nil), c.ResourceIDs...)
	ids := make([]string, len(c.ResourceIDs))
	for index, id := range c.ResourceIDs {
		ids[index] = id.String()
	}
	idempotency, err := prepareResourceIdempotency(call, idempotencyOperationReorderResources, c.IdempotencyKey, c.ExamID, c.ExpectedDraftRevision, "", "", "", "", 0, "", ids)
	if err != nil {
		return nil, err
	}
	authorization, err := s.authorize(ctx, call, c.ExamID, true)
	if err != nil {
		return nil, err
	}
	current, err := s.persistence.List(ctx, c.ExamID)
	if err != nil {
		return nil, mapStore(err)
	}
	if len(current) == len(c.ResourceIDs) {
		same := true
		for index := range current {
			if current[index].Resource == nil || current[index].Resource.ID != c.ResourceIDs[index] {
				same = false
				break
			}
		}
		if same && s.currentActiveDraft(ctx, call, c.ExamID, c.ExpectedDraftRevision) {
			return nil, &Fault{Code: "exam.resource.no_changes"}
		}
	}
	at := model.TimeUTC(s.now())
	action := model.ActionExamManage
	if authorization.override {
		action = model.ActionExamManageOverride
	}
	auditID, err := s.auditor.Begin(ctx, call, action, model.Resource{Type: model.ResourceExam, ID: c.ExamID.String()}, model.RoleScopeAcademicUnit, authorization.academicUnitID.String(), "reorder_resources", map[string]any{"exam_id": c.ExamID.String(), "resource_count": len(c.ResourceIDs)}, nil)
	if err != nil {
		return nil, err
	}
	result, err := s.persistence.Reorder(ctx, &store.ExamResourceReorder{ExamID: c.ExamID, ActorUserID: call.Principal().UserID, ManagerOverride: authorization.override, ExpectedDraftRevision: c.ExpectedDraftRevision, ResourceIDs: append([]model.ExamResourceID(nil), c.ResourceIDs...), ChangedAt: at, AuditEventID: auditID, AuditAt: model.MillisFromTime(at)}, idempotency)
	if err != nil {
		return nil, s.failAudit(ctx, auditID, err)
	}
	if result == nil {
		return nil, unavailable(errors.New("missing reorder outcome"))
	}
	if !result.Replayed {
		if result.DraftRevision < 1 {
			s.failures.Report(ctx, "exam_resource_changed", errors.New("missing committed Draft revision"))
		} else if effectErr := s.effects.Changed(ctx, c.ExamID, "", result.DraftRevision, "reorder"); effectErr != nil {
			s.failures.Report(ctx, "exam_resource_changed", effectErr)
		}
	}
	return result.Items, nil
}

func (s *Service) Remove(ctx context.Context, call Call, c RemoveCommand) (store.ExamResourceRecord, error) {
	if !c.ExamID.IsValid() || !c.ResourceID.IsValid() || c.ExpectedDraftRevision < 1 {
		return store.ExamResourceRecord{}, invalid("identity")
	}
	idempotency, err := prepareResourceIdempotency(call, idempotencyOperationRemoveResource, c.IdempotencyKey, c.ExamID, c.ExpectedDraftRevision, c.ResourceID.String(), "", "", "", 0, "", nil)
	if err != nil {
		return store.ExamResourceRecord{}, err
	}
	authorization, err := s.authorize(ctx, call, c.ExamID, true)
	if err != nil {
		return store.ExamResourceRecord{}, err
	}
	at := model.TimeUTC(s.now())
	action := model.ActionExamManage
	if authorization.override {
		action = model.ActionExamManageOverride
	}
	auditID, err := s.auditor.Begin(ctx, call, action, model.Resource{Type: model.ResourceExam, ID: c.ExamID.String()}, model.RoleScopeAcademicUnit, authorization.academicUnitID.String(), "remove_resource", map[string]any{"exam_id": c.ExamID.String(), "exam_resource_id": c.ResourceID.String()}, nil)
	if err != nil {
		return store.ExamResourceRecord{}, err
	}
	result, err := s.persistence.Remove(ctx, &store.ExamResourceRemoval{ExamID: c.ExamID, ActorUserID: call.Principal().UserID, ManagerOverride: authorization.override, ExpectedDraftRevision: c.ExpectedDraftRevision, ResourceID: c.ResourceID, ChangedAt: at, AuditEventID: auditID, AuditAt: model.MillisFromTime(at)}, idempotency)
	if err != nil {
		return store.ExamResourceRecord{}, s.failAudit(ctx, auditID, err)
	}
	if result == nil || result.Value == nil {
		return store.ExamResourceRecord{}, unavailable(errors.New("missing removal outcome"))
	}
	s.effect(ctx, result, "remove")
	return *result.Value, nil
}

func (s *Service) authorize(ctx context.Context, call Call, examID model.ExamID, manage bool) (authorizationDecision, error) {
	principal := call.Principal()
	if principal.Validate() != nil {
		return authorizationDecision{}, invalid("principal")
	}
	access, err := s.access.Access(ctx, examID, principal.UserID)
	if err != nil {
		return authorizationDecision{}, mapStore(err)
	}
	if access == nil || access.Exam == nil {
		return authorizationDecision{}, unavailable(errors.New("missing access"))
	}
	ordinary := false
	if access.ActorIsManager {
		members, memberErr := s.memberships.ListActiveByUser(ctx, principal.UserID.String(), model.MillisFromTime(s.now()))
		if memberErr != nil {
			return authorizationDecision{}, unavailable(memberErr)
		}
		for _, member := range members {
			if member != nil && member.AcademicUnitID == access.Exam.AcademicUnitID {
				ordinary = true
				break
			}
		}
	}
	action := model.ActionExamView
	if manage {
		action = model.ActionExamManage
	}
	if !ordinary {
		if manage {
			action = model.ActionExamManageOverride
		} else {
			action = model.ActionExamViewOverride
		}
	}
	if err = s.authorizer.Authorize(ctx, call, action, model.Resource{Type: model.ResourceExam, ID: examID.String()}); err != nil {
		return authorizationDecision{}, err
	}
	return authorizationDecision{override: !ordinary, academicUnitID: access.Exam.AcademicUnitID}, nil
}

// currentActiveDraft permits an unaudited no-op only after a bounded
// authoritative read confirms the exact active Draft generation. Apparent
// no-ops against archived or stale state must reach the audited named Store
// command so its locked guards remain authoritative.
func (s *Service) currentActiveDraft(ctx context.Context, call Call, examID model.ExamID, expectedRevision int64) bool {
	snapshot, err := s.access.Get(ctx, examID, call.Principal().UserID)
	return err == nil && snapshot != nil && snapshot.Exam != nil && snapshot.Draft != nil &&
		!snapshot.Exam.IsArchived() && snapshot.Draft.Revision == expectedRevision
}

func (s *Service) failAudit(ctx context.Context, auditID string, err error) error {
	mapped := mapStore(err)
	var fault *Fault
	code := "exam.resource.unavailable"
	if errors.As(mapped, &fault) {
		code = fault.Code
	}
	if auditErr := s.auditor.Fail(ctx, auditID, code); auditErr != nil {
		return auditErr
	}
	return mapped
}
func (s *Service) effect(ctx context.Context, result *store.ExamResourceCommandResult, operation string) {
	if result.Replayed || result.Value == nil {
		return
	}
	if err := s.effects.Changed(ctx, result.Value.Resource.ExamID, result.Value.Resource.ID, result.Value.DraftRevision, operation); err != nil {
		s.failures.Report(ctx, "exam_resource_changed", err)
	}
}
func invalid(field string) error {
	return &Fault{Code: "exam.resource.invalid", SafeFields: map[string]any{"field": field}}
}
func invalidCause(field string, cause error) error {
	return &Fault{Code: "exam.resource.invalid", SafeFields: map[string]any{"field": field}, Cause: cause}
}
func unavailable(cause error) error { return &Fault{Code: "exam.resource.unavailable", Cause: cause} }
func mapContent(err error) error {
	var invalidContent invalidContentError
	if errors.As(err, &invalidContent) {
		return &Fault{Code: "exam.resource.invalid_content", Cause: err}
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
		return &Fault{Code: "exam.resource.not_found", Cause: err}
	case errors.As(err, &conflict):
		codes := map[string]string{"exam_archived": "exam.archived", "exam_draft_revision": "exam.draft.revision_conflict", "exam_resource_limit": "exam.resource.limit", "exam_resource_no_changes": "exam.resource.no_changes", "exam_resource_order": "exam.resource.order_invalid", "exam_resource_upload": "exam.resource.upload_invalid", "exam_resource_changed": "exam.resource.revision_conflict"}
		if code := codes[conflict.Constraint]; code != "" {
			return &Fault{Code: code, Cause: err}
		}
		return &Fault{Code: "exam.resource.conflict", Cause: err}
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
