//go:build integration

// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package sqlstore

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/sudosylabs/execenv"
	"github.com/sudosylabs/execenv/memory"
	"github.com/sudosylabs/execenv/remote"
	appexecution "github.com/sudosylabs/proctor/server/app/execution"
	"github.com/sudosylabs/proctor/server/executionhost"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
	"github.com/sudosylabs/proctor/server/store/storetest"
)

// This fixture tests coordination only. The backend deliberately reports no
// isolation; this test-only directory supplies placement eligibility so the real
// application workflow can run. It cannot certify a production host.
type journalTestDirectory struct{ *executionhost.Directory }

func (d journalTestDirectory) Catalog(ctx context.Context) ([]appexecution.HostStatus, error) {
	values, err := d.Directory.Catalog(ctx)
	for i := range values {
		values[i].Isolated = true
	}
	return values, err
}

type journalTestContent map[model.AttemptWorkspaceObjectID][]byte

func (c journalTestContent) OpenStarterWorkspaceObject(context.Context, model.StarterWorkspaceObjectID) (io.ReadCloser, error) {
	return nil, appexecution.ErrNotFound
}
func (c journalTestContent) OpenAttemptWorkspaceObject(_ context.Context, id model.AttemptWorkspaceObjectID) (io.ReadCloser, error) {
	value, ok := c[id]
	if !ok {
		return nil, appexecution.ErrNotFound
	}
	return io.NopCloser(bytes.NewReader(value)), nil
}

func TestExecutionJournalHostAndPostgres(t *testing.T) {
	persistence := openTestStore(t)
	resetTestStore(t, persistence)
	fixture := storetest.NewExecutionWorkspaceFixture(t, t.Context(), persistence)
	service, host, directory, content := newJournalHostService(t, persistence)
	request := appexecution.Request{AttemptID: fixture.Access.AttemptID, Image: "go", Network: appexecution.NetworkNone}
	placement, err := service.Ensure(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	pty, err := service.Attach(t.Context(), request.AttemptID, placement.GrantID, appexecution.Window{Cols: 80, Rows: 24})
	if err != nil {
		t.Fatal(err)
	}
	defer pty.Close()
	watch, err := service.Watch(t.Context(), request.AttemptID, placement.GrantID, "")
	if err != nil {
		t.Fatal(err)
	}
	defer watch.Close()
	observation := watch.(appexecution.PendingSemanticObservation)
	id := execenv.ID(placement.GrantID.String())
	pauseAndResume := func() {
		t.Helper()
		for _, paused := range []bool{true, false} {
			if err := fixture.SetPaused(paused); err != nil {
				t.Fatal(err)
			}
			if err := service.ReconcileAttempt(t.Context(), request.AttemptID); err != nil && !errors.Is(err, appexecution.ErrProjectionPending) {
				t.Fatal(err)
			}
		}
	}
	// Multiple changes occur before any outcome is accepted. Both original file
	// captures retain the same baseline while later outcomes resolve sequentially.
	if err = host.WriteGuest(t.Context(), id, "file", []byte("first")); err != nil {
		t.Fatal(err)
	}
	if err = host.WriteGuest(t.Context(), id, "file", []byte("second")); err != nil {
		t.Fatal(err)
	}
	// A healthy renewal may commit after capture but before persistence or the
	// execution callback. It must preserve both the captured fence and live PTY.
	if err = fixture.RenewHealthy(); err != nil {
		t.Fatal(err)
	}
	// Captures retain the earlier control revision through a reversible pause.
	pauseAndResume()
	workspace := persistence.ExamAttemptWorkspace()
	var entryID model.AttemptWorkspaceEntryID
	for index := 0; index < 2; index++ {
		next, available, err := observation.NextPending(t.Context())
		if err != nil || !available || next.Semantic == nil {
			t.Fatalf("observation: %#v %v", next, err)
		}
		event := *next.Semantic
		if index == 1 {
			// Pause after selection: the adapter has cached the original event and
			// must refresh authorization without rewriting its immutable identity.
			if err := fixture.SetPaused(true); err != nil {
				t.Fatal(err)
			}
			if err := service.ReconcileAttempt(t.Context(), request.AttemptID); err != nil {
				t.Fatal(err)
			}
			blocked := fixture.Access
			blocked.SourceGrantID, blocked.SourceObservation = placement.GrantID, &event
			_, blockedErr := workspace.ResolveObservation(t.Context(), blocked)
			var conflict *store.ErrConflict
			if !errors.As(blockedErr, &conflict) || conflict.Resource != "execution_observation" || conflict.Constraint != "interaction_blocked" {
				t.Fatalf("paused capture was not recoverably denied: %v", blockedErr)
			}
			if _, available, err := observation.NextPending(t.Context()); !errors.Is(err, appexecution.ErrProjectionPending) || available {
				t.Fatalf("paused cached capture: available=%v err=%v", available, err)
			}
			if err := fixture.SetPaused(false); err != nil {
				t.Fatal(err)
			}
			if err := service.ReconcileAttempt(t.Context(), request.AttemptID); err != nil && !errors.Is(err, appexecution.ErrProjectionPending) {
				t.Fatal(err)
			}
		}
		lease, err := service.AcquireObservationLease(t.Context(), request.AttemptID, placement.GrantID)
		if err != nil {
			t.Fatal(err)
		}
		access := fixture.Access
		access.SourceGrantID = placement.GrantID
		access.SourceObservation = &event
		target, err := workspace.ResolveObservation(t.Context(), access)
		if err != nil {
			t.Fatal(err)
		}
		if err = service.ValidateTerminalInteraction(t.Context(), request.AttemptID, placement.GrantID, placement.Projection.EnvironmentEpoch); err != nil {
			t.Fatalf("healthy renewal blocked the retained terminal: %v", err)
		}
		reader, err := observation.OpenContent(t.Context(), event)
		if err != nil {
			t.Fatal(err)
		}
		data, err := io.ReadAll(reader)
		_ = reader.Close()
		if err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256(data)
		if hex.EncodeToString(sum[:]) != event.Content.SHA256 {
			t.Fatal("captured bytes do not match durable metadata")
		}
		object, err := workspace.ReserveObject(t.Context(), &store.ExamAttemptWorkspaceObjectReservation{Access: access, ObjectID: model.NewAttemptWorkspaceObjectID()})
		if err != nil {
			t.Fatal(err)
		}
		content[object.ID] = bytes.Clone(data)
		object, err = workspace.MarkObjectReady(t.Context(), &store.ExamAttemptWorkspaceObjectReady{Access: access, ObjectID: object.ID, ContentVersion: model.NewWorkspaceContentVersion(), Content: model.AttemptWorkspaceContent{MediaType: "text/plain", SizeBytes: int64(len(data)), SHA256: event.Content.SHA256}})
		if err != nil {
			t.Fatal(err)
		}
		input := store.ExamAttemptWorkspaceMutation{Access: access, Operation: event.Operation, EntryID: target.EntryID, ExpectedPath: event.Path, ExpectedContentVersion: target.ExpectedContentVersion, ObjectID: object.ID, AuditEventID: fixture.NewAudit(), AuditAt: model.GetMillis()}
		if index == 0 {
			input.EntryID = model.NewAttemptWorkspaceEntryID()
			input.ExpectedPath = ""
			input.DestinationPath = event.Path
			entryID = input.EntryID
		}
		result, err := workspace.ApplyMutation(t.Context(), &input, fixture.Command(fmt.Sprintf("captured-%d", index)))
		if err != nil {
			t.Fatal(err)
		}
		if result.Change.EntryID != entryID {
			t.Fatal("stable node identity changed")
		}
		retained, err := workspace.ResolveObservation(t.Context(), access)
		if err != nil || retained.Outcome == nil || retained.Outcome.Change.Cursor != result.Change.Cursor {
			t.Fatalf("durable outcome: %#v %v", retained, err)
		}
		// Simulate losing the ACK after the first database commit: a replacement
		// stream must replay sequence one, not silently skip to sequence two.
		if index == 0 {
			if err = lease.Release(t.Context()); err != nil {
				t.Fatal(err)
			}
			pauseAndResume()
			lease, err = service.AcquireObservationLease(t.Context(), request.AttemptID, placement.GrantID)
			if err != nil {
				t.Fatal(err)
			}
			retained, err = workspace.ResolveObservation(t.Context(), access)
			if err != nil || retained.Outcome == nil || !retained.Outcome.Replayed {
				t.Fatalf("committed capture replay after pause: %#v %v", retained, err)
			}
			_ = observation.Close()
			fresh, err := directory.Existing(t.Context(), "host", appexecution.Spec{ID: string(id), Image: "go", Network: appexecution.NetworkNone})
			if err != nil {
				t.Fatal(err)
			}
			current, err := persistence.ExecutionGrant().Current(t.Context(), request.AttemptID)
			if err != nil {
				t.Fatal(err)
			}
			recovered, err := fresh.(appexecution.ObservationEnvironment).Observe(t.Context(), current.Fence(), 1)
			if err != nil {
				t.Fatal(err)
			}
			observation = recovered.(appexecution.PendingSemanticObservation)
			replay, available, err := observation.NextPending(t.Context())
			if err != nil || !available || replay.Semantic.HostSequence != 1 {
				t.Fatalf("lost ACK replay: %#v %v", replay, err)
			}
		}
		mutation := store.ExecutionProjectionMutation{Change: retained.Outcome.Change, ExpectedContentVersion: retained.ExpectedContentVersion, Content: event.Content}
		if err = observation.Confirm(t.Context(), event, mutation); err != nil {
			t.Fatal(err)
		}
		if err = observation.Acknowledge(t.Context(), event.HostSequence); err != nil {
			t.Fatal(err)
		}
		if err = lease.Release(t.Context()); err != nil {
			t.Fatal(err)
		}
		if err = service.ReconcileAttempt(t.Context(), request.AttemptID); err != nil && !errors.Is(err, appexecution.ErrProjectionPending) {
			t.Fatal(err)
		}
	}
	defer observation.Close()
	current, err := service.Ensure(t.Context(), request)
	if err != nil || current.GrantID != placement.GrantID || current.Projection.EnvironmentEpoch != placement.Projection.EnvironmentEpoch || current.Projection.AppliedWorkspaceCursor <= placement.Projection.AppliedWorkspaceCursor {
		t.Fatalf("final placement: %#v %v", current, err)
	}
	if err = fixture.SetPaused(true); err != nil {
		t.Fatal(err)
	}
	if err = service.ReconcileAttempt(t.Context(), request.AttemptID); err != nil {
		t.Fatal(err)
	}
	frozen, err := persistence.ExecutionGrant().Current(t.Context(), request.AttemptID)
	if err != nil || frozen.ID != placement.GrantID || frozen.State != model.ExecutionGrantReady || frozen.DesiredControlState != model.ExecutionControlFrozen || frozen.ControlAcknowledgedRevision != frozen.ControlRevision {
		t.Fatalf("pause did not durably confirm the same guest frozen: %#v %v", frozen, err)
	}
	if _, err = service.Ensure(t.Context(), request); !errors.Is(err, appexecution.ErrInteractionBlocked) {
		t.Fatalf("paused open must be denied without retirement: %v", err)
	}
	if err = fixture.SetPaused(false); err != nil {
		t.Fatal(err)
	}
	if err = service.ReconcileAttempt(t.Context(), request.AttemptID); err != nil {
		t.Fatal(err)
	}
	resumed, err := service.Ensure(t.Context(), request)
	if err != nil || resumed.GrantID != placement.GrantID || resumed.Projection.EnvironmentEpoch != placement.Projection.EnvironmentEpoch {
		t.Fatalf("resume replaced the guest: %#v %v", resumed, err)
	}
	if err = pty.Resize(t.Context(), appexecution.Window{Cols: 90, Rows: 30}); err != nil {
		t.Fatalf("healthy projection lost PTY: %v", err)
	}
	snapshot, err := persistence.ExecutionGrant().WorkspaceSnapshot(t.Context(), request.AttemptID)
	if err != nil || len(snapshot.Nodes) != 1 || snapshot.Nodes[0].EntryID != entryID || string(content[snapshot.Nodes[0].AttemptObjectID]) != "second" {
		t.Fatalf("authoritative final snapshot: %#v %v", snapshot, err)
	}
}

func newJournalHostService(t *testing.T, persistence store.Store) (*appexecution.Service, *memory.Host, *executionhost.Directory, journalTestContent) {
	t.Helper()
	host, err := memory.New(memory.Config{Images: []execenv.Image{"go"}, Slots: 1})
	if err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() {
		done <- remote.Serve(ctx, listener, host, remote.ServerConfig{Security: remote.SecurityInsecureLocal, Token: []byte("integration-only")})
	}()
	t.Cleanup(func() {
		cancel()
		_ = listener.Close()
		select {
		case err := <-done:
			if err != nil {
				t.Error(err)
			}
		case <-time.After(time.Second):
			t.Error("remote server did not stop")
		}
	})
	directory, err := executionhost.New(executionhost.Settings{Enabled: true, DialTimeout: time.Second, OperationTimeout: 5 * time.Second, Hosts: []executionhost.HostConfig{{ID: "host", Address: listener.Addr().String(), Security: "insecure_local", Token: "integration-only"}}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = directory.Close() })
	content := journalTestContent{}
	service, err := appexecution.New(persistence.ExecutionGrant(), journalTestDirectory{directory}, content, model.NowUTC, model.NewExecutionGrantID)
	if err != nil {
		t.Fatal(err)
	}

	return service, host, directory, content
}

func TestExecutionLargeInitialSnapshotWithHostAndPostgres(t *testing.T) {
	persistence := openTestStore(t)
	resetTestStore(t, persistence)
	fixture := storetest.NewExecutionWorkspaceFixture(t, t.Context(), persistence)
	parent := ""
	for i := 0; i < model.ExamWorkspaceDefaultMaximumEntries; i++ {
		if i%25 == 0 {
			if err := fixture.RenewHealthy(); err != nil {
				t.Fatal(err)
			}
		}
		path := ""
		if i < 3 {
			if parent != "" {
				parent += "/"
			}
			parent += strings.Repeat("&", 255)
			path = parent
		} else {
			path = parent + "/" + strings.Repeat("&", 251) + fmt.Sprintf("%04d", i)
		}
		_, err := persistence.ExamAttemptWorkspace().ApplyMutation(t.Context(), &store.ExamAttemptWorkspaceMutation{Access: fixture.Access, Operation: model.AttemptWorkspaceMutationCreateDirectory, EntryID: model.NewAttemptWorkspaceEntryID(), DestinationPath: path, AuditEventID: fixture.NewAudit(), AuditAt: model.GetMillis()}, fixture.Command(fmt.Sprintf("large-initial-%d", i)))
		if err != nil {
			t.Fatal(err)
		}
	}
	snapshot, err := persistence.ExecutionGrant().WorkspaceSnapshot(t.Context(), fixture.Access.AttemptID)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(snapshot)
	if err != nil || len(raw) <= 1<<20 {
		t.Fatalf("large snapshot setup: bytes=%d err=%v", len(raw), err)
	}
	if err := fixture.RenewHealthy(); err != nil {
		t.Fatal(err)
	}
	service, _, _, _ := newJournalHostService(t, persistence)
	request := appexecution.Request{AttemptID: fixture.Access.AttemptID, Image: "go", Network: appexecution.NetworkNone}
	placement, err := service.Ensure(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	if placement.Projection.AppliedWorkspaceCursor != snapshot.Cursor {
		t.Fatal("initialization invented a cursor or acknowledged a partial tree")
	}
	pty, err := service.Attach(t.Context(), request.AttemptID, placement.GrantID, appexecution.Window{Cols: 80, Rows: 24})
	if err != nil {
		t.Fatal(err)
	}
	defer pty.Close()
	again, err := service.Ensure(t.Context(), request)
	if err != nil || again.GrantID != placement.GrantID || again.Projection != placement.Projection {
		t.Fatalf("large initial projection not reusable: %v", err)
	}
}
