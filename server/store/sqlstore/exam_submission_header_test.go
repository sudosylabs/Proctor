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

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

func TestExamSubmissionHeaderRetirementNullability(t *testing.T) {
	t.Parallel()
	manifest, err := model.NewExamSubmissionManifest(5, nil)
	if err != nil {
		t.Fatal(err)
	}
	base := examSubmissionHeaderRow{ID: model.NewSubmissionID().String(), AttemptID: model.NewExamAttemptID().String(),
		ExamRevisionID: model.NewExamRevisionID().String(), WorkspaceID: model.NewExamAttemptWorkspaceID().String(),
		ManifestSchemaVersion: model.ExamSubmissionManifestSchemaVersion,
		WorkspaceCursor:       sql.NullInt64{Int64: 5, Valid: true}, ManifestDigest: sql.NullString{String: manifest.SHA256, Valid: true},
		ManifestEntryCount: sql.NullInt64{Valid: true}, ManifestTotalFileBytes: sql.NullInt64{Valid: true},
		IntegrityState: string(model.SubmissionIntegrityRetired), IntegrityRetiredAt: sql.NullTime{Time: time.Unix(200, 0), Valid: true},
		Provenance: string(model.ExamSubmissionCandidateSubmitted), SubmittedAt: time.Unix(100, 0)}
	value, err := base.model()
	if err != nil || value.IntegrityState != model.SubmissionIntegrityRetired || value.WorkspaceCursor != 5 || value.ManifestDigest != manifest.SHA256 {
		t.Fatalf("read retained work after integrity retirement: %#v %v", value, err)
	}
	for name, corrupt := range map[string]func(*examSubmissionHeaderRow){
		"retired focus zero still persisted":    func(row *examSubmissionHeaderRow) { row.FinalFocusLossSequence.Valid = true },
		"retired count zero still persisted":    func(row *examSubmissionHeaderRow) { row.UnresolvedIntegrityCount.Valid = true },
		"retired source still persisted":        func(row *examSubmissionHeaderRow) { row.BrowserSourceSessionID.Valid = true },
		"retired browser state still persisted": func(row *examSubmissionHeaderRow) { row.BrowserActivityState.Valid = true },
		"retained manifest missing":             func(row *examSubmissionHeaderRow) { row.ManifestDigest = sql.NullString{} },
		"retirement lacks marker":               func(row *examSubmissionHeaderRow) { row.IntegrityRetiredAt = sql.NullTime{} },
	} {
		t.Run(name, func(t *testing.T) {
			row := base
			corrupt(&row)
			if _, err := row.model(); err == nil {
				t.Fatal("malformed persisted redaction accepted")
			}
		})
	}
	retiredWork := base
	retiredWork.WorkRetiredAt = base.IntegrityRetiredAt
	retiredWork.WorkspaceCursor = sql.NullInt64{}
	retiredWork.ManifestDigest = sql.NullString{}
	retiredWork.ManifestEntryCount = sql.NullInt64{}
	retiredWork.ManifestTotalFileBytes = sql.NullInt64{}
	if _, err := retiredWork.model(); !store.IsNotFound(err) {
		t.Fatalf("work receipt became a readable zero-value header: %v", err)
	}
}
