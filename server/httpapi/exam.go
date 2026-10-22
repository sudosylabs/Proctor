// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package httpapi

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/sudosylabs/proctor/server/app/exam"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

type createExamRequest struct {
	AcademicUnitID       string `json:"academic_unit_id"`
	Title                string `json:"title"`
	InstructionsMarkdown string `json:"instructions_markdown"`
}

type editExamDraftTextRequest struct {
	ExpectedDraftRevision int64            `json:"expected_revision"`
	Title                 Optional[string] `json:"title"`
	InstructionsMarkdown  Optional[string] `json:"instructions_markdown"`
}

type configureExamDraftNativePolicyRequest struct {
	ExpectedDraftRevision int64                      `json:"expected_revision"`
	Native                model.NativeSecurityPolicy `json:"native"`
}

type configureExamDraftFocusLossRequest struct {
	ExpectedDraftRevision       int64  `json:"expected_revision"`
	Enabled                     bool   `json:"enabled"`
	MinimumDurationMilliseconds int64  `json:"minimum_duration_milliseconds"`
	IncidentCount               int    `json:"incident_count"`
	WindowMilliseconds          int64  `json:"window_milliseconds"`
	Outcome                     string `json:"outcome"`
}

type configureExamDraftBrowserPolicyRequest struct {
	ExpectedDraftRevision int64                 `json:"expected_revision"`
	BrowserPolicy         browserPolicyDocument `json:"browser_policy"`
}

type browserPolicyDocument struct {
	Enabled     bool                        `json:"enabled"`
	StartRuleID string                      `json:"start_rule_id,omitempty"`
	Rules       []browserPolicyRuleDocument `json:"rules,omitempty"`
}

type browserPolicyRuleDocument struct {
	RuleID                   string `json:"rule_id"`
	Origin                   string `json:"origin"`
	PathPrefix               string `json:"path_prefix"`
	HostMatch                string `json:"host_match"`
	AllowRedirects           bool   `json:"allow_redirects"`
	BlockedNavigationOutcome string `json:"blocked_navigation_outcome"`
	InstitutionHTTPException bool   `json:"institution_http_exception"`
}

func (d *browserPolicyDocument) UnmarshalJSON(encoded []byte) error {
	if len(encoded) > model.BrowserPolicyMaximumBytes {
		return errors.New("browser policy exceeds its encoded limit")
	}
	if err := rejectDuplicateJSONObjectMembers(encoded, "browser policy"); err != nil {
		return err
	}
	type wire browserPolicyDocument
	var decoded wire
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&decoded); err != nil {
		return err
	}
	var members map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &members); err != nil {
		return err
	}
	if members["enabled"] == nil || bytes.Equal(bytes.TrimSpace(members["enabled"]), []byte("null")) {
		return errors.New("browser policy enabled is required")
	}

	if decoded.Enabled {
		if len(members) != 3 || members["start_rule_id"] == nil || members["rules"] == nil || decoded.Rules == nil {
			return errors.New("enabled browser policy requires exactly start_rule_id and rules")
		}
	} else if len(members) != 1 {
		return errors.New("disabled browser policy contains enabled fields")
	}
	*d = browserPolicyDocument(decoded)
	return nil
}

func (d *browserPolicyRuleDocument) UnmarshalJSON(encoded []byte) error {
	if err := rejectDuplicateJSONObjectMembers(encoded, "browser policy rule"); err != nil {
		return err
	}
	type wire browserPolicyRuleDocument
	var decoded wire
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&decoded); err != nil {
		return err
	}
	var members map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &members); err != nil {
		return err
	}
	for _, name := range []string{"rule_id", "origin", "path_prefix", "host_match", "allow_redirects", "blocked_navigation_outcome", "institution_http_exception"} {
		if members[name] == nil || bytes.Equal(bytes.TrimSpace(members[name]), []byte("null")) {
			return errors.New("browser policy rule fields are required")
		}
	}
	if len(members) != 7 {
		return errors.New("browser policy rule contains an unknown field")
	}
	*d = browserPolicyRuleDocument(decoded)
	return nil
}

func (d browserPolicyDocument) model() (model.BrowserPolicy, error) {
	rules := make([]model.BrowserPolicyRule, len(d.Rules))
	for index, rule := range d.Rules {
		rules[index] = model.BrowserPolicyRule{RuleID: rule.RuleID, Origin: rule.Origin, PathPrefix: rule.PathPrefix,
			HostMatch: model.BrowserPolicyHostMatch(rule.HostMatch), AllowRedirects: rule.AllowRedirects,
			BlockedNavigationOutcome: model.BrowserPolicyBlockedNavigationOutcome(rule.BlockedNavigationOutcome), InstitutionHTTPException: rule.InstitutionHTTPException}
	}
	if !d.Enabled {
		return model.DisabledBrowserPolicy(), nil
	}

	return model.NewBrowserPolicy(true, d.StartRuleID, rules)
}

func browserPolicyDocumentFromModel(policy model.BrowserPolicy) browserPolicyDocument {
	document := browserPolicyDocument{Enabled: policy.Enabled}
	if !policy.Enabled {
		return document
	}
	document.StartRuleID = policy.StartRuleID
	document.Rules = make([]browserPolicyRuleDocument, len(policy.Rules))
	for index, rule := range policy.Rules {
		document.Rules[index] = browserPolicyRuleDocument{RuleID: rule.RuleID, Origin: rule.Origin,
			PathPrefix: rule.PathPrefix, HostMatch: string(rule.HostMatch), AllowRedirects: rule.AllowRedirects,
			BlockedNavigationOutcome: string(rule.BlockedNavigationOutcome), InstitutionHTTPException: rule.InstitutionHTTPException}
	}
	return document
}

type archiveExamRequest struct {
	ExpectedExamRevision int64 `json:"expected_revision"`
}

type addExamManagerRequest struct {
	UserID               string `json:"user_id"`
	ExpectedExamRevision int64  `json:"expected_revision"`
}

type removeExamManagerRequest struct {
	ExpectedExamRevision int64 `json:"expected_revision"`
}

type transferExamOwnershipRequest = addExamManagerRequest

type examCatalogCursor struct {
	UpdatedAt string `json:"updated_at"`
	ExamID    string `json:"exam_id"`
}

type examSummaryResponse struct {
	ID                      string                      `json:"id"`
	AcademicUnitID          string                      `json:"academic_unit_id"`
	AcademicUnitDisplayName string                      `json:"academic_unit_display_name"`
	CreatorUserID           string                      `json:"creator_user_id"`
	OwnerUserID             string                      `json:"owner_user_id"`
	Title                   string                      `json:"title"`
	UpdatedAt               string                      `json:"updated_at"`
	ArchivedAt              *string                     `json:"archived_at,omitempty"`
	Revision                int64                       `json:"revision"`
	ManagerCount            int                         `json:"manager_count"`
	SittingCount            int                         `json:"sitting_count"`
	MatchingSittingCount    int                         `json:"matching_sitting_count"`
	SittingSummary          *examCatalogSittingResponse `json:"sitting_summary"`
}

type examCatalogSittingResponse struct {
	ID               string                 `json:"id"`
	ExamRevisionID   string                 `json:"exam_revision_id"`
	ClassID          string                 `json:"class_id"`
	ClassDisplayName string                 `json:"class_display_name"`
	ScheduledStartAt string                 `json:"scheduled_start_at"`
	ScheduledEndAt   string                 `json:"scheduled_end_at"`
	State            model.ExamSittingState `json:"state"`
	Revision         int64                  `json:"revision"`
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
	ArchivedAt        *string `json:"archived_at,omitempty"`
	Revision          int64   `json:"revision"`
}

type examDraftResponse struct {
	ExamID               string                     `json:"exam_id"`
	Title                string                     `json:"title"`
	InstructionsMarkdown string                     `json:"instructions_markdown"`
	Policy               examPolicyResponse         `json:"policy"`
	BrowserPolicy        browserPolicyDocument      `json:"browser_policy"`
	Capacity             examCapacityPolicyResponse `json:"capacity"`
	BaseRevisionID       string                     `json:"base_revision_id,omitempty"`
	UpdatedAt            string                     `json:"updated_at"`
	Revision             int64                      `json:"revision"`
	ResourceCount        int                        `json:"resource_count"`
	HasStarterWorkspace  bool                       `json:"has_starter_workspace"`
}

type examPolicyResponse struct {
	Native         model.NativeSecurityPolicy   `json:"native"`
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

func examResource(exams ExamApplication) apiResource {
	module := examResourceModule{exams: exams}
	collection := apiPath(literal("exams"))
	member := apiPath(literal("exams"), canonicalID("exam_id"))
	draft := apiPath(literal("exams"), canonicalID("exam_id"), literal("draft"))
	nativePolicy := apiPath(literal("exams"), canonicalID("exam_id"), literal("draft"), literal("policies"), literal("native"))
	focusLossPolicy := apiPath(literal("exams"), canonicalID("exam_id"), literal("draft"), literal("policies"), literal("focus-loss"))
	browserPolicy := apiPath(literal("exams"), canonicalID("exam_id"), literal("draft"), literal("browser-policy"))
	archive := apiPath(literal("exams"), canonicalID("exam_id"), literal("archive"))
	managers := apiPath(literal("exams"), canonicalID("exam_id"), literal("managers"))
	manager := apiPath(literal("exams"), canonicalID("exam_id"), literal("managers"), canonicalID("user_id"))
	owner := apiPath(literal("exams"), canonicalID("exam_id"), literal("owner"))
	return newResource(
		"exams",
		principalRoute(http.MethodGet, collection, academicReadErrorCodes("request.invalid", "exam.invalid", "exam.unavailable"), module.list),
		idempotentPrincipalRoute(IdempotencyRequired, http.MethodPost, collection, academicMutationErrorCodes(
			"request.invalid", "resource.not_found", "exam.invalid", "exam.conflict", "exam.unavailable",
			"idempotency.key_required", "idempotency.key_invalid", "idempotency.conflict", "idempotency.in_progress",
		), module.create),
		principalRoute(http.MethodGet, member, academicReadErrorCodes("request.invalid", "resource.not_found", "exam.unavailable"), module.get),
		idempotentPrincipalRoute(IdempotencyRequired, http.MethodPatch, draft, academicMutationErrorCodes(
			"request.invalid", "resource.not_found", "exam.invalid", "exam.archived",
			"exam.draft.revision_conflict", "exam.draft.no_changes", "exam.unavailable",
			"idempotency.key_required", "idempotency.key_invalid", "idempotency.conflict", "idempotency.in_progress",
		), module.editDraftText),
		idempotentPrincipalRoute(IdempotencyRequired, http.MethodPut, focusLossPolicy, academicMutationErrorCodes(
			"request.invalid", "resource.not_found", "exam.invalid", "exam.archived",
			"exam.draft.revision_conflict", "exam.draft.no_changes", "exam.unavailable",
			"idempotency.key_required", "idempotency.key_invalid", "idempotency.conflict", "idempotency.in_progress",
		), module.configureDraftFocusLoss),
		idempotentPrincipalRoute(IdempotencyRequired, http.MethodPut, nativePolicy, academicMutationErrorCodes(
			"request.invalid", "resource.not_found", "exam.invalid", "exam.archived",
			"exam.draft.revision_conflict", "exam.draft.no_changes", "exam.unavailable",
			"idempotency.key_required", "idempotency.key_invalid", "idempotency.conflict", "idempotency.in_progress",
		), module.configureDraftNativePolicy),
		idempotentPrincipalRoute(IdempotencyRequired, http.MethodPut, browserPolicy, academicMutationErrorCodes(
			"request.invalid", "resource.not_found", "exam.invalid", "exam.archived",
			"exam.draft.revision_conflict", "exam.draft.no_changes", "exam.unavailable",
			"idempotency.key_required", "idempotency.key_invalid", "idempotency.conflict", "idempotency.in_progress",
		), module.configureDraftBrowserPolicy),
		idempotentPrincipalRoute(IdempotencyRequired, http.MethodPost, archive, academicMutationErrorCodes(
			"request.invalid", "resource.not_found", "exam.invalid", "exam.archived", "exam.revision_conflict", "exam.unavailable",
			"idempotency.key_required", "idempotency.key_invalid", "idempotency.conflict", "idempotency.in_progress",
		), module.archive),
		principalRoute(http.MethodGet, managers, academicReadErrorCodes("request.invalid", "resource.not_found", "exam.invalid", "exam.unavailable"), module.listManagers),
		idempotentPrincipalRoute(IdempotencyRequired, http.MethodPost, managers, academicMutationErrorCodes(
			"request.invalid", "resource.not_found", "exam.invalid", "exam.archived", "exam.revision_conflict",
			"exam.manager.exists", "exam.manager.ineligible", "exam.manager.candidate_conflict", "exam.unavailable",
			"idempotency.key_required", "idempotency.key_invalid", "idempotency.conflict", "idempotency.in_progress",
		), module.addManager),
		idempotentPrincipalRoute(IdempotencyRequired, http.MethodDelete, manager, academicMutationErrorCodes(
			"request.invalid", "resource.not_found", "exam.invalid", "exam.archived", "exam.revision_conflict",
			"exam.manager.not_found", "exam.manager.owner_protected", "exam.unavailable",
			"idempotency.key_required", "idempotency.key_invalid", "idempotency.conflict", "idempotency.in_progress",
		), module.removeManager),
		idempotentPrincipalRoute(IdempotencyRequired, http.MethodPut, owner, academicMutationErrorCodes(
			"request.invalid", "resource.not_found", "exam.invalid", "exam.archived", "exam.revision_conflict",
			"exam.manager.not_found", "exam.manager.ineligible", "exam.manager.candidate_conflict", "exam.owner.no_changes", "exam.unavailable",
			"idempotency.key_required", "idempotency.key_invalid", "idempotency.conflict", "idempotency.in_progress",
		), module.transferOwner),
	)
}

func (m examResourceModule) list(request operationRequest) (operationResult, error) {
	query, err := examListQuery(request.request)
	if err != nil {
		return operationResult{}, invalidRequestError(err)
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
		response.NextCursor, err = encodeExamCatalogCursor(examCatalogCursor{UpdatedAt: model.FormatRFC3339(last.UpdatedAt), ExamID: last.ID.String()})
		if err != nil {
			return operationResult{}, unpresentableResult(err)
		}
	}
	return jsonResult(http.StatusOK, response), nil
}

func (m examResourceModule) create(request operationRequest) (operationResult, error) {
	var body createExamRequest
	if err := request.decodeJSON(&body); err != nil {
		return operationResult{}, err
	}
	unitID, err := model.ParseAcademicUnitID(body.AcademicUnitID)
	if err != nil {
		return operationResult{}, invalidRequestError(err)
	}
	view, err := m.exams.CreateExam(request.context, request.invocation(), exam.CreateCommand{
		AcademicUnitID: unitID, Title: body.Title, InstructionsMarkdown: body.InstructionsMarkdown,
		IdempotencyKey: request.idempotencyKey,
	})
	if err != nil {
		return operationResult{}, err
	}
	return jsonResult(http.StatusCreated, examResponseFromView(view)), nil
}

func (m examResourceModule) get(request operationRequest) (operationResult, error) {
	examID, err := request.params.RequireExamID()
	if err != nil {
		return operationResult{}, err
	}
	view, err := m.exams.GetExam(request.context, request.invocation(), exam.GetQuery{ExamID: examID})
	if err != nil {
		return operationResult{}, err
	}
	return jsonResult(http.StatusOK, examResponseFromView(view)), nil
}

func (m examResourceModule) editDraftText(request operationRequest) (operationResult, error) {
	examID, err := request.params.RequireExamID()
	if err != nil {
		return operationResult{}, err
	}
	var body editExamDraftTextRequest
	if err = request.decodeJSON(&body); err != nil {
		return operationResult{}, err
	}
	title := body.Title.ValuePointer()
	instructions := body.InstructionsMarkdown.ValuePointer()
	if title == nil && instructions == nil {
		return operationResult{}, invalidRequestError(errors.New("at least one authored field is required"))
	}
	if body.ExpectedDraftRevision < 1 {
		return operationResult{}, invalidRequestError(errors.New("must be positive"))
	}
	view, err := m.exams.EditExamDraftText(request.context, request.invocation(), exam.EditDraftTextCommand{
		ExamID: examID, ExpectedDraftRevision: body.ExpectedDraftRevision,
		Title: title, InstructionsMarkdown: instructions, IdempotencyKey: request.idempotencyKey,
	})
	if err != nil {
		return operationResult{}, err
	}
	return jsonResult(http.StatusOK, examResponseFromView(view)), nil
}

func (m examResourceModule) configureDraftFocusLoss(request operationRequest) (operationResult, error) {
	examID, err := request.params.RequireExamID()
	if err != nil {
		return operationResult{}, err
	}
	var body configureExamDraftFocusLossRequest
	if err = request.decodeJSON(&body); err != nil {
		return operationResult{}, err
	}
	if body.ExpectedDraftRevision < 1 {
		return operationResult{}, invalidRequestError(errors.New("must be positive"))
	}
	if body.MinimumDurationMilliseconds < 500 || body.MinimumDurationMilliseconds > 300_000 {
		return operationResult{}, invalidRequestError(errors.New("must be between 500 and 300000"))
	}
	if body.IncidentCount < 1 || body.IncidentCount > 100 {
		return operationResult{}, invalidRequestError(errors.New("must be between 1 and 100"))
	}
	if body.WindowMilliseconds < 10_000 || body.WindowMilliseconds > 14_400_000 || body.WindowMilliseconds < body.MinimumDurationMilliseconds {
		return operationResult{}, invalidRequestError(errors.New("must be between 10000 and 14400000 and at least minimum_duration_milliseconds"))
	}
	outcome := model.IntegrityThresholdOutcome(body.Outcome)
	switch outcome {
	case model.IntegrityOutcomeFlag, model.IntegrityOutcomeFlagAndWarn, model.IntegrityOutcomeFlagAndSuspend:
	default:
		return operationResult{}, invalidRequestError(errors.New("is not supported"))
	}
	view, err := m.exams.ConfigureExamDraftFocusLoss(request.context, request.invocation(), exam.ConfigureDraftFocusLossCommand{
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

func (m examResourceModule) configureDraftBrowserPolicy(request operationRequest) (operationResult, error) {
	examID, err := request.params.RequireExamID()
	if err != nil {
		return operationResult{}, err
	}
	var body configureExamDraftBrowserPolicyRequest
	if err = request.decodeJSON(&body); err != nil {
		return operationResult{}, err
	}
	if body.ExpectedDraftRevision < 1 {
		return operationResult{}, invalidRequestError(errors.New("must be positive"))
	}
	policy, err := body.BrowserPolicy.model()
	if err != nil {
		return operationResult{}, invalidRequestError(err)
	}
	view, err := m.exams.ConfigureExamDraftBrowserPolicy(request.context, request.invocation(), exam.ConfigureDraftBrowserPolicyCommand{
		ExamID: examID, ExpectedDraftRevision: body.ExpectedDraftRevision, Policy: policy, IdempotencyKey: request.idempotencyKey,
	})
	if err != nil {
		return operationResult{}, err
	}
	return jsonResult(http.StatusOK, examResponseFromView(view)), nil
}

func (m examResourceModule) configureDraftNativePolicy(request operationRequest) (operationResult, error) {
	examID, err := request.params.RequireExamID()
	if err != nil {
		return operationResult{}, err
	}
	var body configureExamDraftNativePolicyRequest
	if err = request.decodeJSON(&body); err != nil {
		return operationResult{}, err
	}
	if body.ExpectedDraftRevision < 1 {
		return operationResult{}, invalidRequestError(errors.New("must be positive"))
	}
	if err := body.Native.Validate(); err != nil {
		return operationResult{}, invalidRequestError(err)
	}
	view, err := m.exams.ConfigureExamDraftNativePolicy(request.context, request.invocation(), exam.ConfigureDraftNativePolicyCommand{
		ExamID: examID, ExpectedDraftRevision: body.ExpectedDraftRevision, NativePolicy: body.Native, IdempotencyKey: request.idempotencyKey,
	})
	if err != nil {
		return operationResult{}, err
	}
	return jsonResult(http.StatusOK, examResponseFromView(view)), nil
}

func (m examResourceModule) archive(request operationRequest) (operationResult, error) {
	examID, err := request.params.RequireExamID()
	if err != nil {
		return operationResult{}, err
	}
	var body archiveExamRequest
	if err = request.decodeJSON(&body); err != nil {
		return operationResult{}, err
	}
	if body.ExpectedExamRevision < 1 {
		return operationResult{}, invalidRequestError(errors.New("must be positive"))
	}
	exam, err := m.exams.ArchiveExam(request.context, request.invocation(), exam.ArchiveCommand{
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
	query := exam.ListManagersQuery{ExamID: examID, Limit: 50}
	values := request.request.URL.Query()
	if raw := values.Get("limit"); raw != "" {
		query.Limit, err = strconv.Atoi(raw)
		if err != nil || query.Limit < 1 || query.Limit > 200 {
			return operationResult{}, invalidRequestError(errors.New("must be between 1 and 200"))
		}
	}
	if raw := values.Get("cursor"); raw != "" {
		cursor, decodeErr := decodeExamManagerCursor(raw)
		if decodeErr != nil {
			return operationResult{}, invalidRequestError(decodeErr)
		}
		query.BeforeGrantedAt, _ = model.ParseRFC3339(cursor.GrantedAt)
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
		response.NextCursor, err = encodeExamManagerCursor(examManagerCursor{GrantedAt: model.FormatRFC3339(last.GrantedAt), UserID: last.UserID.String()})
		if err != nil {
			return operationResult{}, unpresentableResult(err)
		}
	}
	return jsonResult(http.StatusOK, response), nil
}

func (m examResourceModule) addManager(request operationRequest) (operationResult, error) {
	examID, err := examIDFromRequest(request)
	if err != nil {
		return operationResult{}, err
	}
	var body addExamManagerRequest
	if err = request.decodeJSON(&body); err != nil {
		return operationResult{}, err
	}
	userID, err := parseExamManagerRequest(body.UserID, body.ExpectedExamRevision)
	if err != nil {
		return operationResult{}, err
	}
	change, err := m.exams.AddExamManager(request.context, request.invocation(), exam.AddManagerCommand{
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
	userID, err := request.params.RequireUserID()
	if err != nil {
		return operationResult{}, err
	}
	var body removeExamManagerRequest
	if err = request.decodeJSON(&body); err != nil {
		return operationResult{}, err
	}
	if body.ExpectedExamRevision < 1 {
		return operationResult{}, invalidRequestError(errors.New("must be positive"))
	}
	change, err := m.exams.RemoveExamManager(request.context, request.invocation(), exam.RemoveManagerCommand{
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
	if err = request.decodeJSON(&body); err != nil {
		return operationResult{}, err
	}
	userID, err := parseExamManagerRequest(body.UserID, body.ExpectedExamRevision)
	if err != nil {
		return operationResult{}, err
	}
	change, err := m.exams.TransferExamOwnership(request.context, request.invocation(), exam.TransferOwnerCommand{
		ExamID: examID, UserID: userID, ExpectedExamRevision: body.ExpectedExamRevision, IdempotencyKey: request.idempotencyKey,
	})
	if err != nil {
		return operationResult{}, err
	}
	return jsonResult(http.StatusOK, examManagerChangeResponseFromApplication(change, true)), nil
}

func examIDFromRequest(request operationRequest) (model.ExamID, error) {
	examID, err := request.params.RequireExamID()
	if err != nil {
		return "", err
	}
	return examID, nil
}

func parseExamManagerRequest(rawUserID string, revision int64) (model.UserID, error) {
	userID, err := model.ParseUserID(rawUserID)
	if err != nil {
		return "", invalidRequestError(err)
	}
	if revision < 1 {
		return "", invalidRequestError(errors.New("must be positive"))
	}
	return userID, nil
}

func encodeExamManagerCursor(cursor examManagerCursor) (string, error) {
	return encodeOpaqueCursor(cursor, examManagerCursorSpec())
}

func decodeExamManagerCursor(raw string) (examManagerCursor, error) {
	return decodeOpaqueCursor(raw, examManagerCursorSpec())
}

func examManagerCursorSpec() opaqueCursorSpec[examManagerCursor] {
	return opaqueCursorSpec[examManagerCursor]{
		label: "Exam Manager", maximumEncodedLength: defaultOpaqueCursorMaximumEncodedLength,
		members: []string{"granted_at", "user_id"},
		validate: func(cursor examManagerCursor) error {
			grantedAt, err := model.ParseRFC3339(cursor.GrantedAt)
			if err != nil || grantedAt.IsZero() || !model.UserID(cursor.UserID).IsValid() {
				return errors.New("invalid Exam Manager keyset")
			}
			return nil
		},
	}
}

func examManagerResponseFromSummary(summary exam.ManagerSummary) examManagerResponse {
	return examManagerResponse{UserID: summary.Manager.UserID.String(), GrantedByUserID: summary.Manager.GrantedByUserID.String(),
		GrantedAt: model.FormatRFC3339(summary.Manager.GrantedAt), IsCreator: summary.IsCreator, IsOwner: summary.IsOwner}
}

func examManagerChangeResponseFromApplication(change store.ExamManagerCommandResult, present bool) examManagerChangeResponse {
	return examManagerChangeResponse{Exam: examIdentityResponseFromModel(*change.Exam), Manager: examManagerResponse{
		UserID: change.Manager.UserID.String(), GrantedByUserID: change.Manager.GrantedByUserID.String(),
		GrantedAt: model.FormatRFC3339(change.Manager.GrantedAt),
		IsCreator: change.Manager.UserID == change.Exam.CreatorUserID, IsOwner: present && change.Manager.UserID == change.Exam.OwnerUserID,
	}}
}

func examListQuery(request *http.Request) (exam.ListQuery, error) {
	query := exam.ListQuery{ArchiveFilter: store.ExamArchiveActive, Limit: 50}
	values := request.URL.Query()
	query.Query = values.Get("q")
	if raw := values.Get("academic_unit_id"); raw != "" {
		unitID, err := model.ParseAcademicUnitID(raw)
		if err != nil {
			return query, err
		}
		query.AcademicUnitID = unitID
	}
	if raw := values.Get("archive_state"); raw != "" {
		query.ArchiveFilter = store.ExamArchiveFilter(raw)
	}
	for _, raw := range values["sitting_state"] {
		state := model.ExamSittingState(raw)
		if !state.IsValid() {
			return query, errors.New("invalid sitting_state")
		}
		for _, existing := range query.SittingStates {
			if existing == state {
				return query, errors.New("duplicate sitting_state")
			}
		}
		query.SittingStates = append(query.SittingStates, state)
	}
	endsAfterRaw, startsBeforeRaw := values.Get("ends_after"), values.Get("starts_before")
	if (endsAfterRaw == "") != (startsBeforeRaw == "") {
		return query, errors.New("ends_after and starts_before must be supplied together")
	}
	if endsAfterRaw != "" {
		var parseErr error
		query.EndsAfter, parseErr = model.ParseRFC3339(endsAfterRaw)
		if parseErr != nil {
			return query, errors.New("invalid ends_after")
		}
		query.StartsBefore, parseErr = model.ParseRFC3339(startsBeforeRaw)
		if parseErr != nil || !query.EndsAfter.Before(query.StartsBefore) {
			return query, errors.New("invalid starts_before")
		}
		query.EndsAfter, query.StartsBefore = model.TimeUTC(query.EndsAfter), model.TimeUTC(query.StartsBefore)
	}
	switch query.ArchiveFilter {
	case store.ExamArchiveActive, store.ExamArchiveArchived, store.ExamArchiveAll:
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
		query.BeforeUpdatedAt, err = model.ParseRFC3339(cursor.UpdatedAt)
		if err != nil {
			return query, errors.New("invalid Exam catalog cursor")
		}
		query.BeforeUpdatedAt = model.TimeUTC(query.BeforeUpdatedAt)
		query.BeforeExamID = model.ExamID(cursor.ExamID)
	}
	return query, nil
}

func encodeExamCatalogCursor(cursor examCatalogCursor) (string, error) {
	return encodeOpaqueCursor(cursor, examCatalogCursorSpec())
}

func decodeExamCatalogCursor(raw string) (examCatalogCursor, error) {
	return decodeOpaqueCursor(raw, examCatalogCursorSpec())
}

func examCatalogCursorSpec() opaqueCursorSpec[examCatalogCursor] {
	return opaqueCursorSpec[examCatalogCursor]{
		label: "Exam catalog", maximumEncodedLength: defaultOpaqueCursorMaximumEncodedLength,
		members: []string{"updated_at", "exam_id"},
		validate: func(cursor examCatalogCursor) error {
			updatedAt, err := model.ParseRFC3339(cursor.UpdatedAt)
			if err != nil || updatedAt.IsZero() || !model.ExamID(cursor.ExamID).IsValid() {
				return errors.New("invalid Exam catalog keyset")
			}
			return nil
		},
	}
}

func examSummaryResponseFromApplication(summary store.ExamSummary) examSummaryResponse {
	var archivedAt *string
	if summary.ArchivedAt.Valid {
		formatted := model.FormatRFC3339(summary.ArchivedAt.Time)
		archivedAt = &formatted
	}
	response := examSummaryResponse{ID: summary.ID.String(), AcademicUnitID: summary.AcademicUnitID.String(),
		AcademicUnitDisplayName: summary.AcademicUnitDisplayName,
		CreatorUserID:           summary.CreatorUserID.String(), OwnerUserID: summary.OwnerUserID.String(), Title: summary.Title,
		UpdatedAt: model.FormatRFC3339(summary.UpdatedAt), ArchivedAt: archivedAt,
		Revision: summary.Revision, ManagerCount: summary.ManagerCount, SittingCount: summary.SittingCount,
		MatchingSittingCount: summary.MatchingSittingCount}
	if summary.SittingSummary != nil {
		response.SittingSummary = &examCatalogSittingResponse{ID: summary.SittingSummary.ID.String(),
			ExamRevisionID: summary.SittingSummary.ExamRevisionID.String(), ClassID: summary.SittingSummary.ClassID.String(),
			ClassDisplayName: summary.SittingSummary.ClassDisplayName,
			ScheduledStartAt: model.FormatRFC3339(summary.SittingSummary.ScheduledStartAt),
			ScheduledEndAt:   model.FormatRFC3339(summary.SittingSummary.ScheduledEndAt),
			State:            summary.SittingSummary.State, Revision: summary.SittingSummary.Revision}
	}
	return response
}

func examIdentityResponseFromModel(exam model.Exam) examIdentityResponse {
	var archivedAt *string
	if exam.ArchivedAt.Valid {
		formatted := model.FormatRFC3339(exam.ArchivedAt.Time)
		archivedAt = &formatted
	}
	return examIdentityResponse{ID: exam.ID.String(), AcademicUnitID: exam.AcademicUnitID.String(), CreatorUserID: exam.CreatorUserID.String(),
		OwnerUserID: exam.OwnerUserID.String(), DefaultRevisionID: exam.DefaultRevisionID.String(),
		CreatedAt: model.FormatRFC3339(exam.CreatedAt), UpdatedAt: model.FormatRFC3339(exam.UpdatedAt),
		ArchivedAt: archivedAt, Revision: exam.Revision}
}

func examResponseFromView(view exam.View) examResponse {
	policy := view.Draft.Policy
	identity := examIdentityResponseFromModel(view.Exam)
	browserPolicy := view.Draft.BrowserPolicy
	return examResponse{
		Exam: identity,
		Draft: examDraftResponse{
			ExamID: view.Draft.ExamID.String(), Title: view.Draft.Title,
			InstructionsMarkdown: view.Draft.InstructionsMarkdown,
			Policy: examPolicyResponse{
				Native:         policy.Native.Clone(),
				ConnectionLoss: examConnectionPolicyResponse{Outcome: string(policy.ConnectionLoss.Outcome)},
				FocusLoss: examFocusPolicyResponse{Enabled: policy.FocusLoss.Enabled,
					MinimumDurationMilliseconds: policy.FocusLoss.MinimumDuration.Milliseconds(), IncidentCount: policy.FocusLoss.IncidentCount,
					WindowMilliseconds: policy.FocusLoss.Window.Milliseconds(), Outcome: string(policy.FocusLoss.Outcome)},
			},
			BrowserPolicy:  browserPolicyDocumentFromModel(browserPolicy),
			Capacity:       examCapacityPolicyResponseFromModel(view.Capacity),
			BaseRevisionID: view.Draft.BaseRevisionID.String(), UpdatedAt: model.FormatRFC3339(view.Draft.UpdatedAt),
			Revision: view.Draft.Revision, ResourceCount: view.ResourceCount, HasStarterWorkspace: view.HasStarterWorkspace,
		},
		OwnerUserID: view.OwnerUserID.String(), ManagerCount: view.ManagerCount,
	}
}

func (r *configureExamDraftNativePolicyRequest) UnmarshalJSON(data []byte) error {
	if err := rejectDuplicateJSONObjectMembers(data, "native policy request"); err != nil {
		return err
	}
	var members map[string]json.RawMessage
	if err := json.Unmarshal(data, &members); err != nil {
		return err
	}
	if len(members) != 2 {
		return errors.New("native policy request requires exactly expected_revision and native")
	}
	for _, name := range []string{"expected_revision", "native"} {
		value, exists := members[name]
		if !exists || bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return errors.New("native policy request member is missing or null")
		}
	}
	type wire configureExamDraftNativePolicyRequest
	var decoded wire
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&decoded); err != nil {
		return err
	}
	*r = configureExamDraftNativePolicyRequest(decoded)
	return nil
}
