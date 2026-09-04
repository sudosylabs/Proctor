// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package httpapi

import (
	"context"
	"os"
	"testing"

	"github.com/sudosylabs/proctor/server/localization"
	"github.com/sudosylabs/proctor/server/logging"
)

type testLogger struct {
	log *logging.Logger
}

func newTestLogger(tb testing.TB) (Logger, *logging.Buffer) {
	tb.Helper()
	var logs logging.Buffer
	logger, err := logging.New()
	if err != nil {
		tb.Fatal(err)
	}
	tb.Cleanup(func() { _ = logger.Shutdown() })
	if err := logger.Configure(logging.Config{
		MaxFieldBytes: 1024,
		Targets: []logging.Target{{
			Name: "test", Type: "console", Level: "debug", Format: "json", Writer: &logs,
		}},
	}); err != nil {
		tb.Fatal(err)
	}
	return testLogger{log: logger}, &logs
}

func newTestLocalizer(tb testing.TB) Localizer {
	tb.Helper()
	localizer, err := localization.New(os.DirFS("../i18n"), localization.EnglishLocale)
	if err != nil {
		tb.Fatal(err)
	}
	return localizer
}

func (l testLogger) InfoContext(ctx context.Context, message string, fields ...LogField) {
	l.log.InfoContext(ctx, message, testLogFields(fields)...)
}

func (l testLogger) ErrorContext(ctx context.Context, message string, fields ...LogField) {
	l.log.ErrorContext(ctx, message, testLogFields(fields)...)
	_ = l.log.Flush(ctx)
}

func testLogFields(fields []LogField) []logging.Field {
	out := make([]logging.Field, 0, len(fields))
	for _, field := range fields {
		out = append(out, logging.Any(field.Key, field.Value))
	}
	return out
}
