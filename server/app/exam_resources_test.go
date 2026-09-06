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
	"reflect"
	"strings"
	"testing"

	examresource "github.com/sudosylabs/proctor/server/app/exam/resource"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

func TestExamResourceFacadeForwardsUploadsWithoutConsumingOrNormalizingInput(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	invocation := NewInvocation(model.Principal{UserID: model.NewUserID(), CredentialScopes: []string{"exam:manage"}},
		model.RequestMetadata{RequestID: "resource-upload", IPAddress: "192.0.2.1", UserAgent: "test"})
	body := strings.NewReader("resource content")
	create := CreateExamResourceCommand{
		ExamID: model.NewExamID(), ExpectedDraftRevision: 7, DisplayName: "  Notes  ", DescriptionMarkdown: " **Read** ",
		MediaType: model.ExamResourceMediaText, Body: body, Size: int64(body.Len()),
		ExpectedSHA256: strings.Repeat("a", 64), IdempotencyKey: "  raw-key  ",
	}
	replace := ReplaceExamResourceContentCommand{
		ExamID: create.ExamID, ResourceID: model.NewExamResourceID(), ExpectedDraftRevision: 8,
		MediaType: create.MediaType, Body: body, Size: create.Size, ExpectedSHA256: create.ExpectedSHA256, IdempotencyKey: create.IdempotencyKey,
	}
	result := store.ExamResourceRecord{Resource: &model.ExamResource{}, Rendition: &model.FileRendition{}, DraftRevision: 9}
	for _, test := range []struct {
		name    string
		command any
		invoke  func(*App) (ExamResourceRecord, error)
	}{
		{"create", create, func(app *App) (ExamResourceRecord, error) {
			return app.CreateExamResource(ctx, invocation, create)
		}},
		{"replace", replace, func(app *App) (ExamResourceRecord, error) {
			return app.ReplaceExamResourceContent(ctx, invocation, replace)
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			fake := &examResourceFacadeFake{result: result}
			got, err := test.invoke(&App{examResources: fake})
			if err != nil || got != result {
				t.Fatalf("result = %#v, %v; want %#v", got, err, result)
			}
			if fake.command != test.command || body.Len() != int(create.Size) {
				t.Fatalf("upload changed before reaching child: %#v, unread bytes = %d", fake.command, body.Len())
			}
			if fake.ctx != ctx || !reflect.DeepEqual(fake.call.Principal(), invocation.Principal()) || fake.call.RequestMetadata() != invocation.RequestMetadata() {
				t.Fatalf("child call lost context, principal, or request metadata: %#v", fake.call)
			}
		})
	}
}

func TestExamResourceFacadePreservesMetadataPatchPresence(t *testing.T) {
	t.Parallel()
	empty := ""
	command := EditExamResourceMetadataCommand{ExamID: model.NewExamID(), ResourceID: model.NewExamResourceID(),
		ExpectedDraftRevision: 4, DescriptionMarkdown: &empty, IdempotencyKey: " raw-key "}
	fake := &examResourceFacadeFake{}
	_, err := (&App{examResources: fake}).EditExamResourceMetadata(context.Background(), Invocation{}, command)
	if err != nil {
		t.Fatal(err)
	}
	got, ok := fake.command.(examresource.EditMetadataCommand)
	if !ok || got != command || got.DisplayName != nil || got.DescriptionMarkdown != &empty {
		t.Fatalf("patch presence or value changed: %#v", fake.command)
	}
}

func TestExamResourceFacadeReorderOwnsResourceIDs(t *testing.T) {
	t.Parallel()
	first, second := model.NewExamResourceID(), model.NewExamResourceID()
	for _, test := range []struct {
		name string
		ids  []model.ExamResourceID
	}{
		{"ordered IDs", []model.ExamResourceID{second, first}},
		{"nil IDs", nil},
		{"empty IDs", []model.ExamResourceID{}},
	} {
		t.Run(test.name, func(t *testing.T) {
			command := ReorderExamResourcesCommand{ExamID: model.NewExamID(), ExpectedDraftRevision: 5,
				ResourceIDs: test.ids, IdempotencyKey: " raw-order-key "}
			result := []store.ExamResourceRecord{{DraftRevision: 6}}
			fake := &examResourceFacadeFake{reordered: result}
			got, err := (&App{examResources: fake}).ReorderExamResources(context.Background(), Invocation{}, command)
			if err != nil || !reflect.DeepEqual(got, result) {
				t.Fatalf("result = %#v, %v", got, err)
			}
			captured := fake.command.(examresource.ReorderCommand)
			want := command
			want.ResourceIDs = append([]model.ExamResourceID(nil), command.ResourceIDs...)
			if !reflect.DeepEqual(captured, want) {
				t.Fatalf("reorder = %#v, want %#v", captured, want)
			}
			if len(captured.ResourceIDs) > 0 {
				captured.ResourceIDs[0] = model.NewExamResourceID()
				if command.ResourceIDs[0] != second {
					t.Fatal("child command shares the caller's ResourceIDs backing array")
				}
			}
		})
	}
}

func TestExamResourceFacadePreservesErrorConcealmentAndSafeFields(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name   string
		cause  error
		code   string
		fields map[string]string
	}{
		{"denied", NewError("authorization.denied"), "resource.not_found", nil},
		{"missing", &examresource.Fault{Code: "exam.resource.not_found"}, "resource.not_found", nil},
		{"safe conflict", &examresource.Fault{Code: "exam.draft.conflict", SafeFields: map[string]any{"current_revision": 8}}, "exam.draft.conflict", map[string]string{"current_revision": "8"}},
		{"unknown", errors.New("private adapter detail"), "exam.resource.unavailable", nil},
	} {
		t.Run(test.name, func(t *testing.T) {
			fake := &examResourceFacadeFake{result: store.ExamResourceRecord{DraftRevision: 8}, err: test.cause}
			got, err := (&App{examResources: fake}).RemoveExamResource(context.Background(), Invocation{}, RemoveExamResourceCommand{})
			mapped, ok := As(err)
			if !ok || mapped.Code() != test.code || !reflect.DeepEqual(mapped.Fields(), test.fields) || !errors.Is(err, test.cause) || got != (ExamResourceRecord{}) {
				t.Fatalf("result = %#v, error = %#v; want %s, fields %v", got, mapped, test.code, test.fields)
			}
		})
	}
}

type examResourceFacadeFake struct {
	examResourceUseCases
	ctx       context.Context
	call      examresource.Call
	command   any
	result    store.ExamResourceRecord
	reordered []store.ExamResourceRecord
	err       error
}

func (fake *examResourceFacadeFake) Create(ctx context.Context, call examresource.Call, command examresource.CreateCommand) (store.ExamResourceRecord, error) {
	fake.ctx, fake.call, fake.command = ctx, call, command
	return fake.result, fake.err
}

func (fake *examResourceFacadeFake) ReplaceContent(ctx context.Context, call examresource.Call, command examresource.ReplaceContentCommand) (store.ExamResourceRecord, error) {
	fake.ctx, fake.call, fake.command = ctx, call, command
	return fake.result, fake.err
}

func (fake *examResourceFacadeFake) EditMetadata(ctx context.Context, call examresource.Call, command examresource.EditMetadataCommand) (store.ExamResourceRecord, error) {
	fake.ctx, fake.call, fake.command = ctx, call, command
	return fake.result, fake.err
}

func (fake *examResourceFacadeFake) Reorder(ctx context.Context, call examresource.Call, command examresource.ReorderCommand) ([]store.ExamResourceRecord, error) {
	fake.ctx, fake.call, fake.command = ctx, call, command
	return fake.reordered, fake.err
}

func (fake *examResourceFacadeFake) Remove(ctx context.Context, call examresource.Call, command examresource.RemoveCommand) (store.ExamResourceRecord, error) {
	fake.ctx, fake.call, fake.command = ctx, call, command
	return fake.result, fake.err
}
