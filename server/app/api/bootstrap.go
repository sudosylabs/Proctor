// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package api

import (
	"net/http"

	application "github.com/sudosylabs/proctor/server/app"
	"github.com/sudosylabs/proctor/server/model"
)

type bootstrapRequest struct {
	Institution   *bootstrapInstitutionRequest   `json:"institution"`
	Administrator *bootstrapAdministratorRequest `json:"administrator"`
	Password      string                         `json:"password"`
}

type bootstrapInstitutionRequest struct {
	Name        string `json:"name"`
	DisplayName string `json:"display_name"`
	Description string `json:"description,omitempty"`
}

type bootstrapAdministratorRequest struct {
	Username    string `json:"username"`
	Email       string `json:"email"`
	DisplayName string `json:"display_name,omitempty"`
	FirstName   string `json:"first_name,omitempty"`
	LastName    string `json:"last_name,omitempty"`
	Locale      string `json:"locale,omitempty"`
	Timezone    string `json:"timezone,omitempty"`
}

type installationStatusResponse struct {
	Initialized bool `json:"initialized"`
}

// installationStateResponse is the public bootstrap marker projection.
type installationStateResponse struct {
	InitializedAt       int64  `json:"initialized_at"`
	InstitutionID       string `json:"institution_id"`
	AdministratorUserID string `json:"administrator_user_id"`
}

// installationBootstrapResponse is the transport-owned success body for the
// one-time bootstrap command. Field names match the historical v1 envelope so
// existing clients and integration tests keep working while domain models no
// longer serialize directly.
type installationBootstrapResponse struct {
	State         *installationStateResponse `json:"state"`
	Institution   *institutionResponse       `json:"institution"`
	Administrator *userProfileResponse       `json:"administrator"`
	Role          *roleResponse              `json:"role"`
	RoleBinding   *roleBindingResponse       `json:"role_binding"`
}

func installationStateResponseFromModel(state *model.InstallationState) *installationStateResponse {
	if state == nil {
		return nil
	}
	return &installationStateResponse{
		InitializedAt:       model.MillisFromTime(state.InitializedAt),
		InstitutionID:       state.InstitutionID.String(),
		AdministratorUserID: state.AdministratorUserID.String(),
	}
}

func installationBootstrapResponseFromModel(result *model.InstallationBootstrapResult) installationBootstrapResponse {
	if result == nil {
		return installationBootstrapResponse{}
	}
	var institution *institutionResponse
	if result.Institution != nil {
		mapped := institutionResponseFromModel(result.Institution)
		institution = &mapped
	}
	var administrator *userProfileResponse
	if result.Administrator != nil {
		mapped := userProfileResponseFromModel(result.Administrator)
		administrator = &mapped
	}
	var role *roleResponse
	if result.Role != nil {
		mapped := roleResponseFromModel(result.Role)
		role = &mapped
	}
	var binding *roleBindingResponse
	if result.RoleBinding != nil {
		mapped := roleBindingResponseFromModel(result.RoleBinding)
		binding = &mapped
	}
	return installationBootstrapResponse{
		State:         installationStateResponseFromModel(result.State),
		Institution:   institution,
		Administrator: administrator,
		Role:          role,
		RoleBinding:   binding,
	}
}

func (a *API) InitBootstrap() error {
	if err := a.registerLegacyRoute(
		a.BaseRoutes.Bootstrap,
		"",
		http.MethodGet,
		a.APIHandler(http.HandlerFunc(a.getBootstrapStatus)),
	); err != nil {
		return err
	}
	return a.registerLegacyRoute(
		a.BaseRoutes.Bootstrap,
		"",
		http.MethodPost,
		a.APIHandler(http.HandlerFunc(a.bootstrapInstallation)),
	)
}

func (a *API) getBootstrapStatus(writer http.ResponseWriter, request *http.Request) {
	status, err := a.bootstrap.GetInstallationStatus(request.Context(), application.GetInstallationStatusQuery{})
	if err != nil {
		writeApplicationError(writer, request, a.logger, err)
		return
	}
	writeJSON(writer, http.StatusOK, installationStatusResponse{Initialized: status.Initialized})
}

func (a *API) bootstrapInstallation(writer http.ResponseWriter, request *http.Request) {
	var input bootstrapRequest
	if err := decodeRequestJSON(request, &input); err != nil {
		writeApplicationError(writer, request, a.logger, application.NewError("request.invalid").WithField("field", "body"))
		return
	}
	if input.Institution == nil || input.Administrator == nil {
		writeApplicationError(writer, request, a.logger, application.NewError("request.invalid").WithField("field", "bootstrap"))
		return
	}
	result, err := a.bootstrap.BootstrapInstallation(
		request.Context(),
		application.NewInvocation(model.Principal{}, RequestMetadata(request.Context())),
		application.BootstrapInstallationCommand{
			InstitutionName:          input.Institution.Name,
			InstitutionDisplayName:   input.Institution.DisplayName,
			InstitutionDescription:   input.Institution.Description,
			AdministratorUsername:    input.Administrator.Username,
			AdministratorEmail:       input.Administrator.Email,
			AdministratorDisplayName: input.Administrator.DisplayName,
			AdministratorFirstName:   input.Administrator.FirstName,
			AdministratorLastName:    input.Administrator.LastName,
			AdministratorLocale:      input.Administrator.Locale,
			AdministratorTimezone:    input.Administrator.Timezone,
			Password:                 input.Password,
			Source:                   request.RemoteAddr,
		},
	)
	if err != nil {
		writeApplicationError(writer, request, a.logger, err)
		return
	}
	writeJSON(writer, http.StatusCreated, installationBootstrapResponseFromModel(result))
}
