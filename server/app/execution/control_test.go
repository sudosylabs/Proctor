// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package execution

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

type controlTestStore struct {
	*grantStoreFake
	prepared       []*model.ExecutionGrant
	acknowledgeErr error
	calls          *[]string
}

func (s *controlTestStore) PrepareControl(_ context.Context, id model.ExecutionGrantID, epoch string, _ time.Time) (*model.ExecutionGrant, error) {
	*s.calls = append(*s.calls, "prepare")
	if len(s.prepared) == 0 {
		return nil, ErrUnavailable
	}
	value := *s.prepared[0]
	if len(s.prepared) > 1 {
		s.prepared = s.prepared[1:]
	}
	if value.ID != id || value.EnvironmentEpoch != epoch {
		return nil, ErrConflict
	}
	return &value, nil
}
func (s *controlTestStore) AcknowledgeControl(_ context.Context, fence model.ExecutionFence, state model.ExecutionControlState, _ time.Time) (*model.ExecutionGrant, error) {
	*s.calls = append(*s.calls, "acknowledge")
	if s.acknowledgeErr != nil {
		err := s.acknowledgeErr
		s.acknowledgeErr = nil
		return nil, err
	}
	value := *s.prepared[0]
	if value.Fence() != fence || value.DesiredControlState != state {
		return nil, ErrConflict
	}
	value.ControlAcknowledgedRevision = fence.ControlRevision
	return &value, nil
}

type controlTestEnvironment struct {
	Environment
	epoch       string
	calls       *[]string
	requests    []model.ExecutionFence
	states      []model.ExecutionControlState
	failure     error
	receiptEdit func(*ControlReceipt)
}

func (e *controlTestEnvironment) Epoch() string { return e.epoch }
func (e *controlTestEnvironment) Control(_ context.Context, f model.ExecutionFence, state model.ExecutionControlState) (ControlReceipt, error) {
	*e.calls = append(*e.calls, "host")
	e.requests = append(e.requests, f)
	e.states = append(e.states, state)
	receipt := ControlReceipt{Fence: f, State: state, Confirmed: true}
	if e.receiptEdit != nil {
		e.receiptEdit(&receipt)
	}
	return receipt, e.failure
}

type controlTestLease struct{}

func (controlTestLease) Validate(context.Context) error { return nil }
func (controlTestLease) Release(context.Context) error  { return nil }

func TestExecutionControlOrdersDurabilityAndReconcilesNewAuthority(t *testing.T) {
	t.Parallel()
	for _, scenario := range []string{"lost host response", "new authority during acknowledgement", "confirmed retry"} {
		t.Run(scenario, func(t *testing.T) {
			now := time.Now().UTC()
			initial := &model.ExecutionGrant{ID: model.NewExecutionGrantID(), AttemptID: model.NewExamAttemptID(), EnvironmentEpoch: "epoch", ControlRevision: 7, DesiredControlState: model.ExecutionControlFrozen}
			var calls []string
			grants := &controlTestStore{prepared: []*model.ExecutionGrant{initial}, calls: &calls}
			host := &controlTestEnvironment{epoch: "epoch", calls: &calls}
			want := []string{"prepare", "host", "acknowledge"}
			switch scenario {
			case "lost host response":
				host.failure = context.DeadlineExceeded
				want = []string{"prepare", "host"}
			case "new authority during acknowledgement":
				successor := *initial
				successor.ControlRevision++
				successor.DesiredControlState = model.ExecutionControlRunning
				grants.prepared = append(grants.prepared, &successor)
				grants.acknowledgeErr = store.NewErrConflict("execution_grant", "control_acknowledgement", nil)
				want = []string{"prepare", "host", "acknowledge", "prepare", "host", "acknowledge"}
			case "confirmed retry":
				initial.ControlAcknowledgedRevision = initial.ControlRevision
				want = []string{"prepare"}
			}
			service := &Service{grants: grants, now: func() time.Time { return now }}
			value, err := service.controlEnvironment(context.Background(), initial, host, controlTestLease{})
			if scenario == "lost host response" {
				if !errors.Is(err, context.DeadlineExceeded) || value.ControlAcknowledgedRevision != 0 {
					t.Fatalf("lost response became confirmed: %#v %v", value, err)
				}
			} else if err != nil || value.ControlAcknowledgedRevision != value.ControlRevision {
				t.Fatalf("control failed: %#v %v", value, err)
			}
			if !reflect.DeepEqual(calls, want) {
				t.Fatalf("calls %v; want %v", calls, want)
			}
			if scenario == "new authority during acknowledgement" && (len(host.requests) != 2 || host.requests[1].ControlRevision <= host.requests[0].ControlRevision || host.requests[1].EnvironmentEpoch != host.requests[0].EnvironmentEpoch || host.states[1] != model.ExecutionControlRunning) {
				t.Fatal("recovery did not preserve occupancy with a newer fence")
			}
		})
	}
}

func TestExecutionControlRejectsUnconfirmedOrForeignHostReceipts(t *testing.T) {
	t.Parallel()
	for _, kind := range []string{"unconfirmed", "epoch", "revision", "state"} {
		t.Run(kind, func(t *testing.T) {
			var calls, events []string
			initial := &model.ExecutionGrant{ID: model.NewExecutionGrantID(), AttemptID: model.NewExamAttemptID(), HostID: "host", EnvironmentEpoch: "epoch", ControlRevision: 7, DesiredControlState: model.ExecutionControlFrozen}
			base := &grantStoreFake{current: initial, all: map[model.ExecutionGrantID]*model.ExecutionGrant{initial.ID: initial}, events: &events}
			grants := &controlTestStore{grantStoreFake: base, prepared: []*model.ExecutionGrant{initial}, calls: &calls}
			host := &controlTestEnvironment{epoch: "epoch", calls: &calls, receiptEdit: func(r *ControlReceipt) {
				switch kind {
				case "unconfirmed":
					r.Confirmed = false
				case "epoch":
					r.Fence.EnvironmentEpoch = "foreign"
				case "revision":
					r.Fence.ControlRevision++
				case "state":
					r.State = model.ExecutionControlRunning
				}
			}}
			service := &Service{grants: grants, hosts: hostsFake{events: &events}, now: time.Now}
			if _, err := service.controlEnvironment(context.Background(), initial, host, controlTestLease{}); !errors.Is(err, ErrConflict) {
				t.Fatalf("accepted receipt: %v", err)
			}
			if !reflect.DeepEqual(calls, []string{"prepare", "host"}) {
				t.Fatalf("bad receipt acknowledged: %v", calls)
			}
			if initial.State != model.ExecutionGrantReleased {
				t.Fatal("untrustworthy host retained usable grant")
			}
		})
	}
}
