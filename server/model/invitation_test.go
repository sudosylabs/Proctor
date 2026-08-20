// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package model

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"time"
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
	if err != nil {
		t.Fatal(err)
	}
	if invitation.TargetEmail != "student@example.edu" {
		t.Fatalf("normalized target email = %q", invitation.TargetEmail)
	}
	if invitation.Purpose != InvitationPurposeStudentClass || invitation.State != InvitationPending {
		t.Fatalf("purpose/state = %q/%q", invitation.Purpose, invitation.State)
	}
	if invitation.ClassID != classID || invitation.AcademicPeriodID != periodID ||
		invitation.ScopeType != RoleScopeClass || invitation.ScopeID != classID.String() {
		t.Fatalf("frozen target/scope = %#v", invitation)
	}
	if invitation.ExpiresAt != issuedAt.Add(InvitationLifetime) {
		t.Fatalf("expires_at = %v", invitation.ExpiresAt)
	}
	if invitation.ClaimHash == rawClaim || invitation.ClaimHash == HashToken(rawClaim) ||
		!IsValidTokenHash(invitation.ClaimHash) {
		t.Fatalf("claim hash is not a domain-separated persistent digest")
	}
	if invitation.Suggestions.Username != "student.one" ||
		invitation.Suggestions.DisplayName != " Student One " {
		t.Fatalf("normalized suggestions = %#v", invitation.Suggestions)
	}
	encoded, err := json.Marshal(invitation)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), rawClaim) || strings.Contains(string(encoded), invitation.ClaimHash) {
		t.Fatalf("Invitation JSON exposed claim material: %s", encoded)
	}
	if err := invitation.Validate(); err != nil {
		t.Fatalf("Validate() rejected constructed invitation: %v", err)
	}
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
	if err != nil {
		t.Fatal(err)
	}
	if invitation.Purpose != InvitationPurposeTeacherAcademicUnit || invitation.TargetEmail != "teacher@example.edu" ||
		invitation.AcademicUnitID != unitID || invitation.RoleID != roleID || invitation.ScopeType != RoleScopeAcademicUnit ||
		invitation.ScopeID != unitID.String() || !slices.Equal(invitation.RoleActions, []string{string(ActionAcademicUnitView), string(ActionProgrammeManage)}) {
		t.Fatalf("teacher Invitation package = %#v", invitation)
	}
	actions[0] = string(ActionJobManage)
	if !slices.Equal(invitation.RoleActions, []string{string(ActionAcademicUnitView), string(ActionProgrammeManage)}) {
		t.Fatal("teacher Invitation did not own its action snapshot")
	}
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
			if _, err := NewTeacherAcademicUnitInvitation(input); err == nil {
				t.Fatal("NewTeacherAcademicUnitInvitation() accepted invalid package")
			}
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
	if err != nil {
		t.Fatal(err)
	}
	userID, affiliationID := NewUserID(), NewAffiliationID()
	memberID, bindingID := NewAcademicUnitMemberID(), NewRoleBindingID()
	acceptedAt := issuedAt.Add(time.Minute)
	if err = invitation.AcceptTeacherAcademicUnit(userID, affiliationID, memberID, bindingID, acceptedAt); err != nil {
		t.Fatal(err)
	}
	if invitation.State != InvitationAccepted || invitation.AcceptedUserID != userID ||
		invitation.AcceptedAffiliationID != affiliationID || invitation.AcceptedAcademicUnitMemberID != memberID ||
		invitation.AcceptedRoleBindingID != bindingID || invitation.AcceptedClassMemberID.IsValid() {
		t.Fatalf("accepted teacher Invitation = %#v", invitation)
	}
	if err = invitation.Validate(); err != nil {
		t.Fatalf("Validate() rejected accepted teacher Invitation: %v", err)
	}
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
			if err != nil {
				t.Fatal(err)
			}
			if invitation.Purpose != test.purpose || invitation.TargetEmail != "existing@example.edu" ||
				invitation.AcademicUnitID != test.wantAcademicUnitID || invitation.RoleID != roleID ||
				invitation.ScopeType != test.scopeType || invitation.ScopeID != test.scopeID ||
				!slices.Equal(invitation.RoleActions, []string{string(ActionAcademicUnitView), string(ActionProgrammeManage)}) {
				t.Fatalf("scoped Role Invitation = %#v", invitation)
			}
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
	if err != nil {
		t.Fatal(err)
	}
	userID, bindingID := NewUserID(), NewRoleBindingID()
	if err = invitation.AcceptScopedRole(userID, bindingID, issuedAt.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if invitation.State != InvitationAccepted || invitation.AcceptedUserID != userID ||
		invitation.AcceptedRoleBindingID != bindingID || invitation.AcceptedAffiliationID.IsValid() ||
		invitation.AcceptedAcademicUnitMemberID.IsValid() || invitation.AcceptedClassMemberID.IsValid() {
		t.Fatalf("accepted scoped Role Invitation = %#v", invitation)
	}
	if err = invitation.Validate(); err != nil {
		t.Fatalf("Validate() rejected accepted scoped Role Invitation: %v", err)
	}
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
			if _, err := NewStudentClassInvitation(input); err == nil {
				t.Fatal("NewStudentClassInvitation() accepted invalid package")
			}
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
	if err != nil {
		t.Fatal(err)
	}
	acceptedAt := issuedAt.Add(time.Hour)
	userID := NewUserID()
	affiliationID, classMemberID := NewAffiliationID(), NewClassMemberID()
	if err := invitation.Accept(userID, affiliationID, classMemberID, acceptedAt); err != nil {
		t.Fatal(err)
	}
	if invitation.State != InvitationAccepted || !invitation.AcceptedAt.Valid ||
		invitation.AcceptedAt.Time != acceptedAt || invitation.AcceptedUserID != userID || invitation.AcceptedAffiliationID != affiliationID || invitation.AcceptedClassMemberID != classMemberID ||
		invitation.Revision != 2 {
		t.Fatalf("accepted invitation = %#v", invitation)
	}
	if err := invitation.Accept(userID, NewAffiliationID(), NewClassMemberID(), acceptedAt); err == nil {
		t.Fatal("Accept() accepted a replay as a second transition")
	}
	if err := invitation.Validate(); err != nil {
		t.Fatalf("Validate() rejected accepted invitation: %v", err)
	}
}

func TestInvitationExpireTerminalizesOnlyAnElapsedPendingInvitation(t *testing.T) {
	issuedAt := time.UnixMilli(1_800_000_000_000).UTC()
	classID := NewClassID()
	invitation, err := NewStudentClassInvitation(StudentClassInvitationInput{ID: NewInvitationID(), TargetEmail: "student@example.edu",
		ClassID: classID, AcademicPeriodID: NewAcademicPeriodID(), IntendedStartsAt: issuedAt,
		InviterUserID: NewUserID(), ScopeType: RoleScopeClass, ScopeID: classID.String(),
		ClaimHash: HashInvitationClaim(NewCredentialToken()), IssuedAt: issuedAt})
	if err != nil {
		t.Fatal(err)
	}
	if err = invitation.Expire(invitation.ExpiresAt.Add(-time.Nanosecond)); err == nil {
		t.Fatal("Expire() accepted a live Invitation")
	}
	if err = invitation.Expire(invitation.ExpiresAt); err != nil {
		t.Fatal(err)
	}
	if invitation.State != InvitationExpired || invitation.Revision != 2 || !invitation.UpdatedAt.Equal(invitation.ExpiresAt) {
		t.Fatalf("expired Invitation = %#v", invitation)
	}
	if err = invitation.Expire(invitation.ExpiresAt.Add(time.Second)); err == nil {
		t.Fatal("Expire() accepted a terminal Invitation")
	}
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
		if err != nil {
			t.Fatal(err)
		}
		return invitation
	}

	t.Run("resend rotates only a pending claim", func(t *testing.T) {
		invitation := newInvitation(t)
		previousHash := invitation.ClaimHash
		at := issuedAt.Add(time.Hour)
		if err := invitation.Resend(HashInvitationClaim(NewCredentialToken()), at); err != nil {
			t.Fatal(err)
		}
		if invitation.State != InvitationPending || invitation.ClaimHash == previousHash ||
			!invitation.UpdatedAt.Equal(at) || invitation.Revision != 2 ||
			!invitation.ExpiresAt.Equal(issuedAt.Add(InvitationLifetime)) {
			t.Fatalf("resent Invitation = %#v", invitation)
		}
		if err := invitation.Resend(invitation.ClaimHash, at.Add(time.Minute)); err == nil {
			t.Fatal("Resend() accepted the current claim hash")
		}
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
			if err := transition.apply(invitation, at); err != nil {
				t.Fatal(err)
			}
			if invitation.State != transition.state || !invitation.UpdatedAt.Equal(at) || invitation.Revision != 2 {
				t.Fatalf("terminal Invitation = %#v", invitation)
			}
			if err := transition.apply(invitation, at.Add(time.Minute)); err == nil {
				t.Fatal("terminal transition replay succeeded")
			}
		})
		t.Run(transition.name+" rejects elapsed pending", func(t *testing.T) {
			invitation := newInvitation(t)
			if err := transition.apply(invitation, invitation.ExpiresAt); err == nil || invitation.State != InvitationPending {
				t.Fatalf("elapsed terminal transition = %#v, %v", invitation, err)
			}
		})
	}

	t.Run("package equality excludes lifecycle identity and inviter provenance", func(t *testing.T) {
		first := newInvitation(t)
		second := *first
		second.ID, second.InviterUserID = NewInvitationID(), NewUserID()
		second.ClaimHash = HashInvitationClaim(NewCredentialToken())
		second.CreatedAt, second.UpdatedAt, second.ExpiresAt = issuedAt.Add(time.Minute), issuedAt.Add(time.Minute), issuedAt.Add(time.Minute).Add(InvitationLifetime)
		if !first.HasSamePackage(&second) {
			t.Fatal("HasSamePackage() treated identity or provenance as package data")
		}
		second.TargetEmail = "other@example.edu"
		if first.HasSamePackage(&second) {
			t.Fatal("HasSamePackage() ignored recipient change")
		}
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
	if err := delivery.Validate(); err != nil {
		t.Fatalf("Validate() rejected Invitation-targeted delivery: %v", err)
	}
	delivery.TargetUserID = NewUserID()
	if err := delivery.Validate(); err == nil {
		t.Fatal("Validate() accepted both User and Invitation targets")
	}
	delivery.TargetInvitationID = ""
	if err := delivery.Validate(); err != nil {
		t.Fatalf("Validate() rejected User-targeted delivery: %v", err)
	}
	delivery.TargetUserID = ""
	if err := delivery.Validate(); err == nil {
		t.Fatal("Validate() accepted a delivery without a recipient target")
	}
}

func TestTeacherInvitationMailIsCredentialOccurrence(t *testing.T) {
	t.Parallel()
	at := time.UnixMilli(1_800_000_000_000).UTC()
	occurrence := &MailOccurrence{ID: NewMailOccurrenceID(), Kind: MailOccurrenceInvitation,
		TemplateKey: MailTemplateAccessTeacherAcademicUnitInvitation, ActorUserID: NewUserID(), CreatedAt: at}
	if !MailTemplateAccessTeacherAcademicUnitInvitation.IsValid() || occurrence.Validate() != nil {
		t.Fatalf("teacher Invitation mail meaning is not registered: %#v", occurrence)
	}
}
