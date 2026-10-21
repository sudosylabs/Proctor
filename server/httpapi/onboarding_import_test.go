// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package httpapi

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/server/app"
	"github.com/sudosylabs/proctor/server/model"
)

func TestOnboardingImportResponseExcludesPrivateRowAndCredentialMaterial(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 8, 20, 9, 0, 0, 0, time.UTC)
	view := app.OnboardingImportView{ID: model.NewOnboardingImportID(), Mode: model.OnboardingImportInstitution,
		State: model.OnboardingImportPreviewReady, ScopeType: model.RoleScopeInstitution, ScopeID: model.NewInstitutionID().String(),
		ParseJobID: model.NewJobID(), CreatedAt: at, UpdatedAt: at, ExpiresAt: at.Add(7 * 24 * time.Hour), Revision: 2, TotalRows: 1, ValidRows: 1}
	rows := []app.OnboardingImportRowView{{RowNumber: 1, Reference: "safe-row", Operation: "institution_role.create", Status: model.OnboardingImportRowValid}}
	encoded, err := json.Marshal(onboardingImportResponseFromView(view, rows))
	require.NoError(t, err)
	body := string(encoded)
	for _, forbidden := range []string{"target_email", "email", "claim", "hash", "recipient", "secret"} {
		require.NotContains(t, body, forbidden, "response contains private field")
	}
	require.Contains(t, body, `"reference":"safe-row"`)
	require.Contains(t, body, `"status":"valid"`)
}
