// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package httpapi

import (
	"net/http"

	application "github.com/sudosylabs/proctor/server/app"
	"github.com/sudosylabs/proctor/server/model"
)

type classMemberResponse struct {
	ID               string `json:"id"`
	CreateAt         int64  `json:"create_at"`
	UpdateAt         int64  `json:"update_at"`
	DeleteAt         int64  `json:"delete_at"`
	ClassID          string `json:"class_id"`
	AcademicPeriodID string `json:"academic_period_id"`
	UserID           string `json:"user_id"`
	StartAt          int64  `json:"start_at"`
	EndAt            int64  `json:"end_at,omitempty"`
}

type enrollClassMemberRequest struct {
	ID               string `json:"id"`
	CreateAt         int64  `json:"create_at"`
	UpdateAt         int64  `json:"update_at"`
	DeleteAt         int64  `json:"delete_at"`
	ClassID          string `json:"class_id"`
	AcademicPeriodID string `json:"academic_period_id"`
	UserID           string `json:"user_id"`
	StartAt          int64  `json:"start_at"`
	EndAt            int64  `json:"end_at"`
}

type classEnrollmentResponse struct {
	Membership classMemberResponse  `json:"membership"`
	Previous   *classMemberResponse `json:"previous,omitempty"`
}

type classMemberResourceModule struct {
	members ClassMemberApplication
}

func classMemberResource(members ClassMemberApplication) resource {
	module := classMemberResourceModule{members: members}
	return newResource(
		"class-members",
		principalRoute(
			http.MethodGet,
			apiPath(literal("classes"), canonicalID("class_id"), literal("members")),
			academicRelationshipReadErrorCodes(),
			module.list,
		),
		principalRoute(
			http.MethodPost,
			apiPath(literal("classes"), canonicalID("class_id"), literal("members")),
			academicRelationshipMutationErrorCodes("class_member.invalid", "class_member.student_affiliation_required", "class.enrollment_conflict", "mail.unavailable"),
			module.enroll,
		),
		principalRoute(
			http.MethodDelete,
			apiPath(literal("class-members"), canonicalID("class_member_id")),
			academicRelationshipMutationErrorCodes("class.enrollment_conflict", "mail.unavailable"),
			module.end,
		),
	)
}

func (module classMemberResourceModule) list(request operationRequest) (operationResult, error) {
	classID, err := request.params.RequireClassId()
	if err != nil {
		return operationResult{}, err
	}
	activeAt, err := parseActiveAt(request.request)
	if err != nil {
		return operationResult{}, err
	}
	members, err := module.members.ListClassMembers(request.context, request.invocation(), application.ListClassMembersQuery{ClassID: classID, ActiveAt: activeAt})
	if err != nil {
		return operationResult{}, err
	}
	return jsonResult(http.StatusOK, classMemberResponses(members)), nil
}

func (module classMemberResourceModule) enroll(request operationRequest) (operationResult, error) {
	classID, err := request.params.RequireClassId()
	if err != nil {
		return operationResult{}, err
	}
	var body enrollClassMemberRequest
	if err := request.decodeJSON(&body, "enrollClassMember"); err != nil {
		return operationResult{}, err
	}
	enrollment, err := module.members.EnrollClassMember(request.context, request.invocation(), application.EnrollClassMemberCommand{ClassID: classID, UserID: body.UserID, StartAt: body.StartAt, EndAt: body.EndAt})
	if err != nil {
		return operationResult{}, err
	}
	return jsonResult(http.StatusCreated, classEnrollmentResponseFromModel(enrollment)), nil
}

func (module classMemberResourceModule) end(request operationRequest) (operationResult, error) {
	id, err := request.params.RequireClassMemberId()
	if err != nil {
		return operationResult{}, err
	}
	ended, err := module.members.EndClassMember(request.context, request.invocation(), application.EndClassMemberCommand{ID: id})
	if err != nil {
		return operationResult{}, err
	}
	return jsonResult(http.StatusOK, classMemberResponseFromModel(ended)), nil
}

func classMemberResponseFromModel(member *model.ClassMember) classMemberResponse {
	if member == nil {
		return classMemberResponse{}
	}
	return classMemberResponse{
		ID:               member.ID.String(),
		CreateAt:         model.MillisFromTime(member.CreatedAt),
		UpdateAt:         model.MillisFromTime(member.UpdatedAt),
		DeleteAt:         member.ArchivedAt.Millis(),
		ClassID:          member.ClassID.String(),
		AcademicPeriodID: member.AcademicPeriodID.String(),
		UserID:           member.UserID.String(),
		StartAt:          model.MillisFromTime(member.StartsAt),
		EndAt:            member.EndsAt.Millis(),
	}
}

func classMemberResponses(members []*model.ClassMember) []classMemberResponse {
	result := make([]classMemberResponse, 0, len(members))
	for _, member := range members {
		result = append(result, classMemberResponseFromModel(member))
	}
	return result
}

func classEnrollmentResponseFromModel(enrollment *model.ClassEnrollment) classEnrollmentResponse {
	if enrollment == nil {
		return classEnrollmentResponse{}
	}
	response := classEnrollmentResponse{Membership: classMemberResponseFromModel(enrollment.Membership)}
	if enrollment.Previous != nil {
		previous := classMemberResponseFromModel(enrollment.Previous)
		response.Previous = &previous
	}
	return response
}
