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
	"time"

	application "github.com/sudosylabs/proctor/server/app"
	"github.com/sudosylabs/proctor/server/model"
)

const examSittingCursorVersion = 1

type ExamSittingApplication interface {
	ScheduleExamSitting(context.Context, application.Invocation, application.ScheduleExamSittingCommand) (application.ExamSittingView, error)
	GetExamSitting(context.Context, application.Invocation, application.GetExamSittingQuery) (application.ExamSittingView, error)
	ListExamSittings(context.Context, application.Invocation, application.ListExamSittingsQuery) (application.ExamSittingPage, error)
	UpdateExamSittingSchedule(context.Context, application.Invocation, application.UpdateExamSittingScheduleCommand) (application.ExamSittingView, error)
	CancelExamSitting(context.Context, application.Invocation, application.CancelExamSittingCommand) (application.ExamSittingView, error)
}

type scheduleExamSittingRequest struct {
	ExamRevisionID   string `json:"exam_revision_id"`
	ClassID          string `json:"class_id"`
	ScheduledStartAt string `json:"scheduled_start_at"`
	ScheduledEndAt   string `json:"scheduled_end_at"`
}

type updateExamSittingScheduleRequest struct {
	ExpectedRevision int64            `json:"expected_revision"`
	ExamRevisionID   Optional[string] `json:"exam_revision_id"`
	ClassID          Optional[string] `json:"class_id"`
	ScheduledStartAt Optional[string] `json:"scheduled_start_at"`
	ScheduledEndAt   Optional[string] `json:"scheduled_end_at"`
}

type cancelExamSittingRequest struct {
	ExpectedRevision int64  `json:"expected_revision"`
	Reason           string `json:"reason"`
}

func (body *scheduleExamSittingRequest) UnmarshalJSON(encoded []byte) error {
	type wire scheduleExamSittingRequest
	var decoded wire
	if err := decodeDuplicateFreeExamSittingObject(encoded, &decoded); err != nil {
		return err
	}
	*body = scheduleExamSittingRequest(decoded)
	return nil
}

func (body *updateExamSittingScheduleRequest) UnmarshalJSON(encoded []byte) error {
	type wire updateExamSittingScheduleRequest
	var decoded wire
	if err := decodeDuplicateFreeExamSittingObject(encoded, &decoded); err != nil {
		return err
	}
	*body = updateExamSittingScheduleRequest(decoded)
	return nil
}

func (body *cancelExamSittingRequest) UnmarshalJSON(encoded []byte) error {
	type wire cancelExamSittingRequest
	var decoded wire
	if err := decodeDuplicateFreeExamSittingObject(encoded, &decoded); err != nil {
		return err
	}
	*body = cancelExamSittingRequest(decoded)
	return nil
}

func decodeDuplicateFreeExamSittingObject(encoded []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	token, err := decoder.Token()
	if err != nil || token != json.Delim('{') {
		return errors.New("Exam Sitting request must be a JSON object")
	}
	seen := make(map[string]struct{})
	for decoder.More() {
		keyToken, tokenErr := decoder.Token()
		if tokenErr != nil {
			return tokenErr
		}
		key, ok := keyToken.(string)
		if !ok {
			return errors.New("Exam Sitting request member is invalid")
		}
		if _, duplicate := seen[key]; duplicate {
			return errors.New("Exam Sitting request contains a duplicate member")
		}
		seen[key] = struct{}{}
		var value json.RawMessage
		if tokenErr = decoder.Decode(&value); tokenErr != nil {
			return tokenErr
		}
	}
	if _, err = decoder.Token(); err != nil {
		return err
	}
	strict := json.NewDecoder(bytes.NewReader(encoded))
	strict.DisallowUnknownFields()
	return strict.Decode(target)
}

type examSittingResponse struct {
	ID               string `json:"id"`
	ExamID           string `json:"exam_id"`
	ExamRevisionID   string `json:"exam_revision_id"`
	ClassID          string `json:"class_id"`
	ScheduledStartAt string `json:"scheduled_start_at"`
	ScheduledEndAt   string `json:"scheduled_end_at"`
	State            string `json:"state"`
	CreatedAt        string `json:"created_at"`
	UpdatedAt        string `json:"updated_at"`
	OpenedAt         string `json:"opened_at,omitempty"`
	PausedAt         string `json:"paused_at,omitempty"`
	ClosingAt        string `json:"closing_at,omitempty"`
	ClosedAt         string `json:"closed_at,omitempty"`
	CanceledAt       string `json:"canceled_at,omitempty"`
	ReasonCode       string `json:"reason_code,omitempty"`
	Revision         int64  `json:"revision"`
}

type examSittingListResponse struct {
	Items      []examSittingResponse `json:"items"`
	NextCursor string                `json:"next_cursor,omitempty"`
}

type examSittingCursor struct {
	StartAt time.Time
	ID      model.ExamSittingID
}

type examSittingCursorWire struct {
	Version int    `json:"version"`
	StartAt string `json:"start_at"`
	ID      string `json:"id"`
}

type examSittingHTTPModule struct{ application ExamSittingApplication }

func examSittingResource(app ExamSittingApplication) resource {
	module := examSittingHTTPModule{application: app}
	collection := apiPath(literal("exams"), canonicalID("exam_id"), literal("sittings"))
	member := apiPath(literal("exams"), canonicalID("exam_id"), literal("sittings"), canonicalID("exam_sitting_id"))
	cancel := apiPath(literal("exams"), canonicalID("exam_id"), literal("sittings"), canonicalID("exam_sitting_id"), literal("cancel"))
	readErrors := academicReadErrorCodes("request.invalid", "resource.not_found", "exam.sitting.invalid", "exam.sitting.unavailable")
	return newResource(
		"exam-sittings",
		idempotentPrincipalRoute(IdempotencyRequired, http.MethodPost, collection, examSittingScheduleErrorCodes(), module.schedule),
		principalRoute(http.MethodGet, collection, readErrors, module.list),
		principalRoute(http.MethodGet, member, readErrors, module.get),
		idempotentPrincipalRoute(IdempotencyRequired, http.MethodPatch, member, examSittingUpdateErrorCodes(), module.updateSchedule),
		idempotentPrincipalRoute(IdempotencyRequired, http.MethodPost, cancel, examSittingCancelErrorCodes(), module.cancel),
	)
}

func examSittingMutationErrorCodes(specific ...string) []string {
	common := []string{"request.invalid", "resource.not_found", "exam.sitting.invalid", "exam.sitting.conflict", "exam.sitting.unavailable"}
	common = append(common, specific...)
	return academicMutationErrorCodes(append(common,
		"idempotency.key_required", "idempotency.invalid_key", "idempotency.conflict", "idempotency.in_progress")...)
}

func examSittingScheduleErrorCodes() []string {
	return examSittingMutationErrorCodes("exam.archived", "exam.sitting.class_ineligible", "exam.sitting.schedule_outside_period", "exam.sitting.schedule_not_future")
}

func examSittingUpdateErrorCodes() []string {
	return examSittingMutationErrorCodes("exam.archived", "exam.sitting.revision_conflict", "exam.sitting.no_changes", "exam.sitting.state_conflict", "exam.sitting.class_ineligible", "exam.sitting.schedule_outside_period", "exam.sitting.schedule_not_future")
}

func examSittingCancelErrorCodes() []string {
	return examSittingMutationErrorCodes("exam.archived", "exam.sitting.revision_conflict", "exam.sitting.state_conflict")
}

func (module examSittingHTTPModule) schedule(request operationRequest) (operationResult, error) {
	examID, err := examSittingExamID(request)
	if err != nil {
		return operationResult{}, err
	}
	var body scheduleExamSittingRequest
	if err = request.decodeJSON(&body, "scheduleExamSitting"); err != nil {
		return operationResult{}, err
	}
	revisionID, err := model.ParseExamRevisionID(body.ExamRevisionID)
	if err != nil {
		return operationResult{}, invalidRequestError("exam_revision_id", err)
	}
	classID, err := model.ParseClassID(body.ClassID)
	if err != nil {
		return operationResult{}, invalidRequestError("class_id", err)
	}
	startAt, err := parseExamSittingTime("scheduled_start_at", body.ScheduledStartAt)
	if err != nil {
		return operationResult{}, err
	}
	endAt, err := parseExamSittingTime("scheduled_end_at", body.ScheduledEndAt)
	if err != nil {
		return operationResult{}, err
	}
	if !startAt.Before(endAt) {
		return operationResult{}, invalidRequestError("schedule", errors.New("must be a nonempty interval"))
	}
	view, err := module.application.ScheduleExamSitting(request.context, request.invocation(), application.ScheduleExamSittingCommand{
		ExamID: examID, ExamRevisionID: revisionID, ClassID: classID, ScheduledStartAt: startAt, ScheduledEndAt: endAt,
		IdempotencyKey: request.idempotencyKey,
	})
	if err != nil {
		return operationResult{}, err
	}
	return jsonResult(http.StatusCreated, examSittingResponseFromView(view)), nil
}

func (module examSittingHTTPModule) get(request operationRequest) (operationResult, error) {
	examID, sittingID, err := examSittingIDs(request)
	if err != nil {
		return operationResult{}, err
	}
	view, err := module.application.GetExamSitting(request.context, request.invocation(), application.GetExamSittingQuery{ExamID: examID, SittingID: sittingID})
	if err != nil {
		return operationResult{}, err
	}
	return jsonResult(http.StatusOK, examSittingResponseFromView(view)), nil
}

func (module examSittingHTTPModule) list(request operationRequest) (operationResult, error) {
	examID, err := examSittingExamID(request)
	if err != nil {
		return operationResult{}, err
	}
	query := application.ListExamSittingsQuery{ExamID: examID, Limit: 50}
	values := request.request.URL.Query()
	if raw := values.Get("class_id"); raw != "" {
		query.ClassID, err = model.ParseClassID(raw)
		if err != nil {
			return operationResult{}, invalidRequestError("class_id", err)
		}
	}
	query.States, err = parseExamSittingStates(values["state"])
	if err != nil {
		return operationResult{}, err
	}
	endsAfter, startsBefore := values.Get("ends_after"), values.Get("starts_before")
	if (endsAfter == "") != (startsBefore == "") {
		return operationResult{}, invalidRequestError("overlap", errors.New("ends_after and starts_before must be paired"))
	}
	if endsAfter != "" {
		query.OverlapStartAt, err = parseExamSittingTime("ends_after", endsAfter)
		if err != nil {
			return operationResult{}, err
		}
		query.OverlapEndAt, err = parseExamSittingTime("starts_before", startsBefore)
		if err != nil {
			return operationResult{}, err
		}
		if !query.OverlapStartAt.Before(query.OverlapEndAt) {
			return operationResult{}, invalidRequestError("overlap", errors.New("must be a nonempty interval"))
		}
	}
	if raw := values.Get("limit"); raw != "" {
		query.Limit, err = strconv.Atoi(raw)
		if err != nil || query.Limit < 1 || query.Limit > 200 {
			return operationResult{}, invalidRequestError("limit", errors.New("must be between 1 and 200"))
		}
	}
	if raw := values.Get("cursor"); raw != "" {
		cursor, cursorErr := decodeExamSittingCursor(raw)
		if cursorErr != nil {
			return operationResult{}, invalidRequestError("cursor", cursorErr)
		}
		query.BeforeScheduledStartAt, query.BeforeSittingID = cursor.StartAt, cursor.ID
	}
	page, err := module.application.ListExamSittings(request.context, request.invocation(), query)
	if err != nil {
		return operationResult{}, err
	}
	response := examSittingListResponse{Items: make([]examSittingResponse, 0, len(page.Items))}
	for _, view := range page.Items {
		response.Items = append(response.Items, examSittingResponseFromView(view))
	}
	if page.HasMore && len(page.Items) > 0 {
		last := page.Items[len(page.Items)-1].Sitting
		if last != nil {
			response.NextCursor = encodeExamSittingCursor(examSittingCursor{StartAt: last.ScheduledStartAt, ID: last.ID})
		}
	}
	return jsonResult(http.StatusOK, response), nil
}

func (module examSittingHTTPModule) updateSchedule(request operationRequest) (operationResult, error) {
	examID, sittingID, err := examSittingIDs(request)
	if err != nil {
		return operationResult{}, err
	}
	var body updateExamSittingScheduleRequest
	if err = request.decodeJSON(&body, "updateExamSittingSchedule"); err != nil {
		return operationResult{}, err
	}
	if body.ExpectedRevision < 1 {
		return operationResult{}, invalidRequestError("expected_revision", errors.New("must be positive"))
	}
	if noExamSittingScheduleChange(body) {
		return operationResult{}, invalidRequestError("schedule", errors.New("at least one change is required"))
	}
	command := application.UpdateExamSittingScheduleCommand{ExamID: examID, SittingID: sittingID, ExpectedRevision: body.ExpectedRevision, IdempotencyKey: request.idempotencyKey}
	if body.ExamRevisionID.IsSet() {
		if body.ExamRevisionID.IsNull() {
			return operationResult{}, invalidRequestError("exam_revision_id", errors.New("must not be null"))
		}
		parsed, parseErr := model.ParseExamRevisionID(*body.ExamRevisionID.ValuePointer())
		if parseErr != nil {
			return operationResult{}, invalidRequestError("exam_revision_id", parseErr)
		}
		command.ExamRevisionID = &parsed
	}
	if body.ClassID.IsSet() {
		if body.ClassID.IsNull() {
			return operationResult{}, invalidRequestError("class_id", errors.New("must not be null"))
		}
		parsed, parseErr := model.ParseClassID(*body.ClassID.ValuePointer())
		if parseErr != nil {
			return operationResult{}, invalidRequestError("class_id", parseErr)
		}
		command.ClassID = &parsed
	}
	command.ScheduledStartAt, err = parseOptionalExamSittingTime("scheduled_start_at", body.ScheduledStartAt)
	if err != nil {
		return operationResult{}, err
	}
	command.ScheduledEndAt, err = parseOptionalExamSittingTime("scheduled_end_at", body.ScheduledEndAt)
	if err != nil {
		return operationResult{}, err
	}
	view, err := module.application.UpdateExamSittingSchedule(request.context, request.invocation(), command)
	if err != nil {
		return operationResult{}, err
	}
	return jsonResult(http.StatusOK, examSittingResponseFromView(view)), nil
}

func (module examSittingHTTPModule) cancel(request operationRequest) (operationResult, error) {
	examID, sittingID, err := examSittingIDs(request)
	if err != nil {
		return operationResult{}, err
	}
	var body cancelExamSittingRequest
	if err = request.decodeJSON(&body, "cancelExamSitting"); err != nil {
		return operationResult{}, err
	}
	if body.ExpectedRevision < 1 {
		return operationResult{}, invalidRequestError("expected_revision", errors.New("must be positive"))
	}
	if body.Reason == "" {
		return operationResult{}, invalidRequestError("reason", errors.New("is required"))
	}
	view, err := module.application.CancelExamSitting(request.context, request.invocation(), application.CancelExamSittingCommand{
		ExamID: examID, SittingID: sittingID, ExpectedRevision: body.ExpectedRevision,
		PrivateReason: body.Reason, IdempotencyKey: request.idempotencyKey,
	})
	if err != nil {
		return operationResult{}, err
	}
	return jsonResult(http.StatusOK, examSittingResponseFromView(view)), nil
}

func examSittingExamID(request operationRequest) (model.ExamID, error) {
	raw, err := request.params.RequireExamId()
	if err != nil {
		return "", err
	}
	id, err := model.ParseExamID(raw)
	if err != nil {
		return "", invalidRequestError("exam_id", err)
	}
	return id, nil
}

func examSittingIDs(request operationRequest) (model.ExamID, model.ExamSittingID, error) {
	examID, err := examSittingExamID(request)
	if err != nil {
		return "", "", err
	}
	raw, err := request.params.RequireExamSittingId()
	if err != nil {
		return "", "", err
	}
	sittingID, err := model.ParseExamSittingID(raw)
	if err != nil {
		return "", "", invalidRequestError("exam_sitting_id", err)
	}
	return examID, sittingID, nil
}

func noExamSittingScheduleChange(body updateExamSittingScheduleRequest) bool {
	return !body.ExamRevisionID.IsSet() && !body.ClassID.IsSet() && !body.ScheduledStartAt.IsSet() && !body.ScheduledEndAt.IsSet()
}

func parseOptionalExamSittingTime(field string, value Optional[string]) (*time.Time, error) {
	if !value.IsSet() {
		return nil, nil
	}
	if value.IsNull() {
		return nil, invalidRequestError(field, errors.New("must not be null"))
	}
	parsed, err := parseExamSittingTime(field, *value.ValuePointer())
	if err != nil {
		return nil, err
	}
	return &parsed, nil
}

func parseExamSittingTime(field, raw string) (time.Time, error) {
	parsed, err := time.Parse(time.RFC3339Nano, raw)
	if err != nil || parsed.IsZero() {
		return time.Time{}, invalidRequestError(field, errors.New("must be an RFC 3339 instant"))
	}
	return model.TimeUTC(parsed), nil
}

func parseExamSittingStates(raw []string) ([]model.ExamSittingState, error) {
	states := make([]model.ExamSittingState, 0, len(raw))
	seen := make(map[model.ExamSittingState]struct{}, len(raw))
	for _, value := range raw {
		state := model.ExamSittingState(value)
		if !state.IsValid() {
			return nil, invalidRequestError("state", errors.New("must be a valid Exam Sitting state"))
		}
		if _, duplicate := seen[state]; duplicate {
			continue
		}
		seen[state] = struct{}{}
		states = append(states, state)
		if len(states) > 6 {
			return nil, invalidRequestError("state", errors.New("must contain at most six values"))
		}
	}
	return states, nil
}

func examSittingResponseFromView(view application.ExamSittingView) examSittingResponse {
	if view.Sitting == nil {
		return examSittingResponse{}
	}
	sitting := view.Sitting
	return examSittingResponse{
		ID: sitting.ID.String(), ExamID: sitting.ExamID.String(), ExamRevisionID: sitting.ExamRevisionID.String(), ClassID: sitting.ClassID.String(),
		ScheduledStartAt: model.TimeUTC(sitting.ScheduledStartAt).Format(time.RFC3339Nano), ScheduledEndAt: model.TimeUTC(sitting.ScheduledEndAt).Format(time.RFC3339Nano),
		State: string(sitting.State), CreatedAt: model.TimeUTC(sitting.CreatedAt).Format(time.RFC3339Nano), UpdatedAt: model.TimeUTC(sitting.UpdatedAt).Format(time.RFC3339Nano),
		OpenedAt: sitting.OpenedAt.FormatRFC3339(), PausedAt: sitting.PausedAt.FormatRFC3339(), ClosingAt: sitting.ClosingAt.FormatRFC3339(),
		ClosedAt: sitting.ClosedAt.FormatRFC3339(), CanceledAt: sitting.CanceledAt.FormatRFC3339(), ReasonCode: string(sitting.ReasonCode), Revision: sitting.Revision,
	}
}

func encodeExamSittingCursor(cursor examSittingCursor) string {
	wire := examSittingCursorWire{Version: examSittingCursorVersion, StartAt: model.TimeUTC(cursor.StartAt).Format(time.RFC3339Nano), ID: cursor.ID.String()}
	encoded, _ := json.Marshal(wire)
	return base64.RawURLEncoding.EncodeToString(encoded)
}

func decodeExamSittingCursor(raw string) (examSittingCursor, error) {
	var wire examSittingCursorWire
	decoded, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return examSittingCursor{}, errors.New("invalid Exam Sitting cursor")
	}
	decoder := json.NewDecoder(bytes.NewReader(decoded))
	decoder.DisallowUnknownFields()
	if err = decoder.Decode(&wire); err != nil || wire.Version != examSittingCursorVersion {
		return examSittingCursor{}, errors.New("invalid Exam Sitting cursor")
	}
	var trailing any
	if err = decoder.Decode(&trailing); err != io.EOF {
		return examSittingCursor{}, errors.New("invalid Exam Sitting cursor")
	}
	startAt, err := time.Parse(time.RFC3339Nano, wire.StartAt)
	if err != nil || startAt.IsZero() {
		return examSittingCursor{}, errors.New("invalid Exam Sitting cursor")
	}
	id, err := model.ParseExamSittingID(wire.ID)
	if err != nil {
		return examSittingCursor{}, errors.New("invalid Exam Sitting cursor")
	}
	return examSittingCursor{StartAt: model.TimeUTC(startAt), ID: id}, nil
}
