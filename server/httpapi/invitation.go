// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package httpapi

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/sudosylabs/proctor/server/app"
	"github.com/sudosylabs/proctor/server/app/apperror"
	"github.com/sudosylabs/proctor/server/app/invocation"
	"github.com/sudosylabs/proctor/server/model"
)

type InvitationApplication interface {
	IssueStudentClassInvitation(context.Context, invocation.Invocation, app.IssueStudentClassInvitationCommand) (app.InvitationView, error)
	AcceptStudentClassInvitation(context.Context, invocation.Invocation, app.AcceptStudentClassInvitationCommand) (*app.InvitationAcceptanceView, error)
	IssueTeacherAcademicUnitInvitation(context.Context, invocation.Invocation, app.IssueTeacherAcademicUnitInvitationCommand) (app.InvitationView, error)
	AcceptTeacherAcademicUnitInvitation(context.Context, invocation.Invocation, app.AcceptTeacherAcademicUnitInvitationCommand) (*app.InvitationAcceptanceView, error)
	IssueAcademicUnitRoleInvitation(context.Context, invocation.Invocation, app.IssueAcademicUnitRoleInvitationCommand) (app.InvitationView, error)
	AcceptAcademicUnitRoleInvitation(context.Context, invocation.Invocation, app.AcceptAcademicUnitRoleInvitationCommand) (*app.InvitationAcceptanceView, error)
	IssueInstitutionRoleInvitation(context.Context, invocation.Invocation, app.IssueInstitutionRoleInvitationCommand) (app.InvitationView, error)
	AcceptInstitutionRoleInvitation(context.Context, invocation.Invocation, app.AcceptInstitutionRoleInvitationCommand) (*app.InvitationAcceptanceView, error)
	ListInvitations(context.Context, invocation.Invocation, app.ListInvitationsQuery) (app.InvitationAdministrationPage, error)
	GetInvitation(context.Context, invocation.Invocation, model.InvitationID) (app.InvitationAdministrationView, error)
	ResendInvitation(context.Context, invocation.Invocation, app.ResendInvitationCommand) (app.InvitationAdministrationView, error)
	RevokeInvitation(context.Context, invocation.Invocation, app.RevokeInvitationCommand) (app.InvitationAdministrationView, error)
	ReplaceInvitation(context.Context, invocation.Invocation, app.ReplaceInvitationCommand) (app.InvitationAdministrationView, error)
	RunInvitationBatch(context.Context, invocation.Invocation, app.RunInvitationBatchCommand) (app.InvitationBatchResult, error)
}

type issueStudentClassInvitationRequest struct {
	Email                string `json:"email"`
	StartAt              string `json:"start_at,omitempty"`
	EndAt                string `json:"end_at,omitempty"`
	SuggestedUsername    string `json:"suggested_username,omitempty"`
	SuggestedDisplayName string `json:"suggested_display_name,omitempty"`
	SuggestedFirstName   string `json:"suggested_first_name,omitempty"`
	SuggestedLastName    string `json:"suggested_last_name,omitempty"`
	SuggestedLocale      string `json:"suggested_locale,omitempty"`
	SuggestedTimezone    string `json:"suggested_timezone,omitempty"`
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
	StartAt              string `json:"start_at,omitempty"`
	EndAt                string `json:"end_at,omitempty"`
	SuggestedUsername    string `json:"suggested_username,omitempty"`
	SuggestedDisplayName string `json:"suggested_display_name,omitempty"`
	SuggestedFirstName   string `json:"suggested_first_name,omitempty"`
	SuggestedLastName    string `json:"suggested_last_name,omitempty"`
	SuggestedLocale      string `json:"suggested_locale,omitempty"`
	SuggestedTimezone    string `json:"suggested_timezone,omitempty"`
}

type acceptTeacherAcademicUnitInvitationRequest acceptStudentClassInvitationRequest

type issueScopedRoleInvitationRequest struct {
	Email   string `json:"email"`
	RoleID  string `json:"role_id"`
	StartAt string `json:"start_at,omitempty"`
	EndAt   string `json:"end_at,omitempty"`
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
	StartAt          string   `json:"start_at"`
	EndAt            string   `json:"end_at,omitempty"`
	ExpiresAt        string   `json:"expires_at"`
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
	StartAt              string `json:"start_at,omitempty"`
	EndAt                string `json:"end_at,omitempty"`
	SuggestedUsername    string `json:"suggested_username,omitempty"`
	SuggestedDisplayName string `json:"suggested_display_name,omitempty"`
	SuggestedFirstName   string `json:"suggested_first_name,omitempty"`
	SuggestedLastName    string `json:"suggested_last_name,omitempty"`
	SuggestedLocale      string `json:"suggested_locale,omitempty"`
	SuggestedTimezone    string `json:"suggested_timezone,omitempty"`
}

type invitationBatchItemRequest struct {
	Key                  string `json:"key"`
	InvitationID         string `json:"invitation_id,omitempty"`
	ExpectedRevision     int64  `json:"expected_revision,omitempty"`
	Email                string `json:"email,omitempty"`
	RoleID               string `json:"role_id,omitempty"`
	StartAt              string `json:"start_at,omitempty"`
	EndAt                string `json:"end_at,omitempty"`
	SuggestedUsername    string `json:"suggested_username,omitempty"`
	SuggestedDisplayName string `json:"suggested_display_name,omitempty"`
	SuggestedFirstName   string `json:"suggested_first_name,omitempty"`
	SuggestedLastName    string `json:"suggested_last_name,omitempty"`
	SuggestedLocale      string `json:"suggested_locale,omitempty"`
	SuggestedTimezone    string `json:"suggested_timezone,omitempty"`
}

type invitationBatchRequest struct {
	Operation string                       `json:"operation"`
	ScopeType string                       `json:"scope_type"`
	ScopeID   string                       `json:"scope_id"`
	Items     []invitationBatchItemRequest `json:"items"`
}

type invitationBatchItemResponse struct {
	Index        int    `json:"index"`
	Status       string `json:"status"`
	InvitationID string `json:"invitation_id,omitempty"`
	ErrorCode    string `json:"error_code,omitempty"`
}

type invitationBatchResponse struct {
	Operation string                        `json:"operation"`
	Items     []invitationBatchItemResponse `json:"items"`
	Succeeded int                           `json:"succeeded"`
	NoOp      int                           `json:"no_op"`
	Failed    int                           `json:"failed"`
}

type invitationDeliveryResponse struct {
	TemplateKey       string `json:"template_key"`
	State             string `json:"state"`
	MaskedRecipient   string `json:"masked_recipient"`
	CreatedAt         string `json:"created_at"`
	UpdatedAt         string `json:"updated_at"`
	Deadline          string `json:"deadline"`
	AcceptedAt        string `json:"accepted_at,omitempty"`
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
	StartAt          string                      `json:"start_at"`
	EndAt            string                      `json:"end_at,omitempty"`
	ExpiresAt        string                      `json:"expires_at"`
	Email            string                      `json:"email"`
	InviterUserID    string                      `json:"inviter_user_id"`
	AcceptedUserID   string                      `json:"accepted_user_id,omitempty"`
	CreatedAt        string                      `json:"created_at"`
	UpdatedAt        string                      `json:"updated_at"`
	Revision         int64                       `json:"revision"`
	Delivery         *invitationDeliveryResponse `json:"delivery,omitempty"`
}

type invitationListResponse struct {
	Items      []invitationAdministrationResponse `json:"items"`
	NextCursor string                             `json:"next_cursor,omitempty"`
}

type invitationCursor struct {
	CreatedAt string `json:"created_at"`
	ID        string `json:"id"`
}

type invitationResourceModule struct{ invitations InvitationApplication }

type unavailableInvitationApplication struct{}

func (unavailableInvitationApplication) IssueStudentClassInvitation(context.Context, invocation.Invocation, app.IssueStudentClassInvitationCommand) (app.InvitationView, error) {
	return app.InvitationView{}, errUnpresentableResult
}
func (unavailableInvitationApplication) AcceptStudentClassInvitation(context.Context, invocation.Invocation, app.AcceptStudentClassInvitationCommand) (*app.InvitationAcceptanceView, error) {
	return nil, errUnpresentableResult
}
func (unavailableInvitationApplication) IssueTeacherAcademicUnitInvitation(context.Context, invocation.Invocation, app.IssueTeacherAcademicUnitInvitationCommand) (app.InvitationView, error) {
	return app.InvitationView{}, errUnpresentableResult
}
func (unavailableInvitationApplication) AcceptTeacherAcademicUnitInvitation(context.Context, invocation.Invocation, app.AcceptTeacherAcademicUnitInvitationCommand) (*app.InvitationAcceptanceView, error) {
	return nil, errUnpresentableResult
}
func (unavailableInvitationApplication) IssueAcademicUnitRoleInvitation(context.Context, invocation.Invocation, app.IssueAcademicUnitRoleInvitationCommand) (app.InvitationView, error) {
	return app.InvitationView{}, errUnpresentableResult
}
func (unavailableInvitationApplication) AcceptAcademicUnitRoleInvitation(context.Context, invocation.Invocation, app.AcceptAcademicUnitRoleInvitationCommand) (*app.InvitationAcceptanceView, error) {
	return nil, errUnpresentableResult
}
func (unavailableInvitationApplication) IssueInstitutionRoleInvitation(context.Context, invocation.Invocation, app.IssueInstitutionRoleInvitationCommand) (app.InvitationView, error) {
	return app.InvitationView{}, errUnpresentableResult
}
func (unavailableInvitationApplication) AcceptInstitutionRoleInvitation(context.Context, invocation.Invocation, app.AcceptInstitutionRoleInvitationCommand) (*app.InvitationAcceptanceView, error) {
	return nil, errUnpresentableResult
}
func (unavailableInvitationApplication) ListInvitations(context.Context, invocation.Invocation, app.ListInvitationsQuery) (app.InvitationAdministrationPage, error) {
	return app.InvitationAdministrationPage{}, errUnpresentableResult
}
func (unavailableInvitationApplication) GetInvitation(context.Context, invocation.Invocation, model.InvitationID) (app.InvitationAdministrationView, error) {
	return app.InvitationAdministrationView{}, errUnpresentableResult
}
func (unavailableInvitationApplication) ResendInvitation(context.Context, invocation.Invocation, app.ResendInvitationCommand) (app.InvitationAdministrationView, error) {
	return app.InvitationAdministrationView{}, errUnpresentableResult
}
func (unavailableInvitationApplication) RevokeInvitation(context.Context, invocation.Invocation, app.RevokeInvitationCommand) (app.InvitationAdministrationView, error) {
	return app.InvitationAdministrationView{}, errUnpresentableResult
}
func (unavailableInvitationApplication) ReplaceInvitation(context.Context, invocation.Invocation, app.ReplaceInvitationCommand) (app.InvitationAdministrationView, error) {
	return app.InvitationAdministrationView{}, errUnpresentableResult
}
func (unavailableInvitationApplication) RunInvitationBatch(context.Context, invocation.Invocation, app.RunInvitationBatchCommand) (app.InvitationBatchResult, error) {
	return app.InvitationBatchResult{}, errUnpresentableResult
}

func invitationResource(invitations InvitationApplication) apiResource {
	module := invitationResourceModule{invitations: invitations}
	return newResource("invitations",
		idempotentPrincipalRoute(IdempotencyRequired, http.MethodPost, apiPath(literal("invitation-batches")),
			[]string{"authorization.denied", "authorization.request.invalid", "authorization.unavailable", "audit.unavailable", "request.invalid", "resource.not_found", "authentication.strong_required", "authentication.reauthentication_required", "invitation.unavailable", "idempotency.key_required", "idempotency.key_invalid"}, module.batch),
		principalRoute(http.MethodGet, apiPath(literal("invitations")),
			[]string{"authorization.denied", "authorization.request.invalid", "authorization.unavailable", "request.invalid", "resource.not_found", "invitation.query.invalid", "invitation.unavailable"}, module.list),
		principalRoute(http.MethodGet, apiPath(literal("invitations"), canonicalID("invitation_id")),
			[]string{"authorization.denied", "authorization.request.invalid", "authorization.unavailable", "request.invalid", "resource.not_found", "invitation.unavailable"}, module.get),
		principalRoute(http.MethodPost, apiPath(literal("invitations"), canonicalID("invitation_id"), literal("resend")),
			[]string{"authorization.denied", "authorization.request.invalid", "authorization.unavailable", "audit.unavailable", "request.invalid", "resource.not_found", "invitation.conflict", "invitation.mail_unavailable", "invitation.unavailable", "authentication.reauthentication_required", "authentication.strong_required", "onboarding_batch.duplicate"}, module.resend),
		principalRoute(http.MethodPost, apiPath(literal("invitations"), canonicalID("invitation_id"), literal("revoke")),
			[]string{"authorization.denied", "authorization.request.invalid", "authorization.unavailable", "audit.unavailable", "request.invalid", "resource.not_found", "invitation.conflict", "invitation.mail_unavailable", "invitation.unavailable", "authentication.reauthentication_required", "authentication.strong_required", "onboarding_batch.duplicate"}, module.revoke),
		principalRoute(http.MethodPost, apiPath(literal("invitations"), canonicalID("invitation_id"), literal("replacement")),
			[]string{"authorization.denied", "authorization.request.invalid", "authorization.unavailable", "audit.unavailable", "request.invalid", "resource.not_found", "invitation.invalid", "invitation.role_not_delegable", "invitation.conflict", "invitation.mail_unavailable", "invitation.unavailable", "authentication.strong_required", "authentication.reauthentication_required"}, module.replace),
		principalRoute(http.MethodPost, apiPath(literal("classes"), canonicalID("class_id"), literal("invitations"), literal("student")),
			[]string{"authorization.denied", "authorization.request.invalid", "authorization.unavailable", "audit.unavailable", "request.invalid", "resource.not_found", "invitation.invalid", "invitation.class_period_conflict", "invitation.conflict", "invitation.mail_unavailable", "invitation.unavailable", "onboarding_batch.duplicate"}, module.issueStudentClass),
		publicRoute(http.MethodPost, apiPath(literal("invitations"), literal("student-class"), literal("accept")),
			[]string{"request.invalid", "authentication.rate_limited", "authentication.rate_limit_unavailable", "invitation.invalid", "invitation.user_invalid", "invitation.mail_unavailable", "invitation.unavailable", "authentication.password.invalid", "service.busy"}, module.acceptStudentClass),
		principalRoute(http.MethodPost, apiPath(literal("academic-units"), canonicalID("academic_unit_id"), literal("invitations"), literal("teacher")),
			[]string{"authorization.denied", "authorization.request.invalid", "authorization.unavailable", "audit.unavailable", "request.invalid", "resource.not_found", "invitation.invalid", "invitation.role_not_delegable", "invitation.conflict", "invitation.mail_unavailable", "invitation.unavailable", "onboarding_batch.duplicate"}, module.issueTeacherAcademicUnit),
		publicRoute(http.MethodPost, apiPath(literal("invitations"), literal("teacher-academic-unit"), literal("accept")),
			[]string{"request.invalid", "authentication.rate_limited", "authentication.rate_limit_unavailable", "invitation.invalid", "invitation.user_invalid", "invitation.mail_unavailable", "invitation.unavailable", "authentication.password.invalid", "service.busy"}, module.acceptTeacherAcademicUnit),
		principalRoute(http.MethodPost, apiPath(literal("academic-units"), canonicalID("academic_unit_id"), literal("invitations"), literal("role")),
			[]string{"authorization.denied", "authorization.request.invalid", "authorization.unavailable", "audit.unavailable", "request.invalid", "resource.not_found", "invitation.invalid", "invitation.role_not_delegable", "invitation.conflict", "invitation.mail_unavailable", "invitation.unavailable", "onboarding_batch.duplicate"}, module.issueAcademicUnitRole),
		sessionRoute(http.MethodPost, apiPath(literal("invitations"), literal("academic-unit-role"), literal("accept")),
			[]string{"authentication.rate_limited", "authentication.rate_limit_unavailable", "invitation.invalid", "audit.unavailable", "request.invalid"}, module.acceptAcademicUnitRole),
		strongRecentSessionRoute(http.MethodPost, apiPath(literal("institutions"), canonicalID("institution_id"), literal("invitations"), literal("role")),
			[]string{"authorization.denied", "authorization.request.invalid", "authorization.unavailable", "audit.unavailable", "request.invalid", "resource.not_found", "invitation.invalid", "invitation.role_not_delegable", "invitation.conflict", "invitation.mail_unavailable", "invitation.unavailable", "onboarding_batch.duplicate"}, module.issueInstitutionRole),
		sessionRoute(http.MethodPost, apiPath(literal("invitations"), literal("institution-role"), literal("accept")),
			[]string{"authentication.rate_limited", "authentication.rate_limit_unavailable", "invitation.invalid", "audit.unavailable", "request.invalid"}, module.acceptInstitutionRole),
	)
}

func (m invitationResourceModule) batch(request operationRequest) (operationResult, error) {
	var body invitationBatchRequest
	if err := request.decodeJSON(&body); err != nil {
		return operationResult{}, invalidRequestError(err)
	}
	items := make([]app.InvitationBatchItemCommand, 0, len(body.Items))
	for _, item := range body.Items {
		startAt, endAt, err := wireWindowMillis(item.StartAt, item.EndAt)
		if err != nil {
			return operationResult{}, err
		}
		items = append(items, app.InvitationBatchItemCommand{IdempotencyKey: item.Key, InvitationID: model.InvitationID(item.InvitationID),
			ExpectedRevision: item.ExpectedRevision, TargetEmail: item.Email, RoleID: model.RoleID(item.RoleID),
			IntendedStartsAt: startAt, IntendedEndsAt: endAt, SuggestedUsername: item.SuggestedUsername,
			SuggestedDisplayName: item.SuggestedDisplayName, SuggestedFirstName: item.SuggestedFirstName,
			SuggestedLastName: item.SuggestedLastName, SuggestedLocale: item.SuggestedLocale, SuggestedTimezone: item.SuggestedTimezone})
	}
	result, err := m.invitations.RunInvitationBatch(request.context, request.invocation(), app.RunInvitationBatchCommand{
		Operation: app.InvitationBatchOperation(body.Operation), ScopeType: model.RoleScopeType(body.ScopeType),
		ScopeID: body.ScopeID, IdempotencyKey: request.idempotencyKey, Items: items,
	})
	if err != nil {
		return operationResult{}, err
	}
	response := invitationBatchResponse{Operation: string(result.Operation), Items: make([]invitationBatchItemResponse, 0, len(result.Items)),
		Succeeded: result.Succeeded, NoOp: result.NoOp, Failed: result.Failed}
	for _, item := range result.Items {
		response.Items = append(response.Items, invitationBatchItemResponse{Index: item.Index, Status: string(item.Status),
			InvitationID: item.InvitationID.String(), ErrorCode: item.ErrorCode})
	}
	return jsonResult(http.StatusOK, response), nil
}

func (m invitationResourceModule) list(request operationRequest) (operationResult, error) {
	query, err := invitationListQuery(request.request)
	if err != nil {
		return operationResult{}, apperror.Invalid("request.invalid", "query").Wrap(err)
	}
	page, err := m.invitations.ListInvitations(request.context, request.invocation(), query)
	if err != nil {
		return operationResult{}, err
	}
	response := invitationListResponse{Items: make([]invitationAdministrationResponse, 0, len(page.Items))}
	for _, item := range page.Items {
		response.Items = append(response.Items, invitationAdministrationResponseFromView(item))
	}
	if page.More && len(page.Items) == 0 {
		return operationResult{}, unpresentableResult(errors.New("invitation page made no progress"))
	}
	if page.More {
		last := page.Items[len(page.Items)-1]
		response.NextCursor, err = encodeInvitationCursor(invitationCursor{
			CreatedAt: model.FormatRFC3339(last.CreatedAt), ID: last.ID.String()})
		if err != nil {
			return operationResult{}, unpresentableResult(err)
		}
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
	return m.mutate(request, func(id model.InvitationID, revision int64) (app.InvitationAdministrationView, error) {
		return m.invitations.ResendInvitation(request.context, request.invocation(), app.ResendInvitationCommand{ID: id, ExpectedRevision: revision})
	})
}

func (m invitationResourceModule) revoke(request operationRequest) (operationResult, error) {
	return m.mutate(request, func(id model.InvitationID, revision int64) (app.InvitationAdministrationView, error) {
		return m.invitations.RevokeInvitation(request.context, request.invocation(), app.RevokeInvitationCommand{ID: id, ExpectedRevision: revision})
	})
}

func (m invitationResourceModule) mutate(request operationRequest, mutation func(model.InvitationID, int64) (app.InvitationAdministrationView, error)) (operationResult, error) {
	id, err := request.params.RequireInvitationID()
	if err != nil {
		return operationResult{}, err
	}
	var body invitationMutationRequest
	if err = request.decodeJSON(&body); err != nil {
		return operationResult{}, err
	}
	if body.ExpectedRevision < 1 {
		return operationResult{}, invalidRequestError(errors.New("must be positive"))
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
	if err = request.decodeJSON(&body); err != nil {
		return operationResult{}, err
	}
	if body.ExpectedRevision < 1 {
		return operationResult{}, invalidRequestError(errors.New("must be positive"))
	}
	startAt, endAt, err := wireWindowMillis(body.StartAt, body.EndAt)
	if err != nil {
		return operationResult{}, err
	}
	view, err := m.invitations.ReplaceInvitation(request.context, request.invocation(), app.ReplaceInvitationCommand{
		ID: id, ExpectedRevision: body.ExpectedRevision, Purpose: body.Purpose, TargetEmail: body.Email,
		ClassID: model.ClassID(body.ClassID), AcademicUnitID: model.AcademicUnitID(body.AcademicUnitID), InstitutionID: model.InstitutionID(body.InstitutionID), RoleID: model.RoleID(body.RoleID),
		IntendedStartsAt: startAt, IntendedEndsAt: endAt, SuggestedUsername: body.SuggestedUsername,
		SuggestedDisplayName: body.SuggestedDisplayName, SuggestedFirstName: body.SuggestedFirstName,
		SuggestedLastName: body.SuggestedLastName, SuggestedLocale: body.SuggestedLocale, SuggestedTimezone: body.SuggestedTimezone,
	})
	if err != nil {
		return operationResult{}, err
	}
	return jsonResult(http.StatusCreated, invitationAdministrationResponseFromView(view)).withHeaders(noStoreHeaders()), nil
}

func invitationListQuery(request *http.Request) (app.ListInvitationsQuery, error) {
	values := request.URL.Query()
	query := app.ListInvitationsQuery{Limit: 50, TargetEmail: values.Get("email"), TargetID: values.Get("target_id")}
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
		query.BeforeCreatedAt, _ = model.ParseRFC3339(cursor.CreatedAt)
		query.BeforeID = model.InvitationID(cursor.ID)
	}
	return query, nil
}

func parseInvitationFilterTime(raw string) (time.Time, error) {
	parsed, err := model.ParseRFC3339(raw)
	if err != nil || parsed.IsZero() {
		return time.Time{}, errors.New("invalid Invitation time")
	}
	return parsed, nil
}

func encodeInvitationCursor(cursor invitationCursor) (string, error) {
	return encodeOpaqueCursor(cursor, invitationCursorSpec())
}

func decodeInvitationCursor(raw string) (invitationCursor, error) {
	return decodeOpaqueCursor(raw, invitationCursorSpec())
}

func invitationCursorSpec() opaqueCursorSpec[invitationCursor] {
	return opaqueCursorSpec[invitationCursor]{
		label: "Invitation", maximumEncodedLength: defaultOpaqueCursorMaximumEncodedLength,
		members: []string{"created_at", "id"},
		validate: func(cursor invitationCursor) error {
			createdAt, err := model.ParseRFC3339(cursor.CreatedAt)
			if err != nil || createdAt.IsZero() || !model.InvitationID(cursor.ID).IsValid() {
				return errors.New("invalid Invitation keyset")
			}
			return nil
		},
	}
}

func (m invitationResourceModule) issueAcademicUnitRole(request operationRequest) (operationResult, error) {
	unitID, err := request.params.RequireAcademicUnitID()
	if err != nil {
		return operationResult{}, err
	}
	var body issueScopedRoleInvitationRequest
	if err = request.decodeJSON(&body); err != nil {
		return operationResult{}, err
	}
	startAt, endAt, err := wireWindowMillis(body.StartAt, body.EndAt)
	if err != nil {
		return operationResult{}, err
	}
	created, err := m.invitations.IssueAcademicUnitRoleInvitation(request.context, request.invocation(), app.IssueAcademicUnitRoleInvitationCommand{
		TargetEmail: body.Email, AcademicUnitID: unitID, RoleID: model.RoleID(body.RoleID), IntendedStartsAt: startAt, IntendedEndsAt: endAt,
	})
	if err != nil {
		return operationResult{}, err
	}
	return jsonResult(http.StatusCreated, invitationResponseFromView(created)), nil
}

func (m invitationResourceModule) issueInstitutionRole(request operationRequest) (operationResult, error) {
	institutionID, err := request.params.RequireInstitutionID()
	if err != nil {
		return operationResult{}, err
	}
	var body issueScopedRoleInvitationRequest
	if err = request.decodeJSON(&body); err != nil {
		return operationResult{}, err
	}
	startAt, endAt, err := wireWindowMillis(body.StartAt, body.EndAt)
	if err != nil {
		return operationResult{}, err
	}
	created, err := m.invitations.IssueInstitutionRoleInvitation(request.context, request.invocation(), app.IssueInstitutionRoleInvitationCommand{
		TargetEmail: body.Email, InstitutionID: institutionID, RoleID: model.RoleID(body.RoleID), IntendedStartsAt: startAt, IntendedEndsAt: endAt,
	})
	if err != nil {
		return operationResult{}, err
	}
	return jsonResult(http.StatusCreated, invitationResponseFromView(created)), nil
}

func (m invitationResourceModule) acceptAcademicUnitRole(request operationRequest) (operationResult, error) {
	var body acceptScopedRoleInvitationRequest
	if err := request.decodeJSON(&body); err != nil {
		return operationResult{}, err
	}
	accepted, err := m.invitations.AcceptAcademicUnitRoleInvitation(request.context, request.invocation(), app.AcceptAcademicUnitRoleInvitationCommand{
		Claim: body.Claim, Source: request.metadata.IPAddress,
	})
	if err != nil {
		return operationResult{}, err
	}
	return jsonResult(http.StatusOK, invitationAcceptanceResponseFromView(accepted)), nil
}

func (m invitationResourceModule) acceptInstitutionRole(request operationRequest) (operationResult, error) {
	var body acceptScopedRoleInvitationRequest
	if err := request.decodeJSON(&body); err != nil {
		return operationResult{}, err
	}
	accepted, err := m.invitations.AcceptInstitutionRoleInvitation(request.context, request.invocation(), app.AcceptInstitutionRoleInvitationCommand{
		Claim: body.Claim, Source: request.metadata.IPAddress,
	})
	if err != nil {
		return operationResult{}, err
	}
	return jsonResult(http.StatusOK, invitationAcceptanceResponseFromView(accepted)), nil
}

func (m invitationResourceModule) issueTeacherAcademicUnit(request operationRequest) (operationResult, error) {
	unitID, err := request.params.RequireAcademicUnitID()
	if err != nil {
		return operationResult{}, err
	}
	var body issueTeacherAcademicUnitInvitationRequest
	if err = request.decodeJSON(&body); err != nil {
		return operationResult{}, err
	}
	startAt, endAt, err := wireWindowMillis(body.StartAt, body.EndAt)
	if err != nil {
		return operationResult{}, err
	}
	created, err := m.invitations.IssueTeacherAcademicUnitInvitation(request.context, request.invocation(), app.IssueTeacherAcademicUnitInvitationCommand{
		TargetEmail: body.Email, AcademicUnitID: unitID, RoleID: model.RoleID(body.RoleID), IntendedStartsAt: startAt, IntendedEndsAt: endAt,
		SuggestedUsername: body.SuggestedUsername, SuggestedDisplayName: body.SuggestedDisplayName,
		SuggestedFirstName: body.SuggestedFirstName, SuggestedLastName: body.SuggestedLastName, SuggestedLocale: body.SuggestedLocale, SuggestedTimezone: body.SuggestedTimezone,
	})
	if err != nil {
		return operationResult{}, err
	}
	return jsonResult(http.StatusCreated, invitationResponseFromView(created)), nil
}

func (m invitationResourceModule) acceptTeacherAcademicUnit(request operationRequest) (operationResult, error) {
	var body acceptTeacherAcademicUnitInvitationRequest
	if err := request.decodeJSON(&body); err != nil {
		return operationResult{}, err
	}
	accepted, err := m.invitations.AcceptTeacherAcademicUnitInvitation(request.context, request.invocation(), app.AcceptTeacherAcademicUnitInvitationCommand{
		Claim: body.Claim, Password: body.Password, Username: body.Username, DisplayName: body.DisplayName,
		FirstName: body.FirstName, LastName: body.LastName, Locale: body.Locale, Timezone: body.Timezone, Source: request.metadata.IPAddress,
	})
	if err != nil {
		return operationResult{}, err
	}
	return jsonResult(http.StatusOK, invitationAcceptanceResponseFromView(accepted)), nil
}

func (m invitationResourceModule) issueStudentClass(request operationRequest) (operationResult, error) {
	classID, err := request.params.RequireClassID()
	if err != nil {
		return operationResult{}, err
	}
	var body issueStudentClassInvitationRequest
	if err = request.decodeJSON(&body); err != nil {
		return operationResult{}, err
	}
	startAt, endAt, err := wireWindowMillis(body.StartAt, body.EndAt)
	if err != nil {
		return operationResult{}, err
	}
	created, err := m.invitations.IssueStudentClassInvitation(request.context, request.invocation(), app.IssueStudentClassInvitationCommand{
		TargetEmail: body.Email, ClassID: classID, IntendedStartsAt: startAt, IntendedEndsAt: endAt,
		SuggestedUsername: body.SuggestedUsername, SuggestedDisplayName: body.SuggestedDisplayName,
		SuggestedFirstName: body.SuggestedFirstName, SuggestedLastName: body.SuggestedLastName, SuggestedLocale: body.SuggestedLocale, SuggestedTimezone: body.SuggestedTimezone,
	})
	if err != nil {
		return operationResult{}, err
	}
	return jsonResult(http.StatusCreated, invitationResponseFromView(created)), nil
}

func (m invitationResourceModule) acceptStudentClass(request operationRequest) (operationResult, error) {
	var body acceptStudentClassInvitationRequest
	if err := request.decodeJSON(&body); err != nil {
		return operationResult{}, err
	}
	accepted, err := m.invitations.AcceptStudentClassInvitation(request.context, request.invocation(), app.AcceptStudentClassInvitationCommand{
		Claim: body.Claim, Password: body.Password, Username: body.Username, DisplayName: body.DisplayName,
		FirstName: body.FirstName, LastName: body.LastName, Locale: body.Locale, Timezone: body.Timezone,
		Source: request.metadata.IPAddress,
	})
	if err != nil {
		return operationResult{}, err
	}
	return jsonResult(http.StatusOK, invitationAcceptanceResponseFromView(accepted)), nil
}

func invitationAcceptanceResponseFromView(accepted *app.InvitationAcceptanceView) invitationAcceptanceResponse {
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

func invitationResponseFromView(view app.InvitationView) invitationResponse {
	return invitationResponse{ID: view.ID.String(), Purpose: string(view.Purpose), State: string(view.State), ClassID: view.ClassID.String(),
		AcademicPeriodID: view.AcademicPeriodID.String(), AcademicUnitID: view.AcademicUnitID.String(), RoleID: view.RoleID.String(),
		RoleActions: append([]string(nil), view.RoleActions...), StartAt: model.FormatRFC3339(view.IntendedStartsAt),
		EndAt: view.IntendedEndsAt.FormatRFC3339(), ExpiresAt: model.FormatRFC3339(view.ExpiresAt)}
}

func invitationAdministrationResponseFromView(view app.InvitationAdministrationView) invitationAdministrationResponse {
	base := invitationResponseFromView(view.InvitationView)
	response := invitationAdministrationResponse{ID: base.ID, Purpose: base.Purpose, State: base.State,
		ClassID: base.ClassID, AcademicPeriodID: base.AcademicPeriodID, AcademicUnitID: base.AcademicUnitID,
		RoleID: base.RoleID, RoleActions: base.RoleActions, StartAt: base.StartAt, EndAt: base.EndAt, ExpiresAt: base.ExpiresAt,
		Email: view.TargetEmail, InviterUserID: view.InviterUserID.String(), AcceptedUserID: view.AcceptedUserID.String(),
		CreatedAt: model.FormatRFC3339(view.CreatedAt), UpdatedAt: model.FormatRFC3339(view.UpdatedAt), Revision: view.Revision}
	if view.Delivery != nil {
		response.Delivery = &invitationDeliveryResponse{TemplateKey: string(view.Delivery.TemplateKey), State: string(view.Delivery.State),
			MaskedRecipient: view.Delivery.MaskedRecipient, CreatedAt: model.FormatRFC3339(view.Delivery.CreatedAt),
			UpdatedAt: model.FormatRFC3339(view.Delivery.UpdatedAt), Deadline: model.FormatRFC3339(view.Delivery.Deadline),
			AcceptedAt: view.Delivery.AcceptedAt.FormatRFC3339(), PublicFailureCode: view.Delivery.PublicFailureCode}
	}
	return response
}
