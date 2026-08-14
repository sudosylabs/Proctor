// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"
	"time"

	examworkspace "github.com/sudosylabs/proctor/server/app/exam/workspace"
	"github.com/sudosylabs/proctor/server/model"
)

func TestExamStarterWorkspaceReplacementFingerprintIncludesExpectedContentVersion(t *testing.T) {
	t.Parallel()
	invocation := NewInvocation(model.Principal{UserID: model.NewUserID()}, model.RequestMetadata{})
	examID := model.NewExamID()
	entryID := model.NewStarterWorkspaceEntryID().String()
	first, err := newExamStarterWorkspaceIdempotency(invocation, "exam.starter_workspace.file.replace.v1", "replace-key", examID, 2,
		model.WorkspaceContentVersion("aaaaaaaaaaaaaaaaaaaaaaaaaa"), entryID, "", "text/plain", 1, "checksum")
	if err != nil {
		t.Fatal(err)
	}
	second, err := newExamStarterWorkspaceIdempotency(invocation, "exam.starter_workspace.file.replace.v1", "replace-key", examID, 2,
		model.WorkspaceContentVersion("bbbbbbbbbbbbbbbbbbbbbbbbbb"), entryID, "", "text/plain", 1, "checksum")
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(first.Fingerprint[:], second.Fingerprint[:]) {
		t.Fatal("expected content version was omitted from idempotency fingerprint")
	}
}

func TestExamStarterWorkspaceRealtimeEffectContainsOnlySafeChangeMetadata(t *testing.T) {
	t.Parallel()
	sink := &recordingRealtimeSink{}
	cluster := &recordingRealtimeCluster{}
	realtime := newTestRealtimeService(t, noopAuthenticationCache{})
	if err := realtime.SetSink(sink); err != nil {
		t.Fatal(err)
	}
	if err := realtime.SetClusterFanout(cluster); err != nil {
		t.Fatal(err)
	}
	examID, entryID := model.NewExamID(), model.NewStarterWorkspaceEntryID()
	changedAt := time.Date(2026, 8, 15, 8, 30, 0, 123, time.UTC)
	if err := (examStarterWorkspaceRealtimeEffects{realtime: realtime}).Changed(context.Background(), examID, entryID, 4, examworkspace.ChangeFileReplaced, changedAt); err != nil {
		t.Fatal(err)
	}
	if len(sink.events) != 1 || sink.events[0].Name != "exam_starter_workspace_changed" ||
		sink.events[0].Resource != (model.Resource{Type: model.ResourceExam, ID: examID.String()}) {
		t.Fatalf("events=%#v", sink.events)
	}
	var data map[string]any
	if err := json.Unmarshal(sink.events[0].Data, &data); err != nil {
		t.Fatal(err)
	}
	if len(data) != 5 || data["exam_id"] != examID.String() || data["entry_id"] != entryID.String() ||
		data["operation"] != "file_replaced" || data["draft_revision"] != float64(4) || data["changed_at"] != changedAt.Format(time.RFC3339Nano) {
		t.Fatalf("event data=%#v", data)
	}
	for _, forbidden := range []string{"path", "content", "sha256", "object_id"} {
		if _, exists := data[forbidden]; exists {
			t.Fatalf("event exposed %s: %#v", forbidden, data)
		}
	}
}
