// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package api

import (
	"net/http"

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

func (a *API) InitBootstrap() error {
	if err := a.Register(
		a.BaseRoutes.Bootstrap,
		"",
		http.MethodGet,
		AuthPublic,
		http.HandlerFunc(a.getBootstrapStatus),
	); err != nil {
		return err
	}
	return a.Register(
		a.BaseRoutes.Bootstrap,
		"",
		http.MethodPost,
		AuthPublic,
		http.HandlerFunc(a.bootstrapInstallation),
	)
}

func (a *API) getBootstrapStatus(writer http.ResponseWriter, request *http.Request) {
	status, appErr := a.application.GetInstallationStatus(request.Context())
	if appErr != nil {
		writeApplicationError(writer, request, a.logger, appErr)
		return
	}
	writeJSON(writer, http.StatusOK, status)
}

func (a *API) bootstrapInstallation(writer http.ResponseWriter, request *http.Request) {
	var input bootstrapRequest
	if err := decodeRequestJSON(request, &input); err != nil {
		WriteError(writer, request, invalidRequestError("bootstrapInstallation", err))
		return
	}
	result, appErr := a.application.BootstrapInstallation(
		request.Context(),
		bootstrapInstitution(input.Institution),
		bootstrapAdministrator(input.Administrator),
		input.Password,
		RequestMetadata(request.Context()),
		request.RemoteAddr,
	)
	if appErr != nil {
		writeApplicationError(writer, request, a.logger, appErr)
		return
	}
	writeJSON(writer, http.StatusCreated, result)
}

func bootstrapInstitution(input *bootstrapInstitutionRequest) *model.Institution {
	if input == nil {
		return nil
	}
	return &model.Institution{
		Name: input.Name, DisplayName: input.DisplayName,
		Description: input.Description,
	}
}

func bootstrapAdministrator(input *bootstrapAdministratorRequest) *model.User {
	if input == nil {
		return nil
	}
	return &model.User{
		Username: input.Username, Email: input.Email, DisplayName: input.DisplayName,
		FirstName: input.FirstName, LastName: input.LastName,
		Locale: input.Locale, Timezone: input.Timezone,
	}
}
