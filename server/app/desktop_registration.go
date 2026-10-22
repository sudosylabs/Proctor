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
	"strings"
	"time"

	"github.com/sudosylabs/proctor/server/app/apperror"
	"github.com/sudosylabs/proctor/server/app/invocation"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

type ListDesktopRegistrationsQuery struct{}

type RevokeDesktopRegistrationCommand struct {
	RegistrationID string
}

type desktopRegistrationService struct {
	registrations           store.DesktopRegistrationStore
	audit                   mutationAuditor
	effects                 authenticationSecurityEffects
	recentAuthenticationTTL time.Duration
	now                     func() time.Time
}

func newDesktopRegistrationService(
	registrations store.DesktopRegistrationStore,
	audit mutationAuditor,
	effects authenticationSecurityEffects,
	recentAuthenticationTTL time.Duration,
	now func() time.Time,
) (*desktopRegistrationService, error) {
	if registrations == nil || audit == nil || effects == nil || recentAuthenticationTTL <= 0 || now == nil {
		return nil, errors.New("Desktop Registration dependencies are invalid")
	}
	return &desktopRegistrationService{
		registrations: registrations, audit: audit, effects: effects,
		recentAuthenticationTTL: recentAuthenticationTTL, now: now,
	}, nil
}

func (s *desktopRegistrationService) ListDesktopRegistrations(
	ctx context.Context,
	invocation invocation.Invocation,
	_ ListDesktopRegistrationsQuery,
) ([]*model.DesktopRegistration, error) {
	principal := invocation.Principal()
	if err := requireInteractiveSession(principal, false, s.now(), s.recentAuthenticationTTL); err != nil {
		return nil, err
	}
	registrations, err := s.registrations.ListByUser(ctx, principal.UserID)
	if err != nil {
		return nil, apperror.New("desktop_registration.unavailable").Wrap(err)
	}
	return registrations, nil
}

func (s *desktopRegistrationService) RevokeDesktopRegistration(
	ctx context.Context,
	invocation invocation.Invocation,
	command RevokeDesktopRegistrationCommand,
) error {
	principal := invocation.Principal()
	at := model.TimeUTC(s.now())
	if err := requireInteractiveSession(principal, true, at, s.recentAuthenticationTTL); err != nil {
		return err
	}
	registrationID, err := model.ParseDesktopRegistrationID(strings.TrimSpace(command.RegistrationID))
	if err != nil {
		return apperror.New("request.invalid").WithField("field", "desktop_registration_id")
	}
	auditID, err := s.audit.Begin(
		ctx, invocation, model.ActionDesktopRegistrationRevoke,
		model.Resource{Type: model.ResourceUser, ID: principal.UserID.String()},
		"revoke", map[string]any{"registration_id": registrationID.String()}, nil,
	)
	if err != nil {
		return err
	}
	result, err := s.registrations.Revoke(ctx, &store.DesktopRegistrationRevocation{
		RegistrationID: registrationID, UserID: principal.UserID,
		RevokedAt: at.UnixMilli(), AuditEventID: auditID, AuditAt: at.UnixMilli(),
	})
	if err != nil {
		failure := desktopRegistrationStoreErrors.Translate(err)
		if auditErr := s.audit.Fail(ctx, auditID, apperror.CodeOf(failure, "desktop_registration.unavailable")); auditErr != nil {
			return apperror.New("audit.unavailable").Wrap(auditErr)
		}
		return failure
	}
	if !result.AlreadyRevoked && len(result.Sessions) > 0 {
		s.effects.SessionsRevoked(
			ctx, principal.UserID.String(), sessionIDs(result.Sessions), result.TokenHashes,
		)
	}
	return nil
}

var desktopRegistrationStoreErrors = apperror.StoreTable{
	NotFound:    "resource.not_found",
	Unavailable: "desktop_registration.unavailable",
}
