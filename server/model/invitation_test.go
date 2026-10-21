// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package model

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestNewStudentClassInvitationFreezesSafePackage(t *testing.T) {
	t.Parallel()

	issuedAt := time.UnixMilli(1_800_000_000_000).UTC()
	classID := NewClassID()
	periodID := NewAcademicPeriodID()
	rawClaim := NewCredentialToken()
	invitation, err := NewStudentClassInvitation(StudentClassInvitationInput{
		ID:               NewInvitationID(),
		TargetEmail:      "  Student@Example.EDU  ",
		ClassID:          classID,
		AcademicPeriodID: periodID,
		IntendedStartsAt: issuedAt.Add(-24 * time.Hour),
		IntendedEndsAt:   OptionalTimeFrom(issuedAt.Add(90 * 24 * time.Hour)),
		Suggestions: InvitationProfileSuggestions{
			Username: "STUDENT.ONE", DisplayName: " Student One ",
			FirstName: " Student ", LastName: " One ", Locale: "en-GB",
		},
		InviterUserID: NewUserID(),
		ScopeType:     RoleScopeClass,
		ScopeID:       classID.String(),
		ClaimHash:     HashInvitationClaim(rawClaim),
		IssuedAt:      issuedAt,
	})
	require.NoError(t, err)
	require.Equal(t, "student@example.edu", invitation.TargetEmail)
	require.Equal(t, InvitationPurposeStudentClass, invitation.Purpose, "purpose/state")
	require.Equal(t, InvitationPending, invitation.State, "purpose/state")
	require.Equal(t, classID, invitation.ClassID)
	require.Equal(t, periodID, invitation.AcademicPeriodID)
	require.Equal(t, RoleScopeClass, invitation.ScopeType)
	require.Equal(t, classID.String(), invitation.ScopeID)
	require.Equal(t, issuedAt.Add(InvitationLifetime), invitation.ExpiresAt)
	require.NotEqual(t, rawClaim, invitation.ClaimHash, "claim hash is not a domain-separated persistent digest")
	require.NotEqual(t, HashToken(rawClaim), invitation.ClaimHash, "claim hash is not a domain-separated persistent digest")
	require.True(t, IsValidTokenHash(invitation.ClaimHash), "claim hash is not a domain-separated persistent digest")
	require.Equal(t, "student.one", invitation.Suggestions.Username)
	require.Equal(t, " Student One ", invitation.Suggestions.DisplayName)
	encoded, err := json.Marshal(invitation)
	require.NoError(t, err)
	require.NotContains(t, string(encoded), rawClaim)
	require.NotContains(t, string(encoded), invitation.ClaimHash)
	require.NoError(t, invitation.Validate())
}

func TestNewTeacherAcademicUnitInvitationFreezesDelegablePackage(t *testing.T) {
	t.Parallel()

	issuedAt := time.UnixMilli(1_800_000_000_000).UTC()
	unitID, roleID := NewAcademicUnitID(), NewRoleID()
	actions := []string{string(ActionProgrammeManage), string(ActionAcademicUnitView)}
	invitation, err := NewTeacherAcademicUnitInvitation(TeacherAcademicUnitInvitationInput{
		ID: NewInvitationID(), TargetEmail: " Teacher@Example.EDU ", AcademicUnitID: unitID,
		RoleID: roleID, RoleActions: actions,
		IntendedStartsAt: issuedAt.Add(time.Hour), IntendedEndsAt: OptionalTimeFrom(issuedAt.Add(90 * 24 * time.Hour)),
		Suggestions:   InvitationProfileSuggestions{Username: "TEACHER.ONE", DisplayName: "Teacher One", Locale: "en"},
		InviterUserID: NewUserID(), ScopeType: RoleScopeAcademicUnit, ScopeID: unitID.String(),
		ClaimHash: HashInvitationClaim(NewCredentialToken()), IssuedAt: issuedAt,
	})
	require.NoError(t, err)
	require.Equal(t, InvitationPurposeTeacherAcademicUnit, invitation.Purpose)
	require.Equal(t, "teacher@example.edu", invitation.TargetEmail)
	require.Equal(t, unitID, invitation.AcademicUnitID)
	require.Equal(t, roleID, invitation.RoleID)
	require.Equal(t, RoleScopeAcademicUnit, invitation.ScopeType)
	require.Equal(t, unitID.String(), invitation.ScopeID)
	require.True(t, slices.Equal(invitation.RoleActions, []string{string(ActionAcademicUnitView), string(ActionProgrammeManage)}))
	actions[0] = string(ActionJobManage)
	require.True(t, slices.Equal(invitation.RoleActions, []string{string(ActionAcademicUnitView), string(ActionProgrammeManage)}), "teacher Invitation did not own its action snapshot")
}

func TestTeacherAcademicUnitInvitationRejectsIncompletePackage(t *testing.T) {
	t.Parallel()

	issuedAt := time.UnixMilli(1_800_000_000_000).UTC()
	unitID := NewAcademicUnitID()
	base := TeacherAcademicUnitInvitationInput{
		ID: NewInvitationID(), TargetEmail: "teacher@example.edu", AcademicUnitID: unitID, RoleID: NewRoleID(),
		RoleActions: []string{string(ActionAcademicUnitView)}, IntendedStartsAt: issuedAt,
		InviterUserID: NewUserID(), ScopeType: RoleScopeAcademicUnit, ScopeID: unitID.String(),
		ClaimHash: HashInvitationClaim(NewCredentialToken()), IssuedAt: issuedAt,
	}
	tests := []struct {
		name   string
		mutate func(*TeacherAcademicUnitInvitationInput)
	}{
		{name: "missing unit", mutate: func(input *TeacherAcademicUnitInvitationInput) { input.AcademicUnitID = "" }},
		{name: "missing role", mutate: func(input *TeacherAcademicUnitInvitationInput) { input.RoleID = "" }},
		{name: "missing actions", mutate: func(input *TeacherAcademicUnitInvitationInput) { input.RoleActions = nil }},
		{name: "unknown action", mutate: func(input *TeacherAcademicUnitInvitationInput) { input.RoleActions = []string{"unknown.action"} }},
		{name: "duplicate action", mutate: func(input *TeacherAcademicUnitInvitationInput) {
			input.RoleActions = []string{string(ActionAcademicUnitView), string(ActionAcademicUnitView)}
		}},
		{name: "wrong scope", mutate: func(input *TeacherAcademicUnitInvitationInput) { input.ScopeID = NewAcademicUnitID().String() }},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			input := base
			input.RoleActions = append([]string(nil), base.RoleActions...)
			test.mutate(&input)
			_, err := NewTeacherAcademicUnitInvitation(input)
			require.Error(t, err, "NewTeacherAcademicUnitInvitation() accepted invalid package")
		})
	}
}

func TestTeacherAcademicUnitInvitationAcceptFreezesCompleteOutcome(t *testing.T) {
	t.Parallel()

	issuedAt := time.UnixMilli(1_800_000_000_000).UTC()
	unitID := NewAcademicUnitID()
	invitation, err := NewTeacherAcademicUnitInvitation(TeacherAcademicUnitInvitationInput{
		ID: NewInvitationID(), TargetEmail: "teacher@example.edu", AcademicUnitID: unitID, RoleID: NewRoleID(),
		RoleActions: []string{string(ActionAcademicUnitView)}, IntendedStartsAt: issuedAt,
		InviterUserID: NewUserID(), ScopeType: RoleScopeAcademicUnit, ScopeID: unitID.String(),
		ClaimHash: HashInvitationClaim(NewCredentialToken()), IssuedAt: issuedAt,
	})
	require.NoError(t, err)
	userID, affiliationID := NewUserID(), NewAffiliationID()
	memberID, bindingID := NewAcademicUnitMemberID(), NewRoleBindingID()
	acceptedAt := issuedAt.Add(time.Minute)
	err = invitation.AcceptTeacherAcademicUnit(userID, affiliationID, memberID, bindingID, acceptedAt)
	require.NoError(t, err)
	require.Equal(t, InvitationAccepted, invitation.State)
	require.Equal(t, userID, invitation.AcceptedUserID)
	require.Equal(t, affiliationID, invitation.AcceptedAffiliationID)
	require.Equal(t, memberID, invitation.AcceptedAcademicUnitMemberID)
	require.Equal(t, bindingID, invitation.AcceptedRoleBindingID)
	require.False(t, invitation.AcceptedClassMemberID.IsValid())
	err = invitation.Validate()
	require.NoError(t, err)
}

func TestNewScopedRoleInvitationFreezesExactPurposeAndScope(t *testing.T) {
	t.Parallel()

	issuedAt := time.UnixMilli(1_800_000_000_000).UTC()
	unitID, institutionID, roleID := NewAcademicUnitID(), NewInstitutionID(), NewRoleID()
	actions := []string{string(ActionProgrammeManage), string(ActionAcademicUnitView)}
	tests := []struct {
		name               string
		purpose            InvitationPurpose
		academicUnitID     AcademicUnitID
		scopeType          RoleScopeType
		scopeID            string
		wantAcademicUnitID AcademicUnitID
	}{
		{name: "academic unit", purpose: InvitationPurposeAcademicUnitRole, academicUnitID: unitID,
			scopeType: RoleScopeAcademicUnit, scopeID: unitID.String(), wantAcademicUnitID: unitID},
		{name: "institution", purpose: InvitationPurposeInstitutionRole,
			scopeType: RoleScopeInstitution, scopeID: institutionID.String()},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			invitation, err := NewScopedRoleInvitation(ScopedRoleInvitationInput{
				ID: NewInvitationID(), Purpose: test.purpose, TargetEmail: " Existing@Example.EDU ",
				AcademicUnitID: test.academicUnitID, RoleID: roleID, RoleActions: actions,
				IntendedStartsAt: issuedAt.Add(time.Hour), IntendedEndsAt: OptionalTimeFrom(issuedAt.Add(30 * 24 * time.Hour)),
				InviterUserID: NewUserID(), ScopeType: test.scopeType, ScopeID: test.scopeID,
				ClaimHash: HashInvitationClaim(NewCredentialToken()), IssuedAt: issuedAt,
			})
			require.NoError(t, err)
			require.Equal(t, test.purpose, invitation.Purpose)
			require.Equal(t, "existing@example.edu", invitation.TargetEmail)
			require.Equal(t, test.wantAcademicUnitID, invitation.AcademicUnitID)
			require.Equal(t, roleID, invitation.RoleID)
			require.Equal(t, test.scopeType, invitation.ScopeType)
			require.Equal(t, test.scopeID, invitation.ScopeID)
			require.True(t, slices.Equal(invitation.RoleActions, []string{string(ActionAcademicUnitView), string(ActionProgrammeManage)}))
		})
	}
}

func TestScopedRoleInvitationAcceptRecordsOnlyExistingUserAndBinding(t *testing.T) {
	t.Parallel()

	issuedAt := time.UnixMilli(1_800_000_000_000).UTC()
	unitID := NewAcademicUnitID()
	invitation, err := NewScopedRoleInvitation(ScopedRoleInvitationInput{
		ID: NewInvitationID(), Purpose: InvitationPurposeAcademicUnitRole, TargetEmail: "existing@example.edu",
		AcademicUnitID: unitID, RoleID: NewRoleID(), RoleActions: []string{string(ActionAcademicUnitView)},
		IntendedStartsAt: issuedAt, InviterUserID: NewUserID(), ScopeType: RoleScopeAcademicUnit, ScopeID: unitID.String(),
		ClaimHash: HashInvitationClaim(NewCredentialToken()), IssuedAt: issuedAt,
	})
	require.NoError(t, err)
	userID, bindingID := NewUserID(), NewRoleBindingID()
	err = invitation.AcceptScopedRole(userID, bindingID, issuedAt.Add(time.Minute))
	require.NoError(t, err)
	require.Equal(t, InvitationAccepted, invitation.State)
	require.Equal(t, userID, invitation.AcceptedUserID)
	require.Equal(t, bindingID, invitation.AcceptedRoleBindingID)
	require.False(t, invitation.AcceptedAffiliationID.IsValid())
	require.False(t, invitation.AcceptedAcademicUnitMemberID.IsValid())
	require.False(t, invitation.AcceptedClassMemberID.IsValid())
	err = invitation.Validate()
	require.NoError(t, err)
}

func TestStudentClassInvitationRejectsInvalidFrozenPackage(t *testing.T) {
	t.Parallel()

	issuedAt := time.UnixMilli(1_800_000_000_000).UTC()
	classID := NewClassID()
	base := StudentClassInvitationInput{
		ID: NewInvitationID(), TargetEmail: "student@example.edu",
		ClassID: classID, AcademicPeriodID: NewAcademicPeriodID(),
		IntendedStartsAt: issuedAt, InviterUserID: NewUserID(),
		ScopeType: RoleScopeClass, ScopeID: classID.String(),
		ClaimHash: HashInvitationClaim(NewCredentialToken()), IssuedAt: issuedAt,
	}
	tests := []struct {
		name   string
		mutate func(*StudentClassInvitationInput)
	}{
		{name: "invalid email", mutate: func(input *StudentClassInvitationInput) { input.TargetEmail = "not-an-email" }},
		{name: "missing period", mutate: func(input *StudentClassInvitationInput) { input.AcademicPeriodID = "" }},
		{name: "scope is not exact class", mutate: func(input *StudentClassInvitationInput) { input.ScopeID = NewClassID().String() }},
		{name: "invalid claim hash", mutate: func(input *StudentClassInvitationInput) { input.ClaimHash = "raw" }},
		{name: "elapsed intended interval", mutate: func(input *StudentClassInvitationInput) {
			input.IntendedEndsAt = OptionalTimeFrom(input.IntendedStartsAt)
		}},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			input := base
			test.mutate(&input)
			_, err := NewStudentClassInvitation(input)
			require.Error(t, err, "NewStudentClassInvitation() accepted invalid package")
		})
	}
}

func TestInvitationAcceptIsSingleTerminalTransition(t *testing.T) {
	t.Parallel()

	issuedAt := time.UnixMilli(1_800_000_000_000).UTC()
	classID := NewClassID()
	invitation, err := NewStudentClassInvitation(StudentClassInvitationInput{
		ID: NewInvitationID(), TargetEmail: "student@example.edu",
		ClassID: classID, AcademicPeriodID: NewAcademicPeriodID(),
		IntendedStartsAt: issuedAt, InviterUserID: NewUserID(),
		ScopeType: RoleScopeClass, ScopeID: classID.String(),
		ClaimHash: HashInvitationClaim(NewCredentialToken()), IssuedAt: issuedAt,
	})
	require.NoError(t, err)
	acceptedAt := issuedAt.Add(time.Hour)
	userID := NewUserID()
	affiliationID, classMemberID := NewAffiliationID(), NewClassMemberID()
	require.NoError(t, invitation.Accept(userID, affiliationID, classMemberID, acceptedAt))
	require.Equal(t, InvitationAccepted, invitation.State)
	require.True(t, invitation.AcceptedAt.Valid)
	require.Equal(t, acceptedAt, invitation.AcceptedAt.Time)
	require.Equal(t, userID, invitation.AcceptedUserID)
	require.Equal(t, affiliationID, invitation.AcceptedAffiliationID)
	require.Equal(t, classMemberID, invitation.AcceptedClassMemberID)
	require.Equal(t, int64(2), invitation.Revision)
	require.Error(t, invitation.Accept(userID, NewAffiliationID(), NewClassMemberID(), acceptedAt), "Accept() accepted a replay as a second transition")
	require.NoError(t, invitation.Validate())
}

func TestInvitationExpireTerminalizesOnlyAnElapsedPendingInvitation(t *testing.T) {
	issuedAt := time.UnixMilli(1_800_000_000_000).UTC()
	classID := NewClassID()
	invitation, err := NewStudentClassInvitation(StudentClassInvitationInput{ID: NewInvitationID(), TargetEmail: "student@example.edu",
		ClassID: classID, AcademicPeriodID: NewAcademicPeriodID(), IntendedStartsAt: issuedAt,
		InviterUserID: NewUserID(), ScopeType: RoleScopeClass, ScopeID: classID.String(),
		ClaimHash: HashInvitationClaim(NewCredentialToken()), IssuedAt: issuedAt})
	require.NoError(t, err)
	err = invitation.Expire(invitation.ExpiresAt.Add(-time.Nanosecond))
	require.Error(t, err, "Expire() accepted a live Invitation")
	err = invitation.Expire(invitation.ExpiresAt)
	require.NoError(t, err)
	require.Equal(t, InvitationExpired, invitation.State)
	require.Equal(t, int64(2), invitation.Revision)
	require.True(t, invitation.UpdatedAt.Equal(invitation.ExpiresAt))
	err = invitation.Expire(invitation.ExpiresAt.Add(time.Second))
	require.Error(t, err, "Expire() accepted a terminal Invitation")
}

func TestInvitationAdministrativeLifecycleTransitions(t *testing.T) {
	issuedAt := time.UnixMilli(1_800_000_000_000).UTC()
	newInvitation := func(t *testing.T) *Invitation {
		t.Helper()
		classID := NewClassID()
		invitation, err := NewStudentClassInvitation(StudentClassInvitationInput{
			ID: NewInvitationID(), TargetEmail: "student@example.edu",
			ClassID: classID, AcademicPeriodID: NewAcademicPeriodID(), IntendedStartsAt: issuedAt,
			InviterUserID: NewUserID(), ScopeType: RoleScopeClass, ScopeID: classID.String(),
			ClaimHash: HashInvitationClaim(NewCredentialToken()), IssuedAt: issuedAt,
		})
		require.NoError(t, err)
		return invitation
	}

	t.Run("resend rotates only a pending claim", func(t *testing.T) {
		invitation := newInvitation(t)
		previousHash := invitation.ClaimHash
		at := issuedAt.Add(time.Hour)
		require.NoError(t, invitation.Resend(HashInvitationClaim(NewCredentialToken()), at))
		require.Equal(t, InvitationPending, invitation.State)
		require.NotEqual(t, previousHash, invitation.ClaimHash)
		require.True(t, invitation.UpdatedAt.Equal(at))
		require.Equal(t, int64(2), invitation.Revision)
		require.True(t, invitation.ExpiresAt.Equal(issuedAt.Add(InvitationLifetime)))
		require.Error(t, invitation.Resend(invitation.ClaimHash, at.Add(time.Minute)), "Resend() accepted the current claim hash")
	})

	for _, transition := range []struct {
		name  string
		state InvitationState
		apply func(*Invitation, time.Time) error
	}{
		{name: "revoke", state: InvitationRevoked, apply: (*Invitation).Revoke},
		{name: "supersede", state: InvitationSuperseded, apply: (*Invitation).Supersede},
	} {
		t.Run(transition.name+" terminalizes pending", func(t *testing.T) {
			invitation := newInvitation(t)
			at := issuedAt.Add(time.Hour)
			require.NoError(t, transition.apply(invitation, at))
			require.Equal(t, transition.state, invitation.State)
			require.True(t, invitation.UpdatedAt.Equal(at))
			require.Equal(t, int64(2), invitation.Revision)
			require.Error(t, transition.apply(invitation, at.Add(time.Minute)), "terminal transition replay succeeded")
		})
		t.Run(transition.name+" rejects elapsed pending", func(t *testing.T) {
			invitation := newInvitation(t)
			require.Error(t, transition.apply(invitation, invitation.ExpiresAt), "elapsed terminal transition = %#v", invitation)
			require.Equal(t, InvitationPending, invitation.State, "elapsed terminal transition = %#v", invitation)
		})
	}

	t.Run("package equality excludes lifecycle identity and inviter provenance", func(t *testing.T) {
		first := newInvitation(t)
		second := *first
		second.ID, second.InviterUserID = NewInvitationID(), NewUserID()
		second.ClaimHash = HashInvitationClaim(NewCredentialToken())
		second.CreatedAt, second.UpdatedAt, second.ExpiresAt = issuedAt.Add(time.Minute), issuedAt.Add(time.Minute), issuedAt.Add(time.Minute).Add(InvitationLifetime)
		require.True(t, first.HasSamePackage(&second), "HasSamePackage() treated identity or provenance as package data")
		second.TargetEmail = "other@example.edu"
		require.False(t, first.HasSamePackage(&second), "HasSamePackage() ignored recipient change")
	})
}

func TestMailDeliveryTargetsExactlyOneUserOrInvitation(t *testing.T) {
	t.Parallel()

	at := time.UnixMilli(1_800_000_000_000).UTC()
	delivery := &MailDelivery{
		ID: NewMailDeliveryID(), OccurrenceID: NewMailOccurrenceID(), JobID: NewJobID(),
		TargetInvitationID: NewInvitationID(),
		TemplateKey:        MailTemplateAccessStudentClassInvitation,
		TemplateDigest:     strings.Repeat("a", 64), MaskedRecipient: "s***@example.edu",
		State: MailDeliveryQueued, CreatedAt: at, UpdatedAt: at, MessageDate: at,
		Deadline: at.Add(InvitationLifetime), MessageID: "<mail@example.edu>",
		EncryptedPayload: json.RawMessage(`{"key_id":"0123456789abcdef0123456789abcdef"}`),
		Revision:         1,
	}
	require.NoError(t, delivery.Validate())
	delivery.TargetUserID = NewUserID()
	require.Error(t, delivery.Validate(), "Validate() accepted both User and Invitation targets")
	delivery.TargetInvitationID = ""
	require.NoError(t, delivery.Validate())
	delivery.TargetUserID = ""
	require.Error(t, delivery.Validate(), "Validate() accepted a delivery without a recipient target")
}

func TestTeacherInvitationMailIsCredentialOccurrence(t *testing.T) {
	t.Parallel()
	at := time.UnixMilli(1_800_000_000_000).UTC()
	occurrence := &MailOccurrence{ID: NewMailOccurrenceID(), Kind: MailOccurrenceInvitation,
		TemplateKey: MailTemplateAccessTeacherAcademicUnitInvitation, ActorUserID: NewUserID(), CreatedAt: at}
	require.True(t, MailTemplateAccessTeacherAcademicUnitInvitation.IsValid())
	require.NoError(t, occurrence.Validate())
}
