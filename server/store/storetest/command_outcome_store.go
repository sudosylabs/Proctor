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
	"testing"
	"time"

	"github.com/stretchr/testify/require"

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
	command := &store.CommandIdempotency{UserID: user.ID, Operation: "academic_period.create.v1", KeyDigest: sha256.Sum256([]byte("expiring")), Fingerprint: sha256.Sum256([]byte("command")), Retention: 100 * time.Millisecond, Wait: time.Second}
	_, err := ss.AcademicPeriod().CreateIdempotently(ctx, &store.AcademicPeriodCreation{Period: period, AuditEventID: audit.ID, AuditAt: model.GetMillis()}, command)
	require.NoError(t, err)
	found, err := ss.CommandOutcome().Has(ctx, command)
	require.NoError(t, err, "Has(committed)")
	require.True(t, found, "Has(committed)")
	deadline := time.Now().Add(time.Second)
	for {
		deleted, err := ss.CommandOutcome().DeleteExpired(ctx, 500)
		require.NoError(t, err)
		if deleted == 1 {
			break
		}
		require.False(t, time.Now().After(deadline), "expired command outcome was not deleted")
		time.Sleep(time.Millisecond)
	}
	found, err = ss.CommandOutcome().Has(ctx, command)
	require.NoError(t, err, "Has(expired)")
	require.False(t, found, "Has(expired)")
	_, err = ss.CommandOutcome().DeleteExpired(ctx, 0)
	require.Error(t, err, "DeleteExpired accepted an invalid limit")
}
