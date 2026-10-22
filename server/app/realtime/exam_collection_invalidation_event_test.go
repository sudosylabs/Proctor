// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package realtime

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/server/model"
)

func TestCandidateExamActivityChangedEventHasOnlySchemaVersion(t *testing.T) {
	t.Parallel()
	candidateID := model.NewUserID()
	event, err := NewCandidateExamActivityChangedEvent(candidateID)
	require.NoError(t, err)
	require.Equal(t, "candidate.exam_activity.changed", event.Name)
	require.Equal(t, candidateID, event.UserID)
	require.Empty(t, event.Action)
	require.Equal(t, model.Resource{}, event.Resource)
	var payload map[string]any
	err = json.Unmarshal(event.Data, &payload)
	require.NoError(t, err)
	require.Equal(t, map[string]any{"schema_version": float64(1)}, payload)
	err = event.ValidateForPublish()
	require.NoError(t, err)
}

func TestCurrentUserContextChangedEventHasOnlySchemaVersion(t *testing.T) {
	t.Parallel()
	userID := model.NewUserID()
	event, err := NewCurrentUserContextChangedEvent(userID)
	require.NoError(t, err)
	require.Equal(t, "current_user.context.changed", event.Name)
	require.Equal(t, userID, event.UserID)
	require.Empty(t, event.Action)
	require.Equal(t, model.Resource{}, event.Resource)
	var payload map[string]any
	err = json.Unmarshal(event.Data, &payload)
	require.NoError(t, err)
	require.Equal(t, map[string]any{"schema_version": float64(1)}, payload)
	err = event.ValidateForPublish()
	require.NoError(t, err)
}

func TestManagerSittingBoardChangedEventHasOnlyBoardSelectors(t *testing.T) {
	t.Parallel()
	examID, sittingID := model.NewExamID(), model.NewExamSittingID()
	event, err := NewManagerSittingBoardChangedEvent(examID, sittingID)
	require.NoError(t, err)
	require.Equal(t, "manager.sitting_board.changed", event.Name)
	require.Empty(t, event.UserID)
	require.Equal(t, model.ActionExamSittingView, event.Action)
	require.Equal(t, model.Resource{
		Type: model.ResourceExamSitting, ID: sittingID.String(),
	}, event.Resource)
	var payload map[string]any
	err = json.Unmarshal(event.Data, &payload)
	require.NoError(t, err)
	want := map[string]any{"schema_version": float64(1), "exam_id": examID.String(),
		"exam_sitting_id": sittingID.String()}
	require.Equal(t, want, payload, "payload")
	err = event.ValidateForPublish()
	require.NoError(t, err)
}
