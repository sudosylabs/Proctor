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

// desktopRegistrationServiceDependencies are the collaborators of desktopRegistrationService;
// newDesktopRegistrationService validates them once and desktopRegistrationService keeps them in deps.
type desktopRegistrationServiceDependencies struct {
	Registrations           store.DesktopRegistrationStore
	Audit                   mutationAuditor
	Effects                 authenticationSecurityEffects
	RecentAuthenticationTTL time.Duration
	Now                     func() time.Time
}

type desktopRegistrationService struct {
	deps desktopRegistrationServiceDependencies
}

func newDesktopRegistrationService(deps desktopRegistrationServiceDependencies) (*desktopRegistrationService, error) {
	if deps.Registrations == nil || deps.Audit == nil || deps.Effects == nil || deps.RecentAuthenticationTTL <= 0 || deps.Now == nil {
		return nil, errors.New("Desktop Registration dependencies are invalid")
	}
	return &desktopRegistrationService{deps: deps}, nil
}

func (s *desktopRegistrationService) ListDesktopRegistrations(
	ctx context.Context,
	invocation invocation.Invocation,
	_ ListDesktopRegistrationsQuery,
) ([]*model.DesktopRegistration, error) {
	principal := invocation.Principal()
	if err := requireInteractiveSession(principal, false, s.deps.Now(), s.deps.RecentAuthenticationTTL); err != nil {
		return nil, err
	}
	registrations, err := s.deps.Registrations.ListByUser(ctx, principal.UserID)
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
	at := model.TimeUTC(s.deps.Now())
	if err := requireInteractiveSession(principal, true, at, s.deps.RecentAuthenticationTTL); err != nil {
		return err
	}
	registrationID, err := model.ParseDesktopRegistrationID(strings.TrimSpace(command.RegistrationID))
	if err != nil {
		return apperror.New("request.invalid").WithField("field", "desktop_registration_id")
	}
	auditID, err := s.deps.Audit.Begin(
		ctx, invocation, model.ActionDesktopRegistrationRevoke,
		model.Resource{Type: model.ResourceUser, ID: principal.UserID.String()},
		"revoke", map[string]any{"registration_id": registrationID.String()}, nil,
	)
	if err != nil {
		return err
	}
	result, err := s.deps.Registrations.Revoke(ctx, &store.DesktopRegistrationRevocation{
		RegistrationID: registrationID, UserID: principal.UserID,
		RevokedAt: at.UnixMilli(), AuditEventID: auditID, AuditAt: at.UnixMilli(),
	})
	if err != nil {
		failure := desktopRegistrationStoreErrors.Translate(err)
		if auditErr := s.deps.Audit.Fail(ctx, auditID, apperror.CodeOf(failure, "desktop_registration.unavailable")); auditErr != nil {
			return apperror.New("audit.unavailable").Wrap(auditErr)
		}
		return failure
	}
	if !result.AlreadyRevoked && len(result.Sessions) > 0 {
		s.deps.Effects.SessionsRevoked(
			ctx, principal.UserID.String(), sessionIDs(result.Sessions), result.TokenHashes,
		)
	}
	return nil
}

var desktopRegistrationStoreErrors = apperror.StoreTable{
	NotFound:    "resource.not_found",
	Unavailable: "desktop_registration.unavailable",
}
