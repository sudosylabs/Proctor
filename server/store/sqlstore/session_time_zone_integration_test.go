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
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestSessionsComputeIntervalsInUTCWhateverTheServerZone runs against a server
// whose default zone observes daylight saving time (simulated by startup
// options, which a server default behaves like). An invitation created a few
// days before the October transition must still expire exactly 168 hours
// later, as invitations_lifetime_check requires.
func TestSessionsComputeIntervalsInUTCWhateverTheServerZone(t *testing.T) {
	settings := testSettings(t)
	settings.DataSource = withStartupOptions(t, settings.DataSource, "-c TimeZone=Europe/Paris")
	sqlStore, err := New(context.Background(), settings)
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, sqlStore.Close()) })

	var zone string
	require.NoError(t, sqlStore.GetMaster().Get(context.Background(), &zone, `SHOW TimeZone`))
	require.Equal(t, "UTC", zone)

	created := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
	var expires time.Time
	require.NoError(t, sqlStore.GetMaster().Get(context.Background(), &expires, `SELECT ?::timestamptz + interval '7 days'`, created))
	require.True(t, created.Add(7*24*time.Hour).Equal(expires), "seven days after %s is %s", created, expires)
}

func withStartupOptions(t *testing.T, dataSource, options string) string {
	t.Helper()
	if !strings.Contains(dataSource, "://") {
		return dataSource + " options='" + options + "'"
	}
	parsed, err := url.Parse(dataSource)
	require.NoError(t, err)
	query := parsed.Query()
	query.Set("options", options)
	parsed.RawQuery = query.Encode()
	return parsed.String()
}
