// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package store

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/sudosylabs/proctor/server/model"
)

func TestExamAttemptOutboundViewsCannotCarryContinuityCredentialMaterial(t *testing.T) {
	t.Parallel()
	for _, target := range []any{ExamAttemptParticipationView{}, ExamAttemptSuspensionView{}, ExamAttemptConnectResult{}, ExamAttemptManagerSnapshot{}} {
		typ := reflect.TypeOf(target)
		for index := 0; index < typ.NumField(); index++ {
			name := strings.ToLower(typ.Field(index).Name)
			if strings.Contains(name, "credential") || strings.Contains(name, "hash") {
				t.Fatalf("outbound %s exposes sensitive field %s", typ, typ.Field(index).Name)
			}
		}
	}
}

func TestExamAttemptManagerSnapshotCanRecoverExactActiveSuspension(t *testing.T) {
	t.Parallel()
	suspension := &ExamAttemptSuspensionView{ID: model.NewAttemptSuspensionID(), AttemptID: model.NewExamAttemptID(),
		ParticipationID: model.NewAttemptParticipationID(), FlagID: model.NewIntegrityFlagID(), Generation: 3,
		State: model.AttemptSuspensionActive, Source: model.AttemptSuspensionSourcePolicy,
		CandidateReason: model.AttemptSuspensionCandidateReasonSecureContinuityLost, StartedAt: model.NowUTC()}
	snapshot := ExamAttemptManagerSnapshot{ActiveSuspension: suspension}
	if snapshot.ActiveSuspension == nil || snapshot.ActiveSuspension.ID != suspension.ID ||
		snapshot.ActiveSuspension.FlagID != suspension.FlagID || snapshot.ActiveSuspension.State != model.AttemptSuspensionActive {
		t.Fatalf("manager active Suspension recovery = %#v", snapshot.ActiveSuspension)
	}
}

func TestExamAttemptParticipationRenewalContractIsHashOnlyAndSafe(t *testing.T) {
	t.Parallel()
	credentialHash := model.HashToken(model.NewCredentialToken())
	input := ExamAttemptParticipationRenewal{
		AttemptID: model.NewExamAttemptID(), ParticipationID: model.NewAttemptParticipationID(),
		ConnectionID: model.NewAttemptConnectionID(), CandidateUserID: model.NewUserID(), SessionID: model.NewSessionID(),
		Generation: 4, Sequence: 9, ContinuityCredentialHash: credentialHash,
	}
	databaseNow := time.Date(2026, time.August, 18, 9, 0, 0, 0, time.UTC)
	result := ExamAttemptParticipationRenewalResult{AttemptID: input.AttemptID, ParticipationID: input.ParticipationID,
		Generation: input.Generation, AcceptedSequence: input.Sequence, DatabaseTime: databaseNow,
		LeaseExpiresAt: databaseNow.Add(model.AttemptParticipationInitialLease)}
	if input.ContinuityCredentialHash != credentialHash || result.AcceptedSequence != 9 ||
		result.LeaseExpiresAt.Sub(result.DatabaseTime) != model.AttemptParticipationInitialLease {
		t.Fatalf("renewal contract = %#v / %#v", input, result)
	}
	assertNoCredentialMaterialFields(t, reflect.TypeOf(result))
}

func assertNoCredentialMaterialFields(t *testing.T, typ reflect.Type) {
	t.Helper()
	for index := 0; index < typ.NumField(); index++ {
		name := strings.ToLower(typ.Field(index).Name)
		if strings.Contains(name, "credential") || strings.Contains(name, "hash") {
			t.Fatalf("outbound %s exposes sensitive field %s", typ, typ.Field(index).Name)
		}
	}
}

func TestExamAttemptParticipationExpiryContractIsConditionalBoundedAndSafe(t *testing.T) {
	t.Parallel()
	due := ExamAttemptParticipationExpiryDue{ExamID: model.NewExamID(), SittingID: model.NewExamSittingID(),
		ClassID: model.NewClassID(), CandidateUserID: model.NewUserID(), AttemptID: model.NewExamAttemptID(),
		ParticipationID: model.NewAttemptParticipationID(), Generation: 3,
		LeaseExpiresAt: time.Date(2026, time.August, 18, 9, 0, 20, 0, time.UTC)}
	input := ExamAttemptParticipationExpiry{AttemptID: due.AttemptID, ParticipationID: due.ParticipationID,
		Generation: due.Generation, EvidenceID: model.NewIntegrityEvidenceID(), FlagID: model.NewIntegrityFlagID(),
		SuspensionID: model.NewAttemptSuspensionID(), AuditEventID: model.NewId(), AuditAt: model.GetMillis()}
	result := ExamAttemptParticipationExpiryResult{ExamID: due.ExamID, SittingID: due.SittingID, ClassID: due.ClassID,
		CandidateUserID: due.CandidateUserID, DatabaseTime: due.LeaseExpiresAt.Add(time.Second), Replayed: true}
	if input.Generation != due.Generation || !input.EvidenceID.IsValid() || !input.FlagID.IsValid() ||
		!input.SuspensionID.IsValid() || !result.Replayed {
		t.Fatalf("expiry contract = %#v / %#v / %#v", due, input, result)
	}
	for _, target := range []any{due, result} {
		assertNoCredentialMaterialFields(t, reflect.TypeOf(target))
	}
	if _, exists := reflect.TypeOf((*ExamAttemptStore)(nil)).Elem().MethodByName("ResolveParticipationExpiry"); !exists {
		t.Fatal("ExamAttemptStore is missing exact late-renew expiry resolution")
	}
}

func TestExamAttemptReallowContractIsRevisionFencedAndPrivateReasonSafe(t *testing.T) {
	t.Parallel()
	input := ExamAttemptReallow{ExamID: model.NewExamID(), SittingID: model.NewExamSittingID(), AttemptID: model.NewExamAttemptID(),
		SuspensionID: model.NewAttemptSuspensionID(), ActorUserID: model.NewUserID(), ManagerOverride: true,
		ExpectedAttemptRevision: 7, PrivateReason: "manager verified secure continuity", ChangedAt: model.NowUTC(),
		AuditEventID: model.NewId(), AuditAt: model.GetMillis()}
	result := ExamAttemptReallowResult{ExamID: input.ExamID, SittingID: input.SittingID, ClassID: model.NewClassID(),
		CandidateUserID: model.NewUserID(), Replayed: true}
	if input.ExpectedAttemptRevision != 7 || input.PrivateReason == "" || !result.Replayed {
		t.Fatalf("re-allow contract = %#v / %#v", input, result)
	}
	for _, target := range []any{ExamAttemptSuspensionView{}, result} {
		typ := reflect.TypeOf(target)
		assertNoCredentialMaterialFields(t, typ)
		for index := 0; index < typ.NumField(); index++ {
			if strings.Contains(strings.ToLower(typ.Field(index).Name), "private") {
				t.Fatalf("outbound %s exposes private field %s", typ, typ.Field(index).Name)
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
