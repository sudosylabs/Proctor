// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only
//
// Adapted from Mattermost server/channels/api4/user.go MFA handlers. Proctor
// exposes the assurance transition explicitly, returns setup secrets and
// recovery codes only once, and classifies sensitive mutations with the
// strong/recent session wrappers.

package api

import (
	"net/http"

	"github.com/sudosylabs/proctor/server/model"
)

type mfaCodeRequest struct {
	Code string `json:"code"`
}

type mfaRecoveryCodesResponse struct {
	RecoveryCodes []string `json:"recovery_codes"`
}

func (a *API) InitMFA() error {
	routes := []struct {
		path    string
		method  string
		handler *Handler
	}{
		{
			"",
			http.MethodGet,
			a.APISessionRequired(http.HandlerFunc(a.getMFAStatus)),
		},
		{
			"/setup",
			http.MethodPost,
			a.APIRecentSessionRequired(http.HandlerFunc(a.setupMFA)),
		},
		{
			"/activate",
			http.MethodPost,
			a.APIRecentSessionRequired(http.HandlerFunc(a.activateMFA)),
		},
		{
			"/challenge",
			http.MethodPost,
			a.APISessionRequired(http.HandlerFunc(a.challengeMFA)),
		},
		{
			"/recovery-codes/regenerate",
			http.MethodPost,
			a.APIStrongRecentSessionRequired(
				http.HandlerFunc(a.regenerateMFARecoveryCodes),
			),
		},
		{
			"/disable",
			http.MethodPost,
			a.APIStrongRecentSessionRequired(http.HandlerFunc(a.disableMFA)),
		},
	}
	for _, route := range routes {
		if err := a.Register(
			a.BaseRoutes.MFA,
			route.path,
			route.method,
			route.handler,
		); err != nil {
			return err
		}
	}
	return nil
}

func (a *API) getMFAStatus(writer http.ResponseWriter, request *http.Request) {
	principal, ok := requiredPrincipal(writer, request)
	if !ok {
		return
	}
	status, appErr := a.application.GetMFAStatus(
		request.Context(),
		principal,
	)
	if appErr != nil {
		writeApplicationError(writer, request, a.logger, appErr)
		return
	}
	writer.Header().Set("Cache-Control", "no-store")
	writeJSON(writer, http.StatusOK, status)
}

func (a *API) setupMFA(writer http.ResponseWriter, request *http.Request) {
	principal, ok := requiredPrincipal(writer, request)
	if !ok {
		return
	}
	setup, appErr := a.application.SetupMFA(
		request.Context(),
		principal,
		RequestMetadata(request.Context()),
		"",
	)
	if appErr != nil {
		writeApplicationError(writer, request, a.logger, appErr)
		return
	}
	writer.Header().Set("Cache-Control", "no-store")
	writeJSON(writer, http.StatusCreated, setup)
}

func (a *API) activateMFA(writer http.ResponseWriter, request *http.Request) {
	principal, code, ok := a.mfaCode(writer, request, "activateMFA")
	if !ok {
		return
	}
	activation, appErr := a.application.ActivateMFA(
		request.Context(),
		principal,
		RequestMetadata(request.Context()),
		code,
	)
	if appErr != nil {
		writeApplicationError(writer, request, a.logger, appErr)
		return
	}
	writer.Header().Set("Cache-Control", "no-store")
	writeJSON(writer, http.StatusOK, activation)
}

func (a *API) challengeMFA(writer http.ResponseWriter, request *http.Request) {
	principal, code, ok := a.mfaCode(writer, request, "challengeMFA")
	if !ok {
		return
	}
	session, appErr := a.application.ChallengeMFA(
		request.Context(),
		principal,
		RequestMetadata(request.Context()),
		code,
	)
	if appErr != nil {
		writeApplicationError(writer, request, a.logger, appErr)
		return
	}
	writer.Header().Set("Cache-Control", "no-store")
	writeJSON(writer, http.StatusOK, session)
}

func (a *API) regenerateMFARecoveryCodes(
	writer http.ResponseWriter,
	request *http.Request,
) {
	principal, ok := requiredPrincipal(writer, request)
	if !ok {
		return
	}
	codes, appErr := a.application.RegenerateMFARecoveryCodes(
		request.Context(),
		principal,
		RequestMetadata(request.Context()),
	)
	if appErr != nil {
		writeApplicationError(writer, request, a.logger, appErr)
		return
	}
	writer.Header().Set("Cache-Control", "no-store")
	writeJSON(writer, http.StatusOK, mfaRecoveryCodesResponse{
		RecoveryCodes: codes,
	})
}

func (a *API) disableMFA(writer http.ResponseWriter, request *http.Request) {
	principal, ok := requiredPrincipal(writer, request)
	if !ok {
		return
	}
	if appErr := a.application.DisableMFA(
		request.Context(),
		principal,
		RequestMetadata(request.Context()),
	); appErr != nil {
		writeApplicationError(writer, request, a.logger, appErr)
		return
	}
	writer.Header().Set("Cache-Control", "no-store")
	writer.WriteHeader(http.StatusNoContent)
}

func (a *API) mfaCode(
	writer http.ResponseWriter,
	request *http.Request,
	where string,
) (model.Principal, string, bool) {
	principal, ok := requiredPrincipal(writer, request)
	if !ok {
		return model.Principal{}, "", false
	}
	var input mfaCodeRequest
	if err := decodeRequestJSON(request, &input); err != nil {
		WriteError(writer, request, invalidRequestError(where, err))
		return model.Principal{}, "", false
	}
	return principal, input.Code, true
}
