// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package api

import (
	"context"
	"net/http"

	application "github.com/sudosylabs/proctor/server/app"
)

type InvitationApplication interface {
	IssueStudentClassInvitation(context.Context, application.Invocation, application.IssueStudentClassInvitationCommand) (application.InvitationView, error)
	AcceptStudentClassInvitation(context.Context, application.Invocation, application.AcceptStudentClassInvitationCommand) (*application.InvitationAcceptanceView, error)
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

type invitationResponse struct {
	ID               string `json:"id"`
	Purpose          string `json:"purpose"`
	State            string `json:"state"`
	ClassID          string `json:"class_id"`
	AcademicPeriodID string `json:"academic_period_id"`
	StartAt          int64  `json:"start_at"`
	EndAt            int64  `json:"end_at,omitempty"`
	ExpiresAt        int64  `json:"expires_at"`
}

type invitationAcceptanceResponse struct {
	UserID        string `json:"user_id"`
	InvitationID  string `json:"invitation_id"`
	AffiliationID string `json:"affiliation_id"`
	ClassMemberID string `json:"class_member_id"`
	Replayed      bool   `json:"replayed"`
}

type invitationResourceModule struct{ invitations InvitationApplication }

type unavailableInvitationApplication struct{}

func (unavailableInvitationApplication) IssueStudentClassInvitation(context.Context, application.Invocation, application.IssueStudentClassInvitationCommand) (application.InvitationView, error) {
	return application.InvitationView{}, application.NewError("invitation.unavailable")
}
func (unavailableInvitationApplication) AcceptStudentClassInvitation(context.Context, application.Invocation, application.AcceptStudentClassInvitationCommand) (*application.InvitationAcceptanceView, error) {
	return nil, application.NewError("invitation.unavailable")
}

func invitationResource(invitations InvitationApplication) resource {
	module := invitationResourceModule{invitations: invitations}
	return newResource("invitations",
		principalRoute(http.MethodPost, apiPath(literal("classes"), canonicalID("class_id"), literal("invitations"), literal("student")),
			academicRelationshipMutationErrorCodes("invitation.invalid", "invitation.class_period_invalid", "invitation.conflict", "invitation.mail_unavailable", "invitation.unavailable"), module.issueStudentClass),
		publicRoute(http.MethodPost, apiPath(literal("invitations"), literal("student-class"), literal("accept")),
			[]string{"request.invalid", "authentication.rate_limited", "authentication.rate_limit_unavailable", "invitation.invalid", "invitation.user_invalid", "invitation.mail_unavailable", "invitation.unavailable", "authentication.password.invalid"}, module.acceptStudentClass),
	)
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
	return jsonResult(http.StatusOK, response), nil
}

func invitationResponseFromView(view application.InvitationView) invitationResponse {
	return invitationResponse{ID: view.ID.String(), Purpose: string(view.Purpose), State: string(view.State), ClassID: view.ClassID.String(),
		AcademicPeriodID: view.AcademicPeriodID.String(), StartAt: view.IntendedStartsAt.UnixMilli(),
		EndAt: view.IntendedEndsAt.Millis(), ExpiresAt: view.ExpiresAt.UnixMilli()}
}
