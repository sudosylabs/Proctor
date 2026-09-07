// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package execution

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

type projectionHosts struct {
	hostsFake
	environment Environment
}

func (hosts projectionHosts) Ensure(context.Context, string, Spec) (Environment, error) {
	return hosts.environment, nil
}

func (hosts projectionHosts) Existing(context.Context, string, Spec) (Environment, error) {
	return hosts.environment, nil
}

type projectionEnvironment struct {
	environmentFake
	replace func(Tree) error
	apply   func([]Mutation) error
}

func (env projectionEnvironment) ReplaceTree(_ context.Context, tree Tree) error {
	return env.replace(tree)
}
func (env projectionEnvironment) Apply(_ context.Context, changes []Mutation) error {
	return env.apply(changes)
}

type reservationMutationStore struct {
	*grantStoreFake
}

func (grants reservationMutationStore) Reserve(ctx context.Context, reservation store.ExecutionGrantReservation) (*model.ExecutionGrant, error) {
	grant, err := grants.grantStoreFake.Reserve(ctx, reservation)
	// Model an acknowledged create after placement is committed, before the
	// first host snapshot is read. Its transient callback may have been lost.
	grants.snapshot = &store.ExecutionWorkspaceSnapshot{Cursor: 1, Nodes: []store.ExecutionWorkspaceNode{{Path: "saved", Kind: model.StarterWorkspaceEntryDirectory}}}
	return grant, err
}

func TestEnsureCapturesWorkspaceAfterReservation(t *testing.T) {
	t.Parallel()
	events := []string{}
	grants := reservationMutationStore{&grantStoreFake{events: &events}}
	var projected Tree
	env := projectionEnvironment{replace: func(tree Tree) error { projected = tree; return nil }}
	hosts := projectionHosts{hostsFake: hostsFake{events: &events, catalog: []HostStatus{{ID: "runner", Usable: true, Isolated: true, Images: []string{"go"}, Networks: []Network{NetworkNone}, Slots: 1}}}, environment: env}
	service, err := New(grants, hosts, contentFake{}, time.Now, model.NewExecutionGrantID)
	if err != nil {
		t.Fatal(err)
	}
	placement, err := service.Ensure(t.Context(), Request{AttemptID: model.NewExamAttemptID(), Image: "go", Network: NetworkNone})
	if err != nil {
		t.Fatal(err)
	}
	if !placement.Ready || len(projected) != 1 || projected[0].Path != "saved" || grants.current.AppliedWorkspaceCursor != 1 {
		t.Fatalf("placement=%#v tree=%#v grant=%#v", placement, projected, grants.current)
	}
}

func TestEnsureRetiresSnapshotOvertakenDuringProjection(t *testing.T) {
	t.Parallel()
	events := []string{}
	grants := &grantStoreFake{events: &events, snapshot: &store.ExecutionWorkspaceSnapshot{}}
	env := projectionEnvironment{replace: func(Tree) error { grants.snapshot = &store.ExecutionWorkspaceSnapshot{Cursor: 1}; return nil }}
	hosts := projectionHosts{hostsFake: hostsFake{events: &events, catalog: []HostStatus{{ID: "runner", Usable: true, Isolated: true, Images: []string{"go"}, Networks: []Network{NetworkNone}, Slots: 1}}}, environment: env}
	service, err := New(grants, hosts, contentFake{}, time.Now, model.NewExecutionGrantID)
	if err != nil {
		t.Fatal(err)
	}
	_, err = service.Ensure(t.Context(), Request{AttemptID: model.NewExamAttemptID(), Image: "go", Network: NetworkNone})
	if !errors.Is(err, ErrConflict) || grants.current != nil || !slices.Contains(events, "revoked:runner") {
		t.Fatalf("err=%v current=%#v events=%v", err, grants.current, events)
	}
}

func projectionFixture(t *testing.T, cursor int64) (*grantStoreFake, *model.ExecutionGrant, model.AttemptWorkspaceJournalEntry, *[]string) {
	t.Helper()
	events := []string{}
	at := time.Now().UTC()
	grant := &model.ExecutionGrant{ID: model.NewExecutionGrantID(), AttemptID: model.NewExamAttemptID(), HostID: "runner", Image: "go", Network: model.ExecutionNetworkNone, State: model.ExecutionGrantReady, AppliedSittingState: model.ExamSittingOpen, AppliedSittingRevision: 1, CreatedAt: at, UpdatedAt: at, Revision: 2}
	grants := &grantStoreFake{current: grant, all: map[model.ExecutionGrantID]*model.ExecutionGrant{grant.ID: grant}, events: &events, snapshot: &store.ExecutionWorkspaceSnapshot{Cursor: cursor}, convergence: []store.ExecutionGrantConvergence{{Grant: grant, AttemptState: model.ExamAttemptActive, SittingState: model.ExamSittingOpen, SittingRevision: 1, WorkspaceCursor: cursor}}}
	change := model.AttemptWorkspaceJournalEntry{WorkspaceID: model.NewExamAttemptWorkspaceID(), Cursor: cursor, EntryID: model.NewAttemptWorkspaceEntryID(), EntryKind: model.StarterWorkspaceEntryDirectory, Operation: model.AttemptWorkspaceMutationCreateDirectory, NewPath: "saved", ChangedAt: at}
	return grants, grant, change, &events
}

func TestWorkspaceProjectionFailureRetiresExactGrant(t *testing.T) {
	for _, test := range []struct {
		name      string
		cursor    int64
		fail      bool
		pending   bool
		reconcile bool
	}{
		{name: "Apply fails", cursor: 1, fail: true},
		{name: "later change arrives first", cursor: 2},
		{name: "effect callback lost", cursor: 1, reconcile: true},
		{name: "process interrupted effect", cursor: 0, pending: true, reconcile: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			grants, grant, change, events := projectionFixture(t, test.cursor)
			grant.WorkspacePending = test.pending
			applyCalls := 0
			env := projectionEnvironment{apply: func([]Mutation) error {
				applyCalls++
				if test.fail {
					return ErrUnavailable
				}
				return nil
			}}
			service, err := New(grants, projectionHosts{hostsFake: hostsFake{events: events}, environment: env}, contentFake{}, time.Now, model.NewExecutionGrantID)
			if err != nil {
				t.Fatal(err)
			}
			if test.reconcile {
				_, err = service.Reconcile(t.Context())
			} else {
				err = service.SyncChange(t.Context(), grant.AttemptID, change)
			}
			if grants.current != nil || !grant.RevokedAt.Valid {
				t.Fatalf("err=%v current=%#v grant=%#v", err, grants.current, grant)
			}
			if test.fail && applyCalls != 1 || !test.fail && applyCalls != 0 {
				t.Fatalf("Apply calls=%d", applyCalls)
			}
		})
	}
}

func TestWorkspaceProjectionAcknowledgesGuestAndIgnoresReplay(t *testing.T) {
	t.Parallel()
	grants, grant, change, events := projectionFixture(t, 1)
	service, err := New(grants, hostsFake{events: events}, contentFake{}, time.Now, model.NewExecutionGrantID)
	if err != nil {
		t.Fatal(err)
	}
	if err = service.AcknowledgeChange(t.Context(), grant.AttemptID, grant.ID, change); err != nil {
		t.Fatal(err)
	}
	if err = service.SyncChange(t.Context(), grant.AttemptID, change); err != nil {
		t.Fatal(err)
	}
	if grant.AppliedWorkspaceCursor != 1 || grant.WorkspacePending || len(*events) != 0 {
		t.Fatalf("grant=%#v events=%v", grant, *events)
	}
}

type selectiveProjectionContent struct {
	contentFake
	want   model.AttemptWorkspaceObjectID
	opened int
}

func (content *selectiveProjectionContent) OpenAttemptWorkspaceObject(_ context.Context, id model.AttemptWorkspaceObjectID) (io.ReadCloser, error) {
	if id != content.want {
		return nil, errors.New("unrelated content was opened")
	}
	content.opened++
	return io.NopCloser(strings.NewReader("changed")), nil
}
func TestSyncChangeReadsOnlyChangedFileBody(t *testing.T) {
	t.Parallel()
	grants, grant, change, events := projectionFixture(t, 1)
	content := &selectiveProjectionContent{want: model.NewAttemptWorkspaceObjectID()}
	digest := sha256.Sum256([]byte("changed"))
	change.EntryKind, change.Operation, change.ContentVersion = model.StarterWorkspaceEntryFile, model.AttemptWorkspaceMutationCreateFile, model.NewWorkspaceContentVersion()
	grants.snapshot.Nodes = []store.ExecutionWorkspaceNode{
		{Kind: model.StarterWorkspaceEntryFile, Path: "unrelated", ContentVersion: model.NewWorkspaceContentVersion(), StorageOrigin: model.AttemptWorkspaceStorageAttempt, AttemptObjectID: model.NewAttemptWorkspaceObjectID()},
		{Kind: model.StarterWorkspaceEntryFile, Path: change.NewPath, ContentVersion: change.ContentVersion, StorageOrigin: model.AttemptWorkspaceStorageAttempt, AttemptObjectID: content.want, SizeBytes: 7, SHA256: hex.EncodeToString(digest[:])},
	}
	var applied []Mutation
	env := projectionEnvironment{apply: func(changes []Mutation) error { applied = changes; return nil }}
	service, err := New(grants, projectionHosts{hostsFake: hostsFake{events: events}, environment: env}, content, time.Now, model.NewExecutionGrantID)
	if err != nil {
		t.Fatal(err)
	}
	if err = service.SyncChange(t.Context(), grant.AttemptID, change); err != nil {
		t.Fatal(err)
	}
	if content.opened != 1 || len(applied) != 1 || string(applied[0].Data) != "changed" || grant.AppliedWorkspaceCursor != 1 {
		t.Fatalf("opens=%d mutations=%#v grant=%#v", content.opened, applied, grant)
	}
}

type pendingPageStore struct {
	*grantStoreFake
	pending []*model.ExecutionGrant
}

func (grants pendingPageStore) ListPendingRevocations(_ context.Context, after model.ExecutionGrantID, limit int) ([]*model.ExecutionGrant, error) {
	result := []*model.ExecutionGrant{}
	for _, grant := range grants.pending {
		if grant.ID > after && !grant.RevokedAt.Valid {
			result = append(result, grant)
			if len(result) == limit {
				break
			}
		}
	}
	return result, nil
}

type selectiveRevocationHosts struct{ hostsFake }

func (hosts selectiveRevocationHosts) Revoke(ctx context.Context, host, id string) error {
	if host == "unavailable" {
		return ErrUnavailable
	}
	return hosts.hostsFake.Revoke(ctx, host, id)
}
func TestPendingRevocationsAdvancePastUnavailablePage(t *testing.T) {
	t.Parallel()
	events := []string{}
	grants := pendingPageStore{grantStoreFake: &grantStoreFake{events: &events, all: map[model.ExecutionGrantID]*model.ExecutionGrant{}}}
	at := time.Now().UTC()
	for range PendingRevocationPageSize + 1 {
		grant := &model.ExecutionGrant{ID: model.NewExecutionGrantID(), AttemptID: model.NewExamAttemptID(), HostID: "unavailable", Image: "go", Network: model.ExecutionNetworkNone, State: model.ExecutionGrantReleased, AppliedSittingState: model.ExamSittingOpen, AppliedSittingRevision: 1, CreatedAt: at, UpdatedAt: at, ReleasedAt: model.OptionalTimeFrom(at), Revision: 2}
		grants.pending = append(grants.pending, grant)
		grants.all[grant.ID] = grant
	}
	slices.SortFunc(grants.pending, func(a, b *model.ExecutionGrant) int { return strings.Compare(a.ID.String(), b.ID.String()) })
	healthy := grants.pending[len(grants.pending)-1]
	healthy.HostID = "healthy"
	service, err := New(grants, selectiveRevocationHosts{hostsFake{events: &events}}, contentFake{}, time.Now, model.NewExecutionGrantID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = service.Reconcile(t.Context()); err == nil {
		t.Fatal("unavailable first page should report failures")
	}
	if completed, err := service.Reconcile(t.Context()); err != nil || completed != 1 || !healthy.RevokedAt.Valid {
		t.Fatalf("completed=%d err=%v healthy=%#v", completed, err, healthy)
	}
	if _, err = service.Reconcile(t.Context()); err == nil {
		t.Fatal("wrapped page should retry unavailable grants")
	}
}

func TestTwoNodesNeverApplyWorkspaceEffectsOutOfOrder(t *testing.T) {
	t.Parallel()
	grants, grant, first, events := projectionFixture(t, 1)
	started, finish := make(chan struct{}), make(chan struct{})
	calls := 0
	env := projectionEnvironment{apply: func([]Mutation) error { calls++; close(started); <-finish; return nil }}
	hosts := projectionHosts{hostsFake: hostsFake{events: events}, environment: env}
	firstNode, err := New(grants, hosts, contentFake{}, time.Now, model.NewExecutionGrantID)
	if err != nil {
		t.Fatal(err)
	}
	secondNode, err := New(grants, hosts, contentFake{}, time.Now, model.NewExecutionGrantID)
	if err != nil {
		t.Fatal(err)
	}
	attemptID := grant.AttemptID
	firstResult := make(chan error, 1)
	go func() { firstResult <- firstNode.SyncChange(t.Context(), attemptID, first) }()
	<-started
	grants.mu.Lock()
	grants.snapshot.Cursor = 2
	grants.mu.Unlock()
	second := first
	second.Cursor, second.EntryID, second.NewPath = 2, model.NewAttemptWorkspaceEntryID(), "newer"
	secondResult := make(chan error, 1)
	go func() { secondResult <- secondNode.SyncChange(t.Context(), attemptID, second) }()
	close(finish)
	firstErr, secondErr := <-firstResult, <-secondResult
	if !errors.Is(firstErr, ErrConflict) || secondErr != nil || calls != 1 || grants.current != nil || !grant.RevokedAt.Valid {
		t.Fatalf("first=%v second=%v calls=%d current=%#v grant=%#v", firstErr, secondErr, calls, grants.current, grant)
	}
}

func TestDelayedGuestAcknowledgementCannotAdvanceSuccessor(t *testing.T) {
	t.Parallel()
	grants, grant, change, events := projectionFixture(t, 1)
	service, err := New(grants, hostsFake{events: events}, contentFake{}, time.Now, model.NewExecutionGrantID)
	if err != nil {
		t.Fatal(err)
	}
	if err := service.AcknowledgeChange(t.Context(), grant.AttemptID, model.NewExecutionGrantID(), change); err != nil {
		t.Fatal(err)
	}
	if grant.AppliedWorkspaceCursor != 0 || grant.WorkspacePending || len(*events) != 0 {
		t.Fatalf("successor was falsely acknowledged: %#v events=%v", grant, *events)
	}
	if _, err := service.Reconcile(t.Context()); err != nil {
		t.Fatal(err)
	}
	if grants.current != nil || !grant.RevokedAt.Valid {
		t.Fatalf("divergent successor remained current: %#v", grant)
	}
}

func TestExecutionInteractionsRequireExactGrant(t *testing.T) {
	t.Parallel()
	grants, grant, _, events := projectionFixture(t, 0)
	service, err := New(grants, hostsFake{events: events}, contentFake{}, time.Now, model.NewExecutionGrantID)
	if err != nil {
		t.Fatal(err)
	}
	other := model.NewExecutionGrantID()
	if _, err := service.Watch(t.Context(), grant.AttemptID, other, ""); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("Watch = %v", err)
	}
	if _, err := service.Attach(t.Context(), grant.AttemptID, other, Window{Cols: 80, Rows: 24}); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("Attach = %v", err)
	}
	if _, err := service.OpenFile(t.Context(), grant.AttemptID, other, "file"); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("OpenFile = %v", err)
	}
	if len(*events) != 0 {
		t.Fatalf("wrong-grant interactions reached host: %v", *events)
	}
}

func TestExecutionInteractionCannotRecreateMissingHostHandle(t *testing.T) {
	t.Parallel()
	grants, grant, _, events := projectionFixture(t, 0)
	service, err := New(grants, hostsFake{events: events, fail: map[string]error{"runner": ErrUnavailable}}, contentFake{}, time.Now, model.NewExecutionGrantID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Watch(t.Context(), grant.AttemptID, grant.ID, ""); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("Watch after handle loss = %v", err)
	}
	if !slices.Equal(*events, []string{"existing:runner"}) {
		t.Fatalf("interaction attempted to recreate its guest: %v", *events)
	}
}

type interactionObservation struct {
	observationFake
	closed bool
}

func (observation *interactionObservation) Close() error {
	observation.closed = true
	return nil
}

type interactionEnvironment struct {
	environmentFake
	watch func() (Observation, error)
}

func (environment interactionEnvironment) Watch(context.Context, Cursor) (Observation, error) {
	return environment.watch()
}

func TestExecutionInteractionClosesResourceWhenGrantReleasedDuringAcquisition(t *testing.T) {
	t.Parallel()
	grants, grant, _, _ := projectionFixture(t, 0)
	observation := &interactionObservation{}
	environment := interactionEnvironment{watch: func() (Observation, error) {
		grants.current = nil // Another node's durable release wins during Watch.
		return observation, nil
	}}
	service, err := New(grants, projectionHosts{environment: environment}, contentFake{}, time.Now, model.NewExecutionGrantID)
	if err != nil {
		t.Fatal(err)
	}
	result, err := service.Watch(t.Context(), grant.AttemptID, grant.ID, "")
	if err == nil || result != nil || !observation.closed {
		t.Fatalf("Watch crossed release fence: result=%v error=%v closed=%v", result, err, observation.closed)
	}
}
