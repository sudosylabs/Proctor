// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package executionhost

import (
	"bytes"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/sudosylabs/execenv"
	"github.com/sudosylabs/execenv/memory"
	appexecution "github.com/sudosylabs/proctor/server/app/execution"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

func TestJournalAdapterImmutableReplayAndSameEnvironment(t *testing.T) {
	fixture := newRemoteDirectoryFixture(t)
	catalog, catalogErr := fixture.directory.Catalog(t.Context())
	if catalogErr != nil || len(catalog) != 1 || catalog[0].Isolated {
		t.Fatal("memory protocol fixture was misrepresented as an isolated host")
	}
	id := model.NewExecutionGrantID()
	spec := appexecution.Spec{ID: id.String(), Image: "toolchain", Network: appexecution.NetworkNone}
	environment, err := fixture.directory.Ensure(t.Context(), "runner-a", spec)
	if err != nil {
		t.Fatal(err)
	}
	journal, ok := environment.(appexecution.ObservationEnvironment)
	if !ok {
		t.Fatal("actual semantic host capability was not exposed")
	}
	fence := model.ExecutionFence{GrantID: id, EnvironmentEpoch: journal.Epoch(), ControlRevision: 1}
	control, err := journal.Control(t.Context(), fence, model.ExecutionControlRunning)
	if err != nil || control.Fence != fence || !control.Confirmed {
		t.Fatalf("control: %#v %v", control, err)
	}
	base, err := journal.UploadProjectionContent(t.Context(), fence, bytes.NewBufferString("base"))
	if err != nil {
		t.Fatal(err)
	}
	entryID, workspaceID, version := model.NewAttemptWorkspaceEntryID(), model.NewExamAttemptWorkspaceID(), model.NewWorkspaceContentVersion()
	initial := store.ExecutionProjectionRequest{Fence: fence, MutationID: model.NewId(), Initial: true, ThroughWorkspaceCursor: 5, Entries: []store.ExecutionProjectionEntry{{EntryID: entryID, Kind: model.StarterWorkspaceEntryFile, Path: "file", ContentVersion: version, Content: &base}}}
	receipt, err := journal.ApplyProjection(t.Context(), initial)
	if err != nil || receipt.AppliedWorkspaceCursor != 5 {
		t.Fatalf("initialize: %#v %v", receipt, err)
	}
	pty, err := environment.Attach(t.Context(), appexecution.Window{Cols: 80, Rows: 24})
	if err != nil {
		t.Fatal(err)
	}
	defer pty.Close()
	host := fixture.host.Host.(*memory.Host)
	for _, body := range []string{"first", "second"} {
		if err = host.WriteGuest(t.Context(), execenv.ID(id.String()), "file", []byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	competing := store.ExecutionProjectionRequest{Fence: fence, MutationID: model.NewId(), FromWorkspaceCursor: 5, ThroughWorkspaceCursor: 6, Mutations: []store.ExecutionProjectionMutation{{Change: model.AttemptWorkspaceJournalEntry{WorkspaceID: workspaceID, Cursor: 6, EntryID: model.NewAttemptWorkspaceEntryID(), EntryKind: model.StarterWorkspaceEntryDirectory, Operation: model.AttemptWorkspaceMutationCreateDirectory, NewPath: "other", ChangedAt: time.Now().UTC()}}}}
	if _, err = journal.ApplyProjection(t.Context(), competing); !errors.Is(err, appexecution.ErrHostCursorConflict) {
		t.Fatalf("unseen guest observation must block projection: %v", err)
	}
	// Simulate a committed first outcome whose confirmation/ACK reply was lost.
	stream, err := journal.Observe(t.Context(), fence, 1)
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	observation := stream.(appexecution.PendingSemanticObservation)
	var mutations []store.ExecutionProjectionMutation
	expected := version
	for index, want := range []string{"first", "second"} {
		next, available, err := observation.NextPending(t.Context())
		if err != nil || !available || next.Semantic == nil {
			t.Fatalf("next: %#v %v", next, err)
		}
		event := *next.Semantic
		if event.HostSequence != int64(index+1) || event.ExpectedContentVersion != version || event.BasedOnWorkspaceCursor != 5 {
			t.Fatalf("capture changed: %#v", event)
		}
		body, err := observation.OpenContent(t.Context(), event)
		if err != nil {
			t.Fatal(err)
		}
		content, err := io.ReadAll(body)
		closeErr := body.Close()
		if err != nil || closeErr != nil || string(content) != want {
			t.Fatalf("immutable body: %q %v %v", content, err, closeErr)
		}
		resulting := model.NewWorkspaceContentVersion()
		mutation := store.ExecutionProjectionMutation{ExpectedContentVersion: expected, Content: event.Content, Change: model.AttemptWorkspaceJournalEntry{WorkspaceID: workspaceID, Cursor: int64(index + 6), EntryID: entryID, EntryKind: model.StarterWorkspaceEntryFile, Operation: model.AttemptWorkspaceMutationReplaceFile, OldPath: "file", NewPath: "file", ContentVersion: resulting, ChangedAt: time.Now().UTC()}}
		if err = observation.Confirm(t.Context(), event, mutation); err != nil {
			t.Fatal(err)
		}
		if err = observation.Acknowledge(t.Context(), event.HostSequence); err != nil {
			t.Fatal(err)
		}
		upload, err := journal.UploadProjectionContent(t.Context(), fence, bytes.NewReader(content))
		if err != nil {
			t.Fatal(err)
		}
		mutation.Content = &upload
		mutations = append(mutations, mutation)
		expected = resulting
	}
	request := store.ExecutionProjectionRequest{Fence: fence, MutationID: model.NewId(), FromWorkspaceCursor: 5, ThroughWorkspaceCursor: 7, ExpectedHostCursor: 2, Mutations: mutations}
	receipt, err = journal.ApplyProjection(t.Context(), request)
	if err != nil || receipt.AppliedWorkspaceCursor != 7 || receipt.HostCursor != 2 {
		t.Fatalf("apply accepted outcomes: %#v %v", receipt, err)
	}
	replay, err := journal.ApplyProjection(t.Context(), request)
	if err != nil || replay != receipt {
		t.Fatalf("exact projection retry: %#v %v", replay, err)
	}
	if _, available, err := observation.NextPending(t.Context()); err != nil || available {
		t.Fatalf("projection emitted a guest echo: %t %v", available, err)
	}
	same, err := fixture.directory.Existing(t.Context(), "runner-a", spec)
	if err != nil || same != environment || fixture.host.ensures.Load() != 1 {
		t.Fatal("healthy projection replaced the environment")
	}
	reader, err := same.Open(t.Context(), "file")
	if err != nil {
		t.Fatal(err)
	}
	live, err := io.ReadAll(reader)
	_ = reader.Close()
	if err != nil || string(live) != "second" {
		t.Fatalf("projection overwrote newer guest content: %q %v", live, err)
	}
	fence.ControlRevision++
	if _, err = journal.Control(t.Context(), fence, model.ExecutionControlFrozen); err != nil {
		t.Fatal(err)
	}
	if _, _, err = observation.NextPending(t.Context()); !errors.Is(err, appexecution.ErrProjectionPending) {
		t.Fatalf("frozen stream must wait: %v", err)
	}
	fence.ControlRevision++
	if _, err = journal.Control(t.Context(), fence, model.ExecutionControlRunning); err != nil {
		t.Fatal(err)
	}
	if _, available, err := observation.NextPending(t.Context()); err != nil || available {
		t.Fatalf("resumed stream did not follow current fence: %t %v", available, err)
	}
	if err = pty.Resize(t.Context(), appexecution.Window{Cols: 90, Rows: 30}); err != nil {
		t.Fatalf("projection or freeze destroyed the PTY: %v", err)
	}
}
