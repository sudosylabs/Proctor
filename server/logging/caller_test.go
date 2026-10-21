// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package logging_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/server/logging"
)

func TestSourceReportsCallerOutsideLoggingWrapper(t *testing.T) {
	t.Parallel()
	logger, err := logging.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = logger.Shutdown() })
	var output logging.Buffer
	require.NoError(t, logger.Configure(logging.Config{MaxFieldBytes: 1024, Targets: []logging.Target{{
		Name: "test", Type: "console", Level: "info", Format: "json", Writer: &output, AddSource: true,
	}}}))
	logger.Info("caller")
	require.NoError(t, logger.Flush())
	require.Contains(t, output.String(), "caller_test.go")
}
