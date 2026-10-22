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
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/server/app/apperror"
	"github.com/sudosylabs/proctor/server/app/invocation"
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

func (a *mutationAttemptAuditorFake) BeginAtScope(_ context.Context, invocation invocation.Invocation, action model.Action, resource model.Resource, scopeType model.RoleScopeType, scopeID, operation string, value, prior map[string]any) (model.AuditEventID, error) {
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
	return model.AuditEventID(a.nextBeginID()), a.beginErr
}

func (a *mutationAttemptAuditorFake) Begin(_ context.Context, invocation invocation.Invocation, action model.Action, resource model.Resource, operation string, value, prior map[string]any) (model.AuditEventID, error) {
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
	return model.AuditEventID(a.nextBeginID()), a.beginErr
}

func (a *mutationAttemptAuditorFake) nextBeginID() string {
	if len(a.beginIDs) == 0 {
		return a.beginID
	}
	id := a.beginIDs[0]
	a.beginIDs = a.beginIDs[1:]
	return id
}

func (a *mutationAttemptAuditorFake) Fail(_ context.Context, id model.AuditEventID, code string) error {
	*a.events = append(*a.events, "fail")
	a.failID = id.String()
	a.failCode = code
	return a.failErr
}

func TestMutationAttemptBeginFailurePreventsMutation(t *testing.T) {
	t.Parallel()

	events := []string{}
	beginErr := apperror.New("audit.unavailable")
	auditor := &mutationAttemptAuditorFake{
		events: &events, beginID: model.NewID(), beginErr: beginErr,
	}
	mutated := false
	_, err := runAuditedMutation(
		context.Background(), auditor,
		mutationAttempt{
			Action:    model.ActionAcademicUnitManage,
			Resource:  model.Resource{Type: model.ResourceAcademicUnit, ID: model.NewID()},
			Operation: "create",
		},
		time.Now,
		func(context.Context, mutationAttemptReference) (string, error) {
			mutated = true
			return "", nil
		},
		func(err error) error { return apperror.New("programme.conflict").Wrap(err) },
	)
	require.Equal(t, err, beginErr, "runAuditedMutation() error = %v, want begin error", err)
	require.False(t, mutated, "named mutation ran after begin failure")
	require.Equal(t, []string{"begin"}, events)
}

func TestMutationAttemptUsesIndependentAuditScope(t *testing.T) {
	t.Parallel()

	events := []string{}
	auditID := model.NewID()
	scopeID := model.NewID()
	resource := model.Resource{Type: model.ResourceAcademicPeriod, ID: model.NewID()}
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
			return reference.ID.String(), nil
		},
		func(error) error { return nil },
	)
	require.NoError(t, err, "runAuditedMutation()")
	require.Equal(t, auditID, got, "runAuditedMutation()")
	require.Equal(t, resource, auditor.attempt.Resource)
	require.Equal(t, model.RoleScopeAcademicUnit, auditor.attempt.ScopeType)
	require.Equal(t, scopeID, auditor.attempt.ScopeID)
	// Contract (guardrail 8): the audit begins before the durable write.
	require.Equal(t, []string{"begin-at-scope", "mutate"}, events)
}

func TestMutationAttemptMapsStoreFailureBeforeCompletingAttempt(t *testing.T) {
	t.Parallel()

	events := []string{}
	auditID := model.NewID()
	storeErr := errors.New("store failed")
	mapped := apperror.New("programme.conflict").Wrap(storeErr)
	auditor := &mutationAttemptAuditorFake{events: &events, beginID: auditID}
	at := time.UnixMilli(500)
	_, err := runAuditedMutation(
		context.Background(), auditor,
		mutationAttempt{
			Action:    model.ActionAcademicUnitManage,
			Resource:  model.Resource{Type: model.ResourceAcademicUnit, ID: model.NewID()},
			Operation: "patch",
		},
		func() time.Time { return at },
		func(_ context.Context, reference mutationAttemptReference) (string, error) {
			events = append(events, "mutate")
			require.Equal(t, model.AuditEventID(auditID), reference.ID)
			require.True(t, model.TimeUTC(at).Equal(reference.MutationAt))
			return "", storeErr
		},
		func(err error) error {
			events = append(events, "map")
			require.Equal(t, storeErr, err)
			return mapped
		},
	)
	require.Equal(t, err, mapped, "runAuditedMutation() error = %v, want mapped error", err)
	require.Equal(t, auditID, auditor.failID, "failure completion = id")
	require.Equal(t, mapped.Code(), auditor.failCode, "failure completion = id")
	// Contract (guardrail 8): the audit begins before the durable write, and the
	// audit outcome follows the write.
	require.Equal(t, []string{"begin", "mutate", "map", "fail"}, events)
}

func TestMutationAttemptFailureCompletionErrorJoinsOriginalFailure(t *testing.T) {
	t.Parallel()

	events := []string{}
	storeErr := errors.New("store failed")
	auditErr := apperror.New("audit.unavailable")
	auditor := &mutationAttemptAuditorFake{
		events: &events, beginID: model.NewID(), failErr: auditErr,
	}
	_, err := runAuditedMutation(
		context.Background(), auditor,
		mutationAttempt{
			Action:    model.ActionAcademicUnitManage,
			Resource:  model.Resource{Type: model.ResourceAcademicUnit, ID: model.NewID()},
			Operation: "archive",
		},
		time.Now,
		func(context.Context, mutationAttemptReference) (struct{}, error) {
			return struct{}{}, storeErr
		},
		func(err error) error { return apperror.New("programme.conflict").Wrap(err) },
	)
	// The audit failure must stay observable without replacing the original
	// classification that callers and transport mapping resolve first.
	appErr, ok := apperror.As(err)
	require.True(t, ok, "runAuditedMutation() error")
	require.Equal(t, "programme.conflict", appErr.Code(), "runAuditedMutation() error")
	require.ErrorIs(t, err, storeErr, "runAuditedMutation() error")
	require.ErrorIs(t, err, auditErr, "runAuditedMutation() error")
}

func TestMutationAttemptRejectsInvalidMappedError(t *testing.T) {
	t.Parallel()

	for name, mapped := range map[string]error{
		"nil":   nil,
		"plain": errors.New("unmapped store failure"),
	} {
		t.Run(name, func(t *testing.T) {
			events := []string{}
			auditor := &mutationAttemptAuditorFake{events: &events, beginID: model.NewID()}
			_, err := runAuditedMutation(
				context.Background(), auditor,
				mutationAttempt{
					Action:    model.ActionAcademicUnitManage,
					Resource:  model.Resource{Type: model.ResourceAcademicUnit, ID: model.NewID()},
					Operation: "create",
				},
				time.Now,
				func(context.Context, mutationAttemptReference) (struct{}, error) {
					return struct{}{}, errors.New("store failed")
				},
				func(error) error { return mapped },
			)
			appErr, ok := apperror.As(err)
			require.True(t, ok)
			require.Equal(t, "audit.internal", appErr.Code())
			require.Equal(t, "audit.internal", auditor.failCode)
		})
	}
}

func TestMutationAttemptPreservesMappedErrorWrapper(t *testing.T) {
	t.Parallel()

	events := []string{}
	auditor := &mutationAttemptAuditorFake{events: &events, beginID: model.NewID()}
	appFailure := apperror.New("role.conflict")
	mapped := fmt.Errorf("persistence context: %w", appFailure)
	_, err := runAuditedMutation(
		context.Background(), auditor,
		mutationAttempt{
			Action:    model.ActionRoleManage,
			Resource:  model.Resource{Type: model.ResourceInstitution, ID: model.NewID()},
			Operation: "patch",
		},
		time.Now,
		func(context.Context, mutationAttemptReference) (struct{}, error) {
			return struct{}{}, errors.New("store failed")
		},
		func(error) error { return mapped },
	)
	require.Equal(t, mapped, err, "runAuditedMutation() error")
	require.Equal(t, appFailure.Code(), auditor.failCode)
}

func TestMutationAttemptReturnsSuccessWithoutCompletingIt(t *testing.T) {
	t.Parallel()

	events := []string{}
	auditID := model.NewID()
	resource := model.Resource{Type: model.ResourceAcademicUnit, ID: model.NewID()}
	value := map[string]any{"name": "computer-science"}
	prior := map[string]any{"name": "computing"}
	invocation := invocation.Invocation{}
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
			require.Equal(t, model.AuditEventID(auditID), reference.ID)
			require.True(t, time.UnixMilli(500).UTC().Equal(reference.MutationAt))
			return "saved", nil
		},
		func(error) error {
			require.FailNow(t, "error mapper called on success")
			return nil
		},
	)
	require.NoError(t, err, "runAuditedMutation()")
	require.Equal(t, "saved", got, "runAuditedMutation()")
	require.Equal(t, invocation, auditor.attempt.Invocation)
	require.Equal(t, model.ActionAcademicUnitManage, auditor.attempt.Action)
	require.Equal(t, resource, auditor.attempt.Resource)
	require.Equal(t, "patch", auditor.attempt.Operation)
	require.Equal(t, value, auditor.attempt.Value)
	require.Equal(t, prior, auditor.attempt.Prior)
	require.Equal(t, 1, clockCalls, "clock calls")
	// Contract (guardrail 8): the audit begins before the durable write.
	require.Equal(t, []string{"begin", "mutate"}, events)
}
