// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package api

import (
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/gorilla/mux"
	application "github.com/sudosylabs/proctor/server/app"
	"github.com/sudosylabs/proctor/server/model"
)

type userProfileResponse struct {
	ID                      string `json:"id"`
	CreateAt                int64  `json:"create_at"`
	UpdateAt                int64  `json:"update_at"`
	DeleteAt                int64  `json:"delete_at"`
	Username                string `json:"username"`
	Email                   string `json:"email"`
	EmailVerified           bool   `json:"email_verified"`
	DisplayName             string `json:"display_name"`
	FirstName               string `json:"first_name"`
	LastName                string `json:"last_name"`
	Locale                  string `json:"locale"`
	Timezone                string `json:"timezone"`
	LastLoginAt             int64  `json:"last_login_at,omitempty"`
	LastActivityAt          int64  `json:"last_activity_at,omitempty"`
	DisabledAt              int64  `json:"disabled_at,omitempty"`
	ProfilePictureURL       string `json:"profile_picture_url"`
	ProfilePictureChangedAt int64  `json:"profile_picture_changed_at,omitempty"`
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
		{a.BaseRoutes.User, "/profile-picture", http.MethodPut, a.uploadProfilePicture},
		{a.BaseRoutes.User, "/profile-picture", http.MethodDelete, a.removeProfilePicture},
		{a.BaseRoutes.User, "/profile-picture", http.MethodGet, a.getProfilePicture},
	}
	for _, route := range routes {
		if err := a.registerLegacyRoute(route.base, route.path, route.method, a.APIPrincipalRequired(route.handler)); err != nil {
			return err
		}
	}
	return nil
}

func (a *API) uploadProfilePicture(w http.ResponseWriter, r *http.Request) {
	principal, userID, ok := requiredResourceID(w, r, Params.RequireUserId)
	if !ok {
		return
	}
	expectedSHA256, err := profilePictureIfMatch(r.Header.Get("If-Match"), false)
	if err != nil {
		WriteError(w, r, invalidRequestError("If-Match", err))
		return
	}
	user, err := a.userProfiles.UploadProfilePicture(r.Context(), application.NewInvocation(principal, RequestMetadata(r.Context())), application.UploadProfilePictureCommand{UserID: userID, ExpectedSHA256: expectedSHA256, Body: r.Body, Size: r.ContentLength})
	if err != nil {
		writeApplicationError(w, r, a.logger, err)
		return
	}
	writeJSON(w, http.StatusOK, userProfileResponseFromModel(user))
}

func (a *API) removeProfilePicture(w http.ResponseWriter, r *http.Request) {
	principal, userID, ok := requiredResourceID(w, r, Params.RequireUserId)
	if !ok {
		return
	}
	expectedSHA256, err := profilePictureIfMatch(r.Header.Get("If-Match"), true)
	if err != nil {
		WriteError(w, r, invalidRequestError("If-Match", err))
		return
	}
	user, appErr := a.userProfiles.RemoveProfilePicture(r.Context(), application.NewInvocation(principal, RequestMetadata(r.Context())), application.RemoveProfilePictureCommand{UserID: userID, ExpectedSHA256: expectedSHA256})
	if appErr != nil {
		writeApplicationError(w, r, a.logger, appErr)
		return
	}
	writeJSON(w, http.StatusOK, userProfileResponseFromModel(user))
}

func profilePictureIfMatch(header string, required bool) (string, error) {
	header = strings.TrimSpace(header)
	if header == "" && !required {
		return "", nil
	}
	if len(header) != 66 || header[0] != '"' || header[len(header)-1] != '"' {
		return "", fmt.Errorf("a single strong profile-picture ETag is required")
	}
	checksum := header[1 : len(header)-1]
	if strings.ToLower(checksum) != checksum {
		return "", fmt.Errorf("profile-picture ETag is not canonical")
	}
	if _, err := hex.DecodeString(checksum); err != nil {
		return "", fmt.Errorf("profile-picture ETag is invalid: %w", err)
	}
	return checksum, nil
}

func (a *API) getProfilePicture(w http.ResponseWriter, r *http.Request) {
	principal, userID, ok := requiredResourceID(w, r, Params.RequireUserId)
	if !ok {
		return
	}
	size, err := strconv.Atoi(defaultQuery(r, "size", "256"))
	if err != nil {
		WriteError(w, r, invalidRequestError("size", err))
		return
	}
	content, appErr := a.userProfiles.GetProfilePicture(r.Context(), application.NewInvocation(principal, RequestMetadata(r.Context())), application.GetProfilePictureQuery{UserID: userID, Size: size})
	if appErr != nil {
		writeApplicationError(w, r, a.logger, appErr)
		return
	}
	defer content.Body.Close()
	w.Header().Set("ETag", content.ETag)
	w.Header().Set("Cache-Control", "private, max-age=86400")
	w.Header().Set("Content-Type", content.MediaType)
	w.Header().Set("Content-Length", strconv.FormatInt(content.Size, 10))
	if etagMatches(r.Header.Get("If-None-Match"), content.ETag) {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	w.WriteHeader(http.StatusOK)
	_, _ = io.Copy(w, content.Body)
}

func etagMatches(header, current string) bool {
	current = strings.TrimPrefix(strings.TrimSpace(current), "W/")
	for _, candidate := range strings.Split(header, ",") {
		candidate = strings.TrimSpace(candidate)
		if candidate == "*" || strings.TrimPrefix(candidate, "W/") == current {
			return true
		}
	}
	return false
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
	pictureURL := "/api/v1/users/" + user.ID.String() + "/profile-picture"
	if user.ProfilePictureChangedAt.Valid {
		pictureURL += "?v=" + strconv.FormatInt(user.Revision, 10)
	}
	return userProfileResponse{
		ID:                      user.ID.String(),
		CreateAt:                model.MillisFromTime(user.CreatedAt),
		UpdateAt:                model.MillisFromTime(user.UpdatedAt),
		DeleteAt:                user.ArchivedAt.Millis(),
		Username:                user.Username,
		Email:                   user.Email,
		EmailVerified:           user.EmailVerified,
		DisplayName:             user.DisplayName,
		FirstName:               user.FirstName,
		LastName:                user.LastName,
		Locale:                  user.Locale,
		Timezone:                user.Timezone,
		LastLoginAt:             user.LastLoginAt.Millis(),
		LastActivityAt:          user.LastActivityAt.Millis(),
		DisabledAt:              user.DisabledAt.Millis(),
		ProfilePictureURL:       pictureURL,
		ProfilePictureChangedAt: user.ProfilePictureChangedAt.Millis(),
	}
}

func userProfileResponses(users []*model.User) []userProfileResponse {
	result := make([]userProfileResponse, 0, len(users))
	for _, user := range users {
		result = append(result, userProfileResponseFromModel(user))
	}
	return result
}
