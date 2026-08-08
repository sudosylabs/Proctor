// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"context"

	"github.com/sudosylabs/proctor/server/model"
)

type mutationAuditor interface {
	Begin(
		context.Context, Invocation, model.Action, model.Resource, string,
		map[string]any, map[string]any,
	) (string, error)
	Fail(context.Context, string, string) error
}

type mutationAuditAdapter struct{ audit *AuditService }

func (a mutationAuditAdapter) Begin(
	ctx context.Context,
	invocation Invocation,
	action model.Action,
	resource model.Resource,
	operation string,
	value map[string]any,
	prior map[string]any,
) (string, error) {
	event, appErr := a.audit.BeginCriticalAction(
		ctx, invocation.Principal(), action, resource,
		invocation.RequestMetadata(),
		map[string]any{"operation": operation, "value": value}, prior,
	)
	if appErr != nil {
		return "", appErr
	}
	return event.ID.String(), nil
}

func (a mutationAuditAdapter) Fail(
	ctx context.Context,
	auditID string,
	errorCode string,
) error {
	_, appErr := a.audit.CompleteCriticalAction(
		ctx, auditID, model.AuditStatusFail, errorCode, nil,
	)
	return appErr
}
