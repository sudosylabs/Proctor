// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package sqlstore

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/server/migrations"
)

// TestBaselineIdentifiersFitPostgreSQLLimit rejects any identifier longer than
// PostgreSQL's 63-byte limit. PostgreSQL truncates such a name with only a
// notice, so two long names can collide and tests that name the constraint
// pass only because the comparison truncates too.
func TestBaselineIdentifiersFitPostgreSQLLimit(t *testing.T) {
	t.Parallel()

	source, err := migrations.Assets.ReadFile("postgres/000001_baseline.up.sql")
	require.NoError(t, err)
	tokens, err := sqlTokens(string(source))
	require.NoError(t, err)
	for _, token := range tokens {
		if isSQLIdentifier(token) {
			assert.LessOrEqual(t, len(token), 63, "identifier %q exceeds 63 bytes", token)
		}
	}
}
