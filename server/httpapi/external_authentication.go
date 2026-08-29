// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package httpapi

import (
	"context"
	"errors"
	"net/http"

	application "github.com/sudosylabs/proctor/server/app"
	"github.com/sudosylabs/proctor/server/model"
)

type externalAuthenticationProviderResponse struct {
	ID          string `json:"id"`
	DisplayName string `json:"display_name"`
	Type        string `json:"type"`
}

type externalAuthenticationStartRequest struct {
	InvitationClaim string `json:"invitation_claim"`
	ReturnTo        string `json:"return_to,omitempty"`
	DeviceID        string `json:"device_id,omitempty"`
	DeviceName      string `json:"device_name,omitempty"`
}

func externalAuthenticationProviderResponses(
	providers []model.ExternalAuthenticationProvider,
) []externalAuthenticationProviderResponse {
	responses := make([]externalAuthenticationProviderResponse, 0, len(providers))
	for _, provider := range providers {
		responses = append(responses, externalAuthenticationProviderResponse{
			ID: provider.Id, DisplayName: provider.DisplayName, Type: provider.Type,
		})
	}
	return responses
}

type externalAuthenticationEntryApplication interface {
	ExternalAuthenticationProviders(context.Context) ([]model.ExternalAuthenticationProvider, error)
	BeginExternalAuthentication(context.Context, application.Invocation, application.BeginExternalAuthenticationCommand) (*model.ExternalAuthenticationStart, error)
	CompleteExternalAuthentication(context.Context, application.Invocation, application.CompleteExternalAuthenticationCommand) (*model.ExternalAuthenticationCompletion, error)
}

type externalAuthenticationResourceModule struct {
	authentication externalAuthenticationEntryApplication
	cookies        browserCookies
}

func externalAuthenticationResource(
	authentication externalAuthenticationEntryApplication,
	cookies browserCookies,
) resource {
	module := externalAuthenticationResourceModule{authentication: authentication, cookies: cookies}
	return newResource(
		"external-authentication",
		publicRoute(
			http.MethodGet,
			apiPath(literal("auth"), literal("providers")),
			[]string{"authentication.internal"},
			module.listProviders,
		),
		protocolRoute(
			"external-authentication-login-redirect",
			RouteProtocolRedirect,
			AuthPublic,
			http.MethodGet,
			apiPath(literal("auth"), literal("providers"), providerID("provider_id"), literal("login")),
			[]string{
				"request.invalid", "authentication.external.request.invalid",
				"authentication.external.provider_not_found", "authentication.rate_limited",
				"authentication.rate_limit_unavailable", "authentication.external.unavailable",
				"authentication.external.rejected", "authentication.internal",
			},
			module.begin,
		),
		protocolRoute(
			"external-authentication-login-post-redirect",
			RouteProtocolRedirect,
			AuthPublic,
			http.MethodPost,
			apiPath(literal("auth"), literal("providers"), providerID("provider_id"), literal("login")),
			[]string{
				"request.invalid", "authentication.external.request.invalid",
				"authentication.external.provider_not_found", "authentication.external.account_not_linked",
				"authentication.rate_limited", "authentication.rate_limit_unavailable",
				"authentication.external.unavailable", "authentication.external.rejected", "authentication.internal",
			},
			module.beginFromBody,
		),
		protocolRoute(
			"external-authentication-callback-redirect",
			RouteProtocolRedirect,
			AuthPublic,
			http.MethodGet,
			apiPath(literal("auth"), literal("providers"), providerID("provider_id"), literal("callback")),
			[]string{
				"request.invalid", "authentication.external.invalid", "authentication.external.provider_not_found",
				"authentication.external.rejected", "authentication.external.unavailable",
				"authentication.external.account_conflict", "authentication.external.account_not_linked",
				"authentication.method.disabled", "authentication.method.last_usable", "authentication.method.not_found",
				"authentication.method.provider_conflict", "authentication.method.conflict", "authentication.method.unavailable",
				"authentication.sessions.maximum_reached", "authentication.internal", "audit.unavailable",
				"authentication.desktop_authorization.account_session_locked",
			},
			module.complete,
		),
	)
}

func (module externalAuthenticationResourceModule) beginFromBody(request operationRequest) (protocolResult, error) {
	providerID, appErr := request.params.RequireProviderId()
	if appErr != nil {
		return protocolResult{}, appErr
	}
	var body externalAuthenticationStartRequest
	if err := request.decodeJSON(&body, "begin_external_authentication"); err != nil {
		return protocolResult{}, err
	}
	if !model.IsValidCredentialToken(body.InvitationClaim) {
		return protocolResult{}, invalidRequestError("invitation_claim", errors.New("must be a valid Invitation claim"))
	}
	start, err := module.authentication.BeginExternalAuthentication(request.context,
		application.NewInvocation(model.Principal{}, request.metadata), application.BeginExternalAuthenticationCommand{
			ProviderID: providerID, InvitationClaim: body.InvitationClaim, ReturnTo: body.ReturnTo,
			ClientType: model.SessionClientWeb, DeviceID: body.DeviceID, DeviceName: body.DeviceName,
			Source: request.request.RemoteAddr,
		})
	if err != nil {
		return protocolResult{}, err
	}
	headers := captureResponseHeaders(func(writer http.ResponseWriter) {
		module.cookies.attachExternalLoginBinding(writer, start.Binding, start.ExpiresAt)
	})
	headers.Set("Cache-Control", "no-store")
	return redirectProtocolResult(start.RedirectURL).withHeaders(headers), nil
}

func (module externalAuthenticationResourceModule) listProviders(request operationRequest) (operationResult, error) {
	providers, err := module.authentication.ExternalAuthenticationProviders(request.context)
	if err != nil {
		return operationResult{}, err
	}
	return jsonResult(
		http.StatusOK,
		externalAuthenticationProviderResponses(providers),
	), nil
}

func (module externalAuthenticationResourceModule) begin(
	request operationRequest,
) (protocolResult, error) {
	providerID, appErr := request.params.RequireProviderId()
	if appErr != nil {
		return protocolResult{}, appErr
	}
	clientType := model.SessionClientWeb
	if request.params.ClientType != "" {
		clientType = model.SessionClientType(request.params.ClientType)
	}
	start, err := module.authentication.BeginExternalAuthentication(
		request.context,
		application.NewInvocation(model.Principal{}, request.metadata),
		application.BeginExternalAuthenticationCommand{
			ProviderID: providerID, ReturnTo: request.params.ReturnTo, ClientType: clientType,
			DeviceID: request.params.DeviceId, DeviceName: request.params.DeviceName, Source: request.request.RemoteAddr,
		},
	)
	if err != nil {
		return protocolResult{}, err
	}
	headers := captureResponseHeaders(func(writer http.ResponseWriter) {
		module.cookies.attachExternalLoginBinding(writer, start.Binding, start.ExpiresAt)
	})
	headers.Set("Cache-Control", "no-store")
	return redirectProtocolResult(start.RedirectURL).withHeaders(headers), nil
}

func (module externalAuthenticationResourceModule) complete(
	request operationRequest,
) (protocolResult, error) {
	clearBinding := captureResponseHeaders(module.cookies.clearExternalLoginBinding)
	providerID, appErr := request.params.RequireProviderId()
	if appErr != nil {
		return protocolResult{}, errorWithHeaders(appErr, clearBinding)
	}
	binding, cookieErr := singleCookieValue(
		request.request,
		BrowserExternalLoginCookieName,
	)
	if cookieErr != nil || binding == "" {
		return protocolResult{}, errorWithHeaders(invalidExternalCallbackError(), clearBinding)
	}
	callback, appErr := externalAuthenticationCallbackFromRequest(request.request)
	if appErr != nil {
		return protocolResult{}, errorWithHeaders(appErr, clearBinding)
	}
	completion, err := module.authentication.CompleteExternalAuthentication(
		request.context,
		application.NewInvocation(model.Principal{}, request.metadata),
		application.CompleteExternalAuthenticationCommand{
			ProviderID: providerID, Binding: binding, Callback: callback,
			Source: request.request.RemoteAddr,
		},
	)
	if err != nil {
		return protocolResult{}, errorWithHeaders(err, clearBinding)
	}
	headers := combineResponseHeaders(clearBinding, captureResponseHeaders(func(writer http.ResponseWriter) {
		module.cookies.attach(writer, completion.Tokens)
	}))
	headers.Set("Cache-Control", "no-store")
	return redirectProtocolResult(completion.ReturnTo).withHeaders(headers), nil
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
