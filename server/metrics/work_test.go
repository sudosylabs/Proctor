// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package metrics

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/server/config"
)

func TestWorkMetricsKeepPoolsIndependentAndLabelsClosed(t *testing.T) {
	settings := config.Default().Metrics
	settings.Enabled = true
	module, err := New(settings, BuildInfo{}, Sources{})
	require.NoError(t, err)
	module.SetReady(true)
	module.WorkStarted("password")
	module.WorkStarted("password")
	module.WorkStarted("file_content")
	module.WorkRejected("password")
	module.WorkFinished("password", time.Second)
	module.WorkFinished("file_content", 2*time.Second)
	module.WorkStarted("untrusted-value")
	module.WorkRejected("untrusted-value")
	module.WorkFinished("untrusted-value", time.Second)

	response := httptest.NewRecorder()
	module.handler.ServeHTTP(response, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/metrics", nil))
	require.Equal(t, http.StatusOK, response.Code, "metrics status")
	body := response.Body.String()
	for _, expected := range []string{
		`proctor_work_active{pool="password"} 1`,
		`proctor_work_active{pool="file_content"} 0`,
		`proctor_work_rejected_total{pool="password"} 1`,
		`proctor_work_rejected_total{pool="file_content"} 0`,
		`proctor_work_duration_seconds_count{pool="password"} 1`,
		`proctor_work_duration_seconds_sum{pool="password"} 1`,
		`proctor_work_duration_seconds_sum{pool="file_content"} 2`,
	} {
		assert.Contains(t, body, expected)
	}
	require.NotContains(t, body, "untrusted-value", "unbounded work label was exposed")
	require.True(t, module.ready.Load(), "capacity refusal changed readiness")
}
