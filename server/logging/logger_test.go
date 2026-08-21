// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package logging

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }

func TestLoggerSupportsIndependentTargetsAndLevels(t *testing.T) {
	t.Parallel()

	logger, err := New()
	if err != nil {
		t.Fatal(err)
	}
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
	if err != nil {
		t.Fatal(err)
	}

	logger.Debug("debug entry", String("component", "test"))
	logger.Error("error entry", Int("attempt", 2))
	if err := logger.Flush(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(all.String(), "debug entry") || !strings.Contains(all.String(), "error entry") {
		t.Fatalf("all target = %q", all.String())
	}
	if strings.Contains(errorsOnly.String(), "debug entry") || !strings.Contains(errorsOnly.String(), "error entry") {
		t.Fatalf("error target = %q", errorsOnly.String())
	}
}

func TestLoggerReconfiguresWithoutChangingScopedLoggers(t *testing.T) {
	t.Parallel()

	logger, err := New()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = logger.Shutdown() })
	var first Buffer
	var second Buffer
	if err := logger.Configure(writerConfig(&first, "info")); err != nil {
		t.Fatal(err)
	}
	scoped := logger.With(String("component", "api"))
	scoped.Info("before")

	if err := logger.Configure(writerConfig(&second, "info")); err != nil {
		t.Fatal(err)
	}
	scoped.Info("after")
	if err := logger.Flush(); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(first.String(), "after") {
		t.Fatalf("old target received post-reconfiguration entry: %q", first.String())
	}
	if !strings.Contains(second.String(), "after") || !strings.Contains(second.String(), `"component":"api"`) {
		t.Fatalf("scoped logger did not follow reconfiguration: %q", second.String())
	}
}

func TestLoggerConfigurationLockAndFailurePreserveCurrentTargets(t *testing.T) {
	t.Parallel()

	logger, err := New()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = logger.Shutdown() })
	var output Buffer
	if err := logger.Configure(writerConfig(&output, "info")); err != nil {
		t.Fatal(err)
	}
	if err := logger.Configure(Config{MaxFieldBytes: 10}); err == nil {
		t.Fatal("invalid configuration was accepted")
	}
	logger.Info("still configured")
	if err := logger.Flush(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "still configured") {
		t.Fatal("failed reconfiguration replaced the working target")
	}

	logger.LockConfiguration()
	if err := logger.Configure(writerConfig(&output, "debug")); !errors.Is(err, ErrConfigurationLocked) {
		t.Fatalf("locked Configure() error = %v", err)
	}
}

func TestLoggerLimitsLargeFieldsWithoutBreakingUTF8(t *testing.T) {
	t.Parallel()

	logger, err := New()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = logger.Shutdown() })
	var output Buffer
	cfg := writerConfig(&output, "info")
	cfg.MaxFieldBytes = 256
	if err := logger.Configure(cfg); err != nil {
		t.Fatal(err)
	}
	logger.Info("large", String("value", strings.Repeat("é", 300)))
	if err := logger.Flush(); err != nil {
		t.Fatal(err)
	}

	var entry map[string]any
	if err := json.Unmarshal(output.Bytes(), &entry); err != nil {
		t.Fatalf("output is invalid JSON/UTF-8: %v\n%s", err, output.String())
	}
	value, _ := entry["value"].(string)
	if len(value) > 256 || !strings.HasSuffix(value, "…") {
		t.Fatalf("limited value is %d bytes and %q", len(value), value)
	}
}

func TestFileTargetFlushAndShutdown(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "proctor.log")
	logger, err := New()
	if err != nil {
		t.Fatal(err)
	}
	if err := logger.Configure(Config{
		MaxFieldBytes: 1024,
		Targets: []Target{{
			Name: "file", Type: "file", Level: "info", Format: "json", File: path,
		}},
	}); err != nil {
		t.Fatal(err)
	}
	logger.Info("persisted")
	if err := logger.Flush(); err != nil {
		t.Fatal(err)
	}
	if err := logger.Shutdown(); err != nil {
		t.Fatal(err)
	}
	if err := logger.Shutdown(); err != nil {
		t.Fatalf("second Shutdown() error = %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "persisted") {
		t.Fatalf("file target = %q", data)
	}
}

func TestLoggerDoesNotReflectArbitraryValues(t *testing.T) {
	t.Parallel()
	logger, err := New()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = logger.Shutdown() })
	var output Buffer
	if err := logger.Configure(writerConfig(&output, "info")); err != nil {
		t.Fatal(err)
	}
	secret := struct{ Password string }{Password: "must-not-appear"}
	logger.Info("safe", Any("payload", secret))
	if err := logger.Flush(); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(output.String(), secret.Password) || !strings.Contains(output.String(), "unsupported") {
		t.Fatalf("arbitrary value handling = %q", output.String())
	}
}

func TestLoggerReportsAsynchronousTargetFailures(t *testing.T) {
	t.Parallel()
	logger, err := New()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = logger.Shutdown() })
	if err := logger.Configure(Config{MaxFieldBytes: 1024, Targets: []Target{{
		Name: "broken", Type: "console", Level: "info", Format: "json", Writer: failingWriter{},
	}}}); err != nil {
		t.Fatal(err)
	}
	logger.Error("write fails")
	if err := logger.Flush(); err != nil {
		t.Fatal(err)
	}
	if stats := logger.Stats(); stats.InternalErrors == 0 {
		t.Fatalf("stats = %#v", stats)
	}
}

func writerConfig(output *Buffer, level string) Config {
	return Config{
		MaxFieldBytes: 1024,
		Targets: []Target{{
			Name: "test", Type: "console", Level: level, Format: "json", Writer: output,
		}},
	}
}
