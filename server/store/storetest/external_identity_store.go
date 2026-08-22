// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package storetest

import (
	"context"
	"crypto/sha256"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

func TestExternalIdentityStore(t *testing.T, ss store.Store) {
	t.Run("ConcurrentSessionIssuanceCannotSurviveExactIdentityUnlink", func(t *testing.T) {
		ctx := context.Background()
		user := saveUser(t, ctx, ss)
		_, err := ss.PasswordCredential().Save(ctx, &model.PasswordCredential{UserID: user.ID, PasswordHash: "$argon2id$concurrent-fallback"})
		requireNoError(t, err)
		identity, err := ss.ExternalIdentity().Save(ctx, &model.ExternalIdentity{UserID: user.ID, Provider: "campus-cas",
			Subject: "concurrent-session-" + model.NewId(), LastSeenAt: model.OptionalTimeFromMillis(model.GetMillis())})
		requireNoError(t, err)
		candidate, credentials, _ := newSession(user.ID.String())
		candidate.AuthenticationMethod = "oidc"
		candidate.AuthenticationProviderID = identity.Provider
		candidate.ExternalIdentityID = identity.ID
		audit := saveAuthenticationMethodAuditAttempt(t, ctx, ss, user.ID.String(), "unlink_provider")
		start := make(chan struct{})
		var saved *model.Session
		var saveErr, unlinkErr error
		var wait sync.WaitGroup
		wait.Add(2)
		go func() {
			defer wait.Done()
			<-start
			saved, _, saveErr = ss.Session().Save(ctx, candidate, credentials, 10)
		}()
		go func() {
			defer wait.Done()
			<-start
			_, unlinkErr = ss.ExternalIdentity().UnlinkWithAudit(ctx, &store.ExternalIdentityUnlink{ID: identity.ID,
				UserID: user.ID, Capabilities: store.AccessDeploymentCapabilities{Providers: map[string]store.AccessProviderCapability{"campus-cas": {}}},
				ChangedAt: model.GetMillis(), RevocationReason: model.SessionRevocationExternalIdentityUnlinked, AuditEventID: audit.ID.String(), AuditAt: model.GetMillis()})
		}()
		close(start)
		wait.Wait()
		requireNoError(t, unlinkErr)
		if saveErr != nil && !errors.Is(saveErr, store.ErrAuthenticationMethodDisabled) {
			t.Fatalf("concurrent Session Save error = %v", saveErr)
		}
		if saved != nil {
			persisted, getErr := ss.Session().Get(ctx, saved.ID.String())
			requireNoError(t, getErr)
			if !persisted.RevokedAt.Valid {
				t.Fatalf("concurrent exact-identity Session survived unlink: %#v", persisted)
			}
		}
	})

	t.Run("AuditedLinkIsUniqueAcrossConcurrentUsers", func(t *testing.T) {
		ctx := context.Background()
		first := saveUser(t, ctx, ss)
		second := saveUser(t, ctx, ss)
		subject := "concurrent-subject-" + model.NewId()
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
				_, errs[index] = ss.ExternalIdentity().LinkWithAudit(ctx, &store.ExternalIdentityLink{
					Identity: &model.ExternalIdentity{UserID: users[index], Provider: "campus-cas", Subject: subject,
						LastSeenAt: model.OptionalTimeFromMillis(model.GetMillis())},
					Capabilities: capabilities, AuditEventID: attempts[index].ID.String(), AuditAt: model.GetMillis(),
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
			t.Fatalf("LinkWithAudit() concurrent error = %v", err)
		}
		if succeeded != 1 || conflicted != 1 {
			t.Fatalf("LinkWithAudit() outcomes success=%d conflict=%d errors=%v", succeeded, conflicted, errs)
		}
	})

	t.Run("UnlinkRevokesOnlyExactIdentitySessions", func(t *testing.T) {
		ctx := context.Background()
		user := saveUser(t, ctx, ss)
		_, err := ss.PasswordCredential().Save(ctx, &model.PasswordCredential{UserID: user.ID, PasswordHash: "$argon2id$another-method"})
		requireNoError(t, err)
		otherIdentity, err := ss.ExternalIdentity().Save(ctx, &model.ExternalIdentity{UserID: user.ID,
			Provider: "campus-cas", Subject: "other-revocation-subject-" + model.NewId(),
			LastSeenAt: model.OptionalTimeFromMillis(model.GetMillis())})
		requireNoError(t, err)
		capabilities := store.AccessDeploymentCapabilities{Providers: map[string]store.AccessProviderCapability{
			"campus-cas": {},
		}}
		linkAttempt := saveAuthenticationMethodAuditAttempt(t, ctx, ss, user.ID.String(), "connect_provider")
		linked, err := ss.ExternalIdentity().LinkWithAudit(ctx, &store.ExternalIdentityLink{
			Identity: &model.ExternalIdentity{UserID: user.ID, Provider: "campus-cas",
				Subject: "revocation-subject-" + model.NewId(), LastSeenAt: model.OptionalTimeFromMillis(model.GetMillis())},
			Capabilities: capabilities, AuditEventID: linkAttempt.ID.String(), AuditAt: model.GetMillis(),
		})
		requireNoError(t, err)

		passwordSession, passwordCredentials, _ := newSession(user.ID.String())
		passwordSession, _, err = ss.Session().Save(ctx, passwordSession, passwordCredentials, 10)
		requireNoError(t, err)
		providerSession, providerCredentials, _ := newSession(user.ID.String())
		providerSession.AuthenticationMethod = "oidc"
		providerSession.AuthenticationProviderID = "campus-cas"
		providerSession.ExternalIdentityID = linked.Identity.ID
		providerSession, _, err = ss.Session().Save(ctx, providerSession, providerCredentials, 10)
		requireNoError(t, err)
		otherProviderSession, otherProviderCredentials, _ := newSession(user.ID.String())
		otherProviderSession.AuthenticationMethod = "oidc"
		otherProviderSession.AuthenticationProviderID = "campus-cas"
		otherProviderSession.ExternalIdentityID = otherIdentity.ID
		otherProviderSession, _, err = ss.Session().Save(ctx, otherProviderSession, otherProviderCredentials, 10)
		requireNoError(t, err)

		unlinkAttempt := saveAuthenticationMethodAuditAttempt(t, ctx, ss, user.ID.String(), "unlink_provider")
		result, err := ss.ExternalIdentity().UnlinkWithAudit(ctx, &store.ExternalIdentityUnlink{
			ID: linked.Identity.ID, UserID: user.ID, Capabilities: capabilities, ChangedAt: model.GetMillis(),
			RevocationReason: model.SessionRevocationExternalIdentityUnlinked, AuditEventID: unlinkAttempt.ID.String(), AuditAt: model.GetMillis(),
		})
		requireNoError(t, err)
		if len(result.RevokedSessions) != 1 || result.RevokedSessions[0].ID != providerSession.ID || len(result.RevokedTokenHashes) != 2 {
			t.Fatalf("UnlinkWithAudit() revocations = %#v", result)
		}
		retained, err := ss.Session().Get(ctx, passwordSession.ID.String())
		requireNoError(t, err)
		if retained.RevokedAt.Valid {
			t.Fatalf("password Session was revoked = %#v", retained)
		}
		retained, err = ss.Session().Get(ctx, otherProviderSession.ID.String())
		requireNoError(t, err)
		if retained.RevokedAt.Valid {
			t.Fatalf("other identity Session was revoked = %#v", retained)
		}
		revoked, err := ss.Session().Get(ctx, providerSession.ID.String())
		requireNoError(t, err)
		if !revoked.RevokedAt.Valid || revoked.RevocationReason != model.SessionRevocationExternalIdentityUnlinked {
			t.Fatalf("provider Session was not revoked = %#v", revoked)
		}
	})

	t.Run("SaveGetAndList", func(t *testing.T) {
		ctx := context.Background()
		user := saveUser(t, ctx, ss)
		identity, err := ss.ExternalIdentity().Save(ctx, &model.ExternalIdentity{
			UserID: user.ID, Provider: "campus-cas",
			Subject: "Opaque-Subject/123", LastSeenAt: model.OptionalTimeFromMillis(model.GetMillis()),
		})
		requireNoError(t, err)
		got, err := ss.ExternalIdentity().Get(ctx, identity.ID.String())
		requireNoError(t, err)
		if got.Subject != identity.Subject || got.Provider != "campus-cas" {
			t.Fatalf("Get() = %#v", got)
		}
		bySubject, err := ss.ExternalIdentity().GetByProviderSubject(
			ctx,
			" CAMPUS-CAS ",
			identity.Subject,
		)
		requireNoError(t, err)
		if bySubject.ID != identity.ID {
			t.Fatalf("GetByProviderSubject() = %#v", bySubject)
		}
		list, err := ss.ExternalIdentity().ListByUser(ctx, user.ID.String())
		requireNoError(t, err)
		if len(list) != 1 || list[0].ID != identity.ID {
			t.Fatalf("ListByUser() = %#v", list)
		}
		_, err = ss.ExternalIdentity().Save(ctx, &model.ExternalIdentity{
			UserID: saveUser(t, ctx, ss).ID, Provider: "campus-cas",
			Subject: identity.Subject, LastSeenAt: model.OptionalTimeFromMillis(model.GetMillis()),
		})
		var conflict *store.ErrConflict
		if !errors.As(err, &conflict) ||
			conflict.Constraint != "external_identities_provider_subject_key" {
			t.Fatalf("duplicate identity error = %v", err)
		}
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
				User: creation.User, Settings: creation.Settings, Capabilities: externalIdentityCapabilities(true), ProvisionAudit: &model.AuditEvent{
					Action:    "authentication.external_provision",
					ScopeType: model.RoleScopeInstitution,
					ScopeID:   institution.ID.String(), Status: model.AuditStatusSuccess,
					NodeID: "test-node", AuthMethod: "cas",
				}, DefaultProfilePictureJob: creation.DefaultProfilePictureJob},
		)
		requireNoError(t, err)
		if !resolved.Provisioned ||
			resolved.Identity.UserID != resolved.User.ID ||
			!candidate.ID.IsZero() {
			t.Fatalf("ResolveOrProvision(new) = %#v", resolved)
		}
		queued, err := ss.Job().Get(ctx, creation.DefaultProfilePictureJob.ID)
		requireNoError(t, err)
		if queued.DedupeKey != resolved.User.ID.String() || queued.Status != model.JobStatusQueued {
			t.Fatalf("provisioned default-picture job = %#v", queued)
		}
		settings, err := ss.UserSettings().Get(ctx, resolved.User.ID)
		requireNoError(t, err)
		if settings.Source != model.UserSettingsInitialSource ||
			settings.FormatVersion != model.UserSettingsFormatVersion1 ||
			!settings.CreatedAt.Equal(resolved.User.CreatedAt) {
			t.Fatalf("provisioned user settings = %#v", settings)
		}
		audits, err := ss.Audit().List(ctx, store.AuditListOptions{
			Action:     "authentication.external_provision",
			Limit:      10,
			Visibility: store.AuditVisibilityScope{InstitutionWide: true},
		})
		requireNoError(t, err)
		if len(audits) != 1 ||
			audits[0].ActorID != resolved.User.ID ||
			audits[0].Resource.ID != resolved.User.ID.String() {
			t.Fatalf("provision audits = %#v", audits)
		}
		changedClaims := newUser()
		originalUsername := resolved.User.Username
		again, err := ss.ExternalIdentity().ResolveOrProvision(
			ctx, &store.ExternalIdentityResolutionRequest{Identity: &model.ExternalIdentity{
				Provider: "campus-cas", Subject: "new-subject",
				LastSeenAt: model.OptionalTimeFromMillis(now + 100),
			}, User: changedClaims, Capabilities: externalIdentityCapabilities(false)},
		)
		requireNoError(t, err)
		if again.Provisioned || again.User.ID != resolved.User.ID ||
			again.Identity.LastSeenAt.Millis() != now+100 ||
			again.User.Username != originalUsername || again.User.Username == changedClaims.Username {
			t.Fatalf("ResolveOrProvision(existing) = %#v", again)
		}
	})

	t.Run("NoImplicitAccountLinking", func(t *testing.T) {
		ctx := context.Background()
		institution, err := ss.Institution().GetSingleton(ctx)
		if store.IsNotFound(err) {
			institution = saveInstitution(t, ctx, ss)
			err = nil
		}
		requireNoError(t, err)
		existing := saveUser(t, ctx, ss)
		candidate := newUser()
		candidate.Email = existing.Email
		creation := testUserCreation(candidate, nil)
		_, err = ss.ExternalIdentity().ResolveOrProvision(
			ctx, &store.ExternalIdentityResolutionRequest{Identity: &model.ExternalIdentity{
				Provider: "campus-cas", Subject: "different-subject",
				LastSeenAt: model.OptionalTimeFromMillis(model.GetMillis()),
			}, User: creation.User, Settings: creation.Settings, Capabilities: externalIdentityCapabilities(true), ProvisionAudit: &model.AuditEvent{
				Action:    "authentication.external_provision",
				ScopeType: model.RoleScopeInstitution,
				ScopeID:   institution.ID.String(), Status: model.AuditStatusSuccess,
				NodeID: "test-node", AuthMethod: "cas",
			}, DefaultProfilePictureJob: creation.DefaultProfilePictureJob},
		)
		var conflict *store.ErrConflict
		if !errors.As(err, &conflict) ||
			conflict.Constraint != "users_email_key" {
			t.Fatalf("email collision error = %v", err)
		}
		if _, err := ss.ExternalIdentity().GetByProviderSubject(
			ctx,
			"campus-cas",
			"different-subject",
		); !store.IsNotFound(err) {
			t.Fatalf("colliding identity was persisted: %v", err)
		}
		if _, err := ss.Job().Get(ctx, creation.DefaultProfilePictureJob.ID); !store.IsNotFound(err) {
			t.Fatalf("colliding provision default-picture job was persisted: %v", err)
		}
		if _, err := ss.UserSettings().Get(ctx, creation.User.ID); !store.IsNotFound(err) {
			t.Fatalf("colliding provision settings were persisted: %v", err)
		}
	})

	t.Run("ProvisioningRejectsMismatchedDefaultJobTarget", func(t *testing.T) {
		ctx := context.Background()
		institution, err := ss.Institution().GetSingleton(ctx)
		if store.IsNotFound(err) {
			institution = saveInstitution(t, ctx, ss)
			err = nil
		}
		requireNoError(t, err)
		creation := testUserCreation(newUser(), nil)
		creation.DefaultProfilePictureJob.Command = defaultProfilePictureCommand(model.NewUserID())
		subject := "mismatched-job-" + model.NewId()
		before, err := ss.Audit().List(ctx, store.AuditListOptions{
			Action: "authentication.external_provision", Limit: 100,
			Visibility: store.AuditVisibilityScope{InstitutionWide: true},
		})
		requireNoError(t, err)
		_, err = ss.ExternalIdentity().ResolveOrProvision(
			ctx, &store.ExternalIdentityResolutionRequest{Identity: &model.ExternalIdentity{
				Provider: "campus-cas", Subject: subject,
				LastSeenAt: model.OptionalTimeFromMillis(model.GetMillis()),
			}, User: creation.User, Settings: creation.Settings, Capabilities: externalIdentityCapabilities(true), ProvisionAudit: &model.AuditEvent{
				Action: "authentication.external_provision", ScopeType: model.RoleScopeInstitution,
				ScopeID: institution.ID.String(), Status: model.AuditStatusSuccess,
				NodeID: "test-node", AuthMethod: "cas",
			}, DefaultProfilePictureJob: creation.DefaultProfilePictureJob},
		)
		if err == nil {
			t.Fatal("ResolveOrProvision() accepted a default-picture Job targeting another User")
		}
		if _, err = ss.User().Get(ctx, creation.User.ID.String()); !store.IsNotFound(err) {
			t.Fatalf("provisioned User survived mismatched Job rollback: %v", err)
		}
		if _, err = ss.ExternalIdentity().GetByProviderSubject(ctx, "campus-cas", subject); !store.IsNotFound(err) {
			t.Fatalf("external identity survived mismatched Job rollback: %v", err)
		}
		if _, err = ss.Job().Get(ctx, creation.DefaultProfilePictureJob.ID); !store.IsNotFound(err) {
			t.Fatalf("mismatched provision Job was persisted: %v", err)
		}
		if _, err = ss.UserSettings().Get(ctx, creation.User.ID); !store.IsNotFound(err) {
			t.Fatalf("mismatched provision settings survived rollback: %v", err)
		}
		after, err := ss.Audit().List(ctx, store.AuditListOptions{
			Action: "authentication.external_provision", Limit: 100,
			Visibility: store.AuditVisibilityScope{InstitutionWide: true},
		})
		requireNoError(t, err)
		if len(after) != len(before) {
			t.Fatalf("provision audit survived mismatched Job rollback: before=%d after=%d", len(before), len(after))
		}
		permanent := testUserCreation(newUser(), nil)
		permanent.DefaultProfilePictureJob.DedupePolicy = model.JobDedupePermanent
		_, err = ss.ExternalIdentity().ResolveOrProvision(ctx, &store.ExternalIdentityResolutionRequest{
			Identity: &model.ExternalIdentity{Provider: "campus-cas", Subject: "permanent-job-" + model.NewId(), LastSeenAt: model.OptionalTimeFromMillis(model.GetMillis())},
			User:     permanent.User, Settings: permanent.Settings, Capabilities: externalIdentityCapabilities(true), ProvisionAudit: &model.AuditEvent{
				Action: "authentication.external_provision", ScopeType: model.RoleScopeInstitution,
				ScopeID: institution.ID.String(), Status: model.AuditStatusSuccess, NodeID: "test-node", AuthMethod: "cas",
			}, DefaultProfilePictureJob: permanent.DefaultProfilePictureJob,
		})
		if err == nil {
			t.Fatal("ResolveOrProvision() accepted a permanent-dedupe default-picture intent")
		}
	})

	t.Run("ConfiguredProviderWithoutAutoProvisionCapabilityFailsClosed", func(t *testing.T) {
		_, err := ss.ExternalIdentity().ResolveOrProvision(
			context.Background(), &store.ExternalIdentityResolutionRequest{Identity: &model.ExternalIdentity{
				Provider: "campus-cas", Subject: "unlinked",
				LastSeenAt: model.OptionalTimeFromMillis(model.GetMillis()),
			}, Capabilities: externalIdentityCapabilities(false)},
		)
		if !errors.Is(err, store.ErrAuthenticationMethodDisabled) {
			t.Fatalf("ResolveOrProvision(unlinked) error = %v", err)
		}
	})

	t.Run("ConfiguredProviderRemovalPreservesDurableLink", func(t *testing.T) {
		ctx := context.Background()
		user := saveUser(t, ctx, ss)
		identity, err := ss.ExternalIdentity().Save(ctx, &model.ExternalIdentity{
			UserID: user.ID, Provider: "campus-cas", Subject: "removed-provider-" + model.NewId(),
			LastSeenAt: model.OptionalTimeFromMillis(model.GetMillis()),
		})
		requireNoError(t, err)
		_, err = ss.ExternalIdentity().ResolveOrProvision(ctx, &store.ExternalIdentityResolutionRequest{
			Identity: &model.ExternalIdentity{Provider: identity.Provider, Subject: identity.Subject,
				LastSeenAt: model.OptionalTimeFromMillis(model.GetMillis())},
			Capabilities: store.AccessDeploymentCapabilities{Providers: map[string]store.AccessProviderCapability{}},
		})
		if !errors.Is(err, store.ErrAuthenticationMethodDisabled) {
			t.Fatalf("removed provider resolution error = %v", err)
		}
		preserved, getErr := ss.ExternalIdentity().GetByProviderSubject(ctx, identity.Provider, identity.Subject)
		requireNoError(t, getErr)
		if preserved.ID != identity.ID || preserved.UserID != user.ID {
			t.Fatalf("removed provider changed durable link = %#v", preserved)
		}
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
		requireNoError(t, err)
		institution, err = ss.Institution().GetSingleton(ctx)
		requireNoError(t, err)
	} else {
		institution = saveInstitution(t, ctx, ss)
	}
	existingUser := saveUser(t, ctx, ss)
	existingIdentity, err := ss.ExternalIdentity().Save(ctx, &model.ExternalIdentity{
		UserID: existingUser.ID, Provider: "campus-cas", Subject: "existing-" + model.NewId(),
		LastSeenAt: model.OptionalTimeFromMillis(model.GetMillis()),
	})
	requireNoError(t, err)

	resolved, err := ss.ExternalIdentity().ResolveOrProvision(ctx, &store.ExternalIdentityResolutionRequest{
		Identity: &model.ExternalIdentity{Provider: existingIdentity.Provider, Subject: existingIdentity.Subject,
			LastSeenAt: model.OptionalTimeFromMillis(model.GetMillis())},
		Capabilities: externalIdentityCapabilities(false),
	})
	requireNoError(t, err)
	if resolved.Provisioned || resolved.User.ID != existingUser.ID || resolved.Identity.ID != existingIdentity.ID {
		t.Fatalf("%s existing immutable link resolution = %#v", mode, resolved)
	}

	candidate := newUser()
	if invitation != nil {
		candidate.Email = invitation.TargetEmail
		candidate.EmailVerified = true
	}
	creation := testUserCreation(candidate, nil)
	request := &store.ExternalIdentityResolutionRequest{Identity: &model.ExternalIdentity{
		Provider: "campus-cas", Subject: "unlinked-" + model.NewId(), LastSeenAt: model.OptionalTimeFromMillis(model.GetMillis()),
	}, User: creation.User, Settings: creation.Settings, Capabilities: externalIdentityCapabilities(true), ProvisionAudit: &model.AuditEvent{
		Action: "authentication.external_provision", ScopeType: model.RoleScopeInstitution,
		ScopeID: institution.ID.String(), Status: model.AuditStatusSuccess, NodeID: "admission-mode-test", AuthMethod: "cas",
	}, DefaultProfilePictureJob: creation.DefaultProfilePictureJob}
	if invitation != nil {
		establishExternalAdmissionPolicyAdministrator(t, ctx, ss, existingUser.ID, institution.ID)
		if admitted, admissionErr := ss.ExternalIdentity().ResolveOrProvision(ctx, request); admitted != nil || !store.IsNotFound(admissionErr) {
			t.Fatalf("ordinary resolution admitted an Invitation candidate = %#v, %v", admitted, admissionErr)
		}
		stateToken, bindingToken := model.NewCredentialToken(), model.NewCredentialToken()
		state, stateErr := ss.ExternalLoginState().SaveInvitationAdmission(ctx, &model.ExternalLoginState{
			Provider: "campus-cas", Purpose: model.ExternalAuthenticationPurposeInvitationAdmission,
			StateHash: model.HashToken(stateToken), BindingHash: model.HashToken(bindingToken), ReturnTo: "/join",
			ClientType: model.SessionClientWeb,
		}, time.Minute, invitation.ClaimHash)
		requireNoError(t, stateErr)
		state, stateErr = ss.ExternalLoginState().Consume(ctx, state.Provider, state.StateHash, state.BindingHash)
		requireNoError(t, stateErr)
		acceptance := studentClassInvitationAcceptanceFixture(t, invitation, model.NowUTC())
		acceptance.AuditEvent.ClientType, acceptance.AuditEvent.AuthMethod = string(model.SessionClientWeb), "cas"
		acceptance.AuditEvent.Parameters, stateErr = model.EncodeAuditData(map[string]string{"provider": "campus-cas"})
		requireNoError(t, stateErr)
		external := &store.ExternalIdentityInvitationAcceptance{
			ExternalStateID: state.ID,
			Identity: &model.ExternalIdentity{UserID: acceptance.User.ID, Provider: "campus-cas", Subject: "invited-" + model.NewId(),
				LastSeenAt: model.OptionalTimeFrom(model.NowUTC())},
			ProviderEmail: "provider-" + model.NewId() + "@example.edu",
			User:          acceptance.User, Settings: acceptance.Settings, DefaultProfilePictureJob: acceptance.DefaultProfilePictureJob,
			Affiliation: acceptance.Affiliation, ClassMember: acceptance.ClassMember,
			Notice:     &store.PreparedMail{Occurrence: acceptance.Occurrence, Delivery: acceptance.Delivery, Job: acceptance.DeliveryJob},
			AuditEvent: acceptance.AuditEvent, Capabilities: externalIdentityCapabilities(false),
			RequiredActions: acceptance.RequiredActions,
		}
		if probe.BackdateState != nil {
			expiredToken, expiredBinding := model.NewCredentialToken(), model.NewCredentialToken()
			expiredState, expiredErr := ss.ExternalLoginState().SaveInvitationAdmission(ctx, &model.ExternalLoginState{
				Provider: "campus-cas", Purpose: model.ExternalAuthenticationPurposeInvitationAdmission,
				StateHash: model.HashToken(expiredToken), BindingHash: model.HashToken(expiredBinding), ReturnTo: "/join",
				ClientType: model.SessionClientWeb,
			}, time.Minute, invitation.ClaimHash)
			requireNoError(t, expiredErr)
			expiredState, expiredErr = ss.ExternalLoginState().Consume(ctx, expiredState.Provider, expiredState.StateHash, expiredState.BindingHash)
			requireNoError(t, expiredErr)
			probe.BackdateState(t, expiredState.ID, model.NowUTC().Add(-time.Minute))
			expiredInput := *external
			expiredInput.ExternalStateID = expiredState.ID
			if value, expiredErr := ss.Invitation().AcceptExternalIdentity(ctx, &expiredInput); value != nil || !store.IsConflict(expiredErr) {
				t.Fatalf("expired external Invitation proof = %#v, %v", value, expiredErr)
			}
		}
		removedProvider := *external
		removedProvider.Capabilities = store.AccessDeploymentCapabilities{Providers: map[string]store.AccessProviderCapability{}}
		if value, removedErr := ss.Invitation().AcceptExternalIdentity(ctx, &removedProvider); value != nil ||
			!errors.Is(removedErr, store.ErrAuthenticationMethodDisabled) {
			t.Fatalf("removed provider external Invitation acceptance = %#v, %v", value, removedErr)
		}
		replaceExternalAdmissionPolicy(t, ctx, ss, existingUser.ID, institution.ID, false)
		blocked, blockedErr := ss.Invitation().AcceptExternalIdentity(ctx, external)
		if blocked != nil || !errors.Is(blockedErr, store.ErrAuthenticationMethodDisabled) {
			t.Fatalf("globally disabled invitation admission = %#v, %v", blocked, blockedErr)
		}
		if _, getErr := ss.User().Get(ctx, external.User.ID.String()); !store.IsNotFound(getErr) {
			t.Fatalf("globally disabled invitation admission persisted User: %v", getErr)
		}
		replaceExternalAdmissionPolicy(t, ctx, ss, existingUser.ID, institution.ID, true)
		ownedMailbox := *external
		ownedMailbox.ProviderEmail = existingUser.Email
		if value, ownedErr := ss.Invitation().AcceptExternalIdentity(ctx, &ownedMailbox); value != nil || !store.IsConflict(ownedErr) {
			t.Fatalf("already-owned provider mailbox = %#v, %v", value, ownedErr)
		}
		ownedSubject := *external
		ownedSubject.Identity = &model.ExternalIdentity{UserID: external.User.ID, Provider: existingIdentity.Provider, Subject: existingIdentity.Subject,
			LastSeenAt: model.OptionalTimeFrom(model.NowUTC())}
		if value, ownedErr := ss.Invitation().AcceptExternalIdentity(ctx, &ownedSubject); value != nil || !store.IsConflict(ownedErr) {
			t.Fatalf("already-linked provider subject = %#v, %v", value, ownedErr)
		}
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
				t.Fatalf("concurrent external acceptance %d = %#v, %v", index, result, errorsByIndex[index])
			}
		}
		if accepted == nil || conflicts != 1 {
			t.Fatalf("concurrent external acceptance results=%#v errors=%v", results, errorsByIndex)
		}
		if accepted.Invitation.State != model.InvitationAccepted || accepted.User.ID != external.User.ID ||
			accepted.Identity.UserID != accepted.User.ID || accepted.Identity.Subject != external.Identity.Subject ||
			accepted.Affiliation == nil || accepted.ClassMember == nil || accepted.ClassMember.UserID != accepted.User.ID {
			t.Fatalf("terminal external Invitation acceptance = %#v", accepted)
		}
		if _, credentialErr := ss.PasswordCredential().GetByUser(ctx, accepted.User.ID.String()); !store.IsNotFound(credentialErr) {
			t.Fatalf("external Invitation acceptance created a local credential: %v", credentialErr)
		}
		if delivery, deliveryErr := ss.Mail().GetDelivery(ctx, acceptance.Delivery.ID); deliveryErr != nil ||
			delivery.TargetUserID != accepted.User.ID || delivery.TargetInvitationID.IsValid() {
			t.Fatalf("external acceptance notice = %#v, %v", delivery, deliveryErr)
		}
		if replay, replayErr := ss.Invitation().AcceptExternalIdentity(ctx, external); replay != nil || !store.IsConflict(replayErr) {
			t.Fatalf("external acceptance replay = %#v, %v", replay, replayErr)
		}
		class, getErr := ss.Class().Get(ctx, invitation.ClassID.String())
		requireNoError(t, getErr)
		period, getErr := ss.AcademicPeriod().Get(ctx, invitation.AcademicPeriodID.String())
		requireNoError(t, getErr)
		existingIssue := studentClassInvitationIssueFixture(t, ss, existingUser, class, period, model.NowUTC())
		existingIssue.Invitation.TargetEmail = existingUser.Email
		requireNoError(t, existingIssue.Invitation.Validate())
		existingInvitation, issueErr := ss.Invitation().IssueStudentClass(ctx, existingIssue)
		requireNoError(t, issueErr)
		existingStateToken, existingBindingToken := model.NewCredentialToken(), model.NewCredentialToken()
		existingState, stateErr := ss.ExternalLoginState().SaveInvitationAdmission(ctx, &model.ExternalLoginState{
			Provider: "campus-cas", Purpose: model.ExternalAuthenticationPurposeInvitationAdmission,
			StateHash: model.HashToken(existingStateToken), BindingHash: model.HashToken(existingBindingToken), ReturnTo: "/join",
			ClientType: model.SessionClientWeb,
		}, time.Minute, existingInvitation.ClaimHash)
		requireNoError(t, stateErr)
		existingState, stateErr = ss.ExternalLoginState().Consume(ctx, existingState.Provider, existingState.StateHash, existingState.BindingHash)
		requireNoError(t, stateErr)
		existingAcceptance := studentClassInvitationAcceptanceFixture(t, existingInvitation, model.NowUTC())
		existingAcceptance.AuditEvent.ClientType, existingAcceptance.AuditEvent.AuthMethod = string(model.SessionClientWeb), "cas"
		existingAcceptance.AuditEvent.Parameters, stateErr = model.EncodeAuditData(map[string]string{"provider": "campus-cas"})
		requireNoError(t, stateErr)
		existingCandidateID := existingAcceptance.User.ID
		existingInput := &store.ExternalIdentityInvitationAcceptance{ExternalStateID: existingState.ID,
			Identity:      &model.ExternalIdentity{UserID: existingAcceptance.User.ID, Provider: "campus-cas", Subject: "existing-target-" + model.NewId(), LastSeenAt: model.OptionalTimeFrom(model.NowUTC())},
			ProviderEmail: existingUser.Email, User: existingAcceptance.User, Settings: existingAcceptance.Settings,
			DefaultProfilePictureJob: existingAcceptance.DefaultProfilePictureJob, Affiliation: existingAcceptance.Affiliation,
			ClassMember: existingAcceptance.ClassMember,
			Notice:      &store.PreparedMail{Occurrence: existingAcceptance.Occurrence, Delivery: existingAcceptance.Delivery, Job: existingAcceptance.DeliveryJob},
			AuditEvent:  existingAcceptance.AuditEvent, Capabilities: externalIdentityCapabilities(false), RequiredActions: existingAcceptance.RequiredActions}
		existingResult, existingErr := ss.Invitation().AcceptExternalIdentity(ctx, existingInput)
		requireNoError(t, existingErr)
		if existingResult.User.ID != existingUser.ID || existingResult.Identity.UserID != existingUser.ID ||
			existingResult.ClassMember.UserID != existingUser.ID {
			t.Fatalf("existing canonical User external acceptance = %#v", existingResult)
		}
		if _, candidateErr := ss.User().Get(ctx, existingCandidateID.String()); !store.IsNotFound(candidateErr) {
			t.Fatalf("existing-User acceptance persisted prepared candidate: %v", candidateErr)
		}
		if _, noticeErr := ss.Mail().GetDelivery(ctx, existingAcceptance.Delivery.ID); !store.IsNotFound(noticeErr) {
			t.Fatalf("existing-User acceptance persisted a redundant welcome: %v", noticeErr)
		}
		return
	}
	admitted, err := ss.ExternalIdentity().ResolveOrProvision(ctx, request)
	if mode == model.ProviderAdmissionAutoProvision {
		requireNoError(t, err)
		if !admitted.Provisioned || admitted.User.ID != creation.User.ID {
			t.Fatalf("auto_provision admission = %#v", admitted)
		}
		assertRelationshipFreeUser(t, ctx, ss, admitted.User.ID)
		assertConcurrentExternalAutoProvision(t, ctx, ss, institution)
		return
	}
	if !store.IsNotFound(err) || admitted != nil {
		t.Fatalf("%s unlinked admission = %#v, %v", mode, admitted, err)
	}
	if _, getErr := ss.ExternalIdentity().GetByProviderSubject(ctx, request.Identity.Provider, request.Identity.Subject); !store.IsNotFound(getErr) {
		t.Fatalf("%s persisted an unlinked identity: %v", mode, getErr)
	}
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
	requireNoError(t, err)
	role, err := ss.Role().Save(ctx, &model.Role{
		Name: model.SystemAdministratorRoleName, DisplayName: "System Administrator",
		Permissions: model.AllActions(), BuiltIn: true,
	})
	requireNoError(t, err)
	_, err = ss.RoleBinding().Save(ctx, &model.RoleBinding{
		UserID: userID, RoleID: role.ID, ScopeType: model.RoleScopeInstitution,
		ScopeID: institutionID.String(), StartsAt: model.NowUTC().Add(-time.Second),
	})
	requireNoError(t, err)
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
	requireNoError(t, err)
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
	requireNoError(t, err)
	nonce := model.NewId()
	command := &store.CommandIdempotency{
		UserID: actorID, Operation: "access_policy.replace.v1",
		KeyDigest: sha256.Sum256([]byte("external-admission-key:" + nonce)), FingerprintVersion: 1,
		Fingerprint: sha256.Sum256([]byte("external-admission-command:" + nonce)), OutcomeVersion: 1,
		Retention: time.Hour, Wait: time.Second,
	}
	result, err := ss.AccessPolicy().Replace(ctx, &store.AccessPolicyReplacement{
		Preflight: store.AccessPolicyPreflight{
			ExpectedRevision: snapshot.Policy.Revision, Settings: settings,
			Capabilities: capabilities, CheckedAt: model.NowUTC(),
		},
		ActorID: actorID, AuditEventID: event.ID.String(), AuditAt: model.MillisFromTime(event.CreatedAt),
	}, command)
	requireNoError(t, err)
	if result == nil || !result.Changed || result.Snapshot.Policy.InvitationAdmissionEnabled != enabled {
		t.Fatalf("invitation admission replacement = %#v", result)
	}
}

func assertConcurrentExternalAutoProvision(t *testing.T, ctx context.Context, ss store.Store, institution *model.Institution) {
	t.Helper()
	subject := "concurrent-auto-provision-" + model.NewId()
	requests := make([]*store.ExternalIdentityResolutionRequest, 2)
	for index := range requests {
		creation := testUserCreation(newUser(), nil)
		requests[index] = &store.ExternalIdentityResolutionRequest{
			Identity: &model.ExternalIdentity{Provider: "campus-cas", Subject: subject,
				LastSeenAt: model.OptionalTimeFromMillis(model.GetMillis())},
			User: creation.User, Settings: creation.Settings, Capabilities: externalIdentityCapabilities(true),
			ProvisionAudit: &model.AuditEvent{Action: "authentication.external_provision",
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
		requireNoError(t, err)
	}
	if results[0].User.ID != results[1].User.ID || results[0].Identity.ID != results[1].Identity.ID ||
		results[0].Provisioned == results[1].Provisioned {
		t.Fatalf("concurrent auto-provision outcomes = %#v", results)
	}
	assertRelationshipFreeUser(t, ctx, ss, results[0].User.ID)
}

func assertRelationshipFreeUser(t *testing.T, ctx context.Context, ss store.Store, userID model.UserID) {
	t.Helper()
	affiliations, err := ss.Affiliation().ListByUser(ctx, userID.String())
	requireNoError(t, err)
	academicMemberships, err := ss.AcademicUnitMember().ListByUser(ctx, userID.String())
	requireNoError(t, err)
	classMemberships, err := ss.ClassMember().ListByUser(ctx, userID.String())
	requireNoError(t, err)
	bindings, err := ss.RoleBinding().ListByUser(ctx, userID.String())
	requireNoError(t, err)
	if len(affiliations) != 0 || len(academicMemberships) != 0 || len(classMemberships) != 0 || len(bindings) != 0 {
		t.Fatalf("auto-provisioned User gained authority: affiliations=%#v academic_memberships=%#v class_memberships=%#v role_bindings=%#v",
			affiliations, academicMemberships, classMemberships, bindings)
	}
}

func externalIdentityCapabilities(autoProvision bool) store.AccessDeploymentCapabilities {
	return store.AccessDeploymentCapabilities{Providers: map[string]store.AccessProviderCapability{
		"campus-cas": {AutoProvision: autoProvision},
	}}
}
