// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package httpapi

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	application "github.com/sudosylabs/proctor/server/app"
	"github.com/sudosylabs/proctor/server/model"
)

const defaultJobPageSize = 50

type JobOperationsApplication interface {
	ListJobs(context.Context, application.Invocation, application.ListJobsQuery) (application.JobPage, error)
	GetJob(context.Context, application.Invocation, application.GetJobQuery) (application.JobView, error)
	ListJobAttempts(context.Context, application.Invocation, application.ListJobAttemptsQuery) (application.JobAttemptPage, error)
	CancelJob(context.Context, application.Invocation, application.CancelJobCommand) (application.JobView, error)
	RetryJob(context.Context, application.Invocation, application.RetryJobCommand) (application.JobView, error)
}

type jobProgressResponse struct {
	Current int64  `json:"current"`
	Total   int64  `json:"total"`
	Stage   string `json:"stage"`
}
type jobResponse struct {
	ID              string               `json:"id"`
	Type            model.JobType        `json:"type"`
	Status          model.JobStatus      `json:"status"`
	CreateAt        int64                `json:"create_at"`
	UpdateAt        int64                `json:"update_at"`
	AvailableAt     int64                `json:"available_at"`
	StartAt         int64                `json:"start_at,omitempty"`
	CompleteAt      int64                `json:"complete_at,omitempty"`
	PublicErrorCode string               `json:"public_error_code,omitempty"`
	AttemptCount    int                  `json:"attempt_count"`
	MaximumAttempts int                  `json:"maximum_attempts"`
	Progress        *jobProgressResponse `json:"progress,omitempty"`
	Revision        int64                `json:"revision"`
}
type jobListResponse struct {
	Items      []jobResponse `json:"items"`
	NextCursor string        `json:"next_cursor,omitempty"`
}
type jobAttemptResponse struct {
	ID              string                 `json:"id"`
	Number          int                    `json:"number"`
	Status          model.JobAttemptStatus `json:"status"`
	StartAt         int64                  `json:"start_at"`
	HeartbeatAt     int64                  `json:"heartbeat_at"`
	LeaseExpiresAt  int64                  `json:"lease_expires_at"`
	CompleteAt      int64                  `json:"complete_at,omitempty"`
	PublicErrorCode string                 `json:"public_error_code,omitempty"`
}
type jobAttemptListResponse struct {
	Items      []jobAttemptResponse `json:"items"`
	NextCursor string               `json:"next_cursor,omitempty"`
}
type jobCursor struct {
	CreatedAt int64  `json:"created_at"`
	ID        string `json:"id"`
}
type jobAttemptCursor struct {
	Number int `json:"number"`
}

type jobResourceModule struct {
	jobs JobOperationsApplication
}

func jobResource(jobs JobOperationsApplication) resource {
	module := jobResourceModule{jobs: jobs}
	return newResource(
		"jobs",
		principalRoute(http.MethodGet, apiPath(literal("jobs")),
			operatorReadErrorCodes("audit.unavailable", "job.query.invalid", "job.unavailable"), module.list),
		principalRoute(http.MethodGet, apiPath(literal("jobs"), canonicalID("job_id")),
			operatorReadErrorCodes("audit.unavailable", "resource.not_found", "job.unavailable"), module.get),
		principalRoute(http.MethodGet, apiPath(literal("jobs"), canonicalID("job_id"), literal("attempts")),
			operatorReadErrorCodes("audit.unavailable", "job.query.invalid", "resource.not_found", "job.unavailable"), module.listAttempts),
		principalRoute(http.MethodPost, apiPath(literal("jobs"), canonicalID("job_id"), literal("cancel")),
			operatorMutationErrorCodes("resource.not_found", "job.cancel.unsupported", "job.conflict", "job.unavailable"), module.cancel),
		principalRoute(http.MethodPost, apiPath(literal("jobs"), canonicalID("job_id"), literal("retry")),
			operatorMutationErrorCodes("resource.not_found", "job.retry.unsupported", "job.conflict", "job.unavailable"), module.retry),
	)
}

func (module jobResourceModule) list(request operationRequest) (operationResult, error) {
	query, err := listJobsQuery(request.request)
	if err != nil {
		return operationResult{}, application.NewError("job.query.invalid")
	}
	page, appErr := module.jobs.ListJobs(request.context, request.invocation(), query)
	if appErr != nil {
		return operationResult{}, appErr
	}
	response := jobListResponse{Items: make([]jobResponse, 0, len(page.Items))}
	for _, item := range page.Items {
		response.Items = append(response.Items, jobResponseFromApplication(item))
	}
	if len(page.Items) == query.Limit {
		last := page.Items[len(page.Items)-1]
		response.NextCursor = encodeJobCursor(jobCursor{CreatedAt: model.MillisFromTime(last.CreatedAt), ID: last.ID.String()})
	}
	return jsonResult(http.StatusOK, response), nil
}
func (module jobResourceModule) get(request operationRequest) (operationResult, error) {
	jobID, err := request.params.RequireJobId()
	if err != nil {
		return operationResult{}, err
	}
	view, err := module.jobs.GetJob(request.context, request.invocation(), application.GetJobQuery{ID: model.JobID(jobID)})
	if err != nil {
		return operationResult{}, err
	}
	return jsonResult(http.StatusOK, jobResponseFromApplication(view)), nil
}
func (module jobResourceModule) listAttempts(request operationRequest) (operationResult, error) {
	jobID, err := request.params.RequireJobId()
	if err != nil {
		return operationResult{}, err
	}
	limit, before, err := attemptPagination(request.request)
	if err != nil {
		return operationResult{}, application.NewError("job.query.invalid")
	}
	page, appErr := module.jobs.ListJobAttempts(request.context, request.invocation(), application.ListJobAttemptsQuery{JobID: model.JobID(jobID), BeforeNumber: before, Limit: limit})
	if appErr != nil {
		return operationResult{}, appErr
	}
	response := jobAttemptListResponse{Items: make([]jobAttemptResponse, 0, len(page.Items))}
	for _, item := range page.Items {
		response.Items = append(response.Items, jobAttemptResponseFromApplication(item))
	}
	if len(page.Items) == limit {
		response.NextCursor = encodeJobAttemptCursor(page.Items[len(page.Items)-1].Number)
	}
	return jsonResult(http.StatusOK, response), nil
}
func (module jobResourceModule) cancel(request operationRequest) (operationResult, error) {
	jobID, err := request.params.RequireJobId()
	if err != nil {
		return operationResult{}, err
	}
	view, err := module.jobs.CancelJob(request.context, request.invocation(), application.CancelJobCommand{ID: model.JobID(jobID)})
	if err != nil {
		return operationResult{}, err
	}
	return jsonResult(http.StatusOK, jobResponseFromApplication(view)), nil
}
func (module jobResourceModule) retry(request operationRequest) (operationResult, error) {
	jobID, err := request.params.RequireJobId()
	if err != nil {
		return operationResult{}, err
	}
	view, err := module.jobs.RetryJob(request.context, request.invocation(), application.RetryJobCommand{ID: model.JobID(jobID)})
	if err != nil {
		return operationResult{}, err
	}
	return jsonResult(http.StatusOK, jobResponseFromApplication(view)), nil
}
func listJobsQuery(r *http.Request) (application.ListJobsQuery, error) {
	limit := defaultJobPageSize
	if raw := r.URL.Query().Get("limit"); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil || value < 1 || value > 200 {
			return application.ListJobsQuery{}, err
		}
		limit = value
	}
	query := application.ListJobsQuery{Limit: limit}
	if raw := r.URL.Query().Get("cursor"); raw != "" {
		cursor, err := decodeJobCursor(raw)
		if err != nil {
			return query, err
		}
		query.BeforeCreatedAt = model.TimeFromMillis(cursor.CreatedAt)
		query.BeforeID = model.JobID(cursor.ID)
	}
	for _, raw := range r.URL.Query()["status"] {
		query.Statuses = append(query.Statuses, model.JobStatus(raw))
	}
	return query, nil
}
func attemptPagination(r *http.Request) (int, int, error) {
	limit := defaultJobPageSize
	var err error
	if raw := r.URL.Query().Get("limit"); raw != "" {
		limit, err = strconv.Atoi(raw)
		if err != nil || limit < 1 || limit > 200 {
			return 0, 0, err
		}
	}
	before := 0
	if raw := r.URL.Query().Get("cursor"); raw != "" {
		before, err = decodeJobAttemptCursor(raw)
		if err != nil {
			return 0, 0, err
		}
	}
	return limit, before, nil
}
func encodeJobAttemptCursor(number int) string {
	encoded, _ := json.Marshal(jobAttemptCursor{Number: number})
	return base64.RawURLEncoding.EncodeToString(encoded)
}
func decodeJobAttemptCursor(raw string) (int, error) {
	decoded, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return 0, err
	}
	var cursor jobAttemptCursor
	if err = json.Unmarshal(decoded, &cursor); err != nil || cursor.Number <= 0 {
		return 0, errors.New("invalid job attempt cursor")
	}
	return cursor.Number, nil
}
func encodeJobCursor(cursor jobCursor) string {
	encoded, _ := json.Marshal(cursor)
	return base64.RawURLEncoding.EncodeToString(encoded)
}
func decodeJobCursor(raw string) (jobCursor, error) {
	var cursor jobCursor
	decoded, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return cursor, err
	}
	if err = json.Unmarshal(decoded, &cursor); err != nil || cursor.CreatedAt <= 0 || !model.IsValidId(cursor.ID) {
		return cursor, errors.New("invalid job cursor")
	}
	return cursor, nil
}
func jobResponseFromApplication(view application.JobView) jobResponse {
	response := jobResponse{ID: view.ID.String(), Type: view.Type, Status: view.Status, CreateAt: model.MillisFromTime(view.CreatedAt), UpdateAt: model.MillisFromTime(view.UpdatedAt), AvailableAt: model.MillisFromTime(view.AvailableAt), PublicErrorCode: view.PublicErrorCode, AttemptCount: view.AttemptCount, MaximumAttempts: view.MaximumAttempts, Revision: view.Revision}
	if view.StartedAt.Valid {
		response.StartAt = model.MillisFromTime(view.StartedAt.Time)
	}
	if view.CompletedAt.Valid {
		response.CompleteAt = model.MillisFromTime(view.CompletedAt.Time)
	}
	if view.Progress != nil {
		response.Progress = &jobProgressResponse{Current: view.Progress.Current, Total: view.Progress.Total, Stage: view.Progress.Stage}
	}
	return response
}
func jobAttemptResponseFromApplication(view application.JobAttemptView) jobAttemptResponse {
	response := jobAttemptResponse{ID: view.ID.String(), Number: view.Number, Status: view.Status, StartAt: model.MillisFromTime(view.StartedAt), HeartbeatAt: model.MillisFromTime(view.HeartbeatAt), LeaseExpiresAt: model.MillisFromTime(view.LeaseExpiresAt), PublicErrorCode: view.PublicErrorCode}
	if view.CompletedAt.Valid {
		response.CompleteAt = model.MillisFromTime(view.CompletedAt.Time)
	}
	return response
}
