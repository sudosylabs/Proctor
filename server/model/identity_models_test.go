// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package model

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestIdentityModelsImplementLifecycleContract(t *testing.T) {
	t.Parallel()

	now := GetMillis()
	userID := NewId()
	unitID := NewId()
	roleID := NewId()
	sessionID := NewId()
	tokenHash := HashToken(NewCredentialToken())

	// Affiliation, AcademicUnitMember, and ClassMember use the typed
	// PrepareCreate/Validate lifecycle; see membership-specific tests below.
	tests := []struct {
		name  string
		model persistentModel
	}{
		{
			name: "role",
			model: &Role{
				Name:        "department_teacher",
				DisplayName: "Department Teacher",
				Permissions: []string{"class.view", "class.members.view"},
			},
		},
		{
			name: "role binding",
			model: &RoleBinding{
				UserId:    userID,
				RoleId:    roleID,
				ScopeType: RoleScopeAcademicUnit,
				ScopeId:   unitID,
			},
		},
		{
			name: "session",
			model: &Session{
				UserId:                 userID,
				ClientType:             SessionClientDesktop,
				AuthenticationMethod:   "oidc",
				AuthenticationStrength: AuthenticationSingleFactor,
				IdleExpiresAt:          now + int64(time.Hour/time.Millisecond),
				ExpiresAt:              now + int64((2*time.Hour)/time.Millisecond),
			},
		},
		{
			name: "session credential",
			model: &SessionCredential{
				SessionId: sessionID,
				Kind:      SessionCredentialAccess,
				TokenHash: tokenHash,
				ExpiresAt: now + int64(time.Hour/time.Millisecond),
			},
		},
		{
			name: "personal access token",
			model: &PersonalAccessToken{
				UserId:      userID,
				Description: "Automation on my workstation",
				TokenHash:   tokenHash,
				Scopes:      []string{"class.view"},
				ExpiresAt:   now + int64((24*time.Hour)/time.Millisecond),
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			test.model.PreSave()
			if appErr := test.model.IsValid(); appErr != nil {
				t.Fatalf("model is invalid after PreSave: %v", appErr)
			}
			audit := test.model.Auditable()
			if !IsValidId(audit["id"].(string)) {
				t.Fatalf("audit fields = %#v", audit)
			}
			for _, forbidden := range []string{"password", "password_hash", "token", "token_hash", "subject"} {
				if _, exposed := audit[forbidden]; exposed {
					t.Fatalf("audit fields expose %q: %#v", forbidden, audit)
				}
			}
			test.model.PreUpdate()
			if appErr := test.model.IsValid(); appErr != nil {
				t.Fatalf("model is invalid after PreUpdate: %v", appErr)
			}
		})
	}
}

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

func TestRoleAndAuditCopiesDoNotExposeMutablePermissions(t *testing.T) {
	t.Parallel()

	r := &Role{
		Name:        "class_viewer",
		DisplayName: "Class Viewer",
		Permissions: []string{"class.view"},
	}
	r.PreSave()
	clone := r.Clone()
	clone.Permissions[0] = "class.members.manage"
	if r.Permissions[0] != "class.view" {
		t.Fatal("Clone exposed role permission storage")
	}

	audit := r.Auditable()
	audit["permissions"].([]string)[0] = "class.members.manage"
	if r.Auditable()["permissions"].([]string)[0] != "class.view" {
		t.Fatal("Auditable exposed role permission storage")
	}
}

func TestSessionAndCredentialExpiryAndRotation(t *testing.T) {
	t.Parallel()

	beforeCreate := GetMillis()
	s := &Session{
		UserId:                 NewId(),
		ClientType:             SessionClientCLI,
		AuthenticationMethod:   "password",
		AuthenticationStrength: AuthenticationMultiFactor,
		AuthenticatedAt:        beforeCreate,
		MFACompletedAt:         beforeCreate,
		IdleExpiresAt:          beforeCreate + 60_000,
		ExpiresAt:              beforeCreate + 120_000,
	}
	s.PreSave()
	if appErr := s.IsValid(); appErr != nil {
		t.Fatal(appErr)
	}
	if s.LastActivityAt != s.CreateAt {
		t.Fatalf(
			"PreSave() last_activity_at = %d, create_at = %d",
			s.LastActivityAt,
			s.CreateAt,
		)
	}
	if s.IsExpiredAt(s.IdleExpiresAt - 1) {
		t.Fatal("session expired before its idle deadline")
	}
	if !s.IsExpiredAt(s.IdleExpiresAt) {
		t.Fatal("session did not expire at its idle deadline")
	}

	refresh := &SessionCredential{
		SessionId: s.Id,
		Kind:      SessionCredentialRefresh,
		TokenHash: HashToken(NewCredentialToken()),
		ExpiresAt: s.ExpiresAt,
	}
	refresh.PreSave()
	if !IsValidId(refresh.FamilyId) {
		t.Fatalf("refresh family id = %q", refresh.FamilyId)
	}
	if appErr := refresh.IsValid(); appErr != nil {
		t.Fatal(appErr)
	}

	access := &SessionCredential{
		SessionId: s.Id,
		Kind:      SessionCredentialAccess,
		TokenHash: HashToken(NewCredentialToken()),
		FamilyId:  NewId(),
		ExpiresAt: s.ExpiresAt,
	}
	access.PreSave()
	if appErr := access.IsValid(); appErr == nil ||
		appErr.(*ValidationError).Code != "model.session_credential.is_valid.kind.app_error" {
		t.Fatalf("access credential error = %v", appErr)
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
				r.PreSave()
				return r.IsValid()
			}(),
			code: "model.role.is_valid.permissions.app_error",
		},
		{
			name: "personal token without expiry",
			err: func() error {
				token := &PersonalAccessToken{
					UserId:      NewId(),
					Description: "CLI",
					TokenHash:   HashToken(NewCredentialToken()),
					Scopes:      []string{"class.view"},
				}
				token.PreSave()
				return token.IsValid()
			}(),
			code: "model.personal_access_token.is_valid.expires_at.app_error",
		},
		{
			name: "session idle deadline after absolute deadline",
			err: func() error {
				s := &Session{
					UserId:                 NewId(),
					ClientType:             SessionClientDesktop,
					AuthenticationMethod:   "oidc",
					AuthenticationStrength: AuthenticationSingleFactor,
					IdleExpiresAt:          now + 120_000,
					ExpiresAt:              now + 60_000,
				}
				s.PreSave()
				return s.IsValid()
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
