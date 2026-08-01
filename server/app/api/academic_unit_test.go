// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	application "github.com/sudosylabs/proctor/server/app"
	"github.com/sudosylabs/proctor/server/mlog"
	"github.com/sudosylabs/proctor/server/model"
)

type academicUnitHTTPApplication struct {
	Application
	principal model.Principal
	unit      *model.AcademicUnit
}

func (a *academicUnitHTTPApplication) AuthenticateAccess(
	context.Context, string,
) (*model.Principal, *model.AppError) {
	principal := a.principal
	return &principal, nil
}

func (a *academicUnitHTTPApplication) AuthenticateBearer(
	context.Context, string,
) (*model.Principal, *model.AppError) {
	principal := a.principal
	return &principal, nil
}

func (a *academicUnitHTTPApplication) GetAcademicUnit(
	_ context.Context,
	invocation application.Invocation,
	query application.GetAcademicUnitQuery,
) (*model.AcademicUnit, error) {
	if invocation.Principal().UserId != a.principal.UserId || query.ID != a.unit.Id {
		return nil, application.NewError("request.invalid")
	}
	return a.unit, nil
}

func (a *academicUnitHTTPApplication) ListAcademicUnits(
	context.Context,
	application.Invocation,
	application.ListAcademicUnitsQuery,
) ([]*model.AcademicUnit, error) {
	return nil, nil
}

func (a *academicUnitHTTPApplication) SearchAcademicUnits(
	context.Context,
	application.Invocation,
	application.SearchAcademicUnitsQuery,
) ([]*model.AcademicUnit, error) {
	return nil, nil
}

type academicUnitHTTPHealth struct{}

func (academicUnitHTTPHealth) Live() bool  { return true }
func (academicUnitHTTPHealth) Ready() bool { return true }

func TestAcademicUnitResponsePreservesExistingWireShape(t *testing.T) {
	t.Parallel()

	unit := &model.AcademicUnit{
		Id: model.NewId(), CreateAt: 10, UpdateAt: 20, DeleteAt: 0,
		InstitutionId: model.NewId(), ParentId: model.NewId(),
		Name: "computing", DisplayName: "Computing", Description: "School",
	}
	want, err := json.Marshal(unit)
	if err != nil {
		t.Fatal(err)
	}
	got, err := json.Marshal(academicUnitResponseFromModel(unit))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(want) {
		t.Fatalf("response = %s, want existing wire shape %s", got, want)
	}
}

func TestAcademicUnitCollectionMappingReturnsJSONArrayForNoResults(t *testing.T) {
	t.Parallel()

	encoded, err := json.Marshal(academicUnitResponsesFromModels(nil))
	if err != nil {
		t.Fatal(err)
	}
	if string(encoded) != "[]" {
		t.Fatalf("empty collection = %s, want []", encoded)
	}
}

func TestAcademicUnitHTTPReadMapsDTOWithoutPermissionPreflight(t *testing.T) {
	t.Parallel()

	logger, err := mlog.New()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = logger.Shutdown() })
	unit := &model.AcademicUnit{
		Id: model.NewId(), CreateAt: 10, UpdateAt: 20,
		InstitutionId: model.NewId(), Name: "computing", DisplayName: "Computing",
	}
	application := &academicUnitHTTPApplication{
		principal: model.Principal{
			UserId: model.NewId(), SessionId: model.NewId(), CredentialId: model.NewId(),
			CredentialType:         model.CredentialSessionAccess,
			AuthenticationMethod:   "password",
			AuthenticationStrength: model.AuthenticationSingleFactor,
			ClientType:             model.SessionClientCLI, AuthenticatedAt: time.Now().UnixMilli(),
		},
		unit: unit,
	}
	httpAPI, err := New(Options{
		Logger: logger, Health: academicUnitHTTPHealth{}, Application: application,
		AcademicUnits: application,
		BuildInfo:     BuildInfo{Version: "test"}, PublicURL: "http://localhost:8065",
		MaxBodyBytes: 1 << 20, RecentAuthenticationTTL: time.Minute, NodeID: "node-a",
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = httpAPI.Close() })

	request := httptest.NewRequest(
		http.MethodGet, "/api/v1/academic-units/"+unit.Id, nil,
	)
	request.Header.Set("Authorization", "Bearer test-credential")
	response := httptest.NewRecorder()
	httpAPI.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", response.Code, response.Body.String())
	}
	want, err := json.Marshal(academicUnitResponseFromModel(unit))
	if err != nil {
		t.Fatal(err)
	}
	var got academicUnitResponse
	if err := json.Unmarshal(response.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	if string(encoded) != string(want) {
		t.Fatalf("response = %s, want %s", encoded, want)
	}
}
