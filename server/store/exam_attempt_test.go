// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package store

import (
	"reflect"
	"strings"
	"testing"

	"github.com/sudosylabs/proctor/server/model"
)

func TestExamAttemptOutboundViewsCannotCarryContinuityCredentialMaterial(t *testing.T) {
	t.Parallel()
	for _, target := range []any{ExamAttemptParticipationView{}, ExamAttemptConnectResult{}, ExamAttemptManagerSnapshot{}} {
		typ := reflect.TypeOf(target)
		for index := 0; index < typ.NumField(); index++ {
			name := strings.ToLower(typ.Field(index).Name)
			if strings.Contains(name, "credential") || strings.Contains(name, "hash") {
				t.Fatalf("outbound %s exposes sensitive field %s", typ, typ.Field(index).Name)
			}
		}
	}
}

func TestCandidateAttemptSelectorsBindTheAuthenticatedSession(t *testing.T) {
	t.Parallel()
	sessionID := model.NewSessionID()
	access := CandidateAttemptAccess{AttemptID: model.NewExamAttemptID(), CandidateUserID: model.NewUserID(),
		SessionID: sessionID, ConnectionID: model.NewAttemptConnectionID(), ContinuityCredentialHash: model.HashToken(model.NewCredentialToken())}
	closure := ExamAttemptConnectionClose{ConnectionID: access.ConnectionID, CandidateUserID: access.CandidateUserID, SessionID: sessionID}
	if access.SessionID != sessionID || closure.SessionID != sessionID || closure.CandidateUserID != access.CandidateUserID {
		t.Fatalf("session-bound selectors = %#v / %#v", access, closure)
	}
}
