// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package httpapi

import (
	"net/http"

	application "github.com/sudosylabs/proctor/server/app"
	"github.com/sudosylabs/proctor/server/model"
)

type academicUnitMemberResponse struct {
	ID             string `json:"id"`
	CreateAt       int64  `json:"create_at"`
	UpdateAt       int64  `json:"update_at"`
	DeleteAt       int64  `json:"delete_at"`
	AcademicUnitID string `json:"academic_unit_id"`
	UserID         string `json:"user_id"`
	StartAt        int64  `json:"start_at"`
	EndAt          int64  `json:"end_at,omitempty"`
}

type createAcademicUnitMemberRequest struct {
	ID             string `json:"id"`
	CreateAt       int64  `json:"create_at"`
	UpdateAt       int64  `json:"update_at"`
	DeleteAt       int64  `json:"delete_at"`
	AcademicUnitID string `json:"academic_unit_id"`
	UserID         string `json:"user_id"`
	StartAt        int64  `json:"start_at"`
	EndAt          int64  `json:"end_at"`
}

type academicUnitMemberResourceModule struct {
	members AcademicUnitMemberApplication
}

func academicUnitMemberResource(members AcademicUnitMemberApplication) resource {
	module := academicUnitMemberResourceModule{members: members}
	return newResource(
		"academic-unit-members",
		principalRoute(
			http.MethodGet,
			apiPath(literal("academic-units"), canonicalID("academic_unit_id"), literal("members")),
			academicRelationshipReadErrorCodes(),
			module.list,
		),
		principalRoute(
			http.MethodPost,
			apiPath(literal("academic-units"), canonicalID("academic_unit_id"), literal("members")),
			academicRelationshipMutationErrorCodes("academic_unit_member.invalid", "academic_unit_member.conflict"),
			module.create,
		),
		principalRoute(
			http.MethodDelete,
			apiPath(literal("academic-unit-members"), canonicalID("academic_unit_member_id")),
			academicRelationshipMutationErrorCodes("academic_unit_member.conflict"),
			module.end,
		),
	)
}

func (module academicUnitMemberResourceModule) list(request operationRequest) (operationResult, error) {
	unitID, err := request.params.RequireAcademicUnitId()
	if err != nil {
		return operationResult{}, err
	}
	activeAt, err := parseActiveAt(request.request)
	if err != nil {
		return operationResult{}, err
	}
	members, err := module.members.ListAcademicUnitMembers(request.context, request.invocation(), application.ListAcademicUnitMembersQuery{AcademicUnitID: unitID, ActiveAt: activeAt})
	if err != nil {
		return operationResult{}, err
	}
	return jsonResult(http.StatusOK, academicUnitMemberResponses(members)), nil
}

func (module academicUnitMemberResourceModule) create(request operationRequest) (operationResult, error) {
	unitID, err := request.params.RequireAcademicUnitId()
	if err != nil {
		return operationResult{}, err
	}
	var body createAcademicUnitMemberRequest
	if err := request.decodeJSON(&body, "createAcademicUnitMember"); err != nil {
		return operationResult{}, err
	}
	saved, err := module.members.CreateAcademicUnitMember(request.context, request.invocation(), application.CreateAcademicUnitMemberCommand{AcademicUnitID: unitID, UserID: body.UserID, StartAt: body.StartAt})
	if err != nil {
		return operationResult{}, err
	}
	return jsonResult(http.StatusCreated, academicUnitMemberResponseFromModel(saved)), nil
}

func (module academicUnitMemberResourceModule) end(request operationRequest) (operationResult, error) {
	id, err := request.params.RequireAcademicUnitMemberId()
	if err != nil {
		return operationResult{}, err
	}
	ended, err := module.members.EndAcademicUnitMember(request.context, request.invocation(), application.EndAcademicUnitMemberCommand{ID: id})
	if err != nil {
		return operationResult{}, err
	}
	return jsonResult(http.StatusOK, academicUnitMemberResponseFromModel(ended)), nil
}

func academicUnitMemberResponseFromModel(member *model.AcademicUnitMember) academicUnitMemberResponse {
	if member == nil {
		return academicUnitMemberResponse{}
	}
	return academicUnitMemberResponse{
		ID:             member.ID.String(),
		CreateAt:       model.MillisFromTime(member.CreatedAt),
		UpdateAt:       model.MillisFromTime(member.UpdatedAt),
		DeleteAt:       member.ArchivedAt.Millis(),
		AcademicUnitID: member.AcademicUnitID.String(),
		UserID:         member.UserID.String(),
		StartAt:        model.MillisFromTime(member.StartsAt),
		EndAt:          member.EndsAt.Millis(),
	}
}

func academicUnitMemberResponses(members []*model.AcademicUnitMember) []academicUnitMemberResponse {
	result := make([]academicUnitMemberResponse, 0, len(members))
	for _, member := range members {
		result = append(result, academicUnitMemberResponseFromModel(member))
	}
	return result
}
