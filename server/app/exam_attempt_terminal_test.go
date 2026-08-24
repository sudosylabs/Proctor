// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"sync"
	"testing"
	"time"

	examattempt "github.com/sudosylabs/proctor/server/app/exam/attempt"
	appexecution "github.com/sudosylabs/proctor/server/app/execution"
	"github.com/sudosylabs/proctor/server/model"
)

type terminalAttemptPortFake struct {
	mu           sync.Mutex
	presentation examattempt.Presentation
	err          error
	calls        int
}

func (fake *terminalAttemptPortFake) GetPresentation(context.Context, examattempt.Call, examattempt.CandidateAccess) (examattempt.Presentation, error) {
	fake.mu.Lock()
	defer fake.mu.Unlock()
	fake.calls++
	return fake.presentation, fake.err
}

func (*terminalAttemptPortFake) ListWorkspace(context.Context, examattempt.Call, examattempt.WorkspaceQuery) (examattempt.WorkspacePage, error) {
	return examattempt.WorkspacePage{}, nil
}

func (*terminalAttemptPortFake) CreateWorkspaceDirectory(context.Context, examattempt.Call, examattempt.CreateWorkspaceDirectoryCommand) (examattempt.WorkspaceMutationResult, error) {
	return examattempt.WorkspaceMutationResult{}, nil
}

func (*terminalAttemptPortFake) CreateWorkspaceFile(context.Context, examattempt.Call, examattempt.CreateWorkspaceFileCommand) (examattempt.WorkspaceMutationResult, error) {
	return examattempt.WorkspaceMutationResult{}, nil
}

func (*terminalAttemptPortFake) ReplaceWorkspaceFile(context.Context, examattempt.Call, examattempt.ReplaceWorkspaceFileCommand) (examattempt.WorkspaceMutationResult, error) {
	return examattempt.WorkspaceMutationResult{}, nil
}

func (*terminalAttemptPortFake) MoveWorkspaceEntry(context.Context, examattempt.Call, examattempt.MoveWorkspaceEntryCommand) (examattempt.WorkspaceMutationResult, error) {
	return examattempt.WorkspaceMutationResult{}, nil
}

func (*terminalAttemptPortFake) DeleteWorkspaceEntry(context.Context, examattempt.Call, examattempt.DeleteWorkspaceEntryCommand) (examattempt.WorkspaceMutationResult, error) {
	return examattempt.WorkspaceMutationResult{}, nil
}

type terminalExecutionPortFake struct {
	mu             sync.Mutex
	order          []string
	placement      *appexecution.Placement
	ensureErr      error
	observation    appexecution.Observation
	watchErr       error
	terminal       appexecution.Terminal
	attachErr      error
	releaseErr     error
	releaseErrs    []error
	ensureCalls    int
	watchCalls     int
	attachCalls    int
	releaseCalls   int
	releaseIDs     []model.ExecutionGrantID
	releaseCtxErrs []error
	openFileCalls  int
}

func (fake *terminalExecutionPortFake) appendOrder(value string) {
	fake.mu.Lock()
	fake.order = append(fake.order, value)
	fake.mu.Unlock()
}

func (fake *terminalExecutionPortFake) Ensure(context.Context, appexecution.Request) (*appexecution.Placement, error) {
	fake.mu.Lock()
	fake.order = append(fake.order, "ensure")
	fake.ensureCalls++
	value, err := fake.placement, fake.ensureErr
	fake.mu.Unlock()
	return value, err
}

func (fake *terminalExecutionPortFake) Watch(context.Context, model.ExamAttemptID, appexecution.Cursor) (appexecution.Observation, error) {
	fake.mu.Lock()
	fake.order = append(fake.order, "watch")
	fake.watchCalls++
	value, err := fake.observation, fake.watchErr
	fake.mu.Unlock()
	return value, err
}

func (fake *terminalExecutionPortFake) Attach(context.Context, model.ExamAttemptID, appexecution.Window) (appexecution.Terminal, error) {
	fake.mu.Lock()
	fake.order = append(fake.order, "attach")
	fake.attachCalls++
	value, err := fake.terminal, fake.attachErr
	fake.mu.Unlock()
	return value, err
}

func (fake *terminalExecutionPortFake) OpenFile(context.Context, model.ExamAttemptID, string) (io.ReadCloser, error) {
	fake.mu.Lock()
	fake.openFileCalls++
	fake.mu.Unlock()
	return nil, appexecution.ErrNotFound
}

func (fake *terminalExecutionPortFake) ReleaseGrant(ctx context.Context, grantID model.ExecutionGrantID) error {
	fake.mu.Lock()
	fake.order = append(fake.order, "release")
	fake.releaseCalls++
	fake.releaseIDs = append(fake.releaseIDs, grantID)
	fake.releaseCtxErrs = append(fake.releaseCtxErrs, ctx.Err())
	err := fake.releaseErr
	if index := fake.releaseCalls - 1; index < len(fake.releaseErrs) {
		err = fake.releaseErrs[index]
	}
	fake.mu.Unlock()
	return err
}

type terminalAuditPortFake struct {
	mu                 sync.Mutex
	order              *[]string
	beginErr           error
	completeSuccessErr error
	completeFailureErr error
	beginCalls         int
	statuses           []model.AuditStatus
	values             []map[string]any
}

func (fake *terminalAuditPortFake) appendOrder(value string) {
	if fake.order != nil {
		*fake.order = append(*fake.order, value)
	}
}

func (fake *terminalAuditPortFake) Begin(context.Context, Invocation, examattempt.Presentation, OpenCandidateExamTerminalCommand) (string, error) {
	fake.mu.Lock()
	defer fake.mu.Unlock()
	fake.appendOrder("audit_begin")
	fake.beginCalls++
	return model.NewAuditEventID().String(), fake.beginErr
}

func (fake *terminalAuditPortFake) Complete(_ context.Context, _ string, status model.AuditStatus, _ string, value map[string]any) error {
	fake.mu.Lock()
	defer fake.mu.Unlock()
	if status == model.AuditStatusSuccess {
		fake.appendOrder("audit_success")
	} else {
		fake.appendOrder("audit_failure")
	}
	fake.statuses = append(fake.statuses, status)
	fake.values = append(fake.values, value)
	if status == model.AuditStatusSuccess {
		return fake.completeSuccessErr
	}
	return fake.completeFailureErr
}

type terminalTrackedObservation struct {
	mu         sync.Mutex
	closeCalls int
	closeErr   error
}

func (*terminalTrackedObservation) Cursor() appexecution.Cursor { return "cursor" }

func (*terminalTrackedObservation) Next(ctx context.Context) (appexecution.Event, error) {
	<-ctx.Done()
	return appexecution.Event{}, ctx.Err()
}

func (observation *terminalTrackedObservation) Close() error {
	observation.mu.Lock()
	defer observation.mu.Unlock()
	observation.closeCalls++
	return observation.closeErr
}

type terminalTrackedPTY struct {
	mu         sync.Mutex
	closed     chan struct{}
	closeOnce  sync.Once
	closeCalls int
	writeCalls int
	closeErr   error
}

func newTerminalTrackedPTY() *terminalTrackedPTY {
	return &terminalTrackedPTY{closed: make(chan struct{})}
}

func (terminal *terminalTrackedPTY) Read([]byte) (int, error) {
	<-terminal.closed
	return 0, io.EOF
}

func (terminal *terminalTrackedPTY) Write(data []byte) (int, error) {
	terminal.mu.Lock()
	terminal.writeCalls++
	terminal.mu.Unlock()
	return len(data), nil
}

func (*terminalTrackedPTY) Resize(context.Context, appexecution.Window) error { return nil }

func (terminal *terminalTrackedPTY) Close() error {
	terminal.mu.Lock()
	terminal.closeCalls++
	terminal.mu.Unlock()
	terminal.closeOnce.Do(func() { close(terminal.closed) })
	return terminal.closeErr
}

func validTerminalOpenFixture() (examattempt.Presentation, OpenCandidateExamTerminalCommand) {
	attemptID, sittingID, classID := model.NewExamAttemptID(), model.NewExamSittingID(), model.NewClassID()
	presentation := examattempt.Presentation{
		AttemptID: attemptID, SittingID: sittingID, ClassID: classID,
		ExecutionProfile: model.ExecutionProfile{Enabled: true, Image: "go", Network: model.ExecutionNetworkNone},
	}
	command := OpenCandidateExamTerminalCommand{
		Access: CandidateExamAttemptAccess{AttemptID: attemptID, ConnectionID: model.NewAttemptConnectionID(),
			ContinuityCredential: model.NewCredentialToken()},
		SittingID: sittingID, ClassID: classID, ParticipationID: model.NewAttemptParticipationID(),
		Generation: 1, Window: CandidateExamTerminalWindow{Cols: 80, Rows: 24},
	}
	return presentation, command
}

func TestExamAttemptTerminalOpenOrderingAndOwnership(t *testing.T) {
	t.Parallel()
	presentation, command := validTerminalOpenFixture()
	observation, native := &terminalTrackedObservation{}, newTerminalTrackedPTY()
	execution := &terminalExecutionPortFake{
		placement:   &appexecution.Placement{GrantID: model.NewExecutionGrantID(), AttemptID: presentation.AttemptID, Ready: true},
		observation: observation, terminal: native,
	}
	audit := &terminalAuditPortFake{order: &execution.order}
	service, err := newExamAttemptTerminalService(&terminalAttemptPortFake{presentation: presentation}, execution, audit)
	if err != nil {
		t.Fatal(err)
	}
	terminal, err := service.Open(context.Background(), NewInvocation(examAttemptPrincipal(), model.RequestMetadata{}), command)
	if err != nil {
		t.Fatal(err)
	}
	execution.mu.Lock()
	order := append([]string(nil), execution.order...)
	releases := execution.releaseCalls
	execution.mu.Unlock()
	if stringsJoin(order) != "audit_begin,ensure,watch,attach,audit_success" {
		t.Fatalf("open order = %v", order)
	}
	if releases != 0 {
		t.Fatalf("successful open released grant %d time(s)", releases)
	}
	if err = terminal.Close(); err != nil {
		t.Fatal(err)
	}
	audit.mu.Lock()
	value := audit.values[0]
	audit.mu.Unlock()
	if value["exam_attempt_id"] != presentation.AttemptID.String() || value["execution_grant_id"] == "" {
		t.Fatalf("success audit value = %#v", value)
	}
}

func stringsJoin(values []string) string {
	result := ""
	for index, value := range values {
		if index != 0 {
			result += ","
		}
		result += value
	}
	return result
}

func TestExamAttemptTerminalRejectsBeforeHostEffects(t *testing.T) {
	t.Parallel()
	presentation, valid := validTerminalOpenFixture()
	tests := []struct {
		name         string
		command      OpenCandidateExamTerminalCommand
		presentation examattempt.Presentation
		attemptErr   error
	}{
		{name: "invalid command", command: OpenCandidateExamTerminalCommand{}, presentation: presentation},
		{name: "presentation denied", command: valid, presentation: presentation,
			attemptErr: &examattempt.Fault{Code: "exam.attempt.not_found"}},
		{name: "identity mismatch", command: valid, presentation: func() examattempt.Presentation {
			value := presentation
			value.SittingID = model.NewExamSittingID()
			return value
		}()},
		{name: "profile disabled", command: valid, presentation: func() examattempt.Presentation {
			value := presentation
			value.ExecutionProfile.Enabled = false
			return value
		}()},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			attempts := &terminalAttemptPortFake{presentation: test.presentation, err: test.attemptErr}
			execution, audit := &terminalExecutionPortFake{}, &terminalAuditPortFake{}
			service := &examAttemptTerminalService{attempts: attempts, execution: execution, audit: audit}
			if _, err := service.Open(context.Background(), NewInvocation(examAttemptPrincipal(), model.RequestMetadata{}), test.command); err == nil {
				t.Fatal("Open succeeded")
			}
			execution.mu.Lock()
			hostCalls := execution.ensureCalls + execution.watchCalls + execution.attachCalls + execution.releaseCalls
			execution.mu.Unlock()
			audit.mu.Lock()
			auditCalls := audit.beginCalls + len(audit.statuses)
			audit.mu.Unlock()
			if hostCalls != 0 || auditCalls != 0 {
				t.Fatalf("host calls=%d audit calls=%d", hostCalls, auditCalls)
			}
		})
	}
}

func TestExamAttemptTerminalPartialFailureCleanup(t *testing.T) {
	t.Parallel()
	presentation, command := validTerminalOpenFixture()
	failure := errors.New("adapter failure")
	auditFailure := errors.New("audit completion failure")
	tests := []struct {
		name            string
		configure       func(*terminalExecutionPortFake, *terminalAuditPortFake)
		wantRelease     int
		wantObservation int
		wantTerminal    int
		wantError       error
	}{
		{name: "Ensure", configure: func(execution *terminalExecutionPortFake, _ *terminalAuditPortFake) {
			execution.ensureErr = appexecution.ErrCapacity
		}},
		{name: "nil placement", configure: func(execution *terminalExecutionPortFake, _ *terminalAuditPortFake) {
			execution.placement = nil
		}},
		{name: "invalid placement", configure: func(execution *terminalExecutionPortFake, _ *terminalAuditPortFake) {
			execution.placement = &appexecution.Placement{}
		}},
		{name: "mismatched placement", configure: func(execution *terminalExecutionPortFake, _ *terminalAuditPortFake) {
			execution.placement = &appexecution.Placement{GrantID: model.NewExecutionGrantID(), AttemptID: model.NewExamAttemptID(), Ready: true}
		}, wantRelease: 1},
		{name: "Watch", configure: func(execution *terminalExecutionPortFake, _ *terminalAuditPortFake) {
			execution.watchErr = failure
		}, wantRelease: 1},
		{name: "nil observation", configure: func(execution *terminalExecutionPortFake, _ *terminalAuditPortFake) {
			execution.observation = nil
		}, wantRelease: 1},
		{name: "Attach", configure: func(execution *terminalExecutionPortFake, _ *terminalAuditPortFake) {
			execution.attachErr = failure
		}, wantRelease: 1, wantObservation: 1},
		{name: "nil terminal", configure: func(execution *terminalExecutionPortFake, _ *terminalAuditPortFake) {
			execution.terminal = nil
		}, wantRelease: 1, wantObservation: 1},
		{name: "success audit", configure: func(_ *terminalExecutionPortFake, audit *terminalAuditPortFake) {
			audit.completeSuccessErr = auditFailure
		}, wantRelease: 1, wantObservation: 1, wantTerminal: 1, wantError: auditFailure},
		{name: "failure audit", configure: func(execution *terminalExecutionPortFake, audit *terminalAuditPortFake) {
			execution.ensureErr = failure
			audit.completeFailureErr = auditFailure
		}, wantError: auditFailure},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			observation, native := &terminalTrackedObservation{}, newTerminalTrackedPTY()
			execution := &terminalExecutionPortFake{
				placement:   &appexecution.Placement{GrantID: model.NewExecutionGrantID(), AttemptID: presentation.AttemptID, Ready: true},
				observation: observation, terminal: native,
			}
			audit := &terminalAuditPortFake{}
			test.configure(execution, audit)
			service := &examAttemptTerminalService{attempts: &terminalAttemptPortFake{presentation: presentation}, execution: execution, audit: audit}
			_, err := service.Open(context.Background(), NewInvocation(examAttemptPrincipal(), model.RequestMetadata{}), command)
			if err == nil {
				t.Fatal("Open succeeded")
			}
			if test.wantError != nil && !errors.Is(err, test.wantError) {
				t.Fatalf("Open error = %v; want %v", err, test.wantError)
			}
			execution.mu.Lock()
			releases := execution.releaseCalls
			releaseIDs := append([]model.ExecutionGrantID(nil), execution.releaseIDs...)
			execution.mu.Unlock()
			observation.mu.Lock()
			observationCloses := observation.closeCalls
			observation.mu.Unlock()
			native.mu.Lock()
			terminalCloses := native.closeCalls
			native.mu.Unlock()
			if releases != test.wantRelease || observationCloses != test.wantObservation || terminalCloses != test.wantTerminal {
				t.Fatalf("cleanup release=%d observation=%d terminal=%d; want %d/%d/%d", releases,
					observationCloses, terminalCloses, test.wantRelease, test.wantObservation, test.wantTerminal)
			}
			if releases == 1 && releaseIDs[0] != execution.placement.GrantID {
				t.Fatalf("released grant = %s; want %s", releaseIDs[0], execution.placement.GrantID)
			}
		})
	}
}

func TestExamAttemptTerminalPreReturnReleaseDetachesRequestCancellation(t *testing.T) {
	t.Parallel()
	presentation, command := validTerminalOpenFixture()
	execution := &terminalExecutionPortFake{
		placement:   &appexecution.Placement{GrantID: model.NewExecutionGrantID(), AttemptID: presentation.AttemptID, Ready: true},
		watchErr:    context.Canceled,
		releaseErrs: []error{errors.New("transient release failure"), nil},
	}
	service := &examAttemptTerminalService{attempts: &terminalAttemptPortFake{presentation: presentation},
		execution: execution, audit: &terminalAuditPortFake{}}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := service.Open(ctx, NewInvocation(examAttemptPrincipal(), model.RequestMetadata{}), command); err == nil {
		t.Fatal("Open succeeded")
	}
	execution.mu.Lock()
	releaseCalls := execution.releaseCalls
	releaseCtxErrs := append([]error(nil), execution.releaseCtxErrs...)
	execution.mu.Unlock()
	if releaseCalls != 2 || len(releaseCtxErrs) != 2 || releaseCtxErrs[0] != nil || releaseCtxErrs[1] != nil {
		t.Fatalf("release calls/context errors = %d/%v; want detached cleanup retry through success", releaseCalls, releaseCtxErrs)
	}
}

func TestCandidateExamTerminalCloseAndFailureAreConcurrentSafe(t *testing.T) {
	t.Parallel()
	observationError, terminalError := errors.New("observation close"), errors.New("terminal close")
	observation := &terminalTrackedObservation{closeErr: observationError}
	native := newTerminalTrackedPTY()
	native.closeErr = terminalError
	ctx, cancel := context.WithCancel(context.Background())
	terminal := &candidateExamTerminal{terminal: native, cancel: cancel, observation: observation}
	firstFailure := NewError("exam.attempt.terminal_unavailable")
	if !terminal.beginFailure(firstFailure) {
		t.Fatal("failed to install asynchronous failure fence")
	}

	var wait sync.WaitGroup
	for index := 0; index < 32; index++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			_ = terminal.Close()
		}()
	}
	terminal.completeFailure()
	wait.Wait()
	<-ctx.Done()
	closeErr := terminal.Close()
	if !errors.Is(closeErr, observationError) || !errors.Is(closeErr, terminalError) {
		t.Fatalf("joined close error = %v", closeErr)
	}
	observation.mu.Lock()
	observationCloses := observation.closeCalls
	observation.mu.Unlock()
	native.mu.Lock()
	terminalCloses := native.closeCalls
	native.mu.Unlock()
	if observationCloses != 1 || terminalCloses != 1 {
		t.Fatalf("close counts observation=%d terminal=%d", observationCloses, terminalCloses)
	}
	if _, err := terminal.Read(make([]byte, 1)); !errors.Is(err, firstFailure) {
		t.Fatalf("Read error = %v; want first asynchronous failure", err)
	}

}

func TestCandidateExamTerminalRetainsFirstAsynchronousFailure(t *testing.T) {
	t.Parallel()
	native := newTerminalTrackedPTY()
	_, cancel := context.WithCancel(context.Background())
	terminal := &candidateExamTerminal{terminal: native, cancel: cancel}
	first, second := errors.New("first observation failure"), errors.New("later observation failure")

	terminal.fail(first)
	terminal.fail(second)
	if _, err := terminal.Read(make([]byte, 1)); !errors.Is(err, first) || errors.Is(err, second) {
		t.Fatalf("Read error = %v; want only first failure", err)
	}
}

func TestWorkspaceMutationOriginControlsExecutionEchoButNotRealtime(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name        string
		origin      examattempt.WorkspaceMutationOrigin
		wantChanges int
	}{
		{name: "candidate", origin: examattempt.WorkspaceMutationOriginCandidate, wantChanges: 1},
		{name: "execution host", origin: examattempt.WorkspaceMutationOriginExecutionHost},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			realtime := newTestRealtimeService(t, noopAuthenticationCache{})
			sink := &recordingRealtimeSink{}
			if err := realtime.SetSink(sink); err != nil {
				t.Fatal(err)
			}
			if err := realtime.SetClusterFanout(&recordingRealtimeCluster{}); err != nil {
				t.Fatal(err)
			}
			execution := &executionUseCasesStub{}
			at := model.NowUTC()
			result := examattempt.WorkspaceMutationResult{
				Origin: test.origin, SittingID: model.NewExamSittingID(), AttemptID: model.NewExamAttemptID(),
				CandidateUserID: model.NewUserID(), Change: model.AttemptWorkspaceJournalEntry{
					WorkspaceID: model.NewExamAttemptWorkspaceID(), Cursor: 1, EntryID: model.NewAttemptWorkspaceEntryID(),
					EntryKind: model.StarterWorkspaceEntryDirectory, Operation: model.AttemptWorkspaceMutationCreateDirectory,
					NewPath: "src", ChangedAt: at,
				},
			}
			if err := (examAttemptRealtimeEffects{realtime: realtime, execution: execution}).WorkspaceChanged(context.Background(), result); err != nil {
				t.Fatal(err)
			}
			sink.mu.Lock()
			events := len(sink.events)
			sink.mu.Unlock()
			execution.mu.Lock()
			changes := len(execution.changes)
			execution.mu.Unlock()
			if events != 1 || changes != test.wantChanges {
				t.Fatalf("origin %q events/changes = %d/%d; want 1/%d", test.origin, events, changes, test.wantChanges)
			}
		})
	}
}

type terminalWorkspaceAttemptFake struct {
	mu            sync.Mutex
	pages         []examattempt.WorkspacePage
	listErr       error
	queries       []examattempt.WorkspaceQuery
	mutationErr   error
	directories   []examattempt.CreateWorkspaceDirectoryCommand
	files         []examattempt.CreateWorkspaceFileCommand
	fileBodies    [][]byte
	replacements  []examattempt.ReplaceWorkspaceFileCommand
	replaceBodies [][]byte
	moves         []examattempt.MoveWorkspaceEntryCommand
	deletes       []examattempt.DeleteWorkspaceEntryCommand
}

func (*terminalWorkspaceAttemptFake) GetPresentation(context.Context, examattempt.Call, examattempt.CandidateAccess) (examattempt.Presentation, error) {
	return examattempt.Presentation{}, nil
}

func (fake *terminalWorkspaceAttemptFake) ListWorkspace(_ context.Context, _ examattempt.Call, query examattempt.WorkspaceQuery) (examattempt.WorkspacePage, error) {
	fake.mu.Lock()
	defer fake.mu.Unlock()
	index := len(fake.queries)
	fake.queries = append(fake.queries, query)
	if fake.listErr != nil {
		return examattempt.WorkspacePage{}, fake.listErr
	}
	if index >= len(fake.pages) {
		return examattempt.WorkspacePage{}, nil
	}
	return fake.pages[index], nil
}

func (fake *terminalWorkspaceAttemptFake) CreateWorkspaceDirectory(_ context.Context, _ examattempt.Call, command examattempt.CreateWorkspaceDirectoryCommand) (examattempt.WorkspaceMutationResult, error) {
	fake.mu.Lock()
	defer fake.mu.Unlock()
	fake.directories = append(fake.directories, command)
	return examattempt.WorkspaceMutationResult{}, fake.mutationErr
}

func (fake *terminalWorkspaceAttemptFake) CreateWorkspaceFile(_ context.Context, _ examattempt.Call, command examattempt.CreateWorkspaceFileCommand) (examattempt.WorkspaceMutationResult, error) {
	body, err := io.ReadAll(command.Body)
	if err != nil {
		return examattempt.WorkspaceMutationResult{}, err
	}
	fake.mu.Lock()
	defer fake.mu.Unlock()
	fake.files = append(fake.files, command)
	fake.fileBodies = append(fake.fileBodies, body)
	return examattempt.WorkspaceMutationResult{}, fake.mutationErr
}

func (fake *terminalWorkspaceAttemptFake) ReplaceWorkspaceFile(_ context.Context, _ examattempt.Call, command examattempt.ReplaceWorkspaceFileCommand) (examattempt.WorkspaceMutationResult, error) {
	body, err := io.ReadAll(command.Body)
	if err != nil {
		return examattempt.WorkspaceMutationResult{}, err
	}
	fake.mu.Lock()
	defer fake.mu.Unlock()
	fake.replacements = append(fake.replacements, command)
	fake.replaceBodies = append(fake.replaceBodies, body)
	return examattempt.WorkspaceMutationResult{}, fake.mutationErr
}

func (fake *terminalWorkspaceAttemptFake) MoveWorkspaceEntry(_ context.Context, _ examattempt.Call, command examattempt.MoveWorkspaceEntryCommand) (examattempt.WorkspaceMutationResult, error) {
	fake.mu.Lock()
	defer fake.mu.Unlock()
	fake.moves = append(fake.moves, command)
	return examattempt.WorkspaceMutationResult{}, fake.mutationErr
}

func (fake *terminalWorkspaceAttemptFake) DeleteWorkspaceEntry(_ context.Context, _ examattempt.Call, command examattempt.DeleteWorkspaceEntryCommand) (examattempt.WorkspaceMutationResult, error) {
	fake.mu.Lock()
	defer fake.mu.Unlock()
	fake.deletes = append(fake.deletes, command)
	return examattempt.WorkspaceMutationResult{}, fake.mutationErr
}

type terminalWorkspaceExecutionFake struct {
	watch        func(context.Context, model.ExamAttemptID, appexecution.Cursor) (appexecution.Observation, error)
	openFile     func(context.Context, model.ExamAttemptID, string) (io.ReadCloser, error)
	releaseGrant func(context.Context, model.ExecutionGrantID) error
}

func (*terminalWorkspaceExecutionFake) Ensure(context.Context, appexecution.Request) (*appexecution.Placement, error) {
	return nil, appexecution.ErrUnavailable
}
func (fake *terminalWorkspaceExecutionFake) Watch(ctx context.Context, id model.ExamAttemptID, cursor appexecution.Cursor) (appexecution.Observation, error) {
	if fake.watch == nil {
		return nil, appexecution.ErrUnavailable
	}
	return fake.watch(ctx, id, cursor)
}
func (*terminalWorkspaceExecutionFake) Attach(context.Context, model.ExamAttemptID, appexecution.Window) (appexecution.Terminal, error) {
	return nil, appexecution.ErrUnavailable
}
func (fake *terminalWorkspaceExecutionFake) OpenFile(ctx context.Context, id model.ExamAttemptID, path string) (io.ReadCloser, error) {
	if fake.openFile == nil {
		return nil, appexecution.ErrNotFound
	}
	return fake.openFile(ctx, id, path)
}
func (fake *terminalWorkspaceExecutionFake) ReleaseGrant(ctx context.Context, id model.ExecutionGrantID) error {
	if fake.releaseGrant == nil {
		return nil
	}
	return fake.releaseGrant(ctx, id)
}

type terminalObservationStep struct {
	event appexecution.Event
	err   error
}

type terminalScriptedObservation struct {
	mu          sync.Mutex
	steps       []terminalObservationStep
	started     chan struct{}
	startOnce   sync.Once
	exhausted   chan struct{}
	exhaustOnce sync.Once
	closeCalls  int
	onClose     func()
}

func newTerminalScriptedObservation(steps ...terminalObservationStep) *terminalScriptedObservation {
	return &terminalScriptedObservation{steps: steps, started: make(chan struct{}), exhausted: make(chan struct{})}
}

func (*terminalScriptedObservation) Cursor() appexecution.Cursor { return "cursor" }

func (observation *terminalScriptedObservation) Next(ctx context.Context) (appexecution.Event, error) {
	observation.startOnce.Do(func() { close(observation.started) })
	observation.mu.Lock()
	if len(observation.steps) != 0 {
		step := observation.steps[0]
		observation.steps = observation.steps[1:]
		observation.mu.Unlock()
		return step.event, step.err
	}
	observation.mu.Unlock()
	observation.exhaustOnce.Do(func() { close(observation.exhausted) })
	<-ctx.Done()
	return appexecution.Event{}, ctx.Err()
}

func (observation *terminalScriptedObservation) Close() error {
	observation.mu.Lock()
	observation.closeCalls++
	onClose := observation.onClose
	observation.mu.Unlock()
	if onClose != nil {
		onClose()
	}
	return nil
}

type terminalTrackedBody struct {
	reader io.Reader
	mu     sync.Mutex
	closed int
}

func (body *terminalTrackedBody) Read(buffer []byte) (int, error) { return body.reader.Read(buffer) }
func (body *terminalTrackedBody) Close() error {
	body.mu.Lock()
	body.closed++
	body.mu.Unlock()
	return nil
}

func TestExamAttemptTerminalObservationFailuresCloseAndReleaseExactGrant(t *testing.T) {
	t.Parallel()
	command := OpenCandidateExamTerminalCommand{Access: CandidateExamAttemptAccess{AttemptID: model.NewExamAttemptID()}}
	invocation := NewInvocation(examAttemptPrincipal(), model.RequestMetadata{})
	for _, test := range []struct {
		name  string
		steps []terminalObservationStep
	}{
		{name: "observation lost", steps: []terminalObservationStep{{err: appexecution.ErrObservationLost}}},
		{name: "event failure", steps: []terminalObservationStep{{event: appexecution.Event{Operation: appexecution.Operation(255), Cursor: "bad"}}}},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			grantID := model.NewExecutionGrantID()
			observation := newTerminalScriptedObservation(test.steps...)
			var releaseMu sync.Mutex
			var released []model.ExecutionGrantID
			execution := &terminalWorkspaceExecutionFake{releaseGrant: func(ctx context.Context, id model.ExecutionGrantID) error {
				if ctx.Err() != nil {
					t.Errorf("exact release inherited cancelled context: %v", ctx.Err())
				}
				releaseMu.Lock()
				released = append(released, id)
				releaseMu.Unlock()
				return nil
			}}
			service := &examAttemptTerminalService{attempts: &terminalWorkspaceAttemptFake{}, execution: execution}
			ctx, cancel := context.WithCancel(context.Background())
			native := newTerminalTrackedPTY()
			wrapped := &candidateExamTerminal{terminal: native, cancel: cancel, observation: observation}
			done := make(chan struct{})
			go func() {
				service.synchronizeWorkspace(ctx, invocation, command, grantID, observation, wrapped)
				close(done)
			}()
			select {
			case <-native.closed:
			case <-time.After(time.Second):
				t.Fatal("asynchronous failure did not close terminal")
			}
			<-done
			wrapped.mu.Lock()
			failure := wrapped.failure
			wrapped.mu.Unlock()
			releaseMu.Lock()
			gotReleased := append([]model.ExecutionGrantID(nil), released...)
			releaseMu.Unlock()
			if failure == nil || len(gotReleased) != 1 || gotReleased[0] != grantID {
				t.Fatalf("failure/released = %v/%v; want failure and exact grant %s", failure, gotReleased, grantID)
			}
		})
	}
}

func TestExamAttemptTerminalFailureKeepsTransportClosedToReopenUntilGrantReleaseCompletes(t *testing.T) {
	t.Parallel()
	grantID := model.NewExecutionGrantID()
	observation := newTerminalScriptedObservation(terminalObservationStep{err: appexecution.ErrObservationLost})
	releaseStarted, allowRelease := make(chan struct{}), make(chan struct{})
	var releaseMu sync.Mutex
	releaseCalls := 0
	execution := &terminalWorkspaceExecutionFake{releaseGrant: func(ctx context.Context, id model.ExecutionGrantID) error {
		if id != grantID {
			t.Errorf("released grant = %s; want %s", id, grantID)
		}
		releaseMu.Lock()
		releaseCalls++
		call := releaseCalls
		releaseMu.Unlock()
		if call == 1 {
			return errors.New("transient release failure")
		}
		close(releaseStarted)
		select {
		case <-allowRelease:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}}
	service := &examAttemptTerminalService{attempts: &terminalWorkspaceAttemptFake{}, execution: execution}
	ctx, cancel := context.WithCancel(context.Background())
	native := newTerminalTrackedPTY()
	wrapped := &candidateExamTerminal{terminal: native, cancel: cancel, observation: observation}
	readReturned := make(chan struct{})
	go func() {
		_, _ = wrapped.Read(make([]byte, 1))
		close(readReturned)
	}()
	done := make(chan struct{})
	go func() {
		service.synchronizeWorkspace(ctx, NewInvocation(examAttemptPrincipal(), model.RequestMetadata{}),
			OpenCandidateExamTerminalCommand{Access: CandidateExamAttemptAccess{AttemptID: model.NewExamAttemptID()}},
			grantID, observation, wrapped)
		close(done)
	}()

	select {
	case <-releaseStarted:
	case <-time.After(time.Second):
		t.Fatal("exact release retry did not start")
	}
	select {
	case <-native.closed:
	case <-time.After(time.Second):
		t.Fatal("native PTY remained open while exact release retried")
	}
	closeReturned := make(chan struct{})
	go func() {
		_ = wrapped.Close()
		close(closeReturned)
	}()
	select {
	case <-closeReturned:
		t.Fatal("Close returned before exact grant release completed")
	case <-readReturned:
		t.Fatal("terminal reader exposed reopen before exact grant release completed")
	case <-time.After(50 * time.Millisecond):
	}
	if _, err := wrapped.Write([]byte("stale input")); err == nil {
		t.Fatal("write reached a placement whose release was in progress")
	}

	close(allowRelease)
	select {
	case <-readReturned:
	case <-time.After(time.Second):
		t.Fatal("terminal reader did not return after exact grant release")
	}
	select {
	case <-closeReturned:
	case <-time.After(time.Second):
		t.Fatal("Close remained blocked after exact grant release")
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("workspace synchronizer did not stop")
	}
	releaseMu.Lock()
	gotReleaseCalls := releaseCalls
	releaseMu.Unlock()
	if gotReleaseCalls != 2 {
		t.Fatalf("release calls = %d; want one failed attempt and one successful retry", gotReleaseCalls)
	}
}

func TestExamAttemptTerminalWorkspaceEventApplication(t *testing.T) {
	t.Parallel()
	access := CandidateExamAttemptAccess{AttemptID: model.NewExamAttemptID()}
	command := OpenCandidateExamTerminalCommand{Access: access, ParticipationID: model.NewAttemptParticipationID(), Generation: 3}
	invocation := NewInvocation(examAttemptPrincipal(), model.RequestMetadata{})
	fileID, directoryID := model.NewAttemptWorkspaceEntryID(), model.NewAttemptWorkspaceEntryID()
	version := model.WorkspaceContentVersion("2")
	file := CandidateExamWorkspaceItem{EntryID: fileID, Kind: model.StarterWorkspaceEntryFile, Path: "old.txt", ContentVersion: version}
	directory := CandidateExamWorkspaceItem{EntryID: directoryID, Kind: model.StarterWorkspaceEntryDirectory, Path: "old-dir"}
	key := executionEventIdempotency(appexecution.Event{Cursor: "event-cursor"})
	bodyBytes := []byte("host bytes")

	t.Run("isolated watcher directory create fails before mutation", func(t *testing.T) {
		attempts := &terminalWorkspaceAttemptFake{pages: []examattempt.WorkspacePage{{Cursor: 1}}}
		service := &examAttemptTerminalService{attempts: attempts, execution: &terminalWorkspaceExecutionFake{}}
		event := appexecution.Event{Cursor: "event-cursor", Operation: appexecution.OperationCreate, Path: "dir"}
		if err := service.applyExecutionEvent(context.Background(), invocation, command, event); err == nil {
			t.Fatal("unfenced directory create succeeded")
		}
		if len(attempts.directories) != 0 {
			t.Fatalf("partial directory command = %#v", attempts.directories)
		}
	})

	t.Run("create and replace files", func(t *testing.T) {
		for _, replace := range []bool{false, true} {
			name := "create"
			items, path, operation := []CandidateExamWorkspaceItem(nil), "new.txt", appexecution.OperationCreate
			if replace {
				name, items, path, operation = "replace", []CandidateExamWorkspaceItem{file}, file.Path, appexecution.OperationReplace
			}
			t.Run(name, func(t *testing.T) {
				body := &terminalTrackedBody{reader: bytes.NewReader(bodyBytes)}
				attempts := &terminalWorkspaceAttemptFake{pages: []examattempt.WorkspacePage{{Cursor: 1, Items: items}}}
				service := &examAttemptTerminalService{attempts: attempts, execution: &terminalWorkspaceExecutionFake{openFile: func(context.Context, model.ExamAttemptID, string) (io.ReadCloser, error) {
					return body, nil
				}}}
				event := appexecution.Event{Cursor: "event-cursor", Operation: operation, Path: path}
				if err := service.applyExecutionEvent(context.Background(), invocation, command, event); err != nil {
					t.Fatal(err)
				}
				digest := sha256.Sum256(bodyBytes)
				if replace {
					if len(attempts.replacements) != 1 || attempts.replacements[0].EntryID != fileID ||
						attempts.replacements[0].ExpectedContentVersion != version || attempts.replacements[0].ExpectedSHA256 != hex.EncodeToString(digest[:]) ||
						attempts.replacements[0].MediaType != "application/octet-stream" || !bytes.Equal(attempts.replaceBodies[0], bodyBytes) {
						t.Fatalf("replace command = %#v", attempts.replacements)
					}
				} else if len(attempts.files) != 1 || attempts.files[0].Path != path || attempts.files[0].ExpectedSHA256 != hex.EncodeToString(digest[:]) ||
					attempts.files[0].Size != int64(len(bodyBytes)) || attempts.files[0].IdempotencyKey != key || !bytes.Equal(attempts.fileBodies[0], bodyBytes) {
					t.Fatalf("create command = %#v", attempts.files)
				}
				body.mu.Lock()
				closed := body.closed
				body.mu.Unlock()
				if closed != 1 {
					t.Fatalf("body closed %d times", closed)
				}
			})
		}
	})

	t.Run("move file", func(t *testing.T) {
		attempts := &terminalWorkspaceAttemptFake{pages: []examattempt.WorkspacePage{{Cursor: 1, Items: []CandidateExamWorkspaceItem{file}}}}
		service := &examAttemptTerminalService{attempts: attempts, execution: &terminalWorkspaceExecutionFake{}}
		event := appexecution.Event{Cursor: "event-cursor", Operation: appexecution.OperationMove, From: file.Path, Path: "moved.txt"}
		if err := service.applyExecutionEvent(context.Background(), invocation, command, event); err != nil {
			t.Fatal(err)
		}
		if len(attempts.moves) != 1 || attempts.moves[0].EntryID != fileID || attempts.moves[0].DestinationPath != "moved.txt" || attempts.moves[0].IdempotencyKey != key {
			t.Fatalf("move command = %#v", attempts.moves)
		}
	})

	t.Run("directory move and v0.2 directory-to-file conflict", func(t *testing.T) {
		for _, repair := range []bool{false, true} {
			name := "directory"
			if repair {
				name = "repair"
			}
			t.Run(name, func(t *testing.T) {
				attempts := &terminalWorkspaceAttemptFake{pages: []examattempt.WorkspacePage{{Cursor: 1, Items: []CandidateExamWorkspaceItem{directory}}}}
				execution := &terminalWorkspaceExecutionFake{}
				var opened *terminalTrackedBody
				if repair {
					execution.openFile = func(context.Context, model.ExamAttemptID, string) (io.ReadCloser, error) {
						opened = &terminalTrackedBody{reader: bytes.NewReader(bodyBytes)}
						return opened, nil
					}
				}
				service := &examAttemptTerminalService{attempts: attempts, execution: execution}
				event := appexecution.Event{Cursor: "event-cursor", Operation: appexecution.OperationMove, From: directory.Path, Path: "new-path"}
				if repair {
					if err := service.applyExecutionEvent(context.Background(), invocation, command, event); err == nil {
						t.Fatal("non-atomic directory-to-file replacement succeeded")
					}
					if len(attempts.deletes) != 0 || len(attempts.files) != 0 || len(attempts.moves) != 0 {
						t.Fatalf("partial directory-to-file mutations delete/files/moves = %#v/%#v/%#v",
							attempts.deletes, attempts.files, attempts.moves)
					}
					opened.mu.Lock()
					closed := opened.closed
					opened.mu.Unlock()
					if closed != 1 {
						t.Fatalf("directory-to-file body closed %d times", closed)
					}
				} else {
					if err := service.applyExecutionEvent(context.Background(), invocation, command, event); err != nil {
						t.Fatal(err)
					}
					if len(attempts.moves) != 1 || attempts.moves[0].EntryID != directoryID {
						t.Fatalf("directory move = %#v", attempts.moves)
					}
				}
			})
		}
	})

	t.Run("delete", func(t *testing.T) {
		attempts := &terminalWorkspaceAttemptFake{pages: []examattempt.WorkspacePage{{Cursor: 1, Items: []CandidateExamWorkspaceItem{file}}}}
		service := &examAttemptTerminalService{attempts: attempts, execution: &terminalWorkspaceExecutionFake{}}
		event := appexecution.Event{Cursor: "event-cursor", Operation: appexecution.OperationDelete, Path: file.Path}
		if err := service.applyExecutionEvent(context.Background(), invocation, command, event); err != nil {
			t.Fatal(err)
		}
		if len(attempts.deletes) != 1 || attempts.deletes[0].ExpectedContentVersion != version || attempts.deletes[0].IdempotencyKey != key {
			t.Fatalf("delete command = %#v", attempts.deletes)
		}
	})

	t.Run("isolated watcher directory rename shapes fail before every possible first mutation", func(t *testing.T) {
		parent := CandidateExamWorkspaceItem{EntryID: model.NewAttemptWorkspaceEntryID(), Kind: model.StarterWorkspaceEntryDirectory, Path: "src"}
		child := CandidateExamWorkspaceItem{EntryID: model.NewAttemptWorkspaceEntryID(), Kind: model.StarterWorkspaceEntryFile,
			Path: "src/main.go", ContentVersion: model.WorkspaceContentVersion("3")}
		for _, test := range []struct {
			name     string
			items    []CandidateExamWorkspaceItem
			event    appexecution.Event
			openFile func(context.Context, model.ExamAttemptID, string) (io.ReadCloser, error)
		}{
			{name: "authoritative root delete arrives first", items: []CandidateExamWorkspaceItem{parent, child},
				event: appexecution.Event{Operation: appexecution.OperationDelete, Path: parent.Path}},
			{name: "authoritative descendant delete arrives first", items: []CandidateExamWorkspaceItem{parent, child},
				event: appexecution.Event{Operation: appexecution.OperationDelete, Path: child.Path}},
			{name: "allowed destination root create arrives first",
				event: appexecution.Event{Operation: appexecution.OperationCreate, Path: "restored"}},
			{name: "allowed destination descendant create arrives first",
				event: appexecution.Event{Operation: appexecution.OperationCreate, Path: "restored/main.go"},
				openFile: func(context.Context, model.ExamAttemptID, string) (io.ReadCloser, error) {
					return &terminalTrackedBody{reader: bytes.NewReader(bodyBytes)}, nil
				}},
		} {
			t.Run(test.name, func(t *testing.T) {
				attempts := &terminalWorkspaceAttemptFake{pages: []examattempt.WorkspacePage{{Cursor: 1, Items: test.items}}}
				service := &examAttemptTerminalService{attempts: attempts, execution: &terminalWorkspaceExecutionFake{openFile: test.openFile}}
				if err := service.applyExecutionEvent(context.Background(), invocation, command, test.event); err == nil {
					t.Fatal("unfenced directory topology succeeded")
				}
				if len(attempts.directories) != 0 || len(attempts.files) != 0 || len(attempts.replacements) != 0 ||
					len(attempts.moves) != 0 || len(attempts.deletes) != 0 {
					t.Fatalf("partial mutations directories=%#v files=%#v replacements=%#v moves=%#v deletes=%#v",
						attempts.directories, attempts.files, attempts.replacements, attempts.moves, attempts.deletes)
				}
			})
		}
	})

	t.Run("move from authoritative path into ignored tree becomes delete", func(t *testing.T) {
		attempts := &terminalWorkspaceAttemptFake{pages: []examattempt.WorkspacePage{{Cursor: 1, Items: []CandidateExamWorkspaceItem{file}}}}
		service := &examAttemptTerminalService{attempts: attempts, execution: &terminalWorkspaceExecutionFake{}}
		event := appexecution.Event{Cursor: "event-cursor", Operation: appexecution.OperationMove,
			From: file.Path, Path: "target/cache.txt"}
		if err := service.applyExecutionEvent(context.Background(), invocation, command, event); err != nil {
			t.Fatal(err)
		}
		if len(attempts.deletes) != 1 || len(attempts.moves) != 0 || attempts.deletes[0].EntryID != file.EntryID ||
			attempts.deletes[0].ExpectedContentVersion != file.ContentVersion || attempts.deletes[0].IdempotencyKey != key {
			t.Fatalf("cross-boundary delete/move = %#v/%#v", attempts.deletes, attempts.moves)
		}
	})

	t.Run("directory move from authoritative path into ignored tree fails closed", func(t *testing.T) {
		attempts := &terminalWorkspaceAttemptFake{pages: []examattempt.WorkspacePage{{Cursor: 1, Items: []CandidateExamWorkspaceItem{directory}}}}
		service := &examAttemptTerminalService{attempts: attempts, execution: &terminalWorkspaceExecutionFake{}}
		event := appexecution.Event{Cursor: "event-cursor", Operation: appexecution.OperationMove,
			From: directory.Path, Path: "target/cache"}
		if err := service.applyExecutionEvent(context.Background(), invocation, command, event); err == nil {
			t.Fatal("cross-boundary directory move succeeded")
		}
		if len(attempts.deletes) != 0 || len(attempts.moves) != 0 {
			t.Fatalf("partial directory mutation = deletes %#v moves %#v", attempts.deletes, attempts.moves)
		}
	})

	for _, test := range []struct {
		name      string
		openFile  func(context.Context, model.ExamAttemptID, string) (io.ReadCloser, error)
		wantFile  bool
		wantBytes []byte
	}{
		{name: "file", wantFile: true, wantBytes: bodyBytes,
			openFile: func(context.Context, model.ExamAttemptID, string) (io.ReadCloser, error) {
				return &terminalTrackedBody{reader: bytes.NewReader(bodyBytes)}, nil
			}},
		{name: "directory", openFile: func(context.Context, model.ExamAttemptID, string) (io.ReadCloser, error) {
			return nil, appexecution.ErrNotFound
		}},
	} {
		t.Run("move from ignored tree into authoritative "+test.name, func(t *testing.T) {
			attempts := &terminalWorkspaceAttemptFake{pages: []examattempt.WorkspacePage{{Cursor: 1}}}
			service := &examAttemptTerminalService{attempts: attempts, execution: &terminalWorkspaceExecutionFake{openFile: test.openFile}}
			event := appexecution.Event{Cursor: "event-cursor", Operation: appexecution.OperationMove,
				From: "target/cache", Path: "restored"}
			if test.wantFile {
				if err := service.applyExecutionEvent(context.Background(), invocation, command, event); err != nil {
					t.Fatal(err)
				}
				if len(attempts.files) != 1 || len(attempts.directories) != 0 || attempts.files[0].Path != event.Path ||
					attempts.files[0].IdempotencyKey != key || !bytes.Equal(attempts.fileBodies[0], test.wantBytes) {
					t.Fatalf("cross-boundary file/directory = %#v/%#v", attempts.files, attempts.directories)
				}
			} else {
				if err := service.applyExecutionEvent(context.Background(), invocation, command, event); err == nil {
					t.Fatal("cross-boundary directory move succeeded")
				}
				if len(attempts.directories) != 0 || len(attempts.files) != 0 {
					t.Fatalf("partial cross-boundary directory/file = %#v/%#v", attempts.directories, attempts.files)
				}
			}
		})
	}

	for _, test := range []struct {
		name  string
		items []CandidateExamWorkspaceItem
		event appexecution.Event
	}{
		{name: "create conflict", items: []CandidateExamWorkspaceItem{file}, event: appexecution.Event{Operation: appexecution.OperationCreate, Path: file.Path}},
		{name: "replace missing", event: appexecution.Event{Operation: appexecution.OperationReplace, Path: "missing"}},
		{name: "replace directory", items: []CandidateExamWorkspaceItem{directory}, event: appexecution.Event{Operation: appexecution.OperationReplace, Path: directory.Path}},
		{name: "move missing", event: appexecution.Event{Operation: appexecution.OperationMove, From: "missing", Path: "next"}},
		{name: "delete missing", event: appexecution.Event{Operation: appexecution.OperationDelete, Path: "missing"}},
		{name: "unsupported", event: appexecution.Event{Operation: appexecution.Operation(255)}},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			attempts := &terminalWorkspaceAttemptFake{pages: []examattempt.WorkspacePage{{Cursor: 1, Items: test.items}}}
			service := &examAttemptTerminalService{attempts: attempts, execution: &terminalWorkspaceExecutionFake{}}
			if err := service.applyExecutionEvent(context.Background(), invocation, command, test.event); err == nil {
				t.Fatal("conflicting event succeeded")
			}
		})
	}

	t.Run("child failure is concealed", func(t *testing.T) {
		attempts := &terminalWorkspaceAttemptFake{pages: []examattempt.WorkspacePage{{Cursor: 1}}, mutationErr: &examattempt.Fault{Code: "exam.attempt.not_found"}}
		service := &examAttemptTerminalService{attempts: attempts, execution: &terminalWorkspaceExecutionFake{openFile: func(context.Context,
			model.ExamAttemptID, string,
		) (io.ReadCloser, error) {
			return &terminalTrackedBody{reader: bytes.NewReader(bodyBytes)}, nil
		}}}
		event := appexecution.Event{Cursor: "event-cursor", Operation: appexecution.OperationCreate, Path: "file.txt"}
		err := service.applyExecutionEvent(context.Background(), invocation, command, event)
		appErr, ok := As(err)
		if !ok || appErr.Code() != "resource.not_found" {
			t.Fatalf("concealed child error = %v", err)
		}
	})
}

func TestExamAttemptTerminalIgnoresReservedSegmentsFromEitherEventPath(t *testing.T) {
	t.Parallel()
	for path, want := range map[string]bool{
		"": false, ".proctor/state": true, "src/.git/index": true, "web/node_modules/pkg": true,
		"target/debug/app": true, "pkg/__pycache__/x": true, "src/targeted.go": false, "src/main.go": false,
	} {
		if got := ignoredExecutionPath(path); got != want {
			t.Fatalf("ignoredExecutionPath(%q) = %t, want %t", path, got, want)
		}
	}
	observation := newTerminalScriptedObservation(
		terminalObservationStep{event: appexecution.Event{Operation: appexecution.OperationCreate, Path: "src/.git/index"}},
		terminalObservationStep{event: appexecution.Event{Operation: appexecution.OperationMove, Path: "target/safe", From: "target/cache"}},
	)
	attempts := &terminalWorkspaceAttemptFake{}
	service := &examAttemptTerminalService{attempts: attempts, execution: &terminalWorkspaceExecutionFake{}}
	command := OpenCandidateExamTerminalCommand{Access: CandidateExamAttemptAccess{AttemptID: model.NewExamAttemptID()}}
	ctx, cancel := context.WithCancel(context.Background())
	native := newTerminalTrackedPTY()
	wrapped := &candidateExamTerminal{terminal: native, cancel: cancel, observation: observation}
	done := make(chan struct{})
	go func() {
		service.synchronizeWorkspace(ctx, NewInvocation(examAttemptPrincipal(), model.RequestMetadata{}), command,
			model.NewExecutionGrantID(), observation, wrapped)
		close(done)
	}()
	select {
	case <-observation.exhausted:
	case <-time.After(time.Second):
		t.Fatal("ignored events were not consumed")
	}
	if err := wrapped.Close(); err != nil {
		t.Fatal(err)
	}
	<-done
	attempts.mu.Lock()
	queries := len(attempts.queries)
	attempts.mu.Unlock()
	if queries != 0 {
		t.Fatalf("ignored events read authoritative workspace %d time(s)", queries)
	}
}

func TestExamAttemptTerminalManifestAndFileBounds(t *testing.T) {
	t.Parallel()
	invocation := NewInvocation(examAttemptPrincipal(), model.RequestMetadata{})
	access := CandidateExamAttemptAccess{AttemptID: model.NewExamAttemptID()}
	first, second := CandidateExamWorkspaceItem{EntryID: model.NewAttemptWorkspaceEntryID(), Path: "a"}, CandidateExamWorkspaceItem{EntryID: model.NewAttemptWorkspaceEntryID(), Path: "b"}

	t.Run("pins first cursor across pages", func(t *testing.T) {
		attempts := &terminalWorkspaceAttemptFake{pages: []examattempt.WorkspacePage{
			{Cursor: 7, Items: []CandidateExamWorkspaceItem{first}, HasMore: true},
			{Cursor: 7, Items: []CandidateExamWorkspaceItem{second}},
		}}
		service := &examAttemptTerminalService{attempts: attempts}
		items, err := service.workspaceManifest(context.Background(), invocation, access)
		if err != nil || len(items) != 2 {
			t.Fatalf("manifest = %#v, %v", items, err)
		}
		if len(attempts.queries) != 2 || attempts.queries[0].ExpectedCursor != -1 || attempts.queries[1].ExpectedCursor != 7 || attempts.queries[1].AfterEntryID != first.EntryID {
			t.Fatalf("manifest queries = %#v", attempts.queries)
		}
	})

	for _, test := range []struct {
		name  string
		pages []examattempt.WorkspacePage
	}{
		{name: "refresh required", pages: []examattempt.WorkspacePage{{Cursor: 1, RefreshRequired: true}}},
		{name: "no progress", pages: []examattempt.WorkspacePage{{Cursor: 1, HasMore: true}}},
		{name: "cursor drift", pages: []examattempt.WorkspacePage{{Cursor: 1, Items: []CandidateExamWorkspaceItem{first}, HasMore: true}, {Cursor: 2, Items: []CandidateExamWorkspaceItem{second}}}},
		{name: "entry limit", pages: []examattempt.WorkspacePage{{Cursor: 1,
			Items: make([]CandidateExamWorkspaceItem, model.AttemptWorkspaceMaximumEntries+1)}}},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			service := &examAttemptTerminalService{attempts: &terminalWorkspaceAttemptFake{pages: test.pages}}
			if _, err := service.workspaceManifest(context.Background(), invocation, access); err == nil {
				t.Fatal("invalid manifest succeeded")
			}
		})
	}

	t.Run("bounded reader", func(t *testing.T) {
		if data, err := readBoundedExecutionFile(bytes.NewBufferString("1234"), 4); err != nil || string(data) != "1234" {
			t.Fatalf("boundary data = %q, %v", data, err)
		}
		if _, err := readBoundedExecutionFile(bytes.NewBufferString("12345"), 4); err == nil {
			t.Fatal("maximum+1 file succeeded")
		}
		readFailure := errors.New("read failure")
		if _, err := readBoundedExecutionFile(terminalErrorReader{err: readFailure}, 4); !errors.Is(err, readFailure) {
			t.Fatalf("read error = %v", err)
		}
		body := &terminalTrackedBody{reader: terminalErrorReader{err: readFailure}}
		service := &examAttemptTerminalService{attempts: &terminalWorkspaceAttemptFake{}}
		err := service.persistExecutionFile(context.Background(), examattempt.NewCall(examAttemptPrincipal(), model.RequestMetadata{}),
			ExamAttemptWorkspaceMutationAccess{}, "file", CandidateExamWorkspaceItem{}, body, "key", false)
		body.mu.Lock()
		closed := body.closed
		body.mu.Unlock()
		if !errors.Is(err, readFailure) || closed != 1 {
			t.Fatalf("persist read failure = %v, closes=%d", err, closed)
		}
	})

	t.Run("deterministic event key", func(t *testing.T) {
		event := appexecution.Event{Cursor: "same-cursor"}
		firstKey, secondKey := executionEventIdempotency(event), executionEventIdempotency(event)
		digest := sha256.Sum256([]byte(event.Cursor))
		want := "execution-" + hex.EncodeToString(digest[:])
		if firstKey != secondKey || firstKey != want {
			t.Fatalf("event keys = %q/%q; want %q", firstKey, secondKey, want)
		}
	})
}

type terminalErrorReader struct{ err error }

func (reader terminalErrorReader) Read([]byte) (int, error) { return 0, reader.err }
