// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package api

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	application "github.com/sudosylabs/proctor/server/app"
	"github.com/sudosylabs/proctor/server/model"
)

const (
	candidateAttemptCredentialHeader = "X-Proctor-Attempt-Credential"
	candidateAttemptConnectionHeader = "X-Proctor-Attempt-Connection-ID"
	examAttemptManagerCursorVersion  = 1
	candidateWorkspaceCursorVersion  = 1
)

type candidateAttemptHeaderAccess struct {
	ConnectionID         model.AttemptConnectionID
	ContinuityCredential string
}

func candidateAttemptAccessHeaders(request *http.Request) (candidateAttemptHeaderAccess, error) {
	if request == nil {
		return candidateAttemptHeaderAccess{}, errors.New("candidate Attempt headers are required")
	}
	credentials := request.Header.Values(candidateAttemptCredentialHeader)
	connections := request.Header.Values(candidateAttemptConnectionHeader)
	if len(credentials) != 1 || len(connections) != 1 || !model.IsValidCredentialToken(credentials[0]) {
		return candidateAttemptHeaderAccess{}, errors.New("one canonical candidate Attempt credential and Connection ID are required")
	}
	connectionID, err := model.ParseAttemptConnectionID(connections[0])
	if err != nil {
		return candidateAttemptHeaderAccess{}, errors.New("one canonical candidate Attempt credential and Connection ID are required")
	}
	return candidateAttemptHeaderAccess{ConnectionID: connectionID, ContinuityCredential: credentials[0]}, nil
}

type examAttemptManagerCursor struct {
	CreatedAt time.Time
	ID        model.ExamAttemptID
}

type examAttemptManagerCursorWire struct {
	Version   int    `json:"version"`
	CreatedAt string `json:"created_at"`
	ID        string `json:"id"`
}

func encodeExamAttemptManagerCursor(cursor examAttemptManagerCursor) string {
	wire := examAttemptManagerCursorWire{Version: examAttemptManagerCursorVersion,
		CreatedAt: model.TimeUTC(cursor.CreatedAt).Format(time.RFC3339Nano), ID: cursor.ID.String()}
	encoded, _ := json.Marshal(wire)
	return base64.RawURLEncoding.EncodeToString(encoded)
}

func decodeExamAttemptManagerCursor(raw string) (examAttemptManagerCursor, error) {
	var wire examAttemptManagerCursorWire
	if err := decodeStrictAttemptCursor(raw, &wire); err != nil || wire.Version != examAttemptManagerCursorVersion {
		return examAttemptManagerCursor{}, errors.New("invalid Exam Attempt cursor")
	}
	createdAt, err := time.Parse(time.RFC3339Nano, wire.CreatedAt)
	if err != nil || createdAt.IsZero() {
		return examAttemptManagerCursor{}, errors.New("invalid Exam Attempt cursor")
	}
	id, err := model.ParseExamAttemptID(wire.ID)
	if err != nil {
		return examAttemptManagerCursor{}, errors.New("invalid Exam Attempt cursor")
	}
	return examAttemptManagerCursor{CreatedAt: model.TimeUTC(createdAt), ID: id}, nil
}

type candidateWorkspaceCursor struct {
	Path string
	ID   model.AttemptWorkspaceEntryID
}

type candidateWorkspaceCursorWire struct {
	Version int    `json:"version"`
	Path    string `json:"path"`
	ID      string `json:"id"`
}

func encodeCandidateWorkspaceCursor(cursor candidateWorkspaceCursor) string {
	wire := candidateWorkspaceCursorWire{Version: candidateWorkspaceCursorVersion, Path: cursor.Path, ID: cursor.ID.String()}
	encoded, _ := json.Marshal(wire)
	return base64.RawURLEncoding.EncodeToString(encoded)
}

func decodeCandidateWorkspaceCursor(raw string) (candidateWorkspaceCursor, error) {
	var wire candidateWorkspaceCursorWire
	if err := decodeStrictAttemptCursor(raw, &wire); err != nil || wire.Version != candidateWorkspaceCursorVersion {
		return candidateWorkspaceCursor{}, errors.New("invalid candidate Workspace cursor")
	}
	path, err := model.NormalizeStarterWorkspacePath(wire.Path)
	if err != nil || path != wire.Path {
		return candidateWorkspaceCursor{}, errors.New("invalid candidate Workspace cursor")
	}
	id, err := model.ParseAttemptWorkspaceEntryID(wire.ID)
	if err != nil {
		return candidateWorkspaceCursor{}, errors.New("invalid candidate Workspace cursor")
	}
	return candidateWorkspaceCursor{Path: path, ID: id}, nil
}

func decodeStrictAttemptCursor(raw string, target any) error {
	if raw == "" {
		return errors.New("cursor is required")
	}
	decoded, err := base64.RawURLEncoding.Strict().DecodeString(raw)
	if err != nil {
		return err
	}
	decoder := json.NewDecoder(bytes.NewReader(decoded))
	decoder.DisallowUnknownFields()
	if err = decoder.Decode(target); err != nil {
		return err
	}
	var trailing any
	if err = decoder.Decode(&trailing); err != io.EOF {
		return errors.New("cursor has trailing data")
	}
	return nil
}

type ExamAttemptApplication interface {
	GetExamAttempt(context.Context, application.Invocation, application.GetExamAttemptQuery) (application.ExamAttemptManagerView, error)
	ListExamAttempts(context.Context, application.Invocation, application.ListExamAttemptsQuery) (application.ExamAttemptManagerPage, error)
	GetCandidateExamPresentation(context.Context, application.Invocation, application.CandidateExamAttemptAccess) (application.CandidateExamPresentation, error)
	ListCandidateExamWorkspace(context.Context, application.Invocation, application.ListCandidateExamWorkspaceQuery) (application.CandidateExamWorkspacePage, error)
	OpenCandidateExamResource(context.Context, application.Invocation, application.OpenCandidateExamResourceQuery) (application.OpenedExamAttemptContent, error)
	OpenCandidateExamWorkspaceFile(context.Context, application.Invocation, application.OpenCandidateExamWorkspaceFileQuery) (application.OpenedExamAttemptContent, error)
	ReallowExamAttempt(context.Context, application.Invocation, application.ReallowExamAttemptCommand) (application.ExamAttemptReallowResult, error)
}

type examAttemptHTTPModule struct{ application ExamAttemptApplication }

type examAttemptManagerResponse struct {
	ID                  string                            `json:"id"`
	ExamID              string                            `json:"exam_id"`
	SittingID           string                            `json:"exam_sitting_id"`
	CandidateUserID     string                            `json:"candidate_user_id"`
	AdmissionRevisionID string                            `json:"admission_revision_id"`
	State               string                            `json:"state"`
	CreatedAt           string                            `json:"created_at"`
	UpdatedAt           string                            `json:"updated_at"`
	SubmittedAt         string                            `json:"submitted_at,omitempty"`
	Revision            int64                             `json:"revision"`
	Workspace           examAttemptWorkspaceResponse      `json:"workspace"`
	LatestParticipation *examAttemptParticipationResponse `json:"latest_participation,omitempty"`
	CurrentConnection   *examAttemptConnectionResponse    `json:"current_connection,omitempty"`
	ActiveSuspension    *examAttemptSuspensionResponse    `json:"active_suspension,omitempty"`
}

type examAttemptWorkspaceResponse struct {
	ID        string `json:"id"`
	Cursor    int64  `json:"cursor"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
}

type examAttemptParticipationResponse struct {
	ID              string `json:"id"`
	State           string `json:"state"`
	Generation      int64  `json:"generation"`
	RenewalSequence int64  `json:"renewal_sequence"`
	StartedAt       string `json:"started_at"`
	UpdatedAt       string `json:"updated_at"`
	LeaseExpiresAt  string `json:"lease_expires_at"`
	EndedAt         string `json:"ended_at,omitempty"`
	EndReason       string `json:"end_reason,omitempty"`
}

type examAttemptConnectionResponse struct {
	ID          string `json:"id"`
	State       string `json:"state"`
	OpenedAt    string `json:"opened_at"`
	ClosedAt    string `json:"closed_at,omitempty"`
	CloseReason string `json:"close_reason,omitempty"`
}

type examAttemptSuspensionResponse struct {
	ID                string `json:"id"`
	ParticipationID   string `json:"participation_id"`
	FlagID            string `json:"flag_id"`
	Generation        int64  `json:"generation"`
	State             string `json:"state"`
	Source            string `json:"source"`
	CandidateReason   string `json:"candidate_reason"`
	StartedAt         string `json:"started_at"`
	EndedAt           string `json:"ended_at,omitempty"`
	ReallowedByUserID string `json:"reallowed_by_user_id,omitempty"`
}

type examAttemptManagerListResponse struct {
	Items      []examAttemptManagerResponse `json:"items"`
	NextCursor string                       `json:"next_cursor,omitempty"`
}

type reallowExamAttemptRequest struct {
	SuspensionID            string `json:"suspension_id"`
	ExpectedAttemptRevision int64  `json:"expected_attempt_revision"`
	Reason                  string `json:"reason"`
}

type examAttemptReallowResponse struct {
	ExamAttemptID     string `json:"exam_attempt_id"`
	ExamSittingID     string `json:"exam_sitting_id"`
	State             string `json:"state"`
	AttemptRevision   int64  `json:"attempt_revision"`
	SuspensionID      string `json:"suspension_id"`
	SuspensionState   string `json:"suspension_state"`
	CandidateReason   string `json:"candidate_reason"`
	ReallowedByUserID string `json:"reallowed_by_user_id"`
}

type candidateExamPresentationResponse struct {
	AttemptID            string                          `json:"attempt_id"`
	SittingID            string                          `json:"exam_sitting_id"`
	AdmissionRevisionID  string                          `json:"admission_revision_id"`
	CurrentRevisionID    string                          `json:"current_revision_id"`
	Title                string                          `json:"title"`
	InstructionsMarkdown string                          `json:"instructions_markdown"`
	Resources            []candidateExamResourceResponse `json:"resources"`
}

type candidateExamResourceResponse struct {
	ID                  string `json:"id"`
	DisplayName         string `json:"display_name"`
	DescriptionMarkdown string `json:"description_markdown"`
	Position            int    `json:"position"`
	MediaType           string `json:"media_type"`
	Size                int64  `json:"size"`
	SHA256              string `json:"sha256"`
}

type candidateExamWorkspaceItemResponse struct {
	ID             string `json:"id"`
	Kind           string `json:"kind"`
	Path           string `json:"path"`
	ContentVersion string `json:"content_version,omitempty"`
	MediaType      string `json:"media_type,omitempty"`
	Size           *int64 `json:"size,omitempty"`
	SHA256         string `json:"sha256,omitempty"`
}

type candidateExamWorkspaceListResponse struct {
	Items      []candidateExamWorkspaceItemResponse `json:"items"`
	NextCursor string                               `json:"next_cursor,omitempty"`
}

func examAttemptResource(application ExamAttemptApplication) resource {
	module := examAttemptHTTPModule{application: application}
	managerCollection := apiPath(literal("exams"), canonicalID("exam_id"), literal("sittings"), canonicalID("exam_sitting_id"), literal("attempts"))
	managerMember := apiPath(literal("exams"), canonicalID("exam_id"), literal("sittings"), canonicalID("exam_sitting_id"), literal("attempts"), canonicalID("exam_attempt_id"))
	candidate := apiPath(literal("exam-attempts"), canonicalID("exam_attempt_id"))
	presentation := appendRoutePath(candidate, literal("presentation"))
	workspace := appendRoutePath(candidate, literal("workspace"))
	resourceContent := appendRoutePath(candidate, literal("resources"), canonicalID("exam_resource_id"), literal("content"))
	workspaceContent := appendRoutePath(candidate, literal("workspace"), literal("files"), canonicalID("attempt_workspace_entry_id"), literal("content"))
	reallow := appendRoutePath(managerMember, literal("reallow"))
	managerErrors := academicReadErrorCodes("request.invalid", "resource.not_found", "exam.attempt.invalid", "exam.attempt.unavailable")
	reallowErrors := academicMutationErrorCodes("request.invalid", "resource.not_found", "exam.attempt.invalid",
		"exam.attempt.revision_conflict", "exam.attempt.suspension_conflict", "exam.attempt.state_conflict",
		"exam.attempt.sitting_unavailable", "exam.attempt.conflict", "exam.attempt.unavailable",
		"idempotency.key_required", "idempotency.invalid_key", "idempotency.conflict", "idempotency.in_progress")
	candidateErrors := personalAccessTokenSessionCodes("request.invalid", "resource.not_found", "exam.attempt.invalid",
		"exam.attempt.sitting_unavailable", "exam.attempt.state_conflict", "exam.attempt.unavailable")
	return newResource("exam-attempts",
		principalRoute(http.MethodGet, managerCollection, managerErrors, module.listManaged),
		principalRoute(http.MethodGet, managerMember, managerErrors, module.getManaged),
		idempotentPrincipalRoute(IdempotencyRequired, http.MethodPost, reallow, reallowErrors, module.reallow),
		sessionRoute(http.MethodGet, presentation, candidateErrors, module.presentation),
		sessionRoute(http.MethodGet, workspace, candidateErrors, module.workspace),
		protocolRoute("candidate-exam-resource-content", RouteProtocolBinaryDownload, AuthSessionRequired, http.MethodGet, resourceContent, candidateErrors, module.openResource),
		protocolRoute("candidate-exam-workspace-content", RouteProtocolBinaryDownload, AuthSessionRequired, http.MethodGet, workspaceContent, candidateErrors, module.openWorkspaceFile),
	)
}

func (module examAttemptHTTPModule) reallow(request operationRequest) (operationResult, error) {
	examID, sittingID, attemptID, err := managedExamAttemptIDs(request)
	if err != nil {
		return operationResult{}, err
	}
	var body reallowExamAttemptRequest
	if err = request.decodeJSON(&body, "reallowExamAttempt"); err != nil {
		return operationResult{}, err
	}
	suspensionID, err := model.ParseAttemptSuspensionID(body.SuspensionID)
	if err != nil {
		return operationResult{}, invalidRequestError("suspension_id", err)
	}
	result, err := module.application.ReallowExamAttempt(request.context, request.invocation(), application.ReallowExamAttemptCommand{
		ExamID: examID, SittingID: sittingID, AttemptID: attemptID, SuspensionID: suspensionID,
		ExpectedAttemptRevision: body.ExpectedAttemptRevision, PrivateReason: body.Reason, IdempotencyKey: request.idempotencyKey,
	})
	if err != nil {
		return operationResult{}, err
	}
	response := examAttemptReallowResponse{ExamAttemptID: result.Attempt.ID.String(), ExamSittingID: result.SittingID.String(),
		State: string(result.Attempt.State), AttemptRevision: result.Attempt.Revision, SuspensionID: result.Suspension.ID.String(),
		SuspensionState: string(result.Suspension.State), CandidateReason: string(result.Suspension.CandidateReason),
		ReallowedByUserID: result.Suspension.ReallowedByUserID.String()}
	return jsonResult(http.StatusOK, response).withHeaders(noStoreHeaders()), nil
}

func (module examAttemptHTTPModule) getManaged(request operationRequest) (operationResult, error) {
	examID, sittingID, attemptID, err := managedExamAttemptIDs(request)
	if err != nil {
		return operationResult{}, err
	}
	view, err := module.application.GetExamAttempt(request.context, request.invocation(), application.GetExamAttemptQuery{
		ExamID: examID, SittingID: sittingID, AttemptID: attemptID,
	})
	if err != nil {
		return operationResult{}, err
	}
	response, err := examAttemptManagerResponseFromView(view)
	if err != nil {
		return operationResult{}, err
	}
	return jsonResult(http.StatusOK, response).withHeaders(noStoreHeaders()), nil
}

func (module examAttemptHTTPModule) listManaged(request operationRequest) (operationResult, error) {
	examID, sittingID, err := managedExamAttemptScope(request)
	if err != nil {
		return operationResult{}, err
	}
	query := application.ListExamAttemptsQuery{ExamID: examID, SittingID: sittingID, Limit: 50}
	values := request.request.URL.Query()
	if raw := values.Get("limit"); raw != "" {
		query.Limit, err = strconv.Atoi(raw)
		if err != nil || query.Limit < 1 || query.Limit > 200 {
			return operationResult{}, invalidRequestError("limit", errors.New("must be between 1 and 200"))
		}
	}
	seenStates := make(map[model.ExamAttemptState]struct{})
	for _, raw := range values["state"] {
		state := model.ExamAttemptState(raw)
		switch state {
		case model.ExamAttemptActive, model.ExamAttemptSuspended, model.ExamAttemptSubmitted:
		default:
			return operationResult{}, invalidRequestError("state", errors.New("is not supported"))
		}
		if _, duplicate := seenStates[state]; duplicate {
			return operationResult{}, invalidRequestError("state", errors.New("must not contain duplicates"))
		}
		seenStates[state] = struct{}{}
		query.States = append(query.States, state)
	}
	if raw := values.Get("cursor"); raw != "" {
		cursor, decodeErr := decodeExamAttemptManagerCursor(raw)
		if decodeErr != nil {
			return operationResult{}, invalidRequestError("cursor", decodeErr)
		}
		query.BeforeCreatedAt, query.BeforeAttemptID = cursor.CreatedAt, cursor.ID
	}
	page, err := module.application.ListExamAttempts(request.context, request.invocation(), query)
	if err != nil {
		return operationResult{}, err
	}
	response := examAttemptManagerListResponse{Items: make([]examAttemptManagerResponse, 0, len(page.Items))}
	for _, item := range page.Items {
		mapped, mapErr := examAttemptManagerResponseFromView(item)
		if mapErr != nil {
			return operationResult{}, mapErr
		}
		response.Items = append(response.Items, mapped)
	}
	if page.HasMore {
		if len(page.Items) == 0 || page.Items[len(page.Items)-1].Attempt == nil {
			return operationResult{}, errors.New("Exam Attempt application returned an invalid page")
		}
		last := page.Items[len(page.Items)-1].Attempt
		response.NextCursor = encodeExamAttemptManagerCursor(examAttemptManagerCursor{CreatedAt: last.CreatedAt, ID: last.ID})
	}
	return jsonResult(http.StatusOK, response).withHeaders(noStoreHeaders()), nil
}

func (module examAttemptHTTPModule) presentation(request operationRequest) (operationResult, error) {
	access, err := candidateAccess(request)
	if err != nil {
		return operationResult{}, err
	}
	view, err := module.application.GetCandidateExamPresentation(request.context, request.invocation(), access)
	if err != nil {
		return operationResult{}, err
	}
	response := candidateExamPresentationResponse{AttemptID: view.AttemptID.String(), SittingID: view.SittingID.String(),
		AdmissionRevisionID: view.AdmissionRevisionID.String(), CurrentRevisionID: view.CurrentRevisionID.String(), Title: view.Title,
		InstructionsMarkdown: view.InstructionsMarkdown, Resources: make([]candidateExamResourceResponse, 0, len(view.Resources))}
	for _, resource := range view.Resources {
		response.Resources = append(response.Resources, candidateExamResourceResponse{ID: resource.ResourceID.String(), DisplayName: resource.DisplayName,
			DescriptionMarkdown: resource.DescriptionMarkdown, Position: resource.Position, MediaType: string(resource.MediaType), Size: resource.SizeBytes, SHA256: resource.SHA256})
	}
	return jsonResult(http.StatusOK, response).withHeaders(noStoreHeaders()), nil
}

func (module examAttemptHTTPModule) workspace(request operationRequest) (operationResult, error) {
	access, err := candidateAccess(request)
	if err != nil {
		return operationResult{}, err
	}
	query := application.ListCandidateExamWorkspaceQuery{Access: access, Limit: 50}
	values := request.request.URL.Query()
	if raw := values.Get("limit"); raw != "" {
		query.Limit, err = strconv.Atoi(raw)
		if err != nil || query.Limit < 1 || query.Limit > 200 {
			return operationResult{}, invalidRequestError("limit", errors.New("must be between 1 and 200"))
		}
	}
	if raw := values.Get("cursor"); raw != "" {
		cursor, decodeErr := decodeCandidateWorkspaceCursor(raw)
		if decodeErr != nil {
			return operationResult{}, invalidRequestError("cursor", decodeErr)
		}
		query.AfterPath, query.AfterEntryID = cursor.Path, cursor.ID
	}
	page, err := module.application.ListCandidateExamWorkspace(request.context, request.invocation(), query)
	if err != nil {
		return operationResult{}, err
	}
	response := candidateExamWorkspaceListResponse{Items: make([]candidateExamWorkspaceItemResponse, 0, len(page.Items))}
	for _, item := range page.Items {
		mapped := candidateExamWorkspaceItemResponse{ID: item.EntryID.String(), Kind: string(item.Kind), Path: item.Path,
			ContentVersion: item.ContentVersion.String(), MediaType: item.MediaType, SHA256: item.SHA256}
		if item.Kind == model.StarterWorkspaceEntryFile {
			size := item.SizeBytes
			mapped.Size = &size
		}
		response.Items = append(response.Items, mapped)
	}
	if page.HasMore {
		if len(page.Items) == 0 {
			return operationResult{}, errors.New("Exam Attempt application returned an invalid Workspace page")
		}
		last := page.Items[len(page.Items)-1]
		response.NextCursor = encodeCandidateWorkspaceCursor(candidateWorkspaceCursor{Path: last.Path, ID: last.EntryID})
	}
	return jsonResult(http.StatusOK, response).withHeaders(noStoreHeaders()), nil
}

func (module examAttemptHTTPModule) openResource(request operationRequest) (protocolResult, error) {
	access, err := candidateAccess(request)
	if err != nil {
		return protocolResult{}, err
	}
	raw, err := request.params.RequireExamResourceId()
	if err != nil {
		return protocolResult{}, err
	}
	resourceID, err := model.ParseExamResourceID(raw)
	if err != nil {
		return protocolResult{}, invalidRequestError("exam_resource_id", err)
	}
	opened, err := module.application.OpenCandidateExamResource(request.context, request.invocation(), application.OpenCandidateExamResourceQuery{Access: access, ResourceID: resourceID})
	if err != nil {
		return protocolResult{}, err
	}
	return candidateAttemptContentResult(request, opened, `"`+opened.SHA256+`"`)
}

func (module examAttemptHTTPModule) openWorkspaceFile(request operationRequest) (protocolResult, error) {
	access, err := candidateAccess(request)
	if err != nil {
		return protocolResult{}, err
	}
	raw, err := request.params.RequireAttemptWorkspaceEntryId()
	if err != nil {
		return protocolResult{}, err
	}
	entryID, err := model.ParseAttemptWorkspaceEntryID(raw)
	if err != nil {
		return protocolResult{}, invalidRequestError("attempt_workspace_entry_id", err)
	}
	opened, err := module.application.OpenCandidateExamWorkspaceFile(request.context, request.invocation(), application.OpenCandidateExamWorkspaceFileQuery{Access: access, EntryID: entryID})
	if err != nil {
		return protocolResult{}, err
	}
	return candidateAttemptContentResult(request, opened, `"`+opened.ContentVersion.String()+`"`)
}

func candidateAttemptContentResult(request operationRequest, opened application.OpenedExamAttemptContent, etag string) (protocolResult, error) {
	if opened.Body == nil || opened.SizeBytes < 0 || strings.TrimSpace(opened.MediaType) == "" || etag == `""` {
		return protocolResult{}, errors.New("Exam Attempt application returned incomplete content")
	}
	headers := privateNoStoreHeaders()
	headers.Set("Content-Type", opened.MediaType)
	headers.Set("ETag", etag)
	if etagMatches(request.request.Header.Get("If-None-Match"), etag) {
		_ = opened.Body.Close()
		return notModifiedProtocolResult(opened.SizeBytes).withHeaders(headers), nil
	}
	return binaryDownloadProtocolResult(opened.Body, opened.SizeBytes).withHeaders(headers), nil
}

func candidateAccess(request operationRequest) (application.CandidateExamAttemptAccess, error) {
	raw, err := request.params.RequireExamAttemptId()
	if err != nil {
		return application.CandidateExamAttemptAccess{}, err
	}
	attemptID, err := model.ParseExamAttemptID(raw)
	if err != nil {
		return application.CandidateExamAttemptAccess{}, invalidRequestError("exam_attempt_id", err)
	}
	headers, err := candidateAttemptAccessHeaders(request.request)
	if err != nil {
		return application.CandidateExamAttemptAccess{}, invalidRequestError("candidate_attempt_headers", err)
	}
	return application.CandidateExamAttemptAccess{AttemptID: attemptID, ConnectionID: headers.ConnectionID,
		ContinuityCredential: headers.ContinuityCredential}, nil
}

func managedExamAttemptScope(request operationRequest) (model.ExamID, model.ExamSittingID, error) {
	rawExam, err := request.params.RequireExamId()
	if err != nil {
		return "", "", err
	}
	rawSitting, err := request.params.RequireExamSittingId()
	if err != nil {
		return "", "", err
	}
	examID, err := model.ParseExamID(rawExam)
	if err != nil {
		return "", "", invalidRequestError("exam_id", err)
	}
	sittingID, err := model.ParseExamSittingID(rawSitting)
	if err != nil {
		return "", "", invalidRequestError("exam_sitting_id", err)
	}
	return examID, sittingID, nil
}

func managedExamAttemptIDs(request operationRequest) (model.ExamID, model.ExamSittingID, model.ExamAttemptID, error) {
	examID, sittingID, err := managedExamAttemptScope(request)
	if err != nil {
		return "", "", "", err
	}
	raw, err := request.params.RequireExamAttemptId()
	if err != nil {
		return "", "", "", err
	}
	attemptID, err := model.ParseExamAttemptID(raw)
	if err != nil {
		return "", "", "", invalidRequestError("exam_attempt_id", err)
	}
	return examID, sittingID, attemptID, nil
}

func examAttemptManagerResponseFromView(view application.ExamAttemptManagerView) (examAttemptManagerResponse, error) {
	if view.Attempt == nil || view.Workspace == nil {
		return examAttemptManagerResponse{}, errors.New("Exam Attempt application returned an incomplete manager projection")
	}
	attempt, workspace := view.Attempt, view.Workspace
	response := examAttemptManagerResponse{ID: attempt.ID.String(), ExamID: attempt.ExamID.String(), SittingID: attempt.SittingID.String(),
		CandidateUserID: attempt.CandidateUserID.String(), AdmissionRevisionID: attempt.AdmissionRevisionID.String(), State: string(attempt.State),
		CreatedAt: model.TimeUTC(attempt.CreatedAt).Format(time.RFC3339Nano), UpdatedAt: model.TimeUTC(attempt.UpdatedAt).Format(time.RFC3339Nano),
		SubmittedAt: attempt.SubmittedAt.FormatRFC3339(), Revision: attempt.Revision,
		Workspace: examAttemptWorkspaceResponse{ID: workspace.ID.String(), Cursor: workspace.Cursor,
			CreatedAt: model.TimeUTC(workspace.CreatedAt).Format(time.RFC3339Nano), UpdatedAt: model.TimeUTC(workspace.UpdatedAt).Format(time.RFC3339Nano)}}
	if participation := view.LatestParticipation; participation != nil {
		response.LatestParticipation = &examAttemptParticipationResponse{ID: participation.ID.String(), State: string(participation.State),
			Generation: participation.Generation, RenewalSequence: participation.RenewalSequence,
			StartedAt: model.TimeUTC(participation.StartedAt).Format(time.RFC3339Nano), UpdatedAt: model.TimeUTC(participation.UpdatedAt).Format(time.RFC3339Nano),
			LeaseExpiresAt: model.TimeUTC(participation.LeaseExpiresAt).Format(time.RFC3339Nano), EndedAt: participation.EndedAt.FormatRFC3339(), EndReason: string(participation.EndReason)}
	}
	if connection := view.CurrentConnection; connection != nil {
		response.CurrentConnection = &examAttemptConnectionResponse{ID: connection.ID.String(), State: string(connection.State),
			OpenedAt: model.TimeUTC(connection.OpenedAt).Format(time.RFC3339Nano), ClosedAt: connection.ClosedAt.FormatRFC3339(), CloseReason: string(connection.CloseReason)}
	}
	if suspension := view.ActiveSuspension; suspension != nil {
		response.ActiveSuspension = &examAttemptSuspensionResponse{ID: suspension.ID.String(),
			ParticipationID: suspension.ParticipationID.String(), FlagID: suspension.FlagID.String(), Generation: suspension.Generation,
			State: string(suspension.State), Source: string(suspension.Source), CandidateReason: string(suspension.CandidateReason),
			StartedAt: model.TimeUTC(suspension.StartedAt).Format(time.RFC3339Nano), EndedAt: suspension.EndedAt.FormatRFC3339(),
			ReallowedByUserID: suspension.ReallowedByUserID.String()}
	}
	return response, nil
}

func privateNoStoreHeaders() http.Header { return http.Header{"Cache-Control": {"private, no-store"}} }
