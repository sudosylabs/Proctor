// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package api

import (
	"context"
	"net/http/httptest"
	"testing"

	application "github.com/sudosylabs/proctor/server/app"
	"github.com/sudosylabs/proctor/server/model"
)

type academicUnitMemberHTTPApplication struct {
	result        *model.AcademicUnitMember
	values        []*model.AcademicUnitMember
	createCommand application.CreateAcademicUnitMemberCommand
}

func TestAcademicUnitMemberHistoryQueryIncludesEndedMemberships(t *testing.T) {
	t.Parallel()

	request := httptest.NewRequest("GET", "/api/v1/academic-units/unit/members?active_at=123&history=true", nil)
	recorder := httptest.NewRecorder()
	activeAt, ok := queryActiveAt(recorder, request)
	if !ok {
		t.Fatalf("query rejected: status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if activeAt != 0 {
		t.Fatalf("active at = %d, want 0 for complete history", activeAt)
	}
}

func TestAcademicUnitMemberHistoryQueryRejectsInvalidBoolean(t *testing.T) {
	t.Parallel()

	request := httptest.NewRequest("GET", "/api/v1/academic-units/unit/members?history=invalid", nil)
	recorder := httptest.NewRecorder()
	if _, ok := queryActiveAt(recorder, request); ok {
		t.Fatal("invalid history query was accepted")
	}
	if recorder.Code != 400 {
		t.Fatalf("status = %d, want 400", recorder.Code)
	}
}

func TestAcademicUnitMemberFalseHistoryQueryUsesActiveAt(t *testing.T) {
	t.Parallel()

	request := httptest.NewRequest("GET", "/api/v1/academic-units/unit/members?history=false&active_at=123", nil)
	recorder := httptest.NewRecorder()
	activeAt, ok := queryActiveAt(recorder, request)
	if !ok || activeAt != 123 {
		t.Fatalf("active at = %d, ok = %v; want 123, true", activeAt, ok)
	}
}

func (a *academicUnitMemberHTTPApplication) ListAcademicUnitMembers(context.Context, application.Invocation, application.ListAcademicUnitMembersQuery) ([]*model.AcademicUnitMember, error) {
	return a.values, nil
}
func (a *academicUnitMemberHTTPApplication) CreateAcademicUnitMember(_ context.Context, _ application.Invocation, command application.CreateAcademicUnitMemberCommand) (*model.AcademicUnitMember, error) {
	a.createCommand = command
	return a.result, nil
}
func (a *academicUnitMemberHTTPApplication) EndAcademicUnitMember(context.Context, application.Invocation, application.EndAcademicUnitMemberCommand) (*model.AcademicUnitMember, error) {
	return a.result, nil
}
