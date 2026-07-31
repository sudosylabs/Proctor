// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only
//
// Adapted from Mattermost server/channels/web/params.go and context.go.
// Proctor keeps a deliberately small request-parameter contract and expands it
// only when a registered API resource needs another typed parameter.

package api

import (
	"context"
	"net/http"
	"strings"

	"github.com/gorilla/mux"
	"github.com/sudosylabs/proctor/server/model"
)

type paramsContextKey struct{}

// Params contains normalized variables selected by the matched route. Handlers
// consume this object instead of reaching into mux or parsing URL paths.
type Params struct {
	ProviderId            string
	RoleId                string
	RoleBindingId         string
	UserId                string
	AcademicUnitId        string
	ProgrammeId           string
	ProgrammeLevelId      string
	AcademicPeriodId      string
	ClassId               string
	AffiliationId         string
	AcademicUnitMemberId  string
	ClassMemberId         string
	PersonalAccessTokenId string
	SessionId             string
	ReturnTo              string
	ClientType            string
	DeviceId              string
	DeviceName            string
}

func ParamsFromRequest(request *http.Request) Params {
	variables := mux.Vars(request)
	query := request.URL.Query()
	return Params{
		ProviderId:            strings.ToLower(strings.TrimSpace(variables["provider_id"])),
		RoleId:                strings.TrimSpace(variables["role_id"]),
		RoleBindingId:         strings.TrimSpace(variables["role_binding_id"]),
		UserId:                strings.TrimSpace(variables["user_id"]),
		AcademicUnitId:        strings.TrimSpace(variables["academic_unit_id"]),
		ProgrammeId:           strings.TrimSpace(variables["programme_id"]),
		ProgrammeLevelId:      strings.TrimSpace(variables["programme_level_id"]),
		AcademicPeriodId:      strings.TrimSpace(variables["academic_period_id"]),
		ClassId:               strings.TrimSpace(variables["class_id"]),
		AffiliationId:         strings.TrimSpace(variables["affiliation_id"]),
		AcademicUnitMemberId:  strings.TrimSpace(variables["academic_unit_member_id"]),
		ClassMemberId:         strings.TrimSpace(variables["class_member_id"]),
		PersonalAccessTokenId: strings.TrimSpace(variables["personal_access_token_id"]),
		SessionId:             strings.TrimSpace(variables["session_id"]),
		ReturnTo:              strings.TrimSpace(query.Get("return_to")),
		ClientType:            strings.TrimSpace(query.Get("client_type")),
		DeviceId:              strings.TrimSpace(query.Get("device_id")),
		DeviceName:            strings.TrimSpace(query.Get("device_name")),
	}
}

func (p Params) RequireProviderId() (string, *model.AppError) {
	if len(p.ProviderId) == 0 || len(p.ProviderId) > model.IdentityProviderMaxLength {
		return "", invalidRequestError("provider_id", nil)
	}
	return p.ProviderId, nil
}

func (p Params) RequireUserId() (string, *model.AppError) {
	return requirePathId("user_id", p.UserId)
}

func (p Params) RequireAcademicUnitId() (string, *model.AppError) {
	return requirePathId("academic_unit_id", p.AcademicUnitId)
}

func (p Params) RequireProgrammeId() (string, *model.AppError) {
	return requirePathId("programme_id", p.ProgrammeId)
}

func (p Params) RequireProgrammeLevelId() (string, *model.AppError) {
	return requirePathId("programme_level_id", p.ProgrammeLevelId)
}

func (p Params) RequireAcademicPeriodId() (string, *model.AppError) {
	return requirePathId("academic_period_id", p.AcademicPeriodId)
}

func (p Params) RequireClassId() (string, *model.AppError) {
	return requirePathId("class_id", p.ClassId)
}

func (p Params) RequireAffiliationId() (string, *model.AppError) {
	return requirePathId("affiliation_id", p.AffiliationId)
}

func (p Params) RequireAcademicUnitMemberId() (string, *model.AppError) {
	return requirePathId("academic_unit_member_id", p.AcademicUnitMemberId)
}

func (p Params) RequireClassMemberId() (string, *model.AppError) {
	return requirePathId("class_member_id", p.ClassMemberId)
}

func (p Params) RequirePersonalAccessTokenId() (string, *model.AppError) {
	return requirePathId("personal_access_token_id", p.PersonalAccessTokenId)
}

func (p Params) RequireSessionId() (string, *model.AppError) {
	return requirePathId("session_id", p.SessionId)
}

func RequestParams(ctx context.Context) (Params, bool) {
	params, ok := ctx.Value(paramsContextKey{}).(Params)
	return params, ok
}

func withRequestParams(next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		params := ParamsFromRequest(request)
		ctx := context.WithValue(request.Context(), paramsContextKey{}, params)
		next.ServeHTTP(writer, request.WithContext(ctx))
	})
}

func (p Params) RequireRoleId() (string, *model.AppError) {
	return requirePathId("role_id", p.RoleId)
}

func (p Params) RequireRoleBindingId() (string, *model.AppError) {
	return requirePathId("role_binding_id", p.RoleBindingId)
}

func requirePathId(name, id string) (string, *model.AppError) {
	if !model.IsValidId(id) {
		return "", invalidRequestError(name, nil)
	}
	return id, nil
}

func principalAndRequiredId(
	writer http.ResponseWriter,
	request *http.Request,
	require func(Params) (string, *model.AppError),
) (model.Principal, string, bool) {
	principal, ok := Principal(request.Context())
	if !ok {
		WriteError(writer, request, authenticationRequiredError())
		return model.Principal{}, "", false
	}
	params, ok := RequestParams(request.Context())
	if !ok {
		WriteError(writer, request, invalidRequestError("route_params", nil))
		return model.Principal{}, "", false
	}
	id, appErr := require(params)
	if appErr != nil {
		WriteError(writer, request, appErr)
		return model.Principal{}, "", false
	}
	return principal, id, true
}
