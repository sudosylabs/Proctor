// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package model

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestUserNormalizationAndContextualRelationships(t *testing.T) {
	t.Parallel()

	u := &User{
		Username:    "Alex.Morgan",
		Email:       " ALEX.MORGAN@EXAMPLE.EDU ",
		DisplayName: "Alex Morgan",
	}
	u.PrepareCreate(NewUserID(), NowUTC())
	require.NoError(t, u.Validate())
	require.Equal(t, "alex.morgan", u.Username)
	require.Equal(t, "alex.morgan@example.edu", u.Email)
	require.Equal(t, DefaultLocale, u.Locale, "defaults: locale")
	require.Equal(t, DefaultTimezone, u.Timezone, "defaults: locale")

	at := NowUTC()
	student := &Affiliation{UserID: u.ID, Kind: AffiliationStudent}
	teacher := &Affiliation{UserID: u.ID, Kind: AffiliationTeacher}
	student.PrepareCreate(NewAffiliationID(), at)
	teacher.PrepareCreate(NewAffiliationID(), at)
	require.NoError(t, student.Validate())
	require.NoError(t, teacher.Validate())
}

func TestExternalIdentityPreservesOpaqueSubject(t *testing.T) {
	t.Parallel()

	const subject = "CaseSensitive/Provider/Subject"
	identity := &ExternalIdentity{
		UserID:   UserID(NewID()),
		Provider: "OIDC",
		Subject:  subject,
	}
	identity.PrepareCreate(NewExternalIdentityID(), NowUTC())
	require.Equal(t, "oidc", identity.Provider)
	require.Equal(t, subject, identity.Subject)
	require.NoError(t, identity.Validate())
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
	require.NoError(t, role.Validate())
	require.False(t, role.IsArchived(), "new role should not be archived")
	clone := role.Clone()
	clone.Permissions[0] = "class.members.manage"
	require.Equal(t, "class.view", role.Permissions[0], "Clone exposed role permission storage")
	audit := role.Auditable()
	audit["permissions"].([]string)[0] = "class.members.manage"
	require.Equal(t, "class.view", role.Auditable()["permissions"].([]string)[0], "Auditable exposed role permission storage")

	binding := &RoleBinding{
		UserID:    NewUserID(),
		RoleID:    role.ID,
		ScopeType: RoleScopeAcademicUnit,
		ScopeID:   NewID(),
	}
	binding.PrepareCreate(NewRoleBindingID(), at)
	require.NoError(t, binding.Validate())
	require.True(t, binding.IsActiveAt(at), "binding should be active at create time")
	require.NoError(t, binding.End(at.Add(time.Hour)))
	require.False(t, binding.IsActiveAt(at.Add(time.Hour)), "binding should not be active at exclusive end time")

	invitationBinding := &RoleBinding{UserID: NewUserID(), RoleID: role.ID, ScopeType: RoleScopeAcademicUnit,
		ScopeID: NewAcademicUnitID().String(), OriginInvitationID: NewInvitationID(), OriginAcademicUnitMemberID: NewAcademicUnitMemberID()}
	invitationBinding.PrepareCreate(NewRoleBindingID(), at)
	require.NoError(t, invitationBinding.Validate())
	require.Equal(t, invitationBinding.OriginInvitationID.String(), invitationBinding.Auditable()["origin_invitation_id"])
}

func TestInstallationStateTypedLifecycle(t *testing.T) {
	t.Parallel()

	state := &InstallationState{
		InitializedAt:       NowUTC(),
		InstitutionID:       NewInstitutionID(),
		AdministratorUserID: NewUserID(),
	}
	require.NoError(t, state.Validate())
	fields := state.Auditable()
	require.Equal(t, state.InstitutionID.String(), fields["institution_id"])
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
	require.NoError(t, s.Validate())
	revoked := *s
	revoked.RevokedAt = OptionalTimeFrom(beforeCreate)
	revoked.RevocationReason = SessionRevocationUserLogout
	require.NoError(t, revoked.Validate())
	revoked.RevocationReason = "user clicked the logout button"
	require.Error(t, revoked.Validate(), "presentation prose was accepted as a revocation reason")
	require.True(t, s.LastActivityAt.Equal(s.CreatedAt), "PrepareCreate() last_activity_at = %v, created_at", s.LastActivityAt)
	require.False(t, s.IsExpiredAt(s.IdleExpiresAt.Add(-time.Millisecond)), "session expired before its idle deadline")
	require.True(t, s.IsExpiredAt(s.IdleExpiresAt), "session did not expire at its idle deadline")

	refresh := &SessionCredential{
		SessionID: s.ID,
		Kind:      SessionCredentialRefresh,
		TokenHash: HashToken(NewCredentialToken()),
		ExpiresAt: s.ExpiresAt,
	}
	refresh.PrepareCreate(NewSessionCredentialID(), beforeCreate)
	require.True(t, IsValidOpaqueID(refresh.FamilyID), "refresh family id")
	require.NoError(t, refresh.Validate())

	access := &SessionCredential{
		SessionID: s.ID,
		Kind:      SessionCredentialAccess,
		TokenHash: HashToken(NewCredentialToken()),
		FamilyID:  NewID(),
		ExpiresAt: s.ExpiresAt,
	}
	access.PrepareCreate(NewSessionCredentialID(), beforeCreate)
	var validation *ValidationError
	require.ErrorAs(t, access.Validate(), &validation)
	require.Equal(t, "model.session_credential.is_valid.kind.app_error", validation.Code)

	token := &PersonalAccessToken{
		UserID:      NewUserID(),
		Description: "Automation on my workstation",
		TokenHash:   HashToken(NewCredentialToken()),
		Scopes:      []string{"class.view"},
		ExpiresAt:   beforeCreate.Add(24 * time.Hour),
	}
	token.PrepareCreate(NewPersonalAccessTokenID(), beforeCreate)
	require.NoError(t, token.Validate())
	require.True(t, token.IsActiveAt(beforeCreate.Add(time.Hour)), "token should be active before expiry")
	require.False(t, token.IsActiveAt(token.ExpiresAt), "token should be inactive at expiry")
}

func TestSessionRetainsAValidatedExternalProviderIdentity(t *testing.T) {
	t.Parallel()

	at := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	valid := &Session{
		UserID: NewUserID(), ClientType: SessionClientWeb,
		AuthenticationMethod: "oidc", AuthenticationProviderID: "0-campus.oidc", ExternalIdentityID: NewExternalIdentityID(),
		AuthenticationStrength: AuthenticationSingleFactor, AuthenticatedAt: at,
		IdleExpiresAt: at.Add(time.Hour), ExpiresAt: at.Add(2 * time.Hour),
	}
	valid.PrepareCreate(NewSessionID(), at)
	require.NoError(t, valid.Validate())
	local := *valid
	local.AuthenticationMethod = "password"
	local.AuthenticationProviderID = ""
	local.ExternalIdentityID = ""
	require.NoError(t, local.Validate())
	missingProvider := *valid
	missingProvider.AuthenticationProviderID = ""
	require.Error(t, missingProvider.Validate(), "external Session without an exact provider ID was accepted")
	missingIdentity := *valid
	missingIdentity.ExternalIdentityID = ""
	require.Error(t, missingIdentity.Validate(), "external Session without an exact external identity was accepted")
	localWithIdentity := local
	localWithIdentity.ExternalIdentityID = NewExternalIdentityID()
	require.Error(t, localWithIdentity.Validate(), "local Session with external identity provenance was accepted")
	invalid := *valid
	invalid.AuthenticationProviderID = ".campus"
	require.Error(t, invalid.Validate(), "Session accepted an invalid external provider ID")
}

func TestSessionValidationAllowsLaterMFAUpgrade(t *testing.T) {
	t.Parallel()

	createdAt := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	completedAt := createdAt.Add(time.Minute)
	session := &Session{
		ID:                     NewSessionID(),
		CreatedAt:              createdAt,
		UpdatedAt:              completedAt,
		UserID:                 NewUserID(),
		ClientType:             SessionClientWeb,
		AuthenticationMethod:   "password",
		AuthenticationStrength: AuthenticationMultiFactor,
		AuthenticatedAt:        createdAt,
		MFACompletedAt:         OptionalTimeFrom(completedAt),
		LastActivityAt:         createdAt,
		IdleExpiresAt:          createdAt.Add(time.Hour),
		ExpiresAt:              createdAt.Add(2 * time.Hour),
	}
	require.NoError(t, session.Validate())

	session.MFACompletedAt = OptionalTimeFrom(completedAt.Add(time.Millisecond))
	require.Error(t, session.Validate(), "Validate() accepted MFA completion after the persisted update time")
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
		require.NoError(t, err)
		output := string(encoded)
		for _, secret := range []string{"private-subject", "private-password-hash", tokenHash} {
			require.NotContains(t, output, secret, "%T JSON exposes secret", value)
		}
	}
}

func TestCredentialTokenGenerationAndHashing(t *testing.T) {
	t.Parallel()

	first := NewCredentialToken()
	second := NewCredentialToken()
	require.NotEqual(t, second, first, "generated credentials have unexpected shape")
	require.GreaterOrEqual(t, len(first), 40, "generated credentials have unexpected shape")
	require.NotEqual(t, HashToken(second), HashToken(first), "different credentials produced the same hash")
	require.True(t, IsValidTokenHash(HashToken(first)), "HashToken returned an invalid persistent hash")
}

func TestClassMemberRetainsClosedEnrollmentHistory(t *testing.T) {
	t.Parallel()

	at := time.UnixMilli(1_700_000_000_000).UTC()
	m := &ClassMember{
		ClassID:          ClassID(NewID()),
		AcademicPeriodID: AcademicPeriodID(NewID()),
		UserID:           UserID(NewID()),
	}
	m.PrepareCreate(NewClassMemberID(), at)
	require.True(t, m.IsActiveAt(m.StartsAt), "new class membership is not active")

	endAt := m.StartsAt.Add(time.Millisecond)
	require.NoError(t, m.End(endAt))
	require.False(t, m.IsActiveAt(endAt), "closed class membership remained active")
	require.False(t, m.IsArchived(), "closing enrollment deleted its historical record")
}

func TestMembershipModelsTypedLifecycle(t *testing.T) {
	t.Parallel()

	at := time.UnixMilli(1_700_000_000_000).UTC()
	userID := UserID(NewID())

	affiliation := &Affiliation{UserID: userID, Kind: AffiliationTeacher}
	affiliation.PrepareCreate(NewAffiliationID(), at)
	require.NoError(t, affiliation.Validate())
	require.True(t, affiliation.IsActiveAt(at), "affiliation should be active at create time")
	audit := affiliation.Auditable()
	require.Equal(t, affiliation.ID.String(), audit["id"])
	require.Equal(t, MillisFromTime(at), audit["start_at"])
	require.Equal(t, int64(0), audit["end_at"])

	member := &AcademicUnitMember{
		AcademicUnitID: AcademicUnitID(NewID()),
		UserID:         userID,
	}
	member.PrepareCreate(NewAcademicUnitMemberID(), at)
	require.NoError(t, member.Validate())
	require.True(t, member.IsActiveAt(at), "academic unit member should be active at create time")

	classMember := &ClassMember{
		ClassID:          ClassID(NewID()),
		AcademicPeriodID: AcademicPeriodID(NewID()),
		UserID:           userID,
	}
	classMember.PrepareCreate(NewClassMemberID(), at)
	require.NoError(t, classMember.Validate())
}

func TestIdentityModelsTypedLifecycle(t *testing.T) {
	t.Parallel()

	at := time.UnixMilli(1_700_000_000_000).UTC()
	userID := NewUserID()

	user := &User{
		Username: "alex.morgan", Email: "alex.morgan@example.edu", DisplayName: "Alex",
	}
	user.PrepareCreate(userID, at)
	require.NoError(t, user.Validate())
	require.True(t, user.IsActive(), "new user should be active")
	require.False(t, user.IsArchived(), "new user should be active")

	credential := &PasswordCredential{
		UserID: userID, PasswordHash: "$argon2id$v=19$m=65536,t=3,p=4$c2FsdA$aGFzaA",
	}
	credential.PrepareCreate(NewPasswordCredentialID(), at)
	require.NoError(t, credential.Validate())
	require.Equal(t, int64(1), credential.Revision, "initial password revision")
	credential.Revision = 0
	require.Error(t, credential.Validate(), "password credential accepted a missing revision")
	credential.Revision = 1

	identity := &ExternalIdentity{
		UserID: userID, Provider: "OIDC", Subject: "opaque-subject",
		LastSeenAt: OptionalTimeFrom(at),
	}
	identity.PrepareCreate(NewExternalIdentityID(), at)
	require.Equal(t, "oidc", identity.Provider)
	require.NoError(t, identity.Validate())

	tokenHash := HashToken(NewCredentialToken())
	token := &UserToken{
		UserID: userID, Purpose: UserTokenEmailVerification,
		TokenHash: tokenHash, Target: "alex.morgan@example.edu",
		ExpiresAt: at.Add(time.Hour),
	}
	token.PrepareCreate(NewUserTokenID(), at)
	require.NoError(t, token.Validate())
	require.True(t, token.IsActiveAt(at), "token active window is wrong")
	require.False(t, token.IsActiveAt(token.ExpiresAt), "token active window is wrong")

	state := &ExternalLoginState{
		Provider: "campus-cas", StateHash: tokenHash, BindingHash: tokenHash,
		ReturnTo: "/", ClientType: SessionClientWeb, ExpiresAt: at.Add(time.Minute),
	}
	state.PrepareCreate(NewExternalLoginStateID(), at)
	require.NoError(t, state.Validate())
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
					UserID:                   NewUserID(),
					ClientType:               SessionClientWeb,
					AuthenticationMethod:     "oidc",
					AuthenticationProviderID: "campus-oidc",
					ExternalIdentityID:       NewExternalIdentityID(),
					AuthenticationStrength:   AuthenticationSingleFactor,
					IdleExpiresAt:            at.Add(120 * time.Second),
					ExpiresAt:                at.Add(60 * time.Second),
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
					UserID:   UserID(NewID()),
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
					ClassID: ClassID(NewID()),
					UserID:  UserID(NewID()),
				}
				member.PrepareCreate(NewClassMemberID(), NowUTC())
				return member.Validate()
			}(),
			code: "model.class_member.is_valid.academic_period_id.app_error",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var validation *ValidationError
			require.ErrorAs(t, test.err, &validation)
			require.Equal(t, test.code, validation.Code)
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
		_, exposed := audit[field]
		require.False(t, exposed, "user audit exposes %q: %#v", field, audit)
	}
}
