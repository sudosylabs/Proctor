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
			conflict.Constraint != "external_identities_active_subject_key" {
			t.Fatalf("duplicate identity error = %v", err)
		}
	})

	t.Run("ResolveAndProvision", func(t *testing.T) {
		ctx := context.Background()
		institution := saveInstitution(t, ctx, ss)
		now := model.GetMillis()
		candidate := newUser()
		resolved, err := ss.ExternalIdentity().ResolveOrProvision(
			ctx,
			&model.ExternalIdentity{
				Provider: "campus-cas", Subject: "new-subject",
				LastSeenAt: model.OptionalTimeFromMillis(now),
			},
			candidate,
			true,
			&model.AuditEvent{
				Action:    "authentication.external_provision",
				ScopeType: model.RoleScopeInstitution,
				ScopeID:   institution.ID.String(), Status: model.AuditStatusSuccess,
				NodeID: "test-node", AuthMethod: "cas",
			},
		)
		requireNoError(t, err)
		if !resolved.Provisioned ||
			resolved.Identity.UserID != resolved.User.ID ||
			!candidate.ID.IsZero() {
			t.Fatalf("ResolveOrProvision(new) = %#v", resolved)
		}
		audits, err := ss.Audit().List(ctx, store.AuditListOptions{
			Action: "authentication.external_provision",
			Limit:  10,
		})
		requireNoError(t, err)
		if len(audits) != 1 ||
			audits[0].ActorID != resolved.User.ID ||
			audits[0].Resource.Id != resolved.User.ID.String() {
			t.Fatalf("provision audits = %#v", audits)
		}
		again, err := ss.ExternalIdentity().ResolveOrProvision(
			ctx,
			&model.ExternalIdentity{
				Provider: "campus-cas", Subject: "new-subject",
				LastSeenAt: model.OptionalTimeFromMillis(now+100),
			},
			nil,
			false,
			nil,
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
		_, err = ss.ExternalIdentity().ResolveOrProvision(
			ctx,
			&model.ExternalIdentity{
				Provider: "campus-cas", Subject: "different-subject",
				LastSeenAt: model.OptionalTimeFromMillis(model.GetMillis()),
			},
			candidate,
			true,
			&model.AuditEvent{
				Action:    "authentication.external_provision",
				ScopeType: model.RoleScopeInstitution,
				ScopeID:   institution.ID.String(), Status: model.AuditStatusSuccess,
				NodeID: "test-node", AuthMethod: "cas",
			},
		)
		var conflict *store.ErrConflict
		if !errors.As(err, &conflict) ||
			conflict.Constraint != "users_active_email_key" {
			t.Fatalf("email collision error = %v", err)
		}
		if _, err := ss.ExternalIdentity().GetByProviderSubject(
			ctx,
			"campus-cas",
			"different-subject",
		); !store.IsNotFound(err) {
			t.Fatalf("colliding identity was persisted: %v", err)
		}
	})

	t.Run("ProvisioningDisabled", func(t *testing.T) {
		_, err := ss.ExternalIdentity().ResolveOrProvision(
			context.Background(),
			&model.ExternalIdentity{
				Provider: "campus-cas", Subject: "unlinked",
				LastSeenAt: model.OptionalTimeFromMillis(model.GetMillis()),
			},
			nil,
			false,
			nil,
		)
		if !store.IsNotFound(err) {
			t.Fatalf("ResolveOrProvision(unlinked) error = %v", err)
		}
	})
}
