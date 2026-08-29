// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package attempt

import (
	"context"
	"encoding/hex"
	"errors"
	"io"
	"strings"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

type WorkspaceMutationAccess struct {
	CandidateAccess
	ParticipationID model.AttemptParticipationID
	Generation      int64
}

type WorkspaceMutationOrigin string

const (
	WorkspaceMutationOriginCandidate     WorkspaceMutationOrigin = "candidate"
	WorkspaceMutationOriginExecutionHost WorkspaceMutationOrigin = "execution_host"
)

func (origin WorkspaceMutationOrigin) valid() bool {
	return origin == WorkspaceMutationOriginCandidate || origin == WorkspaceMutationOriginExecutionHost
}

type CreateWorkspaceDirectoryCommand struct {
	Access         WorkspaceMutationAccess
	Origin         WorkspaceMutationOrigin
	Path           string
	IdempotencyKey string
}

type CreateWorkspaceFileCommand struct {
	Access         WorkspaceMutationAccess
	Origin         WorkspaceMutationOrigin
	Path           string
	MediaType      string
	ExpectedSHA256 string
	Body           io.Reader
	Size           int64
	IdempotencyKey string
}

type ReplaceWorkspaceFileCommand struct {
	Access                 WorkspaceMutationAccess
	Origin                 WorkspaceMutationOrigin
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
	Origin          WorkspaceMutationOrigin
	EntryID         model.AttemptWorkspaceEntryID
	ExpectedPath    string
	DestinationPath string
	IdempotencyKey  string
}

type DeleteWorkspaceEntryCommand struct {
	Access                 WorkspaceMutationAccess
	Origin                 WorkspaceMutationOrigin
	EntryID                model.AttemptWorkspaceEntryID
	ExpectedPath           string
	ExpectedContentVersion model.WorkspaceContentVersion
	IdempotencyKey         string
}

type WorkspaceMutationResult struct {
	AttemptID       model.ExamAttemptID
	SittingID       model.ExamSittingID
	CandidateUserID model.UserID
	WorkspaceID     model.ExamAttemptWorkspaceID
	Entry           *store.CandidateAttemptWorkspaceItem
	Change          model.AttemptWorkspaceJournalEntry
	Replayed        bool
	Origin          WorkspaceMutationOrigin
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

func (service *Service) ListWorkspaceJournal(ctx context.Context, call Call, query WorkspaceJournalQuery) (WorkspaceJournalPage, error) {
	access, err := candidateSelector(call, query.Access)
	if err != nil {
		return WorkspaceJournalPage{}, err
	}
	if query.AfterCursor < 0 || query.Limit < 1 || query.Limit > model.AttemptWorkspaceJournalReadMaximum {
		return WorkspaceJournalPage{}, invalid("workspace_journal")
	}
	stored, err := service.deps.Workspace.ListJournal(ctx, store.CandidateWorkspaceJournalOptions{
		Access: access, AfterCursor: query.AfterCursor, Limit: query.Limit,
	})
	if err != nil {
		return WorkspaceJournalPage{}, mapStore(err)
	}
	if stored == nil || !stored.WorkspaceID.IsValid() || stored.CurrentCursor < query.AfterCursor ||
		(stored.RefreshRequired && (len(stored.Entries) != 0 || stored.HasMore)) {
		return WorkspaceJournalPage{}, unavailable(errors.New("inconsistent Attempt Workspace journal page"))
	}
	entries := make([]model.AttemptWorkspaceJournalEntry, len(stored.Entries))
	for index, entry := range stored.Entries {
		if entry.Validate() != nil || entry.WorkspaceID != stored.WorkspaceID || entry.Cursor <= query.AfterCursor || entry.Cursor > stored.CurrentCursor ||
			(index > 0 && entry.Cursor != entries[index-1].Cursor+1) {
			return WorkspaceJournalPage{}, unavailable(errors.New("inconsistent Attempt Workspace journal entry"))
		}
		entries[index] = entry
	}
	return WorkspaceJournalPage{WorkspaceID: stored.WorkspaceID, CurrentCursor: stored.CurrentCursor,
		Entries: entries, HasMore: stored.HasMore, RefreshRequired: stored.RefreshRequired}, nil
}

func (service *Service) CreateWorkspaceDirectory(ctx context.Context, call Call, command CreateWorkspaceDirectoryCommand) (WorkspaceMutationResult, error) {
	access, err := workspaceMutationSelector(call, command.Access)
	if err != nil {
		return WorkspaceMutationResult{}, err
	}
	if !command.Origin.valid() {
		return WorkspaceMutationResult{}, invalid("workspace_origin")
	}
	path, err := model.NormalizeAttemptWorkspacePath(command.Path)
	if err != nil || path != command.Path {
		return WorkspaceMutationResult{}, invalidCause("workspace_command", err)
	}
	idempotency, err := prepareWorkspaceMutationIdempotency(call, command.IdempotencyKey, access.AttemptID,
		model.AttemptWorkspaceMutationCreateDirectory, struct {
			Path string `json:"path"`
		}{path})
	if err != nil {
		return WorkspaceMutationResult{}, err
	}
	entryID := service.deps.NewWorkspaceEntry()
	if !entryID.IsValid() {
		return WorkspaceMutationResult{}, invalid("workspace_entry_id")
	}
	target, err := service.resolveWorkspaceMutationTarget(ctx, access)
	if err != nil {
		return WorkspaceMutationResult{}, err
	}
	return service.applyWorkspaceMutation(ctx, call, target, &store.ExamAttemptWorkspaceMutation{
		Access: access, Operation: model.AttemptWorkspaceMutationCreateDirectory, EntryID: entryID,
		DestinationPath: path,
	}, idempotency, command.Origin, model.StarterWorkspaceEntryDirectory, "")
}

func (service *Service) CreateWorkspaceFile(ctx context.Context, call Call, command CreateWorkspaceFileCommand) (WorkspaceMutationResult, error) {
	entryID := service.deps.NewWorkspaceEntry()
	return service.stageWorkspaceFile(ctx, call, command.Access, entryID, "", command.Path, "",
		command.MediaType, command.ExpectedSHA256, command.Body, command.Size, command.IdempotencyKey,
		command.Origin, model.AttemptWorkspaceMutationCreateFile)
}

func (service *Service) ReplaceWorkspaceFile(ctx context.Context, call Call, command ReplaceWorkspaceFileCommand) (WorkspaceMutationResult, error) {
	return service.stageWorkspaceFile(ctx, call, command.Access, command.EntryID, command.ExpectedPath, "",
		command.ExpectedContentVersion, command.MediaType, command.ExpectedSHA256, command.Body, command.Size, command.IdempotencyKey,
		command.Origin, model.AttemptWorkspaceMutationReplaceFile)
}

func (service *Service) MoveWorkspaceEntry(ctx context.Context, call Call, command MoveWorkspaceEntryCommand) (WorkspaceMutationResult, error) {
	access, err := workspaceMutationSelector(call, command.Access)
	if err != nil {
		return WorkspaceMutationResult{}, err
	}
	if !command.Origin.valid() {
		return WorkspaceMutationResult{}, invalid("workspace_origin")
	}
	expected, destination, err := validateWorkspaceMove(command.EntryID, command.ExpectedPath, command.DestinationPath)
	if err != nil {
		return WorkspaceMutationResult{}, err
	}
	idempotency, err := prepareWorkspaceMutationIdempotency(call, command.IdempotencyKey, access.AttemptID,
		model.AttemptWorkspaceMutationMoveEntry, struct{ EntryID, ExpectedPath, DestinationPath string }{
			command.EntryID.String(), expected, destination})
	if err != nil {
		return WorkspaceMutationResult{}, err
	}
	target, err := service.resolveWorkspaceMutationTarget(ctx, access)
	if err != nil {
		return WorkspaceMutationResult{}, err
	}
	return service.applyWorkspaceMutation(ctx, call, target, &store.ExamAttemptWorkspaceMutation{Access: access,
		Operation: model.AttemptWorkspaceMutationMoveEntry, EntryID: command.EntryID, ExpectedPath: expected,
		DestinationPath: destination}, idempotency, command.Origin, "", "")
}

func (service *Service) DeleteWorkspaceEntry(ctx context.Context, call Call, command DeleteWorkspaceEntryCommand) (WorkspaceMutationResult, error) {
	access, err := workspaceMutationSelector(call, command.Access)
	if err != nil {
		return WorkspaceMutationResult{}, err
	}
	if !command.Origin.valid() {
		return WorkspaceMutationResult{}, invalid("workspace_origin")
	}
	expected, err := validateWorkspaceEntryFence(command.EntryID, command.ExpectedPath)
	if err != nil {
		return WorkspaceMutationResult{}, err
	}
	idempotency, err := prepareWorkspaceMutationIdempotency(call, command.IdempotencyKey, access.AttemptID,
		model.AttemptWorkspaceMutationDeleteEntry, struct{ EntryID, ExpectedPath, ExpectedContentVersion string }{
			command.EntryID.String(), expected, command.ExpectedContentVersion.String()})
	if err != nil {
		return WorkspaceMutationResult{}, err
	}
	target, err := service.resolveWorkspaceMutationTarget(ctx, access)
	if err != nil {
		return WorkspaceMutationResult{}, err
	}
	return service.applyWorkspaceMutation(ctx, call, target, &store.ExamAttemptWorkspaceMutation{Access: access,
		Operation: model.AttemptWorkspaceMutationDeleteEntry, EntryID: command.EntryID, ExpectedPath: expected,
		ExpectedContentVersion: command.ExpectedContentVersion}, idempotency, command.Origin, "", "")
}

func (service *Service) stageWorkspaceFile(ctx context.Context, call Call, mutationAccess WorkspaceMutationAccess,
	entryID model.AttemptWorkspaceEntryID, expectedPath, destinationPath string, expectedVersion model.WorkspaceContentVersion,
	mediaType, expectedSHA string, body io.Reader, size int64, idempotencyKey string, origin WorkspaceMutationOrigin,
	operation model.AttemptWorkspaceMutationKind,
) (WorkspaceMutationResult, error) {
	access, err := workspaceMutationSelector(call, mutationAccess)
	if err != nil {
		return WorkspaceMutationResult{}, err
	}
	if !entryID.IsValid() || body == nil || size < 0 || size > model.AttemptWorkspaceMaximumFileBytes ||
		mediaType == "" || strings.TrimSpace(mediaType) != mediaType || len(mediaType) > 255 ||
		!validWorkspaceSHA256(expectedSHA) || !origin.valid() {
		return WorkspaceMutationResult{}, invalid("workspace_file")
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
		return WorkspaceMutationResult{}, invalidCause("workspace_file", err)
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
	idempotency, err := prepareWorkspaceMutationIdempotency(call, idempotencyKey, access.AttemptID, operation, semantic)
	if err != nil {
		return WorkspaceMutationResult{}, err
	}
	target, err := service.resolveWorkspaceMutationTarget(ctx, access)
	if err != nil {
		return WorkspaceMutationResult{}, err
	}
	objectID, version := service.deps.NewWorkspaceObject(), service.deps.NewWorkspaceVersion()
	if !objectID.IsValid() || !version.IsValid() {
		return WorkspaceMutationResult{}, invalid("workspace_identity")
	}
	if _, err = service.deps.Workspace.ReserveObject(ctx, &store.ExamAttemptWorkspaceObjectReservation{Access: access, ObjectID: objectID}); err != nil {
		return WorkspaceMutationResult{}, mapStore(err)
	}
	staged, err := service.deps.Content.StageAttemptWorkspaceObject(ctx, objectID, body, size, mediaType)
	if err != nil {
		_ = service.deps.Workspace.MarkObjectReclaimable(ctx, objectID)
		if isInvalidAttemptWorkspaceContent(err) {
			return WorkspaceMutationResult{}, invalidCause("workspace_content", err)
		}
		return WorkspaceMutationResult{}, unavailable(err)
	}
	if staged == nil || staged.Validate() != nil || staged.SHA256 != expectedSHA {
		_ = service.deps.Workspace.MarkObjectReclaimable(ctx, objectID)
		return WorkspaceMutationResult{}, invalid("workspace_sha256")
	}
	if _, err = service.deps.Workspace.MarkObjectReady(ctx, &store.ExamAttemptWorkspaceObjectReady{Access: access,
		ObjectID: objectID, ContentVersion: version, Content: *staged}); err != nil {
		mapped := mapStore(err)
		if !isAttemptUnavailable(mapped) {
			_ = service.deps.Workspace.MarkObjectReclaimable(ctx, objectID)
		}
		return WorkspaceMutationResult{}, mapped
	}
	result, err := service.applyWorkspaceMutation(ctx, call, target, &store.ExamAttemptWorkspaceMutation{Access: access,
		Operation: operation, EntryID: entryID, ExpectedPath: expectedPath, DestinationPath: destinationPath,
		ExpectedContentVersion: expectedVersion, ObjectID: objectID}, idempotency, origin, model.StarterWorkspaceEntryFile, version)
	if err != nil {
		if !isAttemptUnavailable(err) {
			_ = service.deps.Workspace.MarkObjectReclaimable(ctx, objectID)
		}
		return WorkspaceMutationResult{}, err
	}
	if result.Replayed {
		_ = service.deps.Workspace.MarkObjectReclaimable(ctx, objectID)
	}
	return result, nil
}

func workspaceMutationSelector(call Call, access WorkspaceMutationAccess) (store.ExamAttemptWorkspaceMutationAccess, error) {
	read, err := candidateSelector(call, access.CandidateAccess)
	if err != nil {
		return store.ExamAttemptWorkspaceMutationAccess{}, err
	}
	if !access.ParticipationID.IsValid() || access.Generation < 1 {
		return store.ExamAttemptWorkspaceMutationAccess{}, invalid("workspace_access")
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
	attemptID model.ExamAttemptID, entryID model.AttemptWorkspaceEntryID, operation model.AttemptWorkspaceMutationKind, origin WorkspaceMutationOrigin, expectedKind model.StarterWorkspaceEntryKind,
	expectedVersion model.WorkspaceContentVersion,
) (WorkspaceMutationResult, error) {
	if stored == nil || stored.SittingID != target.SittingID || stored.ClassID != target.ClassID || stored.CandidateUserID != target.CandidateUserID ||
		stored.WorkspaceID != target.WorkspaceID || stored.Change.Validate() != nil || stored.Change.WorkspaceID != stored.WorkspaceID ||
		stored.Change.Operation != operation || stored.Change.Cursor < 1 || (!stored.Replayed && stored.Change.EntryID != entryID) {
		return WorkspaceMutationResult{}, unavailable(errors.New("inconsistent Attempt Workspace mutation result"))
	}
	if operation == model.AttemptWorkspaceMutationDeleteEntry {
		if stored.Entry != nil {
			return WorkspaceMutationResult{}, unavailable(errors.New("deleted Workspace entry remained visible"))
		}
		return WorkspaceMutationResult{AttemptID: attemptID, SittingID: stored.SittingID, CandidateUserID: stored.CandidateUserID,
			WorkspaceID: stored.WorkspaceID, Change: stored.Change, Replayed: stored.Replayed, Origin: origin}, nil
	}
	if !validCandidateWorkspaceItem(stored.Entry) || stored.Entry.EntryID != stored.Change.EntryID || stored.Entry.Path != stored.Change.NewPath ||
		(!stored.Replayed && stored.Entry.EntryID != entryID) ||
		(expectedKind != "" && stored.Entry.Kind != expectedKind) ||
		(!stored.Replayed && expectedVersion.IsValid() && (stored.Entry.ContentVersion != expectedVersion || stored.Change.ContentVersion != expectedVersion)) {
		return WorkspaceMutationResult{}, unavailable(errors.New("inconsistent Attempt Workspace entry result"))
	}
	entry := *stored.Entry
	return WorkspaceMutationResult{AttemptID: attemptID, SittingID: stored.SittingID, CandidateUserID: stored.CandidateUserID,
		WorkspaceID: stored.WorkspaceID, Entry: &entry, Change: stored.Change, Replayed: stored.Replayed, Origin: origin}, nil
}

func (service *Service) resolveWorkspaceMutationTarget(ctx context.Context, access store.ExamAttemptWorkspaceMutationAccess) (*store.ExamAttemptWorkspaceMutationTarget, error) {
	target, err := service.deps.Workspace.ResolveMutationTarget(ctx, access)
	if err != nil {
		return nil, mapStore(err)
	}
	if !validWorkspaceTarget(target, access) {
		return nil, unavailable(errors.New("inconsistent Attempt Workspace mutation target"))
	}
	return target, nil
}

func (service *Service) applyWorkspaceMutation(ctx context.Context, call Call, target *store.ExamAttemptWorkspaceMutationTarget,
	mutation *store.ExamAttemptWorkspaceMutation, idempotency *store.CommandIdempotency,
	origin WorkspaceMutationOrigin, expectedKind model.StarterWorkspaceEntryKind, expectedVersion model.WorkspaceContentVersion,
) (WorkspaceMutationResult, error) {
	auditID, err := service.deps.Auditor.Begin(ctx, call, model.ActionExamSittingParticipate,
		model.Resource{Type: model.ResourceExamSitting, ID: target.SittingID.String()}, model.RoleScopeClass,
		target.ClassID.String(), store.ExamAttemptWorkspaceMutationOperation,
		map[string]any{"exam_attempt_id": mutation.Access.AttemptID.String(),
			"operation": string(mutation.Operation)})
	if err != nil {
		return WorkspaceMutationResult{}, err
	}
	mutation.AuditEventID, mutation.AuditAt = auditID, model.MillisFromTime(model.TimeUTC(service.deps.Now()))
	stored, err := service.deps.Workspace.ApplyMutation(ctx, mutation, idempotency)
	if err != nil {
		return WorkspaceMutationResult{}, service.failAudit(ctx, auditID, err)
	}
	result, err := projectWorkspaceMutation(stored, target, mutation.Access.AttemptID, mutation.EntryID, mutation.Operation, origin, expectedKind, expectedVersion)
	if err != nil {
		return WorkspaceMutationResult{}, err
	}
	if !result.Replayed {
		if effectErr := service.deps.Effects.WorkspaceChanged(ctx, result); effectErr != nil {
			service.deps.EffectFailures.Report(ctx, "exam_attempt_workspace_changed", effectErr)
		}
	}
	return result, nil
}

func validateWorkspaceEntryFence(entryID model.AttemptWorkspaceEntryID, expectedPath string) (string, error) {
	if !entryID.IsValid() {
		return "", invalid("workspace_command")
	}
	path, err := validateWorkspacePath(expectedPath)
	if err != nil {
		return "", invalidCause("workspace_path", err)
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
		return "", "", invalidCause("workspace_destination_path", err)
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
	var fault *Fault
	return errors.As(err, &fault) && fault.Code == "exam.attempt.unavailable"
}
