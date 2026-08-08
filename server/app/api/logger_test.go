// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package api

import (
	"context"
	"testing"

	"github.com/sudosylabs/proctor/server/mlog"
)

type testLogger struct {
	log *mlog.Logger
}

func newTestLogger(tb testing.TB) (Logger, *mlog.Buffer) {
	tb.Helper()
	var logs mlog.Buffer
	logger, err := mlog.New()
	if err != nil {
		tb.Fatal(err)
	}
	tb.Cleanup(func() { _ = logger.Shutdown() })
	if err := logger.Configure(mlog.Config{
		MaxFieldBytes: 1024,
		Targets: []mlog.Target{{
			Name: "test", Type: "console", Level: "debug", Format: "json", Writer: &logs,
		}},
	}); err != nil {
		tb.Fatal(err)
	}
	return testLogger{log: logger}, &logs
}

func (l testLogger) InfoContext(ctx context.Context, message string, fields ...LogField) {
	l.log.InfoContext(ctx, message, testLogFields(fields)...)
}

func (l testLogger) ErrorContext(ctx context.Context, message string, fields ...LogField) {
	l.log.ErrorContext(ctx, message, testLogFields(fields)...)
}

func testLogFields(fields []LogField) []mlog.Field {
	out := make([]mlog.Field, 0, len(fields))
	for _, field := range fields {
		out = append(out, mlog.Any(field.Key, field.Value))
	}
	return out
}
