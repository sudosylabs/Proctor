// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only
//
// Adapted from Mattermost server/channels/app/audit.go and
// server/public/model/audit_record.go. Proctor writes authoritative events to
// PostgreSQL and treats decision-audit failure as a closed security boundary.

package app

import (
	"context"
	"net/http"
	"time"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

type AuditService struct {
	store  store.Store
	nodeID string
	now    func() time.Time
}

func newAuditService(persistence store.Store, nodeID string) *AuditService {
	return &AuditService{store: persistence, nodeID: nodeID, now: time.Now}
}

// BeginCriticalAction persists an attempt before a security-sensitive mutation
// is allowed to start. Callers must pass only bounded safe values or Auditable
// projections; credentials and secrets are never acceptable audit parameters.
func (s *AuditService) BeginCriticalAction(
	ctx context.Context,
	principal model.Principal,
	action model.Action,
	resource model.Resource,
	metadata model.RequestMetadata,
	parameters any,
	priorState any,
) (*model.AuditEvent, *model.AppError) {
	if !principal.IsValid() {
		return nil, invalidTokenError("AuditService.BeginCriticalAction")
	}
	encodedParameters, appErr := model.EncodeAuditData(parameters)
	if appErr != nil {
		return nil, appErr
	}
	encodedPriorState, appErr := model.EncodeAuditData(priorState)
	if appErr != nil {
		return nil, appErr
	}
	event := &model.AuditEvent{
		ActorId: principal.UserId, SessionId: principal.SessionId,
		Action: string(action), Resource: resource,
		ScopeType: model.RoleScopeType(resource.Type), ScopeId: resource.Id,
		Status: model.AuditStatusAttempt, RequestId: metadata.RequestId,
		NodeId: s.nodeID, ClientType: string(principal.ClientType),
		AuthMethod: principal.AuthenticationMethod, IPAddress: metadata.IPAddress,
		UserAgent: metadata.UserAgent, Parameters: encodedParameters,
		PriorState: encodedPriorState,
	}
	if s.store == nil || s.store.Audit() == nil {
		return nil, auditUnavailableError(
			"AuditService.BeginCriticalAction",
			store.NewErrNotFound("audit_store", ""),
		)
	}
	saved, err := s.store.Audit().Save(ctx, event)
	if err != nil {
		return nil, auditUnavailableError("AuditService.BeginCriticalAction", err)
	}
	return saved, nil
}

// CompleteCriticalAction records the terminal outcome. If this fails after the
// mutation committed, the durable attempt remains for operator reconciliation
// and the use case must return the audit failure instead of reporting success.
func (s *AuditService) CompleteCriticalAction(
	ctx context.Context,
	eventID string,
	status model.AuditStatus,
	errorCode string,
	result any,
) (*model.AuditEvent, *model.AppError) {
	if s.store == nil || s.store.Audit() == nil {
		return nil, auditUnavailableError(
			"AuditService.CompleteCriticalAction",
			store.NewErrNotFound("audit_store", ""),
		)
	}
	encodedResult, appErr := model.EncodeAuditData(result)
	if appErr != nil {
		return nil, appErr
	}
	event, err := s.store.Audit().Complete(
		ctx, eventID, status, errorCode, encodedResult, s.now().UnixMilli(),
	)
	if err != nil {
		return nil, auditUnavailableError("AuditService.CompleteCriticalAction", err)
	}
	return event, nil
}

func (s *AuditService) RecordAuthorizationDecision(
	ctx context.Context,
	principal model.Principal,
	action model.Action,
	resource model.Resource,
	metadata model.RequestMetadata,
	allowed bool,
) *model.AppError {
	status := model.AuditStatusFail
	errorCode := "authorization.denied"
	if allowed {
		status = model.AuditStatusSuccess
		errorCode = ""
	}
	scopeType := model.RoleScopeType(resource.Type)
	event := &model.AuditEvent{
		ActorId: principal.UserId, SessionId: principal.SessionId,
		Action: string(action), Resource: resource,
		ScopeType: scopeType, ScopeId: resource.Id, Status: status,
		RequestId: metadata.RequestId, NodeId: s.nodeID,
		ClientType: string(principal.ClientType),
		AuthMethod: principal.AuthenticationMethod,
		IPAddress:  metadata.IPAddress, UserAgent: metadata.UserAgent,
		ErrorCode: errorCode,
	}
	if s.store == nil || s.store.Audit() == nil {
		return auditUnavailableError(
			"AuditService.RecordAuthorizationDecision",
			store.NewErrNotFound("audit_store", ""),
		)
	}
	if _, err := s.store.Audit().Save(ctx, event); err != nil {
		return auditUnavailableError("AuditService.RecordAuthorizationDecision", err)
	}
	return nil
}

func (s *AuditService) List(
	ctx context.Context,
	query model.AuditQuery,
) ([]*model.AuditEvent, *model.AppError) {
	if query.Limit < 1 || query.Limit > 200 ||
		(query.ActorId != "" && !model.IsValidId(query.ActorId)) ||
		(query.BeforeId != "" && !model.IsValidId(query.BeforeId)) ||
		(query.Resource != nil && !query.Resource.IsValid()) {
		return nil, model.NewAppError(
			"AuditService.List",
			"audit.query.invalid",
			nil,
			"",
			http.StatusBadRequest,
		)
	}
	events, err := s.store.Audit().List(ctx, store.AuditListOptions{
		ActorId: query.ActorId, Action: query.Action, Resource: query.Resource,
		BeforeTime: query.BeforeTime, BeforeId: query.BeforeId, Limit: query.Limit,
	})
	if err != nil {
		return nil, auditUnavailableError("AuditService.List", err)
	}
	return events, nil
}

func auditUnavailableError(where string, err error) *model.AppError {
	return model.NewAppError(
		where,
		"audit.unavailable",
		nil,
		"",
		http.StatusInternalServerError,
	).Wrap(err)
}
