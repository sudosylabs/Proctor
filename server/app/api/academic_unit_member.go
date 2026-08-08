// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package api

import (
	"net/http"

	"github.com/gorilla/mux"
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

func (a *API) registerAcademicUnitMemberRoutes() error {
	routes := []struct {
		base         *mux.Router
		path, method string
		handler      http.HandlerFunc
	}{
		{a.BaseRoutes.AcademicUnit, "/members", http.MethodGet, a.listAcademicUnitMembers},
		{a.BaseRoutes.AcademicUnit, "/members", http.MethodPost, a.createAcademicUnitMember},
		{a.BaseRoutes.AcademicUnitMember, "", http.MethodDelete, a.endAcademicUnitMember},
	}
	for _, route := range routes {
		if err := a.Register(route.base, route.path, route.method, a.APIPrincipalRequired(route.handler)); err != nil {
			return err
		}
	}
	return nil
}

func (a *API) listAcademicUnitMembers(w http.ResponseWriter, r *http.Request) {
	principal, unitID, ok := requiredResourceID(w, r, Params.RequireAcademicUnitId)
	if !ok {
		return
	}
	activeAt, ok := queryActiveAt(w, r)
	if !ok {
		return
	}
	members, err := a.academicUnitMembers.ListAcademicUnitMembers(r.Context(), application.NewInvocation(principal, RequestMetadata(r.Context())), application.ListAcademicUnitMembersQuery{AcademicUnitID: unitID, ActiveAt: activeAt})
	if err != nil {
		writeApplicationError(w, r, a.logger, err)
		return
	}
	writeJSON(w, http.StatusOK, academicUnitMemberResponses(members))
}

func (a *API) createAcademicUnitMember(w http.ResponseWriter, r *http.Request) {
	principal, unitID, ok := requiredResourceID(w, r, Params.RequireAcademicUnitId)
	if !ok {
		return
	}
	var body createAcademicUnitMemberRequest
	if !decodeJSON(w, r, &body, "createAcademicUnitMember") {
		return
	}
	saved, err := a.academicUnitMembers.CreateAcademicUnitMember(r.Context(), application.NewInvocation(principal, RequestMetadata(r.Context())), application.CreateAcademicUnitMemberCommand{AcademicUnitID: unitID, UserID: body.UserID, StartAt: body.StartAt})
	if err != nil {
		writeApplicationError(w, r, a.logger, err)
		return
	}
	writeJSON(w, http.StatusCreated, academicUnitMemberResponseFromModel(saved))
}

func (a *API) endAcademicUnitMember(w http.ResponseWriter, r *http.Request) {
	principal, id, ok := requiredResourceID(w, r, Params.RequireAcademicUnitMemberId)
	if !ok {
		return
	}
	ended, err := a.academicUnitMembers.EndAcademicUnitMember(r.Context(), application.NewInvocation(principal, RequestMetadata(r.Context())), application.EndAcademicUnitMemberCommand{ID: id})
	if err != nil {
		writeApplicationError(w, r, a.logger, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, academicUnitMemberResponseFromModel(ended))
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
