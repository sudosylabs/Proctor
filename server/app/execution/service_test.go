// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package execution

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

type grantStoreFake struct {
	mu               sync.Mutex
	current          *model.ExecutionGrant
	all              map[model.ExecutionGrantID]*model.ExecutionGrant
	events           *[]string
	snapshot         *store.ExecutionWorkspaceSnapshot
	convergence      []store.ExecutionGrantConvergence
	markAppliedErr   error
	leaseValidateErr error
	leaseValidateAt  int
}

func (fake *grantStoreFake) Current(_ context.Context, attemptID model.ExamAttemptID) (*model.ExecutionGrant, error) {
	fake.mu.Lock()
	defer fake.mu.Unlock()
	if fake.current == nil || fake.current.AttemptID != attemptID {
		return nil, store.NewErrNotFound("execution_grant", attemptID.String())
	}
	copy := *fake.current
	return &copy, nil
}

func (fake *grantStoreFake) Reserve(_ context.Context, value store.ExecutionGrantReservation) (*model.ExecutionGrant, error) {
	fake.mu.Lock()
	defer fake.mu.Unlock()
	grant := &model.ExecutionGrant{ID: value.ID, AttemptID: value.AttemptID, HostID: value.HostID, Image: value.Image,
		Network: value.Network, State: model.ExecutionGrantReserved, AppliedSittingState: model.ExamSittingOpen,
		AppliedSittingRevision: 1, CreatedAt: value.At, UpdatedAt: value.At, Revision: 1}
	fake.current = grant
	if fake.all == nil {
		fake.all = make(map[model.ExecutionGrantID]*model.ExecutionGrant)
	}
	fake.all[grant.ID] = grant
	*fake.events = append(*fake.events, "reserve:"+value.HostID)
	copy := *grant
	return &copy, nil
}

func (fake *grantStoreFake) Reassign(_ context.Context, value store.ExecutionGrantReassignment) (*store.ExecutionGrantReassignmentResult, error) {
	fake.mu.Lock()
	defer fake.mu.Unlock()
	previous := *fake.current
	previous.State, previous.ReleasedAt, previous.UpdatedAt, previous.Revision = model.ExecutionGrantReleased,
		model.OptionalTimeFrom(value.Replacement.At), value.Replacement.At, previous.Revision+1
	fake.all[previous.ID] = &previous
	current := &model.ExecutionGrant{ID: value.Replacement.ID, AttemptID: value.Replacement.AttemptID,
		HostID: value.Replacement.HostID, Image: value.Replacement.Image, Network: value.Replacement.Network,
		State: model.ExecutionGrantReserved, AppliedSittingState: model.ExamSittingOpen, AppliedSittingRevision: 1,
		CreatedAt: value.Replacement.At, UpdatedAt: value.Replacement.At, Revision: 1}
	fake.current, fake.all[current.ID] = current, current
	*fake.events = append(*fake.events, "reassign:"+current.HostID)
	previousCopy, currentCopy := previous, *current
	return &store.ExecutionGrantReassignmentResult{Previous: &previousCopy, Current: &currentCopy}, nil
}

func (fake *grantStoreFake) MarkReady(_ context.Context, id model.ExecutionGrantID, revision int64, at time.Time) (*model.ExecutionGrant, error) {
	fake.mu.Lock()
	defer fake.mu.Unlock()
	fake.current.State, fake.current.UpdatedAt, fake.current.Revision = model.ExecutionGrantReady, at, revision+1
	*fake.events = append(*fake.events, "ready:"+fake.current.HostID)
	copy := *fake.current
	return &copy, nil
}

func (fake *grantStoreFake) PrepareSittingStateEffect(_ context.Context, id model.ExecutionGrantID, revision int64, state model.ExamSittingState, sittingRevision int64, at time.Time) (*model.ExecutionGrant, error) {
	fake.mu.Lock()
	defer fake.mu.Unlock()
	grant := fake.all[id]
	if grant == nil || grant.Revision != revision || grant.LifecyclePending {
		return nil, store.NewErrConflict("execution_grant", "sitting_state", nil)
	}
	grant.LifecyclePending, grant.PendingSittingState, grant.PendingSittingRevision = true, state, sittingRevision
	grant.UpdatedAt, grant.Revision = at, revision+1
	copy := *grant
	return &copy, nil
}

func (fake *grantStoreFake) MarkSittingStateApplied(_ context.Context, id model.ExecutionGrantID, revision int64, state model.ExamSittingState, sittingRevision int64, at time.Time) (*model.ExecutionGrant, error) {
	fake.mu.Lock()
	defer fake.mu.Unlock()
	if fake.markAppliedErr != nil {
		return nil, fake.markAppliedErr
	}
	grant := fake.all[id]
	if grant == nil || grant.Revision != revision {
		return nil, store.NewErrConflict("execution_grant", "sitting_state", nil)
	}
	grant.AppliedSittingState, grant.AppliedSittingRevision = state, sittingRevision
	grant.LifecyclePending, grant.PendingSittingState, grant.PendingSittingRevision = false, "", 0
	grant.UpdatedAt, grant.Revision = at, revision+1
	*fake.events = append(*fake.events, "applied:"+string(state))
	copy := *grant
	return &copy, nil
}

func (fake *grantStoreFake) Release(_ context.Context, _ model.ExamAttemptID, at time.Time) (*model.ExecutionGrant, error) {
	fake.mu.Lock()
	defer fake.mu.Unlock()
	fake.current.State, fake.current.ReleasedAt, fake.current.UpdatedAt, fake.current.Revision = model.ExecutionGrantReleased,
		model.OptionalTimeFrom(at), at, fake.current.Revision+1
	*fake.events = append(*fake.events, "release:"+fake.current.HostID)
	copy := *fake.current
	fake.current = nil
	fake.all[copy.ID] = &copy
	return &copy, nil
}

func (fake *grantStoreFake) ReleaseGrant(_ context.Context, id model.ExecutionGrantID, at time.Time) (*model.ExecutionGrant, error) {
	fake.mu.Lock()
	defer fake.mu.Unlock()
	grant := fake.all[id]
	if grant == nil || grant.State == model.ExecutionGrantReleased {
		return nil, store.NewErrNotFound("execution_grant", id.String())
	}
	grant.State, grant.ReleasedAt, grant.UpdatedAt, grant.Revision = model.ExecutionGrantReleased,
		model.OptionalTimeFrom(at), at, grant.Revision+1
	grant.LifecyclePending, grant.PendingSittingState, grant.PendingSittingRevision = false, "", 0
	if fake.current != nil && fake.current.ID == id {
		fake.current = nil
	}
	*fake.events = append(*fake.events, "release:"+grant.HostID)
	copy := *grant
	return &copy, nil
}

func (fake *grantStoreFake) MarkRevoked(_ context.Context, id model.ExecutionGrantID, revision int64, at time.Time) (*model.ExecutionGrant, error) {
	fake.mu.Lock()
	defer fake.mu.Unlock()
	grant := fake.all[id]
	grant.RevokedAt, grant.UpdatedAt, grant.Revision = model.OptionalTimeFrom(at), at, revision+1
	*fake.events = append(*fake.events, "revoked:"+grant.HostID)
	copy := *grant
	return &copy, nil
}

func (fake *grantStoreFake) ListPendingRevocations(context.Context, int) ([]*model.ExecutionGrant, error) {
	return nil, nil
}

type lifecycleLeaseFake struct {
	validateErr error
	failAt      int
	calls       int
}

func (fake *lifecycleLeaseFake) Validate(context.Context) error {
	fake.calls++
	if fake.calls == fake.failAt {
		return fake.validateErr
	}
	return nil
}
func (*lifecycleLeaseFake) Release(context.Context) error { return nil }

func (fake *grantStoreFake) AcquireLifecycleLease(context.Context, model.ExecutionGrantID) (store.ExecutionLifecycleLease, error) {
	failAt := fake.leaseValidateAt
	if failAt == 0 && fake.leaseValidateErr != nil {
		failAt = 2
	}
	return &lifecycleLeaseFake{validateErr: fake.leaseValidateErr, failAt: failAt}, nil
}
func (fake *grantStoreFake) CurrentForReconciliation(_ context.Context, id model.ExecutionGrantID) (*store.ExecutionGrantConvergence, error) {
	for index := range fake.convergence {
		if fake.convergence[index].Grant != nil && fake.convergence[index].Grant.ID == id {
			value := fake.convergence[index]
			return &value, nil
		}
	}
	return nil, store.NewErrNotFound("execution_grant", id.String())
}
func (fake *grantStoreFake) ListCurrentForReconciliation(context.Context, model.ExecutionGrantID, int) ([]store.ExecutionGrantConvergence, error) {
	return append([]store.ExecutionGrantConvergence(nil), fake.convergence...), nil
}
func (fake *grantStoreFake) ListCurrentForSitting(context.Context, model.ExamSittingID, model.ExecutionGrantID, int) ([]*model.ExecutionGrant, error) {
	return nil, nil
}
func (fake *grantStoreFake) WorkspaceSnapshot(context.Context, model.ExamAttemptID) (*store.ExecutionWorkspaceSnapshot, error) {
	if fake.snapshot != nil {
		return fake.snapshot, nil
	}
	return &store.ExecutionWorkspaceSnapshot{}, nil
}

type contentFake struct{}

func (contentFake) OpenStarterWorkspaceObject(context.Context, model.StarterWorkspaceObjectID) (io.ReadCloser, error) {
	return io.NopCloser(strings.NewReader("")), nil
}

type bodyContentFake struct{ body string }

func (fake bodyContentFake) OpenStarterWorkspaceObject(context.Context, model.StarterWorkspaceObjectID) (io.ReadCloser, error) {
	return io.NopCloser(strings.NewReader(fake.body)), nil
}
func (fake bodyContentFake) OpenAttemptWorkspaceObject(context.Context, model.AttemptWorkspaceObjectID) (io.ReadCloser, error) {
	return io.NopCloser(strings.NewReader(fake.body)), nil
}
func (contentFake) OpenAttemptWorkspaceObject(context.Context, model.AttemptWorkspaceObjectID) (io.ReadCloser, error) {
	return io.NopCloser(strings.NewReader("")), nil
}

type hostsFake struct {
	catalog []HostStatus
	fail    map[string]error
	events  *[]string
}

func (fake hostsFake) Catalog(context.Context) ([]HostStatus, error) { return fake.catalog, nil }
func (fake hostsFake) Ensure(_ context.Context, host string, _ Spec) (Environment, error) {
	*fake.events = append(*fake.events, "ensure:"+host)
	if err := fake.fail[host]; err != nil {
		return nil, err
	}
	return environmentFake{host: host, events: fake.events}, nil
}
func (fake hostsFake) Revoke(_ context.Context, host, _ string) error {
	*fake.events = append(*fake.events, "revoke:"+host)
	return nil
}

type environmentFake struct {
	host   string
	events *[]string
}

func (fake environmentFake) ReplaceTree(context.Context, Tree) error {
	*fake.events = append(*fake.events, "tree:"+fake.host)
	return nil
}
func (fake environmentFake) Apply(context.Context, []Mutation) error {
	*fake.events = append(*fake.events, "apply:"+fake.host)
	return nil
}
func (environmentFake) Attach(context.Context, Window) (Terminal, error) { return nil, nil }
func (environmentFake) Watch(context.Context, Cursor) (Observation, error) {
	return observationFake{}, nil
}
func (environmentFake) Open(context.Context, string) (io.ReadCloser, error) { return nil, ErrNotFound }
func (fake environmentFake) Freeze(context.Context) error {
	*fake.events = append(*fake.events, "freeze:"+fake.host)
	return nil
}
func (fake environmentFake) Thaw(context.Context) error {
	*fake.events = append(*fake.events, "thaw:"+fake.host)
	return nil
}

type observationFake struct{}

func (observationFake) Cursor() Cursor                      { return "cursor" }
func (observationFake) Next(context.Context) (Event, error) { return Event{}, io.EOF }
func (observationFake) Close() error                        { return nil }

func TestEnsureReassignsAfterCapacityAndPersistsBeforeEffects(t *testing.T) {
	t.Parallel()
	var events []string
	grants := &grantStoreFake{events: &events}
	hosts := hostsFake{events: &events, fail: map[string]error{"runner-b": ErrCapacity}, catalog: []HostStatus{
		{ID: "runner-a", Usable: true, Isolated: true, Images: []string{"go"}, Networks: []Network{NetworkNone}, Slots: 1},
		{ID: "runner-b", Usable: true, Isolated: true, Images: []string{"go"}, Networks: []Network{NetworkNone}, Slots: 2},
	}}
	now := time.Now().UTC()
	service, err := New(grants, hosts, contentFake{}, func() time.Time { now = now.Add(time.Millisecond); return now }, model.NewExecutionGrantID)
	if err != nil {
		t.Fatal(err)
	}
	placement, err := service.Ensure(context.Background(), Request{AttemptID: model.NewExamAttemptID(), Image: "go", Network: NetworkNone})
	if err != nil {
		t.Fatal(err)
	}
	if placement.HostID != "runner-a" || !placement.Ready {
		t.Fatalf("placement = %#v", placement)
	}
	want := []string{"reserve:runner-b", "ensure:runner-b", "reassign:runner-a", "revoke:runner-b", "revoked:runner-b", "ensure:runner-a", "tree:runner-a", "ready:runner-a"}
	if len(events) != len(want) {
		t.Fatalf("events = %v, want %v", events, want)
	}
	for index := range want {
		if events[index] != want[index] {
			t.Fatalf("events = %v, want %v", events, want)
		}
	}
}

func TestReconcileConvergesCurrentGuestsFromDurableLifecycle(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name         string
		attemptState model.ExamAttemptState
		sittingState model.ExamSittingState
		wantEvents   []string
	}{
		{name: "open remains available", attemptState: model.ExamAttemptActive, sittingState: model.ExamSittingOpen},
		{name: "paused freezes", attemptState: model.ExamAttemptActive, sittingState: model.ExamSittingPaused,
			wantEvents: []string{"ensure:runner-a", "freeze:runner-a", "applied:paused"}},
		{name: "suspended releases", attemptState: model.ExamAttemptSuspended, sittingState: model.ExamSittingOpen,
			wantEvents: []string{"release:runner-a", "revoke:runner-a", "revoked:runner-a"}},
		{name: "terminal Sitting releases", attemptState: model.ExamAttemptActive, sittingState: model.ExamSittingClosing,
			wantEvents: []string{"release:runner-a", "revoke:runner-a", "revoked:runner-a"}},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			var events []string
			now := time.Now().UTC()
			grant := &model.ExecutionGrant{ID: model.NewExecutionGrantID(), AttemptID: model.NewExamAttemptID(), HostID: "runner-a",
				Image: "go", Network: model.ExecutionNetworkNone, State: model.ExecutionGrantReady,
				AppliedSittingState: model.ExamSittingOpen, AppliedSittingRevision: 3,
				CreatedAt: now, UpdatedAt: now, Revision: 2}
			grants := &grantStoreFake{current: grant, all: map[model.ExecutionGrantID]*model.ExecutionGrant{grant.ID: grant}, events: &events,
				convergence: []store.ExecutionGrantConvergence{{Grant: grant, AttemptState: test.attemptState, SittingState: test.sittingState, SittingRevision: 3}}}
			service, err := New(grants, hostsFake{events: &events}, contentFake{}, func() time.Time { return now.Add(time.Second) }, model.NewExecutionGrantID)
			if err != nil {
				t.Fatal(err)
			}
			completed, err := service.Reconcile(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if completed != 1 || !reflect.DeepEqual(events, test.wantEvents) {
				t.Fatalf("completed/events = %d/%v, want 1/%v", completed, events, test.wantEvents)
			}
		})
	}
}

func TestStalePauseTargetsExactGrantAndNeverReplacement(t *testing.T) {
	t.Parallel()
	var events []string
	now := time.Now().UTC()
	oldGrant := &model.ExecutionGrant{ID: model.NewExecutionGrantID(), AttemptID: model.NewExamAttemptID(), HostID: "runner-old",
		Image: "go", Network: model.ExecutionNetworkNone, State: model.ExecutionGrantReady,
		AppliedSittingState: model.ExamSittingOpen, AppliedSittingRevision: 2, CreatedAt: now, UpdatedAt: now, Revision: 2}
	newGrant := *oldGrant
	newGrant.ID, newGrant.HostID, newGrant.AppliedSittingRevision = model.NewExecutionGrantID(), "runner-new", 4
	oldPersisted := *oldGrant
	oldPersisted.State, oldPersisted.ReleasedAt, oldPersisted.Revision = model.ExecutionGrantReleased, model.OptionalTimeFrom(now), 3
	grants := &grantStoreFake{current: &newGrant, all: map[model.ExecutionGrantID]*model.ExecutionGrant{
		oldGrant.ID: &oldPersisted, newGrant.ID: &newGrant,
	}, events: &events, markAppliedErr: store.NewErrConflict("execution_grant", "sitting_state", nil)}
	service, err := New(grants, hostsFake{events: &events}, contentFake{}, func() time.Time { return now.Add(time.Second) }, model.NewExecutionGrantID)
	if err != nil {
		t.Fatal(err)
	}
	err = service.convergeCurrent(context.Background(), store.ExecutionGrantConvergence{Grant: oldGrant,
		AttemptState: model.ExamAttemptActive, SittingState: model.ExamSittingPaused, SittingRevision: 3})
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 0 || grants.current.ID != newGrant.ID || grants.current.State != model.ExecutionGrantReady {
		t.Fatalf("events/current = %v/%#v, want no stale effect/new ready grant", events, grants.current)
	}
}

func TestStalePauseRereadsResumeStateUnderGrantLease(t *testing.T) {
	t.Parallel()
	var events []string
	now := time.Now().UTC()
	current := &model.ExecutionGrant{ID: model.NewExecutionGrantID(), AttemptID: model.NewExamAttemptID(), HostID: "runner-a",
		Image: "go", Network: model.ExecutionNetworkNone, State: model.ExecutionGrantReady,
		AppliedSittingState: model.ExamSittingOpen, AppliedSittingRevision: 4, CreatedAt: now, UpdatedAt: now, Revision: 3}
	stale := *current
	stale.AppliedSittingRevision, stale.Revision = 2, 2
	grants := &grantStoreFake{current: current, all: map[model.ExecutionGrantID]*model.ExecutionGrant{current.ID: current},
		events: &events, convergence: []store.ExecutionGrantConvergence{{Grant: current, AttemptState: model.ExamAttemptActive,
			SittingState: model.ExamSittingOpen, SittingRevision: 4}}}
	service, err := New(grants, hostsFake{events: &events}, contentFake{}, func() time.Time { return now.Add(time.Second) }, model.NewExecutionGrantID)
	if err != nil {
		t.Fatal(err)
	}
	err = service.convergeCurrent(context.Background(), store.ExecutionGrantConvergence{Grant: &stale,
		AttemptState: model.ExamAttemptActive, SittingState: model.ExamSittingPaused, SittingRevision: 3})
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 0 || grants.current == nil || grants.current.State != model.ExecutionGrantReady {
		t.Fatalf("events/current = %v/%#v, want no stale effect/current ready grant", events, grants.current)
	}
}

func TestLostLifecycleLeaseReleasesExactGrantAfterHostEffect(t *testing.T) {
	t.Parallel()
	var events []string
	now := time.Now().UTC()
	grant := &model.ExecutionGrant{ID: model.NewExecutionGrantID(), AttemptID: model.NewExamAttemptID(), HostID: "runner-a",
		Image: "go", Network: model.ExecutionNetworkNone, State: model.ExecutionGrantReady,
		AppliedSittingState: model.ExamSittingOpen, AppliedSittingRevision: 2, CreatedAt: now, UpdatedAt: now, Revision: 2}
	grants := &grantStoreFake{current: grant, all: map[model.ExecutionGrantID]*model.ExecutionGrant{grant.ID: grant}, events: &events,
		leaseValidateErr: errors.New("lease connection lost"), convergence: []store.ExecutionGrantConvergence{{Grant: grant,
			AttemptState: model.ExamAttemptActive, SittingState: model.ExamSittingPaused, SittingRevision: 3}}}
	service, err := New(grants, hostsFake{events: &events}, contentFake{}, func() time.Time { return now.Add(time.Second) }, model.NewExecutionGrantID)
	if err != nil {
		t.Fatal(err)
	}
	err = service.convergeCurrent(context.Background(), grants.convergence[0])
	if err == nil {
		t.Fatal("lost lifecycle lease unexpectedly succeeded")
	}
	want := []string{"ensure:runner-a", "freeze:runner-a", "release:runner-a", "revoke:runner-a", "revoked:runner-a"}
	if !reflect.DeepEqual(events, want) || grants.current != nil {
		t.Fatalf("events/current = %v/%#v, want %v/nil", events, grants.current, want)
	}
}

func TestReleaseGrantCannotRevokeSuccessorPlacement(t *testing.T) {
	t.Parallel()
	var events []string
	now := time.Now().UTC()
	attemptID := model.NewExamAttemptID()
	old := &model.ExecutionGrant{ID: model.NewExecutionGrantID(), AttemptID: attemptID, HostID: "runner-old",
		Image: "go", Network: model.ExecutionNetworkNone, State: model.ExecutionGrantReady,
		AppliedSittingState: model.ExamSittingOpen, AppliedSittingRevision: 1, CreatedAt: now, UpdatedAt: now, Revision: 2}
	successor := &model.ExecutionGrant{ID: model.NewExecutionGrantID(), AttemptID: attemptID, HostID: "runner-new",
		Image: "go", Network: model.ExecutionNetworkNone, State: model.ExecutionGrantReady,
		AppliedSittingState: model.ExamSittingOpen, AppliedSittingRevision: 1, CreatedAt: now, UpdatedAt: now, Revision: 2}
	grants := &grantStoreFake{current: successor, all: map[model.ExecutionGrantID]*model.ExecutionGrant{
		old.ID: old, successor.ID: successor,
	}, events: &events}
	service, err := New(grants, hostsFake{events: &events}, contentFake{}, func() time.Time { return now.Add(time.Second) }, model.NewExecutionGrantID)
	if err != nil {
		t.Fatal(err)
	}
	if err := service.ReleaseGrant(context.Background(), old.ID); err != nil {
		t.Fatal(err)
	}
	want := []string{"release:runner-old", "revoke:runner-old", "revoked:runner-old"}
	if !reflect.DeepEqual(events, want) || grants.current == nil || grants.current.ID != successor.ID ||
		grants.current.State != model.ExecutionGrantReady {
		t.Fatalf("events/current = %v/%#v; want %v/successor ready", events, grants.current, want)
	}
}

func TestEnsureRejectsUnavailableCatalog(t *testing.T) {
	t.Parallel()
	service, err := New(&grantStoreFake{events: &[]string{}}, hostsFake{}, contentFake{}, time.Now, model.NewExecutionGrantID)
	if err != nil {
		t.Fatal(err)
	}
	_, err = service.Ensure(context.Background(), Request{AttemptID: model.NewExamAttemptID(), Image: "go", Network: NetworkNone})
	if !errors.Is(err, ErrUnavailable) {
		t.Fatalf("Ensure() = %v, want unavailable", err)
	}
}

func TestImagesReturnsOnlySafeUsableIsolatedCatalogUnion(t *testing.T) {
	t.Parallel()
	service, err := New(&grantStoreFake{events: &[]string{}}, hostsFake{events: &[]string{}, catalog: []HostStatus{
		{ID: "b", Usable: true, Isolated: true, Images: []string{"python", "go"}, Networks: []Network{NetworkNone}},
		{ID: "a", Usable: true, Isolated: true, Images: []string{"go"}, Networks: []Network{NetworkNone, NetworkAllowlist}},
		{ID: "unsafe", Usable: true, Isolated: false, Images: []string{"hidden"}, Networks: []Network{NetworkNone}},
	}}, contentFake{}, time.Now, model.NewExecutionGrantID)
	if err != nil {
		t.Fatal(err)
	}
	images, err := service.Images(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(images) != 2 || images[0].ID != "go" || images[1].ID != "python" ||
		len(images[0].Networks) != 2 || len(images[1].Networks) != 1 {
		t.Fatalf("Images() = %#v", images)
	}
}

func TestSyncChangeUsesIncrementalApplyWithoutReplacingTree(t *testing.T) {
	t.Parallel()
	attemptID, grantID := model.NewExamAttemptID(), model.NewExecutionGrantID()
	at := time.Now().UTC()
	events := []string{}
	grants := &grantStoreFake{events: &events, current: &model.ExecutionGrant{ID: grantID, AttemptID: attemptID,
		HostID: "runner", Image: "go", Network: model.ExecutionNetworkNone, State: model.ExecutionGrantReady,
		AppliedSittingState: model.ExamSittingOpen, AppliedSittingRevision: 1,
		CreatedAt: at, UpdatedAt: at, Revision: 2}}
	service, err := New(grants, hostsFake{events: &events, catalog: []HostStatus{}}, contentFake{}, time.Now, model.NewExecutionGrantID)
	if err != nil {
		t.Fatal(err)
	}
	change := model.AttemptWorkspaceJournalEntry{WorkspaceID: model.NewExamAttemptWorkspaceID(), Cursor: 1,
		EntryID: model.NewAttemptWorkspaceEntryID(), EntryKind: model.StarterWorkspaceEntryDirectory,
		Operation: model.AttemptWorkspaceMutationCreateDirectory, NewPath: "src", ChangedAt: at}
	if err := service.SyncChange(context.Background(), attemptID, change); err != nil {
		t.Fatal(err)
	}
	if strings.Join(events, ",") != "ensure:runner,apply:runner" {
		t.Fatalf("events = %v", events)
	}
}

func TestEnsureReusesCurrentHostWhenItHasNoFreeSlots(t *testing.T) {
	t.Parallel()
	var events []string
	attemptID := model.NewExamAttemptID()
	grant := &model.ExecutionGrant{
		ID: model.NewExecutionGrantID(), AttemptID: attemptID, HostID: "runner-a", Image: "go",
		Network: model.ExecutionNetworkNone, State: model.ExecutionGrantReady,
		AppliedSittingState: model.ExamSittingOpen, AppliedSittingRevision: 1, Revision: 2,
	}
	grants := &grantStoreFake{events: &events, current: grant}
	hosts := hostsFake{events: &events, catalog: []HostStatus{{
		ID: "runner-a", Usable: true, Isolated: true, Images: []string{"go"},
		Networks: []Network{NetworkNone}, Slots: 0,
	}}}
	service, err := New(grants, hosts, contentFake{}, time.Now, model.NewExecutionGrantID)
	if err != nil {
		t.Fatal(err)
	}
	placement, err := service.Ensure(context.Background(), Request{AttemptID: attemptID, Image: "go", Network: NetworkNone})
	if err != nil {
		t.Fatal(err)
	}
	if placement.HostID != "runner-a" || !placement.Ready {
		t.Fatalf("placement = %#v", placement)
	}
	want := []string{"ensure:runner-a", "tree:runner-a"}
	if len(events) != len(want) {
		t.Fatalf("events = %v, want %v", events, want)
	}
	for index := range want {
		if events[index] != want[index] {
			t.Fatalf("events = %v, want %v", events, want)
		}
	}
}

func TestAuthoritativeTreeVerifiesPinnedContent(t *testing.T) {
	t.Parallel()
	body := "package main\n"
	digest := sha256.Sum256([]byte(body))
	grants := &grantStoreFake{events: &[]string{}, snapshot: &store.ExecutionWorkspaceSnapshot{Nodes: []store.ExecutionWorkspaceNode{{
		Kind: model.StarterWorkspaceEntryFile, Path: "main.go", ContentVersion: model.NewWorkspaceContentVersion(),
		SizeBytes: int64(len(body)), SHA256: hex.EncodeToString(digest[:]), StorageOrigin: model.AttemptWorkspaceStorageAttempt,
		AttemptObjectID: model.NewAttemptWorkspaceObjectID(),
	}}}}
	service, err := New(grants, hostsFake{}, bodyContentFake{body: body}, time.Now, model.NewExecutionGrantID)
	if err != nil {
		t.Fatal(err)
	}
	tree, err := service.authoritativeTree(context.Background(), model.NewExamAttemptID())
	if err != nil {
		t.Fatal(err)
	}
	if len(tree) != 1 || tree[0].Path != "main.go" || string(tree[0].Data) != body {
		t.Fatalf("authoritativeTree() = %#v", tree)
	}
	grants.snapshot.Nodes[0].SHA256 = strings.Repeat("0", 64)
	if _, err := service.authoritativeTree(context.Background(), model.NewExamAttemptID()); !errors.Is(err, ErrInvalid) {
		t.Fatalf("authoritativeTree(corrupt) = %v, want invalid", err)
	}
}
