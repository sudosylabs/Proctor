// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"context"
	"strings"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

type ListAuditEventsQuery struct {
	ActorID    string
	Action     string
	Resource   *model.Resource
	BeforeTime int64
	BeforeID   string
	Limit      int
}

type auditListingStore interface {
	List(context.Context, store.AuditListOptions) ([]*model.AuditEvent, error)
}

type auditListingAuthorizer interface {
	AuthorizeView(context.Context, Invocation) (store.AuditVisibilityScope, error)
}

type auditListingService struct {
	audits        auditListingStore
	authorization auditListingAuthorizer
}

func newAuditListingService(audits auditListingStore, authorization auditListingAuthorizer) *auditListingService {
	return &auditListingService{audits: audits, authorization: authorization}
}

func (a *App) ListAuditEvents(ctx context.Context, invocation Invocation, query ListAuditEventsQuery) ([]*model.AuditEvent, error) {
	return a.auditListings.List(ctx, invocation, query)
}

func (s *auditListingService) List(ctx context.Context, invocation Invocation, query ListAuditEventsQuery) ([]*model.AuditEvent, error) {
	visibility, err := s.authorization.AuthorizeView(ctx, invocation)
	if err != nil {
		return nil, err
	}
	validAcademicVisibility := len(visibility.AllowedActions) > 0 &&
		((visibility.AcademicInstitutionWide && len(visibility.AcademicUnitRootIDs) == 0) ||
			(!visibility.AcademicInstitutionWide && len(visibility.AcademicUnitRootIDs) > 0))
	if !visibility.InstitutionWide && !validAcademicVisibility {
		return nil, authorizationDeniedError("auditListingService.List")
	}
	if query.Limit == 0 {
		query.Limit = 50
	}
	if query.Limit < 1 || query.Limit > 200 ||
		(query.ActorID != "" && !model.IsValidId(query.ActorID)) ||
		(query.BeforeID != "" && !model.IsValidId(query.BeforeID)) ||
		(query.Resource != nil && query.Resource.Validate() != nil) {
		return nil, NewError("audit.query.invalid")
	}
	events, err := s.audits.List(ctx, store.AuditListOptions{
		ActorId: strings.TrimSpace(query.ActorID), Action: strings.TrimSpace(query.Action),
		Resource: query.Resource, BeforeTime: query.BeforeTime, BeforeId: query.BeforeID, Limit: query.Limit,
		Visibility: visibility,
	})
	if err != nil {
		return nil, auditListingError(err)
	}
	if events == nil {
		events = []*model.AuditEvent{}
	}
	if !visibility.InstitutionWide {
		events = academicAuditProjection(events)
	}
	return events, nil
}

func academicAuditProjection(events []*model.AuditEvent) []*model.AuditEvent {
	result := make([]*model.AuditEvent, 0, len(events))
	for _, event := range events {
		if event == nil {
			continue
		}
		projected := event.Clone()
		projected.SessionID = ""
		projected.RequestID = ""
		projected.NodeID = ""
		projected.ClientType = ""
		projected.AuthMethod = ""
		projected.IPAddress = ""
		projected.UserAgent = ""
		projected.Parameters = nil
		projected.PriorState = nil
		projected.Result = nil
		result = append(result, projected)
	}
	return result
}

type auditListingAuthorization struct {
	authorization *accessControlService
	institutions  store.InstitutionStore
}

func (a auditListingAuthorization) AuthorizeView(ctx context.Context, invocation Invocation) (store.AuditVisibilityScope, error) {
	institution, err := a.institutions.GetSingleton(ctx)
	if err != nil {
		return store.AuditVisibilityScope{}, auditListingError(err)
	}
	principal := invocation.Principal()
	global, err := a.authorization.authorizedScopes(ctx, principal, model.ActionAuditView, model.ResourceInstitution)
	if err != nil {
		return store.AuditVisibilityScope{}, err
	}
	if global.InstitutionWide {
		resource := model.Resource{Type: model.ResourceInstitution, ID: institution.ID.String()}
		if err := a.authorization.audit.RecordAuthorizationDecision(ctx, principal, model.ActionAuditView, resource, model.RoleScopeInstitution, institution.ID.String(), invocation.RequestMetadata(), true); err != nil {
			return store.AuditVisibilityScope{}, err
		}
		return store.AuditVisibilityScope{InstitutionWide: true}, nil
	}
	academic, err := a.authorization.authorizedScopes(ctx, principal, model.ActionAcademicAuditView, model.ResourceAcademicUnit)
	if err != nil {
		return store.AuditVisibilityScope{}, err
	}
	allowed := academic.InstitutionWide || len(academic.AcademicUnitRootIDs) > 0
	resource := model.Resource{Type: model.ResourceInstitution, ID: institution.ID.String()}
	scopeType, scopeID := model.RoleScopeInstitution, institution.ID.String()
	if !academic.InstitutionWide && len(academic.AcademicUnitRootIDs) > 0 {
		resource = model.Resource{Type: model.ResourceAcademicUnit, ID: academic.AcademicUnitRootIDs[0]}
		scopeType, scopeID = model.RoleScopeAcademicUnit, resource.ID
	}
	if err := a.authorization.audit.RecordAuthorizationDecision(ctx, principal, model.ActionAcademicAuditView, resource, scopeType, scopeID, invocation.RequestMetadata(), allowed); err != nil {
		return store.AuditVisibilityScope{}, err
	}
	if !allowed {
		return store.AuditVisibilityScope{}, authorizationDeniedError("auditListingAuthorization.AuthorizeView")
	}
	return store.AuditVisibilityScope{
		AcademicInstitutionWide: academic.InstitutionWide,
		AcademicUnitRootIDs:     append([]string(nil), academic.AcademicUnitRootIDs...),
		AllowedActions:          academicAuditActions(),
	}, nil
}

func academicAuditActions() []string {
	return []string{
		string(model.ActionAcademicAuditView), string(model.ActionAcademicUnitView), string(model.ActionAcademicUnitManage),
		string(model.ActionAcademicUnitMembersView), string(model.ActionAcademicUnitMembersManage),
		string(model.ActionAcademicPeriodView), string(model.ActionAcademicPeriodManage),
		string(model.ActionProgrammeView), string(model.ActionProgrammeManage),
		string(model.ActionProgrammeLevelView), string(model.ActionProgrammeLevelManage),
		string(model.ActionClassView), string(model.ActionClassManage), string(model.ActionClassMembersView),
		string(model.ActionClassMembersManage), string(model.ActionAcademicProgressionManage),
		string(model.ActionInvitationView), string(model.ActionInvitationCreate), string(model.ActionInvitationManage),
		string(model.ActionOnboardingBatchView), string(model.ActionOnboardingBatchManage),
		string(model.ActionRoleBindingView), string(model.ActionRoleBindingManage),
		string(model.ActionUserView), "user.search",
	}
}

func auditListingError(err error) error {

	return NewError("audit.unavailable").WithField("resource", "audit").Wrap(err)
}
