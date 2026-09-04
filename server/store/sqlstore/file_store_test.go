// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package sqlstore

import (
	"database/sql"
	"errors"
	"testing"

	"github.com/lib/pq"

	"github.com/sudosylabs/proctor/server/model"
)

func TestFileRenditionRowRehydrationRejectsInvalidPersistedState(t *testing.T) {
	t.Parallel()
	valid := fileRenditionRow{
		ID: model.NewFileRenditionID().String(), RevisionID: model.NewFileRevisionID().String(),
		CreatedAt: model.TimeFromMillis(1), Name: "profile_128", MediaType: "image/webp",
		Size: 1, Width: 128, Height: 128,
		SHA256: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
	}
	tests := []struct {
		name, field string
		mutate      func(*fileRenditionRow)
	}{
		{name: "rendition id", field: "id", mutate: func(row *fileRenditionRow) { row.ID = "bad" }},
		{name: "revision id", field: "file_revision_id", mutate: func(row *fileRenditionRow) { row.RevisionID = "bad" }},
		{name: "domain state", field: "value", mutate: func(row *fileRenditionRow) { row.SHA256 = "object-key-or-hash-must-not-appear" }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			row := valid
			test.mutate(&row)
			_, err := row.model("file_rendition")
			assertFilePersistedStateError(t, err, "file_rendition", test.field)
		})
	}
}

func TestUploadLeaseRowRehydrationRejectsInvalidPersistedState(t *testing.T) {
	t.Parallel()
	valid := uploadLeaseRow{
		ID: model.NewUploadLeaseID().String(), FileRevisionID: model.NewFileRevisionID().String(),
		CreatedByUserID: model.NewUserID().String(), CreatedAt: model.TimeFromMillis(1),
		UpdatedAt: model.TimeFromMillis(1), ExpiresAt: model.TimeFromMillis(3_000_001),
		Revision: 1,
	}
	tests := []struct {
		name, field string
		mutate      func(*uploadLeaseRow)
	}{
		{name: "lease id", field: "id", mutate: func(row *uploadLeaseRow) { row.ID = "bad" }},
		{name: "revision id", field: "file_revision_id", mutate: func(row *uploadLeaseRow) { row.FileRevisionID = "bad" }},
		{name: "creator id", field: "created_by_user_id", mutate: func(row *uploadLeaseRow) { row.CreatedByUserID = "bad" }},
		{name: "domain state", field: "value", mutate: func(row *uploadLeaseRow) { row.BytesReceived = -1 }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			row := valid
			test.mutate(&row)
			_, err := row.model()
			assertFilePersistedStateError(t, err, "upload_lease", test.field)
		})
	}
}

func TestFilePurgeCandidateRowRehydrationRejectsMalformedIdentifiers(t *testing.T) {
	t.Parallel()
	valid := filePurgeCandidateRow{
		Cursor: "lease:" + model.NewUploadLeaseID().String(), Kind: "expired_lease",
		LeaseID: sql.NullString{String: model.NewUploadLeaseID().String(), Valid: true},
		EntryID: model.NewFileEntryID().String(), RevisionID: model.NewFileRevisionID().String(),
		RenditionIDs: pq.StringArray{model.NewFileRenditionID().String()},
	}
	valid.Cursor = "lease:" + valid.LeaseID.String
	tests := []struct {
		name, field string
		mutate      func(*filePurgeCandidateRow)
	}{
		{name: "lease id", field: "lease_id", mutate: func(row *filePurgeCandidateRow) { row.LeaseID = sql.NullString{Valid: true} }},
		{name: "entry id", field: "entry_id", mutate: func(row *filePurgeCandidateRow) { row.EntryID = "bad" }},
		{name: "revision id", field: "revision_id", mutate: func(row *filePurgeCandidateRow) { row.RevisionID = "bad" }},
		{name: "rendition id", field: "rendition_ids", mutate: func(row *filePurgeCandidateRow) { row.RenditionIDs = pq.StringArray{"bad"} }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			row := valid
			test.mutate(&row)
			_, err := row.model()
			assertFilePersistedStateError(t, err, "file_purge_candidate", test.field)
		})
	}
}

func assertFilePersistedStateError(t *testing.T, err error, entity, field string) {
	t.Helper()
	var persisted *persistedStateError
	if !errors.As(err, &persisted) || persisted.Entity != entity || persisted.Field != field {
		t.Fatalf("error = %v, want %s.%s persisted-state error", err, entity, field)
	}
}
