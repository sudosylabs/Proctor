// ---------------------------------------------------------------------------------------------
// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// Modifications Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------
//
// Adapted from Mattermost server/channels/web/params.go and context.go.
// Proctor keeps a deliberately small request-parameter contract and expands it
// only when a registered API resource needs another typed parameter.

package httpapi

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
	BrowserSourceSessionID  string
	NativeStreamID          string
	NativeBatchSequence     string
	ProviderId              string
	RoleID                  string
	RoleBindingID           string
	JobID                   string
	MailDeliveryID          string
	ExamID                  string
	ExamRevisionID          string
	ExamSittingID           string
	SecurityPreflightID     string
	ExamAttemptID           string
	SubmissionID            string
	ExamExportID            string
	RetentionHoldID         string
	RetentionPreviewID      string
	IntegrityFlagID         string
	ExamResourceID          string
	AttemptWorkspaceEntryID string
	StarterWorkspaceEntryID string
	UserID                  string
	InstitutionID           string
	AcademicUnitID          string
	ProgrammeID             string
	ProgrammeLevelID        string
	AcademicPeriodID        string
	ClassID                 string
	AffiliationID           string
	AcademicUnitMemberID    string
	ClassMemberID           string
	PersonalAccessTokenID   string
	SessionID               string
	DesktopRegistrationID   string
	ExternalIdentityID      string
	InvitationID            string
	OnboardingImportID      string
	StudentProgressionID    string
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
		BrowserSourceSessionID:  variables["source_session_id"],
		NativeStreamID:          variables["stream_id"],
		NativeBatchSequence:     variables["batch_sequence"],
		ProviderId:              strings.ToLower(strings.TrimSpace(variables["provider_id"])),
		RoleID:                  strings.TrimSpace(variables["role_id"]),
		RoleBindingID:           strings.TrimSpace(variables["role_binding_id"]),
		JobID:                   strings.TrimSpace(variables["job_id"]),
		MailDeliveryID:          strings.TrimSpace(variables["mail_delivery_id"]),
		ExamID:                  strings.TrimSpace(variables["exam_id"]),
		ExamRevisionID:          strings.TrimSpace(variables["exam_revision_id"]),
		ExamSittingID:           strings.TrimSpace(variables["exam_sitting_id"]),
		SecurityPreflightID:     strings.TrimSpace(variables["security_preflight_id"]),
		ExamAttemptID:           strings.TrimSpace(variables["exam_attempt_id"]),
		SubmissionID:            strings.TrimSpace(variables["submission_id"]),
		ExamExportID:            strings.TrimSpace(variables["exam_export_id"]),
		RetentionHoldID:         strings.TrimSpace(variables["retention_hold_id"]),
		RetentionPreviewID:      strings.TrimSpace(variables["retention_preview_id"]),
		IntegrityFlagID:         strings.TrimSpace(variables["integrity_flag_id"]),
		ExamResourceID:          strings.TrimSpace(variables["exam_resource_id"]),
		AttemptWorkspaceEntryID: strings.TrimSpace(variables["attempt_workspace_entry_id"]),
		StarterWorkspaceEntryID: strings.TrimSpace(variables["starter_workspace_entry_id"]),
		UserID:                  strings.TrimSpace(variables["user_id"]),
		InstitutionID:           strings.TrimSpace(variables["institution_id"]),
		AcademicUnitID:          strings.TrimSpace(variables["academic_unit_id"]),
		ProgrammeID:             strings.TrimSpace(variables["programme_id"]),
		ProgrammeLevelID:        strings.TrimSpace(variables["programme_level_id"]),
		AcademicPeriodID:        strings.TrimSpace(variables["academic_period_id"]),
		ClassID:                 strings.TrimSpace(variables["class_id"]),
		AffiliationID:           strings.TrimSpace(variables["affiliation_id"]),
		AcademicUnitMemberID:    strings.TrimSpace(variables["academic_unit_member_id"]),
		ClassMemberID:           strings.TrimSpace(variables["class_member_id"]),
		PersonalAccessTokenID:   strings.TrimSpace(variables["personal_access_token_id"]),
		SessionID:               strings.TrimSpace(variables["session_id"]),
		DesktopRegistrationID:   strings.TrimSpace(variables["desktop_registration_id"]),
		ExternalIdentityID:      strings.TrimSpace(variables["external_identity_id"]),
		InvitationID:            strings.TrimSpace(variables["invitation_id"]),
		OnboardingImportID:      strings.TrimSpace(variables["onboarding_import_id"]),
		StudentProgressionID:    strings.TrimSpace(variables["student_progression_id"]),
		ReturnTo:                strings.TrimSpace(query.Get("return_to")),
		ClientType:              strings.TrimSpace(query.Get("client_type")),
		DeviceId:                strings.TrimSpace(query.Get("device_id")),
		DeviceName:              strings.TrimSpace(query.Get("device_name")),
		ConnectionId:            strings.TrimSpace(query.Get("connection_id")),
		SequenceNumber:          strings.TrimSpace(query.Get("sequence_number")),
	}
}

func (p Params) RequireExternalIdentityID() (model.ExternalIdentityID, error) {
	return pathID[model.ExternalIdentityID](p.ExternalIdentityID)
}

func (p Params) RequireInvitationID() (model.InvitationID, error) {
	return pathID[model.InvitationID](p.InvitationID)
}

func (p Params) RequireOnboardingImportID() (model.OnboardingImportID, error) {
	return pathID[model.OnboardingImportID](p.OnboardingImportID)
}

func (p Params) RequireStudentProgressionID() (model.OnboardingImportID, error) {
	return pathID[model.OnboardingImportID](p.StudentProgressionID)
}

func (p Params) RequireProviderId() (string, error) {
	if len(p.ProviderId) == 0 || len(p.ProviderId) > model.IdentityProviderMaxLength {
		return "", invalidRequestError(nil)
	}
	return p.ProviderId, nil
}

func (p Params) RequireUserID() (model.UserID, error) {
	return pathID[model.UserID](p.UserID)
}

func (p Params) RequireInstitutionID() (model.InstitutionID, error) {
	return pathID[model.InstitutionID](p.InstitutionID)
}

func (p Params) RequireAcademicUnitID() (model.AcademicUnitID, error) {
	return pathID[model.AcademicUnitID](p.AcademicUnitID)
}

func (p Params) RequireProgrammeID() (model.ProgrammeID, error) {
	return pathID[model.ProgrammeID](p.ProgrammeID)
}

func (p Params) RequireProgrammeLevelID() (model.ProgrammeLevelID, error) {
	return pathID[model.ProgrammeLevelID](p.ProgrammeLevelID)
}

func (p Params) RequireAcademicPeriodID() (model.AcademicPeriodID, error) {
	return pathID[model.AcademicPeriodID](p.AcademicPeriodID)
}

func (p Params) RequireClassID() (model.ClassID, error) {
	return pathID[model.ClassID](p.ClassID)
}

func (p Params) RequireAffiliationID() (model.AffiliationID, error) {
	return pathID[model.AffiliationID](p.AffiliationID)
}

func (p Params) RequireAcademicUnitMemberID() (model.AcademicUnitMemberID, error) {
	return pathID[model.AcademicUnitMemberID](p.AcademicUnitMemberID)
}

func (p Params) RequireClassMemberID() (model.ClassMemberID, error) {
	return pathID[model.ClassMemberID](p.ClassMemberID)
}

func (p Params) RequirePersonalAccessTokenID() (model.PersonalAccessTokenID, error) {
	return pathID[model.PersonalAccessTokenID](p.PersonalAccessTokenID)
}

func (p Params) RequireSessionID() (model.SessionID, error) {
	return pathID[model.SessionID](p.SessionID)
}

func (p Params) RequireDesktopRegistrationID() (model.DesktopRegistrationID, error) {
	return pathID[model.DesktopRegistrationID](p.DesktopRegistrationID)
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

func (p Params) RequireRoleID() (model.RoleID, error) {
	return pathID[model.RoleID](p.RoleID)
}

func (p Params) RequireRoleBindingID() (model.RoleBindingID, error) {
	return pathID[model.RoleBindingID](p.RoleBindingID)
}

func (p Params) RequireJobID() (model.JobID, error) {
	return pathID[model.JobID](p.JobID)
}

func (p Params) RequireMailDeliveryID() (model.MailDeliveryID, error) {
	return pathID[model.MailDeliveryID](p.MailDeliveryID)
}

func (p Params) RequireExamID() (model.ExamID, error) {
	return pathID[model.ExamID](p.ExamID)
}

func (p Params) RequireExamRevisionID() (model.ExamRevisionID, error) {
	return pathID[model.ExamRevisionID](p.ExamRevisionID)
}

func (p Params) RequireExamSittingID() (model.ExamSittingID, error) {
	return pathID[model.ExamSittingID](p.ExamSittingID)
}

func (p Params) RequireExamAttemptID() (model.ExamAttemptID, error) {
	return pathID[model.ExamAttemptID](p.ExamAttemptID)
}

func (p Params) RequireSubmissionID() (model.SubmissionID, error) {
	return pathID[model.SubmissionID](p.SubmissionID)
}

func (p Params) RequireRetentionHoldID() (model.RetentionHoldID, error) {
	return pathID[model.RetentionHoldID](p.RetentionHoldID)
}

func (p Params) RequireIntegrityFlagID() (model.IntegrityFlagID, error) {
	return pathID[model.IntegrityFlagID](p.IntegrityFlagID)
}

func (p Params) RequireExamResourceID() (model.ExamResourceID, error) {
	return pathID[model.ExamResourceID](p.ExamResourceID)
}

func (p Params) RequireStarterWorkspaceEntryID() (model.StarterWorkspaceEntryID, error) {
	return pathID[model.StarterWorkspaceEntryID](p.StarterWorkspaceEntryID)
}

func (p Params) RequireAttemptWorkspaceEntryID() (model.AttemptWorkspaceEntryID, error) {
	return pathID[model.AttemptWorkspaceEntryID](p.AttemptWorkspaceEntryID)
}

// pathID converts a canonical path variable into its typed model identifier,
// rejecting anything that is not a valid identifier as an invalid request.
func pathID[T interface {
	~string
	IsValid() bool
}](value string) (T, error) {
	id := T(value)
	if !id.IsValid() {
		var zero T
		return zero, invalidRequestError(nil)
	}
	return id, nil
}

// opaquePathID accepts a path variable that has the opaque identifier form
// but no fixed entity type.
func opaquePathID(value string) (string, error) {
	if !model.IsValidId(value) {
		return "", invalidRequestError(nil)
	}
	return value, nil
}

// RequireNativeStreamID returns the native delivery stream selector. The
// selector names a delivery stream, Participation, or source session chosen
// by the Store, so it stays an untyped opaque identifier.
func (p Params) RequireNativeStreamID() (string, error) {
	return opaquePathID(p.NativeStreamID)
}

func (p Params) RequireExamExportID() (model.ExamExportID, error) {
	return pathID[model.ExamExportID](p.ExamExportID)
}

// RequireSecurityPreflightID returns the server-issued preflight identifier.
// Preflight identifiers belong to the native agreement vocabulary rather than
// the typed entity catalog, so the path keeps the opaque identifier form.
func (p Params) RequireSecurityPreflightID() (string, error) {
	return opaquePathID(p.SecurityPreflightID)
}
