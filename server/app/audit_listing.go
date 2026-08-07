// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"context"
	"errors"
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
	AuthorizeView(context.Context, Invocation) error
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
	if err := s.authorization.AuthorizeView(ctx, invocation); err != nil {
		return nil, err
	}
	if query.Limit == 0 {
		query.Limit = 50
	}
	if query.Limit < 1 || query.Limit > 200 ||
		(query.ActorID != "" && !model.IsValidId(query.ActorID)) ||
		(query.BeforeID != "" && !model.IsValidId(query.BeforeID)) ||
		(query.Resource != nil && !query.Resource.IsValid()) {
		return nil, NewError("audit.query.invalid")
	}
	events, err := s.audits.List(ctx, store.AuditListOptions{
		ActorId: strings.TrimSpace(query.ActorID), Action: strings.TrimSpace(query.Action),
		Resource: query.Resource, BeforeTime: query.BeforeTime, BeforeId: query.BeforeID, Limit: query.Limit,
	})
	if err != nil {
		return nil, auditListingError(err)
	}
	if events == nil {
		events = []*model.AuditEvent{}
	}
	return events, nil
}

type auditListingAuthorization struct {
	authorization *AuthorizationService
	institutions  store.InstitutionStore
}

func (a auditListingAuthorization) AuthorizeView(ctx context.Context, invocation Invocation) error {
	institution, err := a.institutions.GetSingleton(ctx)
	if err != nil {
		return auditListingError(err)
	}
	return fromLegacyAppError(a.authorization.authorizeCurrentState(
		ctx,
		invocation.Principal(),
		model.ActionAuditView,
		model.Resource{Type: model.ResourceInstitution, Id: institution.Id},
		invocation.RequestMetadata(),
	))
}

func auditListingError(err error) error {
	var appErr *model.AppError
	if errors.As(err, &appErr) {
		return fromLegacyAppError(appErr)
	}
	return NewError("audit.unavailable").WithField("resource", "audit").Wrap(err)
}
