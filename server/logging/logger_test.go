// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package logging

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }

func TestLoggerSupportsIndependentTargetsAndLevels(t *testing.T) {
	t.Parallel()

	logger, err := New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = logger.Shutdown() })
	var all Buffer
	var errorsOnly Buffer
	err = logger.Configure(Config{
		MaxFieldBytes: 1024,
		Targets: []Target{
			{Name: "all", Type: "console", Level: "debug", Format: "json", Writer: &all},
			{Name: "errors", Type: "console", Level: "error", Format: "text", Writer: &errorsOnly},
		},
	})
	require.NoError(t, err)

	logger.Debug("debug entry", String("component", "test"))
	logger.Error("error entry", Int("attempt", 2))
	require.NoError(t, logger.Flush())
	require.Contains(t, all.String(), "debug entry")
	require.Contains(t, all.String(), "error entry")
	require.NotContains(t, errorsOnly.String(), "debug entry")
	require.Contains(t, errorsOnly.String(), "error entry")
}

func TestLoggerReconfiguresWithoutChangingScopedLoggers(t *testing.T) {
	t.Parallel()

	logger, err := New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = logger.Shutdown() })
	var first Buffer
	var second Buffer
	require.NoError(t, logger.Configure(writerConfig(&first, "info")))
	scoped := logger.With(String("component", "api"))
	scoped.Info("before")

	require.NoError(t, logger.Configure(writerConfig(&second, "info")))
	scoped.Info("after")
	require.NoError(t, logger.Flush())
	require.NotContains(t, first.String(), "after")
	require.Contains(t, second.String(), "after")
	require.Contains(t, second.String(), `"component":"api"`)
}

func TestLoggerConfigurationLockAndFailurePreserveCurrentTargets(t *testing.T) {
	t.Parallel()

	logger, err := New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = logger.Shutdown() })
	var output Buffer
	require.NoError(t, logger.Configure(writerConfig(&output, "info")))
	require.Error(t, logger.Configure(Config{MaxFieldBytes: 10}), "invalid configuration was accepted")
	logger.Info("still configured")
	require.NoError(t, logger.Flush())
	require.Contains(t, output.String(), "still configured", "failed reconfiguration replaced the working target")

	logger.LockConfiguration()
	require.ErrorIs(t, logger.Configure(writerConfig(&output, "debug")), ErrConfigurationLocked)
}

func TestLoggerLimitsLargeFieldsWithoutBreakingUTF8(t *testing.T) {
	t.Parallel()

	logger, err := New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = logger.Shutdown() })
	var output Buffer
	cfg := writerConfig(&output, "info")
	cfg.MaxFieldBytes = 256
	require.NoError(t, logger.Configure(cfg))
	logger.Info("large", String("value", strings.Repeat("é", 300)))
	require.NoError(t, logger.Flush())

	var entry map[string]any
	err = json.Unmarshal(output.Bytes(), &entry)
	require.NoError(t, err, "output is invalid JSON/UTF-8: %v\n%s", err, output.String())
	value, _ := entry["value"].(string)
	require.LessOrEqual(t, len(value), 256, "limited value is")
	require.True(t, strings.HasSuffix(value, "…"), "limited value is")
}

func TestFileTargetFlushAndShutdown(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "proctor.log")
	logger, err := New()
	require.NoError(t, err)
	require.NoError(t, logger.Configure(Config{
		MaxFieldBytes: 1024,
		Targets: []Target{{
			Name: "file", Type: "file", Level: "info", Format: "json", File: path,
		}},
	}))
	logger.Info("persisted")
	require.NoError(t, logger.Flush())
	require.NoError(t, logger.Shutdown())
	require.NoError(t, logger.Shutdown())
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Contains(t, string(data), "persisted", "file target")
}

func TestLoggerDoesNotReflectArbitraryValues(t *testing.T) {
	t.Parallel()
	logger, err := New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = logger.Shutdown() })
	var output Buffer
	require.NoError(t, logger.Configure(writerConfig(&output, "info")))
	secret := struct{ Password string }{Password: "must-not-appear"}
	logger.Info("safe", Any("payload", secret))
	require.NoError(t, logger.Flush())
	require.NotContains(t, output.String(), secret.Password)
	require.Contains(t, output.String(), "unsupported")
}

func TestLoggerReportsAsynchronousTargetFailures(t *testing.T) {
	t.Parallel()
	logger, err := New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = logger.Shutdown() })
	require.NoError(t, logger.Configure(Config{MaxFieldBytes: 1024, Targets: []Target{{
		Name: "broken", Type: "console", Level: "info", Format: "json", Writer: failingWriter{},
	}}}))
	logger.Error("write fails")
	require.NoError(t, logger.Flush())
	require.NotEqual(t, uint64(0), logger.Stats().InternalErrors)
}

func writerConfig(output *Buffer, level string) Config {
	return Config{
		MaxFieldBytes: 1024,
		Targets: []Target{{
			Name: "test", Type: "console", Level: level, Format: "json", Writer: output,
		}},
	}
}
