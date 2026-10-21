// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package sqlstore

import (
	"database/sql"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

func TestExamSubmissionHeaderRetirementNullability(t *testing.T) {
	t.Parallel()
	manifest, err := model.NewExamSubmissionManifest(5, nil)
	require.NoError(t, err)
	base := examSubmissionHeaderRow{BrowserState: "not_applicable", BrowserInventoryRevision: 1, ID: model.NewSubmissionID().String(), AttemptID: model.NewExamAttemptID().String(),
		ExamRevisionID: model.NewExamRevisionID().String(), WorkspaceID: model.NewExamAttemptWorkspaceID().String(),
		ManifestSchemaVersion: model.ExamSubmissionManifestSchemaVersion,
		WorkspaceCursor:       sql.NullInt64{Int64: 5, Valid: true}, ManifestDigest: sql.NullString{String: manifest.SHA256, Valid: true},
		ManifestEntryCount: sql.NullInt64{Valid: true}, ManifestTotalFileBytes: sql.NullInt64{Valid: true},
		IntegrityState: string(model.SubmissionIntegrityRetired), IntegrityRetiredAt: sql.NullTime{Time: time.Unix(200, 0), Valid: true},
		Provenance: string(model.ExamSubmissionCandidateSubmitted), SubmittedAt: time.Unix(100, 0)}
	value, err := base.model()
	require.NoError(t, err, "read retained work after integrity retirement: %#v", value)
	require.Equal(t, model.SubmissionIntegrityRetired, value.IntegrityState, "read retained work after integrity retirement: %#v", value)
	require.Equal(t, int64(5), value.WorkspaceCursor, "read retained work after integrity retirement: %#v", value)
	require.Equal(t, manifest.SHA256, value.ManifestDigest, "read retained work after integrity retirement: %#v", value)
	for name, corrupt := range map[string]func(*examSubmissionHeaderRow){
		"retired focus zero still persisted":   func(row *examSubmissionHeaderRow) { row.FinalFocusLossSequence.Valid = true },
		"retired count zero still persisted":   func(row *examSubmissionHeaderRow) { row.UnresolvedIntegrityCount.Valid = true },
		"inconsistent retained browser counts": func(row *examSubmissionHeaderRow) { row.BrowserSourceCount = 1 },
		"invalid retained browser revision":    func(row *examSubmissionHeaderRow) { row.BrowserInventoryRevision = 0 },
		"retained manifest missing":            func(row *examSubmissionHeaderRow) { row.ManifestDigest = sql.NullString{} },
		"retirement lacks marker":              func(row *examSubmissionHeaderRow) { row.IntegrityRetiredAt = sql.NullTime{} },
	} {
		t.Run(name, func(t *testing.T) {
			row := base
			corrupt(&row)
			_, modelErr := row.model()
			require.Error(t, modelErr, "malformed persisted redaction accepted")
		})
	}
	retiredWork := base
	retiredWork.WorkRetiredAt = base.IntegrityRetiredAt
	retiredWork.WorkspaceCursor = sql.NullInt64{}
	retiredWork.ManifestDigest = sql.NullString{}
	retiredWork.ManifestEntryCount = sql.NullInt64{}
	retiredWork.ManifestTotalFileBytes = sql.NullInt64{}
	_, err = retiredWork.model()
	require.True(t, store.IsNotFound(err), "work receipt became a readable zero-value header")
}
