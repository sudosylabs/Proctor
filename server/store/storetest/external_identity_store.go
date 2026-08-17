// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package storetest

import (
	"context"
	"errors"
	"testing"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

func TestExternalIdentityStore(t *testing.T, ss store.Store) {
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
				User: creation.User, Settings: creation.Settings, AutoProvision: true, ProvisionAudit: &model.AuditEvent{
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
		again, err := ss.ExternalIdentity().ResolveOrProvision(
			ctx, &store.ExternalIdentityResolutionRequest{Identity: &model.ExternalIdentity{
				Provider: "campus-cas", Subject: "new-subject",
				LastSeenAt: model.OptionalTimeFromMillis(now + 100),
			}},
		)
		requireNoError(t, err)
		if again.Provisioned || again.User.ID != resolved.User.ID ||
			again.Identity.LastSeenAt.Millis() != now+100 {
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
			}, User: creation.User, Settings: creation.Settings, AutoProvision: true, ProvisionAudit: &model.AuditEvent{
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
			}, User: creation.User, Settings: creation.Settings, AutoProvision: true, ProvisionAudit: &model.AuditEvent{
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
			User:     permanent.User, Settings: permanent.Settings, AutoProvision: true, ProvisionAudit: &model.AuditEvent{
				Action: "authentication.external_provision", ScopeType: model.RoleScopeInstitution,
				ScopeID: institution.ID.String(), Status: model.AuditStatusSuccess, NodeID: "test-node", AuthMethod: "cas",
			}, DefaultProfilePictureJob: permanent.DefaultProfilePictureJob,
		})
		if err == nil {
			t.Fatal("ResolveOrProvision() accepted a permanent-dedupe default-picture intent")
		}
	})

	t.Run("ProvisioningDisabled", func(t *testing.T) {
		_, err := ss.ExternalIdentity().ResolveOrProvision(
			context.Background(), &store.ExternalIdentityResolutionRequest{Identity: &model.ExternalIdentity{
				Provider: "campus-cas", Subject: "unlinked",
				LastSeenAt: model.OptionalTimeFromMillis(model.GetMillis()),
			}},
		)
		if !store.IsNotFound(err) {
			t.Fatalf("ResolveOrProvision(unlinked) error = %v", err)
		}
	})
}
