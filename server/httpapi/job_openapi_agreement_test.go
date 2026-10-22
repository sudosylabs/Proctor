// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package httpapi

import (
	"context"
	"reflect"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/server/app"
	"github.com/sudosylabs/proctor/server/app/invocation"
	"github.com/sudosylabs/proctor/server/app/job"
	"github.com/sudosylabs/proctor/server/model"
)

type jobOperationsAPIFake struct{}

func (jobOperationsAPIFake) ListJobs(context.Context, invocation.Invocation, app.ListJobsQuery) (job.Page, error) {
	return job.Page{}, nil
}
func (jobOperationsAPIFake) GetJob(context.Context, invocation.Invocation, app.GetJobQuery) (job.View, error) {
	return job.View{}, nil
}
func (jobOperationsAPIFake) ListJobAttempts(context.Context, invocation.Invocation, app.ListJobAttemptsQuery) (job.AttemptPage, error) {
	return job.AttemptPage{}, nil
}
func (jobOperationsAPIFake) CancelJob(context.Context, invocation.Invocation, app.CancelJobCommand) (job.View, error) {
	return job.View{}, nil
}
func (jobOperationsAPIFake) RetryJob(context.Context, invocation.Invocation, app.RetryJobCommand) (job.View, error) {
	return job.View{}, nil
}

func TestJobOperationsOpenAPIAgreesWithRuntime(t *testing.T) {
	t.Parallel()
	suite := openAPIAgreementSuite{
		Operations: []openAPIAgreementOperation{
			{
				Key: "GET /api/v1/jobs", Auth: AuthPrincipalRequired,
				SuccessStatus: "200", SuccessRef: "#/components/responses/JobListOK", SuccessSchema: "JobListResponse",
				PublicErrorCodes: principalContractCodes("audit.unavailable", "job.query.invalid", "job.unavailable", "request.invalid"),
			},
			{
				Key: "GET /api/v1/jobs/{job_id}", Auth: AuthPrincipalRequired,
				SuccessStatus: "200", SuccessRef: "#/components/responses/JobOK", SuccessSchema: "JobResponse",
				PublicErrorCodes: principalContractCodes("audit.unavailable", "resource.not_found", "job.unavailable"),
			},
			{
				Key: "GET /api/v1/jobs/{job_id}/attempts", Auth: AuthPrincipalRequired,
				SuccessStatus: "200", SuccessRef: "#/components/responses/JobAttemptListOK", SuccessSchema: "JobAttemptListResponse",
				PublicErrorCodes: principalContractCodes("audit.unavailable", "job.query.invalid", "request.invalid", "resource.not_found", "job.unavailable"),
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
				Required: []string{"id", "type", "status", "created_at", "updated_at", "available_at", "attempt_count", "maximum_attempts", "revision"},
			},
			{
				Name: "JobAttemptResponse", DTO: reflect.TypeOf(jobAttemptResponse{}),
				Required: []string{"id", "number", "status", "started_at", "heartbeat_at", "lease_expires_at"},
			},
		},
	}
	runtimeAPI := newRoutingTestAPI(model.APIURLSuffix)
	require.NoError(t, runtimeAPI.collectResources(model.APIURLSuffix, jobResource(jobOperationsAPIFake{})))
	assertOpenAPIAgreement(t, suite, runtimeAPI.Routes())
}
