// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"strings"
	"sync"
	"time"

	examattempt "github.com/sudosylabs/proctor/server/app/exam/attempt"
	appexecution "github.com/sudosylabs/proctor/server/app/execution"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

type examAttemptTerminalUseCases interface {
	Open(context.Context, Invocation, OpenCandidateExamTerminalCommand) (CandidateExamTerminal, error)
}

type examAttemptTerminalAttempts interface {
	GetPresentation(context.Context, examattempt.Call, examattempt.CandidateAccess) (examattempt.Presentation, error)
	ListWorkspace(context.Context, examattempt.Call, examattempt.WorkspaceQuery) (examattempt.WorkspacePage, error)
	CreateWorkspaceDirectory(context.Context, examattempt.Call, examattempt.CreateWorkspaceDirectoryCommand) (examattempt.WorkspaceMutationResult, error)
	CreateWorkspaceFile(context.Context, examattempt.Call, examattempt.CreateWorkspaceFileCommand) (examattempt.WorkspaceMutationResult, error)
	ReplaceWorkspaceFile(context.Context, examattempt.Call, examattempt.ReplaceWorkspaceFileCommand) (examattempt.WorkspaceMutationResult, error)
	MoveWorkspaceEntry(context.Context, examattempt.Call, examattempt.MoveWorkspaceEntryCommand) (examattempt.WorkspaceMutationResult, error)
	DeleteWorkspaceEntry(context.Context, examattempt.Call, examattempt.DeleteWorkspaceEntryCommand) (examattempt.WorkspaceMutationResult, error)
}

type examAttemptTerminalExecution interface {
	Ensure(context.Context, appexecution.Request) (*appexecution.Placement, error)
	Watch(context.Context, model.ExamAttemptID, appexecution.Cursor) (appexecution.Observation, error)
	Attach(context.Context, model.ExamAttemptID, appexecution.Window) (appexecution.Terminal, error)
	OpenFile(context.Context, model.ExamAttemptID, string) (io.ReadCloser, error)
	ReleaseGrant(context.Context, model.ExecutionGrantID) error
}

type examAttemptTerminalAuditor interface {
	Begin(context.Context, Invocation, examattempt.Presentation, OpenCandidateExamTerminalCommand) (string, error)
	Complete(context.Context, string, model.AuditStatus, string, map[string]any) error
}

type examAttemptTerminalService struct {
	attempts  examAttemptTerminalAttempts
	execution examAttemptTerminalExecution
	audit     examAttemptTerminalAuditor
}

func newExamAttemptTerminalService(attempts examAttemptTerminalAttempts, execution examAttemptTerminalExecution,
	audit examAttemptTerminalAuditor,
) (*examAttemptTerminalService, error) {
	if attempts == nil || execution == nil || audit == nil {
		return nil, errors.New("Exam Attempt Terminal dependencies are required")
	}
	return &examAttemptTerminalService{attempts: attempts, execution: execution, audit: audit}, nil
}

func (service *examAttemptTerminalService) Open(ctx context.Context, invocation Invocation,
	command OpenCandidateExamTerminalCommand,
) (CandidateExamTerminal, error) {
	if service == nil || service.attempts == nil || service.execution == nil || service.audit == nil ||
		!command.SittingID.IsValid() || !command.ClassID.IsValid() || !command.ParticipationID.IsValid() ||
		command.Generation < 1 || command.Window.Cols < 1 || command.Window.Rows < 1 {
		return nil, NewError("exam.attempt.terminal_unavailable")
	}
	presentation, err := service.attempts.GetPresentation(ctx,
		examattempt.NewCall(invocation.Principal(), invocation.RequestMetadata()), command.Access)
	if err != nil {
		return nil, examAttemptError(err, true)
	}
	if presentation.AttemptID != command.Access.AttemptID || presentation.SittingID != command.SittingID ||
		presentation.ClassID != command.ClassID {
		return nil, NewError("exam.attempt.terminal_unavailable")
	}
	profile := presentation.ExecutionProfile
	if !profile.Enabled {
		return nil, NewError("exam.attempt.terminal_disabled")
	}
	switch presentation.RuntimeCapabilities.Terminal.State {
	case store.CandidateTerminalAvailable, store.CandidateTerminalTemporarilyUnavailable:
	case store.CandidateTerminalDisabled:
		return nil, NewError("exam.attempt.terminal_disabled")
	default:
		return nil, NewError("exam.attempt.terminal_unavailable")
	}
	auditID, err := service.audit.Begin(ctx, invocation, presentation, command)
	if err != nil {
		return nil, err
	}
	placement, err := service.execution.Ensure(ctx, appexecution.Request{
		AttemptID: presentation.AttemptID, Image: profile.Image, Network: appexecution.Network(profile.Network),
	})
	if err != nil {
		return nil, service.failAudit(ctx, auditID, executionError(err))
	}
	if placement == nil || !placement.GrantID.IsValid() || placement.AttemptID != presentation.AttemptID || !placement.Ready {
		service.releasePlacement(ctx, placement)
		return nil, service.failAudit(ctx, auditID, NewError("exam.attempt.terminal_unavailable"))
	}
	observation, err := service.execution.Watch(ctx, presentation.AttemptID, "")
	if err != nil || observation == nil {
		service.releasePlacement(ctx, placement)
		if err == nil {
			err = appexecution.ErrUnavailable
		}
		return nil, service.failAudit(ctx, auditID, executionError(err))
	}
	terminal, err := service.execution.Attach(ctx, presentation.AttemptID, command.Window)
	if err != nil || terminal == nil {
		_ = observation.Close()
		service.releasePlacement(ctx, placement)
		if err == nil {
			err = appexecution.ErrUnavailable
		}
		return nil, service.failAudit(ctx, auditID, executionError(err))
	}
	if err := service.audit.Complete(ctx, auditID, model.AuditStatusSuccess, "", map[string]any{
		"exam_attempt_id": presentation.AttemptID.String(), "execution_grant_id": placement.GrantID.String(),
	}); err != nil {
		_ = observation.Close()
		_ = terminal.Close()
		service.releasePlacement(ctx, placement)
		return nil, err
	}
	watchCtx, cancel := context.WithCancel(ctx)
	wrapped := &candidateExamTerminal{terminal: terminal, cancel: cancel, observation: observation}
	call := examattempt.NewCall(invocation.Principal(), invocation.RequestMetadata())
	wrapped.validate = func(validateCtx context.Context) error {
		current, validateErr := service.attempts.GetPresentation(validateCtx, call, command.Access)
		if validateErr != nil {
			return examAttemptError(validateErr, true)
		}
		if current.AttemptID != presentation.AttemptID || current.SittingID != command.SittingID ||
			current.ClassID != command.ClassID || current.RuntimeCapabilities.Terminal.State != store.CandidateTerminalAvailable {
			return NewError("exam.attempt.terminal_unavailable")
		}
		return nil
	}
	wrapped.onInvalid = func() {
		service.releaseGrant(context.WithoutCancel(ctx), placement.GrantID)
		wrapped.completeFailure()
	}
	go service.synchronizeWorkspace(watchCtx, invocation, command, placement.GrantID, observation, wrapped)
	return wrapped, nil
}

func (service *examAttemptTerminalService) releasePlacement(ctx context.Context, placement *appexecution.Placement) {
	if placement != nil && placement.GrantID.IsValid() {
		service.releaseGrant(ctx, placement.GrantID)
	}
}

func (service *examAttemptTerminalService) releaseGrant(ctx context.Context, grantID model.ExecutionGrantID) {
	releaseCtx := context.WithoutCancel(ctx)
	for delay := 25 * time.Millisecond; ; delay = min(delay*2, time.Second) {
		if err := service.execution.ReleaseGrant(releaseCtx, grantID); err == nil {
			return
		}
		timer := time.NewTimer(delay)
		<-timer.C
	}
}

func (service *examAttemptTerminalService) failAudit(ctx context.Context, auditID string, failure error) error {
	code := "exam.attempt.terminal_unavailable"
	if appErr, ok := As(failure); ok {
		code = appErr.Code()
	}
	if err := service.audit.Complete(ctx, auditID, model.AuditStatusFail, code, nil); err != nil {
		return err
	}
	return failure
}

type examAttemptTerminalAuditAdapter struct{ audit *auditService }

func (adapter examAttemptTerminalAuditAdapter) Begin(ctx context.Context, invocation Invocation,
	presentation examattempt.Presentation, command OpenCandidateExamTerminalCommand,
) (string, error) {
	event, err := adapter.audit.BeginCriticalActionAtScope(ctx, invocation.Principal(), model.ActionExamSittingParticipate,
		model.Resource{Type: model.ResourceExamSitting, ID: command.SittingID.String()}, model.RoleScopeClass,
		command.ClassID.String(), invocation.RequestMetadata(), map[string]any{"operation": "open_attempt_terminal", "value": map[string]any{
			"exam_attempt_id": presentation.AttemptID.String(), "generation": command.Generation,
			"image": presentation.ExecutionProfile.Image, "network": string(presentation.ExecutionProfile.Network),
		}}, nil)
	if err != nil {
		return "", err
	}
	return event.ID.String(), nil
}

func (adapter examAttemptTerminalAuditAdapter) Complete(ctx context.Context, auditID string, status model.AuditStatus,
	code string, value map[string]any,
) error {
	_, err := adapter.audit.CompleteCriticalAction(ctx, auditID, status, code, value)
	return err
}

type candidateExamTerminal struct {
	terminal  appexecution.Terminal
	cancel    context.CancelFunc
	validate  func(context.Context) error
	onInvalid func()

	mu                 sync.Mutex
	observation        appexecution.Observation
	failure            error
	failureFence       chan struct{}
	failureFenceClosed bool
	closed             bool
	closeErr           error
	closeOnce          sync.Once
	terminalCloseErr   error
	terminalCloseOnce  sync.Once
}

func (terminal *candidateExamTerminal) Read(buffer []byte) (int, error) {
	count, err := terminal.terminal.Read(buffer)
	terminal.mu.Lock()
	failure := terminal.failure
	failureFence := terminal.failureFence
	terminal.mu.Unlock()
	if failure != nil {
		if failureFence != nil {
			<-failureFence
		}
		return count, failure
	}
	return count, err
}

func (terminal *candidateExamTerminal) Write(buffer []byte) (int, error) {
	terminal.mu.Lock()
	failure, closed := terminal.failure, terminal.closed
	terminal.mu.Unlock()
	if failure != nil {
		return 0, failure
	}
	if closed {
		return 0, io.ErrClosedPipe
	}
	validationCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := terminal.validateInteraction(validationCtx); err != nil {
		return 0, err
	}
	return terminal.terminal.Write(buffer)
}
func (terminal *candidateExamTerminal) Resize(ctx context.Context, window appexecution.Window) error {
	terminal.mu.Lock()
	failure, closed := terminal.failure, terminal.closed
	terminal.mu.Unlock()
	if failure != nil {
		return failure
	}
	if closed {
		return io.ErrClosedPipe
	}
	if err := terminal.validateInteraction(ctx); err != nil {
		return err
	}
	return terminal.terminal.Resize(ctx, window)
}

func (terminal *candidateExamTerminal) validateInteraction(ctx context.Context) error {
	if terminal.validate == nil {
		return nil
	}
	if err := terminal.validate(ctx); err != nil {
		if terminal.beginFailure(err) {
			if terminal.onInvalid != nil {
				go terminal.onInvalid()
			} else {
				terminal.completeFailure()
			}
		}
		return err
	}
	return nil
}

func (terminal *candidateExamTerminal) fail(err error) {
	if !terminal.beginFailure(err) {
		return
	}
	terminal.completeFailure()
}

// beginFailure fences new PTY writes and normal Close before publishing the
// asynchronous failure. The caller must durably release the exact grant and
// then call completeFailure; until then a WebSocket cannot expose a reopen that
// could attach the placement being released.
func (terminal *candidateExamTerminal) beginFailure(err error) bool {
	if err == nil {
		return false
	}
	terminal.mu.Lock()
	if terminal.closed || terminal.failure != nil {
		terminal.mu.Unlock()
		return false
	}
	terminal.failure = err
	terminal.failureFence = make(chan struct{})
	cancel := terminal.cancel
	terminal.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	// Stop the guest PTY immediately. Read deliberately waits on failureFence
	// before returning the resulting adapter error, so the transport cannot
	// advertise a reopen until the exact durable grant release succeeds.
	_ = terminal.closeUnderlyingTerminal()
	return true
}

func (terminal *candidateExamTerminal) completeFailure() {
	terminal.mu.Lock()
	if terminal.failureFence != nil && !terminal.failureFenceClosed {
		close(terminal.failureFence)
		terminal.failureFenceClosed = true
	}
	terminal.mu.Unlock()
	// Close owns the single adapter-close path. Calling it only after the grant
	// fence opens wakes the blocked reader without making a stale grant reopenable.
	_ = terminal.Close()
}

func (terminal *candidateExamTerminal) detachObservation() appexecution.Observation {
	terminal.mu.Lock()
	observation := terminal.observation
	terminal.observation = nil
	terminal.mu.Unlock()
	return observation
}

func (terminal *candidateExamTerminal) closeUnderlyingTerminal() error {
	terminal.terminalCloseOnce.Do(func() {
		err := terminal.terminal.Close()
		terminal.mu.Lock()
		terminal.terminalCloseErr = err
		terminal.mu.Unlock()
	})
	terminal.mu.Lock()
	err := terminal.terminalCloseErr
	terminal.mu.Unlock()
	return err
}

func (terminal *candidateExamTerminal) Close() error {
	terminal.mu.Lock()
	failureFence := terminal.failureFence
	if failureFence == nil {
		// Linearize a normal caller close against beginFailure. If Close wins,
		// the watcher observes cancellation and does not invent grant ownership.
		terminal.closed = true
	}
	terminal.mu.Unlock()
	if failureFence != nil {
		<-failureFence
	}
	terminal.closeOnce.Do(func() {
		if terminal.cancel != nil {
			terminal.cancel()
		}
		terminal.mu.Lock()
		terminal.closed = true
		observation := terminal.observation
		terminal.observation = nil
		terminal.mu.Unlock()
		var observationErr error
		if observation != nil {
			observationErr = observation.Close()
		}
		closeErr := errors.Join(observationErr, terminal.closeUnderlyingTerminal())
		terminal.mu.Lock()
		terminal.closeErr = closeErr
		terminal.mu.Unlock()
	})
	terminal.mu.Lock()
	err := terminal.closeErr
	terminal.mu.Unlock()
	return err
}

func (service *examAttemptTerminalService) synchronizeWorkspace(ctx context.Context, invocation Invocation,
	command OpenCandidateExamTerminalCommand, grantID model.ExecutionGrantID, observation appexecution.Observation,
	terminal *candidateExamTerminal,
) {
	defer func() {
		if current := terminal.detachObservation(); current != nil {
			_ = current.Close()
		}
	}()
	for {
		event, err := observation.Next(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			service.failActiveTerminal(ctx, grantID, terminal, executionError(err))
			return
		}
		if err := service.applyExecutionEvent(ctx, invocation, command, event); err != nil {
			if ctx.Err() != nil {
				return
			}
			service.failActiveTerminal(ctx, grantID, terminal, err)
			return
		}
	}
}

func (service *examAttemptTerminalService) failActiveTerminal(ctx context.Context, grantID model.ExecutionGrantID,
	terminal *candidateExamTerminal, failure error,
) {
	if !terminal.beginFailure(failure) {
		return
	}
	// beginFailure cancels the connection context and blocks terminal Close.
	// Exact placement cleanup must reach durable state before completeFailure
	// wakes the reader and permits the transport to expose another open.
	service.releaseGrant(ctx, grantID)
	terminal.completeFailure()
}

func ignoredExecutionPath(path string) bool {
	if path == "" {
		return false
	}
	for _, segment := range strings.Split(path, "/") {
		switch segment {
		case ".proctor", ".git", "node_modules", "target", "__pycache__":
			return true
		}
	}
	return false
}

func executionPathParent(path string) string {
	index := strings.LastIndexByte(path, '/')
	if index < 0 {
		return ""
	}
	return path[:index]
}

func executionCreateParentsAreAuthoritative(path string, byPath map[string]CandidateExamWorkspaceItem) bool {
	for parent := executionPathParent(path); parent != ""; parent = executionPathParent(parent) {
		item, exists := byPath[parent]
		if !exists || item.Kind != model.StarterWorkspaceEntryDirectory {
			return false
		}
	}
	return true
}

func executionDeleteHasDirectoryAncestor(path string, byPath map[string]CandidateExamWorkspaceItem) bool {
	for parent := executionPathParent(path); parent != ""; parent = executionPathParent(parent) {
		if item, exists := byPath[parent]; exists && item.Kind == model.StarterWorkspaceEntryDirectory {
			return true
		}
	}
	return false
}

func (service *examAttemptTerminalService) applyExecutionEvent(ctx context.Context, invocation Invocation,
	command OpenCandidateExamTerminalCommand, event appexecution.Event,
) error {
	pathIgnored, fromIgnored := ignoredExecutionPath(event.Path), ignoredExecutionPath(event.From)
	if event.Operation == appexecution.OperationMove {
		if pathIgnored && fromIgnored {
			return nil
		}
	} else if pathIgnored {
		return nil
	}
	items, err := service.workspaceManifest(ctx, invocation, command.Access)
	if err != nil {
		return err
	}
	byPath := make(map[string]CandidateExamWorkspaceItem, len(items))
	for _, item := range items {
		byPath[item.Path] = item
	}
	access := ExamAttemptWorkspaceMutationAccess{CandidateAccess: command.Access,
		ParticipationID: command.ParticipationID, Generation: command.Generation}
	call := examattempt.NewCall(invocation.Principal(), invocation.RequestMetadata())
	key := executionEventIdempotency(event)
	switch event.Operation {
	case appexecution.OperationCreate:
		if _, exists := byPath[event.Path]; exists {
			return errors.New("execution create conflicts with authoritative workspace")
		}
		// execenv v0.2's isolated watcher expands directory renames into an
		// unordered run of deletes followed by creates without a batch fence.
		// A child whose parent is not already authoritative can therefore be one
		// member of an unrepresentable directory topology; reject before mutating.
		if !executionCreateParentsAreAuthoritative(event.Path, byPath) {
			return errors.New("execution create parent topology is not authoritative")
		}
		body, openErr := service.execution.OpenFile(ctx, command.Access.AttemptID, event.Path)
		if errors.Is(openErr, appexecution.ErrNotFound) {
			return errors.New("execution directory create lacks an atomic host event")
		}
		if openErr != nil {
			return executionError(openErr)
		}
		return service.persistExecutionFile(ctx, call, access, event.Path, CandidateExamWorkspaceItem{}, body, key, false)
	case appexecution.OperationReplace:
		item, exists := byPath[event.Path]
		if !exists || item.Kind != model.StarterWorkspaceEntryFile {
			return errors.New("execution replace target is not an authoritative file")
		}
		body, openErr := service.execution.OpenFile(ctx, command.Access.AttemptID, event.Path)
		if openErr != nil {
			return executionError(openErr)
		}
		return service.persistExecutionFile(ctx, call, access, event.Path, item, body, key, true)
	case appexecution.OperationMove:
		if fromIgnored {
			if _, exists := byPath[event.Path]; exists {
				return errors.New("execution move destination conflicts with authoritative workspace")
			}
			body, openErr := service.execution.OpenFile(ctx, command.Access.AttemptID, event.Path)
			if errors.Is(openErr, appexecution.ErrNotFound) {
				return errors.New("execution cannot acknowledge a directory moved out of an ignored tree")
			}
			if openErr != nil {
				return executionError(openErr)
			}
			return service.persistExecutionFile(ctx, call, access, event.Path, CandidateExamWorkspaceItem{}, body, key, false)
		}
		item, exists := byPath[event.From]
		if !exists {
			return errors.New("execution move source is not authoritative")
		}
		if pathIgnored {
			if item.Kind == model.StarterWorkspaceEntryDirectory {
				return errors.New("execution cannot acknowledge a directory moved into an ignored tree")
			}
			_, err = service.attempts.DeleteWorkspaceEntry(ctx, call, examattempt.DeleteWorkspaceEntryCommand{
				Access: access, Origin: examattempt.WorkspaceMutationOriginExecutionHost, EntryID: item.EntryID,
				ExpectedPath: event.From, ExpectedContentVersion: item.ContentVersion, IdempotencyKey: key})
			return mapTerminalAttemptError(err)
		}
		if item.Kind == model.StarterWorkspaceEntryDirectory {
			body, openErr := service.execution.OpenFile(ctx, command.Access.AttemptID, event.Path)
			if openErr == nil {
				_ = body.Close()
				return errors.New("execution cannot atomically replace a directory with a file")
			}
			if !errors.Is(openErr, appexecution.ErrNotFound) {
				return executionError(openErr)
			}
		}
		_, err = service.attempts.MoveWorkspaceEntry(ctx, call, examattempt.MoveWorkspaceEntryCommand{
			Access: access, Origin: examattempt.WorkspaceMutationOriginExecutionHost, EntryID: item.EntryID,
			ExpectedPath: event.From, DestinationPath: event.Path, IdempotencyKey: key})
		return mapTerminalAttemptError(err)
	case appexecution.OperationDelete:
		item, exists := byPath[event.Path]
		if !exists {
			return errors.New("execution delete target is not authoritative")
		}
		// Directory deletes and descendant deletes are ambiguous in execenv
		// v0.2: either may be the first event from a directory rename whose
		// remaining members have not arrived. Failing here prevents any partial
		// authoritative projection. Atomic OperationMove remains supported.
		if item.Kind == model.StarterWorkspaceEntryDirectory || executionDeleteHasDirectoryAncestor(event.Path, byPath) {
			return errors.New("execution directory delete topology lacks an atomic host event")
		}
		_, err = service.attempts.DeleteWorkspaceEntry(ctx, call, examattempt.DeleteWorkspaceEntryCommand{
			Access: access, Origin: examattempt.WorkspaceMutationOriginExecutionHost, EntryID: item.EntryID,
			ExpectedPath: event.Path, ExpectedContentVersion: item.ContentVersion, IdempotencyKey: key})
		return mapTerminalAttemptError(err)
	default:
		return errors.New("unsupported execution workspace event")
	}
}

func mapTerminalAttemptError(err error) error {
	if err == nil {
		return nil
	}
	return examAttemptError(err, true)
}

func (service *examAttemptTerminalService) workspaceManifest(ctx context.Context, invocation Invocation,
	access CandidateExamAttemptAccess,
) ([]CandidateExamWorkspaceItem, error) {
	result := make([]CandidateExamWorkspaceItem, 0)
	expected := int64(-1)
	var after model.AttemptWorkspaceEntryID
	call := examattempt.NewCall(invocation.Principal(), invocation.RequestMetadata())
	for {
		page, err := service.attempts.ListWorkspace(ctx, call, examattempt.WorkspaceQuery{Access: access,
			ExpectedCursor: expected, AfterEntryID: after, Limit: model.AttemptWorkspaceJournalReadMaximum})
		if err != nil {
			return nil, examAttemptError(err, true)
		}
		if page.RefreshRequired {
			return nil, errors.New("execution workspace manifest changed during read")
		}
		if expected >= 0 && page.Cursor != expected {
			return nil, errors.New("execution workspace manifest cursor changed during read")
		}
		if len(page.Items) > model.AttemptWorkspaceMaximumEntries-len(result) {
			return nil, errors.New("execution workspace manifest exceeds entry limit")
		}
		result = append(result, page.Items...)
		if !page.HasMore {
			return result, nil
		}
		if len(page.Items) == 0 {
			return nil, errors.New("execution workspace pagination made no progress")
		}
		if expected == -1 {
			expected = page.Cursor
		}
		after = page.Items[len(page.Items)-1].EntryID
	}
}

func (service *examAttemptTerminalService) persistExecutionFile(ctx context.Context, call examattempt.Call,
	access ExamAttemptWorkspaceMutationAccess, path string, item CandidateExamWorkspaceItem, body io.ReadCloser,
	key string, replace bool,
) error {
	defer body.Close()
	data, err := readBoundedExecutionFile(body, model.AttemptWorkspaceMaximumFileBytes)
	if err != nil {
		return err
	}
	digest := sha256.Sum256(data)
	sha := hex.EncodeToString(digest[:])
	if replace {
		_, err = service.attempts.ReplaceWorkspaceFile(ctx, call, examattempt.ReplaceWorkspaceFileCommand{
			Access: access, Origin: examattempt.WorkspaceMutationOriginExecutionHost, EntryID: item.EntryID,
			ExpectedPath: path, ExpectedContentVersion: item.ContentVersion, MediaType: "application/octet-stream",
			ExpectedSHA256: sha, Body: bytes.NewReader(data), Size: int64(len(data)), IdempotencyKey: key})
	} else {
		_, err = service.attempts.CreateWorkspaceFile(ctx, call, examattempt.CreateWorkspaceFileCommand{
			Access: access, Origin: examattempt.WorkspaceMutationOriginExecutionHost, Path: path,
			MediaType: "application/octet-stream", ExpectedSHA256: sha, Body: bytes.NewReader(data),
			Size: int64(len(data)), IdempotencyKey: key})
	}
	return mapTerminalAttemptError(err)
}

func readBoundedExecutionFile(body io.Reader, maximum int64) ([]byte, error) {
	if body == nil || maximum < 0 {
		return nil, errors.New("execution file reader is invalid")
	}
	data, err := io.ReadAll(io.LimitReader(body, maximum+1))
	if err != nil || int64(len(data)) > maximum {
		return nil, errors.Join(err, errors.New("execution file exceeds workspace limit"))
	}
	return data, nil
}

func executionEventIdempotency(event appexecution.Event) string {
	digest := sha256.Sum256([]byte(event.Cursor))
	return "execution-" + hex.EncodeToString(digest[:])
}

func executionError(err error) error {
	switch {
	case errors.Is(err, appexecution.ErrInvalid):
		return NewError("exam.attempt.terminal_invalid").Wrap(err)
	case errors.Is(err, appexecution.ErrConflict):
		return NewError("exam.attempt.terminal_conflict").Wrap(err)
	case errors.Is(err, appexecution.ErrCapacity):
		return NewError("exam.attempt.terminal_capacity").Wrap(err)
	default:
		return NewError("exam.attempt.terminal_unavailable").Wrap(err)
	}
}
