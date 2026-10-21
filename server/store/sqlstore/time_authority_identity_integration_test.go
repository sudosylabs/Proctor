//go:build integration

// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package sqlstore

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store/storetest"
)

// timeAuthorityIdentityProbe moves Invitation and User node-clock instants
// five minutes after PostgreSQL time, as a creating node whose clock leads the
// database does. Invitation expiry moves with creation to keep its lifetime.
func timeAuthorityIdentityProbe(persistence *SQLStore) storetest.TimeAuthorityIdentitySQLProbe {
	lead := func(t *testing.T, ctx context.Context, query, id string) {
		t.Helper()
		result, err := persistence.GetMaster().Exec(ctx, query, id)
		require.NoError(t, err)
		affected, affectedErr := result.RowsAffected()
		require.NoError(t, affectedErr, "lead database clock: affected")
		require.Equal(t, int64(1), affected, "lead database clock: affected")
	}
	return storetest.TimeAuthorityIdentitySQLProbe{
		LeadInvitation: func(t *testing.T, ctx context.Context, id model.InvitationID) {
			t.Helper()
			lead(t, ctx, `UPDATE invitations SET created_at=created_at+INTERVAL '5 minutes',
				updated_at=updated_at+INTERVAL '5 minutes', expires_at=expires_at+INTERVAL '5 minutes' WHERE id=?`, id.String())
		},
		LeadUser: func(t *testing.T, ctx context.Context, id model.UserID) {
			t.Helper()
			lead(t, ctx, `UPDATE users SET created_at=created_at+INTERVAL '5 minutes',
				updated_at=updated_at+INTERVAL '5 minutes' WHERE id=?`, id.String())
		},
	}
}

func openTimeAuthorityIdentityStore(t *testing.T) *SQLStore {
	t.Helper()
	persistence := openTestStore(t)
	resetPristineTestStore(t, persistence)
	seedTestAuthenticationPolicy(t, persistence, nil)
	return persistence
}

func TestTimeAuthorityInvitationTransitionsFollowLeadingIssue(t *testing.T) {
	persistence := openTimeAuthorityIdentityStore(t)
	storetest.RunTimeAuthorityInvitationTransitions(t, persistence, timeAuthorityIdentityProbe(persistence))
}

func TestTimeAuthorityOnboardingImportRowOutcomeFollowsLeadingImport(t *testing.T) {
	persistence := openTimeAuthorityIdentityStore(t)
	storetest.RunTimeAuthorityOnboardingImportRowOutcome(t, persistence)
}

func TestTimeAuthorityUserDisableFollowsLeadingUser(t *testing.T) {
	persistence := openTimeAuthorityIdentityStore(t)
	storetest.RunTimeAuthorityUserDisable(t, persistence, timeAuthorityIdentityProbe(persistence))
}
