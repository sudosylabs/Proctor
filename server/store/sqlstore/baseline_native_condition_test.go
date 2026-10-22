// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package sqlstore

import (
	"regexp"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/server/migrations"
	"github.com/sudosylabs/proctor/server/model"
)

// TestBaselineNativeConditionDigestDefaultIsTheEmptyInventory keeps the SQL
// default for a new Attempt's Native Condition inventory equal to the model's
// empty-inventory digest, which a domain-tag change would otherwise silently
// desynchronize.
func TestBaselineNativeConditionDigestDefaultIsTheEmptyInventory(t *testing.T) {
	t.Parallel()
	source, err := migrations.Assets.ReadFile("postgres/000001_baseline.up.sql")
	require.NoError(t, err)
	match := regexp.MustCompile(`native_condition_digest varchar\(71\) NOT NULL DEFAULT '([^']+)'`).FindSubmatch(source)
	require.Len(t, match, 2, "baseline declares no native_condition_digest default")
	require.Equal(t, model.NewNativeConditionInventory().Digest, string(match[1]))
}
