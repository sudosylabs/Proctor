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
	"reflect"
	"testing"
	"time"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

type projectionTestStore struct {
	*grantStoreFake
	page     *store.ExecutionWorkspacePage
	pending  *store.ExecutionProjectionRequest
	prepared []store.ExecutionProjectionRequest
	calls    *[]string
}

func (s *projectionTestStore) WorkspaceChanges(context.Context, model.ExecutionFence, int) (*store.ExecutionWorkspacePage, error) {
	return s.page, nil
}
func (s *projectionTestStore) PendingProjection(context.Context, model.ExecutionGrantID) (*store.ExecutionProjectionRequest, error) {
	if s.pending == nil {
		return nil, store.NewErrNotFound("execution_projection", "pending")
	}
	return s.pending, nil
}
func (s *projectionTestStore) PrepareProjection(_ context.Context, request store.ExecutionProjectionRequest) (*store.ExecutionProjectionEffect, error) {
	if err := request.Validate(); err != nil {
		return nil, err
	}
	*s.calls = append(*s.calls, "prepare")
	if s.pending != nil && !reflect.DeepEqual(request, *s.pending) {
		return nil, ErrConflict
	}
	s.prepared = append(s.prepared, request)
	s.pending = &request
	s.current.WorkspacePending = true
	return &store.ExecutionProjectionEffect{Request: &request}, nil
}
func (s *projectionTestStore) CompleteProjection(_ context.Context, receipt store.ExecutionProjectionReceipt) (*model.ExecutionGrant, error) {
	*s.calls = append(*s.calls, "complete")
	s.current.AppliedWorkspaceCursor = receipt.AppliedWorkspaceCursor
	s.current.WorkspacePending = false
	s.current.State = model.ExecutionGrantReady
	s.pending = nil
	value := *s.current
	return &value, nil
}

type projectionTestEnvironment struct {
	*controlTestEnvironment
	calls       *[]string
	uploaded    [][]byte
	applied     []store.ExecutionProjectionRequest
	failure     error
	receiptEdit func(*store.ExecutionProjectionReceipt)
}

func (e *projectionTestEnvironment) UploadProjectionContent(_ context.Context, _ model.ExecutionFence, body io.Reader) (store.ExecutionProjectionContent, error) {
	data, err := io.ReadAll(body)
	if err != nil {
		return store.ExecutionProjectionContent{}, err
	}
	*e.calls = append(*e.calls, "upload")
	e.uploaded = append(e.uploaded, data)
	digest := sha256.Sum256(data)
	return store.ExecutionProjectionContent{TransferID: model.NewId(), Size: int64(len(data)), SHA256: hex.EncodeToString(digest[:])}, nil
}
func (e *projectionTestEnvironment) ApplyProjection(_ context.Context, request store.ExecutionProjectionRequest) (store.ExecutionProjectionReceipt, error) {
	*e.calls = append(*e.calls, "apply")
	e.applied = append(e.applied, request)
	receipt := store.ExecutionProjectionReceipt{Fence: request.Fence, MutationID: request.MutationID, AppliedWorkspaceCursor: request.ThroughWorkspaceCursor, HostCursor: request.ExpectedHostCursor}
	if e.receiptEdit != nil {
		e.receiptEdit(&receipt)
	}
	return receipt, e.failure
}

func TestProjectionRetainsExactInterruptedEffect(t *testing.T) {
	t.Parallel()
	var calls []string
	now := time.Now().UTC()
	body := "original journal bytes"
	digest := sha256.Sum256([]byte(body))
	entry := model.NewAttemptWorkspaceEntryID()
	version := model.NewWorkspaceContentVersion()
	grant := &model.ExecutionGrant{ID: model.NewExecutionGrantID(), AttemptID: model.NewExamAttemptID(), State: model.ExecutionGrantReady, EnvironmentEpoch: "epoch", ControlRevision: 1, ControlAcknowledgedRevision: 1, DesiredControlState: model.ExecutionControlRunning}
	change := store.ExecutionWorkspaceChange{Change: model.AttemptWorkspaceJournalEntry{WorkspaceID: model.NewExamAttemptWorkspaceID(), Cursor: 1, EntryID: entry, EntryKind: model.StarterWorkspaceEntryFile, Operation: model.AttemptWorkspaceMutationCreateFile, NewPath: "a.txt", ContentVersion: version, ChangedAt: now}, Content: &store.ExecutionWorkspaceNode{EntryID: entry, Kind: model.StarterWorkspaceEntryFile, Path: "a.txt", ContentVersion: version, SizeBytes: int64(len(body)), SHA256: hex.EncodeToString(digest[:]), StorageOrigin: model.AttemptWorkspaceStorageAttempt, AttemptObjectID: model.NewAttemptWorkspaceObjectID()}}
	grants := &projectionTestStore{grantStoreFake: &grantStoreFake{current: grant, events: &calls}, page: &store.ExecutionWorkspacePage{CurrentCursor: 1, Changes: []store.ExecutionWorkspaceChange{change}}, calls: &calls}
	host := &projectionTestEnvironment{controlTestEnvironment: &controlTestEnvironment{epoch: "epoch"}, calls: &calls, failure: context.DeadlineExceeded}
	service := &Service{grants: grants, content: bodyContentFake{body: body}}
	if _, err := service.projectEnvironment(context.Background(), grant, host, controlTestLease{}); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("lost response: %v", err)
	}
	if !grant.WorkspacePending || grant.AppliedWorkspaceCursor != 0 || grants.pending == nil {
		t.Fatal("lost response erased recovery intent")
	}
	// Once prepared, neither a later live snapshot nor the next journal lookup is
	// consulted. The original immutable transfer receipt is replayed exactly.
	grants.page = nil
	service.content = bodyContentFake{body: "different current bytes"}
	host.failure = nil
	result, err := service.projectEnvironment(context.Background(), grant, host, controlTestLease{})
	if err != nil {
		t.Fatal(err)
	}
	if result.AppliedWorkspaceCursor != 1 || result.WorkspacePending || len(host.uploaded) != 1 || string(host.uploaded[0]) != body || len(host.applied) != 2 || !reflect.DeepEqual(host.applied[0], host.applied[1]) {
		t.Fatal("retry changed the saved effect or lost receipt")
	}
	if !reflect.DeepEqual(calls, []string{"upload", "prepare", "apply", "prepare", "apply", "complete"}) {
		t.Fatalf("effect order: %v", calls)
	}
}

func TestProjectionDoesNotAdvanceOnWrongReceipt(t *testing.T) {
	t.Parallel()
	var calls []string
	grant := &model.ExecutionGrant{ID: model.NewExecutionGrantID(), AttemptID: model.NewExamAttemptID(), HostID: "runner", State: model.ExecutionGrantReserved, EnvironmentEpoch: "epoch", ControlRevision: 1, ControlAcknowledgedRevision: 1, DesiredControlState: model.ExecutionControlRunning}
	grants := &projectionTestStore{grantStoreFake: &grantStoreFake{current: grant, events: &calls, snapshot: &store.ExecutionWorkspaceSnapshot{}}, calls: &calls}
	host := &projectionTestEnvironment{controlTestEnvironment: &controlTestEnvironment{epoch: "epoch"}, calls: &calls, receiptEdit: func(r *store.ExecutionProjectionReceipt) { r.HostCursor++ }}
	service := &Service{grants: grants, hosts: hostsFake{events: &calls}, now: time.Now}
	if _, err := service.projectEnvironment(context.Background(), grant, host, controlTestLease{}); !errors.Is(err, ErrConflict) {
		t.Fatalf("wrong receipt: %v", err)
	}
	for _, call := range calls {
		if call == "complete" {
			t.Fatal("wrong receipt advanced durable progress")
		}
	}
}

func TestFrozenProjectionDoesNotTouchHost(t *testing.T) {
	t.Parallel()
	var calls []string
	grant := &model.ExecutionGrant{ID: model.NewExecutionGrantID(), EnvironmentEpoch: "epoch", ControlRevision: 2, ControlAcknowledgedRevision: 2, DesiredControlState: model.ExecutionControlFrozen}
	host := &projectionTestEnvironment{controlTestEnvironment: &controlTestEnvironment{epoch: "epoch"}, calls: &calls}
	if _, err := (&Service{}).projectEnvironment(context.Background(), grant, host, controlTestLease{}); !errors.Is(err, ErrProjectionPending) {
		t.Fatalf("frozen projection: %v", err)
	}
	if len(calls) != 0 {
		t.Fatal("frozen projection performed host I/O")
	}
}

func (s *projectionTestStore) RejectProjection(_ context.Context, f model.ExecutionFence, id string) (*model.ExecutionGrant, error) {
	if s.pending == nil || s.pending.Fence != f || s.pending.MutationID != id {
		return nil, ErrConflict
	}
	*s.calls = append(*s.calls, "reject")
	s.pending = nil
	s.current.WorkspacePending = false
	return s.current, nil
}

func TestProjectionRetriesWithNewIntentOnlyAfterDefinitiveRefusal(t *testing.T) {
	t.Parallel()
	var calls []string
	grant := &model.ExecutionGrant{ID: model.NewExecutionGrantID(), AttemptID: model.NewExamAttemptID(), State: model.ExecutionGrantReady, EnvironmentEpoch: "epoch", ControlRevision: 1, ControlAcknowledgedRevision: 1, DesiredControlState: model.ExecutionControlRunning}
	change := model.AttemptWorkspaceJournalEntry{WorkspaceID: model.NewExamAttemptWorkspaceID(), Cursor: 1, EntryID: model.NewAttemptWorkspaceEntryID(), EntryKind: model.StarterWorkspaceEntryDirectory, Operation: model.AttemptWorkspaceMutationCreateDirectory, NewPath: "dir", ChangedAt: time.Now().UTC()}
	grants := &projectionTestStore{grantStoreFake: &grantStoreFake{current: grant, events: &calls}, calls: &calls, page: &store.ExecutionWorkspacePage{CurrentCursor: 1, Changes: []store.ExecutionWorkspaceChange{{Change: change}}}}
	host := &projectionTestEnvironment{controlTestEnvironment: &controlTestEnvironment{epoch: "epoch"}, calls: &calls, failure: ErrHostCursorConflict}
	service := &Service{grants: grants}
	if _, err := service.projectEnvironment(context.Background(), grant, host, controlTestLease{}); !errors.Is(err, ErrProjectionPending) {
		t.Fatalf("refusal: %v", err)
	}
	if grant.WorkspacePending || grant.AppliedWorkspaceCursor != 0 || grants.pending != nil {
		t.Fatal("refusal left an irrecoverable pending intent")
	}
	host.failure = nil
	if _, err := service.projectEnvironment(context.Background(), grant, host, controlTestLease{}); err != nil {
		t.Fatal(err)
	}
	if len(host.applied) != 2 || host.applied[0].MutationID == host.applied[1].MutationID || grant.AppliedWorkspaceCursor != 1 {
		t.Fatal("new request reused a refused identity or did not advance")
	}
}

func TestEnsureRejectsFutureCursorBeforeHostAccess(t *testing.T) {
	t.Parallel()
	service := &Service{grants: &grantStoreFake{snapshot: &store.ExecutionWorkspaceSnapshot{Cursor: 7}}}
	placement, err := service.Ensure(context.Background(), Request{AttemptID: model.NewExamAttemptID(), Image: "go", Network: NetworkNone, ExpectedWorkspaceCursor: 8})
	if placement != nil || !errors.Is(err, ErrInvalid) {
		t.Fatalf("future cursor: %v", err)
	}
}
