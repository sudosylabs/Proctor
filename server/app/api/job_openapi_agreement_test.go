// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package api

import (
	"context"
	"encoding/json"
	"net/http"
	"reflect"
	"sort"
	"strconv"
	"strings"
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
	document := readOpenAPIDocument(t)
	runtimeAPI := newRoutingTestAPI(model.APIURLSuffix)
	if err := runtimeAPI.InitJobs(jobOperationsAPIFake{}); err != nil {
		t.Fatal(err)
	}
	runtimeOperations := map[string]AuthRequirement{}
	for _, route := range runtimeAPI.Routes() {
		if strings.HasPrefix(route.Path, model.APIURLSuffix+"/jobs") {
			path := strings.ReplaceAll(route.Path, "{job_id:"+canonicalIDRoutePattern()+"}", "{job_id}")
			runtimeOperations[route.Method+" "+path] = route.Auth
		}
	}
	expected := map[string]openAPIOperationContract{
		"GET /api/v1/jobs":                   {successStatus: "200", successRef: "#/components/responses/JobListOK", successSchema: "JobListResponse", errorCodes: principalContractCodes("audit.unavailable", "job.query.invalid", "job.unavailable")},
		"GET /api/v1/jobs/{job_id}":          {successStatus: "200", successRef: "#/components/responses/JobOK", successSchema: "JobResponse", errorCodes: principalContractCodes("audit.unavailable", "resource.not_found", "job.unavailable")},
		"GET /api/v1/jobs/{job_id}/attempts": {successStatus: "200", successRef: "#/components/responses/JobAttemptListOK", successSchema: "JobAttemptListResponse", errorCodes: principalContractCodes("audit.unavailable", "job.query.invalid", "resource.not_found", "job.unavailable")},
		"POST /api/v1/jobs/{job_id}/cancel":  {successStatus: "200", successRef: "#/components/responses/JobOK", successSchema: "JobResponse", errorCodes: principalMutationContractCodes("resource.not_found", "job.cancel.unsupported", "job.conflict", "job.unavailable")},
		"POST /api/v1/jobs/{job_id}/retry":   {successStatus: "200", successRef: "#/components/responses/JobOK", successSchema: "JobResponse", errorCodes: principalMutationContractCodes("resource.not_found", "job.retry.unsupported", "job.conflict", "job.unavailable")},
	}
	documented := map[string]AuthRequirement{}
	statuses := ApplicationErrorStatuses()
	statuses["authentication.credential_ambiguous"] = http.StatusBadRequest
	statuses["authentication.csrf.invalid"] = http.StatusForbidden
	for path, item := range document.Paths {
		if !strings.HasPrefix(path, "/api/v1/jobs") {
			continue
		}
		for method, raw := range item {
			upper := strings.ToUpper(method)
			if !isHTTPMethod(upper) {
				continue
			}
			key := upper + " " + path
			var operation openAPIOperation
			if err := json.Unmarshal(raw, &operation); err != nil {
				t.Fatal(err)
			}
			documented[key] = operation.Auth
			contract, ok := expected[key]
			if !ok {
				t.Fatalf("unexpected operation %s", key)
			}
			assertPrincipalSecurity(t, key, upper, operation.Security)
			assertOpenAPISuccessResponse(t, document, key, contract)
			got, want := append([]string(nil), operation.ErrorCodes...), append([]string(nil), contract.errorCodes...)
			sort.Strings(got)
			sort.Strings(want)
			if !reflect.DeepEqual(got, want) {
				t.Errorf("%s errors=%v want=%v", key, got, want)
			}
			for _, code := range operation.ErrorCodes {
				assertOpenAPIProblemResponse(t, document, key, statuses[code], operation.Responses[strconv.Itoa(statuses[code])])
			}
		}
	}
	if !reflect.DeepEqual(documented, runtimeOperations) {
		t.Fatalf("documented=%v runtime=%v", documented, runtimeOperations)
	}
	assertOpenAPISchemaMatchesDTO(t, document, "JobResponse", reflect.TypeOf(jobResponse{}), []string{"id", "type", "status", "create_at", "update_at", "available_at", "attempt_count", "maximum_attempts", "revision"})
	assertOpenAPISchemaMatchesDTO(t, document, "JobAttemptResponse", reflect.TypeOf(jobAttemptResponse{}), []string{"id", "number", "status", "start_at", "heartbeat_at", "lease_expires_at"})
}
