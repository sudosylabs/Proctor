// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"reflect"
	"strings"
	"testing"
	"time"

	examattempt "github.com/sudosylabs/proctor/server/app/exam/attempt"
	appexecution "github.com/sudosylabs/proctor/server/app/execution"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

type semanticTerminalAttemptFake struct {
	*terminalAttemptPortFake
	calls     *[]string
	target    store.ExecutionObservationTarget
	committed bool
	command   examattempt.ReplaceWorkspaceFileCommand
	failure   error
	body      string
	outcome   *store.ExamAttemptWorkspaceMutationResult
}

func (f *semanticTerminalAttemptFake) ResolveExecutionObservation(context.Context, examattempt.Call, examattempt.WorkspaceMutationAccess) (*store.ExecutionObservationTarget, error) {
	*f.calls = append(*f.calls, "resolve")
	if f.failure != nil {
		return nil, f.failure
	}
	result := f.target
	if f.committed {
		result.Outcome = f.outcome
	}
	return &result, nil
}
func (f *semanticTerminalAttemptFake) ReplaceWorkspaceFile(_ context.Context, _ examattempt.Call, command examattempt.ReplaceWorkspaceFileCommand) (examattempt.WorkspaceMutationResult, error) {
	*f.calls = append(*f.calls, "commit")
	f.command = command
	body, err := io.ReadAll(command.Body)
	if err != nil {
		return examattempt.WorkspaceMutationResult{}, err
	}
	f.body = string(body)
	f.committed = true
	return examattempt.WorkspaceMutationResult{Change: f.outcome.Change}, nil
}

type semanticTerminalLease struct{ calls *[]string }

func (l semanticTerminalLease) Validate(context.Context) error {
	*l.calls = append(*l.calls, "validate")
	return nil
}
func (l semanticTerminalLease) Release(context.Context) error {
	*l.calls = append(*l.calls, "release")
	return nil
}

type semanticTerminalExecutionFake struct {
	*terminalExecutionPortFake
	calls *[]string
}

func (f semanticTerminalExecutionFake) AcquireObservationLease(context.Context, model.ExamAttemptID, model.ExecutionGrantID) (store.ExecutionLifecycleLease, error) {
	*f.calls = append(*f.calls, "lease")
	return semanticTerminalLease{f.calls}, nil
}

type semanticTerminalObservationFake struct {
	appexecution.Observation
	calls    *[]string
	failure  error
	mutation store.ExecutionProjectionMutation
}

func (f *semanticTerminalObservationFake) OpenContent(context.Context, store.ExecutionObservation) (io.ReadCloser, error) {
	*f.calls = append(*f.calls, "captured-content")
	return io.NopCloser(strings.NewReader("captured")), nil
}
func (f *semanticTerminalObservationFake) Confirm(_ context.Context, _ store.ExecutionObservation, m store.ExecutionProjectionMutation) error {
	*f.calls = append(*f.calls, "confirm")
	f.mutation = m
	return f.failure
}
func (f *semanticTerminalObservationFake) Acknowledge(context.Context, int64) error {
	*f.calls = append(*f.calls, "ack")
	return nil
}

func TestSemanticTerminalPersistsOriginalVersionBeforeHostAcknowledgement(t *testing.T) {
	t.Parallel()
	for _, scenario := range []string{"new write", "already committed", "confirmation lost", "competing baseline"} {
		t.Run(scenario, func(t *testing.T) {
			var calls []string
			_, command := validTerminalOpenFixture()
			id := model.NewExecutionGrantID()
			before, after := model.NewWorkspaceContentVersion(), model.NewWorkspaceContentVersion()
			entry := model.NewAttemptWorkspaceEntryID()
			event := store.ExecutionObservation{Fence: model.ExecutionFence{GrantID: id, EnvironmentEpoch: "epoch", ControlRevision: 1}, HostSequence: 1, Operation: model.AttemptWorkspaceMutationReplaceFile, NodeIdentity: "node", Kind: model.StarterWorkspaceEntryFile, Path: "file.txt", ExpectedContentVersion: before, Content: &store.ExecutionProjectionContent{TransferID: "captured", Size: 8, SHA256: strings.Repeat("a", 64)}}
			change := model.AttemptWorkspaceJournalEntry{WorkspaceID: model.NewExamAttemptWorkspaceID(), Cursor: 9, EntryID: entry, EntryKind: model.StarterWorkspaceEntryFile, Operation: model.AttemptWorkspaceMutationReplaceFile, OldPath: event.Path, NewPath: event.Path, ContentVersion: after, ChangedAt: time.Now().UTC()}
			attempts := &semanticTerminalAttemptFake{terminalAttemptPortFake: &terminalAttemptPortFake{}, calls: &calls, target: store.ExecutionObservationTarget{EntryID: entry, ExpectedContentVersion: before}, outcome: &store.ExamAttemptWorkspaceMutationResult{Change: change}}
			observation := &semanticTerminalObservationFake{calls: &calls}
			execution := semanticTerminalExecutionFake{terminalExecutionPortFake: &terminalExecutionPortFake{}, calls: &calls}
			service := &examAttemptTerminalService{attempts: attempts, execution: execution}
			want := []string{"lease", "resolve", "captured-content", "commit", "resolve", "validate", "confirm", "validate", "ack", "release"}
			switch scenario {
			case "already committed":
				attempts.committed = true
				want = []string{"lease", "resolve", "validate", "confirm", "validate", "ack", "release"}
			case "confirmation lost":
				observation.failure = context.DeadlineExceeded
				want = []string{"lease", "resolve", "captured-content", "commit", "resolve", "validate", "confirm", "release"}
			case "competing baseline":
				attempts.failure = errors.New("competing baseline")
				want = []string{"lease", "resolve", "release"}
			}
			err := service.applySemanticExecutionEvent(context.Background(), NewInvocation(examAttemptPrincipal(), model.RequestMetadata{}), command, id, event, observation)
			failed := scenario == "confirmation lost" || scenario == "competing baseline"
			if (err != nil) != failed {
				t.Fatalf("result: %v", err)
			}
			if !reflect.DeepEqual(calls, want) {
				t.Fatalf("effects: %v, want %v", calls, want)
			}
			if scenario == "new write" || scenario == "confirmation lost" {
				if attempts.body != "captured" || attempts.command.ExpectedContentVersion != before || attempts.command.Access.SourceObservation == nil || attempts.command.Access.SourceObservation.ExpectedContentVersion != before {
					t.Fatal("host mutation substituted live content or current manifest version")
				}
			}
			if scenario != "competing baseline" && (observation.mutation.Change.ContentVersion != after || observation.mutation.ExpectedContentVersion != before) {
				t.Fatal("confirmation lost exact resulting/precondition versions")
			}
		})
	}
}

func (f *semanticTerminalAttemptFake) RecordIgnoredExecutionObservation(context.Context, examattempt.Call, examattempt.WorkspaceMutationAccess) (*store.ExecutionObservationTarget, error) {
	return &store.ExecutionObservationTarget{Ignored: true, Processed: true}, nil
}

type startupSemanticObservation struct {
	*semanticTerminalObservationFake
	fence     model.ExecutionFence
	remaining int
	sequence  int64
	closed    chan struct{}
}

func (o *startupSemanticObservation) NextPending(context.Context) (appexecution.Event, bool, error) {
	if o.remaining == 0 {
		return appexecution.Event{}, false, nil
	}
	event := store.ExecutionObservation{Fence: o.fence, HostSequence: o.sequence + 1, Operation: model.AttemptWorkspaceMutationCreateDirectory, NodeIdentity: "ignored", Kind: model.StarterWorkspaceEntryDirectory, Path: "node_modules"}
	return appexecution.Event{Semantic: &event}, true, nil
}
func (o *startupSemanticObservation) Acknowledge(context.Context, int64) error {
	*o.calls = append(*o.calls, "ack")
	o.remaining--
	o.sequence++
	return nil
}
func (o *startupSemanticObservation) Next(ctx context.Context) (appexecution.Event, error) {
	<-ctx.Done()
	return appexecution.Event{}, ctx.Err()
}
func (o *startupSemanticObservation) Close() error { return nil }

type startupTerminalExecution struct{ semanticTerminalExecutionFake }

func (e *startupTerminalExecution) Ensure(ctx context.Context, request appexecution.Request) (*appexecution.Placement, error) {
	result, err := e.terminalExecutionPortFake.Ensure(ctx, request)
	if err != nil {
		return result, err
	}
	value := *result
	if e.ensureCalls == 1 {
		value.Projection.State = store.ExecutionProjectionSynchronizing
		return &value, appexecution.ErrProjectionPending
	}
	return &value, nil
}

func TestTerminalStartupDrainsBeforeAttachAndBoundsCatchUp(t *testing.T) {
	for _, count := range []int{1, 129} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			presentation, command := validTerminalOpenFixture()
			id := model.NewExecutionGrantID()
			base := &terminalExecutionPortFake{placement: &appexecution.Placement{GrantID: id, AttemptID: presentation.AttemptID, Ready: true, Projection: appexecution.ProjectionStatus{EnvironmentEpoch: "test_epoch", State: store.ExecutionProjectionReady}}, terminal: newTerminalTrackedPTY()}
			observation := &startupSemanticObservation{semanticTerminalObservationFake: &semanticTerminalObservationFake{calls: &base.order}, fence: model.ExecutionFence{GrantID: id, EnvironmentEpoch: "test_epoch", ControlRevision: 1}, remaining: count}
			base.observation = observation
			execution := &startupTerminalExecution{semanticTerminalExecutionFake{terminalExecutionPortFake: base, calls: &base.order}}
			attempts := &semanticTerminalAttemptFake{terminalAttemptPortFake: &terminalAttemptPortFake{presentation: presentation}, calls: &base.order, target: store.ExecutionObservationTarget{Ignored: true, Processed: true}}
			service, err := newExamAttemptTerminalService(attempts, execution, &terminalAuditPortFake{order: &base.order})
			if err != nil {
				t.Fatal(err)
			}
			terminal, err := service.Open(context.Background(), NewInvocation(examAttemptPrincipal(), model.RequestMetadata{}), command)
			if count == 1 {
				if err != nil || terminal == nil || base.attachCalls != 1 || base.ensureCalls != 2 || observation.sequence != 1 {
					t.Fatalf("startup did not recover before attach: %v %v", err, base.order)
				}
				_ = terminal.Close()
			} else {
				failure, ok := As(err)
				if terminal != nil || !ok || failure.Code() != "execution.projection_pending" || base.attachCalls != 0 || observation.sequence != 128 || base.releaseCalls != 0 {
					t.Fatalf("unbounded/destructive startup: %v %v", err, base.order)
				}
			}
		})
	}
}
