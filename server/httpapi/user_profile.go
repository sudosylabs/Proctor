// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package httpapi

import (
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	application "github.com/sudosylabs/proctor/server/app"
	"github.com/sudosylabs/proctor/server/model"
)

type userProfileResponse struct {
	ID                      string  `json:"id"`
	CreateAt                int64   `json:"create_at"`
	UpdateAt                int64   `json:"update_at"`
	DeleteAt                int64   `json:"delete_at"`
	Username                string  `json:"username"`
	Email                   *string `json:"email,omitempty"`
	EmailVerified           *bool   `json:"email_verified,omitempty"`
	DisplayName             string  `json:"display_name"`
	FirstName               string  `json:"first_name"`
	LastName                string  `json:"last_name"`
	Locale                  *string `json:"locale,omitempty"`
	Timezone                *string `json:"timezone,omitempty"`
	LastLoginAt             int64   `json:"last_login_at,omitempty"`
	LastActivityAt          int64   `json:"last_activity_at,omitempty"`
	DisabledAt              int64   `json:"disabled_at,omitempty"`
	ProfilePictureURL       string  `json:"profile_picture_url"`
	ProfilePictureChangedAt int64   `json:"profile_picture_changed_at,omitempty"`
}

type updateUserProfileRequest struct {
	Username    *string `json:"username,omitempty"`
	DisplayName *string `json:"display_name,omitempty"`
	FirstName   *string `json:"first_name,omitempty"`
	LastName    *string `json:"last_name,omitempty"`
	Locale      *string `json:"locale,omitempty"`
	Timezone    *string `json:"timezone,omitempty"`
}

type changeUserEmailRequest struct {
	Email string `json:"email"`
}

type userEmailStateResponse struct {
	ID            string `json:"id"`
	EmailVerified bool   `json:"email_verified"`
}

type currentUserContextResponse struct {
	User                         currentUserContextUserResponse       `json:"user"`
	NoCurrentAffiliation         bool                                 `json:"no_current_affiliation"`
	NoAssignedAccess             bool                                 `json:"no_assigned_access"`
	AvailableProductAreas        []application.CurrentUserProductArea `json:"available_product_areas"`
	ManagementScopes             []currentUserContextScopeResponse    `json:"management_scopes"`
	ManagementScopesHasMore      bool                                 `json:"management_scopes_has_more"`
	UnresolvedAttempt            *currentUserContextAttemptResponse   `json:"unresolved_attempt"`
	SessionManagementAvailable   bool                                 `json:"session_management_available"`
	CurrentDesktopRegistrationID *string                              `json:"current_desktop_registration_id"`
}

type currentUserContextScopeResponse struct {
	ScopeType   string `json:"scope_type"`
	ScopeID     string `json:"scope_id"`
	DisplayName string `json:"display_name"`
}

type currentUserContextAttemptResponse struct {
	ID        string                 `json:"id"`
	SittingID string                 `json:"exam_sitting_id"`
	State     model.ExamAttemptState `json:"state"`
}

type currentUserContextUserResponse struct {
	ID                      string `json:"id"`
	Username                string `json:"username"`
	DisplayName             string `json:"display_name"`
	ProfilePictureReference string `json:"profile_picture_reference"`
}

const (
	candidateExamActivityCursorVersion = 1
	candidateExamActivityCursorKind    = "candidate_exam_activity"
)

type candidateExamActivityCursor struct {
	Version          int    `json:"version"`
	Kind             string `json:"kind"`
	ScheduledStartAt string `json:"scheduled_start_at"`
	ExamSittingID    string `json:"exam_sitting_id"`
}

type candidateExamActivityResponse struct {
	ServerTime string                              `json:"server_time"`
	Items      []candidateExamActivityItemResponse `json:"items"`
	NextCursor string                              `json:"next_cursor,omitempty"`
}

type candidateExamActivityItemResponse struct {
	ExamID            string                                   `json:"exam_id"`
	ExamSittingID     string                                   `json:"exam_sitting_id"`
	Title             string                                   `json:"title"`
	AcademicUnit      candidateExamActivityRelationResponse    `json:"academic_unit"`
	Class             candidateExamActivityRelationResponse    `json:"class"`
	ScheduledStartAt  string                                   `json:"scheduled_start_at"`
	ScheduledEndAt    string                                   `json:"scheduled_end_at"`
	SittingState      model.ExamSittingState                   `json:"sitting_state"`
	SittingReasonCode *string                                  `json:"sitting_reason_code"`
	ActivityState     application.CandidateExamActivityState   `json:"activity_state"`
	AccessState       application.CandidateExamAccessState     `json:"access_state"`
	AllowedActions    []application.CandidateExamAllowedAction `json:"allowed_actions"`
	Attempt           *candidateExamActivityAttemptResponse    `json:"attempt"`
	Submission        *candidateExamActivitySubmissionResponse `json:"submission"`
	Result            *candidateExamActivityResultResponse     `json:"result"`
}

type candidateExamActivityRelationResponse struct {
	ID          string `json:"id"`
	DisplayName string `json:"display_name"`
}

type candidateExamActivityAttemptResponse struct {
	ID                   string                 `json:"id"`
	State                model.ExamAttemptState `json:"state"`
	SuspensionReasonCode *string                `json:"suspension_reason_code"`
}

type candidateExamActivitySubmissionResponse struct {
	ID          string                         `json:"id"`
	SubmittedAt string                         `json:"submitted_at"`
	Provenance  model.ExamSubmissionProvenance `json:"provenance"`
}

type candidateExamActivityResultResponse struct {
	ReleasedAt string `json:"released_at"`
}

type userProfileResourceModule struct {
	profiles UserProfileApplication
}

func userProfileResource(profiles UserProfileApplication) resource {
	module := userProfileResourceModule{profiles: profiles}
	user := apiPath(literal("users"), canonicalID("user_id"))
	picture := appendRoutePath(user, literal("profile-picture"))
	email := appendRoutePath(user, literal("email"))
	return newResource(
		"user-profiles",
		principalRoute(http.MethodGet, apiPath(literal("users")), userProfileReadCodes("request.invalid", "user.invalid"), module.search),
		principalRoute(http.MethodGet, apiPath(literal("users"), literal("me")), userProfileReadCodes("resource.not_found"), module.current),
		sessionRoute(http.MethodGet, apiPath(literal("users"), literal("me"), literal("context")),
			currentUserContextSessionCodes("administration.unavailable"), module.context),
		sessionRoute(http.MethodGet, apiPath(literal("users"), literal("me"), literal("exam-activity")),
			currentUserContextSessionCodes("request.invalid", "exam.attempt.unavailable"), module.examActivity),
		principalRoute(http.MethodGet, user, userProfileReadCodes("request.invalid", "resource.not_found"), module.get),
		principalRoute(http.MethodPatch, user, userProfileMutationCodes("request.invalid", "resource.not_found", "user.invalid", "user.conflict"), module.update),
		strongRecentSessionRoute(http.MethodPut, email, userProfileMutationCodes("authentication.strong_required", "authentication.reauthentication_required", "request.invalid", "resource.not_found", "user.invalid", "user.conflict", "authentication.account_recovery.unavailable"), module.changeEmail),
		strongRecentSessionRoute(http.MethodPost, appendRoutePath(email, literal("verify")), userProfileMutationCodes("authentication.strong_required", "authentication.reauthentication_required", "request.invalid", "resource.not_found", "user.conflict", "authentication.account_recovery.unavailable"), module.verifyEmail),
		protocolRoute("profile-picture-download", RouteProtocolBinaryDownload, AuthPrincipalRequired, http.MethodGet, picture, userProfilePrincipalCodes("request.invalid", "resource.not_found", "profile_picture.unavailable"), module.downloadPicture),
		protocolRoute("profile-picture-upload", RouteProtocolStreamingUpload, AuthPrincipalRequired, http.MethodPut, picture, userProfilePrincipalMutationCodes("request.invalid", "resource.not_found", "profile_picture.invalid", "profile_picture.unavailable", "user.conflict"), module.uploadPicture),
		principalRoute(http.MethodDelete, picture, userProfilePrincipalMutationCodes("request.invalid", "resource.not_found", "profile_picture.unavailable", "user.conflict"), module.removePicture),
	)
}

func (module userProfileResourceModule) examActivity(request operationRequest) (operationResult, error) {
	query := application.ListCandidateExamActivityQuery{Limit: 50}
	values := request.request.URL.Query()
	if raw := values.Get("limit"); raw != "" {
		limit, err := strconv.Atoi(raw)
		if err != nil || limit < 1 || limit > 200 {
			return operationResult{}, invalidRequestError("limit", errors.New("must be between 1 and 200"))
		}
		query.Limit = limit
	}
	if raw := values.Get("cursor"); raw != "" {
		cursor, err := decodeOpaqueCursor(raw, candidateExamActivityCursorSpec())
		if err != nil {
			return operationResult{}, invalidRequestError("cursor", err)
		}
		query.BeforeScheduledStart, _ = time.Parse(time.RFC3339Nano, cursor.ScheduledStartAt)
		query.BeforeSittingID = model.ExamSittingID(cursor.ExamSittingID)
	}
	page, err := module.profiles.ListCandidateExamActivity(request.context, request.invocation(), query)
	if err != nil {
		return operationResult{}, err
	}
	response := candidateExamActivityResponse{ServerTime: model.TimeUTC(page.ServerTime).Format(time.RFC3339Nano),
		Items: make([]candidateExamActivityItemResponse, 0, len(page.Items))}
	for _, item := range page.Items {
		mapped := candidateExamActivityItemResponse{ExamID: item.ExamID.String(), ExamSittingID: item.SittingID.String(), Title: item.Title,
			AcademicUnit:     candidateExamActivityRelationResponse{ID: item.AcademicUnitID.String(), DisplayName: item.AcademicUnitDisplayName},
			Class:            candidateExamActivityRelationResponse{ID: item.ClassID.String(), DisplayName: item.ClassDisplayName},
			ScheduledStartAt: model.TimeUTC(item.ScheduledStartAt).Format(time.RFC3339Nano),
			ScheduledEndAt:   model.TimeUTC(item.ScheduledEndAt).Format(time.RFC3339Nano), SittingState: item.SittingState,
			ActivityState: item.ActivityState, AccessState: item.AccessState,
			AllowedActions: append([]application.CandidateExamAllowedAction(nil), item.AllowedActions...)}
		if item.SittingReasonCode != "" {
			value := item.SittingReasonCode
			mapped.SittingReasonCode = &value
		}
		if item.Attempt != nil {
			mapped.Attempt = &candidateExamActivityAttemptResponse{ID: item.Attempt.ID.String(), State: item.Attempt.State}
			if item.Attempt.SuspensionReasonCode != "" {
				value := string(item.Attempt.SuspensionReasonCode)
				mapped.Attempt.SuspensionReasonCode = &value
			}
		}
		if item.Submission != nil {
			mapped.Submission = &candidateExamActivitySubmissionResponse{ID: item.Submission.ID.String(),
				SubmittedAt: model.TimeUTC(item.Submission.SubmittedAt).Format(time.RFC3339Nano), Provenance: item.Submission.Provenance}
		}
		if item.Result != nil {
			mapped.Result = &candidateExamActivityResultResponse{ReleasedAt: model.TimeUTC(item.Result.ReleasedAt).Format(time.RFC3339Nano)}
		}
		response.Items = append(response.Items, mapped)
	}
	if page.HasMore {
		if len(page.Items) == 0 {
			return operationResult{}, application.NewError("exam.attempt.unavailable")
		}
		last := page.Items[len(page.Items)-1]
		response.NextCursor, err = encodeOpaqueCursor(candidateExamActivityCursor{
			Kind: candidateExamActivityCursorKind, ScheduledStartAt: model.TimeUTC(last.ScheduledStartAt).Format(time.RFC3339Nano),
			ExamSittingID: last.SittingID.String(),
		}, candidateExamActivityCursorSpec())
		if err != nil {
			return operationResult{}, application.NewError("exam.attempt.unavailable").Wrap(err)
		}
	}
	return jsonResult(http.StatusOK, response).withHeaders(noStoreHeaders()), nil
}

func candidateExamActivityCursorSpec() opaqueCursorSpec[candidateExamActivityCursor] {
	return opaqueCursorSpec[candidateExamActivityCursor]{label: "candidate Exam activity",
		maximumEncodedLength: defaultOpaqueCursorMaximumEncodedLength, currentVersion: candidateExamActivityCursorVersion,
		members:        []string{"version", "kind", "scheduled_start_at", "exam_sitting_id"},
		version:        func(cursor candidateExamActivityCursor) int { return cursor.Version },
		setVersion:     func(cursor *candidateExamActivityCursor, version int) { cursor.Version = version },
		acceptsVersion: func(version int) bool { return version == candidateExamActivityCursorVersion },
		validate: func(cursor candidateExamActivityCursor) error {
			at, err := time.Parse(time.RFC3339Nano, cursor.ScheduledStartAt)
			if cursor.Kind != candidateExamActivityCursorKind || err != nil || at.IsZero() ||
				!model.ExamSittingID(cursor.ExamSittingID).IsValid() {
				return errors.New("invalid candidate Exam activity keyset")
			}
			return nil
		}}
}

func currentUserContextSessionCodes(extra ...string) []string {
	return append([]string{"authentication.required", "authentication.invalid_token", "authentication.credential_ambiguous"}, extra...)
}

func (module userProfileResourceModule) context(request operationRequest) (operationResult, error) {
	view, err := module.profiles.GetCurrentUserContext(request.context, request.invocation())
	if err != nil {
		return operationResult{}, err
	}
	response := currentUserContextResponse{User: currentUserContextUserResponse{ID: view.UserID.String(), Username: view.Username,
		DisplayName: view.DisplayName, ProfilePictureReference: view.ProfilePictureReference},
		NoCurrentAffiliation: view.NoCurrentAffiliation, NoAssignedAccess: view.NoAssignedAccess,
		AvailableProductAreas:   append([]application.CurrentUserProductArea(nil), view.AvailableProductAreas...),
		ManagementScopesHasMore: view.ManagementScopesHasMore, SessionManagementAvailable: view.SessionManagementAvailable,
		ManagementScopes: make([]currentUserContextScopeResponse, 0, len(view.ManagementScopes))}
	for _, scope := range view.ManagementScopes {
		response.ManagementScopes = append(response.ManagementScopes, currentUserContextScopeResponse{
			ScopeType: string(scope.ScopeType), ScopeID: scope.ScopeID, DisplayName: scope.DisplayName,
		})
	}
	if view.UnresolvedAttempt != nil {
		response.UnresolvedAttempt = &currentUserContextAttemptResponse{ID: view.UnresolvedAttempt.AttemptID.String(),
			SittingID: view.UnresolvedAttempt.SittingID.String(), State: view.UnresolvedAttempt.State}
	}
	if view.CurrentDesktopRegistrationID.IsValid() {
		id := view.CurrentDesktopRegistrationID.String()
		response.CurrentDesktopRegistrationID = &id
	}
	return jsonResult(http.StatusOK, response).withHeaders(noStoreHeaders()), nil
}

func (module userProfileResourceModule) changeEmail(request operationRequest) (operationResult, error) {
	userID, err := request.params.RequireUserId()
	if err != nil {
		return operationResult{}, err
	}
	var body changeUserEmailRequest
	if err = request.decodeJSON(&body, "changeUserEmail"); err != nil {
		return operationResult{}, err
	}
	state, appErr := module.profiles.ChangeUserEmail(request.context, request.invocation(), application.ChangeUserEmailCommand{UserID: userID, Email: body.Email})
	if appErr != nil {
		return operationResult{}, appErr
	}
	return jsonResult(http.StatusOK, userEmailStateResponseFromApplication(state)), nil
}

func (module userProfileResourceModule) verifyEmail(request operationRequest) (operationResult, error) {
	userID, err := request.params.RequireUserId()
	if err != nil {
		return operationResult{}, err
	}
	state, appErr := module.profiles.VerifyUserEmailPrivileged(request.context, request.invocation(), application.VerifyUserEmailPrivilegedCommand{UserID: userID})
	if appErr != nil {
		return operationResult{}, appErr
	}
	return jsonResult(http.StatusOK, userEmailStateResponseFromApplication(state)), nil
}

func userEmailStateResponseFromApplication(state *application.UserEmailState) userEmailStateResponse {
	if state == nil {
		return userEmailStateResponse{}
	}
	return userEmailStateResponse{ID: state.UserID.String(), EmailVerified: state.EmailVerified}
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
	user, err := module.profiles.UpdateUserProfile(request.context, request.invocation(), application.UpdateUserProfileCommand{ID: userID, Username: body.Username, DisplayName: body.DisplayName, FirstName: body.FirstName, LastName: body.LastName, Locale: body.Locale, Timezone: body.Timezone})
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
	response := userProfileResponse{
		ID:                      user.ID.String(),
		CreateAt:                model.MillisFromTime(user.CreatedAt),
		UpdateAt:                model.MillisFromTime(user.UpdatedAt),
		DeleteAt:                user.ArchivedAt.Millis(),
		Username:                user.Username,
		DisplayName:             user.DisplayName,
		FirstName:               user.FirstName,
		LastName:                user.LastName,
		LastLoginAt:             user.LastLoginAt.Millis(),
		LastActivityAt:          user.LastActivityAt.Millis(),
		DisabledAt:              user.DisabledAt.Millis(),
		ProfilePictureURL:       pictureURL,
		ProfilePictureChangedAt: user.ProfilePictureChangedAt.Millis(),
	}
	if user.Email != "" {
		response.Email = &user.Email
		response.EmailVerified = &user.EmailVerified
		response.Locale = &user.Locale
		response.Timezone = &user.Timezone
	}
	return response
}

func userProfileResponses(users []*model.User) []userProfileResponse {
	result := make([]userProfileResponse, 0, len(users))
	for _, user := range users {
		result = append(result, userProfileResponseFromModel(user))
	}
	return result
}
