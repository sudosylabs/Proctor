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
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

// TestMailInsertHelpersTakeThePrimaryKeyFence proves every shared encrypted
// mail insert refuses a payload sealed with a key other than the required
// primary, so no aggregate (Personal Access Token notices, retention notices,
// and so on) can insert an old-key payload behind a started rekey.
func TestMailInsertHelpersTakeThePrimaryKeyFence(t *testing.T) {
	persistence := openTestStore(t)
	resetTestStore(t, persistence)
	ctx := context.Background()
	_, err := persistence.GetMaster().Exec(ctx, `UPDATE mail_key_state SET required_primary_key_id=? WHERE singleton=TRUE`, strings.Repeat("b", 32))
	require.NoError(t, err)
	staleKeyID := strings.Repeat("a", 32)
	helpers := map[string]func(context.Context, *sqlxTxWrapper) error{
		"recovery": func(ctx context.Context, tx *sqlxTxWrapper) error {
			return insertRecoveryMail(ctx, tx, &model.MailOccurrence{}, &model.MailDelivery{}, &model.Job{}, staleKeyID)
		},
		"security notice": func(ctx context.Context, tx *sqlxTxWrapper) error {
			return insertSecurityNoticeMail(ctx, tx, &model.MailOccurrence{}, &model.MailDelivery{}, &model.Job{}, staleKeyID)
		},
		"invitation": func(ctx context.Context, tx *sqlxTxWrapper) error {
			return insertInvitationMail(ctx, tx, &model.MailOccurrence{}, &model.MailDelivery{}, &model.Job{}, staleKeyID)
		},
	}
	for name, insert := range helpers {
		t.Run(name, func(t *testing.T) {
			_, err := executeSQLTransaction(ctx, persistence.GetMaster().Begin, rawSQLTransactionPolicy[struct{}](false,
				func(_ struct{}, err error) error { return err }),
				func(ctx context.Context, tx *sqlxTxWrapper) (struct{}, error) { return struct{}{}, insert(ctx, tx) })
			require.True(t, store.IsConflict(err), "insert error = %v", err)
		})
	}
}
