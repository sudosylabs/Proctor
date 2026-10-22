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

	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/server/model"
)

func TestExamAttemptOutboundViewsCannotCarryContinuityCredentialMaterial(t *testing.T) {
	t.Parallel()
	for _, target := range []any{ExamAttemptParticipationView{}, ExamAttemptSuspensionView{}, ExamAttemptConnectResult{}, ExamAttemptManagerSnapshot{}} {
		typ := reflect.TypeOf(target)
		for index := 0; index < typ.NumField(); index++ {
			name := strings.ToLower(typ.Field(index).Name)
			require.NotContains(t, name, "credential", "outbound")
			require.NotContains(t, name, "hash", "outbound")
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
	require.NotNil(t, snapshot.ActiveSuspension)
	require.Equal(t, suspension.ID, snapshot.ActiveSuspension.ID)
	require.Equal(t, suspension.FlagID, snapshot.ActiveSuspension.FlagID)
	require.Equal(t, model.AttemptSuspensionActive, snapshot.ActiveSuspension.State)
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
	require.Equal(t, credentialHash, input.ContinuityCredentialHash, "renewal contract = %#v / %#v", input, result)
	require.Equal(t, int64(9), result.AcceptedSequence, "renewal contract = %#v / %#v", input, result)
	require.Equal(t, model.AttemptParticipationInitialLease, result.LeaseExpiresAt.Sub(result.DatabaseTime), "renewal contract = %#v / %#v", input, result)
	assertNoCredentialMaterialFields(t, reflect.TypeOf(result))
}

func assertNoCredentialMaterialFields(t *testing.T, typ reflect.Type) {
	t.Helper()
	for index := 0; index < typ.NumField(); index++ {
		name := strings.ToLower(typ.Field(index).Name)
		require.NotContains(t, name, "credential", "outbound")
		require.NotContains(t, name, "hash", "outbound")
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
		SuspensionID: model.NewAttemptSuspensionID(), AuditEventID: model.NewAuditEventID(), AuditAt: model.NowUTC()}
	result := ExamAttemptParticipationExpiryResult{ExamID: due.ExamID, SittingID: due.SittingID, ClassID: due.ClassID,
		CandidateUserID: due.CandidateUserID, DatabaseTime: due.LeaseExpiresAt.Add(time.Second), Replayed: true}
	require.Equal(t, due.Generation, input.Generation, "expiry contract = %#v / %#v / %#v", due, input, result)
	require.True(t, input.EvidenceID.IsValid(), "expiry contract = %#v / %#v / %#v", due, input, result)
	require.True(t, input.FlagID.IsValid(), "expiry contract = %#v / %#v / %#v", due, input, result)
	require.True(t, input.SuspensionID.IsValid(), "expiry contract = %#v / %#v / %#v", due, input, result)
	require.True(t, result.Replayed, "expiry contract = %#v / %#v / %#v", due, input, result)
	for _, target := range []any{due, result} {
		assertNoCredentialMaterialFields(t, reflect.TypeOf(target))
	}
	_, exists := reflect.TypeOf((*ExamAttemptStore)(nil)).Elem().MethodByName("ResolveParticipationExpiry")
	require.True(t, exists, "ExamAttemptStore is missing exact late-renew expiry resolution")
}

func TestExamAttemptReallowContractIsRevisionFencedAndPrivateReasonSafe(t *testing.T) {
	t.Parallel()
	input := ExamAttemptReallow{ExamID: model.NewExamID(), SittingID: model.NewExamSittingID(), AttemptID: model.NewExamAttemptID(),
		SuspensionID: model.NewAttemptSuspensionID(), ActorUserID: model.NewUserID(), ManagerOverride: true,
		ExpectedRevision: 7, PrivateReason: "manager verified secure continuity",
		AuditEventID: model.NewAuditEventID(), AuditAt: model.NowUTC()}
	result := ExamAttemptReallowResult{ExamID: input.ExamID, SittingID: input.SittingID, ClassID: model.NewClassID(),
		CandidateUserID: model.NewUserID(), Replayed: true}
	require.Equal(t, int64(7), input.ExpectedRevision, "re-allow contract = %#v / %#v", input, result)
	require.NotEmpty(t, input.PrivateReason, "re-allow contract = %#v / %#v", input, result)
	require.True(t, result.Replayed, "re-allow contract = %#v / %#v", input, result)
	for _, target := range []any{ExamAttemptSuspensionView{}, result} {
		typ := reflect.TypeOf(target)
		assertNoCredentialMaterialFields(t, typ)
		for index := 0; index < typ.NumField(); index++ {
			require.NotContains(t, strings.ToLower(typ.Field(index).Name), "private", "outbound %s exposes private field", typ)
		}
	}
}

func TestCandidateAttemptSelectorsBindTheAuthenticatedSession(t *testing.T) {
	t.Parallel()
	sessionID := model.NewSessionID()
	access := CandidateAttemptAccess{AttemptID: model.NewExamAttemptID(), CandidateUserID: model.NewUserID(),
		SessionID: sessionID, ConnectionID: model.NewAttemptConnectionID(), ContinuityCredentialHash: model.HashToken(model.NewCredentialToken())}
	closure := ExamAttemptConnectionClose{ConnectionID: access.ConnectionID, CandidateUserID: access.CandidateUserID, SessionID: sessionID}
	require.Equal(t, sessionID, access.SessionID, "session-bound selectors = %#v / %#v", access, closure)
	require.Equal(t, sessionID, closure.SessionID, "session-bound selectors = %#v / %#v", access, closure)
	require.Equal(t, access.CandidateUserID, closure.CandidateUserID, "session-bound selectors = %#v / %#v", access, closure)
}
