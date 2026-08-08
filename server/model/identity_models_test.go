// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package model

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestUserNormalizationAndContextualRelationships(t *testing.T) {
	t.Parallel()

	u := &User{
		Username:    "Alex.Morgan",
		Email:       " ALEX.MORGAN@EXAMPLE.EDU ",
		DisplayName: "Alex Morgan",
	}
	u.PrepareCreate(NewUserID(), NowUTC())
	if err := u.Validate(); err != nil {
		t.Fatal(err)
	}
	if u.Username != "alex.morgan" || u.Email != "alex.morgan@example.edu" {
		t.Fatalf("normalized user = %#v", u)
	}
	if u.Locale != DefaultLocale || u.Timezone != DefaultTimezone {
		t.Fatalf("defaults: locale=%q timezone=%q", u.Locale, u.Timezone)
	}

	at := NowUTC()
	student := &Affiliation{UserID: u.ID, Kind: AffiliationStudent}
	teacher := &Affiliation{UserID: u.ID, Kind: AffiliationTeacher}
	student.PrepareCreate(NewAffiliationID(), at)
	teacher.PrepareCreate(NewAffiliationID(), at)
	if err := student.Validate(); err != nil {
		t.Fatal(err)
	}
	if err := teacher.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestExternalIdentityPreservesOpaqueSubject(t *testing.T) {
	t.Parallel()

	const subject = "CaseSensitive/Provider/Subject"
	identity := &ExternalIdentity{
		UserID:   UserID(NewId()),
		Provider: "OIDC",
		Subject:  subject,
	}
	identity.PrepareCreate(NewExternalIdentityID(), NowUTC())
	if identity.Provider != "oidc" || identity.Subject != subject {
		t.Fatalf("identity changed unexpectedly: %#v", identity)
	}
	if err := identity.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestRoleAndRoleBindingTypedLifecycle(t *testing.T) {
	t.Parallel()

	at := NowUTC()
	role := &Role{
		Name:        "department_teacher",
		DisplayName: "Department Teacher",
		Permissions: []string{"class.view", "class.members.view"},
	}
	role.PrepareCreate(NewRoleID(), at)
	if err := role.Validate(); err != nil {
		t.Fatalf("role Validate() = %v", err)
	}
	if role.IsArchived() {
		t.Fatal("new role should not be archived")
	}
	clone := role.Clone()
	clone.Permissions[0] = "class.members.manage"
	if role.Permissions[0] != "class.view" {
		t.Fatal("Clone exposed role permission storage")
	}
	audit := role.Auditable()
	audit["permissions"].([]string)[0] = "class.members.manage"
	if role.Auditable()["permissions"].([]string)[0] != "class.view" {
		t.Fatal("Auditable exposed role permission storage")
	}

	binding := &RoleBinding{
		UserID:    NewUserID(),
		RoleID:    role.ID,
		ScopeType: RoleScopeAcademicUnit,
		ScopeID:   NewId(),
	}
	binding.PrepareCreate(NewRoleBindingID(), at)
	if err := binding.Validate(); err != nil {
		t.Fatalf("binding Validate() = %v", err)
	}
	if !binding.IsActiveAt(at) {
		t.Fatal("binding should be active at create time")
	}
	if err := binding.End(at.Add(time.Hour)); err != nil {
		t.Fatalf("End() = %v", err)
	}
	if binding.IsActiveAt(at.Add(time.Hour)) {
		t.Fatal("binding should not be active at exclusive end time")
	}
}

func TestInstallationStateTypedLifecycle(t *testing.T) {
	t.Parallel()

	state := &InstallationState{
		InitializedAt:       NowUTC(),
		InstitutionID:       NewInstitutionID(),
		AdministratorUserID: NewUserID(),
	}
	if err := state.Validate(); err != nil {
		t.Fatalf("Validate() = %v", err)
	}
	fields := state.Auditable()
	if fields["institution_id"] != state.InstitutionID.String() {
		t.Fatalf("Auditable() = %#v", fields)
	}
}

func TestSessionAndCredentialExpiryAndRotation(t *testing.T) {
	t.Parallel()

	beforeCreate := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	s := &Session{
		UserID:                 NewUserID(),
		ClientType:             SessionClientCLI,
		AuthenticationMethod:   "password",
		AuthenticationStrength: AuthenticationMultiFactor,
		AuthenticatedAt:        beforeCreate,
		MFACompletedAt:         OptionalTimeFrom(beforeCreate),
		IdleExpiresAt:          beforeCreate.Add(time.Minute),
		ExpiresAt:              beforeCreate.Add(2 * time.Minute),
	}
	s.PrepareCreate(NewSessionID(), beforeCreate)
	if err := s.Validate(); err != nil {
		t.Fatal(err)
	}
	if !s.LastActivityAt.Equal(s.CreatedAt) {
		t.Fatalf(
			"PrepareCreate() last_activity_at = %v, created_at = %v",
			s.LastActivityAt,
			s.CreatedAt,
		)
	}
	if s.IsExpiredAt(s.IdleExpiresAt.Add(-time.Millisecond)) {
		t.Fatal("session expired before its idle deadline")
	}
	if !s.IsExpiredAt(s.IdleExpiresAt) {
		t.Fatal("session did not expire at its idle deadline")
	}

	refresh := &SessionCredential{
		SessionID: s.ID,
		Kind:      SessionCredentialRefresh,
		TokenHash: HashToken(NewCredentialToken()),
		ExpiresAt: s.ExpiresAt,
	}
	refresh.PrepareCreate(NewSessionCredentialID(), beforeCreate)
	if !IsValidId(refresh.FamilyID) {
		t.Fatalf("refresh family id = %q", refresh.FamilyID)
	}
	if err := refresh.Validate(); err != nil {
		t.Fatal(err)
	}

	access := &SessionCredential{
		SessionID: s.ID,
		Kind:      SessionCredentialAccess,
		TokenHash: HashToken(NewCredentialToken()),
		FamilyID:  NewId(),
		ExpiresAt: s.ExpiresAt,
	}
	access.PrepareCreate(NewSessionCredentialID(), beforeCreate)
	if err := access.Validate(); err == nil ||
		err.(*ValidationError).Code != "model.session_credential.is_valid.kind.app_error" {
		t.Fatalf("access credential error = %v", err)
	}

	token := &PersonalAccessToken{
		UserID:      NewUserID(),
		Description: "Automation on my workstation",
		TokenHash:   HashToken(NewCredentialToken()),
		Scopes:      []string{"class.view"},
		ExpiresAt:   beforeCreate.Add(24 * time.Hour),
	}
	token.PrepareCreate(NewPersonalAccessTokenID(), beforeCreate)
	if err := token.Validate(); err != nil {
		t.Fatal(err)
	}
	if !token.IsActiveAt(beforeCreate.Add(time.Hour)) {
		t.Fatal("token should be active before expiry")
	}
	if token.IsActiveAt(token.ExpiresAt) {
		t.Fatal("token should be inactive at expiry")
	}
}

func TestCredentialSecretsAreExcludedFromJSON(t *testing.T) {
	t.Parallel()

	tokenHash := HashToken(NewCredentialToken())
	values := []any{
		&ExternalIdentity{Subject: "private-subject"},
		&PasswordCredential{PasswordHash: "private-password-hash"},
		&SessionCredential{TokenHash: tokenHash},
		&PersonalAccessToken{TokenHash: tokenHash},
		&UserToken{TokenHash: tokenHash},
	}
	for _, value := range values {
		encoded, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		output := string(encoded)
		for _, secret := range []string{"private-subject", "private-password-hash", tokenHash} {
			if strings.Contains(output, secret) {
				t.Fatalf("%T JSON exposes secret: %s", value, output)
			}
		}
	}
}

func TestCredentialTokenGenerationAndHashing(t *testing.T) {
	t.Parallel()

	first := NewCredentialToken()
	second := NewCredentialToken()
	if first == second || len(first) < 40 {
		t.Fatalf("generated credentials have unexpected shape")
	}
	if HashToken(first) == HashToken(second) {
		t.Fatal("different credentials produced the same hash")
	}
	if !IsValidTokenHash(HashToken(first)) {
		t.Fatal("HashToken returned an invalid persistent hash")
	}
}

func TestClassMemberRetainsClosedEnrollmentHistory(t *testing.T) {
	t.Parallel()

	at := time.UnixMilli(1_700_000_000_000).UTC()
	m := &ClassMember{
		ClassID:          ClassID(NewId()),
		AcademicPeriodID: AcademicPeriodID(NewId()),
		UserID:           UserID(NewId()),
	}
	m.PrepareCreate(NewClassMemberID(), at)
	if !m.IsActiveAt(m.StartsAt) {
		t.Fatal("new class membership is not active")
	}

	endAt := m.StartsAt.Add(time.Millisecond)
	if err := m.End(endAt); err != nil {
		t.Fatal(err)
	}
	if m.IsActiveAt(endAt) {
		t.Fatal("closed class membership remained active")
	}
	if m.IsArchived() {
		t.Fatal("closing enrollment deleted its historical record")
	}
}

func TestMembershipModelsTypedLifecycle(t *testing.T) {
	t.Parallel()

	at := time.UnixMilli(1_700_000_000_000).UTC()
	userID := UserID(NewId())

	affiliation := &Affiliation{UserID: userID, Kind: AffiliationTeacher}
	affiliation.PrepareCreate(NewAffiliationID(), at)
	if err := affiliation.Validate(); err != nil {
		t.Fatalf("Affiliation.Validate() = %v", err)
	}
	if !affiliation.IsActiveAt(at) {
		t.Fatal("affiliation should be active at create time")
	}
	audit := affiliation.Auditable()
	if audit["id"] != affiliation.ID.String() || audit["start_at"] != MillisFromTime(at) || audit["end_at"] != int64(0) {
		t.Fatalf("affiliation audit = %#v", audit)
	}

	member := &AcademicUnitMember{
		AcademicUnitID: AcademicUnitID(NewId()),
		UserID:         userID,
	}
	member.PrepareCreate(NewAcademicUnitMemberID(), at)
	if err := member.Validate(); err != nil {
		t.Fatalf("AcademicUnitMember.Validate() = %v", err)
	}
	if !member.IsActiveAt(at) {
		t.Fatal("academic unit member should be active at create time")
	}

	classMember := &ClassMember{
		ClassID:          ClassID(NewId()),
		AcademicPeriodID: AcademicPeriodID(NewId()),
		UserID:           userID,
	}
	classMember.PrepareCreate(NewClassMemberID(), at)
	if err := classMember.Validate(); err != nil {
		t.Fatalf("ClassMember.Validate() = %v", err)
	}
}

func TestIdentityModelsTypedLifecycle(t *testing.T) {
	t.Parallel()

	at := time.UnixMilli(1_700_000_000_000).UTC()
	userID := NewUserID()

	user := &User{
		Username: "alex.morgan", Email: "alex.morgan@example.edu", DisplayName: "Alex",
	}
	user.PrepareCreate(userID, at)
	if err := user.Validate(); err != nil {
		t.Fatalf("User.Validate() = %v", err)
	}
	if !user.IsActive() || user.IsArchived() {
		t.Fatal("new user should be active")
	}

	credential := &PasswordCredential{
		UserID: userID, PasswordHash: "$argon2id$v=19$m=65536,t=3,p=4$c2FsdA$aGFzaA",
	}
	credential.PrepareCreate(NewPasswordCredentialID(), at)
	if err := credential.Validate(); err != nil {
		t.Fatalf("PasswordCredential.Validate() = %v", err)
	}

	identity := &ExternalIdentity{
		UserID: userID, Provider: "OIDC", Subject: "opaque-subject",
		LastSeenAt: OptionalTimeFrom(at),
	}
	identity.PrepareCreate(NewExternalIdentityID(), at)
	if identity.Provider != "oidc" {
		t.Fatalf("provider = %q", identity.Provider)
	}
	if err := identity.Validate(); err != nil {
		t.Fatalf("ExternalIdentity.Validate() = %v", err)
	}

	tokenHash := HashToken(NewCredentialToken())
	token := &UserToken{
		UserID: userID, Purpose: UserTokenEmailVerification,
		TokenHash: tokenHash, Target: "alex.morgan@example.edu",
		ExpiresAt: at.Add(time.Hour),
	}
	token.PrepareCreate(NewUserTokenID(), at)
	if err := token.Validate(); err != nil {
		t.Fatalf("UserToken.Validate() = %v", err)
	}
	if !token.IsActiveAt(at) || token.IsActiveAt(token.ExpiresAt) {
		t.Fatal("token active window is wrong")
	}

	state := &ExternalLoginState{
		Provider: "campus-cas", StateHash: tokenHash, BindingHash: tokenHash,
		ReturnTo: "/", ClientType: SessionClientWeb, ExpiresAt: at.Add(time.Minute),
	}
	state.PrepareCreate(NewExternalLoginStateID(), at)
	if err := state.Validate(); err != nil {
		t.Fatalf("ExternalLoginState.Validate() = %v", err)
	}
}

func TestSecurityModelValidationReturnsPreciseErrors(t *testing.T) {
	t.Parallel()

	now := GetMillis()
	tests := []struct {
		name string
		err  error
		code string
	}{
		{
			name: "duplicate role permission",
			err: func() error {
				r := &Role{
					Name:        "viewer",
					DisplayName: "Viewer",
					Permissions: []string{"class.view", "class.view"},
				}
				r.PrepareCreate(NewRoleID(), NowUTC())
				return r.Validate()
			}(),
			code: "model.role.is_valid.permissions.app_error",
		},
		{
			name: "personal token without expiry",
			err: func() error {
				token := &PersonalAccessToken{
					UserID:      NewUserID(),
					Description: "CLI",
					TokenHash:   HashToken(NewCredentialToken()),
					Scopes:      []string{"class.view"},
				}
				token.PrepareCreate(NewPersonalAccessTokenID(), NowUTC())
				return token.Validate()
			}(),
			code: "model.personal_access_token.is_valid.expires_at.app_error",
		},
		{
			name: "session idle deadline after absolute deadline",
			err: func() error {
				at := TimeFromMillis(now)
				s := &Session{
					UserID:                 NewUserID(),
					ClientType:             SessionClientDesktop,
					AuthenticationMethod:   "oidc",
					AuthenticationStrength: AuthenticationSingleFactor,
					IdleExpiresAt:          at.Add(120 * time.Second),
					ExpiresAt:              at.Add(60 * time.Second),
				}
				s.PrepareCreate(NewSessionID(), at)
				return s.Validate()
			}(),
			code: "model.session.is_valid.idle_expires_at.app_error",
		},
		{
			name: "external subject with direction control",
			err: func() error {
				identity := &ExternalIdentity{
					UserID:   UserID(NewId()),
					Provider: "oidc",
					Subject:  "subject\u202E",
				}
				identity.PrepareCreate(NewExternalIdentityID(), NowUTC())
				return identity.Validate()
			}(),
			code: "model.external_identity.is_valid.subject.app_error",
		},
		{
			name: "class membership without period",
			err: func() error {
				member := &ClassMember{
					ClassID: ClassID(NewId()),
					UserID:  UserID(NewId()),
				}
				member.PrepareCreate(NewClassMemberID(), NowUTC())
				return member.Validate()
			}(),
			code: "model.class_member.is_valid.academic_period_id.app_error",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if test.err == nil || test.err.(*ValidationError).Code != test.code {
				t.Fatalf("error = %v, want %q", test.err, test.code)
			}
		})
	}
}

func TestUserAuditOmitsProfilePII(t *testing.T) {
	t.Parallel()

	u := &User{
		Username:    "alex.morgan",
		Email:       "alex.morgan@example.edu",
		DisplayName: "Alex Morgan",
		FirstName:   "Alex",
		LastName:    "Morgan",
	}
	u.PrepareCreate(NewUserID(), NowUTC())
	audit := u.Auditable()
	for _, field := range []string{"email", "display_name", "first_name", "last_name"} {
		if _, exposed := audit[field]; exposed {
			t.Fatalf("user audit exposes %q: %#v", field, audit)
		}
	}
}
