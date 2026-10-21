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

	"github.com/sudosylabs/proctor/server/app/apperror"
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

func (a mailAuditAdapter) Fail(ctx context.Context, auditID model.AuditEventID, errorCode string) error {
	_, appErr := a.audit.CompleteCriticalAction(ctx, auditID, model.AuditStatusFail, errorCode, nil)
	return appErr
}

func (a mailAuditAdapter) PrepareControl(ctx context.Context, invocation Invocation, institution model.Resource, delivery *model.MailDelivery, operation string) (model.AuditEventID, error) {
	if a.audit == nil || institution.Type != model.ResourceInstitution || !model.InstitutionID(institution.ID).IsValid() ||
		delivery == nil || delivery.Validate() != nil || (operation != "cancel" && operation != "retry") {
		return "", auditUnavailable(errors.New("mail control audit dependencies are invalid"))
	}
	event, appErr := a.audit.BeginCriticalActionAtScope(ctx, invocation.Principal(), model.ActionMailManage,
		model.Resource{Type: model.ResourceMailDelivery, ID: delivery.ID.String()}, model.RoleScopeInstitution, institution.ID,
		invocation.RequestMetadata(), map[string]any{"operation": operation, "value": delivery.Auditable()}, delivery.Auditable())
	if appErr != nil {
		return "", appErr
	}
	return event.ID, nil
}

func (a mailAuditAdapter) PrepareTest(ctx context.Context, invocation Invocation, institution model.Resource, deliveryID model.MailDeliveryID) (*model.AuditEvent, error) {
	if a.audit == nil || institution.Type != model.ResourceInstitution || !model.InstitutionID(institution.ID).IsValid() || !deliveryID.IsValid() {
		return nil, auditUnavailable(errors.New("mail audit dependencies are invalid"))
	}
	principal := invocation.Principal()
	if principal.Validate() != nil || principal.CredentialType != model.CredentialSessionAccess {
		return nil, invalidTokenAppError()
	}
	parameters, err := model.EncodeAuditData(map[string]string{"delivery_id": deliveryID.String(), "template_key": string(model.MailTemplateSystemTest)})
	if err != nil {
		return nil, domainInvalid("audit.internal", err)
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
		return nil, apperror.New("audit.internal")
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
		return nil, domainInvalid("audit.internal", err)
	}
	metadata := input.Invocation.RequestMetadata()
	return &model.AuditEvent{
		ActorID: principal.UserID, SessionID: principal.SessionID,
		Action:    string(model.ActionUserSettingsReplace),
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
	if !model.UserID(userID).IsValid() || !model.InstitutionID(institutionID).IsValid() ||
		!clientType.IsValid() || method == "" {
		return nil, apperror.New("audit.internal")
	}
	if s.audits == nil {
		return nil, auditUnavailable(store.NewErrNotFound("audit_store", ""))
	}
	parameters, err := model.EncodeAuditData(map[string]string{
		"provider": providerID,
	})
	if err != nil {
		return nil, domainInvalid("audit.internal", err)
	}
	event := &model.AuditEvent{
		ActorID: model.UserID(userID),
		Action:  string(model.ActionAuthenticationExternalLogin),
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

// BeginLocalLogin persists the attempt for a password login whose credential
// and second factor were proved, before any Session is issued. The event is
// completed with the Session projection or the issuance failure.
func (s *auditService) BeginLocalLogin(
	ctx context.Context,
	userID model.UserID,
	clientType model.SessionClientType,
	metadata model.RequestMetadata,
) (*model.AuditEvent, error) {
	if !userID.IsValid() || !clientType.IsValid() {
		return nil, apperror.New("audit.internal")
	}
	if s.audits == nil {
		return nil, auditUnavailable(store.NewErrNotFound("audit_store", ""))
	}
	institution, err := s.institutions.GetSingleton(ctx)
	if err != nil {
		return nil, auditUnavailable(err)
	}
	event := localLoginAuditEvent(s.nodeID, institution.ID.String(), clientType, metadata)
	event.ActorID = userID
	event.Resource = model.Resource{Type: model.ResourceUser, ID: userID.String()}
	event.Status = model.AuditStatusAttempt
	saved, err := s.audits.Save(ctx, event)
	if err != nil {
		return nil, auditUnavailable(err)
	}
	return saved, nil
}

// RecordLocalLoginFailure persists one terminal failed password login. The
// failed caller never becomes an actor; a resolved account is recorded only as
// the targeted resource so operators can see attacks against it without the
// raw, user-controlled login identifier entering audit data.
func (s *auditService) RecordLocalLoginFailure(
	ctx context.Context,
	target model.UserID,
	clientType model.SessionClientType,
	metadata model.RequestMetadata,
	errorCode string,
) error {
	if errorCode == "" {
		return apperror.New("audit.internal")
	}
	if s.audits == nil {
		return auditUnavailable(store.NewErrNotFound("audit_store", ""))
	}
	institution, err := s.institutions.GetSingleton(ctx)
	if err != nil {
		return auditUnavailable(err)
	}
	event := localLoginAuditEvent(s.nodeID, institution.ID.String(), clientType, metadata)
	event.Resource = model.Resource{Type: model.ResourceInstitution, ID: institution.ID.String()}
	if target.IsValid() {
		event.Resource = model.Resource{Type: model.ResourceUser, ID: target.String()}
	}
	event.Status = model.AuditStatusFail
	event.ErrorCode = errorCode
	if _, err := s.audits.Save(ctx, event); err != nil {
		return auditUnavailable(err)
	}
	return nil
}

func localLoginAuditEvent(nodeID, institutionID string, clientType model.SessionClientType,
	metadata model.RequestMetadata,
) *model.AuditEvent {
	return &model.AuditEvent{
		Action:    string(model.ActionAuthenticationLogin),
		ScopeType: model.RoleScopeInstitution, ScopeID: institutionID,
		RequestID: metadata.RequestID, NodeID: nodeID,
		ClientType: string(clientType), AuthMethod: "password",
		IPAddress: metadata.IPAddress, UserAgent: metadata.UserAgent,
	}
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
		return apperror.New("audit.internal")
	}
	parameters, err := model.EncodeAuditData(map[string]string{
		"provider": providerID,
	})
	if err != nil {
		return domainInvalid("audit.internal", err)
	}
	if s.audits == nil {
		return auditUnavailable(store.NewErrNotFound("audit_store", ""))
	}
	event := &model.AuditEvent{
		Action: string(model.ActionAuthenticationExternalLogin),
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
	if principal.ValidateMFARecovery() != nil {
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
	if principal.ValidateMFARecovery() != nil {
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
	if principal.ValidateMFARecovery() != nil {
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
		return nil, domainInvalid("audit.internal", err)
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

// accountAuditActor is the proved account behind an authentication handoff
// whose request carries no Principal yet, such as Desktop approval or code
// exchange. It is never fabricated from user-controlled input.
type accountAuditActor struct {
	userID     model.UserID
	sessionID  model.SessionID
	clientType model.SessionClientType
}

// BeginAccountActionAtScope persists a critical attempt performed for a proved
// account outside an authenticated Principal, retaining the request identity.
func (s *auditService) BeginAccountActionAtScope(
	ctx context.Context,
	actor accountAuditActor,
	action model.Action,
	resource model.Resource,
	scopeType model.RoleScopeType,
	scopeID string,
	metadata model.RequestMetadata,
	parameters any,
) (*model.AuditEvent, error) {
	if !actor.userID.IsValid() || !actor.clientType.IsValid() {
		return nil, apperror.New("audit.internal")
	}
	if s.audits == nil {
		return nil, auditUnavailable(store.NewErrNotFound("audit_store", ""))
	}
	encodedParameters, err := model.EncodeAuditData(parameters)
	if err != nil {
		return nil, domainInvalid("audit.internal", err)
	}
	saved, err := s.audits.Save(ctx, &model.AuditEvent{
		ActorID: actor.userID, SessionID: actor.sessionID,
		Action: string(action), Resource: resource,
		ScopeType: scopeType, ScopeID: scopeID,
		Status: model.AuditStatusAttempt, RequestID: metadata.RequestID,
		NodeID: s.nodeID, ClientType: string(actor.clientType),
		IPAddress: metadata.IPAddress, UserAgent: metadata.UserAgent,
		Parameters: encodedParameters,
	})
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
		return nil, domainInvalid("audit.internal", err)
	}
	encodedPriorState, err := model.EncodeAuditData(priorState)
	if err != nil {
		return nil, domainInvalid("audit.internal", err)
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
// The write ignores cancellation of ctx: a client that disconnects must not
// leave the attempt unresolved, and the Store's query timeout still bounds it.
func (s *auditService) CompleteCriticalAction(
	ctx context.Context,
	eventID model.AuditEventID,
	status model.AuditStatus,
	errorCode string,
	result any,
) (*model.AuditEvent, error) {
	ctx = context.WithoutCancel(ctx)
	if s.audits == nil {
		return nil, auditUnavailable(store.NewErrNotFound("audit_store", ""))
	}
	encodedResult, err := model.EncodeAuditData(result)
	if err != nil {
		return nil, domainInvalid("audit.internal", err)
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
		ctx, principal, string(model.ActionUserSearch), resource,
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
		(query.ActorID != "" && !model.UserID(query.ActorID).IsValid()) ||
		(query.BeforeID != "" && !model.AuditEventID(query.BeforeID).IsValid()) ||
		(query.Resource != nil && query.Resource.Validate() != nil) {
		return nil, apperror.New("audit.query.invalid")
	}
	events, err := s.audits.List(ctx, store.AuditListOptions{
		ActorID: model.UserID(query.ActorID), Action: query.Action, Resource: query.Resource,
		BeforeCreatedAt: query.BeforeCreatedAt, BeforeID: model.AuditEventID(query.BeforeID), Limit: query.Limit,
		Visibility: store.AuditVisibilityScope{InstitutionWide: true},
	})
	if err != nil {
		return nil, auditUnavailable(err)
	}
	return events, nil
}

func auditUnavailable(err error) error {
	return apperror.New("audit.unavailable").Wrap(err)
}
