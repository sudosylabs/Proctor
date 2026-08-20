// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package api

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	application "github.com/sudosylabs/proctor/server/app"
	"github.com/sudosylabs/proctor/server/model"
)

type InvitationApplication interface {
	IssueStudentClassInvitation(context.Context, application.Invocation, application.IssueStudentClassInvitationCommand) (application.InvitationView, error)
	AcceptStudentClassInvitation(context.Context, application.Invocation, application.AcceptStudentClassInvitationCommand) (*application.InvitationAcceptanceView, error)
	IssueTeacherAcademicUnitInvitation(context.Context, application.Invocation, application.IssueTeacherAcademicUnitInvitationCommand) (application.InvitationView, error)
	AcceptTeacherAcademicUnitInvitation(context.Context, application.Invocation, application.AcceptTeacherAcademicUnitInvitationCommand) (*application.InvitationAcceptanceView, error)
	IssueAcademicUnitRoleInvitation(context.Context, application.Invocation, application.IssueAcademicUnitRoleInvitationCommand) (application.InvitationView, error)
	AcceptAcademicUnitRoleInvitation(context.Context, application.Invocation, application.AcceptAcademicUnitRoleInvitationCommand) (*application.InvitationAcceptanceView, error)
	IssueInstitutionRoleInvitation(context.Context, application.Invocation, application.IssueInstitutionRoleInvitationCommand) (application.InvitationView, error)
	AcceptInstitutionRoleInvitation(context.Context, application.Invocation, application.AcceptInstitutionRoleInvitationCommand) (*application.InvitationAcceptanceView, error)
	ListInvitations(context.Context, application.Invocation, application.ListInvitationsQuery) (application.InvitationAdministrationPage, error)
	GetInvitation(context.Context, application.Invocation, string) (application.InvitationAdministrationView, error)
	ResendInvitation(context.Context, application.Invocation, application.ResendInvitationCommand) (application.InvitationAdministrationView, error)
	RevokeInvitation(context.Context, application.Invocation, application.RevokeInvitationCommand) (application.InvitationAdministrationView, error)
	ReplaceInvitation(context.Context, application.Invocation, application.ReplaceInvitationCommand) (application.InvitationAdministrationView, error)
}

type issueStudentClassInvitationRequest struct {
	Email                string `json:"email"`
	StartAt              int64  `json:"start_at,omitempty"`
	EndAt                int64  `json:"end_at,omitempty"`
	SuggestedUsername    string `json:"suggested_username,omitempty"`
	SuggestedDisplayName string `json:"suggested_display_name,omitempty"`
	SuggestedFirstName   string `json:"suggested_first_name,omitempty"`
	SuggestedLastName    string `json:"suggested_last_name,omitempty"`
	SuggestedLocale      string `json:"suggested_locale,omitempty"`
}

type acceptStudentClassInvitationRequest struct {
	Claim       string `json:"claim"`
	Password    string `json:"password"`
	Username    string `json:"username"`
	DisplayName string `json:"display_name,omitempty"`
	FirstName   string `json:"first_name,omitempty"`
	LastName    string `json:"last_name,omitempty"`
	Locale      string `json:"locale,omitempty"`
	Timezone    string `json:"timezone,omitempty"`
}

type issueTeacherAcademicUnitInvitationRequest struct {
	Email                string `json:"email"`
	RoleID               string `json:"role_id"`
	StartAt              int64  `json:"start_at,omitempty"`
	EndAt                int64  `json:"end_at,omitempty"`
	SuggestedUsername    string `json:"suggested_username,omitempty"`
	SuggestedDisplayName string `json:"suggested_display_name,omitempty"`
	SuggestedFirstName   string `json:"suggested_first_name,omitempty"`
	SuggestedLastName    string `json:"suggested_last_name,omitempty"`
	SuggestedLocale      string `json:"suggested_locale,omitempty"`
}

type acceptTeacherAcademicUnitInvitationRequest acceptStudentClassInvitationRequest

type issueScopedRoleInvitationRequest struct {
	Email   string `json:"email"`
	RoleID  string `json:"role_id"`
	StartAt int64  `json:"start_at,omitempty"`
	EndAt   int64  `json:"end_at,omitempty"`
}

type acceptScopedRoleInvitationRequest struct {
	Claim string `json:"claim"`
}

type invitationResponse struct {
	ID               string   `json:"id"`
	Purpose          string   `json:"purpose"`
	State            string   `json:"state"`
	ClassID          string   `json:"class_id,omitempty"`
	AcademicPeriodID string   `json:"academic_period_id,omitempty"`
	AcademicUnitID   string   `json:"academic_unit_id,omitempty"`
	RoleID           string   `json:"role_id,omitempty"`
	RoleActions      []string `json:"role_actions,omitempty"`
	StartAt          int64    `json:"start_at"`
	EndAt            int64    `json:"end_at,omitempty"`
	ExpiresAt        int64    `json:"expires_at"`
}

type invitationAcceptanceResponse struct {
	UserID               string `json:"user_id"`
	InvitationID         string `json:"invitation_id"`
	AffiliationID        string `json:"affiliation_id,omitempty"`
	ClassMemberID        string `json:"class_member_id,omitempty"`
	AcademicUnitMemberID string `json:"academic_unit_member_id,omitempty"`
	RoleBindingID        string `json:"role_binding_id,omitempty"`
	Replayed             bool   `json:"replayed"`
}

type invitationMutationRequest struct {
	ExpectedRevision int64 `json:"expected_revision"`
}

type replaceInvitationRequest struct {
	ExpectedRevision     int64  `json:"expected_revision"`
	Purpose              string `json:"purpose"`
	Email                string `json:"email"`
	ClassID              string `json:"class_id,omitempty"`
	AcademicUnitID       string `json:"academic_unit_id,omitempty"`
	InstitutionID        string `json:"institution_id,omitempty"`
	RoleID               string `json:"role_id,omitempty"`
	StartAt              int64  `json:"start_at,omitempty"`
	EndAt                int64  `json:"end_at,omitempty"`
	SuggestedUsername    string `json:"suggested_username,omitempty"`
	SuggestedDisplayName string `json:"suggested_display_name,omitempty"`
	SuggestedFirstName   string `json:"suggested_first_name,omitempty"`
	SuggestedLastName    string `json:"suggested_last_name,omitempty"`
	SuggestedLocale      string `json:"suggested_locale,omitempty"`
}

type invitationDeliveryResponse struct {
	TemplateKey       string `json:"template_key"`
	State             string `json:"state"`
	MaskedRecipient   string `json:"masked_recipient"`
	CreatedAt         int64  `json:"created_at"`
	UpdatedAt         int64  `json:"updated_at"`
	Deadline          int64  `json:"deadline"`
	AcceptedAt        int64  `json:"accepted_at,omitempty"`
	PublicFailureCode string `json:"public_failure_code,omitempty"`
}

type invitationAdministrationResponse struct {
	ID               string                      `json:"id"`
	Purpose          string                      `json:"purpose"`
	State            string                      `json:"state"`
	ClassID          string                      `json:"class_id,omitempty"`
	AcademicPeriodID string                      `json:"academic_period_id,omitempty"`
	AcademicUnitID   string                      `json:"academic_unit_id,omitempty"`
	RoleID           string                      `json:"role_id,omitempty"`
	RoleActions      []string                    `json:"role_actions,omitempty"`
	StartAt          int64                       `json:"start_at"`
	EndAt            int64                       `json:"end_at,omitempty"`
	ExpiresAt        int64                       `json:"expires_at"`
	Email            string                      `json:"email"`
	InviterUserID    string                      `json:"inviter_user_id"`
	AcceptedUserID   string                      `json:"accepted_user_id,omitempty"`
	CreatedAt        int64                       `json:"created_at"`
	UpdatedAt        int64                       `json:"updated_at"`
	Revision         int64                       `json:"revision"`
	Delivery         *invitationDeliveryResponse `json:"delivery,omitempty"`
}

type invitationListResponse struct {
	Items      []invitationAdministrationResponse `json:"items"`
	NextCursor string                             `json:"next_cursor,omitempty"`
}

type invitationCursor struct {
	Version   int    `json:"version"`
	CreatedAt string `json:"created_at"`
	ID        string `json:"id"`
}

type invitationResourceModule struct{ invitations InvitationApplication }

type unavailableInvitationApplication struct{}

func (unavailableInvitationApplication) IssueStudentClassInvitation(context.Context, application.Invocation, application.IssueStudentClassInvitationCommand) (application.InvitationView, error) {
	return application.InvitationView{}, application.NewError("invitation.unavailable")
}
func (unavailableInvitationApplication) AcceptStudentClassInvitation(context.Context, application.Invocation, application.AcceptStudentClassInvitationCommand) (*application.InvitationAcceptanceView, error) {
	return nil, application.NewError("invitation.unavailable")
}
func (unavailableInvitationApplication) IssueTeacherAcademicUnitInvitation(context.Context, application.Invocation, application.IssueTeacherAcademicUnitInvitationCommand) (application.InvitationView, error) {
	return application.InvitationView{}, application.NewError("invitation.unavailable")
}
func (unavailableInvitationApplication) AcceptTeacherAcademicUnitInvitation(context.Context, application.Invocation, application.AcceptTeacherAcademicUnitInvitationCommand) (*application.InvitationAcceptanceView, error) {
	return nil, application.NewError("invitation.unavailable")
}
func (unavailableInvitationApplication) IssueAcademicUnitRoleInvitation(context.Context, application.Invocation, application.IssueAcademicUnitRoleInvitationCommand) (application.InvitationView, error) {
	return application.InvitationView{}, application.NewError("invitation.unavailable")
}
func (unavailableInvitationApplication) AcceptAcademicUnitRoleInvitation(context.Context, application.Invocation, application.AcceptAcademicUnitRoleInvitationCommand) (*application.InvitationAcceptanceView, error) {
	return nil, application.NewError("invitation.unavailable")
}
func (unavailableInvitationApplication) IssueInstitutionRoleInvitation(context.Context, application.Invocation, application.IssueInstitutionRoleInvitationCommand) (application.InvitationView, error) {
	return application.InvitationView{}, application.NewError("invitation.unavailable")
}
func (unavailableInvitationApplication) AcceptInstitutionRoleInvitation(context.Context, application.Invocation, application.AcceptInstitutionRoleInvitationCommand) (*application.InvitationAcceptanceView, error) {
	return nil, application.NewError("invitation.unavailable")
}
func (unavailableInvitationApplication) ListInvitations(context.Context, application.Invocation, application.ListInvitationsQuery) (application.InvitationAdministrationPage, error) {
	return application.InvitationAdministrationPage{}, application.NewError("invitation.unavailable")
}
func (unavailableInvitationApplication) GetInvitation(context.Context, application.Invocation, string) (application.InvitationAdministrationView, error) {
	return application.InvitationAdministrationView{}, application.NewError("invitation.unavailable")
}
func (unavailableInvitationApplication) ResendInvitation(context.Context, application.Invocation, application.ResendInvitationCommand) (application.InvitationAdministrationView, error) {
	return application.InvitationAdministrationView{}, application.NewError("invitation.unavailable")
}
func (unavailableInvitationApplication) RevokeInvitation(context.Context, application.Invocation, application.RevokeInvitationCommand) (application.InvitationAdministrationView, error) {
	return application.InvitationAdministrationView{}, application.NewError("invitation.unavailable")
}
func (unavailableInvitationApplication) ReplaceInvitation(context.Context, application.Invocation, application.ReplaceInvitationCommand) (application.InvitationAdministrationView, error) {
	return application.InvitationAdministrationView{}, application.NewError("invitation.unavailable")
}

func invitationResource(invitations InvitationApplication) resource {
	module := invitationResourceModule{invitations: invitations}
	return newResource("invitations",
		principalRoute(http.MethodGet, apiPath(literal("invitations")),
			append(academicRelationshipReadErrorCodes(), "invitation.query.invalid", "invitation.unavailable"), module.list),
		principalRoute(http.MethodGet, apiPath(literal("invitations"), canonicalID("invitation_id")),
			append(academicRelationshipReadErrorCodes(), "invitation.unavailable"), module.get),
		principalRoute(http.MethodPost, apiPath(literal("invitations"), canonicalID("invitation_id"), literal("resend")),
			academicRelationshipMutationErrorCodes("invitation.conflict", "invitation.mail_unavailable", "invitation.unavailable"), module.resend),
		principalRoute(http.MethodPost, apiPath(literal("invitations"), canonicalID("invitation_id"), literal("revoke")),
			academicRelationshipMutationErrorCodes("invitation.conflict", "invitation.mail_unavailable", "invitation.unavailable"), module.revoke),
		principalRoute(http.MethodPost, apiPath(literal("invitations"), canonicalID("invitation_id"), literal("replacement")),
			append(academicRelationshipMutationErrorCodes("invitation.invalid", "invitation.role_not_delegable", "invitation.conflict", "invitation.mail_unavailable", "invitation.unavailable"),
				"authentication.strong_required", "authentication.reauthentication_required"), module.replace),
		principalRoute(http.MethodPost, apiPath(literal("classes"), canonicalID("class_id"), literal("invitations"), literal("student")),
			academicRelationshipMutationErrorCodes("invitation.invalid", "invitation.class_period_invalid", "invitation.conflict", "invitation.mail_unavailable", "invitation.unavailable"), module.issueStudentClass),
		publicRoute(http.MethodPost, apiPath(literal("invitations"), literal("student-class"), literal("accept")),
			[]string{"request.invalid", "authentication.rate_limited", "authentication.rate_limit_unavailable", "invitation.invalid", "invitation.user_invalid", "invitation.mail_unavailable", "invitation.unavailable", "authentication.password.invalid"}, module.acceptStudentClass),
		principalRoute(http.MethodPost, apiPath(literal("academic-units"), canonicalID("academic_unit_id"), literal("invitations"), literal("teacher")),
			academicRelationshipMutationErrorCodes("invitation.invalid", "invitation.role_not_delegable", "invitation.conflict", "invitation.mail_unavailable", "invitation.unavailable"), module.issueTeacherAcademicUnit),
		publicRoute(http.MethodPost, apiPath(literal("invitations"), literal("teacher-academic-unit"), literal("accept")),
			[]string{"request.invalid", "authentication.rate_limited", "authentication.rate_limit_unavailable", "invitation.invalid", "invitation.user_invalid", "invitation.mail_unavailable", "invitation.unavailable", "authentication.password.invalid"}, module.acceptTeacherAcademicUnit),
		principalRoute(http.MethodPost, apiPath(literal("academic-units"), canonicalID("academic_unit_id"), literal("invitations"), literal("role")),
			academicRelationshipMutationErrorCodes("invitation.invalid", "invitation.role_not_delegable", "invitation.conflict", "invitation.mail_unavailable", "invitation.unavailable"), module.issueAcademicUnitRole),
		sessionRoute(http.MethodPost, apiPath(literal("invitations"), literal("academic-unit-role"), literal("accept")),
			sessionAuthenticationMutationErrorCodes("authentication.rate_limited", "authentication.rate_limit_unavailable", "invitation.invalid", "invitation.unavailable"), module.acceptAcademicUnitRole),
		strongRecentSessionRoute(http.MethodPost, apiPath(literal("institutions"), canonicalID("institution_id"), literal("invitations"), literal("role")),
			append(academicRelationshipMutationErrorCodes("invitation.invalid", "invitation.role_not_delegable", "invitation.conflict", "invitation.mail_unavailable", "invitation.unavailable"),
				"authentication.strong_required", "authentication.reauthentication_required"), module.issueInstitutionRole),
		sessionRoute(http.MethodPost, apiPath(literal("invitations"), literal("institution-role"), literal("accept")),
			sessionAuthenticationMutationErrorCodes("authentication.rate_limited", "authentication.rate_limit_unavailable", "invitation.invalid", "invitation.unavailable"), module.acceptInstitutionRole),
	)
}

func (m invitationResourceModule) list(request operationRequest) (operationResult, error) {
	query, err := invitationListQuery(request.request)
	if err != nil {
		return operationResult{}, application.NewError("invitation.query.invalid").Wrap(err)
	}
	page, err := m.invitations.ListInvitations(request.context, request.invocation(), query)
	if err != nil {
		return operationResult{}, err
	}
	response := invitationListResponse{Items: make([]invitationAdministrationResponse, 0, len(page.Items))}
	for _, item := range page.Items {
		response.Items = append(response.Items, invitationAdministrationResponseFromView(item))
	}
	if page.More && len(page.Items) > 0 {
		last := page.Items[len(page.Items)-1]
		response.NextCursor = encodeInvitationCursor(invitationCursor{Version: 1,
			CreatedAt: model.TimeUTC(last.CreatedAt).Format(time.RFC3339Nano), ID: last.ID.String()})
	}
	return jsonResult(http.StatusOK, response).withHeaders(noStoreHeaders()), nil
}

func (m invitationResourceModule) get(request operationRequest) (operationResult, error) {
	id, err := request.params.RequireInvitationID()
	if err != nil {
		return operationResult{}, err
	}
	view, err := m.invitations.GetInvitation(request.context, request.invocation(), id)
	if err != nil {
		return operationResult{}, err
	}
	return jsonResult(http.StatusOK, invitationAdministrationResponseFromView(view)).withHeaders(noStoreHeaders()), nil
}

func (m invitationResourceModule) resend(request operationRequest) (operationResult, error) {
	return m.mutate(request, func(id string, revision int64) (application.InvitationAdministrationView, error) {
		return m.invitations.ResendInvitation(request.context, request.invocation(), application.ResendInvitationCommand{ID: id, ExpectedRevision: revision})
	})
}

func (m invitationResourceModule) revoke(request operationRequest) (operationResult, error) {
	return m.mutate(request, func(id string, revision int64) (application.InvitationAdministrationView, error) {
		return m.invitations.RevokeInvitation(request.context, request.invocation(), application.RevokeInvitationCommand{ID: id, ExpectedRevision: revision})
	})
}

func (m invitationResourceModule) mutate(request operationRequest, mutation func(string, int64) (application.InvitationAdministrationView, error)) (operationResult, error) {
	id, err := request.params.RequireInvitationID()
	if err != nil {
		return operationResult{}, err
	}
	var body invitationMutationRequest
	if err = request.decodeJSON(&body, "mutateInvitation"); err != nil {
		return operationResult{}, err
	}
	if body.ExpectedRevision < 1 {
		return operationResult{}, invalidRequestError("expected_revision", errors.New("must be positive"))
	}
	view, err := mutation(id, body.ExpectedRevision)
	if err != nil {
		return operationResult{}, err
	}
	return jsonResult(http.StatusOK, invitationAdministrationResponseFromView(view)).withHeaders(noStoreHeaders()), nil
}

func (m invitationResourceModule) replace(request operationRequest) (operationResult, error) {
	id, err := request.params.RequireInvitationID()
	if err != nil {
		return operationResult{}, err
	}
	var body replaceInvitationRequest
	if err = request.decodeJSON(&body, "replaceInvitation"); err != nil {
		return operationResult{}, err
	}
	if body.ExpectedRevision < 1 {
		return operationResult{}, invalidRequestError("expected_revision", errors.New("must be positive"))
	}
	view, err := m.invitations.ReplaceInvitation(request.context, request.invocation(), application.ReplaceInvitationCommand{
		ID: id, ExpectedRevision: body.ExpectedRevision, Purpose: body.Purpose, TargetEmail: body.Email,
		ClassID: body.ClassID, AcademicUnitID: body.AcademicUnitID, InstitutionID: body.InstitutionID, RoleID: body.RoleID,
		IntendedStartsAt: body.StartAt, IntendedEndsAt: body.EndAt, SuggestedUsername: body.SuggestedUsername,
		SuggestedDisplayName: body.SuggestedDisplayName, SuggestedFirstName: body.SuggestedFirstName,
		SuggestedLastName: body.SuggestedLastName, SuggestedLocale: body.SuggestedLocale,
	})
	if err != nil {
		return operationResult{}, err
	}
	return jsonResult(http.StatusCreated, invitationAdministrationResponseFromView(view)).withHeaders(noStoreHeaders()), nil
}

func invitationListQuery(request *http.Request) (application.ListInvitationsQuery, error) {
	values := request.URL.Query()
	query := application.ListInvitationsQuery{Limit: 50, TargetEmail: values.Get("email"), TargetID: values.Get("target_id")}
	if raw := values.Get("limit"); raw != "" {
		limit, err := strconv.Atoi(raw)
		if err != nil || limit < 1 || limit > 200 {
			return query, errors.New("invalid Invitation limit")
		}
		query.Limit = limit
	}
	if raw := values.Get("purpose"); raw != "" {
		query.Purpose = model.InvitationPurpose(raw)
		if !query.Purpose.IsValid() {
			return query, errors.New("invalid Invitation purpose")
		}
	}
	if raw := values.Get("state"); raw != "" {
		query.State = model.InvitationState(raw)
		if !query.State.IsValid() {
			return query, errors.New("invalid Invitation state")
		}
	}
	var err error
	if raw := values.Get("created_after"); raw != "" {
		query.CreatedAfter, err = parseInvitationFilterTime(raw)
		if err != nil {
			return query, err
		}
	}
	if raw := values.Get("created_before"); raw != "" {
		query.CreatedBefore, err = parseInvitationFilterTime(raw)
		if err != nil {
			return query, err
		}
	}
	if !query.CreatedAfter.IsZero() && !query.CreatedBefore.IsZero() && !query.CreatedBefore.After(query.CreatedAfter) {
		return query, errors.New("invalid Invitation time range")
	}
	if raw := values.Get("cursor"); raw != "" {
		cursor, err := decodeInvitationCursor(raw)
		if err != nil {
			return query, err
		}
		query.BeforeCreatedAt, _ = time.Parse(time.RFC3339Nano, cursor.CreatedAt)
		query.BeforeID = model.InvitationID(cursor.ID)
	}
	return query, nil
}

func parseInvitationFilterTime(raw string) (time.Time, error) {
	millis, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || millis <= 0 {
		return time.Time{}, errors.New("invalid Invitation time")
	}
	return model.TimeFromMillis(millis), nil
}

func encodeInvitationCursor(cursor invitationCursor) string {
	encoded, _ := json.Marshal(cursor)
	return base64.RawURLEncoding.EncodeToString(encoded)
}

func decodeInvitationCursor(raw string) (invitationCursor, error) {
	var cursor invitationCursor
	if len(raw) == 0 || len(raw) > 512 {
		return cursor, errors.New("invalid Invitation cursor")
	}
	decoded, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return cursor, err
	}
	decoder := json.NewDecoder(strings.NewReader(string(decoded)))
	decoder.DisallowUnknownFields()
	if err = decoder.Decode(&cursor); err != nil || cursor.Version != 1 || !model.InvitationID(cursor.ID).IsValid() {
		return cursor, errors.New("invalid Invitation cursor")
	}
	if parsed, parseErr := time.Parse(time.RFC3339Nano, cursor.CreatedAt); parseErr != nil || parsed.IsZero() {
		return cursor, errors.New("invalid Invitation cursor")
	}
	if decoder.Decode(&struct{}{}) == nil {
		return cursor, errors.New("invalid Invitation cursor")
	}
	return cursor, nil
}

func (m invitationResourceModule) issueAcademicUnitRole(request operationRequest) (operationResult, error) {
	unitID, err := request.params.RequireAcademicUnitId()
	if err != nil {
		return operationResult{}, err
	}
	var body issueScopedRoleInvitationRequest
	if err = request.decodeJSON(&body, "issueAcademicUnitRoleInvitation"); err != nil {
		return operationResult{}, err
	}
	created, err := m.invitations.IssueAcademicUnitRoleInvitation(request.context, request.invocation(), application.IssueAcademicUnitRoleInvitationCommand{
		TargetEmail: body.Email, AcademicUnitID: unitID, RoleID: body.RoleID, IntendedStartsAt: body.StartAt, IntendedEndsAt: body.EndAt,
	})
	if err != nil {
		return operationResult{}, err
	}
	return jsonResult(http.StatusCreated, invitationResponseFromView(created)), nil
}

func (m invitationResourceModule) issueInstitutionRole(request operationRequest) (operationResult, error) {
	institutionID, err := request.params.RequireInstitutionId()
	if err != nil {
		return operationResult{}, err
	}
	var body issueScopedRoleInvitationRequest
	if err = request.decodeJSON(&body, "issueInstitutionRoleInvitation"); err != nil {
		return operationResult{}, err
	}
	created, err := m.invitations.IssueInstitutionRoleInvitation(request.context, request.invocation(), application.IssueInstitutionRoleInvitationCommand{
		TargetEmail: body.Email, InstitutionID: institutionID, RoleID: body.RoleID, IntendedStartsAt: body.StartAt, IntendedEndsAt: body.EndAt,
	})
	if err != nil {
		return operationResult{}, err
	}
	return jsonResult(http.StatusCreated, invitationResponseFromView(created)), nil
}

func (m invitationResourceModule) acceptAcademicUnitRole(request operationRequest) (operationResult, error) {
	var body acceptScopedRoleInvitationRequest
	if err := request.decodeJSON(&body, "acceptAcademicUnitRoleInvitation"); err != nil {
		return operationResult{}, err
	}
	accepted, err := m.invitations.AcceptAcademicUnitRoleInvitation(request.context, request.invocation(), application.AcceptAcademicUnitRoleInvitationCommand{
		Claim: body.Claim, Source: request.request.RemoteAddr,
	})
	if err != nil {
		return operationResult{}, err
	}
	return jsonResult(http.StatusOK, invitationAcceptanceResponseFromView(accepted)), nil
}

func (m invitationResourceModule) acceptInstitutionRole(request operationRequest) (operationResult, error) {
	var body acceptScopedRoleInvitationRequest
	if err := request.decodeJSON(&body, "acceptInstitutionRoleInvitation"); err != nil {
		return operationResult{}, err
	}
	accepted, err := m.invitations.AcceptInstitutionRoleInvitation(request.context, request.invocation(), application.AcceptInstitutionRoleInvitationCommand{
		Claim: body.Claim, Source: request.request.RemoteAddr,
	})
	if err != nil {
		return operationResult{}, err
	}
	return jsonResult(http.StatusOK, invitationAcceptanceResponseFromView(accepted)), nil
}

func (m invitationResourceModule) issueTeacherAcademicUnit(request operationRequest) (operationResult, error) {
	unitID, err := request.params.RequireAcademicUnitId()
	if err != nil {
		return operationResult{}, err
	}
	var body issueTeacherAcademicUnitInvitationRequest
	if err = request.decodeJSON(&body, "issueTeacherAcademicUnitInvitation"); err != nil {
		return operationResult{}, err
	}
	created, err := m.invitations.IssueTeacherAcademicUnitInvitation(request.context, request.invocation(), application.IssueTeacherAcademicUnitInvitationCommand{
		TargetEmail: body.Email, AcademicUnitID: unitID, RoleID: body.RoleID, IntendedStartsAt: body.StartAt, IntendedEndsAt: body.EndAt,
		SuggestedUsername: body.SuggestedUsername, SuggestedDisplayName: body.SuggestedDisplayName,
		SuggestedFirstName: body.SuggestedFirstName, SuggestedLastName: body.SuggestedLastName, SuggestedLocale: body.SuggestedLocale,
	})
	if err != nil {
		return operationResult{}, err
	}
	return jsonResult(http.StatusCreated, invitationResponseFromView(created)), nil
}

func (m invitationResourceModule) acceptTeacherAcademicUnit(request operationRequest) (operationResult, error) {
	var body acceptTeacherAcademicUnitInvitationRequest
	if err := request.decodeJSON(&body, "acceptTeacherAcademicUnitInvitation"); err != nil {
		return operationResult{}, err
	}
	accepted, err := m.invitations.AcceptTeacherAcademicUnitInvitation(request.context, request.invocation(), application.AcceptTeacherAcademicUnitInvitationCommand{
		Claim: body.Claim, Password: body.Password, Username: body.Username, DisplayName: body.DisplayName,
		FirstName: body.FirstName, LastName: body.LastName, Locale: body.Locale, Timezone: body.Timezone, Source: request.request.RemoteAddr,
	})
	if err != nil {
		return operationResult{}, err
	}
	return jsonResult(http.StatusOK, invitationAcceptanceResponseFromView(accepted)), nil
}

func (m invitationResourceModule) issueStudentClass(request operationRequest) (operationResult, error) {
	classID, err := request.params.RequireClassId()
	if err != nil {
		return operationResult{}, err
	}
	var body issueStudentClassInvitationRequest
	if err = request.decodeJSON(&body, "issueStudentClassInvitation"); err != nil {
		return operationResult{}, err
	}
	created, err := m.invitations.IssueStudentClassInvitation(request.context, request.invocation(), application.IssueStudentClassInvitationCommand{
		TargetEmail: body.Email, ClassID: classID, IntendedStartsAt: body.StartAt, IntendedEndsAt: body.EndAt,
		SuggestedUsername: body.SuggestedUsername, SuggestedDisplayName: body.SuggestedDisplayName,
		SuggestedFirstName: body.SuggestedFirstName, SuggestedLastName: body.SuggestedLastName, SuggestedLocale: body.SuggestedLocale,
	})
	if err != nil {
		return operationResult{}, err
	}
	return jsonResult(http.StatusCreated, invitationResponseFromView(created)), nil
}

func (m invitationResourceModule) acceptStudentClass(request operationRequest) (operationResult, error) {
	var body acceptStudentClassInvitationRequest
	if err := request.decodeJSON(&body, "acceptStudentClassInvitation"); err != nil {
		return operationResult{}, err
	}
	accepted, err := m.invitations.AcceptStudentClassInvitation(request.context, request.invocation(), application.AcceptStudentClassInvitationCommand{
		Claim: body.Claim, Password: body.Password, Username: body.Username, DisplayName: body.DisplayName,
		FirstName: body.FirstName, LastName: body.LastName, Locale: body.Locale, Timezone: body.Timezone,
		Source: request.request.RemoteAddr,
	})
	if err != nil {
		return operationResult{}, err
	}
	return jsonResult(http.StatusOK, invitationAcceptanceResponseFromView(accepted)), nil
}

func invitationAcceptanceResponseFromView(accepted *application.InvitationAcceptanceView) invitationAcceptanceResponse {
	response := invitationAcceptanceResponse{Replayed: accepted.Replayed}
	if accepted.User != nil {
		response.UserID = accepted.User.ID.String()
	}
	response.InvitationID = accepted.Invitation.ID.String()
	if accepted.Affiliation != nil {
		response.AffiliationID = accepted.Affiliation.ID.String()
	}
	if accepted.ClassMember != nil {
		response.ClassMemberID = accepted.ClassMember.ID.String()
	}
	if accepted.AcademicUnitMember != nil {
		response.AcademicUnitMemberID = accepted.AcademicUnitMember.ID.String()
	}
	if accepted.RoleBinding != nil {
		response.RoleBindingID = accepted.RoleBinding.ID.String()
	}
	return response
}

func invitationResponseFromView(view application.InvitationView) invitationResponse {
	return invitationResponse{ID: view.ID.String(), Purpose: string(view.Purpose), State: string(view.State), ClassID: view.ClassID.String(),
		AcademicPeriodID: view.AcademicPeriodID.String(), AcademicUnitID: view.AcademicUnitID.String(), RoleID: view.RoleID.String(),
		RoleActions: append([]string(nil), view.RoleActions...), StartAt: view.IntendedStartsAt.UnixMilli(),
		EndAt: view.IntendedEndsAt.Millis(), ExpiresAt: view.ExpiresAt.UnixMilli()}
}

func invitationAdministrationResponseFromView(view application.InvitationAdministrationView) invitationAdministrationResponse {
	base := invitationResponseFromView(view.InvitationView)
	response := invitationAdministrationResponse{ID: base.ID, Purpose: base.Purpose, State: base.State,
		ClassID: base.ClassID, AcademicPeriodID: base.AcademicPeriodID, AcademicUnitID: base.AcademicUnitID,
		RoleID: base.RoleID, RoleActions: base.RoleActions, StartAt: base.StartAt, EndAt: base.EndAt, ExpiresAt: base.ExpiresAt,
		Email: view.TargetEmail, InviterUserID: view.InviterUserID.String(), AcceptedUserID: view.AcceptedUserID.String(),
		CreatedAt: model.MillisFromTime(view.CreatedAt), UpdatedAt: model.MillisFromTime(view.UpdatedAt), Revision: view.Revision}
	if view.Delivery != nil {
		response.Delivery = &invitationDeliveryResponse{TemplateKey: string(view.Delivery.TemplateKey), State: string(view.Delivery.State),
			MaskedRecipient: view.Delivery.MaskedRecipient, CreatedAt: model.MillisFromTime(view.Delivery.CreatedAt),
			UpdatedAt: model.MillisFromTime(view.Delivery.UpdatedAt), Deadline: model.MillisFromTime(view.Delivery.Deadline),
			AcceptedAt: view.Delivery.AcceptedAt.Millis(), PublicFailureCode: view.Delivery.PublicFailureCode}
	}
	return response
}
