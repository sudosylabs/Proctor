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

func (a *API) InitBootstrap() error {
	if err := a.Register(
		a.BaseRoutes.Bootstrap,
		"",
		http.MethodGet,
		a.APIHandler(http.HandlerFunc(a.getBootstrapStatus)),
	); err != nil {
		return err
	}
	return a.Register(
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
	// Preserve existing v1 bootstrap response shape for integration clients.
	writeJSON(writer, http.StatusCreated, result)
}
