// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package httpapi

import (
	"context"
	"net/http"

	application "github.com/sudosylabs/proctor/server/app"
	"github.com/sudosylabs/proctor/server/model"
)

type DesktopRegistrations interface {
	ListDesktopRegistrations(context.Context, application.Invocation, application.ListDesktopRegistrationsQuery) ([]*model.DesktopRegistration, error)
	RevokeDesktopRegistration(context.Context, application.Invocation, application.RevokeDesktopRegistrationCommand) error
}

type desktopRegistrationResponse struct {
	ID               string `json:"id"`
	DisplayName      string `json:"display_name"`
	DesktopRelease   string `json:"desktop_release"`
	DesktopBuildID   string `json:"desktop_build_id"`
	Platform         string `json:"platform"`
	Architecture     string `json:"architecture"`
	RealtimeProtocol int    `json:"realtime_protocol"`
	CreatedAt        int64  `json:"created_at"`
	LastUsedAt       int64  `json:"last_used_at"`
	Status           string `json:"status"`
	Current          bool   `json:"current"`
}

type desktopRegistrationResourceModule struct {
	application DesktopRegistrations
}

func desktopRegistrationResource(application DesktopRegistrations) resource {
	module := desktopRegistrationResourceModule{application: application}
	collection := apiPath(literal("users"), literal("me"), literal("desktop-registrations"))
	item := appendRoutePath(collection, canonicalID("desktop_registration_id"))
	sessionErrors := []string{
		"authentication.required", "authentication.invalid_token",
		"authentication.credential_ambiguous", "desktop_registration.unavailable",
	}
	return newResource(
		"desktop-registrations",
		sessionRoute(http.MethodGet, collection, sessionErrors, module.list),
		strongRecentSessionRoute(http.MethodDelete, item, append(sessionErrors,
			"authentication.strong_required", "authentication.reauthentication_required",
			"request.invalid", "resource.not_found", "audit.unavailable"), module.revoke),
	)
}

func (m desktopRegistrationResourceModule) list(request operationRequest) (operationResult, error) {
	registrations, err := m.application.ListDesktopRegistrations(
		request.context, request.invocation(), application.ListDesktopRegistrationsQuery{},
	)
	if err != nil {
		return operationResult{}, err
	}
	principal, _ := Principal(request.context)
	response := make([]desktopRegistrationResponse, 0, len(registrations))
	for _, registration := range registrations {
		response = append(response, desktopRegistrationResponseFromModel(
			registration, principal.DesktopRegistrationID == registration.ID,
		))
	}
	return jsonResult(http.StatusOK, response).withHeaders(privateNoStoreHeaders()), nil
}

func (m desktopRegistrationResourceModule) revoke(request operationRequest) (operationResult, error) {
	registrationID, err := request.params.RequireDesktopRegistrationId()
	if err != nil {
		return operationResult{}, err
	}
	if err = m.application.RevokeDesktopRegistration(
		request.context, request.invocation(),
		application.RevokeDesktopRegistrationCommand{RegistrationID: registrationID.String()},
	); err != nil {
		return operationResult{}, err
	}
	return noContentResult().withHeaders(privateNoStoreHeaders()), nil
}

func desktopRegistrationResponseFromModel(
	registration *model.DesktopRegistration,
	current bool,
) desktopRegistrationResponse {
	if registration == nil {
		return desktopRegistrationResponse{}
	}
	status := "active"
	if registration.RevokedAt.Valid {
		status = "revoked"
	}
	return desktopRegistrationResponse{
		ID: registration.ID.String(), DisplayName: registration.DisplayName,
		DesktopRelease: registration.DesktopRelease, DesktopBuildID: registration.DesktopBuildID,
		Platform: string(registration.Platform), Architecture: string(registration.Architecture),
		RealtimeProtocol: registration.RealtimeProtocol,
		CreatedAt:        model.MillisFromTime(registration.CreatedAt),
		LastUsedAt:       model.MillisFromTime(registration.LastUsedAt),
		Status:           status, Current: current,
	}
}
