// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package app

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	examworkspace "github.com/sudosylabs/proctor/server/app/exam/workspace"
	"github.com/sudosylabs/proctor/server/model"
)

func TestExamStarterWorkspaceFacadeForwardsRawCommandsAndStreams(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	invocation := NewInvocation(model.Principal{UserID: model.NewUserID(), CredentialScopes: []string{"exam:manage"}},
		model.RequestMetadata{RequestID: "starter-workspace", IPAddress: "192.0.2.1", UserAgent: "test"})
	body := strings.NewReader("starter content")
	directory := CreateExamStarterWorkspaceDirectoryCommand{ExamID: model.NewExamID(), ExpectedDraftRevision: 3,
		Path: " src/ ", IdempotencyKey: " raw-directory-key "}
	create := CreateExamStarterWorkspaceFileCommand{ExamID: directory.ExamID, ExpectedDraftRevision: 4,
		Path: " src/main.go ", MediaType: "text/plain", ExpectedSHA256: strings.Repeat("b", 64),
		Body: body, Size: int64(body.Len()), IdempotencyKey: " raw-file-key "}
	move := MoveExamStarterWorkspaceEntryCommand{ExamID: directory.ExamID, EntryID: model.NewStarterWorkspaceEntryID(),
		ExpectedDraftRevision: 5, Path: " lib/main.go ", IdempotencyKey: " raw-move-key "}
	replace := ReplaceExamStarterWorkspaceFileCommand{ExamID: directory.ExamID, EntryID: move.EntryID,
		ExpectedDraftRevision: 6, ExpectedContentVersion: model.NewWorkspaceContentVersion(), MediaType: create.MediaType,
		ExpectedSHA256: create.ExpectedSHA256, Body: body, Size: create.Size, IdempotencyKey: " raw-replace-key "}
	remove := RemoveExamStarterWorkspaceEntryCommand{ExamID: directory.ExamID, EntryID: move.EntryID,
		ExpectedDraftRevision: 7, IdempotencyKey: " raw-remove-key "}
	result := examworkspace.Result{Entry: model.StarterWorkspaceEntry{ID: move.EntryID}, Object: &model.StarterWorkspaceObject{},
		DraftRevision: 8, Replayed: true}
	for _, test := range []struct {
		name    string
		command any
		invoke  func(*App) (ExamStarterWorkspaceResult, error)
	}{
		{"create directory", examworkspace.CreateDirectoryCommand(directory), func(app *App) (ExamStarterWorkspaceResult, error) {
			return app.CreateExamStarterWorkspaceDirectory(ctx, invocation, directory)
		}},
		{"create file", examworkspace.CreateFileCommand(create), func(app *App) (ExamStarterWorkspaceResult, error) {
			return app.CreateExamStarterWorkspaceFile(ctx, invocation, create)
		}},
		{"move entry", examworkspace.MoveEntryCommand(move), func(app *App) (ExamStarterWorkspaceResult, error) {
			return app.MoveExamStarterWorkspaceEntry(ctx, invocation, move)
		}},
		{"replace file", examworkspace.ReplaceFileCommand(replace), func(app *App) (ExamStarterWorkspaceResult, error) {
			return app.ReplaceExamStarterWorkspaceFile(ctx, invocation, replace)
		}},
		{"remove entry", examworkspace.RemoveEntryCommand(remove), func(app *App) (ExamStarterWorkspaceResult, error) {
			return app.RemoveExamStarterWorkspaceEntry(ctx, invocation, remove)
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			fake := &examStarterWorkspaceFacadeFake{result: result}
			got, err := test.invoke(&App{examStarterWorkspace: fake})
			if err != nil || !reflect.DeepEqual(got, result) {
				t.Fatalf("result = %#v, %v; want %#v", got, err, result)
			}
			if fake.command != test.command || body.Len() != int(create.Size) {
				t.Fatalf("command changed before reaching child: %#v, unread bytes = %d", fake.command, body.Len())
			}
			if fake.ctx != ctx || !reflect.DeepEqual(fake.call.Principal(), invocation.Principal()) || fake.call.RequestMetadata() != invocation.RequestMetadata() {
				t.Fatalf("child call lost context, principal, or request metadata: %#v", fake.call)
			}
		})
	}
}

func TestExamStarterWorkspaceFacadePreservesErrorConcealmentAndSafeFields(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name   string
		cause  error
		code   string
		fields map[string]string
	}{
		{"denied", NewError("authorization.denied"), "resource.not_found", nil},
		{"missing", &examworkspace.Fault{Code: "exam.starter_workspace.not_found"}, "resource.not_found", nil},
		{"safe conflict", &examworkspace.Fault{Code: "exam.draft.conflict", SafeFields: map[string]any{"current_revision": 8}}, "exam.draft.conflict", map[string]string{"current_revision": "8"}},
		{"unknown", errors.New("private adapter detail"), "exam.starter_workspace.unavailable", nil},
	} {
		t.Run(test.name, func(t *testing.T) {
			fake := &examStarterWorkspaceFacadeFake{result: examworkspace.Result{DraftRevision: 8}, err: test.cause}
			got, err := (&App{examStarterWorkspace: fake}).RemoveExamStarterWorkspaceEntry(context.Background(), Invocation{}, RemoveExamStarterWorkspaceEntryCommand{})
			mapped, ok := As(err)
			if !ok || mapped.Code() != test.code || !reflect.DeepEqual(mapped.Fields(), test.fields) || !errors.Is(err, test.cause) || !reflect.DeepEqual(got, ExamStarterWorkspaceResult{}) {
				t.Fatalf("result = %#v, error = %#v; want %s, fields %v", got, mapped, test.code, test.fields)
			}
		})
	}
}

type examStarterWorkspaceFacadeFake struct {
	examStarterWorkspaceUseCases
	ctx     context.Context
	call    examworkspace.Call
	command any
	result  examworkspace.Result
	err     error
}

func (fake *examStarterWorkspaceFacadeFake) CreateDirectory(ctx context.Context, call examworkspace.Call, command examworkspace.CreateDirectoryCommand) (examworkspace.Result, error) {
	fake.ctx, fake.call, fake.command = ctx, call, command
	return fake.result, fake.err
}

func (fake *examStarterWorkspaceFacadeFake) CreateFile(ctx context.Context, call examworkspace.Call, command examworkspace.CreateFileCommand) (examworkspace.Result, error) {
	fake.ctx, fake.call, fake.command = ctx, call, command
	return fake.result, fake.err
}

func (fake *examStarterWorkspaceFacadeFake) MoveEntry(ctx context.Context, call examworkspace.Call, command examworkspace.MoveEntryCommand) (examworkspace.Result, error) {
	fake.ctx, fake.call, fake.command = ctx, call, command
	return fake.result, fake.err
}

func (fake *examStarterWorkspaceFacadeFake) ReplaceFile(ctx context.Context, call examworkspace.Call, command examworkspace.ReplaceFileCommand) (examworkspace.Result, error) {
	fake.ctx, fake.call, fake.command = ctx, call, command
	return fake.result, fake.err
}

func (fake *examStarterWorkspaceFacadeFake) RemoveEntry(ctx context.Context, call examworkspace.Call, command examworkspace.RemoveEntryCommand) (examworkspace.Result, error) {
	fake.ctx, fake.call, fake.command = ctx, call, command
	return fake.result, fake.err
}

func TestExamStarterWorkspaceRealtimeEffectContainsOnlySafeChangeMetadata(t *testing.T) {
	t.Parallel()
	sink := &recordingRealtimeSink{}
	cluster := &recordingRealtimeCluster{}
	realtime := newTestRealtimeService(t, noopAuthenticationCache{})
	if err := realtime.SetSink(sink); err != nil {
		t.Fatal(err)
	}
	if err := realtime.SetClusterFanout(cluster); err != nil {
		t.Fatal(err)
	}
	examID, entryID := model.NewExamID(), model.NewStarterWorkspaceEntryID()
	changedAt := time.Date(2026, 8, 15, 8, 30, 0, 123456789, time.UTC)
	if err := (examStarterWorkspaceRealtimeEffects{realtime: realtime}).Changed(context.Background(), examID, entryID, 4, examworkspace.ChangeFileReplaced, changedAt); err != nil {
		t.Fatal(err)
	}
	if len(sink.events) != 1 || sink.events[0].Name != "exam_starter_workspace_changed" ||
		sink.events[0].Resource != (model.Resource{Type: model.ResourceExam, ID: examID.String()}) {
		t.Fatalf("events=%#v", sink.events)
	}
	var data map[string]any
	if err := json.Unmarshal(sink.events[0].Data, &data); err != nil {
		t.Fatal(err)
	}
	if len(data) != 5 || data["exam_id"] != examID.String() || data["entry_id"] != entryID.String() ||
		data["operation"] != "file_replaced" || data["draft_revision"] != float64(4) || data["changed_at"] != "2026-08-15T08:30:00.123456Z" {
		t.Fatalf("event data=%#v", data)
	}
	for _, forbidden := range []string{"path", "content", "sha256", "object_id"} {
		if _, exists := data[forbidden]; exists {
			t.Fatalf("event exposed %s: %#v", forbidden, data)
		}
	}
}
