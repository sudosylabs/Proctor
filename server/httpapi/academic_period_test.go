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

	application "github.com/sudosylabs/proctor/server/app"
	"github.com/sudosylabs/proctor/server/model"
)

type academicPeriodHTTPApplication struct {
	result        *model.AcademicPeriod
	list          []*model.AcademicPeriod
	createCommand application.CreateAcademicPeriodCommand
}

func (a *academicPeriodHTTPApplication) GetAcademicPeriod(context.Context, application.Invocation, application.GetAcademicPeriodQuery) (*model.AcademicPeriod, error) {
	return a.result, nil
}
func (a *academicPeriodHTTPApplication) ListAcademicPeriods(context.Context, application.Invocation, application.ListAcademicPeriodsQuery) ([]*model.AcademicPeriod, error) {
	return a.list, nil
}
func (a *academicPeriodHTTPApplication) CreateAcademicPeriod(_ context.Context, _ application.Invocation, command application.CreateAcademicPeriodCommand) (*model.AcademicPeriod, error) {
	a.createCommand = command
	return a.result, nil
}
func (a *academicPeriodHTTPApplication) UpdateAcademicPeriod(context.Context, application.Invocation, application.UpdateAcademicPeriodCommand) (*model.AcademicPeriod, error) {
	return a.result, nil
}
func (*academicPeriodHTTPApplication) ArchiveAcademicPeriod(context.Context, application.Invocation, application.ArchiveAcademicPeriodCommand) error {
	return nil
}

func TestAcademicPeriodHTTPMapsDTOAndIgnoresServerOwnedCreateFields(t *testing.T) {
	t.Parallel()
	logger, _ := newTestLogger(t)
	principal := model.Principal{UserID: model.NewUserID(), SessionID: model.NewSessionID(), CredentialID: model.PrincipalCredentialID(model.NewId()), CredentialType: model.CredentialSessionAccess, AuthenticationMethod: "password", AuthenticationStrength: model.AuthenticationSingleFactor, ClientType: model.SessionClientCLI, AuthenticatedAt: time.Now()}
	period := &model.AcademicPeriod{ID: model.AcademicPeriodID(model.NewId()), Owner: model.NewAcademicUnitAcademicPeriodOwner(model.NewAcademicUnitID()), Name: "2026-2027", DisplayName: "2026-2027", StartsAt: model.TimeFromMillis(100), EndsAt: model.TimeFromMillis(200)}
	periods := &academicPeriodHTTPApplication{result: period}
	httpAPI := newFocusedResourceAPI(
		t, logger, classRouteAuthenticator{principal: principal}, academicPeriodResource(periods),
	)
	request := httptest.NewRequest(http.MethodPost, "/api/v1/academic-periods", strings.NewReader(`{"id":"ignored","owner_type":"academic_unit","owner_id":"`+period.Owner.ID()+`","create_at":12,"name":"2026-2027","display_name":"2026-2027","start_at":100,"end_at":200}`))
	request.Header.Set("Authorization", "Bearer credential")
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	httpAPI.ServeHTTP(response, request)
	if response.Code != http.StatusCreated {
		t.Fatalf("status = %d: %s", response.Code, response.Body.String())
	}
	if periods.createCommand.OwnerType != string(model.ResourceAcademicUnit) || periods.createCommand.OwnerID != period.Owner.ID() || periods.createCommand.Name != period.Name || periods.createCommand.StartAt != model.MillisFromTime(period.StartsAt) || periods.createCommand.EndAt != model.MillisFromTime(period.EndsAt) {
		t.Fatalf("create command = %#v", periods.createCommand)
	}
	var body academicPeriodResponse
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.ID != period.ID.String() || body.OwnerType != string(model.ResourceAcademicUnit) || body.OwnerID != period.Owner.ID() {
		t.Fatalf("response = %#v", body)
	}
}

func TestAcademicPeriodResourceRejectsInvalidQueryLimit(t *testing.T) {
	t.Parallel()

	logger, _ := newTestLogger(t)
	principal := model.Principal{
		UserID: model.NewUserID(), SessionID: model.NewSessionID(),
		CredentialID:           model.PrincipalCredentialID(model.NewId()),
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
	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d: %s", response.Code, response.Body.String())
	}
	var problem Problem
	if err := json.Unmarshal(response.Body.Bytes(), &problem); err != nil {
		t.Fatal(err)
	}
	if problem.Code != "request.invalid" {
		t.Fatalf("problem code = %q, want request.invalid", problem.Code)
	}
}

func TestAcademicPeriodPatchOptionalWireStates(t *testing.T) {
	t.Parallel()
	var body updateAcademicPeriodRequest
	if err := json.Unmarshal([]byte(`{"start_at":null,"end_at":0}`), &body); err != nil {
		t.Fatal(err)
	}
	if body.StartAt.ValuePointer() != nil {
		t.Fatal("explicit null must preserve v1 no-op semantics")
	}
	end := body.EndAt.ValuePointer()
	if end == nil || *end != 0 {
		t.Fatalf("end_at = %#v", end)
	}
}
