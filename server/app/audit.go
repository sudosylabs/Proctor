// ---------------------------------------------------------------------------------------------
// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// Modifications Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------
//
// Adapted from Mattermost server/channels/app/audit.go and
// server/public/model/audit_record.go. Proctor writes authoritative events to
// PostgreSQL and treats decision-audit failure as a closed security boundary.

package app

import (
	"context"
	"errors"
	"time"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

type auditService struct {
	audits       store.AuditStore
	institutions store.InstitutionStore
	nodeID       string
	now          func() time.Time
}

type userSettingsAuditAdapter struct{ audit *auditService }

type mailAuditAdapter struct{ audit *auditService }

func (a mailAuditAdapter) Begin(ctx context.Context, invocation Invocation, action model.Action, resource model.Resource,
	operation string, value, prior map[string]any,
) (string, error) {
	return (mutationAuditAdapter{audit: a.audit}).Begin(ctx, invocation, action, resource, operation, value, prior)
}

func (a mailAuditAdapter) BeginAtScope(ctx context.Context, invocation Invocation, action model.Action,
	resource model.Resource, scopeType model.RoleScopeType, scopeID, operation string, value, prior map[string]any,
) (string, error) {
	return (mutationAuditAdapter{audit: a.audit}).BeginAtScope(ctx, invocation, action, resource, scopeType, scopeID,
		operation, value, prior)
}

func (a mailAuditAdapter) Fail(ctx context.Context, auditID, errorCode string) error {
	return (mutationAuditAdapter{audit: a.audit}).Fail(ctx, auditID, errorCode)
}

func (a mailAuditAdapter) PrepareControl(ctx context.Context, invocation Invocation, institution model.Resource, delivery *model.MailDelivery, operation string) (string, error) {
	if a.audit == nil || institution.Type != model.ResourceInstitution || !model.IsValidId(institution.ID) ||
		delivery == nil || delivery.Validate() != nil || (operation != "cancel" && operation != "retry") {
		return "", auditUnavailable(errors.New("mail control audit dependencies are invalid"))
	}
	event, appErr := a.audit.BeginCriticalActionAtScope(ctx, invocation.Principal(), model.ActionMailManage,
		model.Resource{Type: model.ResourceMailDelivery, ID: delivery.ID.String()}, model.RoleScopeInstitution, institution.ID,
		invocation.RequestMetadata(), map[string]any{"operation": operation, "value": delivery.Auditable()}, delivery.Auditable())
	if appErr != nil {
		return "", appErr
	}
	return event.ID.String(), nil
}

func (a mailAuditAdapter) PrepareTest(ctx context.Context, invocation Invocation, institution model.Resource, deliveryID model.MailDeliveryID) (*model.AuditEvent, error) {
	if a.audit == nil || institution.Type != model.ResourceInstitution || !model.IsValidId(institution.ID) || !deliveryID.IsValid() {
		return nil, auditUnavailable(errors.New("mail audit dependencies are invalid"))
	}
	principal := invocation.Principal()
	if principal.Validate() != nil || principal.CredentialType != model.CredentialSessionAccess {
		return nil, invalidTokenAppError()
	}
	parameters, err := model.EncodeAuditData(map[string]string{"delivery_id": deliveryID.String(), "template_key": string(model.MailTemplateSystemTest)})
	if err != nil {
		return nil, domainInvalid("audit.event.invalid", err)
	}
	metadata := invocation.RequestMetadata()
	return &model.AuditEvent{ActorID: principal.UserID, SessionID: principal.SessionID, Action: string(model.ActionMailManage), Resource: model.Resource{Type: model.ResourceMailDelivery, ID: deliveryID.String()}, ScopeType: model.RoleScopeInstitution, ScopeID: institution.ID, Status: model.AuditStatusSuccess, RequestID: metadata.RequestID, NodeID: a.audit.nodeID, ClientType: string(principal.ClientType), AuthMethod: principal.AuthenticationMethod, IPAddress: metadata.IPAddress, UserAgent: metadata.UserAgent, Parameters: parameters}, nil
}

func (a userSettingsAuditAdapter) PrepareReplacement(
	ctx context.Context,
	input userSettingsAuditInput,
) (*model.AuditEvent, error) {
	if a.audit == nil {
		return nil, auditUnavailable(errors.New("user settings audit service is unavailable"))
	}
	principal := input.Invocation.Principal()
	if principal.Validate() != nil || principal.CredentialType != model.CredentialSessionAccess ||
		principal.UserID != input.UserID || !input.PreviousRevision.IsValid() ||
		!input.ResultingRevision.IsValid() || input.FormatVersion <= 0 || input.SourceBytes < 0 {
		return nil, NewError("audit.event.invalid")
	}
	institution, err := a.audit.institutions.GetSingleton(ctx)
	if err != nil {
		return nil, auditUnavailable(err)
	}
	parameters, err := model.EncodeAuditData(map[string]any{
		"previous_revision":  input.PreviousRevision.String(),
		"resulting_revision": input.ResultingRevision.String(),
		"format_version":     input.FormatVersion,
		"source_bytes":       input.SourceBytes,
	})
	if err != nil {
		return nil, domainInvalid("audit.event.invalid", err)
	}
	metadata := input.Invocation.RequestMetadata()
	return &model.AuditEvent{
		ActorID: principal.UserID, SessionID: principal.SessionID,
		Action:    "user.settings.replace",
		Resource:  model.Resource{Type: model.ResourceUser, ID: principal.UserID.String()},
		ScopeType: model.RoleScopeInstitution, ScopeID: institution.ID.String(),
		Status: model.AuditStatusSuccess, RequestID: metadata.RequestID,
		NodeID: a.audit.nodeID, ClientType: string(principal.ClientType),
		AuthMethod: principal.AuthenticationMethod, IPAddress: metadata.IPAddress,
		UserAgent: metadata.UserAgent, Parameters: parameters,
	}, nil
}

func newAuditService(audits store.AuditStore, institutions store.InstitutionStore, nodeID string) (*auditService, error) {
	if audits == nil {
		return nil, errors.New("audit store is required")
	}
	if institutions == nil {
		return nil, errors.New("audit institution store is required")
	}
	if nodeID == "" {
		return nil, errors.New("audit node ID is required")
	}
	return &auditService{audits: audits, institutions: institutions, nodeID: nodeID, now: time.Now}, nil
}

func (s *auditService) BeginAuthentication(
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
	if s.audits == nil {
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
	saved, err := s.audits.Save(ctx, event)
	if err != nil {
		return nil, auditUnavailable(err)
	}
	return saved, nil
}

func (s *auditService) RecordExternalAuthenticationFailure(
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
	if s.audits == nil {
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
	if _, err := s.audits.Save(ctx, event); err != nil {
		return auditUnavailable(err)
	}
	return nil
}

// BeginCriticalAction persists an attempt before a security-sensitive mutation
// is allowed to start. Callers must pass only bounded safe values or Auditable
// projections; credentials and secrets are never acceptable audit parameters.
func (s *auditService) BeginCriticalAction(
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
	if s.audits == nil {
		return nil, auditUnavailable(store.NewErrNotFound("audit_store", ""))
	}
	scopeType := model.RoleScopeType(resource.Type)
	scopeID := resource.ID
	if resource.Type == model.ResourceUser {
		institution, err := s.institutions.GetSingleton(ctx)
		if err != nil {
			return nil, auditUnavailable(err)
		}
		scopeType = model.RoleScopeInstitution
		scopeID = institution.ID.String()
	}
	return s.beginCriticalActionAtScope(ctx, principal, action, resource, scopeType, scopeID, metadata, parameters, priorState)
}

// PrepareCriticalAction builds the bounded safe audit draft owned by a named
// durable pre-mutation aggregate. The aggregate, rather than AuditStore, is
// the pending attempt; it inserts exactly one terminal AuditEvent or no event
// for an authoritative idempotent replay.
func (s *auditService) PrepareCriticalAction(
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
	scopeType := model.RoleScopeType(resource.Type)
	scopeID := resource.ID
	if resource.Type == model.ResourceUser {
		institution, err := s.institutions.GetSingleton(ctx)
		if err != nil {
			return nil, auditUnavailable(err)
		}
		scopeType = model.RoleScopeInstitution
		scopeID = institution.ID.String()
	}
	return s.prepareCriticalActionAtScope(principal, action, resource, scopeType, scopeID, metadata, parameters, priorState)
}

// BeginCriticalActionAtScope records a mutation against its domain resource
// while retaining the independently resolved authorization scope. This is
// required for resources such as Exams whose identity is not itself a role
// binding scope.
func (s *auditService) BeginCriticalActionAtScope(
	ctx context.Context,
	principal model.Principal,
	action model.Action,
	resource model.Resource,
	scopeType model.RoleScopeType,
	scopeID string,
	metadata model.RequestMetadata,
	parameters any,
	priorState any,
) (*model.AuditEvent, error) {
	if principal.Validate() != nil {
		return nil, invalidTokenAppError()
	}
	if s.audits == nil {
		return nil, auditUnavailable(store.NewErrNotFound("audit_store", ""))
	}
	return s.beginCriticalActionAtScope(ctx, principal, action, resource, scopeType, scopeID, metadata, parameters, priorState)
}

// BeginSystemCriticalActionAtScope persists an actor-less critical mutation
// attempt for trusted durable work. It is intentionally distinct from the
// principal-bearing path so Jobs never fabricate a user identity or silently
// bypass user authorization.
func (s *auditService) BeginSystemCriticalActionAtScope(
	ctx context.Context,
	action model.Action,
	resource model.Resource,
	scopeType model.RoleScopeType,
	scopeID string,
	parameters any,
) (*model.AuditEvent, error) {
	if s.audits == nil {
		return nil, auditUnavailable(store.NewErrNotFound("audit_store", ""))
	}
	encodedParameters, err := model.EncodeAuditData(parameters)
	if err != nil {
		return nil, domainInvalid("audit.event.invalid", err)
	}
	event := &model.AuditEvent{
		Action: string(action), Resource: resource,
		ScopeType: scopeType, ScopeID: scopeID,
		Status: model.AuditStatusAttempt, NodeID: s.nodeID,
		Parameters: encodedParameters,
	}
	saved, err := s.audits.Save(ctx, event)
	if err != nil {
		return nil, auditUnavailable(err)
	}
	return saved, nil
}

func (s *auditService) beginCriticalActionAtScope(
	ctx context.Context,
	principal model.Principal,
	action model.Action,
	resource model.Resource,
	scopeType model.RoleScopeType,
	scopeID string,
	metadata model.RequestMetadata,
	parameters any,
	priorState any,
) (*model.AuditEvent, error) {
	event, appErr := s.prepareCriticalActionAtScope(principal, action, resource, scopeType, scopeID, metadata, parameters, priorState)
	if appErr != nil {
		return nil, appErr
	}
	saved, err := s.audits.Save(ctx, event)
	if err != nil {
		return nil, auditUnavailable(err)
	}
	return saved, nil
}

func (s *auditService) prepareCriticalActionAtScope(
	principal model.Principal,
	action model.Action,
	resource model.Resource,
	scopeType model.RoleScopeType,
	scopeID string,
	metadata model.RequestMetadata,
	parameters any,
	priorState any,
) (*model.AuditEvent, error) {
	encodedParameters, err := model.EncodeAuditData(parameters)
	if err != nil {
		return nil, domainInvalid("audit.event.invalid", err)
	}
	encodedPriorState, err := model.EncodeAuditData(priorState)
	if err != nil {
		return nil, domainInvalid("audit.event.invalid", err)
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
	return event, nil
}

// CompleteCriticalAction records the terminal outcome. If this fails after the
// mutation committed, the durable attempt remains for operator reconciliation
// and the use case must return the audit failure instead of reporting success.
func (s *auditService) CompleteCriticalAction(
	ctx context.Context,
	eventID string,
	status model.AuditStatus,
	errorCode string,
	result any,
) (*model.AuditEvent, error) {
	if s.audits == nil {
		return nil, auditUnavailable(store.NewErrNotFound("audit_store", ""))
	}
	encodedResult, err := model.EncodeAuditData(result)
	if err != nil {
		return nil, domainInvalid("audit.event.invalid", err)
	}
	event, err := s.audits.Complete(
		ctx, eventID, status, errorCode, encodedResult, s.now().UnixMilli(),
	)
	if err != nil {
		return nil, auditUnavailable(err)
	}
	return event, nil
}

func (s *auditService) RecordAuthorizationDecision(
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
func (s *auditService) RecordUserSearchDecision(
	ctx context.Context,
	principal model.Principal,
	resource model.Resource,
	scopeType model.RoleScopeType,
	scopeID string,
	metadata model.RequestMetadata,
	allowed bool,
) error {
	return s.recordDecision(
		ctx, principal, "user.search", resource,
		scopeType, scopeID, metadata, allowed,
	)
}

func (s *auditService) recordDecision(
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
	if s.audits == nil {
		return auditUnavailable(store.NewErrNotFound("audit_store", ""))
	}
	if _, err := s.audits.Save(ctx, event); err != nil {
		return auditUnavailable(err)
	}
	return nil
}

func (s *auditService) List(
	ctx context.Context,
	query model.AuditQuery,
) ([]*model.AuditEvent, error) {
	if query.Limit < 1 || query.Limit > 200 ||
		(query.ActorID != "" && !model.IsValidId(query.ActorID)) ||
		(query.BeforeID != "" && !model.IsValidId(query.BeforeID)) ||
		(query.Resource != nil && query.Resource.Validate() != nil) {
		return nil, NewError("audit.query.invalid")
	}
	events, err := s.audits.List(ctx, store.AuditListOptions{
		ActorId: query.ActorID, Action: query.Action, Resource: query.Resource,
		BeforeTime: query.BeforeTime, BeforeId: query.BeforeID, Limit: query.Limit,
		Visibility: store.AuditVisibilityScope{InstitutionWide: true},
	})
	if err != nil {
		return nil, auditUnavailable(err)
	}
	return events, nil
}

func auditUnavailable(err error) error {
	return NewError("audit.unavailable").Wrap(err)
}
