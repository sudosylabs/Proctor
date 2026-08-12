// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/sudosylabs/proctor/server/model"
)

type mutationAttemptAuditorFake struct {
	events   *[]string
	beginID  string
	beginErr error
	failErr  error
	failID   string
	failCode string
	attempt  mutationAttempt
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
	return a.beginID, a.beginErr
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
		func(err error) *Error { return NewError("programme.conflict").Wrap(err) },
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
			if reference.ID != auditID || reference.At != at.UnixMilli() {
				t.Fatalf("reference = %#v", reference)
			}
			return "", storeErr
		},
		func(err error) *Error {
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
		func(err error) *Error { return NewError("programme.conflict").Wrap(err) },
	)
	if err != auditErr {
		t.Fatalf("runAuditedMutation() error = %v, want audit error", err)
	}
}

func TestMutationAttemptRejectsNilMappedError(t *testing.T) {
	t.Parallel()

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
		func(error) *Error { return nil },
	)
	appErr, ok := As(err)
	if !ok || appErr.Code() != "audit.event.invalid" {
		t.Fatalf("runAuditedMutation() error = %v", err)
	}
	if auditor.failCode != "audit.event.invalid" {
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
			if reference.ID != auditID || reference.At != 500 {
				t.Fatalf("reference = %#v", reference)
			}
			return "saved", nil
		},
		func(error) *Error {
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
