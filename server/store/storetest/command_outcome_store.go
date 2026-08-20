// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package storetest

import (
	"context"
	"crypto/sha256"
	"testing"
	"time"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

func TestCommandOutcomeStore(t *testing.T, ss store.Store) {
	ctx := context.Background()
	institution := saveInstitution(t, ctx, ss)
	user := saveUser(t, ctx, ss)
	period := &model.AcademicPeriod{Owner: model.NewInstitutionAcademicPeriodOwner(institution.ID), Name: "expiring-outcome", DisplayName: "Expiring Outcome", StartsAt: model.TimeFromMillis(10), EndsAt: model.TimeFromMillis(20)}
	period.PrepareCreate(model.NewAcademicPeriodID(), model.NowUTC())
	audit := saveAcademicPeriodAuditAttempt(t, ctx, ss, institution.ID.String())
	command := &store.CommandIdempotency{UserID: user.ID, Operation: "academic_period.create.v1", KeyDigest: sha256.Sum256([]byte("expiring")), FingerprintVersion: 1, Fingerprint: sha256.Sum256([]byte("command")), OutcomeVersion: 1, Retention: 100 * time.Millisecond, Wait: time.Second}
	if _, err := ss.AcademicPeriod().CreateIdempotently(ctx, &store.AcademicPeriodCreation{Period: period, AuditEventID: audit.ID.String(), AuditAt: model.GetMillis()}, command); err != nil {
		t.Fatal(err)
	}
	if found, err := ss.CommandOutcome().Has(ctx, command); err != nil || !found {
		t.Fatalf("Has(committed) = %v, %v", found, err)
	}
	deadline := time.Now().Add(time.Second)
	for {
		deleted, err := ss.CommandOutcome().DeleteExpired(ctx, 500)
		if err != nil {
			t.Fatal(err)
		}
		if deleted == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("expired command outcome was not deleted")
		}
		time.Sleep(time.Millisecond)
	}
	if found, err := ss.CommandOutcome().Has(ctx, command); err != nil || found {
		t.Fatalf("Has(expired) = %v, %v", found, err)
	}
	if _, err := ss.CommandOutcome().DeleteExpired(ctx, 0); err == nil {
		t.Fatal("DeleteExpired accepted an invalid limit")
	}
}
