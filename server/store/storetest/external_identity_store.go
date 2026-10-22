// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package storetest

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

func TestExternalIdentityStore(t *testing.T, ss store.Store) {
	t.Run("ConcurrentSessionIssuanceCannotSurviveExactIdentityUnlink", func(t *testing.T) {
		ctx := context.Background()
		user := saveUser(t, ctx, ss)
		_, err := ss.PasswordCredential().Save(ctx, &model.PasswordCredential{UserID: user.ID, PasswordHash: "$argon2id$concurrent-fallback"})
		require.NoError(t, err)
		identity, err := ss.ExternalIdentity().Save(ctx, &model.ExternalIdentity{UserID: user.ID, Provider: "campus-cas",
			Subject: "concurrent-session-" + model.NewID(), LastSeenAt: model.OptionalTimeFromMillis(model.GetMillis())})
		require.NoError(t, err)
		candidate, credentials, _ := newSession(user.ID.String())
		candidate.AuthenticationMethod = "oidc"
		candidate.AuthenticationProviderID = identity.Provider
		candidate.ExternalIdentityID = identity.ID
		audit := saveAuthenticationMethodAuditAttempt(t, ctx, ss, user.ID.String(), "unlink_provider")
		start := make(chan struct{})
		creation := testSessionCreation(t, ctx, ss, candidate, credentials, 10)
		var saved *model.Session
		var saveErr, unlinkErr error
		var wait sync.WaitGroup
		wait.Add(2)
		go func() {
			defer wait.Done()
			<-start
			saved, _, saveErr = SaveSession(ctx, ss, creation)
		}()
		go func() {
			defer wait.Done()
			<-start
			_, unlinkErr = ss.ExternalIdentity().Unlink(ctx, &store.ExternalIdentityUnlink{ID: identity.ID,
				UserID: user.ID, Capabilities: store.AccessDeploymentCapabilities{Providers: map[string]store.AccessProviderCapability{"campus-cas": {}}},
				ChangedAt: model.NowUTC(), RevocationReason: model.SessionRevocationExternalIdentityUnlinked, AuditEventID: audit.ID, AuditAt: model.NowUTC()})
		}()
		close(start)
		wait.Wait()
		require.NoError(t, unlinkErr)
		if saveErr != nil {
			require.ErrorIs(t, saveErr, store.ErrAuthenticationMethodDisabled)
		}
		if saved != nil {
			persisted, getErr := ss.Session().Get(ctx, saved.ID)
			require.NoError(t, getErr)
			require.True(t, persisted.RevokedAt.Valid)
		}
	})

	t.Run("AuditedLinkIsUniqueAcrossConcurrentUsers", func(t *testing.T) {
		ctx := context.Background()
		first := saveUser(t, ctx, ss)
		second := saveUser(t, ctx, ss)
		subject := "concurrent-subject-" + model.NewID()
		users := []model.UserID{first.ID, second.ID}
		attempts := []*model.AuditEvent{
			saveAuthenticationMethodAuditAttempt(t, ctx, ss, first.ID.String(), "connect_provider"),
			saveAuthenticationMethodAuditAttempt(t, ctx, ss, second.ID.String(), "connect_provider"),
		}
		capabilities := store.AccessDeploymentCapabilities{Providers: map[string]store.AccessProviderCapability{
			"campus-cas": {},
		}}
		start := make(chan struct{})
		errs := make([]error, len(users))
		var wg sync.WaitGroup
		for i := range users {
			wg.Add(1)
			go func(index int) {
				defer wg.Done()
				<-start
				_, errs[index] = ss.ExternalIdentity().Link(ctx, &store.ExternalIdentityLink{
					Identity: &model.ExternalIdentity{UserID: users[index], Provider: "campus-cas", Subject: subject,
						LastSeenAt: model.OptionalTimeFromMillis(model.GetMillis())},
					Capabilities: capabilities, AuditEventID: attempts[index].ID, AuditAt: model.NowUTC(),
				})
			}(i)
		}
		close(start)
		wg.Wait()
		succeeded, conflicted := 0, 0
		for _, err := range errs {
			if err == nil {
				succeeded++
				continue
			}
			var conflict *store.ErrConflict
			if errors.As(err, &conflict) && conflict.Constraint == "external_identities_provider_subject_key" {
				conflicted++
				continue
			}
			require.FailNow(t, fmt.Sprintf("Link() concurrent error = %v", err))
		}
		require.Equal(t, 1, succeeded, "Link() outcomes success=%d conflict=%d errors=%v", succeeded, conflicted, errs)
		require.Equal(t, 1, conflicted, "Link() outcomes success=%d conflict=%d errors=%v", succeeded, conflicted, errs)
	})

	t.Run("UnlinkRevokesOnlyExactIdentitySessions", func(t *testing.T) {
		ctx := context.Background()
		user := saveUser(t, ctx, ss)
		_, err := ss.PasswordCredential().Save(ctx, &model.PasswordCredential{UserID: user.ID, PasswordHash: "$argon2id$another-method"})
		require.NoError(t, err)
		otherIdentity, err := ss.ExternalIdentity().Save(ctx, &model.ExternalIdentity{UserID: user.ID,
			Provider: "campus-cas", Subject: "other-revocation-subject-" + model.NewID(),
			LastSeenAt: model.OptionalTimeFromMillis(model.GetMillis())})
		require.NoError(t, err)
		capabilities := store.AccessDeploymentCapabilities{Providers: map[string]store.AccessProviderCapability{
			"campus-cas": {},
		}}
		linkAttempt := saveAuthenticationMethodAuditAttempt(t, ctx, ss, user.ID.String(), "connect_provider")
		linked, err := ss.ExternalIdentity().Link(ctx, &store.ExternalIdentityLink{
			Identity: &model.ExternalIdentity{UserID: user.ID, Provider: "campus-cas",
				Subject: "revocation-subject-" + model.NewID(), LastSeenAt: model.OptionalTimeFromMillis(model.GetMillis())},
			Capabilities: capabilities, AuditEventID: linkAttempt.ID, AuditAt: model.NowUTC(),
		})
		require.NoError(t, err)

		passwordSession, passwordCredentials, _ := newSession(user.ID.String())
		passwordSession, _, err = SaveSession(ctx, ss, testSessionCreation(t, ctx, ss, passwordSession, passwordCredentials, 10))
		require.NoError(t, err)
		providerSession, providerCredentials, _ := newSession(user.ID.String())
		providerSession.AuthenticationMethod = "oidc"
		providerSession.AuthenticationProviderID = "campus-cas"
		providerSession.ExternalIdentityID = linked.Identity.ID
		providerSession, _, err = SaveSession(ctx, ss, testSessionCreation(t, ctx, ss, providerSession, providerCredentials, 10))
		require.NoError(t, err)
		otherProviderSession, otherProviderCredentials, _ := newSession(user.ID.String())
		otherProviderSession.AuthenticationMethod = "oidc"
		otherProviderSession.AuthenticationProviderID = "campus-cas"
		otherProviderSession.ExternalIdentityID = otherIdentity.ID
		otherProviderSession, _, err = SaveSession(ctx, ss, testSessionCreation(t, ctx, ss, otherProviderSession, otherProviderCredentials, 10))
		require.NoError(t, err)

		unlinkAttempt := saveAuthenticationMethodAuditAttempt(t, ctx, ss, user.ID.String(), "unlink_provider")
		result, err := ss.ExternalIdentity().Unlink(ctx, &store.ExternalIdentityUnlink{
			ID: linked.Identity.ID, UserID: user.ID, Capabilities: capabilities, ChangedAt: model.NowUTC(),
			RevocationReason: model.SessionRevocationExternalIdentityUnlinked, AuditEventID: unlinkAttempt.ID, AuditAt: model.NowUTC(),
		})
		require.NoError(t, err)
		require.Len(t, result.RevokedSessions, 1)
		require.Equal(t, providerSession.ID, result.RevokedSessions[0].ID)
		require.Len(t, result.RevokedTokenHashes, 2)
		retained, err := ss.Session().Get(ctx, passwordSession.ID)
		require.NoError(t, err)
		require.False(t, retained.RevokedAt.Valid)
		retained, err = ss.Session().Get(ctx, otherProviderSession.ID)
		require.NoError(t, err)
		require.False(t, retained.RevokedAt.Valid)
		revoked, err := ss.Session().Get(ctx, providerSession.ID)
		require.NoError(t, err)
		require.True(t, revoked.RevokedAt.Valid)
		require.Equal(t, model.SessionRevocationExternalIdentityUnlinked, revoked.RevocationReason)
	})

	t.Run("SaveGetAndList", func(t *testing.T) {
		ctx := context.Background()
		user := saveUser(t, ctx, ss)
		identity, err := ss.ExternalIdentity().Save(ctx, &model.ExternalIdentity{
			UserID: user.ID, Provider: "campus-cas",
			Subject: "Opaque-Subject/123", LastSeenAt: model.OptionalTimeFromMillis(model.GetMillis()),
		})
		require.NoError(t, err)
		got, err := ss.ExternalIdentity().Get(ctx, identity.ID)
		require.NoError(t, err)
		require.Equal(t, identity.Subject, got.Subject)
		require.Equal(t, "campus-cas", got.Provider)
		bySubject, err := ss.ExternalIdentity().GetByProviderSubject(
			ctx,
			" CAMPUS-CAS ",
			identity.Subject,
		)
		require.NoError(t, err)
		require.Equal(t, identity.ID, bySubject.ID)
		list, err := ss.ExternalIdentity().ListByUser(ctx, user.ID)
		require.NoError(t, err)
		require.Len(t, list, 1)
		require.Equal(t, identity.ID, list[0].ID)
		_, err = ss.ExternalIdentity().Save(ctx, &model.ExternalIdentity{
			UserID: saveUser(t, ctx, ss).ID, Provider: "campus-cas",
			Subject: identity.Subject, LastSeenAt: model.OptionalTimeFromMillis(model.GetMillis()),
		})
		var conflict *store.ErrConflict
		require.ErrorAs(t, err, &conflict)
		require.Equal(t, "external_identities_provider_subject_key", conflict.Constraint)
	})

	t.Run("ResolveAndProvision", func(t *testing.T) {
		ctx := context.Background()
		institution := saveInstitution(t, ctx, ss)
		now := model.GetMillis()
		candidate := newUser()
		creation := testUserCreation(candidate, nil)
		resolved, err := ss.ExternalIdentity().ResolveOrProvision(
			ctx, &store.ExternalIdentityResolutionRequest{Identity: &model.ExternalIdentity{
				Provider: "campus-cas", Subject: "new-subject",
				LastSeenAt: model.OptionalTimeFromMillis(now),
			},
				User: creation.User, Settings: creation.Settings, Capabilities: externalIdentityCapabilities(true), AuditEvent: &model.AuditEvent{
					Action:    "authentication.external_provision",
					ScopeType: model.RoleScopeInstitution,
					ScopeID:   institution.ID.String(), Status: model.AuditStatusSuccess,
					NodeID: "test-node", AuthMethod: "cas",
				}, DefaultProfilePictureJob: creation.DefaultProfilePictureJob},
		)
		require.NoError(t, err)
		require.True(t, resolved.Provisioned)
		require.Equal(t, resolved.User.ID, resolved.Identity.UserID)
		require.True(t, candidate.ID.IsZero())
		queued, err := ss.Job().Get(ctx, creation.DefaultProfilePictureJob.ID)
		require.NoError(t, err)
		require.Equal(t, resolved.User.ID.String(), queued.DedupeKey)
		require.Equal(t, model.JobStatusQueued, queued.Status)
		settings, err := ss.UserSettings().Get(ctx, resolved.User.ID)
		require.NoError(t, err)
		require.Equal(t, model.UserSettingsInitialSource, settings.Source)
		require.Equal(t, model.UserSettingsFormatVersion1, settings.FormatVersion)
		require.True(t, settings.CreatedAt.Equal(resolved.User.CreatedAt))
		audits, err := ss.Audit().List(ctx, store.AuditListOptions{
			Action:     "authentication.external_provision",
			Limit:      10,
			Visibility: store.AuditVisibilityScope{InstitutionWide: true},
		})
		require.NoError(t, err)
		require.Len(t, audits, 1)
		require.Equal(t, resolved.User.ID, audits[0].ActorID)
		require.Equal(t, resolved.User.ID.String(), audits[0].Resource.ID)
		changedClaims := newUser()
		originalUsername := resolved.User.Username
		again, err := ss.ExternalIdentity().ResolveOrProvision(
			ctx, &store.ExternalIdentityResolutionRequest{Identity: &model.ExternalIdentity{
				Provider: "campus-cas", Subject: "new-subject",
				LastSeenAt: model.OptionalTimeFromMillis(now + 100),
			}, User: changedClaims, Capabilities: externalIdentityCapabilities(false)},
		)
		require.NoError(t, err)
		require.False(t, again.Provisioned)
		require.Equal(t, resolved.User.ID, again.User.ID)
		require.Equal(t, now+100, again.Identity.LastSeenAt.Millis())
		require.Equal(t, originalUsername, again.User.Username)
		require.NotEqual(t, changedClaims.Username, again.User.Username)
	})

	t.Run("NoImplicitAccountLinking", func(t *testing.T) {
		ctx := context.Background()
		institution, err := ss.Institution().GetSingleton(ctx)
		if store.IsNotFound(err) {
			institution = saveInstitution(t, ctx, ss)
			err = nil
		}
		require.NoError(t, err)
		existing := saveUser(t, ctx, ss)
		candidate := newUser()
		candidate.Email = existing.Email
		creation := testUserCreation(candidate, nil)
		_, err = ss.ExternalIdentity().ResolveOrProvision(
			ctx, &store.ExternalIdentityResolutionRequest{Identity: &model.ExternalIdentity{
				Provider: "campus-cas", Subject: "different-subject",
				LastSeenAt: model.OptionalTimeFromMillis(model.GetMillis()),
			}, User: creation.User, Settings: creation.Settings, Capabilities: externalIdentityCapabilities(true), AuditEvent: &model.AuditEvent{
				Action:    "authentication.external_provision",
				ScopeType: model.RoleScopeInstitution,
				ScopeID:   institution.ID.String(), Status: model.AuditStatusSuccess,
				NodeID: "test-node", AuthMethod: "cas",
			}, DefaultProfilePictureJob: creation.DefaultProfilePictureJob},
		)
		var conflict *store.ErrConflict
		require.ErrorAs(t, err, &conflict)
		require.Equal(t, "users_email_key", conflict.Constraint)
		_, err = ss.ExternalIdentity().GetByProviderSubject(
			ctx,
			"campus-cas",
			"different-subject",
		)
		require.True(t, store.IsNotFound(err), "colliding identity was persisted")
		_, err = ss.Job().Get(ctx, creation.DefaultProfilePictureJob.ID)
		require.True(t, store.IsNotFound(err), "colliding provision default-picture job was persisted")
		_, err = ss.UserSettings().Get(ctx, creation.User.ID)
		require.True(t, store.IsNotFound(err), "colliding provision settings were persisted")
	})

	t.Run("ProvisioningRejectsMismatchedDefaultJobTarget", func(t *testing.T) {
		ctx := context.Background()
		institution, err := ss.Institution().GetSingleton(ctx)
		if store.IsNotFound(err) {
			institution = saveInstitution(t, ctx, ss)
			err = nil
		}
		require.NoError(t, err)
		creation := testUserCreation(newUser(), nil)
		creation.DefaultProfilePictureJob.Command = defaultProfilePictureCommand(model.NewUserID())
		subject := "mismatched-job-" + model.NewID()
		before, err := ss.Audit().List(ctx, store.AuditListOptions{
			Action: "authentication.external_provision", Limit: 100,
			Visibility: store.AuditVisibilityScope{InstitutionWide: true},
		})
		require.NoError(t, err)
		_, err = ss.ExternalIdentity().ResolveOrProvision(
			ctx, &store.ExternalIdentityResolutionRequest{Identity: &model.ExternalIdentity{
				Provider: "campus-cas", Subject: subject,
				LastSeenAt: model.OptionalTimeFromMillis(model.GetMillis()),
			}, User: creation.User, Settings: creation.Settings, Capabilities: externalIdentityCapabilities(true), AuditEvent: &model.AuditEvent{
				Action: "authentication.external_provision", ScopeType: model.RoleScopeInstitution,
				ScopeID: institution.ID.String(), Status: model.AuditStatusSuccess,
				NodeID: "test-node", AuthMethod: "cas",
			}, DefaultProfilePictureJob: creation.DefaultProfilePictureJob},
		)
		require.Error(t, err, "ResolveOrProvision() accepted a default-picture Job targeting another User")
		_, err = ss.User().Get(ctx, creation.User.ID)
		require.True(t, store.IsNotFound(err), "provisioned User survived mismatched Job rollback")
		_, err = ss.ExternalIdentity().GetByProviderSubject(ctx, "campus-cas", subject)
		require.True(t, store.IsNotFound(err), "external identity survived mismatched Job rollback")
		_, err = ss.Job().Get(ctx, creation.DefaultProfilePictureJob.ID)
		require.True(t, store.IsNotFound(err), "mismatched provision Job was persisted")
		_, err = ss.UserSettings().Get(ctx, creation.User.ID)
		require.True(t, store.IsNotFound(err), "mismatched provision settings survived rollback")
		after, err := ss.Audit().List(ctx, store.AuditListOptions{
			Action: "authentication.external_provision", Limit: 100,
			Visibility: store.AuditVisibilityScope{InstitutionWide: true},
		})
		require.NoError(t, err)
		require.Len(t, after, len(before), "provision audit survived mismatched Job rollback: before")
		permanent := testUserCreation(newUser(), nil)
		permanent.DefaultProfilePictureJob.DedupePolicy = model.JobDedupePermanent
		_, err = ss.ExternalIdentity().ResolveOrProvision(ctx, &store.ExternalIdentityResolutionRequest{
			Identity: &model.ExternalIdentity{Provider: "campus-cas", Subject: "permanent-job-" + model.NewID(), LastSeenAt: model.OptionalTimeFromMillis(model.GetMillis())},
			User:     permanent.User, Settings: permanent.Settings, Capabilities: externalIdentityCapabilities(true), AuditEvent: &model.AuditEvent{
				Action: "authentication.external_provision", ScopeType: model.RoleScopeInstitution,
				ScopeID: institution.ID.String(), Status: model.AuditStatusSuccess, NodeID: "test-node", AuthMethod: "cas",
			}, DefaultProfilePictureJob: permanent.DefaultProfilePictureJob,
		})
		require.Error(t, err, "ResolveOrProvision() accepted a permanent-dedupe default-picture intent")
	})

	t.Run("ConfiguredProviderWithoutAutoProvisionCapabilityFailsClosed", func(t *testing.T) {
		_, err := ss.ExternalIdentity().ResolveOrProvision(
			context.Background(), &store.ExternalIdentityResolutionRequest{Identity: &model.ExternalIdentity{
				Provider: "campus-cas", Subject: "unlinked",
				LastSeenAt: model.OptionalTimeFromMillis(model.GetMillis()),
			}, Capabilities: externalIdentityCapabilities(false)},
		)
		require.ErrorIs(t, err, store.ErrAuthenticationMethodDisabled)
	})

	t.Run("ConfiguredProviderRemovalPreservesDurableLink", func(t *testing.T) {
		ctx := context.Background()
		user := saveUser(t, ctx, ss)
		identity, err := ss.ExternalIdentity().Save(ctx, &model.ExternalIdentity{
			UserID: user.ID, Provider: "campus-cas", Subject: "removed-provider-" + model.NewID(),
			LastSeenAt: model.OptionalTimeFromMillis(model.GetMillis()),
		})
		require.NoError(t, err)
		_, err = ss.ExternalIdentity().ResolveOrProvision(ctx, &store.ExternalIdentityResolutionRequest{
			Identity: &model.ExternalIdentity{Provider: identity.Provider, Subject: identity.Subject,
				LastSeenAt: model.OptionalTimeFromMillis(model.GetMillis())},
			Capabilities: store.AccessDeploymentCapabilities{Providers: map[string]store.AccessProviderCapability{}},
		})
		require.ErrorIs(t, err, store.ErrAuthenticationMethodDisabled)
		preserved, getErr := ss.ExternalIdentity().GetByProviderSubject(ctx, identity.Provider, identity.Subject)
		require.NoError(t, getErr)
		require.Equal(t, identity.ID, preserved.ID)
		require.Equal(t, user.ID, preserved.UserID)
	})
}

// TestExternalIdentityAdmissionMode verifies the terminal admission decision
// against a Store whose authoritative Access Policy is seeded with mode.
type ExternalIdentityAdmissionSQLProbe struct {
	BackdateState func(*testing.T, model.ExternalLoginStateID, time.Time)
}

func TestExternalIdentityAdmissionMode(t *testing.T, ss store.Store, mode model.ProviderAdmissionMode, probes ...ExternalIdentityAdmissionSQLProbe) {
	t.Helper()
	var probe ExternalIdentityAdmissionSQLProbe
	if len(probes) > 0 {
		probe = probes[0]
	}
	ctx := context.Background()
	var invitation *model.Invitation
	var institution *model.Institution
	if mode == model.ProviderAdmissionInvitationRequired {
		fixture, class, inviter, _, _, issuedAt := invitationAcceptanceStoreFixture(t, ctx, ss, "provider-admission")
		issue := studentClassInvitationIssueFixture(t, ss, inviter, class, fixture.period, issuedAt)
		var err error
		invitation, err = ss.Invitation().IssueStudentClass(ctx, issue)
		require.NoError(t, err)
		institution, err = ss.Institution().GetSingleton(ctx)
		require.NoError(t, err)
	} else {
		institution = saveInstitution(t, ctx, ss)
	}
	existingUser := saveUser(t, ctx, ss)
	existingIdentity, err := ss.ExternalIdentity().Save(ctx, &model.ExternalIdentity{
		UserID: existingUser.ID, Provider: "campus-cas", Subject: "existing-" + model.NewID(),
		LastSeenAt: model.OptionalTimeFromMillis(model.GetMillis()),
	})
	require.NoError(t, err)

	resolved, err := ss.ExternalIdentity().ResolveOrProvision(ctx, &store.ExternalIdentityResolutionRequest{
		Identity: &model.ExternalIdentity{Provider: existingIdentity.Provider, Subject: existingIdentity.Subject,
			LastSeenAt: model.OptionalTimeFromMillis(model.GetMillis())},
		Capabilities: externalIdentityCapabilities(false),
	})
	require.NoError(t, err)
	require.False(t, resolved.Provisioned, "%s existing immutable link resolution = %#v", mode, resolved)
	require.Equal(t, existingUser.ID, resolved.User.ID, "%s existing immutable link resolution = %#v", mode, resolved)
	require.Equal(t, existingIdentity.ID, resolved.Identity.ID, "%s existing immutable link resolution = %#v", mode, resolved)

	candidate := newUser()
	if invitation != nil {
		candidate.Email = invitation.TargetEmail
		candidate.EmailVerified = true
	}
	creation := testUserCreation(candidate, nil)
	request := &store.ExternalIdentityResolutionRequest{Identity: &model.ExternalIdentity{
		Provider: "campus-cas", Subject: "unlinked-" + model.NewID(), LastSeenAt: model.OptionalTimeFromMillis(model.GetMillis()),
	}, User: creation.User, Settings: creation.Settings, Capabilities: externalIdentityCapabilities(true), AuditEvent: &model.AuditEvent{
		Action: "authentication.external_provision", ScopeType: model.RoleScopeInstitution,
		ScopeID: institution.ID.String(), Status: model.AuditStatusSuccess, NodeID: "admission-mode-test", AuthMethod: "cas",
	}, DefaultProfilePictureJob: creation.DefaultProfilePictureJob}
	if invitation != nil {
		establishExternalAdmissionPolicyAdministrator(t, ctx, ss, existingUser.ID, institution.ID)
		admitted, admissionErr := ss.ExternalIdentity().ResolveOrProvision(ctx, request)
		require.Nil(t, admitted, "ordinary resolution admitted an Invitation candidate")
		require.True(t, store.IsNotFound(admissionErr), "ordinary resolution admitted an Invitation candidate")
		stateToken, bindingToken := model.NewCredentialToken(), model.NewCredentialToken()
		state, stateErr := ss.ExternalLoginState().SaveInvitationAdmission(ctx, &store.ExternalLoginStateInvitationAdmission{State: &model.ExternalLoginState{
			Provider: "campus-cas", Purpose: model.ExternalAuthenticationPurposeInvitationAdmission,
			StateHash: model.HashToken(stateToken), BindingHash: model.HashToken(bindingToken), ReturnTo: "/join",
			ClientType: model.SessionClientWeb,
		}, Lifetime: time.Minute, ClaimHash: invitation.ClaimHash})
		require.NoError(t, stateErr)
		state, stateErr = ss.ExternalLoginState().Consume(ctx, &store.ExternalLoginStateConsumption{Provider: state.Provider, StateHash: state.StateHash, BindingHash: state.BindingHash})
		require.NoError(t, stateErr)
		acceptance := studentClassInvitationAcceptanceFixture(t, invitation, model.NowUTC())
		acceptance.AuditEvent.ClientType, acceptance.AuditEvent.AuthMethod = string(model.SessionClientWeb), "cas"
		acceptance.AuditEvent.Parameters, stateErr = model.EncodeAuditData(map[string]string{"provider": "campus-cas"})
		require.NoError(t, stateErr)
		external := &store.ExternalIdentityInvitationAcceptance{
			ExternalStateID: state.ID,
			Identity: &model.ExternalIdentity{UserID: acceptance.User.ID, Provider: "campus-cas", Subject: "invited-" + model.NewID(),
				LastSeenAt: model.OptionalTimeFrom(model.NowUTC())},
			ProviderEmail: "provider-" + model.NewID() + "@example.edu",
			User:          acceptance.User, Settings: acceptance.Settings, DefaultProfilePictureJob: acceptance.DefaultProfilePictureJob,
			Affiliation: acceptance.Affiliation, ClassMember: acceptance.ClassMember,
			Notice:     &store.PreparedMail{Occurrence: acceptance.Occurrence, Delivery: acceptance.Delivery, Job: acceptance.DeliveryJob},
			AuditEvent: acceptance.AuditEvent, Capabilities: externalIdentityCapabilities(false),
			RequiredActions: acceptance.RequiredActions,
		}
		if probe.BackdateState != nil {
			expiredToken, expiredBinding := model.NewCredentialToken(), model.NewCredentialToken()
			expiredState, expiredErr := ss.ExternalLoginState().SaveInvitationAdmission(ctx, &store.ExternalLoginStateInvitationAdmission{State: &model.ExternalLoginState{
				Provider: "campus-cas", Purpose: model.ExternalAuthenticationPurposeInvitationAdmission,
				StateHash: model.HashToken(expiredToken), BindingHash: model.HashToken(expiredBinding), ReturnTo: "/join",
				ClientType: model.SessionClientWeb,
			}, Lifetime: time.Minute, ClaimHash: invitation.ClaimHash})
			require.NoError(t, expiredErr)
			expiredState, expiredErr = ss.ExternalLoginState().Consume(ctx, &store.ExternalLoginStateConsumption{Provider: expiredState.Provider, StateHash: expiredState.StateHash, BindingHash: expiredState.BindingHash})
			require.NoError(t, expiredErr)
			probe.BackdateState(t, expiredState.ID, model.NowUTC().Add(-time.Minute))
			expiredInput := *external
			expiredInput.ExternalStateID = expiredState.ID
			value, expiredErr := ss.Invitation().AcceptExternalIdentity(ctx, &expiredInput)
			require.Nil(t, value, "expired external Invitation proof")
			require.True(t, store.IsConflict(expiredErr), "expired external Invitation proof")
		}
		removedProvider := *external
		removedProvider.Capabilities = store.AccessDeploymentCapabilities{Providers: map[string]store.AccessProviderCapability{}}
		value, removedErr := ss.Invitation().AcceptExternalIdentity(ctx, &removedProvider)
		require.Nil(t, value, "removed provider external Invitation acceptance")
		require.ErrorIs(t, removedErr, store.ErrAuthenticationMethodDisabled, "removed provider external Invitation acceptance")
		replaceExternalAdmissionPolicy(t, ctx, ss, existingUser.ID, institution.ID, false)
		blocked, blockedErr := ss.Invitation().AcceptExternalIdentity(ctx, external)
		require.Nil(t, blocked, "globally disabled invitation admission")
		require.ErrorIs(t, blockedErr, store.ErrAuthenticationMethodDisabled, "globally disabled invitation admission")
		_, getErr := ss.User().Get(ctx, external.User.ID)
		require.True(t, store.IsNotFound(getErr), "globally disabled invitation admission persisted User")
		replaceExternalAdmissionPolicy(t, ctx, ss, existingUser.ID, institution.ID, true)
		ownedMailbox := *external
		ownedMailbox.ProviderEmail = existingUser.Email
		value, ownedErr := ss.Invitation().AcceptExternalIdentity(ctx, &ownedMailbox)
		require.Nil(t, value, "already-owned provider mailbox")
		require.True(t, store.IsConflict(ownedErr), "already-owned provider mailbox")
		ownedSubject := *external
		ownedSubject.Identity = &model.ExternalIdentity{UserID: external.User.ID, Provider: existingIdentity.Provider, Subject: existingIdentity.Subject,
			LastSeenAt: model.OptionalTimeFrom(model.NowUTC())}
		value, ownedErr = ss.Invitation().AcceptExternalIdentity(ctx, &ownedSubject)
		require.Nil(t, value, "already-linked provider subject")
		require.True(t, store.IsConflict(ownedErr), "already-linked provider subject")
		results := make([]*store.ExternalIdentityInvitationAcceptanceResult, 2)
		errorsByIndex := make([]error, 2)
		start := make(chan struct{})
		var wait sync.WaitGroup
		for index := range results {
			wait.Add(1)
			go func(index int) {
				defer wait.Done()
				<-start
				results[index], errorsByIndex[index] = ss.Invitation().AcceptExternalIdentity(ctx, external)
			}(index)
		}
		close(start)
		wait.Wait()
		var accepted *store.ExternalIdentityInvitationAcceptanceResult
		conflicts := 0
		for index, result := range results {
			if errorsByIndex[index] == nil {
				accepted = result
			} else if store.IsConflict(errorsByIndex[index]) {
				conflicts++
			} else {
				require.FailNow(t, fmt.Sprintf("concurrent external acceptance %d = %#v, %v", index, result, errorsByIndex[index]))
			}
		}
		require.NotNil(t, accepted, "concurrent external acceptance results=%#v errors=%v", results, errorsByIndex)
		require.Equal(t, 1, conflicts, "concurrent external acceptance results=%#v errors=%v", results, errorsByIndex)
		require.Equal(t, model.InvitationAccepted, accepted.Invitation.State)
		require.Equal(t, external.User.ID, accepted.User.ID)
		require.Equal(t, accepted.User.ID, accepted.Identity.UserID)
		require.Equal(t, external.Identity.Subject, accepted.Identity.Subject)
		require.NotNil(t, accepted.Affiliation)
		require.NotNil(t, accepted.ClassMember)
		require.Equal(t, accepted.User.ID, accepted.ClassMember.UserID)
		_, credentialErr := ss.PasswordCredential().GetByUser(ctx, accepted.User.ID)
		require.True(t, store.IsNotFound(credentialErr), "external Invitation acceptance created a local credential")
		delivery, deliveryErr := ss.Mail().GetDelivery(ctx, acceptance.Delivery.ID)
		require.NoError(t, deliveryErr, "external acceptance notice = %#v", delivery)
		require.Equal(t, accepted.User.ID, delivery.TargetUserID, "external acceptance notice = %#v", delivery)
		require.False(t, delivery.TargetInvitationID.IsValid(), "external acceptance notice = %#v", delivery)
		replay, replayErr := ss.Invitation().AcceptExternalIdentity(ctx, external)
		require.Nil(t, replay, "external acceptance replay")
		require.True(t, store.IsConflict(replayErr), "external acceptance replay")
		class, getErr := ss.Class().Get(ctx, invitation.ClassID)
		require.NoError(t, getErr)
		period, getErr := ss.AcademicPeriod().Get(ctx, invitation.AcademicPeriodID)
		require.NoError(t, getErr)
		existingIssue := studentClassInvitationIssueFixture(t, ss, existingUser, class, period, model.NowUTC())
		existingIssue.Invitation.TargetEmail = existingUser.Email
		require.NoError(t, existingIssue.Invitation.Validate())
		existingInvitation, issueErr := ss.Invitation().IssueStudentClass(ctx, existingIssue)
		require.NoError(t, issueErr)
		existingStateToken, existingBindingToken := model.NewCredentialToken(), model.NewCredentialToken()
		existingState, stateErr := ss.ExternalLoginState().SaveInvitationAdmission(ctx, &store.ExternalLoginStateInvitationAdmission{State: &model.ExternalLoginState{
			Provider: "campus-cas", Purpose: model.ExternalAuthenticationPurposeInvitationAdmission,
			StateHash: model.HashToken(existingStateToken), BindingHash: model.HashToken(existingBindingToken), ReturnTo: "/join",
			ClientType: model.SessionClientWeb,
		}, Lifetime: time.Minute, ClaimHash: existingInvitation.ClaimHash})
		require.NoError(t, stateErr)
		existingState, stateErr = ss.ExternalLoginState().Consume(ctx, &store.ExternalLoginStateConsumption{Provider: existingState.Provider, StateHash: existingState.StateHash, BindingHash: existingState.BindingHash})
		require.NoError(t, stateErr)
		existingAcceptance := studentClassInvitationAcceptanceFixture(t, existingInvitation, model.NowUTC())
		existingAcceptance.AuditEvent.ClientType, existingAcceptance.AuditEvent.AuthMethod = string(model.SessionClientWeb), "cas"
		existingAcceptance.AuditEvent.Parameters, stateErr = model.EncodeAuditData(map[string]string{"provider": "campus-cas"})
		require.NoError(t, stateErr)
		existingCandidateID := existingAcceptance.User.ID
		existingInput := &store.ExternalIdentityInvitationAcceptance{ExternalStateID: existingState.ID,
			Identity:      &model.ExternalIdentity{UserID: existingAcceptance.User.ID, Provider: "campus-cas", Subject: "existing-target-" + model.NewID(), LastSeenAt: model.OptionalTimeFrom(model.NowUTC())},
			ProviderEmail: existingUser.Email, User: existingAcceptance.User, Settings: existingAcceptance.Settings,
			DefaultProfilePictureJob: existingAcceptance.DefaultProfilePictureJob, Affiliation: existingAcceptance.Affiliation,
			ClassMember: existingAcceptance.ClassMember,
			Notice:      &store.PreparedMail{Occurrence: existingAcceptance.Occurrence, Delivery: existingAcceptance.Delivery, Job: existingAcceptance.DeliveryJob},
			AuditEvent:  existingAcceptance.AuditEvent, Capabilities: externalIdentityCapabilities(false), RequiredActions: existingAcceptance.RequiredActions}
		existingResult, existingErr := ss.Invitation().AcceptExternalIdentity(ctx, existingInput)
		require.NoError(t, existingErr)
		require.Equal(t, existingUser.ID, existingResult.User.ID)
		require.Equal(t, existingUser.ID, existingResult.Identity.UserID)
		require.Equal(t, existingUser.ID, existingResult.ClassMember.UserID)
		_, candidateErr := ss.User().Get(ctx, existingCandidateID)
		require.True(t, store.IsNotFound(candidateErr), "existing-User acceptance persisted prepared candidate")
		_, noticeErr := ss.Mail().GetDelivery(ctx, existingAcceptance.Delivery.ID)
		require.True(t, store.IsNotFound(noticeErr), "existing-User acceptance persisted a redundant welcome")
		return
	}
	admitted, err := ss.ExternalIdentity().ResolveOrProvision(ctx, request)
	if mode == model.ProviderAdmissionAutoProvision {
		require.NoError(t, err)
		require.True(t, admitted.Provisioned)
		require.Equal(t, creation.User.ID, admitted.User.ID)
		assertRelationshipFreeUser(t, ctx, ss, admitted.User.ID)
		assertConcurrentExternalAutoProvision(t, ctx, ss, institution)
		return
	}
	require.True(t, store.IsNotFound(err), "%s unlinked admission", mode)
	require.Nil(t, admitted, "%s unlinked admission", mode)
	_, getErr := ss.ExternalIdentity().GetByProviderSubject(ctx, request.Identity.Provider, request.Identity.Subject)
	require.True(t, store.IsNotFound(getErr), "%s persisted an unlinked identity", mode)
}

func establishExternalAdmissionPolicyAdministrator(
	t *testing.T,
	ctx context.Context,
	ss store.Store,
	userID model.UserID,
	institutionID model.InstitutionID,
) {
	t.Helper()
	_, err := ss.PasswordCredential().Save(ctx, &model.PasswordCredential{
		UserID: userID, PasswordHash: "$argon2id$external-admission-policy-test",
	})
	require.NoError(t, err)
	role, err := SaveRole(ctx, ss, &model.Role{
		Name: model.SystemAdministratorRoleName, DisplayName: "System Administrator",
		Permissions: model.AllActions(), BuiltIn: true,
	})
	require.NoError(t, err)
	_, err = SaveRoleBinding(ctx, ss, &model.RoleBinding{
		UserID: userID, RoleID: role.ID, ScopeType: model.RoleScopeInstitution,
		ScopeID: institutionID.String(), StartsAt: model.NowUTC().Add(-time.Second),
	})
	require.NoError(t, err)
}

func replaceExternalAdmissionPolicy(
	t *testing.T,
	ctx context.Context,
	ss store.Store,
	actorID model.UserID,
	institutionID model.InstitutionID,
	enabled bool,
) {
	t.Helper()
	snapshot, err := ss.AccessPolicy().Get(ctx, 1)
	require.NoError(t, err)
	settings := snapshot.Policy.Settings()
	settings.InvitationAdmissionEnabled = enabled
	settings.InvitationLocalCredentialEnabled = enabled
	capabilities := externalIdentityCapabilities(true)
	capabilities.DurableMail = true
	event, err := ss.Audit().Save(ctx, &model.AuditEvent{
		ActorID: actorID, Action: string(model.ActionAccessPolicyManage),
		Resource:  model.Resource{Type: model.ResourceInstitution, ID: institutionID.String()},
		ScopeType: model.RoleScopeInstitution, ScopeID: institutionID.String(),
		Status: model.AuditStatusAttempt, NodeID: "external-identity-store-test",
	})
	require.NoError(t, err)
	nonce := model.NewID()
	command := &store.CommandIdempotency{
		UserID: actorID, Operation: "access_policy.replace",
		KeyDigest:   sha256.Sum256([]byte("external-admission-key:" + nonce)),
		Fingerprint: sha256.Sum256([]byte("external-admission-command:" + nonce)),
		Retention:   time.Hour, Wait: time.Second,
	}
	result, err := ss.AccessPolicy().Replace(ctx, &store.AccessPolicyReplacement{
		Preflight: store.AccessPolicyPreflight{
			ExpectedRevision: snapshot.Policy.Revision, Settings: settings,
			Capabilities: capabilities, CheckedAt: model.NowUTC(),
		},
		ActorID: actorID, AuditEventID: event.ID, AuditAt: event.CreatedAt,
	}, command)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.True(t, result.Changed)
	require.Equal(t, enabled, result.Snapshot.Policy.InvitationAdmissionEnabled)
}

func assertConcurrentExternalAutoProvision(t *testing.T, ctx context.Context, ss store.Store, institution *model.Institution) {
	t.Helper()
	subject := "concurrent-auto-provision-" + model.NewID()
	requests := make([]*store.ExternalIdentityResolutionRequest, 2)
	for index := range requests {
		creation := testUserCreation(newUser(), nil)
		requests[index] = &store.ExternalIdentityResolutionRequest{
			Identity: &model.ExternalIdentity{Provider: "campus-cas", Subject: subject,
				LastSeenAt: model.OptionalTimeFromMillis(model.GetMillis())},
			User: creation.User, Settings: creation.Settings, Capabilities: externalIdentityCapabilities(true),
			AuditEvent: &model.AuditEvent{Action: "authentication.external_provision",
				ScopeType: model.RoleScopeInstitution, ScopeID: institution.ID.String(), Status: model.AuditStatusSuccess,
				NodeID: "concurrent-admission-mode-test", AuthMethod: "cas"},
			DefaultProfilePictureJob: creation.DefaultProfilePictureJob,
		}
	}
	results := make([]*store.ExternalIdentityResolution, len(requests))
	errs := make([]error, len(requests))
	start := make(chan struct{})
	var wait sync.WaitGroup
	for index := range requests {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			<-start
			results[index], errs[index] = ss.ExternalIdentity().ResolveOrProvision(ctx, requests[index])
		}(index)
	}
	close(start)
	wait.Wait()
	for _, err := range errs {
		require.NoError(t, err)
	}
	require.Equal(t, results[1].User.ID, results[0].User.ID)
	require.Equal(t, results[1].Identity.ID, results[0].Identity.ID)
	require.NotEqual(t, results[1].Provisioned, results[0].Provisioned)
	assertRelationshipFreeUser(t, ctx, ss, results[0].User.ID)
}

func assertRelationshipFreeUser(t *testing.T, ctx context.Context, ss store.Store, userID model.UserID) {
	t.Helper()
	affiliations, err := ss.Affiliation().ListByUser(ctx, userID)
	require.NoError(t, err)
	academicMemberships, err := ss.AcademicUnitMember().ListByUser(ctx, userID)
	require.NoError(t, err)
	classMemberships, err := ss.ClassMember().ListByUser(ctx, userID)
	require.NoError(t, err)
	bindings, err := ss.RoleBinding().ListByUser(ctx, userID)
	require.NoError(t, err)
	require.Empty(t, affiliations, "auto-provisioned User gained authority: affiliations")
	require.Empty(t, academicMemberships, "auto-provisioned User gained authority: affiliations")
	require.Empty(t, classMemberships, "auto-provisioned User gained authority: affiliations")
	require.Empty(t, bindings, "auto-provisioned User gained authority: affiliations")
}

func externalIdentityCapabilities(autoProvision bool) store.AccessDeploymentCapabilities {
	return store.AccessDeploymentCapabilities{Providers: map[string]store.AccessProviderCapability{
		"campus-cas": {AutoProvision: autoProvision},
	}}
}
