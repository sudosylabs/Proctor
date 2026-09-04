// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

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
	PauseExamSitting(context.Context, application.Invocation, application.PauseExamSittingCommand) (application.ExamSittingView, error)
	ResumeExamSitting(context.Context, application.Invocation, application.ResumeExamSittingCommand) (application.ExamSittingView, error)
	ExtendExamSitting(context.Context, application.Invocation, application.ExtendExamSittingCommand) (application.ExamSittingView, error)
	CloseExamSitting(context.Context, application.Invocation, application.CloseExamSittingCommand) (application.ExamSittingView, error)
	ListExamSittingNoShows(context.Context, application.Invocation, application.ListExamSittingNoShowsQuery) (application.ExamSittingNoShowPage, error)
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

type examSittingManagerTransitionRequest struct {
	ExpectedRevision int64  `json:"expected_revision"`
	Reason           string `json:"reason"`
}

type extendExamSittingRequest struct {
	ExpectedRevision int64  `json:"expected_revision"`
	ScheduledEndAt   string `json:"scheduled_end_at"`
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

func (body *examSittingManagerTransitionRequest) UnmarshalJSON(encoded []byte) error {
	type wire examSittingManagerTransitionRequest
	var decoded wire
	if err := decodeDuplicateFreeExamSittingObject(encoded, &decoded); err != nil {
		return err
	}
	*body = examSittingManagerTransitionRequest(decoded)
	return nil
}

func (body *extendExamSittingRequest) UnmarshalJSON(encoded []byte) error {
	type wire extendExamSittingRequest
	var decoded wire
	if err := decodeDuplicateFreeExamSittingObject(encoded, &decoded); err != nil {
		return err
	}
	*body = extendExamSittingRequest(decoded)
	return nil
}

func decodeDuplicateFreeExamSittingObject(encoded []byte, target any) error {
	if !utf8.Valid(encoded) {
		return errors.New("Exam Sitting request must be valid UTF-8")
	}
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
	if _, err = decoder.Token(); err != io.EOF {
		return errors.New("Exam Sitting request contains trailing JSON")
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

type examSittingNoShowResponse struct {
	CandidateUserID string `json:"candidate_user_id"`
}

type examSittingNoShowListResponse struct {
	Items      []examSittingNoShowResponse `json:"items"`
	NextCursor string                      `json:"next_cursor,omitempty"`
}

type examSittingNoShowCursorWire struct {
	Version         int    `json:"version"`
	CandidateUserID string `json:"candidate_user_id"`
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
	pause := apiPath(literal("exams"), canonicalID("exam_id"), literal("sittings"), canonicalID("exam_sitting_id"), literal("pause"))
	resume := apiPath(literal("exams"), canonicalID("exam_id"), literal("sittings"), canonicalID("exam_sitting_id"), literal("resume"))
	extend := apiPath(literal("exams"), canonicalID("exam_id"), literal("sittings"), canonicalID("exam_sitting_id"), literal("extend"))
	closeSitting := apiPath(literal("exams"), canonicalID("exam_id"), literal("sittings"), canonicalID("exam_sitting_id"), literal("close"))
	noShows := apiPath(literal("exams"), canonicalID("exam_id"), literal("sittings"), canonicalID("exam_sitting_id"), literal("no-shows"))
	readErrors := academicReadErrorCodes("request.invalid", "resource.not_found", "exam.sitting.invalid", "exam.sitting.unavailable")
	noShowErrors := append(append([]string(nil), readErrors...), "exam.sitting.state_conflict")
	return newResource(
		"exam-sittings",
		idempotentPrincipalRoute(IdempotencyRequired, http.MethodPost, collection, examSittingScheduleErrorCodes(), module.schedule),
		principalRoute(http.MethodGet, collection, readErrors, module.list),
		principalRoute(http.MethodGet, member, readErrors, module.get),
		idempotentPrincipalRoute(IdempotencyRequired, http.MethodPatch, member, examSittingUpdateErrorCodes(), module.updateSchedule),
		idempotentPrincipalRoute(IdempotencyRequired, http.MethodPost, cancel, examSittingCancelErrorCodes(), module.cancel),
		idempotentPrincipalRoute(IdempotencyRequired, http.MethodPost, pause, examSittingPauseErrorCodes(), module.pause),
		idempotentPrincipalRoute(IdempotencyRequired, http.MethodPost, resume, examSittingResumeErrorCodes(), module.resume),
		idempotentPrincipalRoute(IdempotencyRequired, http.MethodPost, extend, examSittingExtendErrorCodes(), module.extend),
		idempotentPrincipalRoute(IdempotencyRequired, http.MethodPost, closeSitting, examSittingCloseErrorCodes(), module.close),
		principalRoute(http.MethodGet, noShows, noShowErrors, module.listNoShows),
	)
}

func (module examSittingHTTPModule) listNoShows(request operationRequest) (operationResult, error) {
	examID, sittingID, err := examSittingIDs(request)
	if err != nil {
		return operationResult{}, err
	}
	query := application.ListExamSittingNoShowsQuery{ExamID: examID, SittingID: sittingID, Limit: 50}
	values := request.request.URL.Query()
	if raw := values.Get("limit"); raw != "" {
		query.Limit, err = strconv.Atoi(raw)
		if err != nil || query.Limit < 1 || query.Limit > 200 {
			return operationResult{}, invalidRequestError("limit", errors.New("must be between 1 and 200"))
		}
	}
	if raw := values.Get("cursor"); raw != "" {
		query.AfterCandidateUserID, err = decodeExamSittingNoShowCursor(raw)
		if err != nil {
			return operationResult{}, invalidRequestError("cursor", err)
		}
	}
	page, err := module.application.ListExamSittingNoShows(request.context, request.invocation(), query)
	if err != nil {
		return operationResult{}, err
	}
	response := examSittingNoShowListResponse{Items: make([]examSittingNoShowResponse, 0, len(page.Items))}
	for _, item := range page.Items {
		response.Items = append(response.Items, examSittingNoShowResponse{CandidateUserID: item.CandidateUserID.String()})
	}
	if page.HasMore && len(page.Items) == 0 {
		return operationResult{}, application.NewError("exam.sitting.unavailable").Wrap(errors.New("no-show page made no progress"))
	}
	if page.HasMore {
		response.NextCursor, err = encodeExamSittingNoShowCursor(page.Items[len(page.Items)-1].CandidateUserID)
		if err != nil {
			return operationResult{}, application.NewError("exam.sitting.unavailable").Wrap(err)
		}
	}
	return jsonResult(http.StatusOK, response), nil
}

func encodeExamSittingNoShowCursor(id model.UserID) (string, error) {
	return encodeOpaqueCursor(examSittingNoShowCursorWire{CandidateUserID: id.String()}, examSittingNoShowCursorSpec())
}

func decodeExamSittingNoShowCursor(raw string) (model.UserID, error) {
	wire, err := decodeOpaqueCursor(raw, examSittingNoShowCursorSpec())
	if err != nil {
		return "", err
	}
	id, err := model.ParseUserID(wire.CandidateUserID)
	if err != nil {
		return "", errors.New("invalid no-show cursor")
	}
	return id, nil
}

func examSittingNoShowCursorSpec() opaqueCursorSpec[examSittingNoShowCursorWire] {
	return opaqueCursorSpec[examSittingNoShowCursorWire]{
		label: "no-show", maximumEncodedLength: 342, currentVersion: 1,
		members:        []string{"version", "candidate_user_id"},
		version:        func(cursor examSittingNoShowCursorWire) int { return cursor.Version },
		setVersion:     func(cursor *examSittingNoShowCursorWire, version int) { cursor.Version = version },
		acceptsVersion: func(version int) bool { return version == 1 },
		validate: func(cursor examSittingNoShowCursorWire) error {
			if !model.UserID(cursor.CandidateUserID).IsValid() {
				return errors.New("invalid no-show keyset")
			}
			return nil
		},
	}
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

func examSittingPauseErrorCodes() []string {
	return examSittingMutationErrorCodes("exam.sitting.revision_conflict", "exam.sitting.state_conflict", "exam.sitting.deadline_reached")
}

func examSittingResumeErrorCodes() []string {
	return examSittingMutationErrorCodes("exam.archived", "exam.sitting.revision_conflict", "exam.sitting.state_conflict", "exam.sitting.deadline_reached")
}

func examSittingExtendErrorCodes() []string {
	return examSittingMutationErrorCodes("exam.archived", "exam.sitting.revision_conflict", "exam.sitting.state_conflict",
		"exam.sitting.deadline_reached", "exam.sitting.extension_not_later", "exam.sitting.class_ineligible", "exam.sitting.schedule_outside_period")
}

func examSittingCloseErrorCodes() []string {
	return examSittingMutationErrorCodes("exam.sitting.revision_conflict", "exam.sitting.state_conflict", "exam.sitting.deadline_reached")
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
	if page.HasMore && len(page.Items) == 0 {
		return operationResult{}, application.NewError("exam.sitting.unavailable").Wrap(errors.New("sitting page made no progress"))
	}
	if page.HasMore {
		last := page.Items[len(page.Items)-1].Sitting
		if last == nil {
			return operationResult{}, application.NewError("exam.sitting.unavailable")
		}
		response.NextCursor, err = encodeExamSittingCursor(examSittingCursor{StartAt: last.ScheduledStartAt, ID: last.ID})
		if err != nil {
			return operationResult{}, application.NewError("exam.sitting.unavailable").Wrap(err)
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
	if !validExamSittingManagerReason(body.Reason) {
		return operationResult{}, invalidRequestError("reason", errors.New("must be trimmed valid UTF-8 between 1 and 1000 characters and at most 4000 bytes"))
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

type examSittingManagerCommandCall func(context.Context, application.Invocation, application.PauseExamSittingCommand) (application.ExamSittingView, error)

func (module examSittingHTTPModule) pause(request operationRequest) (operationResult, error) {
	return module.managerTransition(request, "pauseExamSitting", module.application.PauseExamSitting)
}

func (module examSittingHTTPModule) resume(request operationRequest) (operationResult, error) {
	return module.managerTransition(request, "resumeExamSitting", module.application.ResumeExamSitting)
}

func (module examSittingHTTPModule) close(request operationRequest) (operationResult, error) {
	return module.managerTransition(request, "closeExamSitting", module.application.CloseExamSitting)
}

func (module examSittingHTTPModule) managerTransition(request operationRequest, operation string,
	run examSittingManagerCommandCall,
) (operationResult, error) {
	examID, sittingID, err := examSittingIDs(request)
	if err != nil {
		return operationResult{}, err
	}
	var body examSittingManagerTransitionRequest
	if err = request.decodeJSON(&body, operation); err != nil {
		return operationResult{}, err
	}
	if body.ExpectedRevision < 1 {
		return operationResult{}, invalidRequestError("expected_revision", errors.New("must be positive"))
	}
	if !validExamSittingManagerReason(body.Reason) {
		return operationResult{}, invalidRequestError("reason", errors.New("must be trimmed valid UTF-8 between 1 and 1000 characters and at most 4000 bytes"))
	}
	view, err := run(request.context, request.invocation(), application.PauseExamSittingCommand{
		ExamID: examID, SittingID: sittingID, ExpectedRevision: body.ExpectedRevision,
		PrivateReason: body.Reason, IdempotencyKey: request.idempotencyKey,
	})
	if err != nil {
		return operationResult{}, err
	}
	return jsonResult(http.StatusOK, examSittingResponseFromView(view)), nil
}

func (module examSittingHTTPModule) extend(request operationRequest) (operationResult, error) {
	examID, sittingID, err := examSittingIDs(request)
	if err != nil {
		return operationResult{}, err
	}
	var body extendExamSittingRequest
	if err = request.decodeJSON(&body, "extendExamSitting"); err != nil {
		return operationResult{}, err
	}
	if body.ExpectedRevision < 1 {
		return operationResult{}, invalidRequestError("expected_revision", errors.New("must be positive"))
	}
	endAt, err := parseExamSittingTime("scheduled_end_at", body.ScheduledEndAt)
	if err != nil {
		return operationResult{}, err
	}
	if !validExamSittingManagerReason(body.Reason) {
		return operationResult{}, invalidRequestError("reason", errors.New("must be trimmed valid UTF-8 between 1 and 1000 characters and at most 4000 bytes"))
	}
	view, err := module.application.ExtendExamSitting(request.context, request.invocation(), application.ExtendExamSittingCommand{
		ExamID: examID, SittingID: sittingID, ExpectedRevision: body.ExpectedRevision, ScheduledEndAt: endAt,
		PrivateReason: body.Reason, IdempotencyKey: request.idempotencyKey,
	})
	if err != nil {
		return operationResult{}, err
	}
	return jsonResult(http.StatusOK, examSittingResponseFromView(view)), nil
}

func validExamSittingManagerReason(reason string) bool {
	return utf8.ValidString(reason) && reason == strings.TrimSpace(reason) && utf8.RuneCountInString(reason) >= 1 &&
		utf8.RuneCountInString(reason) <= 1000 && len(reason) <= 4000
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

func encodeExamSittingCursor(cursor examSittingCursor) (string, error) {
	wire := examSittingCursorWire{Version: examSittingCursorVersion, StartAt: model.TimeUTC(cursor.StartAt).Format(time.RFC3339Nano), ID: cursor.ID.String()}
	return encodeOpaqueCursor(wire, examSittingCursorSpec())
}

func decodeExamSittingCursor(raw string) (examSittingCursor, error) {
	wire, err := decodeOpaqueCursor(raw, examSittingCursorSpec())
	if err != nil {
		return examSittingCursor{}, err
	}
	startAt, _ := time.Parse(time.RFC3339Nano, wire.StartAt)
	id, _ := model.ParseExamSittingID(wire.ID)
	return examSittingCursor{StartAt: model.TimeUTC(startAt), ID: id}, nil
}

func examSittingCursorSpec() opaqueCursorSpec[examSittingCursorWire] {
	return opaqueCursorSpec[examSittingCursorWire]{
		label: "Exam Sitting", maximumEncodedLength: defaultOpaqueCursorMaximumEncodedLength, currentVersion: examSittingCursorVersion,
		members:        []string{"version", "start_at", "id"},
		version:        func(cursor examSittingCursorWire) int { return cursor.Version },
		setVersion:     func(cursor *examSittingCursorWire, version int) { cursor.Version = version },
		acceptsVersion: func(version int) bool { return version == examSittingCursorVersion },
		validate: func(cursor examSittingCursorWire) error {
			startAt, err := time.Parse(time.RFC3339Nano, cursor.StartAt)
			if err != nil || startAt.IsZero() || !model.ExamSittingID(cursor.ID).IsValid() {
				return errors.New("invalid Exam Sitting keyset")
			}
			return nil
		},
	}
}
