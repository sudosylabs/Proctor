// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package api

import (
	"encoding/hex"
	"fmt"
	"net/http"
	"strconv"
	"strings"

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

type userProfileResourceModule struct {
	profiles UserProfileApplication
}

func userProfileResource(profiles UserProfileApplication) resource {
	module := userProfileResourceModule{profiles: profiles}
	user := apiPath(literal("users"), canonicalID("user_id"))
	picture := appendRoutePath(user, literal("profile-picture"))
	return newResource(
		"user-profiles",
		principalRoute(http.MethodGet, apiPath(literal("users")), userProfileReadCodes("request.invalid", "user.invalid"), module.search),
		principalRoute(http.MethodGet, apiPath(literal("users"), literal("me")), userProfileReadCodes("resource.not_found"), module.current),
		principalRoute(http.MethodGet, user, userProfileReadCodes("request.invalid", "resource.not_found"), module.get),
		principalRoute(http.MethodPatch, user, userProfileMutationCodes("request.invalid", "resource.not_found", "user.invalid", "user.conflict"), module.update),
		protocolRoute("profile-picture-download", RouteProtocolBinaryDownload, AuthPrincipalRequired, http.MethodGet, picture, userProfilePrincipalCodes("request.invalid", "resource.not_found", "profile_picture.unavailable"), module.downloadPicture),
		protocolRoute("profile-picture-upload", RouteProtocolStreamingUpload, AuthPrincipalRequired, http.MethodPut, picture, userProfilePrincipalMutationCodes("request.invalid", "resource.not_found", "profile_picture.invalid", "profile_picture.unavailable", "user.conflict"), module.uploadPicture),
		principalRoute(http.MethodDelete, picture, userProfilePrincipalMutationCodes("request.invalid", "resource.not_found", "profile_picture.unavailable", "user.conflict"), module.removePicture),
	)
}

func userProfileReadCodes(extra ...string) []string {
	return append(userProfilePrincipalCodes(extra...), "administration.unavailable")
}

func userProfilePrincipalCodes(extra ...string) []string {
	codes := []string{
		"authentication.required", "authentication.invalid_token", "authentication.credential_ambiguous",
		"authorization.denied", "authorization.request.invalid", "authorization.unavailable",
	}
	return append(codes, extra...)
}

func userProfileMutationCodes(extra ...string) []string {
	return append(userProfilePrincipalMutationCodes(extra...), "administration.unavailable")
}

func userProfilePrincipalMutationCodes(extra ...string) []string {
	codes := []string{
		"authentication.required", "authentication.invalid_token", "authentication.credential_ambiguous",
		"authentication.csrf.invalid", "authorization.denied", "authorization.request.invalid",
		"authorization.unavailable", "audit.unavailable",
	}
	return append(codes, extra...)
}

func (module userProfileResourceModule) uploadPicture(request operationRequest) (protocolResult, error) {
	userID, err := request.params.RequireUserId()
	if err != nil {
		return protocolResult{}, err
	}
	expectedSHA256, err := profilePictureIfMatch(request.request.Header.Get("If-Match"), false)
	if err != nil {
		return protocolResult{}, invalidRequestError("If-Match", err)
	}
	user, err := module.profiles.UploadProfilePicture(request.context, request.invocation(), application.UploadProfilePictureCommand{UserID: userID, ExpectedSHA256: expectedSHA256, Body: request.request.Body, Size: request.request.ContentLength})
	if err != nil {
		return protocolResult{}, err
	}
	return streamingUploadProtocolResult(http.StatusOK, userProfileResponseFromModel(user)), nil
}

func (module userProfileResourceModule) removePicture(request operationRequest) (operationResult, error) {
	userID, err := request.params.RequireUserId()
	if err != nil {
		return operationResult{}, err
	}
	expectedSHA256, err := profilePictureIfMatch(request.request.Header.Get("If-Match"), true)
	if err != nil {
		return operationResult{}, invalidRequestError("If-Match", err)
	}
	user, appErr := module.profiles.RemoveProfilePicture(request.context, request.invocation(), application.RemoveProfilePictureCommand{UserID: userID, ExpectedSHA256: expectedSHA256})
	if appErr != nil {
		return operationResult{}, appErr
	}
	return jsonResult(http.StatusOK, userProfileResponseFromModel(user)), nil
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

func (module userProfileResourceModule) downloadPicture(request operationRequest) (protocolResult, error) {
	userID, err := request.params.RequireUserId()
	if err != nil {
		return protocolResult{}, err
	}
	size, err := strconv.Atoi(defaultQuery(request.request, "size", "256"))
	if err != nil {
		return protocolResult{}, invalidRequestError("size", err)
	}
	content, appErr := module.profiles.GetProfilePicture(request.context, request.invocation(), application.GetProfilePictureQuery{UserID: userID, Size: size})
	if appErr != nil {
		return protocolResult{}, appErr
	}
	headers := http.Header{
		"ETag":          []string{content.ETag},
		"Cache-Control": []string{"private, max-age=86400"},
		"Content-Type":  []string{content.MediaType},
	}
	if etagMatches(request.request.Header.Get("If-None-Match"), content.ETag) {
		_ = content.Body.Close()
		return notModifiedProtocolResult(content.Size).withHeaders(headers), nil
	}
	return binaryDownloadProtocolResult(content.Body, content.Size).withHeaders(headers), nil
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

func (module userProfileResourceModule) search(request operationRequest) (operationResult, error) {
	limit, err := request.queryLimit()
	if err != nil {
		return operationResult{}, err
	}
	includeDisabled, err := strconv.ParseBool(defaultQuery(request.request, "include_disabled", "false"))
	if err != nil {
		return operationResult{}, invalidRequestError("include_disabled", err)
	}
	users, err := module.profiles.SearchUsers(request.context, request.invocation(), application.SearchUsersQuery{Query: request.request.URL.Query().Get("q"), AfterUsername: request.request.URL.Query().Get("after_username"), AfterID: request.request.URL.Query().Get("after_id"), Limit: limit, IncludeDisabled: includeDisabled})
	if err != nil {
		return operationResult{}, err
	}
	return jsonResult(http.StatusOK, userProfileResponses(users)), nil
}

func (module userProfileResourceModule) current(request operationRequest) (operationResult, error) {
	user, err := module.profiles.GetUserProfile(request.context, request.invocation(), application.GetUserProfileQuery{ID: request.principal.UserID.String()})
	if err != nil {
		return operationResult{}, err
	}
	return jsonResult(http.StatusOK, userProfileResponseFromModel(user)), nil
}

func (module userProfileResourceModule) get(request operationRequest) (operationResult, error) {
	userID, err := request.params.RequireUserId()
	if err != nil {
		return operationResult{}, err
	}
	user, err := module.profiles.GetUserProfile(request.context, request.invocation(), application.GetUserProfileQuery{ID: userID})
	if err != nil {
		return operationResult{}, err
	}
	return jsonResult(http.StatusOK, userProfileResponseFromModel(user)), nil
}

func (module userProfileResourceModule) update(request operationRequest) (operationResult, error) {
	userID, err := request.params.RequireUserId()
	if err != nil {
		return operationResult{}, err
	}
	var body updateUserProfileRequest
	if err := request.decodeJSON(&body, "updateUserProfile"); err != nil {
		return operationResult{}, err
	}
	user, err := module.profiles.UpdateUserProfile(request.context, request.invocation(), application.UpdateUserProfileCommand{ID: userID, Username: body.Username, Email: body.Email, EmailVerified: body.EmailVerified, DisplayName: body.DisplayName, FirstName: body.FirstName, LastName: body.LastName, Locale: body.Locale, Timezone: body.Timezone})
	if err != nil {
		return operationResult{}, err
	}
	return jsonResult(http.StatusOK, userProfileResponseFromModel(user)), nil
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
