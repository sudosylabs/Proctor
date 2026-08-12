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

func (s *AuditService) BeginAuthentication(
	ctx context.Context,
	userID string,
	method string,
	providerID string,
	clientType model.SessionClientType,
	metadata model.RequestMetadata,
	institutionID string,
) (*model.AuditEvent, error) {
	if !model.IsValidId(userID) || !model.IsValidId(institutionID) ||
		!clientType.IsValid() || method == "" {
		return nil, NewError("audit.event.invalid")
	}
	if s.store == nil || s.store.Audit() == nil {
		return nil, auditUnavailable(store.NewErrNotFound("audit_store", ""))
	}
	parameters, err := model.EncodeAuditData(map[string]string{
		"provider": providerID,
	})
	if err != nil {
		return nil, domainInvalid("audit.event.invalid", err)
	}
	event := &model.AuditEvent{
		ActorID: model.UserID(userID),
		Action:  "authentication.external_login",
		Resource: model.Resource{
			Type: model.ResourceUser,
			ID:   userID,
		},
		ScopeType:  model.RoleScopeInstitution,
		ScopeID:    institutionID,
		Status:     model.AuditStatusAttempt,
		RequestID:  metadata.RequestID,
		NodeID:     s.nodeID,
		ClientType: string(clientType),
		AuthMethod: method,
		IPAddress:  metadata.IPAddress,
		UserAgent:  metadata.UserAgent,
		Parameters: parameters,
	}
	saved, err := s.store.Audit().Save(ctx, event)
	if err != nil {
		return nil, auditUnavailable(err)
	}
	return saved, nil
}

func (s *AuditService) RecordExternalAuthenticationFailure(
	ctx context.Context,
	providerID string,
	method string,
	metadata model.RequestMetadata,
	institutionID string,
	errorCode string,
) error {
	if method == "" {
		return NewError("audit.event.invalid")
	}
	parameters, err := model.EncodeAuditData(map[string]string{
		"provider": providerID,
	})
	if err != nil {
		return domainInvalid("audit.event.invalid", err)
	}
	if s.store == nil || s.store.Audit() == nil {
		return auditUnavailable(store.NewErrNotFound("audit_store", ""))
	}
	event := &model.AuditEvent{
		Action: "authentication.external_login",
		Resource: model.Resource{
			Type: model.ResourceInstitution,
			ID:   institutionID,
		},
		ScopeType:  model.RoleScopeInstitution,
		ScopeID:    institutionID,
		Status:     model.AuditStatusFail,
		RequestID:  metadata.RequestID,
		NodeID:     s.nodeID,
		AuthMethod: method,
		IPAddress:  metadata.IPAddress,
		UserAgent:  metadata.UserAgent,
		ErrorCode:  errorCode,
		Parameters: parameters,
	}
	if _, err := s.store.Audit().Save(ctx, event); err != nil {
		return auditUnavailable(err)
	}
	return nil
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
) (*model.AuditEvent, error) {
	if principal.Validate() != nil {
		return nil, invalidTokenAppError()
	}
	if s.store == nil || s.store.Audit() == nil {
		return nil, auditUnavailable(store.NewErrNotFound("audit_store", ""))
	}
	encodedParameters, err := model.EncodeAuditData(parameters)
	if err != nil {
		return nil, domainInvalid("audit.event.invalid", err)
	}
	encodedPriorState, err := model.EncodeAuditData(priorState)
	if err != nil {
		return nil, domainInvalid("audit.event.invalid", err)
	}
	scopeType := model.RoleScopeType(resource.Type)
	scopeID := resource.ID
	if resource.Type == model.ResourceUser {
		institution, err := s.store.Institution().GetSingleton(ctx)
		if err != nil {
			return nil, auditUnavailable(err)
		}
		scopeType = model.RoleScopeInstitution
		scopeID = institution.ID.String()
	}
	event := &model.AuditEvent{
		ActorID: principal.UserID, SessionID: principal.SessionID,
		Action: string(action), Resource: resource,
		ScopeType: scopeType, ScopeID: scopeID,
		Status: model.AuditStatusAttempt, RequestID: metadata.RequestID,
		NodeID: s.nodeID, ClientType: string(principal.ClientType),
		AuthMethod: principal.AuthenticationMethod, IPAddress: metadata.IPAddress,
		UserAgent: metadata.UserAgent, Parameters: encodedParameters,
		PriorState: encodedPriorState,
	}
	saved, err := s.store.Audit().Save(ctx, event)
	if err != nil {
		return nil, auditUnavailable(err)
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
) (*model.AuditEvent, error) {
	if s.store == nil || s.store.Audit() == nil {
		return nil, auditUnavailable(store.NewErrNotFound("audit_store", ""))
	}
	encodedResult, err := model.EncodeAuditData(result)
	if err != nil {
		return nil, domainInvalid("audit.event.invalid", err)
	}
	event, err := s.store.Audit().Complete(
		ctx, eventID, status, errorCode, encodedResult, s.now().UnixMilli(),
	)
	if err != nil {
		return nil, auditUnavailable(err)
	}
	return event, nil
}

func (s *AuditService) RecordAuthorizationDecision(
	ctx context.Context,
	principal model.Principal,
	action model.Action,
	resource model.Resource,
	scopeType model.RoleScopeType,
	scopeID string,
	metadata model.RequestMetadata,
	allowed bool,
) error {
	return s.recordDecision(ctx, principal, string(action), resource, scopeType, scopeID, metadata, allowed)
}

// RecordUserSearchDecision records collection-level User search without
// introducing a permission action. Authority comes from user.view and
// class.members.view grants.
func (s *AuditService) RecordUserSearchDecision(
	ctx context.Context,
	principal model.Principal,
	resource model.Resource,
	metadata model.RequestMetadata,
	allowed bool,
) error {
	return s.recordDecision(
		ctx, principal, "user.search", resource,
		model.RoleScopeInstitution, resource.ID, metadata, allowed,
	)
}

func (s *AuditService) recordDecision(
	ctx context.Context,
	principal model.Principal,
	action string,
	resource model.Resource,
	scopeType model.RoleScopeType,
	scopeID string,
	metadata model.RequestMetadata,
	allowed bool,
) error {
	status := model.AuditStatusFail
	errorCode := "authorization.denied"
	if allowed {
		status = model.AuditStatusSuccess
		errorCode = ""
	}
	event := &model.AuditEvent{
		ActorID: principal.UserID, SessionID: principal.SessionID,
		Action: action, Resource: resource,
		ScopeType: scopeType, ScopeID: scopeID, Status: status,
		RequestID: metadata.RequestID, NodeID: s.nodeID,
		ClientType: string(principal.ClientType),
		AuthMethod: principal.AuthenticationMethod,
		IPAddress:  metadata.IPAddress, UserAgent: metadata.UserAgent,
		ErrorCode: errorCode,
	}
	if s.store == nil || s.store.Audit() == nil {
		return auditUnavailable(store.NewErrNotFound("audit_store", ""))
	}
	if _, err := s.store.Audit().Save(ctx, event); err != nil {
		return auditUnavailable(err)
	}
	return nil
}

func (s *AuditService) List(
	ctx context.Context,
	query model.AuditQuery,
) ([]*model.AuditEvent, error) {
	if query.Limit < 1 || query.Limit > 200 ||
		(query.ActorID != "" && !model.IsValidId(query.ActorID)) ||
		(query.BeforeID != "" && !model.IsValidId(query.BeforeID)) ||
		(query.Resource != nil && query.Resource.Validate() != nil) {
		return nil, NewError("audit.query.invalid")
	}
	events, err := s.store.Audit().List(ctx, store.AuditListOptions{
		ActorId: query.ActorID, Action: query.Action, Resource: query.Resource,
		BeforeTime: query.BeforeTime, BeforeId: query.BeforeID, Limit: query.Limit,
	})
	if err != nil {
		return nil, auditUnavailable(err)
	}
	return events, nil
}

func auditUnavailable(err error) error {
	return NewError("audit.unavailable").Wrap(err)
}
