// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package logging

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

type correlationKey struct{}

func TestContextFieldsCorrelateRecordsWithoutOverridingExplicitFields(t *testing.T) {
	t.Parallel()

	logger, err := New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = logger.Shutdown() })
	var output Buffer
	require.NoError(t, logger.Configure(Config{
		MaxFieldBytes: 1024,
		Targets:       []Target{{Name: "all", Type: "console", Level: "info", Format: "json", Writer: &output}},
	}))
	scoped := logger.With(String("component", "test"))
	scoped.SetContextFields(func(ctx context.Context) []Field {
		value, _ := ctx.Value(correlationKey{}).(string)
		if value == "" {
			return nil
		}
		return []Field{String("request_id", value), String("trace_id", "trace-"+value)}
	})

	ctx := context.WithValue(context.Background(), correlationKey{}, "abc")
	logger.InfoContext(ctx, "derived")
	logger.InfoContext(ctx, "explicit", String("request_id", "explicit-id"))
	logger.Info("background")
	logger.SetContextFields(nil)
	logger.InfoContext(ctx, "removed")
	require.NoError(t, logger.Flush())

	lines := strings.Split(strings.TrimSpace(output.String()), "\n")
	require.Len(t, lines, 4)
	records := make([]map[string]any, len(lines))
	for index, line := range lines {
		require.NoError(t, json.Unmarshal([]byte(line), &records[index]), line)
	}
	require.Equal(t, "abc", records[0]["request_id"], "extractor installed through a scoped logger applies to its core")
	require.Equal(t, "trace-abc", records[0]["trace_id"])
	require.Equal(t, "explicit-id", records[1]["request_id"], "explicit field wins")
	require.Equal(t, "trace-abc", records[1]["trace_id"])
	require.NotContains(t, records[2], "request_id", "background record gained correlation")
	require.NotContains(t, records[3], "trace_id", "removed extractor still applied")
	require.Equal(t, 1, strings.Count(lines[1], `"request_id"`), "duplicate request_id key")
}
