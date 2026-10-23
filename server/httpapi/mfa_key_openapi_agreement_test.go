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
	"net/http"
	"reflect"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/server/app"
	"github.com/sudosylabs/proctor/server/app/apperror"
	"github.com/sudosylabs/proctor/server/app/invocation"
	"github.com/sudosylabs/proctor/server/model"
)

type mfaKeyAPIFake struct {
	state   app.MFAKeyStateView
	started app.StartMFARekeyCommand
	status  app.GetMFARekeyStatusQuery
	err     error
}

func (f *mfaKeyAPIFake) GetMFAKeyState(context.Context, invocation.Invocation) (app.MFAKeyStateView, error) {
	return f.state, f.err
}

func (f *mfaKeyAPIFake) StartMFARekey(_ context.Context, _ invocation.Invocation, command app.StartMFARekeyCommand) (app.MFARekeyView, error) {
	f.started = command
	return app.MFARekeyView{JobID: model.NewJobID(), PrimaryKeyID: "0123456789abcdef0123456789abcdef", RetiringKeyID: command.RetiringKeyID}, f.err
}

func (f *mfaKeyAPIFake) GetMFARekeyStatus(_ context.Context, _ invocation.Invocation, query app.GetMFARekeyStatusQuery) (app.MFARekeyStatusView, error) {
	f.status = query
	return app.MFARekeyStatusView{JobID: query.JobID}, f.err
}

func TestMFAKeyOpenAPIAgreesWithRuntime(t *testing.T) {
	t.Parallel()
	suite := openAPIAgreementSuite{
		Operations: []openAPIAgreementOperation{
			{Key: "GET /api/v1/mfa/keys", Auth: AuthStrongRecentSessionRequired, SuccessStatus: "200", SuccessRef: "#/components/responses/MFAKeyStateOK", SuccessSchema: "MFAKeyStateResponse", PublicErrorCodes: principalContractCodes("authentication.strong_required", "authentication.reauthentication_required", "authentication.mfa.unavailable")},
			{Key: "POST /api/v1/mfa/rekey", Auth: AuthStrongRecentSessionRequired, RequestBodyRef: "#/components/requestBodies/StartMFARekey", RequestSchema: "MFARekeyRequest", SuccessStatus: "202", SuccessRef: "#/components/responses/MFARekeyAccepted", SuccessSchema: "MFARekeyResponse", PublicErrorCodes: principalMutationContractCodes("authentication.strong_required", "authentication.reauthentication_required", "request.invalid", "authentication.mfa.rekey.invalid", "authentication.mfa.rekey.conflict", "authentication.mfa.unavailable")},
			{Key: "GET /api/v1/mfa/rekey/{job_id}", Auth: AuthStrongRecentSessionRequired, SuccessStatus: "200", SuccessRef: "#/components/responses/MFARekeyStatusOK", SuccessSchema: "MFARekeyStatusResponse", PublicErrorCodes: principalContractCodes("authentication.strong_required", "authentication.reauthentication_required", "request.invalid", "resource.not_found", "authentication.mfa.unavailable")},
		},
		Schemas: []openAPIAgreementSchema{
			{Name: "MFARekeyRequest", DTO: reflect.TypeOf(mfaRekeyRequest{}), Required: []string{"retiring_key_id"}},
			{Name: "MFAKeyStateResponse", DTO: reflect.TypeOf(mfaKeyStateResponse{}), Required: []string{"primary_key_id", "active"}},
			{Name: "MFASecretKeyUsage", DTO: reflect.TypeOf(mfaSecretKeyUsageResponse{}), Required: []string{"key_id", "credentials", "configured"}},
			{Name: "MFARekeyResponse", DTO: reflect.TypeOf(mfaRekeyResponse{}), Required: []string{"job_id", "primary_key_id", "retiring_key_id", "created_at"}},
			{Name: "MFARekeyProgress", DTO: reflect.TypeOf(mfaRekeyProgressResponse{}), Required: []string{"current", "total", "stage"}},
			{Name: "MFARekeyProof", DTO: reflect.TypeOf(mfaRekeyProofResponse{}), Required: []string{"non_primary_references", "retiring_references", "retirement_safe"}},
			{Name: "MFARekeyStatusResponse", DTO: reflect.TypeOf(mfaRekeyStatusResponse{}), Required: []string{"job_id", "status", "primary_key_id", "retiring_key_id", "created_at", "updated_at", "attempt_count", "maximum_attempts", "processed", "resealed"}},
		},
	}
	runtimeAPI := newRoutingTestAPI(model.APIURLSuffix)
	require.NoError(t, runtimeAPI.collectResources(model.APIURLSuffix, mfaKeyResource(&mfaKeyAPIFake{})))
	assertOpenAPIAgreement(t, suite, runtimeAPI.Routes())
}

func TestMFARekeyAcceptsOnlyAKeyIdentity(t *testing.T) {
	t.Parallel()
	fake := &mfaKeyAPIFake{}
	module := mfaKeyResourceModule{application: fake}
	request, _ := http.NewRequest(http.MethodPost, "/api/v1/mfa/rekey", bytes.NewBufferString(`{"retiring_key_id":"11111111111111111111111111111111"}`))
	request.Header.Set("Content-Type", "application/json")
	result, err := module.startRekey(operationRequest{request: request})
	require.NoError(t, err)
	require.Equal(t, http.StatusAccepted, result.status)
	require.Equal(t, app.StartMFARekeyCommand{RetiringKeyID: "11111111111111111111111111111111"}, fake.started)

	fake.started = app.StartMFARekeyCommand{}
	secretBearing, _ := http.NewRequest(http.MethodPost, "/api/v1/mfa/rekey",
		bytes.NewBufferString(`{"retiring_key_id":"11111111111111111111111111111111","encryption_key":"secret"}`))
	secretBearing.Header.Set("Content-Type", "application/json")
	_, err = module.startRekey(operationRequest{request: secretBearing})
	require.True(t, apperror.Is(err, "request.invalid"), "a body carrying key material: error = %v", err)
	require.Equal(t, app.StartMFARekeyCommand{}, fake.started, "the use case never saw the secret-bearing body")
}

func TestMFAKeyStateProjectsIdentitiesAndCountsOnly(t *testing.T) {
	t.Parallel()
	jobID := model.NewJobID()
	fake := &mfaKeyAPIFake{state: app.MFAKeyStateView{PrimaryKeyID: "0123456789abcdef0123456789abcdef",
		RequiredPrimaryKeyID: "0123456789abcdef0123456789abcdef", ActiveRekeyJobID: jobID,
		Active: []app.MFASecretKeyUsageView{{KeyID: "11111111111111111111111111111111", Credentials: 4}}}}
	result, err := mfaKeyResourceModule{application: fake}.getKeyState(operationRequest{request: &http.Request{}})
	require.NoError(t, err)
	require.Equal(t, mfaKeyStateResponse{PrimaryKeyID: "0123456789abcdef0123456789abcdef",
		RequiredPrimaryKeyID: "0123456789abcdef0123456789abcdef", ActiveRekeyJobID: jobID.String(),
		Active: []mfaSecretKeyUsageResponse{{KeyID: "11111111111111111111111111111111", Credentials: 4}}}, result.body)
}
