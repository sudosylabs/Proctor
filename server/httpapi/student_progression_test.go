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
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	application "github.com/sudosylabs/proctor/server/app"
	"github.com/sudosylabs/proctor/server/model"
)

type studentProgressionApplicationFake struct {
	view    application.OnboardingImportView
	rows    []application.OnboardingImportRowView
	command application.DryRunStudentProgressionCommand
	getID   string
}

func (f *studentProgressionApplicationFake) DryRunStudentProgression(_ context.Context, _ application.Invocation, command application.DryRunStudentProgressionCommand) (application.OnboardingImportView, error) {
	f.command = command
	return f.view, nil
}
func (f *studentProgressionApplicationFake) GetStudentProgression(_ context.Context, _ application.Invocation, id string) (application.OnboardingImportView, []application.OnboardingImportRowView, error) {
	f.getID = id
	return f.view, f.rows, nil
}
func (f *studentProgressionApplicationFake) CommitStudentProgression(context.Context, application.Invocation, application.CommitStudentProgressionCommand) (application.OnboardingImportView, error) {
	return f.view, nil
}
func (f *studentProgressionApplicationFake) CancelStudentProgression(context.Context, application.Invocation, string) (application.OnboardingImportView, error) {
	return f.view, nil
}
func (f *studentProgressionApplicationFake) StudentProgressionReport(context.Context, application.Invocation, string, io.Writer) error {
	return nil
}

func TestStudentProgressionHTTPRequiresRFC3339AndForwardsExactTargets(t *testing.T) {
	logger, _ := newTestLogger(t)
	at := time.Date(2026, 9, 1, 9, 0, 0, 0, time.UTC)
	sourcePeriodID, sourceClassID := model.NewAcademicPeriodID(), model.NewClassID()
	destinationPeriodID, destinationClassID := model.NewAcademicPeriodID(), model.NewClassID()
	fake := &studentProgressionApplicationFake{view: application.OnboardingImportView{ID: model.NewOnboardingImportID(), Mode: model.OnboardingImportStudentProgression,
		State: model.OnboardingImportParsing, ScopeType: model.RoleScopeClass, ScopeID: destinationClassID.String(), SourcePeriodID: sourcePeriodID,
		SourceClassID: sourceClassID, DestinationPeriodID: destinationPeriodID, DestinationClassID: destinationClassID, EffectiveAt: at,
		ParseJobID: model.NewJobID(), CreatedAt: at, UpdatedAt: at, ExpiresAt: at.Add(7 * 24 * time.Hour), Revision: 1}}
	principal := model.Principal{UserID: model.NewUserID(), SessionID: model.NewSessionID(), CredentialID: model.PrincipalCredentialID(model.NewId()),
		CredentialType: model.CredentialSessionAccess, AuthenticationMethod: "password", AuthenticationStrength: model.AuthenticationSingleFactor,
		ClientType: model.SessionClientWeb, AuthenticatedAt: at}
	httpAPI := newFocusedResourceAPI(t, logger, classRouteAuthenticator{principal: principal}, studentProgressionResource(fake))
	body := `{"source_period_id":"` + sourcePeriodID.String() + `","source_class_id":"` + sourceClassID.String() + `","destination_period_id":"` + destinationPeriodID.String() + `","destination_class_id":"` + destinationClassID.String() + `","effective_at":"` + at.Format(time.RFC3339Nano) + `"}`
	request := httptest.NewRequest(http.MethodPost, "/api/v1/student-progressions", strings.NewReader(body))
	request.Header.Set("Authorization", "Bearer session")
	response := httptest.NewRecorder()
	httpAPI.ServeHTTP(response, request)
	if response.Code != http.StatusAccepted || response.Header().Get("Cache-Control") != "no-store" ||
		fake.command.SourcePeriodID != sourcePeriodID.String() || fake.command.SourceClassID != sourceClassID.String() ||
		fake.command.DestinationPeriodID != destinationPeriodID.String() || fake.command.DestinationClassID != destinationClassID.String() || fake.command.EffectiveAt != at.UnixMilli() {
		t.Fatalf("student progression = %d %s command=%#v", response.Code, response.Body.String(), fake.command)
	}
	invalid := httptest.NewRequest(http.MethodPost, "/api/v1/student-progressions", strings.NewReader(strings.Replace(body, at.Format(time.RFC3339Nano), "1800000000000", 1)))
	invalid.Header.Set("Authorization", "Bearer session")
	invalidResponse := httptest.NewRecorder()
	httpAPI.ServeHTTP(invalidResponse, invalid)
	if invalidResponse.Code != http.StatusBadRequest {
		t.Fatalf("non-RFC3339 effective_at = %d %s", invalidResponse.Code, invalidResponse.Body.String())
	}
}

func TestStudentProgressionResponseExposesOnlySafeExactTargetsAndRows(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 8, 21, 9, 0, 0, 0, time.UTC)
	view := application.OnboardingImportView{ID: model.NewOnboardingImportID(), Mode: model.OnboardingImportStudentProgression,
		State: model.OnboardingImportPreviewReady, ScopeType: model.RoleScopeClass, ScopeID: model.NewClassID().String(),
		SourcePeriodID: model.NewAcademicPeriodID(), SourceClassID: model.NewClassID(), DestinationPeriodID: model.NewAcademicPeriodID(),
		DestinationClassID: model.NewClassID(), EffectiveAt: at.Add(24 * time.Hour), ParseJobID: model.NewJobID(),
		CreatedAt: at, UpdatedAt: at, ExpiresAt: at.Add(7 * 24 * time.Hour), Revision: 2, TotalRows: 1, ValidRows: 1}
	rows := []application.OnboardingImportRowView{{RowNumber: 1, Reference: "safe-membership-reference", Operation: "class.enroll", Status: model.OnboardingImportRowValid}}
	encoded, err := json.Marshal(onboardingImportResponseFromView(view, rows))
	if err != nil {
		t.Fatal(err)
	}
	body := string(encoded)
	for _, required := range []string{view.SourcePeriodID.String(), view.SourceClassID.String(), view.DestinationPeriodID.String(), view.DestinationClassID.String(), `"effective_at":"`} {
		if !strings.Contains(body, required) {
			t.Fatalf("response omits %q: %s", required, body)
		}
	}
	for _, forbidden := range []string{"email", "username", "recipient", "claim", "secret", "principal"} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("response contains private field %q: %s", forbidden, body)
		}
	}
}
