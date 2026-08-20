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
	ProviderId              string
	RoleId                  string
	RoleBindingId           string
	JobID                   string
	MailDeliveryID          string
	ExamID                  string
	ExamRevisionID          string
	ExamSittingID           string
	ExamAttemptID           string
	SubmissionID            string
	IntegrityFlagID         string
	ExamResourceID          string
	AttemptWorkspaceEntryID string
	StarterWorkspaceEntryID string
	UserID                  string
	InstitutionID           string
	AcademicUnitID          string
	ProgrammeId             string
	ProgrammeLevelId        string
	AcademicPeriodId        string
	ClassId                 string
	AffiliationId           string
	AcademicUnitMemberId    string
	ClassMemberId           string
	PersonalAccessTokenId   string
	SessionID               string
	ExternalIdentityID      string
	InvitationID            string
	OnboardingImportID      string
	ReturnTo                string
	ClientType              string
	DeviceId                string
	DeviceName              string
	ConnectionId            string
	SequenceNumber          string
}

func ParamsFromRequest(request *http.Request) Params {
	variables := mux.Vars(request)
	query := request.URL.Query()
	return Params{
		ProviderId:              strings.ToLower(strings.TrimSpace(variables["provider_id"])),
		RoleId:                  strings.TrimSpace(variables["role_id"]),
		RoleBindingId:           strings.TrimSpace(variables["role_binding_id"]),
		JobID:                   strings.TrimSpace(variables["job_id"]),
		MailDeliveryID:          strings.TrimSpace(variables["mail_delivery_id"]),
		ExamID:                  strings.TrimSpace(variables["exam_id"]),
		ExamRevisionID:          strings.TrimSpace(variables["exam_revision_id"]),
		ExamSittingID:           strings.TrimSpace(variables["exam_sitting_id"]),
		ExamAttemptID:           strings.TrimSpace(variables["exam_attempt_id"]),
		SubmissionID:            strings.TrimSpace(variables["submission_id"]),
		IntegrityFlagID:         strings.TrimSpace(variables["integrity_flag_id"]),
		ExamResourceID:          strings.TrimSpace(variables["exam_resource_id"]),
		AttemptWorkspaceEntryID: strings.TrimSpace(variables["attempt_workspace_entry_id"]),
		StarterWorkspaceEntryID: strings.TrimSpace(variables["starter_workspace_entry_id"]),
		UserID:                  strings.TrimSpace(variables["user_id"]),
		InstitutionID:           strings.TrimSpace(variables["institution_id"]),
		AcademicUnitID:          strings.TrimSpace(variables["academic_unit_id"]),
		ProgrammeId:             strings.TrimSpace(variables["programme_id"]),
		ProgrammeLevelId:        strings.TrimSpace(variables["programme_level_id"]),
		AcademicPeriodId:        strings.TrimSpace(variables["academic_period_id"]),
		ClassId:                 strings.TrimSpace(variables["class_id"]),
		AffiliationId:           strings.TrimSpace(variables["affiliation_id"]),
		AcademicUnitMemberId:    strings.TrimSpace(variables["academic_unit_member_id"]),
		ClassMemberId:           strings.TrimSpace(variables["class_member_id"]),
		PersonalAccessTokenId:   strings.TrimSpace(variables["personal_access_token_id"]),
		SessionID:               strings.TrimSpace(variables["session_id"]),
		ExternalIdentityID:      strings.TrimSpace(variables["external_identity_id"]),
		InvitationID:            strings.TrimSpace(variables["invitation_id"]),
		OnboardingImportID:      strings.TrimSpace(variables["onboarding_import_id"]),
		ReturnTo:                strings.TrimSpace(query.Get("return_to")),
		ClientType:              strings.TrimSpace(query.Get("client_type")),
		DeviceId:                strings.TrimSpace(query.Get("device_id")),
		DeviceName:              strings.TrimSpace(query.Get("device_name")),
		ConnectionId:            strings.TrimSpace(query.Get("connection_id")),
		SequenceNumber:          strings.TrimSpace(query.Get("sequence_number")),
	}
}

func (p Params) RequireExternalIdentityID() (string, error) {
	return requirePathId("external_identity_id", p.ExternalIdentityID)
}

func (p Params) RequireInvitationID() (string, error) {
	return requirePathId("invitation_id", p.InvitationID)
}

func (p Params) RequireOnboardingImportID() (string, error) {
	return requirePathId("onboarding_import_id", p.OnboardingImportID)
}

func (p Params) RequireProviderId() (string, error) {
	if len(p.ProviderId) == 0 || len(p.ProviderId) > model.IdentityProviderMaxLength {
		return "", invalidRequestError("provider_id", nil)
	}
	return p.ProviderId, nil
}

func (p Params) RequireUserId() (string, error) {
	return requirePathId("user_id", p.UserID)
}

func (p Params) RequireInstitutionId() (string, error) {
	return requirePathId("institution_id", p.InstitutionID)
}

func (p Params) RequireAcademicUnitId() (string, error) {
	return requirePathId("academic_unit_id", p.AcademicUnitID)
}

func (p Params) RequireProgrammeId() (string, error) {
	return requirePathId("programme_id", p.ProgrammeId)
}

func (p Params) RequireProgrammeLevelId() (string, error) {
	return requirePathId("programme_level_id", p.ProgrammeLevelId)
}

func (p Params) RequireAcademicPeriodId() (string, error) {
	return requirePathId("academic_period_id", p.AcademicPeriodId)
}

func (p Params) RequireClassId() (string, error) {
	return requirePathId("class_id", p.ClassId)
}

func (p Params) RequireAffiliationId() (string, error) {
	return requirePathId("affiliation_id", p.AffiliationId)
}

func (p Params) RequireAcademicUnitMemberId() (string, error) {
	return requirePathId("academic_unit_member_id", p.AcademicUnitMemberId)
}

func (p Params) RequireClassMemberId() (string, error) {
	return requirePathId("class_member_id", p.ClassMemberId)
}

func (p Params) RequirePersonalAccessTokenId() (string, error) {
	return requirePathId("personal_access_token_id", p.PersonalAccessTokenId)
}

func (p Params) RequireSessionId() (string, error) {
	return requirePathId("session_id", p.SessionID)
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

func (p Params) RequireRoleId() (string, error) {
	return requirePathId("role_id", p.RoleId)
}

func (p Params) RequireRoleBindingId() (string, error) {
	return requirePathId("role_binding_id", p.RoleBindingId)
}

func (p Params) RequireJobId() (string, error) {
	return requirePathId("job_id", p.JobID)
}

func (p Params) RequireMailDeliveryID() (string, error) {
	return requirePathId("mail_delivery_id", p.MailDeliveryID)
}

func (p Params) RequireExamId() (string, error) {
	return requirePathId("exam_id", p.ExamID)
}

func (p Params) RequireExamRevisionId() (string, error) {
	return requirePathId("exam_revision_id", p.ExamRevisionID)
}

func (p Params) RequireExamSittingId() (string, error) {
	return requirePathId("exam_sitting_id", p.ExamSittingID)
}

func (p Params) RequireExamAttemptId() (string, error) {
	return requirePathId("exam_attempt_id", p.ExamAttemptID)
}

func (p Params) RequireSubmissionId() (string, error) {
	return requirePathId("submission_id", p.SubmissionID)
}

func (p Params) RequireIntegrityFlagID() (string, error) {
	return requirePathId("integrity_flag_id", p.IntegrityFlagID)
}

func (p Params) RequireExamResourceId() (string, error) {
	return requirePathId("exam_resource_id", p.ExamResourceID)
}

func (p Params) RequireStarterWorkspaceEntryId() (string, error) {
	return requirePathId("starter_workspace_entry_id", p.StarterWorkspaceEntryID)
}

func (p Params) RequireAttemptWorkspaceEntryId() (string, error) {
	return requirePathId("attempt_workspace_entry_id", p.AttemptWorkspaceEntryID)
}

func requirePathId(name, id string) (string, error) {
	if !model.IsValidId(id) {
		return "", invalidRequestError(name, nil)
	}
	return id, nil
}

func principalAndRequiredId(
	writer http.ResponseWriter,
	request *http.Request,
	require func(Params) (string, error),
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
