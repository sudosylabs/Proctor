// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package api

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/gorilla/mux"
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

func (a *API) InitJobs(provided ...JobOperationsApplication) error {
	var operations JobOperationsApplication
	if len(provided) == 1 {
		operations = provided[0]
	} else if len(provided) == 0 {
		operations, _ = a.application.(JobOperationsApplication)
	}
	if operations == nil && a.application != nil {
		return errors.New("job operations application is required")
	}
	jobs := a.subrouter(a.BaseRoutes.APIRoot, "/jobs")
	job := a.subrouter(jobs, "/{job_id:"+canonicalIDRoutePattern()+"}")
	if err := a.registerLegacyRoute(jobs, "", http.MethodGet, a.APIPrincipalRequired(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { a.listJobs(w, r, operations) }))); err != nil {
		return err
	}
	if err := a.registerLegacyRoute(job, "", http.MethodGet, a.APIPrincipalRequired(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { a.getJob(w, r, operations) }))); err != nil {
		return err
	}
	if err := a.registerLegacyRoute(job, "/attempts", http.MethodGet, a.APIPrincipalRequired(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { a.listJobAttempts(w, r, operations) }))); err != nil {
		return err
	}
	if err := a.registerLegacyRoute(job, "/cancel", http.MethodPost, a.APIPrincipalRequired(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { a.cancelJob(w, r, operations) }))); err != nil {
		return err
	}
	return a.registerLegacyRoute(job, "/retry", http.MethodPost, a.APIPrincipalRequired(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { a.retryJob(w, r, operations) })))
}

func (a *API) listJobs(w http.ResponseWriter, r *http.Request, operations JobOperationsApplication) {
	invocation, ok := jobInvocation(w, r)
	if !ok {
		return
	}
	query, err := listJobsQuery(r)
	if err != nil {
		writeApplicationError(w, r, a.logger, application.NewError("job.query.invalid"))
		return
	}
	page, appErr := operations.ListJobs(r.Context(), invocation, query)
	if appErr != nil {
		writeApplicationError(w, r, a.logger, appErr)
		return
	}
	response := jobListResponse{Items: make([]jobResponse, 0, len(page.Items))}
	for _, item := range page.Items {
		response.Items = append(response.Items, jobResponseFromApplication(item))
	}
	if len(page.Items) == query.Limit {
		last := page.Items[len(page.Items)-1]
		response.NextCursor = encodeJobCursor(jobCursor{CreatedAt: model.MillisFromTime(last.CreatedAt), ID: last.ID.String()})
	}
	writeJSON(w, http.StatusOK, response)
}
func (a *API) getJob(w http.ResponseWriter, r *http.Request, operations JobOperationsApplication) {
	invocation, ok := jobInvocation(w, r)
	if !ok {
		return
	}
	view, err := operations.GetJob(r.Context(), invocation, application.GetJobQuery{ID: model.JobID(mux.Vars(r)["job_id"])})
	if err != nil {
		writeApplicationError(w, r, a.logger, err)
		return
	}
	writeJSON(w, http.StatusOK, jobResponseFromApplication(view))
}
func (a *API) listJobAttempts(w http.ResponseWriter, r *http.Request, operations JobOperationsApplication) {
	invocation, ok := jobInvocation(w, r)
	if !ok {
		return
	}
	limit, before, err := attemptPagination(r)
	if err != nil {
		writeApplicationError(w, r, a.logger, application.NewError("job.query.invalid"))
		return
	}
	page, appErr := operations.ListJobAttempts(r.Context(), invocation, application.ListJobAttemptsQuery{JobID: model.JobID(mux.Vars(r)["job_id"]), BeforeNumber: before, Limit: limit})
	if appErr != nil {
		writeApplicationError(w, r, a.logger, appErr)
		return
	}
	response := jobAttemptListResponse{Items: make([]jobAttemptResponse, 0, len(page.Items))}
	for _, item := range page.Items {
		response.Items = append(response.Items, jobAttemptResponseFromApplication(item))
	}
	if len(page.Items) == limit {
		response.NextCursor = encodeJobAttemptCursor(page.Items[len(page.Items)-1].Number)
	}
	writeJSON(w, http.StatusOK, response)
}
func (a *API) cancelJob(w http.ResponseWriter, r *http.Request, operations JobOperationsApplication) {
	invocation, ok := jobInvocation(w, r)
	if !ok {
		return
	}
	view, err := operations.CancelJob(r.Context(), invocation, application.CancelJobCommand{ID: model.JobID(mux.Vars(r)["job_id"])})
	if err != nil {
		writeApplicationError(w, r, a.logger, err)
		return
	}
	writeJSON(w, http.StatusOK, jobResponseFromApplication(view))
}
func (a *API) retryJob(w http.ResponseWriter, r *http.Request, operations JobOperationsApplication) {
	invocation, ok := jobInvocation(w, r)
	if !ok {
		return
	}
	view, err := operations.RetryJob(r.Context(), invocation, application.RetryJobCommand{ID: model.JobID(mux.Vars(r)["job_id"])})
	if err != nil {
		writeApplicationError(w, r, a.logger, err)
		return
	}
	writeJSON(w, http.StatusOK, jobResponseFromApplication(view))
}

func jobInvocation(w http.ResponseWriter, r *http.Request) (application.Invocation, bool) {
	principal, ok := Principal(r.Context())
	if !ok {
		WriteError(w, r, authenticationRequiredError())
		return application.Invocation{}, false
	}
	return application.NewInvocation(principal, RequestMetadata(r.Context())), true
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
