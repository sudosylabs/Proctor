// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/server/app"
	"github.com/sudosylabs/proctor/server/app/invocation"
	"github.com/sudosylabs/proctor/server/model"
)

type academicPeriodHTTPApplication struct {
	updateCommand  app.UpdateAcademicPeriodCommand
	archiveCommand app.ArchiveAcademicPeriodCommand
	result         *model.AcademicPeriod
	list           []*model.AcademicPeriod
	createCommand  app.CreateAcademicPeriodCommand
}

func (a *academicPeriodHTTPApplication) GetAcademicPeriod(context.Context, invocation.Invocation, app.GetAcademicPeriodQuery) (*model.AcademicPeriod, error) {
	return a.result, nil
}
func (a *academicPeriodHTTPApplication) ListAcademicPeriods(context.Context, invocation.Invocation, app.ListAcademicPeriodsQuery) (app.Page[*model.AcademicPeriod], error) {
	return app.Page[*model.AcademicPeriod]{Items: a.list}, nil
}
func (a *academicPeriodHTTPApplication) CreateAcademicPeriod(_ context.Context, _ invocation.Invocation, command app.CreateAcademicPeriodCommand) (*model.AcademicPeriod, error) {
	a.createCommand = command
	return a.result, nil
}
func (a *academicPeriodHTTPApplication) UpdateAcademicPeriod(_ context.Context, _ invocation.Invocation, command app.UpdateAcademicPeriodCommand) (*model.AcademicPeriod, error) {
	a.updateCommand = command
	return a.result, nil
}
func (a *academicPeriodHTTPApplication) ArchiveAcademicPeriod(_ context.Context, _ invocation.Invocation, command app.ArchiveAcademicPeriodCommand) error {
	a.archiveCommand = command
	return nil
}

func TestAcademicPeriodHTTPMapsDTOAndNormalizesRFC3339Bounds(t *testing.T) {
	t.Parallel()
	logger, _ := newTestLogger(t)
	principal := model.Principal{UserID: model.NewUserID(), SessionID: model.NewSessionID(), CredentialID: model.PrincipalCredentialID(model.NewID()), CredentialType: model.CredentialSessionAccess, AuthenticationMethod: "password", AuthenticationStrength: model.AuthenticationSingleFactor, ClientType: model.SessionClientCLI, AuthenticatedAt: time.Now()}
	period := &model.AcademicPeriod{ID: model.AcademicPeriodID(model.NewID()), Owner: model.NewAcademicUnitAcademicPeriodOwner(model.NewAcademicUnitID()), Name: "2026-2027", DisplayName: "2026-2027", StartsAt: model.TimeFromMillis(100), EndsAt: model.TimeFromMillis(200)}
	periods := &academicPeriodHTTPApplication{result: period}
	httpAPI := newFocusedResourceAPI(
		t, logger, classRouteAuthenticator{principal: principal}, academicPeriodResource(periods),
	)
	request := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/api/v1/academic-periods", strings.NewReader(`{"owner_type":"academic_unit","owner_id":"`+period.Owner.ID()+`","name":"2026-2027","display_name":"2026-2027","start_at":"1970-01-01T00:00:00.1Z","end_at":"1970-01-01T01:00:00.2+01:00"}`))
	request.Header.Set("Authorization", "Bearer credential")
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	httpAPI.ServeHTTP(response, request)
	require.Equal(t, http.StatusCreated, response.Code, "status = %d: %s", response.Code, response.Body.String())
	require.Equal(t, string(model.ResourceAcademicUnit), periods.createCommand.OwnerType)
	require.Equal(t, period.Owner.ID(), periods.createCommand.OwnerID)
	require.Equal(t, period.Name, periods.createCommand.Name)
	require.Equal(t, model.MillisFromTime(period.StartsAt), periods.createCommand.StartAt)
	require.Equal(t, model.MillisFromTime(period.EndsAt), periods.createCommand.EndAt)
	var body academicPeriodResponse
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &body))
	require.Equal(t, period.ID.String(), body.ID)
	require.Equal(t, string(model.ResourceAcademicUnit), body.OwnerType)
	require.Equal(t, period.Owner.ID(), body.OwnerID)
}

func TestAcademicPeriodResourceRejectsInvalidQueryLimit(t *testing.T) {
	t.Parallel()

	logger, _ := newTestLogger(t)
	principal := model.Principal{
		UserID: model.NewUserID(), SessionID: model.NewSessionID(),
		CredentialID:           model.PrincipalCredentialID(model.NewID()),
		CredentialType:         model.CredentialSessionAccess,
		AuthenticationMethod:   "password",
		AuthenticationStrength: model.AuthenticationSingleFactor,
		ClientType:             model.SessionClientCLI,
		AuthenticatedAt:        time.Now(),
	}
	periods := &academicPeriodHTTPApplication{}
	httpAPI := newFocusedResourceAPI(
		t, logger, classRouteAuthenticator{principal: principal}, academicPeriodResource(periods),
	)
	request := httptest.NewRequest(
		http.MethodGet, "/api/v1/academic-periods?limit=0", nil,
	)
	request.Header.Set("Authorization", "Bearer credential")
	response := httptest.NewRecorder()
	httpAPI.ServeHTTP(response, request)
	require.Equal(t, http.StatusBadRequest, response.Code, "status = %d: %s", response.Code, response.Body.String())
	var problem Problem
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &problem))
	require.Equal(t, "request.invalid", problem.Code, "problem code")
}

func TestAcademicPeriodPatchOptionalWireStates(t *testing.T) {
	t.Parallel()
	var body updateAcademicPeriodRequest
	require.NoError(t, json.Unmarshal([]byte(`{"start_at":null,"end_at":"2026-01-02T03:04:05Z"}`), &body))
	require.Nil(t, body.StartAt.ValuePointer(), "explicit null must preserve v1 no-op semantics")
	end := body.EndAt.ValuePointer()
	require.NotNil(t, end)
	require.Equal(t, "2026-01-02T03:04:05Z", *end)
}
