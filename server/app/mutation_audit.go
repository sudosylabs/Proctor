// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package app

import (
	"context"

	"github.com/sudosylabs/proctor/server/app/invocation"
	"github.com/sudosylabs/proctor/server/model"
)

type mutationAuditor interface {
	Begin(
		context.Context, invocation.Invocation, model.Action, model.Resource, string,
		map[string]any, map[string]any,
	) (model.AuditEventID, error)
	Fail(context.Context, model.AuditEventID, string) error
}

// mutationAuditAdapter is the one Invocation-shaped view of the audit service.
// Every parent use case and Exam child receives it directly for
// principal-attributed mutation audit; it never wraps another adapter.
type mutationAuditAdapter struct{ audit *auditService }

func (a mutationAuditAdapter) Begin(ctx context.Context, invocation invocation.Invocation, action model.Action, resource model.Resource, operation string, value, prior map[string]any) (model.AuditEventID, error) {
	event, appErr := a.audit.BeginCriticalAction(
		ctx, invocation.Principal(), action, resource,
		invocation.RequestMetadata(),
		map[string]any{"operation": operation, "value": value}, prior,
	)
	if appErr != nil {
		return "", appErr
	}
	return event.ID, nil
}

func (a mutationAuditAdapter) BeginAtScope(ctx context.Context, invocation invocation.Invocation, action model.Action, resource model.Resource, scopeType model.RoleScopeType, scopeID, operation string, value, prior map[string]any) (model.AuditEventID, error) {
	event, appErr := a.audit.BeginCriticalActionAtScope(
		ctx, invocation.Principal(), action, resource, scopeType, scopeID,
		invocation.RequestMetadata(),
		map[string]any{"operation": operation, "value": value}, prior,
	)
	if appErr != nil {
		return "", appErr
	}
	return event.ID, nil
}

func (a mutationAuditAdapter) Fail(ctx context.Context, auditID model.AuditEventID, errorCode string) error {
	_, appErr := a.audit.CompleteCriticalAction(
		ctx, auditID, model.AuditStatusFail, errorCode, nil,
	)
	return appErr
}

func (a mutationAuditAdapter) PrepareAtScope(_ context.Context, invocation invocation.Invocation, action model.Action,
	resource model.Resource, scopeType model.RoleScopeType, scopeID, operation string, value map[string]any,
) (*model.AuditEvent, error) {
	return a.audit.prepareCriticalActionAtScope(invocation.Principal(), action, resource, scopeType, scopeID,
		invocation.RequestMetadata(), map[string]any{"operation": operation, "value": value}, nil)
}

func (a mutationAuditAdapter) RecordAuthorizationDecision(ctx context.Context, invocation invocation.Invocation,
	action model.Action, resource model.Resource, scopeType model.RoleScopeType, scopeID string, allowed bool,
) error {
	return a.audit.RecordAuthorizationDecision(ctx, invocation.Principal(), action, resource, scopeType, scopeID,
		invocation.RequestMetadata(), allowed)
}

func (a mutationAuditAdapter) Complete(ctx context.Context, auditID model.AuditEventID, value map[string]any) error {
	_, err := a.audit.CompleteCriticalAction(ctx, auditID, model.AuditStatusSuccess, "", value)
	return err
}
