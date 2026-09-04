// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package logging_test

import (
	"strings"
	"testing"

	"github.com/sudosylabs/proctor/server/logging"
)

func TestSourceReportsCallerOutsideLoggingWrapper(t *testing.T) {
	t.Parallel()
	logger, err := logging.New()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = logger.Shutdown() })
	var output logging.Buffer
	if err := logger.Configure(logging.Config{MaxFieldBytes: 1024, Targets: []logging.Target{{
		Name: "test", Type: "console", Level: "info", Format: "json", Writer: &output, AddSource: true,
	}}}); err != nil {
		t.Fatal(err)
	}
	logger.Info("caller")
	if err := logger.Flush(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "caller_test.go") {
		t.Fatalf("source = %q", output.String())
	}
}
