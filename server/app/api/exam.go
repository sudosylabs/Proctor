// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package api

import (
	"net/http"

	application "github.com/sudosylabs/proctor/server/app"
	"github.com/sudosylabs/proctor/server/model"
)

type createExamRequest struct {
	AcademicUnitID       string `json:"academic_unit_id"`
	Title                string `json:"title"`
	InstructionsMarkdown string `json:"instructions_markdown"`
}

type examResponse struct {
	Exam         examIdentityResponse `json:"exam"`
	Draft        examDraftResponse    `json:"draft"`
	OwnerUserID  string               `json:"owner_user_id"`
	ManagerCount int                  `json:"manager_count"`
}

type examIdentityResponse struct {
	ID                string `json:"id"`
	AcademicUnitID    string `json:"academic_unit_id"`
	CreatorUserID     string `json:"creator_user_id"`
	OwnerUserID       string `json:"owner_user_id"`
	DefaultRevisionID string `json:"default_revision_id,omitempty"`
	CreateAt          int64  `json:"create_at"`
	UpdateAt          int64  `json:"update_at"`
	DeleteAt          int64  `json:"delete_at"`
	Revision          int64  `json:"revision"`
}

type examDraftResponse struct {
	ExamID               string             `json:"exam_id"`
	Title                string             `json:"title"`
	InstructionsMarkdown string             `json:"instructions_markdown"`
	Policy               examPolicyResponse `json:"policy"`
	BaseRevisionID       string             `json:"base_revision_id,omitempty"`
	UpdateAt             int64              `json:"update_at"`
	Revision             int64              `json:"revision"`
	ResourceCount        int                `json:"resource_count"`
	HasStarterWorkspace  bool               `json:"has_starter_workspace"`
}

type examPolicyResponse struct {
	SchemaVersion  int                          `json:"schema_version"`
	ConnectionLoss examConnectionPolicyResponse `json:"connection_loss"`
	FocusLoss      examFocusPolicyResponse      `json:"focus_loss"`
}

type examConnectionPolicyResponse struct {
	Outcome string `json:"outcome"`
}

type examFocusPolicyResponse struct {
	Enabled                     bool   `json:"enabled"`
	MinimumDurationMilliseconds int64  `json:"minimum_duration_milliseconds"`
	IncidentCount               int    `json:"incident_count"`
	WindowMilliseconds          int64  `json:"window_milliseconds"`
	Outcome                     string `json:"outcome"`
}

type examResourceModule struct{ exams ExamApplication }

func examResource(exams ExamApplication) resource {
	module := examResourceModule{exams: exams}
	collection := apiPath(literal("exams"))
	member := apiPath(literal("exams"), canonicalID("exam_id"))
	return newResource(
		"exams",
		idempotentPrincipalRoute(IdempotencyOptional, http.MethodPost, collection, academicMutationErrorCodes(
			"request.invalid", "resource.not_found", "exam.invalid", "exam.conflict", "exam.unavailable",
			"idempotency.invalid_key", "idempotency.conflict", "idempotency.in_progress",
		), module.create),
		principalRoute(http.MethodGet, member, academicReadErrorCodes("request.invalid", "resource.not_found", "exam.unavailable"), module.get),
	)
}

func (m examResourceModule) create(request operationRequest) (operationResult, error) {
	var body createExamRequest
	if err := request.decodeJSON(&body, "createExam"); err != nil {
		return operationResult{}, err
	}
	unitID, err := model.ParseAcademicUnitID(body.AcademicUnitID)
	if err != nil {
		return operationResult{}, invalidRequestError("academic_unit_id", err)
	}
	view, err := m.exams.CreateExam(request.context, request.invocation(), application.CreateExamCommand{
		AcademicUnitID: unitID, Title: body.Title, InstructionsMarkdown: body.InstructionsMarkdown,
		IdempotencyKey: request.idempotencyKey,
	})
	if err != nil {
		return operationResult{}, err
	}
	return jsonResult(http.StatusCreated, examResponseFromView(view)), nil
}

func (m examResourceModule) get(request operationRequest) (operationResult, error) {
	raw, err := request.params.RequireExamId()
	if err != nil {
		return operationResult{}, err
	}
	examID, err := model.ParseExamID(raw)
	if err != nil {
		return operationResult{}, invalidRequestError("exam_id", err)
	}
	view, err := m.exams.GetExam(request.context, request.invocation(), application.GetExamQuery{ExamID: examID})
	if err != nil {
		return operationResult{}, err
	}
	return jsonResult(http.StatusOK, examResponseFromView(view)), nil
}

func examResponseFromView(view application.ExamView) examResponse {
	policy := view.Draft.Policy
	return examResponse{
		Exam: examIdentityResponse{
			ID: view.Exam.ID.String(), AcademicUnitID: view.Exam.AcademicUnitID.String(),
			CreatorUserID: view.Exam.CreatorUserID.String(), OwnerUserID: view.Exam.OwnerUserID.String(),
			DefaultRevisionID: view.Exam.DefaultRevisionID.String(), CreateAt: model.MillisFromTime(view.Exam.CreatedAt),
			UpdateAt: model.MillisFromTime(view.Exam.UpdatedAt), DeleteAt: view.Exam.ArchivedAt.Millis(), Revision: view.Exam.Revision,
		},
		Draft: examDraftResponse{
			ExamID: view.Draft.ExamID.String(), Title: view.Draft.Title,
			InstructionsMarkdown: view.Draft.InstructionsMarkdown,
			Policy: examPolicyResponse{
				SchemaVersion:  policy.SchemaVersion,
				ConnectionLoss: examConnectionPolicyResponse{Outcome: string(policy.ConnectionLoss.Outcome)},
				FocusLoss: examFocusPolicyResponse{Enabled: policy.FocusLoss.Enabled,
					MinimumDurationMilliseconds: policy.FocusLoss.MinimumDuration.Milliseconds(), IncidentCount: policy.FocusLoss.IncidentCount,
					WindowMilliseconds: policy.FocusLoss.Window.Milliseconds(), Outcome: string(policy.FocusLoss.Outcome)},
			},
			BaseRevisionID: view.Draft.BaseRevisionID.String(), UpdateAt: model.MillisFromTime(view.Draft.UpdatedAt),
			Revision: view.Draft.Revision, ResourceCount: view.ResourceCount, HasStarterWorkspace: view.HasStarterWorkspace,
		},
		OwnerUserID: view.OwnerUserID.String(), ManagerCount: view.ManagerCount,
	}
}
