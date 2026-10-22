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
	"time"

	"github.com/sudosylabs/proctor/server/app/apperror"
	"github.com/sudosylabs/proctor/server/app/invocation"
	"github.com/sudosylabs/proctor/server/model"
)

// mutationAttempt describes the safe, application-owned audit context for one
// named Store mutation. Authorization, candidate preparation, and safe audit
// projections are complete before the runner receives this value.
type mutationAttempt struct {
	Invocation invocation.Invocation
	Action     model.Action
	Resource   model.Resource
	ScopeType  model.RoleScopeType
	ScopeID    string
	Operation  string
	Value      map[string]any
	Prior      map[string]any
}

type scopedMutationAuditor interface {
	BeginAtScope(
		context.Context, invocation.Invocation, model.Action, model.Resource,
		model.RoleScopeType, string, string, map[string]any, map[string]any,
	) (model.AuditEventID, error)
}

// mutationAttemptReference is the complete audit input a named Store mutation
// needs to commit durable state and successful audit completion atomically.
type mutationAttemptReference struct {
	ID         model.AuditEventID
	MutationAt time.Time
}

// runAuditedMutation owns the common attempt and failure protocol. Successful
// completion remains inside the named Store mutation; post-commit effects
// remain with the calling use case. The mapper may preserve contextual wrappers
// but must return an error chain containing an application Error.
func runAuditedMutation[T any](
	ctx context.Context,
	auditor mutationAuditor,
	attempt mutationAttempt,
	now func() time.Time,
	mutate func(context.Context, mutationAttemptReference) (T, error),
	mapError func(error) error,
) (T, error) {
	var zero T
	var auditID model.AuditEventID
	var err error
	if attempt.ScopeType != "" || attempt.ScopeID != "" {
		scoped, ok := auditor.(scopedMutationAuditor)
		if !ok || !attempt.ScopeType.IsValid() || !model.IsValidOpaqueID(attempt.ScopeID) {
			return zero, apperror.New("audit.internal")
		}
		auditID, err = scoped.BeginAtScope(
			ctx, attempt.Invocation, attempt.Action, attempt.Resource,
			attempt.ScopeType, attempt.ScopeID, attempt.Operation,
			attempt.Value, attempt.Prior,
		)
	} else {
		auditID, err = auditor.Begin(
			ctx,
			attempt.Invocation,
			attempt.Action,
			attempt.Resource,
			attempt.Operation,
			attempt.Value,
			attempt.Prior,
		)
	}
	if err != nil {
		return zero, err
	}

	result, err := mutate(ctx, mutationAttemptReference{
		ID:         auditID,
		MutationAt: model.TimeUTC(now()),
	})
	if err == nil {
		return result, nil
	}

	mapped := mapError(err)
	failure, ok := apperror.As(mapped)
	if !ok {
		mapped = apperror.New("audit.internal").Wrap(
			errors.New("mutation error mapper returned no application error"),
		)
		failure, _ = apperror.As(mapped)
	}
	if auditErr := auditor.Fail(ctx, auditID, failure.Code()); auditErr != nil {
		return zero, errors.Join(mapped, auditErr)
	}
	return zero, mapped
}
