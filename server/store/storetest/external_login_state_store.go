// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package storetest

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

type ExternalLoginStateSQLProbe struct {
	Backdate func(*testing.T, model.ExternalLoginStateID, time.Time, time.Time)
}

func TestExternalLoginStateStore(t *testing.T, ss store.Store, probe ExternalLoginStateSQLProbe) {
	ctx := context.Background()
	stateToken := model.NewCredentialToken()
	bindingToken := model.NewCredentialToken()
	input := &model.ExternalLoginState{
		Provider: "campus-cas", StateHash: model.HashToken(stateToken),
		BindingHash: model.HashToken(bindingToken), ReturnTo: "/exams?active=true",
		ClientType: model.SessionClientDesktop, DeviceID: "desktop-1",
		DeviceName: "Desktop",
	}
	saved, err := ss.ExternalLoginState().Save(ctx, input, time.Minute)
	require.NoError(t, err)
	require.True(t, saved.ID.IsValid(), "Save() saved=%#v input=%#v", saved, input)
	require.True(t, input.ID.IsZero(), "Save() saved=%#v input=%#v", saved, input)
	require.True(t, input.CreatedAt.IsZero(), "Save() saved=%#v input=%#v", saved, input)
	require.True(t, input.ExpiresAt.IsZero(), "Save() saved=%#v input=%#v", saved, input)
	require.Equal(t, time.Minute, saved.ExpiresAt.Sub(saved.CreatedAt), "Save() lifetime")
	got, err := ss.ExternalLoginState().GetByStateHash(ctx, saved.StateHash)
	require.NoError(t, err)
	require.Equal(t, saved.ID, got.ID)
	require.Equal(t, saved.BindingHash, got.BindingHash)
	_, err = ss.ExternalLoginState().Consume(
		ctx,
		saved.Provider,
		saved.StateHash,
		model.HashToken(model.NewCredentialToken()),
	)
	require.True(t, store.IsNotFound(err), "Consume(wrong binding) error")
	consumed, err := ss.ExternalLoginState().Consume(
		ctx,
		saved.Provider,
		saved.StateHash,
		saved.BindingHash,
	)
	require.NoError(t, err)
	require.True(t, consumed.ConsumedAt.Valid)
	require.False(t, consumed.ConsumedAt.Time.Before(saved.CreatedAt))
	require.True(t, consumed.ConsumedAt.Time.Before(saved.ExpiresAt))
	_, err = ss.ExternalLoginState().Consume(
		ctx,
		saved.Provider,
		saved.StateHash,
		saved.BindingHash,
	)
	require.True(t, store.IsNotFound(err), "Consume(replay) error")

	fixture, class, inviter, _, _, issuedAt := invitationAcceptanceStoreFixture(t, ctx, ss, "external-admission")
	issue := studentClassInvitationIssueFixture(t, ss, inviter, class, fixture.period, issuedAt)
	invitation, err := ss.Invitation().IssueStudentClass(ctx, issue)
	require.NoError(t, err)
	admissionInput := &model.ExternalLoginState{
		Provider: "campus-cas", Purpose: model.ExternalAuthenticationPurposeInvitationAdmission,
		StateHash: model.HashToken(model.NewCredentialToken()), BindingHash: model.HashToken(model.NewCredentialToken()),
		ReturnTo: "/join", ClientType: model.SessionClientWeb,
	}
	admission, err := ss.ExternalLoginState().SaveInvitationAdmission(ctx, admissionInput, time.Minute, invitation.ClaimHash)
	require.NoError(t, err)
	require.Equal(t, invitation.ID, admission.InvitationID, "SaveInvitationAdmission() = %#v input=%#v", admission, admissionInput)
	require.True(t, admissionInput.InvitationID.IsZero(), "SaveInvitationAdmission() = %#v input=%#v", admission, admissionInput)
	_, err = ss.ExternalLoginState().SaveInvitationAdmission(ctx, &model.ExternalLoginState{
		Provider: "campus-cas", Purpose: model.ExternalAuthenticationPurposeInvitationAdmission,
		StateHash: model.HashToken(model.NewCredentialToken()), BindingHash: model.HashToken(model.NewCredentialToken()),
		ReturnTo: "/join", ClientType: model.SessionClientWeb,
	}, time.Minute, model.HashInvitationClaim(model.NewCredentialToken()))
	require.True(t, store.IsNotFound(err), "SaveInvitationAdmission(unknown claim) error")

	expired := &model.ExternalLoginState{
		Provider:    "campus-cas",
		StateHash:   model.HashToken(model.NewCredentialToken()),
		BindingHash: model.HashToken(model.NewCredentialToken()),
		ReturnTo:    "/", ClientType: model.SessionClientWeb,
	}
	expired, err = ss.ExternalLoginState().Save(ctx, expired, 2*time.Minute)
	require.NoError(t, err)
	pastExpiry := model.NowUTC().Add(-time.Minute)
	probe.Backdate(t, expired.ID, pastExpiry.Add(-2*time.Minute), pastExpiry)
	_, err = ss.ExternalLoginState().Consume(
		ctx,
		expired.Provider,
		expired.StateHash,
		expired.BindingHash,
	)
	require.True(t, store.IsNotFound(err), "Consume(expired) error")

	user := saveUser(t, ctx, ss)
	institution := saveInstitution(t, ctx, ss)
	audit, err := ss.Audit().Save(ctx, &model.AuditEvent{ActorID: user.ID,
		Action: string(model.ActionExternalIdentityManage), Resource: model.Resource{Type: model.ResourceUser, ID: user.ID.String()},
		ScopeType: model.RoleScopeInstitution, ScopeID: institution.ID.String(), Status: model.AuditStatusAttempt,
		NodeID: "external-login-maintenance-test"})
	require.NoError(t, err)
	abandoned, err := ss.ExternalLoginState().Save(ctx, &model.ExternalLoginState{Provider: "campus-cas",
		Purpose: model.ExternalAuthenticationPurposeConnect, TargetUserID: user.ID, AuditEventID: audit.ID.String(),
		StateHash: model.HashToken(model.NewCredentialToken()), BindingHash: model.HashToken(model.NewCredentialToken()),
		ReturnTo: "/account/security", ClientType: model.SessionClientWeb}, time.Minute)
	require.NoError(t, err)
	pastExpiry = model.NowUTC().Add(-time.Minute)
	probe.Backdate(t, abandoned.ID, pastExpiry.Add(-time.Minute), pastExpiry)
	maintained, err := ss.ExternalLoginState().Maintain(ctx, 1)
	require.NoError(t, err)
	require.Equal(t, 1, maintained.Terminalized)
	require.Equal(t, 0, maintained.Purged)
	require.False(t, maintained.More)
	terminalAudit, err := ss.Audit().Get(ctx, audit.ID)
	require.NoError(t, err)
	require.Equal(t, model.AuditStatusFail, terminalAudit.Status)
	require.Equal(t, "authentication.external.expired", terminalAudit.ErrorCode)
	retainedExpiry := model.NowUTC().Add(-25 * time.Hour)
	probe.Backdate(t, abandoned.ID, retainedExpiry.Add(-time.Minute), retainedExpiry)
	maintained, err = ss.ExternalLoginState().Maintain(ctx, 1)
	require.NoError(t, err)
	require.Equal(t, 0, maintained.Terminalized)
	require.Equal(t, 1, maintained.Purged)
	_, err = ss.ExternalLoginState().GetByStateHash(ctx, abandoned.StateHash)
	require.True(t, store.IsNotFound(err), "purged state error")
	for _, invalidLimit := range []int{0, 1001} {
		_, err = ss.ExternalLoginState().Maintain(ctx, invalidLimit)
		require.Error(t, err, "Maintain(%d) accepted invalid limit", invalidLimit)
	}
	for _, invalidLifetime := range []time.Duration{0, time.Minute - time.Millisecond, 30*time.Minute + time.Millisecond} {
		_, err = ss.ExternalLoginState().Save(ctx, &model.ExternalLoginState{
			Provider: "campus-cas", StateHash: model.HashToken(model.NewCredentialToken()),
			BindingHash: model.HashToken(model.NewCredentialToken()), ReturnTo: "/", ClientType: model.SessionClientWeb,
		}, invalidLifetime)
		require.Error(t, err, "Save(lifetime=%v) accepted invalid lifetime", invalidLifetime)
	}
}
