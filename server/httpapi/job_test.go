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

func TestJobResponseCannotExposePrivateExecutionDocuments(t *testing.T) {
	view := app.JobView{ID: model.NewJobID(), Type: model.JobTypeProfilePictureGenerateDefault, Status: model.JobStatusRunning, CreatedAt: time.Now(), UpdatedAt: time.Now(), AvailableAt: time.Now(), AttemptCount: 1, MaximumAttempts: 8, Revision: 2}
	encoded, err := json.Marshal(jobResponseFromApplication(view))
	require.NoError(t, err)
	for _, forbidden := range []string{"command", "checkpoint", "result", "claim_token", "dedupe", "node_id"} {
		require.NotContains(t, string(encoded), forbidden, "response leaked")
	}
}

func TestJobCursorIsOpaqueAndRejectsInvalidInput(t *testing.T) {
	cursor := jobCursor{Version: 1, CreatedAt: "2026-01-02T03:04:05.123Z", ID: model.NewID()}
	encoded, err := encodeJobCursor(cursor)
	require.NoError(t, err)
	roundTrip, err := decodeJobCursor(encoded)
	require.NoError(t, err, "cursor")
	require.Equal(t, cursor, roundTrip, "cursor")
	_, err = decodeJobCursor("not-a-cursor")
	require.Error(t, err, "invalid cursor accepted")
}
