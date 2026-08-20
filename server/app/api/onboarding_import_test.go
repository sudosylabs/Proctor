// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package api

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	application "github.com/sudosylabs/proctor/server/app"
	"github.com/sudosylabs/proctor/server/model"
)

func TestOnboardingImportResponseExcludesPrivateRowAndCredentialMaterial(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 8, 20, 9, 0, 0, 0, time.UTC)
	view := application.OnboardingImportView{ID: model.NewOnboardingImportID(), Mode: model.OnboardingImportInstitution,
		State: model.OnboardingImportPreviewReady, ScopeType: model.RoleScopeInstitution, ScopeID: model.NewInstitutionID().String(),
		ParseJobID: model.NewJobID(), CreatedAt: at, UpdatedAt: at, ExpiresAt: at.Add(7 * 24 * time.Hour), Revision: 2, TotalRows: 1, ValidRows: 1}
	rows := []application.OnboardingImportRowView{{RowNumber: 1, Reference: "safe-row", Operation: "institution_role.create", Status: model.OnboardingImportRowValid}}
	encoded, err := json.Marshal(onboardingImportResponseFromView(view, rows))
	if err != nil {
		t.Fatal(err)
	}
	body := string(encoded)
	for _, forbidden := range []string{"target_email", "email", "claim", "hash", "recipient", "secret"} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("response contains private field %q: %s", forbidden, body)
		}
	}
	if !strings.Contains(body, `"reference":"safe-row"`) || !strings.Contains(body, `"status":"valid"`) {
		t.Fatalf("response omits safe row projection: %s", body)
	}
}
