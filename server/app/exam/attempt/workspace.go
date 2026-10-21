// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package attempt

import (
	"context"
	"encoding/hex"
	"errors"
	"io"
	"strings"

	"github.com/sudosylabs/proctor/server/app/apperror"
	"github.com/sudosylabs/proctor/server/app/invocation"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

type WorkspaceMutationAccess struct {
	CandidateAccess
	ParticipationID model.AttemptParticipationID
	Generation      int64
}

type CreateWorkspaceDirectoryCommand struct {
	Access         WorkspaceMutationAccess
	Path           string
	IdempotencyKey string
}

type CreateWorkspaceFileCommand struct {
	Access         WorkspaceMutationAccess
	Path           string
	MediaType      string
	ExpectedSHA256 string
	Body           io.Reader
	Size           int64
	IdempotencyKey string
}

type ReplaceWorkspaceFileCommand struct {
	Access                 WorkspaceMutationAccess
	EntryID                model.AttemptWorkspaceEntryID
	ExpectedPath           string
	ExpectedContentVersion model.WorkspaceContentVersion
	MediaType              string
	ExpectedSHA256         string
	Body                   io.Reader
	Size                   int64
	IdempotencyKey         string
}

type MoveWorkspaceEntryCommand struct {
	Access          WorkspaceMutationAccess
	EntryID         model.AttemptWorkspaceEntryID
	ExpectedPath    string
	DestinationPath string
	IdempotencyKey  string
}

type DeleteWorkspaceEntryCommand struct {
	Access                  WorkspaceMutationAccess
	EntryID                 model.AttemptWorkspaceEntryID
	ExpectedPath            string
	ExpectedContentVersion  model.WorkspaceContentVersion
	Recursive               bool
	ExpectedWorkspaceCursor *int64
	IdempotencyKey          string
}

type WorkspaceMutationResult struct {
	AttemptID       model.ExamAttemptID
	SittingID       model.ExamSittingID
	CandidateUserID model.UserID
	WorkspaceID     model.ExamAttemptWorkspaceID
	Entry           *store.CandidateAttemptWorkspaceItem
	Change          model.AttemptWorkspaceJournalEntry
	Replayed        bool
}

type WorkspaceJournalQuery struct {
	Access      CandidateAccess
	AfterCursor int64
	Limit       int
}

type WorkspaceJournalPage struct {
	WorkspaceID     model.ExamAttemptWorkspaceID
	CurrentCursor   int64
	Entries         []model.AttemptWorkspaceJournalEntry
	HasMore         bool
	RefreshRequired bool
}

// ListCandidateExamWorkspaceJournal returns the Attempt Workspace journal
// entries strictly after AfterCursor, in contiguous cursor order. When
// RefreshRequired is set the page carries no entries and the client must
// reload the manifest.
func (s *Service) ListCandidateExamWorkspaceJournal(ctx context.Context, invocation invocation.Invocation, query WorkspaceJournalQuery) (_ WorkspaceJournalPage, resultErr error) {
	defer func() { resultErr = publicFailures.Conceal(resultErr) }()
	access, err := candidateSelector(invocation, query.Access)
	if err != nil {
		return WorkspaceJournalPage{}, err
	}
	if query.AfterCursor < 0 || query.Limit < 1 || query.Limit > model.AttemptWorkspaceJournalReadMaximum {
		return WorkspaceJournalPage{}, apperror.Invalid("exam.attempt.invalid", "workspace_journal")
	}
	stored, err := s.deps.Workspace.ListJournal(ctx, store.CandidateWorkspaceJournalOptions{
		Access: access, AfterCursor: query.AfterCursor, Limit: query.Limit,
	})
	if err != nil {
		return WorkspaceJournalPage{}, storeError(storeErrors, err)
	}
	if stored == nil || !stored.WorkspaceID.IsValid() || stored.CurrentCursor < query.AfterCursor ||
		(stored.RefreshRequired && (len(stored.Entries) != 0 || stored.HasMore)) {
		return WorkspaceJournalPage{}, apperror.New("exam.attempt.unavailable").Wrap(errors.New("inconsistent Attempt Workspace journal page"))
	}
	entries := make([]model.AttemptWorkspaceJournalEntry, len(stored.Entries))
	for index, entry := range stored.Entries {
		if entry.Validate() != nil || entry.WorkspaceID != stored.WorkspaceID || entry.Cursor <= query.AfterCursor || entry.Cursor > stored.CurrentCursor ||
			(index > 0 && entry.Cursor != entries[index-1].Cursor+1) {
			return WorkspaceJournalPage{}, apperror.New("exam.attempt.unavailable").Wrap(errors.New("inconsistent Attempt Workspace journal entry"))
		}
		entries[index] = entry
	}
	return WorkspaceJournalPage{WorkspaceID: stored.WorkspaceID, CurrentCursor: stored.CurrentCursor,
		Entries: entries, HasMore: stored.HasMore, RefreshRequired: stored.RefreshRequired}, nil
}

// CreateCandidateExamWorkspaceDirectory idempotently creates a directory at a
// normalized path in the candidate's Attempt Workspace, fenced by
// participation generation.
func (s *Service) CreateCandidateExamWorkspaceDirectory(ctx context.Context, invocation invocation.Invocation, command CreateWorkspaceDirectoryCommand) (_ WorkspaceMutationResult, resultErr error) {
	defer func() { resultErr = publicFailures.Conceal(resultErr) }()
	access, err := workspaceMutationSelector(invocation, command.Access)
	if err != nil {
		return WorkspaceMutationResult{}, err
	}
	path, err := model.NormalizeAttemptWorkspacePath(command.Path)
	if err != nil || path != command.Path {
		return WorkspaceMutationResult{}, apperror.Invalid("exam.attempt.invalid", "workspace_command").Wrap(err)
	}
	idempotency, err := prepareWorkspaceMutationIdempotency(invocation, command.IdempotencyKey, access.AttemptID,
		model.AttemptWorkspaceMutationCreateDirectory, struct {
			Path string `json:"path"`
		}{path})
	if err != nil {
		return WorkspaceMutationResult{}, err
	}
	entryID := s.deps.NewWorkspaceEntry()
	if !entryID.IsValid() {
		return WorkspaceMutationResult{}, apperror.Invalid("exam.attempt.invalid", "workspace_entry_id")
	}
	target, err := s.resolveWorkspaceMutationTarget(ctx, access)
	if err != nil {
		return WorkspaceMutationResult{}, err
	}
	return s.applyWorkspaceMutation(ctx, invocation, target, &store.ExamAttemptWorkspaceMutation{
		Access: access, Operation: model.AttemptWorkspaceMutationCreateDirectory, EntryID: entryID,
		DestinationPath: path,
	}, idempotency, model.StarterWorkspaceEntryDirectory, "")
}

// CreateCandidateExamWorkspaceFile idempotently stages and creates a new file
// in the candidate's Attempt Workspace, verifying the uploaded body against
// its expected SHA-256.
func (s *Service) CreateCandidateExamWorkspaceFile(ctx context.Context, invocation invocation.Invocation, command CreateWorkspaceFileCommand) (_ WorkspaceMutationResult, resultErr error) {
	defer func() { resultErr = publicFailures.Conceal(resultErr) }()
	entryID := s.deps.NewWorkspaceEntry()
	return s.stageWorkspaceFile(ctx, invocation, command.Access, entryID, "", command.Path, "",
		command.MediaType, command.ExpectedSHA256, command.Body, command.Size, command.IdempotencyKey,
		model.AttemptWorkspaceMutationCreateFile)
}

// ReplaceCandidateExamWorkspaceFile idempotently replaces a file's content in
// the candidate's Attempt Workspace, fenced by its expected path and
// Workspace Content Version.
func (s *Service) ReplaceCandidateExamWorkspaceFile(ctx context.Context, invocation invocation.Invocation, command ReplaceWorkspaceFileCommand) (_ WorkspaceMutationResult, resultErr error) {
	defer func() { resultErr = publicFailures.Conceal(resultErr) }()
	return s.stageWorkspaceFile(ctx, invocation, command.Access, command.EntryID, command.ExpectedPath, "",
		command.ExpectedContentVersion, command.MediaType, command.ExpectedSHA256, command.Body, command.Size, command.IdempotencyKey,
		model.AttemptWorkspaceMutationReplaceFile)
}

// MoveCandidateExamWorkspaceEntry idempotently moves a Workspace Entry from
// its expected path to a destination path, preserving its identity.
func (s *Service) MoveCandidateExamWorkspaceEntry(ctx context.Context, invocation invocation.Invocation, command MoveWorkspaceEntryCommand) (_ WorkspaceMutationResult, resultErr error) {
	defer func() { resultErr = publicFailures.Conceal(resultErr) }()
	access, err := workspaceMutationSelector(invocation, command.Access)
	if err != nil {
		return WorkspaceMutationResult{}, err
	}
	expected, destination, err := validateWorkspaceMove(command.EntryID, command.ExpectedPath, command.DestinationPath)
	if err != nil {
		return WorkspaceMutationResult{}, err
	}
	idempotency, err := prepareWorkspaceMutationIdempotency(invocation, command.IdempotencyKey, access.AttemptID,
		model.AttemptWorkspaceMutationMoveEntry, struct{ EntryID, ExpectedPath, DestinationPath string }{
			command.EntryID.String(), expected, destination})
	if err != nil {
		return WorkspaceMutationResult{}, err
	}
	target, err := s.resolveWorkspaceMutationTarget(ctx, access)
	if err != nil {
		return WorkspaceMutationResult{}, err
	}
	return s.applyWorkspaceMutation(ctx, invocation, target, &store.ExamAttemptWorkspaceMutation{Access: access,
		Operation: model.AttemptWorkspaceMutationMoveEntry, EntryID: command.EntryID, ExpectedPath: expected,
		DestinationPath: destination}, idempotency, "", "")
}

// DeleteCandidateExamWorkspaceEntry idempotently deletes a Workspace Entry at
// its expected path. A recursive directory delete requires
// ExpectedWorkspaceCursor; a non-recursive delete must not set it.
func (s *Service) DeleteCandidateExamWorkspaceEntry(ctx context.Context, invocation invocation.Invocation, command DeleteWorkspaceEntryCommand) (_ WorkspaceMutationResult, resultErr error) {
	defer func() { resultErr = publicFailures.Conceal(resultErr) }()
	access, err := workspaceMutationSelector(invocation, command.Access)
	if err != nil {
		return WorkspaceMutationResult{}, err
	}
	if (command.Recursive && (command.ExpectedWorkspaceCursor == nil ||
		*command.ExpectedWorkspaceCursor < 0 || !command.ExpectedContentVersion.IsZero())) ||
		(!command.Recursive && command.ExpectedWorkspaceCursor != nil) {
		return WorkspaceMutationResult{}, apperror.Invalid("exam.attempt.invalid", "workspace_recursive_delete")
	}
	expected, err := validateWorkspaceEntryFence(command.EntryID, command.ExpectedPath)
	if err != nil {
		return WorkspaceMutationResult{}, err
	}
	idempotency, err := prepareWorkspaceMutationIdempotency(invocation, command.IdempotencyKey, access.AttemptID,
		model.AttemptWorkspaceMutationDeleteEntry, struct {
			EntryID, ExpectedPath, ExpectedContentVersion string
			Recursive                                     bool   `json:"recursive,omitempty"`
			ExpectedWorkspaceCursor                       *int64 `json:"expected_workspace_cursor,omitempty"`
		}{command.EntryID.String(), expected, command.ExpectedContentVersion.String(), command.Recursive, command.ExpectedWorkspaceCursor})
	if err != nil {
		return WorkspaceMutationResult{}, err
	}
	target, err := s.resolveWorkspaceMutationTarget(ctx, access)
	if err != nil {
		return WorkspaceMutationResult{}, err
	}
	return s.applyWorkspaceMutation(ctx, invocation, target, &store.ExamAttemptWorkspaceMutation{Access: access,
		Operation: model.AttemptWorkspaceMutationDeleteEntry, EntryID: command.EntryID, ExpectedPath: expected,
		ExpectedContentVersion: command.ExpectedContentVersion, Recursive: command.Recursive,
		ExpectedWorkspaceCursor: command.ExpectedWorkspaceCursor}, idempotency, "", "")
}

func (s *Service) stageWorkspaceFile(ctx context.Context, invocation invocation.Invocation, mutationAccess WorkspaceMutationAccess,
	entryID model.AttemptWorkspaceEntryID, expectedPath, destinationPath string, expectedVersion model.WorkspaceContentVersion,
	mediaType, expectedSHA string, body io.Reader, size int64, idempotencyKey string,
	operation model.AttemptWorkspaceMutationKind,
) (WorkspaceMutationResult, error) {
	access, err := workspaceMutationSelector(invocation, mutationAccess)
	if err != nil {
		return WorkspaceMutationResult{}, err
	}
	if !entryID.IsValid() || body == nil || size < 0 || size > model.AttemptWorkspaceMaximumFileBytes ||
		mediaType == "" || strings.TrimSpace(mediaType) != mediaType || len(mediaType) > 255 ||
		!validWorkspaceSHA256(expectedSHA) {
		return WorkspaceMutationResult{}, apperror.Invalid("exam.attempt.invalid", "workspace_file")
	}
	if operation == model.AttemptWorkspaceMutationCreateFile {
		destinationPath, err = validateWorkspacePath(destinationPath)
	} else {
		expectedPath, err = validateWorkspacePath(expectedPath)
		if err == nil && !expectedVersion.IsValid() {
			err = errors.New("content version is required")
		}
	}
	if err != nil {
		return WorkspaceMutationResult{}, apperror.Invalid("exam.attempt.invalid", "workspace_file").Wrap(err)
	}
	var semantic any
	if operation == model.AttemptWorkspaceMutationCreateFile {
		semantic = struct {
			Path, MediaType, SHA256 string
			Size                    int64
		}{destinationPath, mediaType, expectedSHA, size}
	} else {
		semantic = struct {
			EntryID, Path, Version, MediaType, SHA256 string
			Size                                      int64
		}{entryID.String(), expectedPath, expectedVersion.String(), mediaType, expectedSHA, size}
	}
	idempotency, err := prepareWorkspaceMutationIdempotency(invocation, idempotencyKey, access.AttemptID, operation, semantic)
	if err != nil {
		return WorkspaceMutationResult{}, err
	}
	target, err := s.resolveWorkspaceMutationTarget(ctx, access)
	if err != nil {
		return WorkspaceMutationResult{}, err
	}
	objectID, version := s.deps.NewWorkspaceObject(), s.deps.NewWorkspaceVersion()
	if !objectID.IsValid() || !version.IsValid() {
		return WorkspaceMutationResult{}, apperror.Invalid("exam.attempt.invalid", "workspace_identity")
	}
	if _, err = s.deps.Workspace.ReserveObject(ctx, &store.ExamAttemptWorkspaceObjectReservation{Access: access, ObjectID: objectID}); err != nil {
		return WorkspaceMutationResult{}, storeError(storeErrors, err)
	}
	staged, err := s.deps.Content.StageAttemptWorkspaceObject(ctx, objectID, body, size, mediaType)
	if err != nil {
		_ = s.deps.Workspace.MarkObjectReclaimable(ctx, objectID)
		if isInvalidAttemptWorkspaceContent(err) {
			return WorkspaceMutationResult{}, apperror.Invalid("exam.attempt.invalid", "workspace_content").Wrap(err)
		}
		return WorkspaceMutationResult{}, apperror.New("exam.attempt.unavailable").Wrap(err)
	}
	if staged == nil || staged.Validate() != nil || staged.SHA256 != expectedSHA {
		_ = s.deps.Workspace.MarkObjectReclaimable(ctx, objectID)
		return WorkspaceMutationResult{}, apperror.Invalid("exam.attempt.invalid", "workspace_sha256")
	}
	if _, err = s.deps.Workspace.MarkObjectReady(ctx, &store.ExamAttemptWorkspaceObjectReady{Access: access,
		ObjectID: objectID, ContentVersion: version, Content: *staged}); err != nil {
		mapped := storeError(storeErrors, err)
		if !isAttemptUnavailable(mapped) {
			_ = s.deps.Workspace.MarkObjectReclaimable(ctx, objectID)
		}
		return WorkspaceMutationResult{}, mapped
	}
	result, err := s.applyWorkspaceMutation(ctx, invocation, target, &store.ExamAttemptWorkspaceMutation{Access: access,
		Operation: operation, EntryID: entryID, ExpectedPath: expectedPath, DestinationPath: destinationPath,
		ExpectedContentVersion: expectedVersion, ObjectID: objectID}, idempotency, model.StarterWorkspaceEntryFile, version)
	if err != nil {
		if !isAttemptUnavailable(err) {
			_ = s.deps.Workspace.MarkObjectReclaimable(ctx, objectID)
		}
		return WorkspaceMutationResult{}, err
	}
	if result.Replayed {
		_ = s.deps.Workspace.MarkObjectReclaimable(ctx, objectID)
	}
	return result, nil
}

func workspaceMutationSelector(invocation invocation.Invocation, access WorkspaceMutationAccess) (store.ExamAttemptWorkspaceMutationAccess, error) {
	read, err := candidateSelector(invocation, access.CandidateAccess)
	if err != nil {
		return store.ExamAttemptWorkspaceMutationAccess{}, err
	}
	if !access.ParticipationID.IsValid() || access.Generation < 1 {
		return store.ExamAttemptWorkspaceMutationAccess{}, apperror.Invalid("exam.attempt.invalid", "workspace_access")
	}
	return store.ExamAttemptWorkspaceMutationAccess{AttemptID: read.AttemptID, ParticipationID: access.ParticipationID,
		Generation: access.Generation, CandidateUserID: read.CandidateUserID, SessionID: read.SessionID,
		DesktopRegistrationID: read.DesktopRegistrationID, DPoPKeyThumbprint: read.DPoPKeyThumbprint,
		ConnectionID: read.ConnectionID, ContinuityCredentialHash: read.ContinuityCredentialHash}, nil
}

func validWorkspaceTarget(target *store.ExamAttemptWorkspaceMutationTarget, access store.ExamAttemptWorkspaceMutationAccess) bool {
	return target != nil && target.ExamID.IsValid() && target.SittingID.IsValid() && target.ClassID.IsValid() &&
		target.CandidateUserID == access.CandidateUserID && target.WorkspaceID.IsValid()
}

func projectWorkspaceMutation(stored *store.ExamAttemptWorkspaceMutationResult, target *store.ExamAttemptWorkspaceMutationTarget,
	attemptID model.ExamAttemptID, entryID model.AttemptWorkspaceEntryID, operation model.AttemptWorkspaceMutationKind, expectedKind model.StarterWorkspaceEntryKind,
	expectedVersion model.WorkspaceContentVersion,
) (WorkspaceMutationResult, error) {
	if stored == nil || stored.SittingID != target.SittingID || stored.ClassID != target.ClassID || stored.CandidateUserID != target.CandidateUserID ||
		stored.WorkspaceID != target.WorkspaceID || stored.Change.Validate() != nil || stored.Change.WorkspaceID != stored.WorkspaceID ||
		stored.Change.Operation != operation || stored.Change.Cursor < 1 || (!stored.Replayed && stored.Change.EntryID != entryID) {
		return WorkspaceMutationResult{}, apperror.New("exam.attempt.unavailable").Wrap(errors.New("inconsistent Attempt Workspace mutation result"))
	}
	if operation == model.AttemptWorkspaceMutationDeleteEntry {
		if stored.Entry != nil {
			return WorkspaceMutationResult{}, apperror.New("exam.attempt.unavailable").Wrap(errors.New("deleted Workspace entry remained visible"))
		}
		return WorkspaceMutationResult{AttemptID: attemptID, SittingID: stored.SittingID, CandidateUserID: stored.CandidateUserID,
			WorkspaceID: stored.WorkspaceID, Change: stored.Change, Replayed: stored.Replayed}, nil
	}
	if !validCandidateWorkspaceItem(stored.Entry) || stored.Entry.EntryID != stored.Change.EntryID || stored.Entry.Path != stored.Change.NewPath ||
		(!stored.Replayed && stored.Entry.EntryID != entryID) ||
		(expectedKind != "" && stored.Entry.Kind != expectedKind) ||
		(!stored.Replayed && expectedVersion.IsValid() && (stored.Entry.ContentVersion != expectedVersion || stored.Change.ContentVersion != expectedVersion)) {
		return WorkspaceMutationResult{}, apperror.New("exam.attempt.unavailable").Wrap(errors.New("inconsistent Attempt Workspace entry result"))
	}
	entry := *stored.Entry
	return WorkspaceMutationResult{AttemptID: attemptID, SittingID: stored.SittingID, CandidateUserID: stored.CandidateUserID,
		WorkspaceID: stored.WorkspaceID, Entry: &entry, Change: stored.Change, Replayed: stored.Replayed}, nil
}

func (s *Service) resolveWorkspaceMutationTarget(ctx context.Context, access store.ExamAttemptWorkspaceMutationAccess) (*store.ExamAttemptWorkspaceMutationTarget, error) {
	target, err := s.deps.Workspace.ResolveMutationTarget(ctx, access)
	if err != nil {
		return nil, storeError(storeErrors, err)
	}
	if !validWorkspaceTarget(target, access) {
		return nil, apperror.New("exam.attempt.unavailable").Wrap(errors.New("inconsistent Attempt Workspace mutation target"))
	}
	return target, nil
}

func (s *Service) applyWorkspaceMutation(ctx context.Context, invocation invocation.Invocation, target *store.ExamAttemptWorkspaceMutationTarget,
	mutation *store.ExamAttemptWorkspaceMutation, idempotency *store.CommandIdempotency,
	expectedKind model.StarterWorkspaceEntryKind, expectedVersion model.WorkspaceContentVersion,
) (WorkspaceMutationResult, error) {
	auditID, err := s.deps.Auditor.BeginAtScope(ctx, invocation, model.ActionExamSittingParticipate,
		model.Resource{Type: model.ResourceExamSitting, ID: target.SittingID.String()}, model.RoleScopeClass,
		target.ClassID.String(), store.ExamAttemptWorkspaceMutationOperation,
		map[string]any{"exam_attempt_id": mutation.Access.AttemptID.String(),
			"operation": string(mutation.Operation)}, nil)
	if err != nil {
		return WorkspaceMutationResult{}, err
	}
	mutation.AuditEventID, mutation.AuditAt = auditID, model.MillisFromTime(model.TimeUTC(s.deps.Now()))
	stored, err := s.deps.Workspace.ApplyMutation(ctx, mutation, idempotency)
	if err != nil {
		return WorkspaceMutationResult{}, s.failAudit(ctx, auditID, err)
	}
	result, err := projectWorkspaceMutation(stored, target, mutation.Access.AttemptID, mutation.EntryID, mutation.Operation, expectedKind, expectedVersion)
	if err != nil {
		return WorkspaceMutationResult{}, err
	}
	if result.Change.Recursive != mutation.Recursive {
		return WorkspaceMutationResult{}, apperror.New("exam.attempt.unavailable").Wrap(errors.New("inconsistent recursive Workspace mutation result"))
	}
	if !result.Replayed {
		if effectErr := s.deps.Effects.WorkspaceChanged(ctx, result); effectErr != nil {
			s.deps.EffectFailures.Report(ctx, "exam_attempt_workspace_changed", effectErr)
		}
	}
	return result, nil
}

func validateWorkspaceEntryFence(entryID model.AttemptWorkspaceEntryID, expectedPath string) (string, error) {
	if !entryID.IsValid() {
		return "", apperror.Invalid("exam.attempt.invalid", "workspace_command")
	}
	path, err := validateWorkspacePath(expectedPath)
	if err != nil {
		return "", apperror.Invalid("exam.attempt.invalid", "workspace_path").Wrap(err)
	}
	return path, nil
}

func validateWorkspaceMove(entryID model.AttemptWorkspaceEntryID, expectedPath, destinationPath string) (string, string, error) {
	expected, err := validateWorkspaceEntryFence(entryID, expectedPath)
	if err != nil {
		return "", "", err
	}
	destination, err := validateWorkspacePath(destinationPath)
	if err != nil || destination == expected {
		return "", "", apperror.Invalid("exam.attempt.invalid", "workspace_destination_path").Wrap(err)
	}
	return expected, destination, nil
}

func validateWorkspacePath(value string) (string, error) {
	normalized, err := model.NormalizeAttemptWorkspacePath(value)
	if err != nil || normalized != value {
		return "", errors.New("Workspace path is not canonical")
	}
	return normalized, nil
}

func validWorkspaceSHA256(value string) bool {
	if len(value) != 64 || strings.ToLower(value) != value {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func validCandidateWorkspaceItem(item *store.CandidateAttemptWorkspaceItem) bool {
	if item == nil || !item.EntryID.IsValid() {
		return false
	}
	path, err := validateWorkspacePath(item.Path)
	if err != nil || path != item.Path {
		return false
	}
	switch item.Kind {
	case model.StarterWorkspaceEntryDirectory:
		return item.ContentVersion.IsZero() && item.MediaType == "" && item.SizeBytes == 0 && item.SHA256 == ""
	case model.StarterWorkspaceEntryFile:
		return item.ContentVersion.IsValid() && (model.AttemptWorkspaceContent{MediaType: item.MediaType,
			SizeBytes: item.SizeBytes, SHA256: item.SHA256}).Validate() == nil
	default:
		return false
	}
}

func isInvalidAttemptWorkspaceContent(err error) bool {
	var invalid interface{ InvalidAttemptWorkspaceContent() bool }
	return errors.As(err, &invalid) && invalid.InvalidAttemptWorkspaceContent()
}

func isAttemptUnavailable(err error) bool {
	var fault *apperror.Error
	return errors.As(err, &fault) && fault.Code() == "exam.attempt.unavailable"
}
