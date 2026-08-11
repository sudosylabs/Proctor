// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package api

import (
	"net/http"

	"github.com/gorilla/mux"
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

func (a *API) registerClassMemberRoutes() error {
	routes := []struct {
		base         *mux.Router
		path, method string
		handler      http.HandlerFunc
	}{
		{a.BaseRoutes.Class, "/members", http.MethodGet, a.listClassMembers},
		{a.BaseRoutes.Class, "/members", http.MethodPost, a.enrollClassMember},
		{a.BaseRoutes.ClassMember, "", http.MethodDelete, a.endClassMember},
	}
	for _, route := range routes {
		if err := a.registerLegacyRoute(route.base, route.path, route.method, a.APIPrincipalRequired(route.handler)); err != nil {
			return err
		}
	}
	return nil
}

func (a *API) listClassMembers(w http.ResponseWriter, r *http.Request) {
	principal, classID, ok := requiredResourceID(w, r, Params.RequireClassId)
	if !ok {
		return
	}
	activeAt, ok := queryActiveAt(w, r)
	if !ok {
		return
	}
	members, err := a.classMembers.ListClassMembers(r.Context(), application.NewInvocation(principal, RequestMetadata(r.Context())), application.ListClassMembersQuery{ClassID: classID, ActiveAt: activeAt})
	if err != nil {
		writeApplicationError(w, r, a.logger, err)
		return
	}
	writeJSON(w, http.StatusOK, classMemberResponses(members))
}

func (a *API) enrollClassMember(w http.ResponseWriter, r *http.Request) {
	principal, classID, ok := requiredResourceID(w, r, Params.RequireClassId)
	if !ok {
		return
	}
	var body enrollClassMemberRequest
	if !decodeJSON(w, r, &body, "enrollClassMember") {
		return
	}
	enrollment, err := a.classMembers.EnrollClassMember(r.Context(), application.NewInvocation(principal, RequestMetadata(r.Context())), application.EnrollClassMemberCommand{ClassID: classID, UserID: body.UserID, StartAt: body.StartAt, EndAt: body.EndAt})
	if err != nil {
		writeApplicationError(w, r, a.logger, err)
		return
	}
	writeJSON(w, http.StatusCreated, classEnrollmentResponseFromModel(enrollment))
}

func (a *API) endClassMember(w http.ResponseWriter, r *http.Request) {
	principal, id, ok := requiredResourceID(w, r, Params.RequireClassMemberId)
	if !ok {
		return
	}
	ended, err := a.classMembers.EndClassMember(r.Context(), application.NewInvocation(principal, RequestMetadata(r.Context())), application.EndClassMemberCommand{ID: id})
	if err != nil {
		writeApplicationError(w, r, a.logger, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, classMemberResponseFromModel(ended))
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
