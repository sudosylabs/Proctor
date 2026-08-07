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

	application "github.com/sudosylabs/proctor/server/app"
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
	status, err := a.application.GetMFAStatus(
		request.Context(),
		application.NewInvocation(principal, RequestMetadata(request.Context())),
		application.GetMFAStatusQuery{},
	)
	if err != nil {
		writeApplicationError(writer, request, a.logger, err)
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
	setup, err := a.application.SetupMFA(
		request.Context(),
		application.NewInvocation(principal, RequestMetadata(request.Context())),
		application.SetupMFACommand{},
	)
	if err != nil {
		writeApplicationError(writer, request, a.logger, err)
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
	activation, err := a.application.ActivateMFA(
		request.Context(),
		application.NewInvocation(principal, RequestMetadata(request.Context())),
		application.ActivateMFACommand{Code: code},
	)
	if err != nil {
		writeApplicationError(writer, request, a.logger, err)
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
	session, err := a.application.ChallengeMFA(
		request.Context(),
		application.NewInvocation(principal, RequestMetadata(request.Context())),
		application.ChallengeMFACommand{Code: code},
	)
	if err != nil {
		writeApplicationError(writer, request, a.logger, err)
		return
	}
	writer.Header().Set("Cache-Control", "no-store")
	writeJSON(writer, http.StatusOK, sessionResponseFromModel(session))
}

func (a *API) regenerateMFARecoveryCodes(
	writer http.ResponseWriter,
	request *http.Request,
) {
	principal, ok := requiredPrincipal(writer, request)
	if !ok {
		return
	}
	codes, err := a.application.RegenerateMFARecoveryCodes(
		request.Context(),
		application.NewInvocation(principal, RequestMetadata(request.Context())),
		application.RegenerateMFARecoveryCodesCommand{},
	)
	if err != nil {
		writeApplicationError(writer, request, a.logger, err)
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
	if err := a.application.DisableMFA(
		request.Context(),
		application.NewInvocation(principal, RequestMetadata(request.Context())),
		application.DisableMFACommand{},
	); err != nil {
		writeApplicationError(writer, request, a.logger, err)
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
