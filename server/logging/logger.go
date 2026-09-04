// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package logging

import (
	"context"
	"errors"
	"fmt"
	"log"
	"log/slog"
	"os"
	"strings"
	"sync"
	"sync/atomic"

	logr "github.com/mattermost/logr/v2"
)

const LevelTrace = slog.Level(-8)

var ErrConfigurationLocked = errors.New("logger configuration is locked")

type diagnostics struct {
	dropped        atomic.Uint64
	internalErrors atomic.Uint64
}

func (d *diagnostics) report(err error) {
	d.internalErrors.Add(1)
	if err != nil {
		_, _ = fmt.Fprintln(os.Stderr, "logging backend error:", boundedDiagnostic(err.Error(), 1024))
	}
}

type Stats struct {
	Dropped        uint64
	InternalErrors uint64
}

type core struct {
	mu          sync.RWMutex
	backend     *backend
	locked      bool
	closed      bool
	diagnostics diagnostics
}

type Logger struct {
	core   *core
	fields []Field
}

func New() (*Logger, error) {
	logCore := &core{}
	configured, err := buildBackend(Config{
		MaxFieldBytes: 16 << 10,
		Targets:       []Target{{Name: "console", Type: "console", Level: "info", Format: "text"}},
	}, &logCore.diagnostics)
	if err != nil {
		return nil, err
	}
	logCore.backend = configured
	return &Logger{core: logCore}, nil
}

func (l *Logger) Configure(cfg Config) error {
	if l == nil || l.core == nil {
		return errors.New("logger is nil")
	}
	l.core.mu.RLock()
	closed, locked := l.core.closed, l.core.locked
	l.core.mu.RUnlock()
	if closed {
		return errors.New("logger is closed")
	}
	if locked {
		return ErrConfigurationLocked
	}
	configured, err := buildBackend(cfg, &l.core.diagnostics)
	if err != nil {
		return err
	}
	l.core.mu.Lock()
	if l.core.closed || l.core.locked {
		closed, locked = l.core.closed, l.core.locked
		l.core.mu.Unlock()
		_ = configured.engine.Shutdown()
		if closed {
			return errors.New("logger is closed")
		}
		if locked {
			return ErrConfigurationLocked
		}
	}
	previous := l.core.backend
	l.core.backend = configured
	l.core.mu.Unlock()
	if previous != nil {
		if shutdownErr := previous.engine.Shutdown(); shutdownErr != nil {
			l.core.diagnostics.report(shutdownErr)
		}
	}
	return nil
}

func (l *Logger) LockConfiguration() bool {
	l.core.mu.Lock()
	defer l.core.mu.Unlock()
	previous := l.core.locked
	l.core.locked = true
	return previous
}

func (l *Logger) UnlockConfiguration() bool {
	l.core.mu.Lock()
	defer l.core.mu.Unlock()
	previous := l.core.locked
	l.core.locked = false
	return previous
}

func (l *Logger) With(fields ...Field) *Logger {
	combined := make([]Field, 0, len(l.fields)+len(fields))
	combined = append(combined, l.fields...)
	combined = append(combined, fields...)
	return &Logger{core: l.core, fields: combined}
}

func (l *Logger) Enabled(level slog.Level) bool {
	backend := l.currentBackend()
	return backend != nil && backend.logger.IsLevelEnabled(toLogrLevel(level))
}

func (l *Logger) Trace(message string, fields ...Field) {
	l.Log(context.Background(), LevelTrace, message, fields...)
}
func (l *Logger) Debug(message string, fields ...Field) {
	l.Log(context.Background(), slog.LevelDebug, message, fields...)
}
func (l *Logger) Info(message string, fields ...Field) {
	l.Log(context.Background(), slog.LevelInfo, message, fields...)
}
func (l *Logger) Warn(message string, fields ...Field) {
	l.Log(context.Background(), slog.LevelWarn, message, fields...)
}
func (l *Logger) Error(message string, fields ...Field) {
	l.Log(context.Background(), slog.LevelError, message, fields...)
}

func (l *Logger) DebugContext(ctx context.Context, message string, fields ...Field) {
	l.Log(ctx, slog.LevelDebug, message, fields...)
}
func (l *Logger) InfoContext(ctx context.Context, message string, fields ...Field) {
	l.Log(ctx, slog.LevelInfo, message, fields...)
}
func (l *Logger) WarnContext(ctx context.Context, message string, fields ...Field) {
	l.Log(ctx, slog.LevelWarn, message, fields...)
}
func (l *Logger) ErrorContext(ctx context.Context, message string, fields ...Field) {
	l.Log(ctx, slog.LevelError, message, fields...)
}

func (l *Logger) Log(ctx context.Context, level slog.Level, message string, fields ...Field) {
	if l == nil || l.core == nil {
		return
	}
	l.core.mu.RLock()
	defer l.core.mu.RUnlock()
	if l.core.closed || l.core.backend == nil {
		return
	}
	backend := l.core.backend
	combined := make([]Field, 0, len(l.fields)+len(fields))
	combined = append(combined, l.fields...)
	combined = append(combined, fields...)
	backend.logger.Log(
		toLogrLevel(level),
		truncateUTF8(message, backend.maxFieldBytes),
		unwrapFields(combined, backend.maxFieldBytes)...,
	)
}

func (l *Logger) StdLogger(level slog.Level) *log.Logger {
	return log.New(stdWriter{logger: l, level: level}, "", 0)
}

func (l *Logger) Flush(contexts ...context.Context) error {
	backend := l.currentBackend()
	if backend == nil {
		return nil
	}
	ctx := firstContext(contexts)
	return backend.engine.FlushWithTimeout(ctx)
}

func (l *Logger) Shutdown(contexts ...context.Context) error {
	if l == nil || l.core == nil {
		return nil
	}
	l.core.mu.Lock()
	if l.core.closed {
		l.core.mu.Unlock()
		return nil
	}
	l.core.closed = true
	backend := l.core.backend
	l.core.backend = nil
	l.core.mu.Unlock()
	if backend == nil {
		return nil
	}
	ctx := firstContext(contexts)
	return backend.engine.ShutdownWithTimeout(ctx)
}

func (l *Logger) Stats() Stats {
	if l == nil || l.core == nil {
		return Stats{}
	}
	return Stats{Dropped: l.core.diagnostics.dropped.Load(), InternalErrors: l.core.diagnostics.internalErrors.Load()}
}

func (l *Logger) currentBackend() *backend {
	if l == nil || l.core == nil {
		return nil
	}
	l.core.mu.RLock()
	defer l.core.mu.RUnlock()
	if l.core.closed {
		return nil
	}
	return l.core.backend
}

type stdWriter struct {
	logger *Logger
	level  slog.Level
}

func (w stdWriter) Write(value []byte) (int, error) {
	w.logger.Log(context.Background(), w.level, strings.TrimSuffix(string(value), "\n"))
	return len(value), nil
}

func toLogrLevel(level slog.Level) logr.Level {
	switch {
	case level <= LevelTrace:
		return logr.Trace
	case level < slog.LevelInfo:
		return logr.Debug
	case level < slog.LevelWarn:
		return logr.Info
	case level < slog.LevelError:
		return logr.Warn
	default:
		return logr.Error
	}
}

func boundedDiagnostic(value string, max int) string {
	return truncateUTF8(value, max)
}

func firstContext(contexts []context.Context) context.Context {
	if len(contexts) > 0 && contexts[0] != nil {
		return contexts[0]
	}
	return context.Background()
}

func unwrapFields(fields []Field, maxBytes int) []logr.Field {
	result := make([]logr.Field, 0, len(fields))
	for _, field := range fields {
		value := field.value
		value.Key = truncateUTF8(value.Key, 128)
		switch value.Type {
		case logr.StringType:
			value.String = truncateUTF8(value.String, maxBytes)
		case logr.ErrorType:
			value = logr.String(value.Key, truncateUTF8(fmt.Sprint(value.Interface), maxBytes))
		}
		result = append(result, value)
	}
	return result
}

func truncateUTF8(value string, maxBytes int) string {
	if maxBytes <= 0 || len(value) <= maxBytes {
		return value
	}
	const suffix = "…"
	limit := maxBytes - len(suffix)
	if limit <= 0 {
		return ""
	}
	for limit > 0 && value[limit]&0xc0 == 0x80 {
		limit--
	}
	return value[:limit] + suffix
}
