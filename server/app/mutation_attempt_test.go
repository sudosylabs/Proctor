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
	"reflect"
	"testing"
	"time"

	"github.com/sudosylabs/proctor/server/model"
)

type mutationAttemptAuditorFake struct {
	events   *[]string
	beginID  string
	beginIDs []string
	beginErr error
	failErr  error
	failID   string
	failCode string
	attempt  mutationAttempt
	attempts []mutationAttempt
}

func (a *mutationAttemptAuditorFake) BeginAtScope(
	_ context.Context,
	invocation Invocation,
	action model.Action,
	resource model.Resource,
	scopeType model.RoleScopeType,
	scopeID string,
	operation string,
	value map[string]any,
	prior map[string]any,
) (string, error) {
	*a.events = append(*a.events, "begin-at-scope")
	a.attempt = mutationAttempt{
		Invocation: invocation,
		Action:     action,
		Resource:   resource,
		ScopeType:  scopeType,
		ScopeID:    scopeID,
		Operation:  operation,
		Value:      value,
		Prior:      prior,
	}
	a.attempts = append(a.attempts, a.attempt)
	return a.nextBeginID(), a.beginErr
}

func (a *mutationAttemptAuditorFake) Begin(
	_ context.Context,
	invocation Invocation,
	action model.Action,
	resource model.Resource,
	operation string,
	value map[string]any,
	prior map[string]any,
) (string, error) {
	*a.events = append(*a.events, "begin")
	a.attempt = mutationAttempt{
		Invocation: invocation,
		Action:     action,
		Resource:   resource,
		Operation:  operation,
		Value:      value,
		Prior:      prior,
	}
	a.attempts = append(a.attempts, a.attempt)
	return a.nextBeginID(), a.beginErr
}

func (a *mutationAttemptAuditorFake) nextBeginID() string {
	if len(a.beginIDs) == 0 {
		return a.beginID
	}
	id := a.beginIDs[0]
	a.beginIDs = a.beginIDs[1:]
	return id
}

func (a *mutationAttemptAuditorFake) Fail(_ context.Context, id, code string) error {
	*a.events = append(*a.events, "fail")
	a.failID = id
	a.failCode = code
	return a.failErr
}

func TestMutationAttemptBeginFailurePreventsMutation(t *testing.T) {
	t.Parallel()

	events := []string{}
	beginErr := NewError("audit.unavailable")
	auditor := &mutationAttemptAuditorFake{
		events: &events, beginID: model.NewId(), beginErr: beginErr,
	}
	mutated := false
	_, err := runAuditedMutation(
		context.Background(), auditor,
		mutationAttempt{
			Action:    model.ActionAcademicUnitManage,
			Resource:  model.Resource{Type: model.ResourceAcademicUnit, ID: model.NewId()},
			Operation: "create",
		},
		time.Now,
		func(context.Context, mutationAttemptReference) (string, error) {
			mutated = true
			return "", nil
		},
		func(err error) error { return NewError("programme.conflict").Wrap(err) },
	)
	if err != beginErr {
		t.Fatalf("runAuditedMutation() error = %v, want begin error", err)
	}
	if mutated {
		t.Fatal("named mutation ran after begin failure")
	}
	if !reflect.DeepEqual(events, []string{"begin"}) {
		t.Fatalf("events = %v", events)
	}
}

func TestMutationAttemptUsesIndependentAuditScope(t *testing.T) {
	t.Parallel()

	events := []string{}
	auditID := model.NewId()
	scopeID := model.NewId()
	resource := model.Resource{Type: model.ResourceAcademicPeriod, ID: model.NewId()}
	auditor := &mutationAttemptAuditorFake{events: &events, beginID: auditID}
	got, err := runAuditedMutation(
		context.Background(), auditor,
		mutationAttempt{
			Action: model.ActionAcademicPeriodManage, Resource: resource,
			ScopeType: model.RoleScopeAcademicUnit, ScopeID: scopeID,
			Operation: "create",
		},
		time.Now,
		func(_ context.Context, reference mutationAttemptReference) (string, error) {
			events = append(events, "mutate")
			return reference.ID, nil
		},
		func(error) error { return nil },
	)
	if err != nil || got != auditID {
		t.Fatalf("runAuditedMutation() = %q, %v", got, err)
	}
	if auditor.attempt.Resource != resource || auditor.attempt.ScopeType != model.RoleScopeAcademicUnit || auditor.attempt.ScopeID != scopeID {
		t.Fatalf("scoped attempt = %#v", auditor.attempt)
	}
	if !reflect.DeepEqual(events, []string{"begin-at-scope", "mutate"}) {
		t.Fatalf("events = %v", events)
	}
}

func TestMutationAttemptMapsStoreFailureBeforeCompletingAttempt(t *testing.T) {
	t.Parallel()

	events := []string{}
	auditID := model.NewId()
	storeErr := errors.New("store failed")
	mapped := NewError("programme.conflict").Wrap(storeErr)
	auditor := &mutationAttemptAuditorFake{events: &events, beginID: auditID}
	at := time.UnixMilli(500)
	_, err := runAuditedMutation(
		context.Background(), auditor,
		mutationAttempt{
			Action:    model.ActionAcademicUnitManage,
			Resource:  model.Resource{Type: model.ResourceAcademicUnit, ID: model.NewId()},
			Operation: "patch",
		},
		func() time.Time { return at },
		func(_ context.Context, reference mutationAttemptReference) (string, error) {
			events = append(events, "mutate")
			if reference.ID != auditID || reference.MutationAtMillis != at.UnixMilli() {
				t.Fatalf("reference = %#v", reference)
			}
			return "", storeErr
		},
		func(err error) error {
			events = append(events, "map")
			if err != storeErr {
				t.Fatalf("mapped error = %v", err)
			}
			return mapped
		},
	)
	if err != mapped {
		t.Fatalf("runAuditedMutation() error = %v, want mapped error", err)
	}
	if auditor.failID != auditID || auditor.failCode != mapped.Code() {
		t.Fatalf("failure completion = id %q code %q", auditor.failID, auditor.failCode)
	}
	if !reflect.DeepEqual(events, []string{"begin", "mutate", "map", "fail"}) {
		t.Fatalf("events = %v", events)
	}
}

func TestMutationAttemptFailureCompletionErrorWins(t *testing.T) {
	t.Parallel()

	events := []string{}
	storeErr := errors.New("store failed")
	auditErr := NewError("audit.unavailable")
	auditor := &mutationAttemptAuditorFake{
		events: &events, beginID: model.NewId(), failErr: auditErr,
	}
	_, err := runAuditedMutation(
		context.Background(), auditor,
		mutationAttempt{
			Action:    model.ActionAcademicUnitManage,
			Resource:  model.Resource{Type: model.ResourceAcademicUnit, ID: model.NewId()},
			Operation: "archive",
		},
		time.Now,
		func(context.Context, mutationAttemptReference) (struct{}, error) {
			return struct{}{}, storeErr
		},
		func(err error) error { return NewError("programme.conflict").Wrap(err) },
	)
	if err != auditErr {
		t.Fatalf("runAuditedMutation() error = %v, want audit error", err)
	}
}

func TestMutationAttemptRejectsInvalidMappedError(t *testing.T) {
	t.Parallel()

	for name, mapped := range map[string]error{
		"nil":   nil,
		"plain": errors.New("unmapped store failure"),
	} {
		t.Run(name, func(t *testing.T) {
			events := []string{}
			auditor := &mutationAttemptAuditorFake{events: &events, beginID: model.NewId()}
			_, err := runAuditedMutation(
				context.Background(), auditor,
				mutationAttempt{
					Action:    model.ActionAcademicUnitManage,
					Resource:  model.Resource{Type: model.ResourceAcademicUnit, ID: model.NewId()},
					Operation: "create",
				},
				time.Now,
				func(context.Context, mutationAttemptReference) (struct{}, error) {
					return struct{}{}, errors.New("store failed")
				},
				func(error) error { return mapped },
			)
			appErr, ok := As(err)
			if !ok || appErr.Code() != "audit.event.invalid" {
				t.Fatalf("runAuditedMutation() error = %v", err)
			}
			if auditor.failCode != "audit.event.invalid" {
				t.Fatalf("failure code = %q", auditor.failCode)
			}
		})
	}
}

func TestMutationAttemptPreservesMappedErrorWrapper(t *testing.T) {
	t.Parallel()

	events := []string{}
	auditor := &mutationAttemptAuditorFake{events: &events, beginID: model.NewId()}
	appFailure := NewError("role.conflict")
	mapped := fmt.Errorf("persistence context: %w", appFailure)
	_, err := runAuditedMutation(
		context.Background(), auditor,
		mutationAttempt{
			Action:    model.ActionRoleManage,
			Resource:  model.Resource{Type: model.ResourceInstitution, ID: model.NewId()},
			Operation: "patch",
		},
		time.Now,
		func(context.Context, mutationAttemptReference) (struct{}, error) {
			return struct{}{}, errors.New("store failed")
		},
		func(error) error { return mapped },
	)
	if err != mapped {
		t.Fatalf("runAuditedMutation() error = %v, want original mapped wrapper", err)
	}
	if auditor.failCode != appFailure.Code() {
		t.Fatalf("failure code = %q", auditor.failCode)
	}
}

func TestMutationAttemptReturnsSuccessWithoutCompletingIt(t *testing.T) {
	t.Parallel()

	events := []string{}
	auditID := model.NewId()
	resource := model.Resource{Type: model.ResourceAcademicUnit, ID: model.NewId()}
	value := map[string]any{"name": "computer-science"}
	prior := map[string]any{"name": "computing"}
	invocation := Invocation{}
	auditor := &mutationAttemptAuditorFake{events: &events, beginID: auditID}
	clockCalls := 0
	got, err := runAuditedMutation(
		context.Background(), auditor,
		mutationAttempt{
			Invocation: invocation,
			Action:     model.ActionAcademicUnitManage,
			Resource:   resource,
			Operation:  "patch",
			Value:      value,
			Prior:      prior,
		},
		func() time.Time {
			clockCalls++
			return time.UnixMilli(500)
		},
		func(_ context.Context, reference mutationAttemptReference) (string, error) {
			events = append(events, "mutate")
			if reference.ID != auditID || reference.MutationAtMillis != 500 {
				t.Fatalf("reference = %#v", reference)
			}
			return "saved", nil
		},
		func(error) error {
			t.Fatal("error mapper called on success")
			return nil
		},
	)
	if err != nil || got != "saved" {
		t.Fatalf("runAuditedMutation() = %q, %v", got, err)
	}
	if !reflect.DeepEqual(auditor.attempt.Invocation, invocation) || auditor.attempt.Action != model.ActionAcademicUnitManage ||
		auditor.attempt.Resource != resource || auditor.attempt.Operation != "patch" ||
		!reflect.DeepEqual(auditor.attempt.Value, value) || !reflect.DeepEqual(auditor.attempt.Prior, prior) {
		t.Fatalf("attempt = %#v", auditor.attempt)
	}
	if clockCalls != 1 {
		t.Fatalf("clock calls = %d, want 1", clockCalls)
	}
	if !reflect.DeepEqual(events, []string{"begin", "mutate"}) {
		t.Fatalf("events = %v", events)
	}
}
