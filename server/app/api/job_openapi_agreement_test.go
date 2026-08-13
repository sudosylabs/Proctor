// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package api

import (
	"context"
	"reflect"
	"testing"

	application "github.com/sudosylabs/proctor/server/app"
	"github.com/sudosylabs/proctor/server/model"
)

type jobOperationsAPIFake struct{}

func (jobOperationsAPIFake) ListJobs(context.Context, application.Invocation, application.ListJobsQuery) (application.JobPage, error) {
	return application.JobPage{}, nil
}
func (jobOperationsAPIFake) GetJob(context.Context, application.Invocation, application.GetJobQuery) (application.JobView, error) {
	return application.JobView{}, nil
}
func (jobOperationsAPIFake) ListJobAttempts(context.Context, application.Invocation, application.ListJobAttemptsQuery) (application.JobAttemptPage, error) {
	return application.JobAttemptPage{}, nil
}
func (jobOperationsAPIFake) CancelJob(context.Context, application.Invocation, application.CancelJobCommand) (application.JobView, error) {
	return application.JobView{}, nil
}
func (jobOperationsAPIFake) RetryJob(context.Context, application.Invocation, application.RetryJobCommand) (application.JobView, error) {
	return application.JobView{}, nil
}

func TestJobOperationsOpenAPIAgreesWithRuntime(t *testing.T) {
	t.Parallel()
	suite := openAPIAgreementSuite{
		Operations: []openAPIAgreementOperation{
			{
				Key: "GET /api/v1/jobs", Auth: AuthPrincipalRequired,
				SuccessStatus: "200", SuccessRef: "#/components/responses/JobListOK", SuccessSchema: "JobListResponse",
				PublicErrorCodes: principalContractCodes("audit.unavailable", "job.query.invalid", "job.unavailable"),
			},
			{
				Key: "GET /api/v1/jobs/{job_id}", Auth: AuthPrincipalRequired,
				SuccessStatus: "200", SuccessRef: "#/components/responses/JobOK", SuccessSchema: "JobResponse",
				PublicErrorCodes: principalContractCodes("audit.unavailable", "resource.not_found", "job.unavailable"),
			},
			{
				Key: "GET /api/v1/jobs/{job_id}/attempts", Auth: AuthPrincipalRequired,
				SuccessStatus: "200", SuccessRef: "#/components/responses/JobAttemptListOK", SuccessSchema: "JobAttemptListResponse",
				PublicErrorCodes: principalContractCodes("audit.unavailable", "job.query.invalid", "resource.not_found", "job.unavailable"),
			},
			{
				Key: "POST /api/v1/jobs/{job_id}/cancel", Auth: AuthPrincipalRequired,
				SuccessStatus: "200", SuccessRef: "#/components/responses/JobOK", SuccessSchema: "JobResponse",
				PublicErrorCodes: principalMutationContractCodes("resource.not_found", "job.cancel.unsupported", "job.conflict", "job.unavailable"),
			},
			{
				Key: "POST /api/v1/jobs/{job_id}/retry", Auth: AuthPrincipalRequired,
				SuccessStatus: "200", SuccessRef: "#/components/responses/JobOK", SuccessSchema: "JobResponse",
				PublicErrorCodes: principalMutationContractCodes("resource.not_found", "job.retry.unsupported", "job.conflict", "job.unavailable"),
			},
		},
		Schemas: []openAPIAgreementSchema{
			{
				Name: "JobResponse", DTO: reflect.TypeOf(jobResponse{}),
				Required: []string{"id", "type", "status", "create_at", "update_at", "available_at", "attempt_count", "maximum_attempts", "revision"},
			},
			{
				Name: "JobAttemptResponse", DTO: reflect.TypeOf(jobAttemptResponse{}),
				Required: []string{"id", "number", "status", "start_at", "heartbeat_at", "lease_expires_at"},
			},
		},
	}
	runtimeAPI := newRoutingTestAPI(model.APIURLSuffix)
	if err := runtimeAPI.collectResources(model.APIURLSuffix, jobResource(jobOperationsAPIFake{})); err != nil {
		t.Fatal(err)
	}
	assertOpenAPIAgreement(t, suite, runtimeAPI.Routes())
}
