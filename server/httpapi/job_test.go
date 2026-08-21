// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package httpapi

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	application "github.com/sudosylabs/proctor/server/app"
	"github.com/sudosylabs/proctor/server/model"
)

func TestJobResponseCannotExposePrivateExecutionDocuments(t *testing.T) {
	view := application.JobView{ID: model.NewJobID(), Type: model.JobTypeProfilePictureGenerateDefault, Status: model.JobStatusRunning, CreatedAt: time.Now(), UpdatedAt: time.Now(), AvailableAt: time.Now(), AttemptCount: 1, MaximumAttempts: 8, Revision: 2}
	encoded, err := json.Marshal(jobResponseFromApplication(view))
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"command", "checkpoint", "result", "claim_token", "dedupe", "node_id"} {
		if strings.Contains(string(encoded), forbidden) {
			t.Fatalf("response leaked %q: %s", forbidden, encoded)
		}
	}
}

func TestJobCursorIsOpaqueAndRejectsInvalidInput(t *testing.T) {
	cursor := jobCursor{CreatedAt: 123, ID: model.NewId()}
	roundTrip, err := decodeJobCursor(encodeJobCursor(cursor))
	if err != nil || roundTrip != cursor {
		t.Fatalf("cursor = %#v, %v", roundTrip, err)
	}
	if _, err = decodeJobCursor("not-a-cursor"); err == nil {
		t.Fatal("invalid cursor accepted")
	}
}
