// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package httpapi

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"time"

	application "github.com/sudosylabs/proctor/server/app"
	"github.com/sudosylabs/proctor/server/model"
)

type createExamRequest struct {
	AcademicUnitID       string `json:"academic_unit_id"`
	Title                string `json:"title"`
	InstructionsMarkdown string `json:"instructions_markdown"`
}

type editExamDraftTextRequest struct {
	ExpectedDraftRevision int64            `json:"expected_draft_revision"`
	Title                 Optional[string] `json:"title"`
	InstructionsMarkdown  Optional[string] `json:"instructions_markdown"`
}

type configureExamDraftFocusLossRequest struct {
	ExpectedDraftRevision       int64  `json:"expected_draft_revision"`
	Enabled                     bool   `json:"enabled"`
	MinimumDurationMilliseconds int64  `json:"minimum_duration_milliseconds"`
	IncidentCount               int    `json:"incident_count"`
	WindowMilliseconds          int64  `json:"window_milliseconds"`
	Outcome                     string `json:"outcome"`
}

type configureExamDraftExecutionProfileRequest struct {
	ExpectedDraftRevision int64  `json:"expected_draft_revision"`
	Enabled               bool   `json:"enabled"`
	Image                 string `json:"image"`
	Network               string `json:"network"`
}

func (r *configureExamDraftExecutionProfileRequest) UnmarshalJSON(data []byte) error {
	if err := rejectDuplicateJSONObjectMembers(data, "execution profile"); err != nil {
		return err
	}
	type wire configureExamDraftExecutionProfileRequest
	var decoded wire
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&decoded); err != nil {
		return err
	}
	var members map[string]json.RawMessage
	if err := json.Unmarshal(data, &members); err != nil {
		return err
	}
	enabled, ok := members["enabled"]
	if !ok || bytes.Equal(bytes.TrimSpace(enabled), []byte("null")) {
		return errors.New("enabled must be provided and non-null")
	}
	*r = configureExamDraftExecutionProfileRequest(decoded)
	return nil
}

type archiveExamRequest struct {
	ExpectedExamRevision int64 `json:"expected_exam_revision"`
}

type addExamManagerRequest struct {
	UserID               string `json:"user_id"`
	ExpectedExamRevision int64  `json:"expected_exam_revision"`
}

type removeExamManagerRequest struct {
	ExpectedExamRevision int64 `json:"expected_exam_revision"`
}

type transferExamOwnershipRequest = addExamManagerRequest

const examCatalogCursorVersion = 1

type examCatalogCursor struct {
	Version   int    `json:"version"`
	UpdatedAt string `json:"updated_at"`
	ExamID    string `json:"exam_id"`
}

type examSummaryResponse struct {
	ID             string  `json:"id"`
	AcademicUnitID string  `json:"academic_unit_id"`
	CreatorUserID  string  `json:"creator_user_id"`
	OwnerUserID    string  `json:"owner_user_id"`
	Title          string  `json:"title"`
	UpdatedAt      string  `json:"updated_at"`
	ArchivedAt     *string `json:"archived_at"`
	Revision       int64   `json:"revision"`
	ManagerCount   int     `json:"manager_count"`
}

type examListResponse struct {
	Items      []examSummaryResponse `json:"items"`
	NextCursor string                `json:"next_cursor,omitempty"`
}

type examManagerResponse struct {
	UserID          string `json:"user_id"`
	GrantedByUserID string `json:"granted_by_user_id"`
	GrantedAt       string `json:"granted_at"`
	IsCreator       bool   `json:"is_creator"`
	IsOwner         bool   `json:"is_owner"`
}

type examManagerListResponse struct {
	Items      []examManagerResponse `json:"items"`
	NextCursor string                `json:"next_cursor,omitempty"`
}

type examManagerChangeResponse struct {
	Exam    examIdentityResponse `json:"exam"`
	Manager examManagerResponse  `json:"manager"`
}

type examManagerCursor struct {
	Version   int    `json:"version"`
	GrantedAt string `json:"granted_at"`
	UserID    string `json:"user_id"`
}

func (r *configureExamDraftFocusLossRequest) UnmarshalJSON(data []byte) error {
	if err := rejectDuplicateExamPolicyRequestMembers(data); err != nil {
		return err
	}
	type wire configureExamDraftFocusLossRequest
	var decoded wire
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&decoded); err != nil {
		return err
	}
	var members map[string]json.RawMessage
	if err := json.Unmarshal(data, &members); err != nil {
		return err
	}
	enabled, ok := members["enabled"]
	if !ok || bytes.Equal(bytes.TrimSpace(enabled), []byte("null")) {
		return errors.New("enabled must be provided and non-null")
	}
	*r = configureExamDraftFocusLossRequest(decoded)
	return nil
}

func rejectDuplicateExamPolicyRequestMembers(data []byte) error {
	return rejectDuplicateJSONObjectMembers(data, "focus loss policy")
}

func rejectDuplicateJSONObjectMembers(data []byte, label string) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	token, err := decoder.Token()
	if err != nil || token != json.Delim('{') {
		return errors.New(label + " must be a JSON object")
	}
	seen := make(map[string]struct{})
	for decoder.More() {
		keyToken, err := decoder.Token()
		if err != nil {
			return err
		}
		key, ok := keyToken.(string)
		if !ok {
			return errors.New(label + " member is invalid")
		}
		if _, exists := seen[key]; exists {
			return errors.New(label + " contains a duplicate member")
		}
		seen[key] = struct{}{}
		var value json.RawMessage
		if err := decoder.Decode(&value); err != nil {
			return err
		}
	}
	_, err = decoder.Token()
	return err
}

type examResponse struct {
	Exam         examIdentityResponse `json:"exam"`
	Draft        examDraftResponse    `json:"draft"`
	OwnerUserID  string               `json:"owner_user_id"`
	ManagerCount int                  `json:"manager_count"`
}

type examIdentityResponse struct {
	ID                string  `json:"id"`
	AcademicUnitID    string  `json:"academic_unit_id"`
	CreatorUserID     string  `json:"creator_user_id"`
	OwnerUserID       string  `json:"owner_user_id"`
	DefaultRevisionID string  `json:"default_revision_id,omitempty"`
	CreatedAt         string  `json:"created_at"`
	UpdatedAt         string  `json:"updated_at"`
	ArchivedAt        *string `json:"archived_at"`
	Revision          int64   `json:"revision"`
}

type examDraftResponse struct {
	ExamID               string                   `json:"exam_id"`
	Title                string                   `json:"title"`
	InstructionsMarkdown string                   `json:"instructions_markdown"`
	Policy               examPolicyResponse       `json:"policy"`
	ExecutionProfile     executionProfileResponse `json:"execution_profile"`
	BaseRevisionID       string                   `json:"base_revision_id,omitempty"`
	UpdatedAt            string                   `json:"updated_at"`
	Revision             int64                    `json:"revision"`
	ResourceCount        int                      `json:"resource_count"`
	HasStarterWorkspace  bool                     `json:"has_starter_workspace"`
}

type executionProfileResponse struct {
	Enabled bool   `json:"enabled"`
	Image   string `json:"image"`
	Network string `json:"network"`
}

type executionImageResponse struct {
	ID       string   `json:"id"`
	Networks []string `json:"networks"`
}

type executionImageListResponse struct {
	Items []executionImageResponse `json:"items"`
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
	draft := apiPath(literal("exams"), canonicalID("exam_id"), literal("draft"))
	focusLossPolicy := apiPath(literal("exams"), canonicalID("exam_id"), literal("draft"), literal("policies"), literal("focus-loss"))
	executionProfile := apiPath(literal("exams"), canonicalID("exam_id"), literal("draft"), literal("execution-profile"))
	executionImages := apiPath(literal("exams"), canonicalID("exam_id"), literal("draft"), literal("execution-images"))
	archive := apiPath(literal("exams"), canonicalID("exam_id"), literal("archive"))
	managers := apiPath(literal("exams"), canonicalID("exam_id"), literal("managers"))
	manager := apiPath(literal("exams"), canonicalID("exam_id"), literal("managers"), canonicalID("user_id"))
	owner := apiPath(literal("exams"), canonicalID("exam_id"), literal("owner"))
	return newResource(
		"exams",
		principalRoute(http.MethodGet, collection, academicReadErrorCodes("request.invalid", "exam.invalid", "exam.unavailable"), module.list),
		idempotentPrincipalRoute(IdempotencyRequired, http.MethodPost, collection, academicMutationErrorCodes(
			"request.invalid", "resource.not_found", "exam.invalid", "exam.conflict", "exam.unavailable",
			"idempotency.key_required", "idempotency.invalid_key", "idempotency.conflict", "idempotency.in_progress",
		), module.create),
		principalRoute(http.MethodGet, member, academicReadErrorCodes("request.invalid", "resource.not_found", "exam.unavailable"), module.get),
		idempotentPrincipalRoute(IdempotencyRequired, http.MethodPatch, draft, academicMutationErrorCodes(
			"request.invalid", "resource.not_found", "exam.invalid", "exam.archived",
			"exam.draft.revision_conflict", "exam.draft.no_changes", "exam.unavailable",
			"idempotency.key_required", "idempotency.invalid_key", "idempotency.conflict", "idempotency.in_progress",
		), module.editDraftText),
		idempotentPrincipalRoute(IdempotencyRequired, http.MethodPut, focusLossPolicy, academicMutationErrorCodes(
			"request.invalid", "resource.not_found", "exam.invalid", "exam.archived",
			"exam.draft.revision_conflict", "exam.draft.no_changes", "exam.unavailable",
			"idempotency.key_required", "idempotency.invalid_key", "idempotency.conflict", "idempotency.in_progress",
		), module.configureDraftFocusLoss),
		idempotentPrincipalRoute(IdempotencyRequired, http.MethodPut, executionProfile, academicMutationErrorCodes(
			"request.invalid", "resource.not_found", "exam.invalid", "exam.archived",
			"exam.draft.revision_conflict", "exam.draft.no_changes", "exam.unavailable",
			"idempotency.key_required", "idempotency.invalid_key", "idempotency.conflict", "idempotency.in_progress",
		), module.configureDraftExecutionProfile),
		principalRoute(http.MethodGet, executionImages, academicReadErrorCodes("request.invalid", "resource.not_found", "exam.unavailable"), module.listExecutionImages),
		idempotentPrincipalRoute(IdempotencyRequired, http.MethodPost, archive, academicMutationErrorCodes(
			"request.invalid", "resource.not_found", "exam.invalid", "exam.archived", "exam.revision_conflict", "exam.unavailable",
			"idempotency.key_required", "idempotency.invalid_key", "idempotency.conflict", "idempotency.in_progress",
		), module.archive),
		principalRoute(http.MethodGet, managers, academicReadErrorCodes("request.invalid", "resource.not_found", "exam.invalid", "exam.unavailable"), module.listManagers),
		idempotentPrincipalRoute(IdempotencyRequired, http.MethodPost, managers, academicMutationErrorCodes(
			"request.invalid", "resource.not_found", "exam.invalid", "exam.archived", "exam.revision_conflict",
			"exam.manager.exists", "exam.manager.ineligible", "exam.unavailable",
			"idempotency.key_required", "idempotency.invalid_key", "idempotency.conflict", "idempotency.in_progress",
		), module.addManager),
		idempotentPrincipalRoute(IdempotencyRequired, http.MethodDelete, manager, academicMutationErrorCodes(
			"request.invalid", "resource.not_found", "exam.invalid", "exam.archived", "exam.revision_conflict",
			"exam.manager.not_found", "exam.manager.owner_protected", "exam.unavailable",
			"idempotency.key_required", "idempotency.invalid_key", "idempotency.conflict", "idempotency.in_progress",
		), module.removeManager),
		idempotentPrincipalRoute(IdempotencyRequired, http.MethodPut, owner, academicMutationErrorCodes(
			"request.invalid", "resource.not_found", "exam.invalid", "exam.archived", "exam.revision_conflict",
			"exam.manager.not_found", "exam.manager.ineligible", "exam.owner.no_changes", "exam.unavailable",
			"idempotency.key_required", "idempotency.invalid_key", "idempotency.conflict", "idempotency.in_progress",
		), module.transferOwner),
	)
}

func (m examResourceModule) list(request operationRequest) (operationResult, error) {
	query, err := examListQuery(request.request)
	if err != nil {
		return operationResult{}, invalidRequestError("exam_list", err)
	}
	page, err := m.exams.ListExams(request.context, request.invocation(), query)
	if err != nil {
		return operationResult{}, err
	}
	response := examListResponse{Items: make([]examSummaryResponse, 0, len(page.Items))}
	for _, item := range page.Items {
		response.Items = append(response.Items, examSummaryResponseFromApplication(item))
	}
	if len(page.Items) == query.Limit {
		last := page.Items[len(page.Items)-1]
		response.NextCursor = encodeExamCatalogCursor(examCatalogCursor{UpdatedAt: model.TimeUTC(last.UpdatedAt).Format(time.RFC3339Nano), ExamID: last.ID.String()})
	}
	return jsonResult(http.StatusOK, response), nil
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

func (m examResourceModule) editDraftText(request operationRequest) (operationResult, error) {
	raw, err := request.params.RequireExamId()
	if err != nil {
		return operationResult{}, err
	}
	examID, err := model.ParseExamID(raw)
	if err != nil {
		return operationResult{}, invalidRequestError("exam_id", err)
	}
	var body editExamDraftTextRequest
	if err := request.decodeJSON(&body, "editExamDraftText"); err != nil {
		return operationResult{}, err
	}
	title := body.Title.ValuePointer()
	instructions := body.InstructionsMarkdown.ValuePointer()
	if title == nil && instructions == nil {
		return operationResult{}, invalidRequestError("fields", errors.New("at least one authored field is required"))
	}
	if body.ExpectedDraftRevision < 1 {
		return operationResult{}, invalidRequestError("expected_draft_revision", errors.New("must be positive"))
	}
	view, err := m.exams.EditExamDraftText(request.context, request.invocation(), application.EditExamDraftTextCommand{
		ExamID: examID, ExpectedDraftRevision: body.ExpectedDraftRevision,
		Title: title, InstructionsMarkdown: instructions, IdempotencyKey: request.idempotencyKey,
	})
	if err != nil {
		return operationResult{}, err
	}
	return jsonResult(http.StatusOK, examResponseFromView(view)), nil
}

func (m examResourceModule) configureDraftFocusLoss(request operationRequest) (operationResult, error) {
	raw, err := request.params.RequireExamId()
	if err != nil {
		return operationResult{}, err
	}
	examID, err := model.ParseExamID(raw)
	if err != nil {
		return operationResult{}, invalidRequestError("exam_id", err)
	}
	var body configureExamDraftFocusLossRequest
	if err := request.decodeJSON(&body, "configureExamDraftFocusLoss"); err != nil {
		return operationResult{}, err
	}
	if body.ExpectedDraftRevision < 1 {
		return operationResult{}, invalidRequestError("expected_draft_revision", errors.New("must be positive"))
	}
	if body.MinimumDurationMilliseconds < 500 || body.MinimumDurationMilliseconds > 300_000 {
		return operationResult{}, invalidRequestError("minimum_duration_milliseconds", errors.New("must be between 500 and 300000"))
	}
	if body.IncidentCount < 1 || body.IncidentCount > 100 {
		return operationResult{}, invalidRequestError("incident_count", errors.New("must be between 1 and 100"))
	}
	if body.WindowMilliseconds < 10_000 || body.WindowMilliseconds > 14_400_000 || body.WindowMilliseconds < body.MinimumDurationMilliseconds {
		return operationResult{}, invalidRequestError("window_milliseconds", errors.New("must be between 10000 and 14400000 and at least minimum_duration_milliseconds"))
	}
	outcome := model.IntegrityThresholdOutcome(body.Outcome)
	switch outcome {
	case model.IntegrityOutcomeFlag, model.IntegrityOutcomeFlagAndWarn, model.IntegrityOutcomeFlagAndSuspend:
	default:
		return operationResult{}, invalidRequestError("outcome", errors.New("is not supported"))
	}
	view, err := m.exams.ConfigureExamDraftFocusLoss(request.context, request.invocation(), application.ConfigureExamDraftFocusLossCommand{
		ExamID: examID, ExpectedDraftRevision: body.ExpectedDraftRevision, Enabled: body.Enabled,
		MinimumDuration: time.Duration(body.MinimumDurationMilliseconds) * time.Millisecond,
		IncidentCount:   body.IncidentCount, Window: time.Duration(body.WindowMilliseconds) * time.Millisecond,
		Outcome: outcome, IdempotencyKey: request.idempotencyKey,
	})
	if err != nil {
		return operationResult{}, err
	}
	return jsonResult(http.StatusOK, examResponseFromView(view)), nil
}

func (m examResourceModule) configureDraftExecutionProfile(request operationRequest) (operationResult, error) {
	raw, err := request.params.RequireExamId()
	if err != nil {
		return operationResult{}, err
	}
	examID, err := model.ParseExamID(raw)
	if err != nil {
		return operationResult{}, invalidRequestError("exam_id", err)
	}
	var body configureExamDraftExecutionProfileRequest
	if err := request.decodeJSON(&body, "configureExamDraftExecutionProfile"); err != nil {
		return operationResult{}, err
	}
	if body.ExpectedDraftRevision < 1 {
		return operationResult{}, invalidRequestError("expected_draft_revision", errors.New("must be positive"))
	}
	profile := model.ExecutionProfile{Enabled: body.Enabled, Image: body.Image, Network: model.ExecutionNetwork(body.Network)}
	if err := profile.Validate(); err != nil {
		return operationResult{}, invalidRequestError("execution_profile", err)
	}
	view, err := m.exams.ConfigureExamDraftExecutionProfile(request.context, request.invocation(), application.ConfigureExamDraftExecutionProfileCommand{
		ExamID: examID, ExpectedDraftRevision: body.ExpectedDraftRevision, Enabled: body.Enabled,
		Image: body.Image, Network: model.ExecutionNetwork(body.Network), IdempotencyKey: request.idempotencyKey,
	})
	if err != nil {
		return operationResult{}, err
	}
	return jsonResult(http.StatusOK, examResponseFromView(view)), nil
}

func (m examResourceModule) listExecutionImages(request operationRequest) (operationResult, error) {
	raw, err := request.params.RequireExamId()
	if err != nil {
		return operationResult{}, err
	}
	examID, err := model.ParseExamID(raw)
	if err != nil {
		return operationResult{}, invalidRequestError("exam_id", err)
	}
	images, err := m.exams.ListExamExecutionImages(request.context, request.invocation(), application.GetExamQuery{ExamID: examID})
	if err != nil {
		return operationResult{}, err
	}
	response := executionImageListResponse{Items: make([]executionImageResponse, len(images))}
	for index, image := range images {
		response.Items[index] = executionImageResponse{ID: image.ID, Networks: make([]string, len(image.Networks))}
		for networkIndex, network := range image.Networks {
			response.Items[index].Networks[networkIndex] = string(network)
		}
	}
	return jsonResult(http.StatusOK, response), nil
}

func (m examResourceModule) archive(request operationRequest) (operationResult, error) {
	raw, err := request.params.RequireExamId()
	if err != nil {
		return operationResult{}, err
	}
	examID, err := model.ParseExamID(raw)
	if err != nil {
		return operationResult{}, invalidRequestError("exam_id", err)
	}
	var body archiveExamRequest
	if err := request.decodeJSON(&body, "archiveExam"); err != nil {
		return operationResult{}, err
	}
	if body.ExpectedExamRevision < 1 {
		return operationResult{}, invalidRequestError("expected_exam_revision", errors.New("must be positive"))
	}
	exam, err := m.exams.ArchiveExam(request.context, request.invocation(), application.ArchiveExamCommand{
		ExamID: examID, ExpectedExamRevision: body.ExpectedExamRevision, IdempotencyKey: request.idempotencyKey,
	})
	if err != nil {
		return operationResult{}, err
	}
	return jsonResult(http.StatusOK, examIdentityResponseFromModel(exam)), nil
}

func (m examResourceModule) listManagers(request operationRequest) (operationResult, error) {
	examID, err := examIDFromRequest(request)
	if err != nil {
		return operationResult{}, err
	}
	query := application.ListExamManagersQuery{ExamID: examID, Limit: 50}
	values := request.request.URL.Query()
	if raw := values.Get("limit"); raw != "" {
		query.Limit, err = strconv.Atoi(raw)
		if err != nil || query.Limit < 1 || query.Limit > 200 {
			return operationResult{}, invalidRequestError("limit", errors.New("must be between 1 and 200"))
		}
	}
	if raw := values.Get("cursor"); raw != "" {
		cursor, decodeErr := decodeExamManagerCursor(raw)
		if decodeErr != nil {
			return operationResult{}, invalidRequestError("cursor", decodeErr)
		}
		query.BeforeGrantedAt, _ = time.Parse(time.RFC3339Nano, cursor.GrantedAt)
		query.BeforeUserID = model.UserID(cursor.UserID)
	}
	page, err := m.exams.ListExamManagers(request.context, request.invocation(), query)
	if err != nil {
		return operationResult{}, err
	}
	response := examManagerListResponse{Items: make([]examManagerResponse, 0, len(page.Items))}
	for _, item := range page.Items {
		response.Items = append(response.Items, examManagerResponseFromSummary(item))
	}
	if len(page.Items) == query.Limit {
		last := page.Items[len(page.Items)-1].Manager
		response.NextCursor = encodeExamManagerCursor(examManagerCursor{GrantedAt: model.TimeUTC(last.GrantedAt).Format(time.RFC3339Nano), UserID: last.UserID.String()})
	}
	return jsonResult(http.StatusOK, response), nil
}

func (m examResourceModule) addManager(request operationRequest) (operationResult, error) {
	examID, err := examIDFromRequest(request)
	if err != nil {
		return operationResult{}, err
	}
	var body addExamManagerRequest
	if err := request.decodeJSON(&body, "addExamManager"); err != nil {
		return operationResult{}, err
	}
	userID, err := parseExamManagerRequest(body.UserID, body.ExpectedExamRevision)
	if err != nil {
		return operationResult{}, err
	}
	change, err := m.exams.AddExamManager(request.context, request.invocation(), application.AddExamManagerCommand{
		ExamID: examID, UserID: userID, ExpectedExamRevision: body.ExpectedExamRevision, IdempotencyKey: request.idempotencyKey,
	})
	if err != nil {
		return operationResult{}, err
	}
	return jsonResult(http.StatusCreated, examManagerChangeResponseFromApplication(change, true)), nil
}

func (m examResourceModule) removeManager(request operationRequest) (operationResult, error) {
	examID, err := examIDFromRequest(request)
	if err != nil {
		return operationResult{}, err
	}
	rawUserID, err := request.params.RequireUserId()
	if err != nil {
		return operationResult{}, err
	}
	userID, err := model.ParseUserID(rawUserID)
	if err != nil {
		return operationResult{}, invalidRequestError("user_id", err)
	}
	var body removeExamManagerRequest
	if err := request.decodeJSON(&body, "removeExamManager"); err != nil {
		return operationResult{}, err
	}
	if body.ExpectedExamRevision < 1 {
		return operationResult{}, invalidRequestError("expected_exam_revision", errors.New("must be positive"))
	}
	change, err := m.exams.RemoveExamManager(request.context, request.invocation(), application.RemoveExamManagerCommand{
		ExamID: examID, UserID: userID, ExpectedExamRevision: body.ExpectedExamRevision, IdempotencyKey: request.idempotencyKey,
	})
	if err != nil {
		return operationResult{}, err
	}
	return jsonResult(http.StatusOK, examManagerChangeResponseFromApplication(change, false)), nil
}

func (m examResourceModule) transferOwner(request operationRequest) (operationResult, error) {
	examID, err := examIDFromRequest(request)
	if err != nil {
		return operationResult{}, err
	}
	var body transferExamOwnershipRequest
	if err := request.decodeJSON(&body, "transferExamOwnership"); err != nil {
		return operationResult{}, err
	}
	userID, err := parseExamManagerRequest(body.UserID, body.ExpectedExamRevision)
	if err != nil {
		return operationResult{}, err
	}
	change, err := m.exams.TransferExamOwnership(request.context, request.invocation(), application.TransferExamOwnershipCommand{
		ExamID: examID, UserID: userID, ExpectedExamRevision: body.ExpectedExamRevision, IdempotencyKey: request.idempotencyKey,
	})
	if err != nil {
		return operationResult{}, err
	}
	return jsonResult(http.StatusOK, examManagerChangeResponseFromApplication(change, true)), nil
}

func examIDFromRequest(request operationRequest) (model.ExamID, error) {
	raw, err := request.params.RequireExamId()
	if err != nil {
		return "", err
	}
	examID, err := model.ParseExamID(raw)
	if err != nil {
		return "", invalidRequestError("exam_id", err)
	}
	return examID, nil
}

func parseExamManagerRequest(rawUserID string, revision int64) (model.UserID, error) {
	userID, err := model.ParseUserID(rawUserID)
	if err != nil {
		return "", invalidRequestError("user_id", err)
	}
	if revision < 1 {
		return "", invalidRequestError("expected_exam_revision", errors.New("must be positive"))
	}
	return userID, nil
}

func encodeExamManagerCursor(cursor examManagerCursor) string {
	cursor.Version = 1
	encoded, _ := json.Marshal(cursor)
	return base64.RawURLEncoding.EncodeToString(encoded)
}

func decodeExamManagerCursor(raw string) (examManagerCursor, error) {
	var cursor examManagerCursor
	decoded, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return cursor, errors.New("invalid Exam Manager cursor")
	}
	decoder := json.NewDecoder(bytes.NewReader(decoded))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&cursor); err != nil || cursor.Version != 1 || !model.UserID(cursor.UserID).IsValid() {
		return cursor, errors.New("invalid Exam Manager cursor")
	}
	if _, err := time.Parse(time.RFC3339Nano, cursor.GrantedAt); err != nil {
		return cursor, errors.New("invalid Exam Manager cursor")
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return cursor, errors.New("invalid Exam Manager cursor")
	}
	return cursor, nil
}

func examManagerResponseFromSummary(summary application.ExamManagerSummary) examManagerResponse {
	return examManagerResponse{UserID: summary.Manager.UserID.String(), GrantedByUserID: summary.Manager.GrantedByUserID.String(),
		GrantedAt: model.TimeUTC(summary.Manager.GrantedAt).Format(time.RFC3339Nano), IsCreator: summary.IsCreator, IsOwner: summary.IsOwner}
}

func examManagerChangeResponseFromApplication(change application.ExamManagerChange, present bool) examManagerChangeResponse {
	return examManagerChangeResponse{Exam: examIdentityResponseFromModel(*change.Exam), Manager: examManagerResponse{
		UserID: change.Manager.UserID.String(), GrantedByUserID: change.Manager.GrantedByUserID.String(),
		GrantedAt: model.TimeUTC(change.Manager.GrantedAt).Format(time.RFC3339Nano),
		IsCreator: change.Manager.UserID == change.Exam.CreatorUserID, IsOwner: present && change.Manager.UserID == change.Exam.OwnerUserID,
	}}
}

func examListQuery(request *http.Request) (application.ListExamsQuery, error) {
	query := application.ListExamsQuery{ArchiveFilter: application.ExamArchiveActive, Limit: 50}
	values := request.URL.Query()
	if raw := values.Get("academic_unit_id"); raw != "" {
		unitID, err := model.ParseAcademicUnitID(raw)
		if err != nil {
			return query, err
		}
		query.AcademicUnitID = unitID
	}
	if raw := values.Get("archive_state"); raw != "" {
		query.ArchiveFilter = application.ExamArchiveFilter(raw)
	}
	switch query.ArchiveFilter {
	case application.ExamArchiveActive, application.ExamArchiveArchived, application.ExamArchiveAll:
	default:
		return query, errors.New("invalid archive_state")
	}
	if raw := values.Get("limit"); raw != "" {
		limit, err := strconv.Atoi(raw)
		if err != nil || limit < 1 || limit > 200 {
			return query, errors.New("limit must be between 1 and 200")
		}
		query.Limit = limit
	}
	if raw := values.Get("cursor"); raw != "" {
		cursor, err := decodeExamCatalogCursor(raw)
		if err != nil {
			return query, err
		}
		query.BeforeUpdatedAt, err = time.Parse(time.RFC3339Nano, cursor.UpdatedAt)
		if err != nil {
			return query, errors.New("invalid Exam catalog cursor")
		}
		query.BeforeUpdatedAt = model.TimeUTC(query.BeforeUpdatedAt)
		query.BeforeExamID = model.ExamID(cursor.ExamID)
	}
	return query, nil
}

func encodeExamCatalogCursor(cursor examCatalogCursor) string {
	cursor.Version = examCatalogCursorVersion
	encoded, _ := json.Marshal(cursor)
	return base64.RawURLEncoding.EncodeToString(encoded)
}

func decodeExamCatalogCursor(raw string) (examCatalogCursor, error) {
	var cursor examCatalogCursor
	decoded, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return cursor, err
	}
	decoder := json.NewDecoder(bytes.NewReader(decoded))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&cursor); err != nil || cursor.Version != 0 && cursor.Version != examCatalogCursorVersion || cursor.UpdatedAt == "" || !model.ExamID(cursor.ExamID).IsValid() {
		return cursor, errors.New("invalid Exam catalog cursor")
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return cursor, errors.New("invalid Exam catalog cursor")
	}
	if _, err := time.Parse(time.RFC3339Nano, cursor.UpdatedAt); err != nil {
		return cursor, errors.New("invalid Exam catalog cursor")
	}
	return cursor, nil
}

func examSummaryResponseFromApplication(summary application.ExamSummary) examSummaryResponse {
	var archivedAt *string
	if summary.ArchivedAt.Valid {
		formatted := model.TimeUTC(summary.ArchivedAt.Time).Format(time.RFC3339Nano)
		archivedAt = &formatted
	}
	return examSummaryResponse{ID: summary.ID.String(), AcademicUnitID: summary.AcademicUnitID.String(),
		CreatorUserID: summary.CreatorUserID.String(), OwnerUserID: summary.OwnerUserID.String(), Title: summary.Title,
		UpdatedAt: model.TimeUTC(summary.UpdatedAt).Format(time.RFC3339Nano), ArchivedAt: archivedAt,
		Revision: summary.Revision, ManagerCount: summary.ManagerCount}
}

func examIdentityResponseFromModel(exam model.Exam) examIdentityResponse {
	var archivedAt *string
	if exam.ArchivedAt.Valid {
		formatted := model.TimeUTC(exam.ArchivedAt.Time).Format(time.RFC3339Nano)
		archivedAt = &formatted
	}
	return examIdentityResponse{ID: exam.ID.String(), AcademicUnitID: exam.AcademicUnitID.String(), CreatorUserID: exam.CreatorUserID.String(),
		OwnerUserID: exam.OwnerUserID.String(), DefaultRevisionID: exam.DefaultRevisionID.String(),
		CreatedAt: model.TimeUTC(exam.CreatedAt).Format(time.RFC3339Nano), UpdatedAt: model.TimeUTC(exam.UpdatedAt).Format(time.RFC3339Nano),
		ArchivedAt: archivedAt, Revision: exam.Revision}
}

func examResponseFromView(view application.ExamView) examResponse {
	policy := view.Draft.Policy
	identity := examIdentityResponseFromModel(view.Exam)
	return examResponse{
		Exam: identity,
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
			ExecutionProfile: executionProfileResponse{Enabled: view.Draft.ExecutionProfile.Enabled,
				Image: view.Draft.ExecutionProfile.Image, Network: string(view.Draft.ExecutionProfile.Network)},
			BaseRevisionID: view.Draft.BaseRevisionID.String(), UpdatedAt: model.TimeUTC(view.Draft.UpdatedAt).Format(time.RFC3339Nano),
			Revision: view.Draft.Revision, ResourceCount: view.ResourceCount, HasStarterWorkspace: view.HasStarterWorkspace,
		},
		OwnerUserID: view.OwnerUserID.String(), ManagerCount: view.ManagerCount,
	}
}
