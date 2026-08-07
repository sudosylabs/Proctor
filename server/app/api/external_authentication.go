// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package api

import (
	"net/http"

	application "github.com/sudosylabs/proctor/server/app"
	"github.com/sudosylabs/proctor/server/model"
)

func (a *API) InitExternalAuthentication() error {
	if err := a.Register(
		a.BaseRoutes.IdentityProviders,
		"",
		http.MethodGet,
		a.APIHandler(http.HandlerFunc(a.listExternalAuthenticationProviders)),
	); err != nil {
		return err
	}
	if err := a.Register(
		a.BaseRoutes.IdentityProvider,
		"/login",
		http.MethodGet,
		a.APIHandler(http.HandlerFunc(a.beginExternalAuthentication)),
	); err != nil {
		return err
	}
	return a.Register(
		a.BaseRoutes.IdentityProvider,
		"/callback",
		http.MethodGet,
		a.APIHandler(http.HandlerFunc(a.completeExternalAuthentication)),
	)
}

func (a *API) listExternalAuthenticationProviders(
	writer http.ResponseWriter,
	request *http.Request,
) {
	writeJSON(
		writer,
		http.StatusOK,
		a.application.ExternalAuthenticationProviders(),
	)
}

func (a *API) beginExternalAuthentication(
	writer http.ResponseWriter,
	request *http.Request,
) {
	params, ok := RequestParams(request.Context())
	if !ok {
		WriteError(writer, request, invalidRequestError("route_params", nil))
		return
	}
	providerID, appErr := params.RequireProviderId()
	if appErr != nil {
		WriteError(writer, request, appErr)
		return
	}
	clientType := model.SessionClientDesktop
	if params.ClientType != "" {
		clientType = model.SessionClientType(params.ClientType)
	}
	start, err := a.application.BeginExternalAuthentication(
		request.Context(),
		application.NewInvocation(model.Principal{}, RequestMetadata(request.Context())),
		application.BeginExternalAuthenticationCommand{
			ProviderID: providerID, ReturnTo: params.ReturnTo, ClientType: clientType,
			DeviceID: params.DeviceId, DeviceName: params.DeviceName, Source: request.RemoteAddr,
		},
	)
	if err != nil {
		writeApplicationError(writer, request, a.logger, err)
		return
	}
	a.cookies.attachExternalLoginBinding(
		writer,
		start.Binding,
		start.ExpiresAt,
	)
	writer.Header().Set("Cache-Control", "no-store")
	http.Redirect(
		writer,
		request,
		start.RedirectURL,
		http.StatusSeeOther,
	)
}

func (a *API) completeExternalAuthentication(
	writer http.ResponseWriter,
	request *http.Request,
) {
	a.cookies.clearExternalLoginBinding(writer)
	params, ok := RequestParams(request.Context())
	if !ok {
		WriteError(writer, request, invalidRequestError("route_params", nil))
		return
	}
	providerID, appErr := params.RequireProviderId()
	if appErr != nil {
		WriteError(writer, request, appErr)
		return
	}
	binding, cookieErr := singleCookieValue(
		request,
		BrowserExternalLoginCookieName,
	)
	if cookieErr != nil || binding == "" {
		WriteError(
			writer,
			request,
			invalidExternalCallbackError(),
		)
		return
	}
	callback, appErr := externalAuthenticationCallbackFromRequest(request)
	if appErr != nil {
		WriteError(writer, request, appErr)
		return
	}
	completion, err := a.application.CompleteExternalAuthentication(
		request.Context(),
		application.NewInvocation(model.Principal{}, RequestMetadata(request.Context())),
		application.CompleteExternalAuthenticationCommand{
			ProviderID: providerID, Binding: binding, Callback: callback,
			Source: request.RemoteAddr,
		},
	)
	if err != nil {
		writeApplicationError(writer, request, a.logger, err)
		return
	}
	a.cookies.attach(writer, completion.Tokens)
	writer.Header().Set("Cache-Control", "no-store")
	http.Redirect(
		writer,
		request,
		completion.ReturnTo,
		http.StatusSeeOther,
	)
}

func externalAuthenticationCallbackFromRequest(
	request *http.Request,
) (model.ExternalAuthenticationCallback, error) {
	valuesFromRequest := request.URL.Query()
	if len(valuesFromRequest) == 0 ||
		len(valuesFromRequest) > model.ExternalCallbackMaxFields {
		return model.ExternalAuthenticationCallback{}, invalidExternalCallbackError()
	}
	values := make(map[string][]string, len(valuesFromRequest))
	for key, items := range valuesFromRequest {
		if key == "" || len(key) > model.ExternalCallbackMaxKeyLength ||
			len(items) == 0 || len(items) > model.ExternalCallbackMaxValues {
			return model.ExternalAuthenticationCallback{}, invalidExternalCallbackError()
		}
		values[key] = make([]string, len(items))
		for index, item := range items {
			if len(item) > model.ExternalCallbackMaxValueLength {
				return model.ExternalAuthenticationCallback{}, invalidExternalCallbackError()
			}
			values[key][index] = item
		}
	}
	return model.ExternalAuthenticationCallback{Values: values}, nil
}

func invalidExternalCallbackError() error {
	return application.NewError("authentication.external.invalid")
}
