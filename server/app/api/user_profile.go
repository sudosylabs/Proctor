// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package api

import (
	"net/http"
	"strconv"

	"github.com/gorilla/mux"
	application "github.com/sudosylabs/proctor/server/app"
	"github.com/sudosylabs/proctor/server/model"
)

type userProfileResponse struct {
	ID             string `json:"id"`
	CreateAt       int64  `json:"create_at"`
	UpdateAt       int64  `json:"update_at"`
	DeleteAt       int64  `json:"delete_at"`
	Username       string `json:"username"`
	Email          string `json:"email"`
	EmailVerified  bool   `json:"email_verified"`
	DisplayName    string `json:"display_name"`
	FirstName      string `json:"first_name"`
	LastName       string `json:"last_name"`
	Locale         string `json:"locale"`
	Timezone       string `json:"timezone"`
	LastLoginAt    int64  `json:"last_login_at,omitempty"`
	LastActivityAt int64  `json:"last_activity_at,omitempty"`
	DisabledAt     int64  `json:"disabled_at,omitempty"`
}

type updateUserProfileRequest struct {
	Username      *string `json:"username,omitempty"`
	Email         *string `json:"email,omitempty"`
	EmailVerified *bool   `json:"email_verified,omitempty"`
	DisplayName   *string `json:"display_name,omitempty"`
	FirstName     *string `json:"first_name,omitempty"`
	LastName      *string `json:"last_name,omitempty"`
	Locale        *string `json:"locale,omitempty"`
	Timezone      *string `json:"timezone,omitempty"`
}

func (a *API) registerUserProfileRoutes() error {
	routes := []struct {
		base         *mux.Router
		path, method string
		handler      http.HandlerFunc
	}{
		{a.BaseRoutes.Users, "", http.MethodGet, a.searchUsers},
		{a.BaseRoutes.User, "", http.MethodGet, a.getUserProfile},
		{a.BaseRoutes.User, "", http.MethodPatch, a.updateUserProfile},
	}
	for _, route := range routes {
		if err := a.Register(route.base, route.path, route.method, a.APIPrincipalRequired(route.handler)); err != nil {
			return err
		}
	}
	return nil
}

func (a *API) searchUsers(w http.ResponseWriter, r *http.Request) {
	principal, ok := requiredPrincipal(w, r)
	if !ok {
		return
	}
	limit, ok := queryLimit(w, r)
	if !ok {
		return
	}
	includeDisabled, err := strconv.ParseBool(defaultQuery(r, "include_disabled", "false"))
	if err != nil {
		WriteError(w, r, invalidRequestError("include_disabled", err))
		return
	}
	users, err := a.userProfiles.SearchUsers(r.Context(), application.NewInvocation(principal, RequestMetadata(r.Context())), application.SearchUsersQuery{Query: r.URL.Query().Get("q"), AfterUsername: r.URL.Query().Get("after_username"), AfterID: r.URL.Query().Get("after_id"), Limit: limit, IncludeDisabled: includeDisabled})
	if err != nil {
		writeApplicationError(w, r, a.logger, err)
		return
	}
	writeJSON(w, http.StatusOK, userProfileResponses(users))
}

func (a *API) getUserProfile(w http.ResponseWriter, r *http.Request) {
	principal, userID, ok := requiredResourceID(w, r, Params.RequireUserId)
	if !ok {
		return
	}
	user, err := a.userProfiles.GetUserProfile(r.Context(), application.NewInvocation(principal, RequestMetadata(r.Context())), application.GetUserProfileQuery{ID: userID})
	if err != nil {
		writeApplicationError(w, r, a.logger, err)
		return
	}
	writeJSON(w, http.StatusOK, userProfileResponseFromModel(user))
}

func (a *API) updateUserProfile(w http.ResponseWriter, r *http.Request) {
	principal, userID, ok := requiredResourceID(w, r, Params.RequireUserId)
	if !ok {
		return
	}
	var body updateUserProfileRequest
	if !decodeJSON(w, r, &body, "updateUserProfile") {
		return
	}
	user, err := a.userProfiles.UpdateUserProfile(r.Context(), application.NewInvocation(principal, RequestMetadata(r.Context())), application.UpdateUserProfileCommand{ID: userID, Username: body.Username, Email: body.Email, EmailVerified: body.EmailVerified, DisplayName: body.DisplayName, FirstName: body.FirstName, LastName: body.LastName, Locale: body.Locale, Timezone: body.Timezone})
	if err != nil {
		writeApplicationError(w, r, a.logger, err)
		return
	}
	writeJSON(w, http.StatusOK, userProfileResponseFromModel(user))
}

func userProfileResponseFromModel(user *model.User) userProfileResponse {
	if user == nil {
		return userProfileResponse{}
	}
	return userProfileResponse{ID: user.Id, CreateAt: user.CreateAt, UpdateAt: user.UpdateAt, DeleteAt: user.DeleteAt, Username: user.Username, Email: user.Email, EmailVerified: user.EmailVerified, DisplayName: user.DisplayName, FirstName: user.FirstName, LastName: user.LastName, Locale: user.Locale, Timezone: user.Timezone, LastLoginAt: user.LastLoginAt, LastActivityAt: user.LastActivityAt, DisabledAt: user.DisabledAt}
}

func userProfileResponses(users []*model.User) []userProfileResponse {
	result := make([]userProfileResponse, 0, len(users))
	for _, user := range users {
		result = append(result, userProfileResponseFromModel(user))
	}
	return result
}
