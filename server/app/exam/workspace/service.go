// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package workspace

import (
	"context"
	"encoding/hex"
	"errors"
	"io"
	"time"

	"github.com/sudosylabs/proctor/server/app/apperror"
	"github.com/sudosylabs/proctor/server/app/exam/manageraccess"
	applicationidempotency "github.com/sudosylabs/proctor/server/app/idempotency"
	"github.com/sudosylabs/proctor/server/app/invocation"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

type CreateDirectoryCommand struct {
	ExamID                model.ExamID
	ExpectedDraftRevision int64
	Path                  string
	IdempotencyKey        string
}

type CreateFileCommand struct {
	ExamID                model.ExamID
	ExpectedDraftRevision int64
	Path                  string
	MediaType             string
	ExpectedSHA256        string
	Body                  io.Reader
	Size                  int64
	IdempotencyKey        string
}

type MoveEntryCommand struct {
	ExamID                model.ExamID
	EntryID               model.StarterWorkspaceEntryID
	ExpectedDraftRevision int64
	Path                  string
	IdempotencyKey        string
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
	IdempotencyKey         string
}

type RemoveEntryCommand struct {
	ExamID                model.ExamID
	EntryID               model.StarterWorkspaceEntryID
	ExpectedDraftRevision int64
	Recursive             bool
	IdempotencyKey        string
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

type Authorizer interface {
	Authorize(context.Context, invocation.Invocation, model.Action, model.Resource) error
}

type Auditor interface {
	BeginAtScope(context.Context, invocation.Invocation, model.Action, model.Resource, model.RoleScopeType, string, string, map[string]any, map[string]any) (model.AuditEventID, error)
	Fail(context.Context, model.AuditEventID, string) error
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

// Service manages the Starter Workspace of an Exam Draft.
type Service struct{ deps Dependencies }

// Dependencies are the collaborators of Service; New validates them once and the
// service retains them unchanged.
type Dependencies struct {
	Persistence    store.ExamStarterWorkspaceStore
	Access         accessStore
	Memberships    manageraccess.Memberships
	Authorizer     Authorizer
	Auditor        Auditor
	Content        Content
	Effects        Effects
	EffectFailures EffectFailures
	Now            func() time.Time
	NewEntryID     func() model.StarterWorkspaceEntryID
	NewObjectID    func() model.StarterWorkspaceObjectID
	NewVersion     func() model.WorkspaceContentVersion
}

// New returns a Starter Workspace Service, or an error when a required
// dependency is missing.
func New(deps Dependencies) (*Service, error) {
	if deps.Persistence == nil || deps.Access == nil || deps.Memberships == nil || deps.Authorizer == nil || deps.Auditor == nil || deps.Content == nil || deps.Effects == nil ||
		deps.EffectFailures == nil || deps.Now == nil || deps.NewEntryID == nil || deps.NewObjectID == nil || deps.NewVersion == nil {
		return nil, errors.New("Starter Workspace dependencies are required")
	}
	return &Service{deps: deps}, nil
}

// ListQuery names the Exam whose Draft Starter Workspace is listed.
type ListQuery struct{ ExamID model.ExamID }

// ListExamStarterWorkspace returns the entries of the Exam Draft's Starter
// Workspace to a caller authorized to view the Exam.
func (s *Service) ListExamStarterWorkspace(ctx context.Context, invocation invocation.Invocation, query ListQuery) (_ []store.ExamStarterWorkspaceItem, resultErr error) {
	defer func() { resultErr = publicFailures.Conceal(resultErr) }()
	if _, err := s.authorize(ctx, invocation, query.ExamID, false); err != nil {
		return nil, err
	}
	items, err := s.deps.Persistence.List(ctx, query.ExamID)
	if err != nil {
		return nil, storeErrors.Translate(err)
	}
	return items, nil
}

// OpenFileQuery names one Draft Starter Workspace file of an Exam.
type OpenFileQuery struct {
	ExamID  model.ExamID
	EntryID model.StarterWorkspaceEntryID
}

// OpenExamStarterWorkspaceFile opens one file of the Exam Draft's Starter
// Workspace for a caller authorized to view the Exam. The caller must close
// the body.
func (s *Service) OpenExamStarterWorkspaceFile(ctx context.Context, invocation invocation.Invocation, query OpenFileQuery) (_ OpenedFile, resultErr error) {
	defer func() { resultErr = publicFailures.Conceal(resultErr) }()
	if !query.EntryID.IsValid() {
		return OpenedFile{}, apperror.Invalid("exam.starter_workspace.invalid", "entry_id")
	}
	if _, err := s.authorize(ctx, invocation, query.ExamID, false); err != nil {
		return OpenedFile{}, err
	}
	item, err := s.deps.Persistence.GetFile(ctx, query.ExamID, query.EntryID)
	if err != nil {
		return OpenedFile{}, storeErrors.Translate(err)
	}
	if item == nil || item.Object == nil {
		return OpenedFile{}, apperror.New("exam.starter_workspace.unavailable").Wrap(errors.New("Starter Workspace Store returned incomplete file metadata"))
	}
	body, err := s.deps.Content.OpenStarterWorkspaceObject(ctx, item.Object.ID)
	if err != nil {
		return OpenedFile{}, apperror.New("exam.starter_workspace.unavailable").Wrap(err)
	}
	return OpenedFile{Body: body, MediaType: item.Object.MediaType, SizeBytes: item.Object.SizeBytes,
		SHA256: item.Object.SHA256, ContentVersion: item.Object.ContentVersion}, nil
}

// CreateExamStarterWorkspaceDirectory idempotently creates a directory in the
// Exam Draft's Starter Workspace, fenced by ExpectedDraftRevision.
func (s *Service) CreateExamStarterWorkspaceDirectory(ctx context.Context, invocation invocation.Invocation, command CreateDirectoryCommand) (_ Result, resultErr error) {
	defer func() { resultErr = publicFailures.Conceal(resultErr) }()
	path, err := validateMutation(command.ExamID, "", command.ExpectedDraftRevision, command.Path)
	if err != nil {
		return Result{}, err
	}
	idempotency, err := prepareWorkspaceIdempotency(invocation, idempotencyOperationCreateDirectory, command.IdempotencyKey,
		command.ExamID, command.ExpectedDraftRevision, "", "", path, "", 0, "")
	if err != nil {
		return Result{}, err
	}
	authorization, err := s.authorize(ctx, invocation, command.ExamID, true)
	if err != nil {
		return Result{}, err
	}
	at := model.TimeUTC(s.deps.Now())
	entryID := s.deps.NewEntryID()
	if !entryID.IsValid() {
		return Result{}, apperror.Invalid("exam.starter_workspace.invalid", "entry_id")
	}
	mutation := &store.ExamStarterWorkspaceMutation{ExamID: command.ExamID, ActorUserID: invocation.Principal().UserID,
		ManagerOverride: authorization.override, ExpectedDraftRevision: command.ExpectedDraftRevision,
		ChangedAt: at, EntryID: entryID, Path: path}
	return s.runMutation(ctx, invocation, authorization, "directory_create", ChangeDirectoryCreated, mutation, idempotency, s.deps.Persistence.CreateDirectory)
}

// CreateExamStarterWorkspaceFile idempotently uploads and creates a file in
// the Exam Draft's Starter Workspace, fenced by ExpectedDraftRevision. Size
// -1 means the length is not known in advance.
func (s *Service) CreateExamStarterWorkspaceFile(ctx context.Context, invocation invocation.Invocation, command CreateFileCommand) (_ Result, resultErr error) {
	defer func() { resultErr = publicFailures.Conceal(resultErr) }()
	path, err := validateMutation(command.ExamID, "", command.ExpectedDraftRevision, command.Path)
	if err != nil || command.Body == nil || command.Size < -1 || command.Size > model.StarterWorkspaceMaximumFileBytes || command.MediaType == "" {
		if err != nil {
			return Result{}, err
		}
		return Result{}, apperror.Invalid("exam.starter_workspace.invalid", "content")
	}
	idempotency, err := prepareWorkspaceIdempotency(invocation, idempotencyOperationCreateFile, command.IdempotencyKey,
		command.ExamID, command.ExpectedDraftRevision, "", "", path, command.MediaType, command.Size, command.ExpectedSHA256)
	if err != nil {
		return Result{}, err
	}
	authorization, err := s.authorize(ctx, invocation, command.ExamID, true)
	if err != nil {
		return Result{}, err
	}
	return s.stageAndFinalize(ctx, invocation, authorization, command.ExamID, "", command.ExpectedDraftRevision, path,
		"", command.MediaType, command.ExpectedSHA256, command.Body, command.Size, idempotency, "file_create", ChangeFileCreated, s.deps.Persistence.CreateFile)
}

// MoveExamStarterWorkspaceEntry idempotently moves an entry of the Exam
// Draft's Starter Workspace to a new path, fenced by ExpectedDraftRevision.
func (s *Service) MoveExamStarterWorkspaceEntry(ctx context.Context, invocation invocation.Invocation, command MoveEntryCommand) (_ Result, resultErr error) {
	defer func() { resultErr = publicFailures.Conceal(resultErr) }()
	path, err := validateMutation(command.ExamID, command.EntryID, command.ExpectedDraftRevision, command.Path)
	if err != nil {
		return Result{}, err
	}
	idempotency, err := prepareWorkspaceIdempotency(invocation, idempotencyOperationMoveEntry, command.IdempotencyKey,
		command.ExamID, command.ExpectedDraftRevision, "", command.EntryID.String(), path, "", 0, "")
	if err != nil {
		return Result{}, err
	}
	authorization, err := s.authorize(ctx, invocation, command.ExamID, true)
	if err != nil {
		return Result{}, err
	}
	at := model.TimeUTC(s.deps.Now())
	mutation := &store.ExamStarterWorkspaceMutation{ExamID: command.ExamID, ActorUserID: invocation.Principal().UserID,
		ManagerOverride: authorization.override, ExpectedDraftRevision: command.ExpectedDraftRevision,
		ChangedAt: at, EntryID: command.EntryID, Path: path}
	return s.runMutation(ctx, invocation, authorization, "entry_move", ChangeEntryMoved, mutation, idempotency, s.deps.Persistence.MoveEntry)
}

// ReplaceExamStarterWorkspaceFile idempotently replaces a Starter Workspace
// file's content, fenced by ExpectedDraftRevision and its expected Workspace
// Content Version.
func (s *Service) ReplaceExamStarterWorkspaceFile(ctx context.Context, invocation invocation.Invocation, command ReplaceFileCommand) (_ Result, resultErr error) {
	defer func() { resultErr = publicFailures.Conceal(resultErr) }()
	if err := validateMutationBase(command.ExamID, command.EntryID, command.ExpectedDraftRevision); err != nil {
		return Result{}, err
	}
	if !command.ExpectedContentVersion.IsValid() || command.Body == nil || command.Size < -1 || command.Size > model.StarterWorkspaceMaximumFileBytes || command.MediaType == "" {
		return Result{}, apperror.Invalid("exam.starter_workspace.invalid", "content")
	}
	idempotency, err := prepareWorkspaceIdempotency(invocation, idempotencyOperationReplaceFile, command.IdempotencyKey,
		command.ExamID, command.ExpectedDraftRevision, command.ExpectedContentVersion, command.EntryID.String(), "", command.MediaType, command.Size, command.ExpectedSHA256)
	if err != nil {
		return Result{}, err
	}
	authorization, err := s.authorize(ctx, invocation, command.ExamID, true)
	if err != nil {
		return Result{}, err
	}
	return s.stageAndFinalize(ctx, invocation, authorization, command.ExamID, command.EntryID, command.ExpectedDraftRevision, "",
		command.ExpectedContentVersion, command.MediaType, command.ExpectedSHA256, command.Body, command.Size, idempotency, "file_replace", ChangeFileReplaced, s.deps.Persistence.ReplaceFile)
}

// RemoveExamStarterWorkspaceEntry idempotently removes an entry, recursively
// when requested, from the Exam Draft's Starter Workspace, fenced by
// ExpectedDraftRevision.
func (s *Service) RemoveExamStarterWorkspaceEntry(ctx context.Context, invocation invocation.Invocation, command RemoveEntryCommand) (_ Result, resultErr error) {
	defer func() { resultErr = publicFailures.Conceal(resultErr) }()
	if err := validateMutationBase(command.ExamID, command.EntryID, command.ExpectedDraftRevision); err != nil {
		return Result{}, err
	}
	idempotency, err := applicationidempotency.Require(invocation, idempotencyOperationRemoveEntry, command.IdempotencyKey, struct {
		ExamID                string `json:"exam_id"`
		ExpectedDraftRevision int64  `json:"expected_draft_revision"`
		EntryID               string `json:"entry_id,omitempty"`
		Recursive             bool   `json:"recursive,omitempty"`
	}{command.ExamID.String(), command.ExpectedDraftRevision, command.EntryID.String(), command.Recursive})
	if err != nil {
		return Result{}, err
	}
	authorization, err := s.authorize(ctx, invocation, command.ExamID, true)
	if err != nil {
		return Result{}, err
	}
	at := model.TimeUTC(s.deps.Now())
	mutation := &store.ExamStarterWorkspaceMutation{ExamID: command.ExamID, ActorUserID: invocation.Principal().UserID,
		ManagerOverride: authorization.override, ExpectedDraftRevision: command.ExpectedDraftRevision,
		ChangedAt: at, EntryID: command.EntryID, Recursive: command.Recursive}
	return s.runMutation(ctx, invocation, authorization, "entry_remove", ChangeEntryRemoved, mutation, idempotency, s.deps.Persistence.RemoveEntry)
}

type authorization struct {
	action   model.Action
	unitID   model.AcademicUnitID
	override bool
}

func (s *Service) authorize(ctx context.Context, invocation invocation.Invocation, examID model.ExamID, mutation bool) (authorization, error) {
	principal := invocation.Principal()
	if principal.Validate() != nil || !examID.IsValid() {
		return authorization{}, apperror.Invalid("exam.starter_workspace.invalid", "exam_id")
	}
	access, err := s.deps.Access.Access(ctx, examID, principal.UserID)
	if err != nil {
		return authorization{}, storeErrors.Translate(err)
	}
	if access == nil || access.Exam == nil {
		return authorization{}, apperror.New("exam.starter_workspace.unavailable").Wrap(errors.New("Exam access projection is incomplete"))
	}
	ordinaryAction, overrideAction := model.ActionExamView, model.ActionExamViewOverride
	if mutation {
		ordinaryAction, overrideAction = model.ActionExamManage, model.ActionExamManageOverride
	}
	var at time.Time
	if access.ActorIsManager {
		at = model.TimeUTC(s.deps.Now())
	}
	action, err := manageraccess.SelectAction(ctx, s.deps.Memberships, principal.UserID, access, at, ordinaryAction, overrideAction)
	if err != nil {
		return authorization{}, apperror.New("exam.starter_workspace.unavailable").Wrap(err)
	}
	resource := model.Resource{Type: model.ResourceExam, ID: examID.String()}
	if err := s.deps.Authorizer.Authorize(ctx, invocation, action, resource); err != nil {
		return authorization{}, err
	}
	return authorization{action: action, unitID: access.Exam.AcademicUnitID, override: action == overrideAction}, nil
}

type mutationRunner func(context.Context, *store.ExamStarterWorkspaceMutation, *store.CommandIdempotency) (*store.ExamStarterWorkspaceMutationResult, error)

func (s *Service) runMutation(ctx context.Context, invocation invocation.Invocation, authorization authorization, auditOperation string, effectOperation ChangeOperation,
	mutation *store.ExamStarterWorkspaceMutation, idempotency *store.CommandIdempotency, run mutationRunner) (Result, error) {
	auditID, err := s.deps.Auditor.BeginAtScope(ctx, invocation, authorization.action, model.Resource{Type: model.ResourceExam, ID: mutation.ExamID.String()},
		model.RoleScopeAcademicUnit, authorization.unitID.String(), auditOperation,
		map[string]any{"exam_id": mutation.ExamID.String(), "entry_id": mutation.EntryID.String(), "expected_draft_revision": mutation.ExpectedDraftRevision}, nil)
	if err != nil {
		return Result{}, err
	}
	mutation.AuditEventID = auditID
	mutation.AuditAt = model.MillisFromTime(mutation.ChangedAt)
	result, err := run(ctx, mutation, idempotency)
	if err != nil {
		mapped := storeErrors.Translate(err)
		if failErr := s.deps.Auditor.Fail(ctx, auditID, apperror.CodeOf(mapped, "exam.starter_workspace.unavailable")); failErr != nil {
			return Result{}, errors.Join(mapped, failErr)
		}
		return Result{}, mapped
	}
	if result == nil || result.Entry == nil || result.DraftRevision < 1 {
		return Result{}, apperror.New("exam.starter_workspace.unavailable").Wrap(errors.New("Starter Workspace Store returned incomplete mutation result"))
	}
	if !result.Replayed {
		if effectErr := s.deps.Effects.Changed(ctx, mutation.ExamID, result.Entry.ID, result.DraftRevision, effectOperation, mutation.ChangedAt); effectErr != nil {
			s.deps.EffectFailures.Report(ctx, "exam_starter_workspace_"+string(effectOperation), effectErr)
		}
	}
	return Result{Entry: *result.Entry, Object: result.Object, DraftRevision: result.DraftRevision, Replayed: result.Replayed}, nil
}

func (s *Service) stageAndFinalize(ctx context.Context, invocation invocation.Invocation, authorization authorization, examID model.ExamID, entryID model.StarterWorkspaceEntryID,
	expectedRevision int64, path string, expectedContentVersion model.WorkspaceContentVersion, mediaType, expectedSHA256 string, body io.Reader, size int64, idempotency *store.CommandIdempotency,
	auditOperation string, effectOperation ChangeOperation, run mutationRunner) (Result, error) {
	at := model.TimeUTC(s.deps.Now())
	if entryID.IsZero() {
		entryID = s.deps.NewEntryID()
	}
	objectID, version := s.deps.NewObjectID(), s.deps.NewVersion()
	if !entryID.IsValid() || !objectID.IsValid() || !version.IsValid() {
		return Result{}, apperror.Invalid("exam.starter_workspace.invalid", "identity")
	}
	object, err := model.NewStagedStarterWorkspaceObject(objectID, examID, invocation.Principal().UserID, at, at.Add(model.StarterWorkspaceUploadLease))
	if err != nil {
		return Result{}, apperror.Invalid("exam.starter_workspace.invalid", "object").Wrap(err)
	}
	_, err = s.deps.Persistence.ReserveObject(ctx, &store.ExamStarterWorkspaceReservation{Object: object})
	if err != nil {
		return Result{}, storeErrors.Translate(err)
	}
	staged, err := s.deps.Content.StageStarterWorkspaceObject(ctx, objectID, body, size, mediaType)
	if err != nil {
		_ = s.deps.Persistence.MarkObjectReclaimable(ctx, objectID, at)
		if isInvalidContentError(err) {
			return Result{}, apperror.Invalid("exam.starter_workspace.invalid", "content").Wrap(err)
		}
		return Result{}, apperror.New("exam.starter_workspace.unavailable").Wrap(err)
	}
	if !validSHA256(expectedSHA256) || staged.SHA256 != expectedSHA256 {
		_ = s.deps.Persistence.MarkObjectReclaimable(ctx, objectID, at)
		return Result{}, apperror.Invalid("exam.starter_workspace.invalid", "sha256")
	}
	mutation := &store.ExamStarterWorkspaceMutation{ExamID: examID, ActorUserID: invocation.Principal().UserID, ManagerOverride: authorization.override,
		ExpectedDraftRevision: expectedRevision, ChangedAt: at, EntryID: entryID, Path: path, ObjectID: objectID,
		ExpectedContentVersion: expectedContentVersion, ContentVersion: version, MediaType: staged.MediaType, SizeBytes: staged.SizeBytes, SHA256: staged.SHA256}
	result, err := s.runMutation(ctx, invocation, authorization, auditOperation, effectOperation, mutation, idempotency, run)
	if err != nil {
		if !apperror.Is(err, "exam.starter_workspace.unavailable") {
			_ = s.deps.Persistence.MarkObjectReclaimable(ctx, objectID, at)
		}
		return Result{}, err
	}
	if result.Replayed {
		_ = s.deps.Persistence.MarkObjectReclaimable(ctx, objectID, at)
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

func validateMutation(examID model.ExamID, entryID model.StarterWorkspaceEntryID, revision int64, path string) (string, error) {
	if err := validateMutationBase(examID, entryID, revision); err != nil {
		return "", err
	}
	normalized, err := NormalizePath(path)
	if err != nil || normalized != path {
		return "", apperror.Invalid("exam.starter_workspace.invalid", "path").Wrap(err)
	}
	return normalized, nil
}

func validateMutationBase(examID model.ExamID, entryID model.StarterWorkspaceEntryID, revision int64) error {
	if !examID.IsValid() || revision < 1 || !entryID.IsZero() && !entryID.IsValid() {
		return apperror.Invalid("exam.starter_workspace.invalid", "command")
	}
	return nil
}

// publicFailures presents every Starter Workspace use-case failure: unexpected errors
// become exam.starter_workspace.unavailable, and protected targets conceal denial and absence.
var publicFailures = apperror.Concealment{
	Unavailable: "exam.starter_workspace.unavailable",
	Concealed:   []apperror.Code{"exam.starter_workspace.not_found"},
}

var storeErrors = apperror.StoreTable{
	Constraints: map[string]apperror.Code{
		"exam_archived":                 "exam.archived",
		"exam_draft_revision":           "exam.draft.revision_conflict",
		"workspace_path_collision":      "exam.starter_workspace.path_conflict",
		"workspace_parent_missing":      "exam.starter_workspace.parent_not_found",
		"workspace_directory_not_empty": "exam.starter_workspace.directory_not_empty",
		"workspace_entry_limit":         "exam.starter_workspace.entry_limit",
		"workspace_total_size":          "exam.starter_workspace.total_size_limit",
		"workspace_object_expired":      "exam.starter_workspace.upload_expired",
		"workspace_object_state":        "exam.starter_workspace.object_conflict",
		"workspace_no_changes":          "exam.starter_workspace.no_changes",
		"workspace_descendant_move":     "exam.starter_workspace.move_conflict",
		"workspace_path_limit":          "exam.starter_workspace.move_conflict",
		"workspace_entry_kind":          "exam.starter_workspace.entry_kind",
		"workspace_content_version":     "exam.starter_workspace.content_conflict",
	},
	NotFound:    "exam.starter_workspace.not_found",
	Conflict:    "exam.starter_workspace.conflict",
	Invalid:     "exam.starter_workspace.invalid",
	Unavailable: "exam.starter_workspace.unavailable",
}

func isInvalidContentError(err error) bool {
	var invalid interface{ InvalidStarterWorkspaceContent() bool }
	return errors.As(err, &invalid) && invalid.InvalidStarterWorkspaceContent()
}
