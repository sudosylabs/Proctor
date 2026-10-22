// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package sqlstore

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/server/model"
)

func TestDecodeOutcomeRejectsDocumentsOfAnotherFormat(t *testing.T) {
	t.Parallel()
	id := model.NewID()
	for _, test := range []struct {
		name    string
		encoded string
	}{
		{name: "unknown member", encoded: `{"export_id":"` + id + `","ExportID":"` + id + `"}`},
		{name: "go field name", encoded: `{"ExportID":"` + id + `"}`},
		{name: "trailing document", encoded: `{"export_id":"` + id + `"}{}`},
		{name: "not an object", encoded: `"` + id + `"`},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			var outcome examExportOutcome
			require.Error(t, decodeOutcome([]byte(test.encoded), &outcome))
		})
	}
	var outcome examExportOutcome
	require.NoError(t, decodeOutcome([]byte(`{"export_id":"`+id+`"}`), &outcome))
	require.Equal(t, model.ExamExportID(id), outcome.ExportID)
}

func TestExamSittingOutcomeRoundTripsByJSONNames(t *testing.T) {
	t.Parallel()
	start := time.Date(2026, time.August, 15, 12, 0, 0, 0, time.UTC)
	sitting := &model.ExamSitting{
		ID: model.ExamSittingID(model.NewID()), ExamID: model.ExamID(model.NewID()),
		ExamRevisionID: model.ExamRevisionID(model.NewID()), ClassID: model.ClassID(model.NewID()),
		ScheduledStartAt: start, ScheduledEndAt: start.Add(time.Hour), State: model.ExamSittingCanceled,
		CreatedAt: start.Add(-time.Hour), UpdatedAt: start.Add(-time.Minute),
		CanceledAt: model.OptionalTimeFrom(start.Add(-time.Minute)), ReasonCode: model.ExamSittingReasonManagerCanceled,
		Revision: 2,
	}
	encoded, err := encodeExamSittingOutcome(sitting)
	require.NoError(t, err)
	require.Contains(t, string(encoded), `"scheduled_start_at":`)
	require.NotContains(t, string(encoded), `"ScheduledStartAt"`)
	decoded, err := decodeExamSittingOutcome(encoded)
	require.NoError(t, err)
	require.Equal(t, sitting, decoded)
}
