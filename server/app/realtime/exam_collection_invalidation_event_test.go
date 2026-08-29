// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package realtime

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/sudosylabs/proctor/server/model"
)

func TestCandidateExamActivityChangedEventHasOnlySchemaVersion(t *testing.T) {
	t.Parallel()
	candidateID := model.NewUserID()
	event, err := NewCandidateExamActivityChangedEvent(candidateID)
	if err != nil {
		t.Fatal(err)
	}
	if event.Name != "candidate.exam_activity.changed" || event.UserID != candidateID.String() ||
		event.Action != "" || event.Resource != (model.Resource{}) {
		t.Fatalf("event = %#v", event)
	}
	var payload map[string]any
	if err = json.Unmarshal(event.Data, &payload); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(payload, map[string]any{"schema_version": float64(1)}) {
		t.Fatalf("payload = %#v", payload)
	}
	if err = event.ValidateForPublish(); err != nil {
		t.Fatal(err)
	}
}

func TestCurrentUserContextChangedEventHasOnlySchemaVersion(t *testing.T) {
	t.Parallel()
	userID := model.NewUserID()
	event, err := NewCurrentUserContextChangedEvent(userID)
	if err != nil {
		t.Fatal(err)
	}
	if event.Name != "current_user.context.changed" || event.UserID != userID.String() ||
		event.Action != "" || event.Resource != (model.Resource{}) {
		t.Fatalf("event = %#v", event)
	}
	var payload map[string]any
	if err = json.Unmarshal(event.Data, &payload); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(payload, map[string]any{"schema_version": float64(1)}) {
		t.Fatalf("payload = %#v", payload)
	}
	if err = event.ValidateForPublish(); err != nil {
		t.Fatal(err)
	}
}

func TestManagerSittingBoardChangedEventHasOnlyBoardSelectors(t *testing.T) {
	t.Parallel()
	examID, sittingID := model.NewExamID(), model.NewExamSittingID()
	event, err := NewManagerSittingBoardChangedEvent(examID, sittingID)
	if err != nil {
		t.Fatal(err)
	}
	if event.Name != "manager.sitting_board.changed" || event.UserID != "" ||
		event.Action != model.ActionExamSittingView || event.Resource != (model.Resource{
		Type: model.ResourceExamSitting, ID: sittingID.String(),
	}) {
		t.Fatalf("event = %#v", event)
	}
	var payload map[string]any
	if err = json.Unmarshal(event.Data, &payload); err != nil {
		t.Fatal(err)
	}
	want := map[string]any{"schema_version": float64(1), "exam_id": examID.String(),
		"exam_sitting_id": sittingID.String()}
	if !reflect.DeepEqual(payload, want) {
		t.Fatalf("payload = %#v, want %#v", payload, want)
	}
	if err = event.ValidateForPublish(); err != nil {
		t.Fatal(err)
	}
}
