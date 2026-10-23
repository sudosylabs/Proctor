// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package server

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/server/logging"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
	"github.com/sudosylabs/proctor/server/store/timerlayer"
)

type slowUserStore struct {
	store.UserStore
	delay time.Duration
}

func (s slowUserStore) GetByEmail(context.Context, string) (*model.User, error) {
	time.Sleep(s.delay)
	return nil, errors.New("lookup failed for student@example.edu")
}

type slowUserRoot struct {
	store.Store
	users store.UserStore
}

func (s slowUserRoot) User() store.UserStore { return s.users }

func TestSlowStoreOperationLogRecordsOnlyOperationAndDuration(t *testing.T) {
	t.Parallel()

	logger, err := logging.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = logger.Shutdown() })
	var output logging.Buffer
	require.NoError(t, logger.Configure(logging.Config{
		MaxFieldBytes: 1024,
		Targets: []logging.Target{{
			Name: "test", Type: "console", Level: "info", Format: "json", Writer: &output,
		}},
	}))
	layer, err := timerlayer.New(
		slowUserRoot{users: slowUserStore{delay: 5 * time.Millisecond}},
		timerlayer.NopRecorder{},
		timerlayer.Options{SlowThreshold: time.Millisecond, Slow: slowStoreOperationLog{logger: logger}},
	)
	require.NoError(t, err)

	_, err = layer.User().GetByEmail(context.Background(), "student@example.edu")
	require.Error(t, err)
	require.NoError(t, logger.Flush(context.Background()))

	line := strings.TrimSpace(output.String())
	require.NotContains(t, line, "student@example.edu", "slow-operation log leaked a store argument or error")
	var record map[string]any
	require.NoError(t, json.Unmarshal([]byte(line), &record), "slow-operation record %q", line)
	require.Equal(t, "slow store operation", record["msg"])
	require.Equal(t, "user.get_by_email", record["operation"])
	require.GreaterOrEqual(t, record["duration_ms"], float64(5))
}
