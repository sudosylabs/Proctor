// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package workspace

import (
	"context"
	"encoding/hex"
	"errors"
	"io"
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

func (call Call) Principal() model.Principal {
	principal := call.principal
	principal.CredentialScopes = append([]string(nil), principal.CredentialScopes...)
	return principal
}

func (call Call) RequestMetadata() model.RequestMetadata { return call.metadata }

type Fault struct {
	Code       string
	SafeFields map[string]any
	Cause      error
}

func (fault *Fault) Error() string {
	if fault == nil {
		return "Starter Workspace fault"
	}
	return fault.Code
}

func (fault *Fault) Unwrap() error {
	if fault == nil {
		return nil
	}
	return fault.Cause
}

type CreateDirectoryCommand struct {
	ExamID                model.ExamID
	ExpectedDraftRevision int64
	Path                  string
	Idempotency           *store.CommandIdempotency
}

type CreateFileCommand struct {
	ExamID                model.ExamID
	ExpectedDraftRevision int64
	Path                  string
	MediaType             string
	ExpectedSHA256        string
	Body                  io.Reader
	Size                  int64
	Idempotency           *store.CommandIdempotency
}

type MoveEntryCommand struct {
	ExamID                model.ExamID
	EntryID               model.StarterWorkspaceEntryID
	ExpectedDraftRevision int64
	Path                  string
	Idempotency           *store.CommandIdempotency
}

type ReplaceFileCommand struct {
	ExamID                 model.ExamID
	EntryID                model.StarterWorkspaceEntryID
	ExpectedDraftRevision  int64
	ExpectedContentVersion model.WorkspaceContentVersion
	MediaType              string
	ExpectedSHA256         string
	Body                   io.Reader
	Size                   int64
	Idempotency            *store.CommandIdempotency
}

type RemoveEntryCommand struct {
	ExamID                model.ExamID
	EntryID               model.StarterWorkspaceEntryID
	ExpectedDraftRevision int64
	Idempotency           *store.CommandIdempotency
}

type Result struct {
	Entry         model.StarterWorkspaceEntry
	Object        *model.StarterWorkspaceObject
	DraftRevision int64
	Replayed      bool
}

type OpenedFile struct {
	Body           io.ReadCloser
	MediaType      string
	SizeBytes      int64
	SHA256         string
	ContentVersion model.WorkspaceContentVersion
}

type accessStore interface {
	Access(context.Context, model.ExamID, model.UserID) (*store.ExamAccessSnapshot, error)
}

type memberships interface {
	ListActiveByUser(context.Context, string, int64) ([]*model.AcademicUnitMember, error)
}

type Authorizer interface {
	Authorize(context.Context, Call, model.Action, model.Resource) error
}

type Auditor interface {
	Begin(context.Context, Call, model.Action, model.Resource, model.RoleScopeType, string, string, map[string]any, map[string]any) (string, error)
	Fail(context.Context, string, string) error
}

type Content interface {
	StageStarterWorkspaceObject(context.Context, model.StarterWorkspaceObjectID, io.Reader, int64, string) (*model.StarterWorkspaceContent, error)
	OpenStarterWorkspaceObject(context.Context, model.StarterWorkspaceObjectID) (io.ReadCloser, error)
}

type ChangeOperation string

const (
	ChangeDirectoryCreated ChangeOperation = "directory_created"
	ChangeFileCreated      ChangeOperation = "file_created"
	ChangeEntryMoved       ChangeOperation = "entry_moved"
	ChangeFileReplaced     ChangeOperation = "file_replaced"
	ChangeEntryRemoved     ChangeOperation = "entry_removed"
)

type Effects interface {
	Changed(context.Context, model.ExamID, model.StarterWorkspaceEntryID, int64, ChangeOperation, time.Time) error
}

type EffectFailures interface {
	Report(context.Context, string, error)
}

type Service struct {
	persistence store.ExamStarterWorkspaceStore
	access      accessStore
	memberships memberships
	authorizer  Authorizer
	auditor     Auditor
	content     Content
	effects     Effects
	failures    EffectFailures
	now         func() time.Time
	newEntryID  func() model.StarterWorkspaceEntryID
	newObjectID func() model.StarterWorkspaceObjectID
	newVersion  func() model.WorkspaceContentVersion
}

func NewService(persistence store.ExamStarterWorkspaceStore, access accessStore, memberships memberships, authorizer Authorizer, auditor Auditor,
	content Content, effects Effects, failures EffectFailures, now func() time.Time, newEntryID func() model.StarterWorkspaceEntryID,
	newObjectID func() model.StarterWorkspaceObjectID, newVersion func() model.WorkspaceContentVersion) (*Service, error) {
	if persistence == nil || access == nil || memberships == nil || authorizer == nil || auditor == nil || content == nil || effects == nil ||
		failures == nil || now == nil || newEntryID == nil || newObjectID == nil || newVersion == nil {
		return nil, errors.New("Starter Workspace dependencies are required")
	}
	return &Service{persistence: persistence, access: access, memberships: memberships, authorizer: authorizer, auditor: auditor,
		content: content, effects: effects, failures: failures, now: now, newEntryID: newEntryID, newObjectID: newObjectID, newVersion: newVersion}, nil
}

func (service *Service) List(ctx context.Context, call Call, examID model.ExamID) ([]store.ExamStarterWorkspaceItem, error) {
	if _, err := service.authorize(ctx, call, examID, false); err != nil {
		return nil, err
	}
	items, err := service.persistence.List(ctx, examID)
	if err != nil {
		return nil, mapStoreError(err)
	}
	return items, nil
}

func (service *Service) OpenFile(ctx context.Context, call Call, examID model.ExamID, entryID model.StarterWorkspaceEntryID) (*OpenedFile, error) {
	if !entryID.IsValid() {
		return nil, invalid("entry_id")
	}
	if _, err := service.authorize(ctx, call, examID, false); err != nil {
		return nil, err
	}
	item, err := service.persistence.GetFile(ctx, examID, entryID)
	if err != nil {
		return nil, mapStoreError(err)
	}
	if item == nil || item.Object == nil {
		return nil, unavailable(errors.New("Starter Workspace Store returned incomplete file metadata"))
	}
	body, err := service.content.OpenStarterWorkspaceObject(ctx, item.Object.ID)
	if err != nil {
		return nil, unavailable(err)
	}
	return &OpenedFile{Body: body, MediaType: item.Object.MediaType, SizeBytes: item.Object.SizeBytes,
		SHA256: item.Object.SHA256, ContentVersion: item.Object.ContentVersion}, nil
}

func (service *Service) CreateDirectory(ctx context.Context, call Call, command CreateDirectoryCommand) (Result, error) {
	path, err := validateMutation(command.ExamID, "", command.ExpectedDraftRevision, command.Path, command.Idempotency)
	if err != nil {
		return Result{}, err
	}
	authorization, err := service.authorize(ctx, call, command.ExamID, true)
	if err != nil {
		return Result{}, err
	}
	at := model.TimeUTC(service.now())
	entryID := service.newEntryID()
	if !entryID.IsValid() {
		return Result{}, invalid("entry_id")
	}
	mutation := &store.ExamStarterWorkspaceMutation{ExamID: command.ExamID, ActorUserID: call.Principal().UserID,
		ManagerOverride: authorization.override, ExpectedDraftRevision: command.ExpectedDraftRevision,
		ChangedAt: model.MillisFromTime(at), EntryID: entryID, Path: path}
	return service.runMutation(ctx, call, authorization, "directory_create", ChangeDirectoryCreated, mutation, command.Idempotency, service.persistence.CreateDirectory)
}

func (service *Service) CreateFile(ctx context.Context, call Call, command CreateFileCommand) (Result, error) {
	path, err := validateMutation(command.ExamID, "", command.ExpectedDraftRevision, command.Path, command.Idempotency)
	if err != nil || command.Body == nil || command.Size < -1 || command.Size > model.StarterWorkspaceMaximumFileBytes || command.MediaType == "" {
		if err != nil {
			return Result{}, err
		}
		return Result{}, invalid("content")
	}
	authorization, err := service.authorize(ctx, call, command.ExamID, true)
	if err != nil {
		return Result{}, err
	}
	return service.stageAndFinalize(ctx, call, authorization, command.ExamID, "", command.ExpectedDraftRevision, path,
		"", command.MediaType, command.ExpectedSHA256, command.Body, command.Size, command.Idempotency, "file_create", ChangeFileCreated, service.persistence.CreateFile)
}

func (service *Service) MoveEntry(ctx context.Context, call Call, command MoveEntryCommand) (Result, error) {
	path, err := validateMutation(command.ExamID, command.EntryID, command.ExpectedDraftRevision, command.Path, command.Idempotency)
	if err != nil {
		return Result{}, err
	}
	authorization, err := service.authorize(ctx, call, command.ExamID, true)
	if err != nil {
		return Result{}, err
	}
	at := model.TimeUTC(service.now())
	mutation := &store.ExamStarterWorkspaceMutation{ExamID: command.ExamID, ActorUserID: call.Principal().UserID,
		ManagerOverride: authorization.override, ExpectedDraftRevision: command.ExpectedDraftRevision,
		ChangedAt: model.MillisFromTime(at), EntryID: command.EntryID, Path: path}
	return service.runMutation(ctx, call, authorization, "entry_move", ChangeEntryMoved, mutation, command.Idempotency, service.persistence.MoveEntry)
}

func (service *Service) ReplaceFile(ctx context.Context, call Call, command ReplaceFileCommand) (Result, error) {
	if err := validateMutationBase(command.ExamID, command.EntryID, command.ExpectedDraftRevision, command.Idempotency); err != nil {
		return Result{}, err
	}
	if !command.ExpectedContentVersion.IsValid() || command.Body == nil || command.Size < -1 || command.Size > model.StarterWorkspaceMaximumFileBytes || command.MediaType == "" {
		return Result{}, invalid("content")
	}
	authorization, err := service.authorize(ctx, call, command.ExamID, true)
	if err != nil {
		return Result{}, err
	}
	return service.stageAndFinalize(ctx, call, authorization, command.ExamID, command.EntryID, command.ExpectedDraftRevision, "",
		command.ExpectedContentVersion, command.MediaType, command.ExpectedSHA256, command.Body, command.Size, command.Idempotency, "file_replace", ChangeFileReplaced, service.persistence.ReplaceFile)
}

func (service *Service) RemoveEntry(ctx context.Context, call Call, command RemoveEntryCommand) (Result, error) {
	if err := validateMutationBase(command.ExamID, command.EntryID, command.ExpectedDraftRevision, command.Idempotency); err != nil {
		return Result{}, err
	}
	authorization, err := service.authorize(ctx, call, command.ExamID, true)
	if err != nil {
		return Result{}, err
	}
	at := model.TimeUTC(service.now())
	mutation := &store.ExamStarterWorkspaceMutation{ExamID: command.ExamID, ActorUserID: call.Principal().UserID,
		ManagerOverride: authorization.override, ExpectedDraftRevision: command.ExpectedDraftRevision,
		ChangedAt: model.MillisFromTime(at), EntryID: command.EntryID}
	return service.runMutation(ctx, call, authorization, "entry_remove", ChangeEntryRemoved, mutation, command.Idempotency, service.persistence.RemoveEntry)
}

type authorization struct {
	action   model.Action
	unitID   model.AcademicUnitID
	override bool
}

func (service *Service) authorize(ctx context.Context, call Call, examID model.ExamID, mutation bool) (authorization, error) {
	principal := call.Principal()
	if principal.Validate() != nil || !examID.IsValid() {
		return authorization{}, invalid("exam_id")
	}
	access, err := service.access.Access(ctx, examID, principal.UserID)
	if err != nil {
		return authorization{}, mapStoreError(err)
	}
	if access == nil || access.Exam == nil {
		return authorization{}, unavailable(errors.New("Exam access projection is incomplete"))
	}
	action, override := model.ActionExamViewOverride, true
	if mutation {
		action = model.ActionExamManageOverride
	}
	if access.ActorIsManager {
		memberships, listErr := service.memberships.ListActiveByUser(ctx, principal.UserID.String(), model.MillisFromTime(model.TimeUTC(service.now())))
		if listErr != nil {
			return authorization{}, unavailable(listErr)
		}
		for _, membership := range memberships {
			if membership != nil && membership.AcademicUnitID == access.Exam.AcademicUnitID {
				override = false
				if mutation {
					action = model.ActionExamManage
				} else {
					action = model.ActionExamView
				}
				break
			}
		}
	}
	resource := model.Resource{Type: model.ResourceExam, ID: examID.String()}
	if err := service.authorizer.Authorize(ctx, call, action, resource); err != nil {
		return authorization{}, err
	}
	return authorization{action: action, unitID: access.Exam.AcademicUnitID, override: override}, nil
}

type mutationRunner func(context.Context, *store.ExamStarterWorkspaceMutation, *store.CommandIdempotency) (*store.ExamStarterWorkspaceMutationResult, error)

func (service *Service) runMutation(ctx context.Context, call Call, authorization authorization, auditOperation string, effectOperation ChangeOperation,
	mutation *store.ExamStarterWorkspaceMutation, idempotency *store.CommandIdempotency, run mutationRunner) (Result, error) {
	auditID, err := service.auditor.Begin(ctx, call, authorization.action, model.Resource{Type: model.ResourceExam, ID: mutation.ExamID.String()},
		model.RoleScopeAcademicUnit, authorization.unitID.String(), auditOperation,
		map[string]any{"exam_id": mutation.ExamID.String(), "entry_id": mutation.EntryID.String(), "expected_draft_revision": mutation.ExpectedDraftRevision}, nil)
	if err != nil {
		return Result{}, err
	}
	mutation.AuditEventID = auditID
	mutation.AuditAt = mutation.ChangedAt
	result, err := run(ctx, mutation, idempotency)
	if err != nil {
		mapped := mapStoreError(err)
		if failErr := service.auditor.Fail(ctx, auditID, faultCodeForAudit(mapped)); failErr != nil {
			return Result{}, failErr
		}
		return Result{}, mapped
	}
	if result == nil || result.Entry == nil || result.DraftRevision < 1 {
		return Result{}, unavailable(errors.New("Starter Workspace Store returned incomplete mutation result"))
	}
	if !result.Replayed {
		if effectErr := service.effects.Changed(ctx, mutation.ExamID, result.Entry.ID, result.DraftRevision, effectOperation, model.TimeFromMillis(mutation.ChangedAt)); effectErr != nil {
			service.failures.Report(ctx, "exam_starter_workspace_"+string(effectOperation), effectErr)
		}
	}
	return Result{Entry: *result.Entry, Object: result.Object, DraftRevision: result.DraftRevision, Replayed: result.Replayed}, nil
}

func (service *Service) stageAndFinalize(ctx context.Context, call Call, authorization authorization, examID model.ExamID, entryID model.StarterWorkspaceEntryID,
	expectedRevision int64, path string, expectedContentVersion model.WorkspaceContentVersion, mediaType, expectedSHA256 string, body io.Reader, size int64, idempotency *store.CommandIdempotency,
	auditOperation string, effectOperation ChangeOperation, run mutationRunner) (Result, error) {
	at := model.TimeUTC(service.now())
	if entryID.IsZero() {
		entryID = service.newEntryID()
	}
	objectID, version := service.newObjectID(), service.newVersion()
	if !entryID.IsValid() || !objectID.IsValid() || !version.IsValid() {
		return Result{}, invalid("identity")
	}
	object, err := model.NewStagedStarterWorkspaceObject(objectID, examID, call.Principal().UserID, at, at.Add(model.StarterWorkspaceUploadLease))
	if err != nil {
		return Result{}, invalidCause("object", err)
	}
	_, err = service.persistence.ReserveObject(ctx, &store.ExamStarterWorkspaceReservation{Object: object})
	if err != nil {
		return Result{}, mapStoreError(err)
	}
	staged, err := service.content.StageStarterWorkspaceObject(ctx, objectID, body, size, mediaType)
	if err != nil {
		_ = service.persistence.MarkObjectReclaimable(ctx, objectID, at)
		if isInvalidContentError(err) {
			return Result{}, invalidCause("content", err)
		}
		return Result{}, unavailable(err)
	}
	if !validSHA256(expectedSHA256) || staged.SHA256 != expectedSHA256 {
		_ = service.persistence.MarkObjectReclaimable(ctx, objectID, at)
		return Result{}, invalid("sha256")
	}
	mutation := &store.ExamStarterWorkspaceMutation{ExamID: examID, ActorUserID: call.Principal().UserID, ManagerOverride: authorization.override,
		ExpectedDraftRevision: expectedRevision, ChangedAt: model.MillisFromTime(at), EntryID: entryID, Path: path, ObjectID: objectID,
		ExpectedContentVersion: expectedContentVersion, ContentVersion: version, MediaType: staged.MediaType, SizeBytes: staged.SizeBytes, SHA256: staged.SHA256}
	result, err := service.runMutation(ctx, call, authorization, auditOperation, effectOperation, mutation, idempotency, run)
	if err != nil {
		if !isUnavailableFault(err) {
			_ = service.persistence.MarkObjectReclaimable(ctx, objectID, at)
		}
		return Result{}, err
	}
	if result.Replayed {
		_ = service.persistence.MarkObjectReclaimable(ctx, objectID, at)
	}
	return result, nil
}

func validSHA256(value string) bool {
	if len(value) != 64 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func validateMutation(examID model.ExamID, entryID model.StarterWorkspaceEntryID, revision int64, path string, idempotency *store.CommandIdempotency) (string, error) {
	if err := validateMutationBase(examID, entryID, revision, idempotency); err != nil {
		return "", err
	}
	normalized, err := NormalizePath(path)
	if err != nil || normalized != path {
		return "", invalidCause("path", err)
	}
	return normalized, nil
}

func validateMutationBase(examID model.ExamID, entryID model.StarterWorkspaceEntryID, revision int64, idempotency *store.CommandIdempotency) error {
	if !examID.IsValid() || revision < 1 || idempotency == nil || !entryID.IsZero() && !entryID.IsValid() {
		return invalid("command")
	}
	return nil
}

func mapStoreError(err error) error {
	var idempotencyConflict *store.ErrIdempotencyConflict
	var idempotencyInProgress *store.ErrIdempotencyInProgress
	var conflict *store.ErrConflict
	var invalidInput *store.ErrInvalidInput
	switch {
	case errors.As(err, &idempotencyConflict):
		return &Fault{Code: "idempotency.conflict", Cause: err}
	case errors.As(err, &idempotencyInProgress):
		return &Fault{Code: "idempotency.in_progress", Cause: err}
	case store.IsNotFound(err):
		return &Fault{Code: "exam.starter_workspace.not_found", Cause: err}
	case errors.As(err, &conflict):
		codes := map[string]string{
			"exam_archived": "exam.archived", "exam_draft_revision": "exam.draft.revision_conflict",
			"workspace_path_collision": "exam.starter_workspace.path_conflict", "workspace_parent_missing": "exam.starter_workspace.parent_not_found",
			"workspace_directory_not_empty": "exam.starter_workspace.directory_not_empty", "workspace_entry_limit": "exam.starter_workspace.entry_limit",
			"workspace_total_size": "exam.starter_workspace.total_size_limit", "workspace_object_expired": "exam.starter_workspace.upload_expired",
			"workspace_object_state": "exam.starter_workspace.object_conflict", "workspace_no_changes": "exam.starter_workspace.no_changes",
			"workspace_descendant_move": "exam.starter_workspace.invalid_move", "workspace_path_limit": "exam.starter_workspace.invalid_move",
			"workspace_entry_kind": "exam.starter_workspace.entry_kind", "workspace_content_version": "exam.starter_workspace.content_conflict",
		}
		if code := codes[conflict.Constraint]; code != "" {
			return &Fault{Code: code, Cause: err}
		}
		return &Fault{Code: "exam.starter_workspace.conflict", Cause: err}
	case errors.As(err, &invalidInput):
		return &Fault{Code: "exam.starter_workspace.invalid", Cause: err}
	default:
		return unavailable(err)
	}
}

func invalid(field string) error {
	return &Fault{Code: "exam.starter_workspace.invalid", SafeFields: map[string]any{"field": field}}
}
func invalidCause(field string, cause error) error {
	return &Fault{Code: "exam.starter_workspace.invalid", SafeFields: map[string]any{"field": field}, Cause: cause}
}
func unavailable(cause error) error {
	return &Fault{Code: "exam.starter_workspace.unavailable", Cause: cause}
}
func isUnavailableFault(err error) bool {
	var fault *Fault
	return errors.As(err, &fault) && fault.Code == "exam.starter_workspace.unavailable"
}

func isInvalidContentError(err error) bool {
	var invalid interface{ InvalidStarterWorkspaceContent() bool }
	return errors.As(err, &invalid) && invalid.InvalidStarterWorkspaceContent()
}
func faultCodeForAudit(err error) string {
	var fault *Fault
	if errors.As(err, &fault) {
		return fault.Code
	}
	return "exam.starter_workspace.unavailable"
}
