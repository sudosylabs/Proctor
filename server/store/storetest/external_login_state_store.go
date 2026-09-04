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
	requireNoError(t, err)
	if !saved.ID.IsValid() || !input.ID.IsZero() || !input.CreatedAt.IsZero() || !input.ExpiresAt.IsZero() {
		t.Fatalf("Save() saved=%#v input=%#v", saved, input)
	}
	if saved.ExpiresAt.Sub(saved.CreatedAt) != time.Minute {
		t.Fatalf("Save() lifetime = %v, want %v", saved.ExpiresAt.Sub(saved.CreatedAt), time.Minute)
	}
	got, err := ss.ExternalLoginState().GetByStateHash(ctx, saved.StateHash)
	requireNoError(t, err)
	if got.ID != saved.ID || got.BindingHash != saved.BindingHash {
		t.Fatalf("GetByStateHash() = %#v", got)
	}
	if _, err := ss.ExternalLoginState().Consume(
		ctx,
		saved.Provider,
		saved.StateHash,
		model.HashToken(model.NewCredentialToken()),
	); !store.IsNotFound(err) {
		t.Fatalf("Consume(wrong binding) error = %v", err)
	}
	consumed, err := ss.ExternalLoginState().Consume(
		ctx,
		saved.Provider,
		saved.StateHash,
		saved.BindingHash,
	)
	requireNoError(t, err)
	if !consumed.ConsumedAt.Valid || consumed.ConsumedAt.Time.Before(saved.CreatedAt) || !consumed.ConsumedAt.Time.Before(saved.ExpiresAt) {
		t.Fatalf("Consume() = %#v", consumed)
	}
	if _, err := ss.ExternalLoginState().Consume(
		ctx,
		saved.Provider,
		saved.StateHash,
		saved.BindingHash,
	); !store.IsNotFound(err) {
		t.Fatalf("Consume(replay) error = %v", err)
	}

	fixture, class, inviter, _, _, issuedAt := invitationAcceptanceStoreFixture(t, ctx, ss, "external-admission")
	issue := studentClassInvitationIssueFixture(t, ss, inviter, class, fixture.period, issuedAt)
	invitation, err := ss.Invitation().IssueStudentClass(ctx, issue)
	requireNoError(t, err)
	admissionInput := &model.ExternalLoginState{
		Provider: "campus-cas", Purpose: model.ExternalAuthenticationPurposeInvitationAdmission,
		StateHash: model.HashToken(model.NewCredentialToken()), BindingHash: model.HashToken(model.NewCredentialToken()),
		ReturnTo: "/join", ClientType: model.SessionClientWeb,
	}
	admission, err := ss.ExternalLoginState().SaveInvitationAdmission(ctx, admissionInput, time.Minute, invitation.ClaimHash)
	requireNoError(t, err)
	if admission.InvitationID != invitation.ID || !admissionInput.InvitationID.IsZero() {
		t.Fatalf("SaveInvitationAdmission() = %#v input=%#v", admission, admissionInput)
	}
	if _, err = ss.ExternalLoginState().SaveInvitationAdmission(ctx, &model.ExternalLoginState{
		Provider: "campus-cas", Purpose: model.ExternalAuthenticationPurposeInvitationAdmission,
		StateHash: model.HashToken(model.NewCredentialToken()), BindingHash: model.HashToken(model.NewCredentialToken()),
		ReturnTo: "/join", ClientType: model.SessionClientWeb,
	}, time.Minute, model.HashInvitationClaim(model.NewCredentialToken())); !store.IsNotFound(err) {
		t.Fatalf("SaveInvitationAdmission(unknown claim) error = %v", err)
	}

	expired := &model.ExternalLoginState{
		Provider:    "campus-cas",
		StateHash:   model.HashToken(model.NewCredentialToken()),
		BindingHash: model.HashToken(model.NewCredentialToken()),
		ReturnTo:    "/", ClientType: model.SessionClientWeb,
	}
	expired, err = ss.ExternalLoginState().Save(ctx, expired, 2*time.Minute)
	requireNoError(t, err)
	pastExpiry := model.NowUTC().Add(-time.Minute)
	probe.Backdate(t, expired.ID, pastExpiry.Add(-2*time.Minute), pastExpiry)
	if _, err := ss.ExternalLoginState().Consume(
		ctx,
		expired.Provider,
		expired.StateHash,
		expired.BindingHash,
	); !store.IsNotFound(err) {
		t.Fatalf("Consume(expired) error = %v", err)
	}

	user := saveUser(t, ctx, ss)
	institution := saveInstitution(t, ctx, ss)
	audit, err := ss.Audit().Save(ctx, &model.AuditEvent{ActorID: user.ID,
		Action: string(model.ActionExternalIdentityManage), Resource: model.Resource{Type: model.ResourceUser, ID: user.ID.String()},
		ScopeType: model.RoleScopeInstitution, ScopeID: institution.ID.String(), Status: model.AuditStatusAttempt,
		NodeID: "external-login-maintenance-test"})
	requireNoError(t, err)
	abandoned, err := ss.ExternalLoginState().Save(ctx, &model.ExternalLoginState{Provider: "campus-cas",
		Purpose: model.ExternalAuthenticationPurposeConnect, TargetUserID: user.ID, AuditEventID: audit.ID.String(),
		StateHash: model.HashToken(model.NewCredentialToken()), BindingHash: model.HashToken(model.NewCredentialToken()),
		ReturnTo: "/account/security", ClientType: model.SessionClientWeb}, time.Minute)
	requireNoError(t, err)
	pastExpiry = model.NowUTC().Add(-time.Minute)
	probe.Backdate(t, abandoned.ID, pastExpiry.Add(-time.Minute), pastExpiry)
	maintained, err := ss.ExternalLoginState().Maintain(ctx, 1)
	requireNoError(t, err)
	if maintained.Terminalized != 1 || maintained.Purged != 0 || maintained.More {
		t.Fatalf("Maintain(abandoned) = %#v", maintained)
	}
	terminalAudit, err := ss.Audit().Get(ctx, audit.ID.String())
	requireNoError(t, err)
	if terminalAudit.Status != model.AuditStatusFail || terminalAudit.ErrorCode != "authentication.external.expired" {
		t.Fatalf("abandoned audit = %#v", terminalAudit)
	}
	retainedExpiry := model.NowUTC().Add(-25 * time.Hour)
	probe.Backdate(t, abandoned.ID, retainedExpiry.Add(-time.Minute), retainedExpiry)
	maintained, err = ss.ExternalLoginState().Maintain(ctx, 1)
	requireNoError(t, err)
	if maintained.Terminalized != 0 || maintained.Purged != 1 {
		t.Fatalf("Maintain(retained) = %#v", maintained)
	}
	if _, err = ss.ExternalLoginState().GetByStateHash(ctx, abandoned.StateHash); !store.IsNotFound(err) {
		t.Fatalf("purged state error = %v", err)
	}
	for _, invalidLimit := range []int{0, 1001} {
		if _, err = ss.ExternalLoginState().Maintain(ctx, invalidLimit); err == nil {
			t.Fatalf("Maintain(%d) accepted invalid limit", invalidLimit)
		}
	}
	for _, invalidLifetime := range []time.Duration{0, time.Minute - time.Millisecond, 30*time.Minute + time.Millisecond} {
		if _, err = ss.ExternalLoginState().Save(ctx, &model.ExternalLoginState{
			Provider: "campus-cas", StateHash: model.HashToken(model.NewCredentialToken()),
			BindingHash: model.HashToken(model.NewCredentialToken()), ReturnTo: "/", ClientType: model.SessionClientWeb,
		}, invalidLifetime); err == nil {
			t.Fatalf("Save(lifetime=%v) accepted invalid lifetime", invalidLifetime)
		}
	}
}
