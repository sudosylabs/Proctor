// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package mlog

import (
	"context"
	"errors"
	"log"
	"log/slog"
	"os"
	"sync"
)

const LevelTrace = slog.Level(-8)

var ErrConfigurationLocked = errors.New("logger configuration is locked")

type core struct {
	mu        sync.RWMutex
	handler   slog.Handler
	resources []*os.File
	locked    bool
	closed    bool
}

type Logger struct {
	core   *core
	logger *slog.Logger
}

func New() (*Logger, error) {
	configured, err := buildHandlers(Config{
		MaxFieldBytes: 16 << 10,
		Targets: []Target{{
			Name:   "console",
			Type:   "console",
			Level:  "info",
			Format: "text",
		}},
	})
	if err != nil {
		return nil, err
	}
	logCore := &core{
		handler:   configured.handler,
		resources: configured.resources,
	}
	handler := &dynamicHandler{core: logCore}
	return &Logger{core: logCore, logger: slog.New(handler)}, nil
}

func (l *Logger) Configure(cfg Config) error {
	l.core.mu.Lock()
	defer l.core.mu.Unlock()
	if l.core.closed {
		return errors.New("logger is closed")
	}
	if l.core.locked {
		return ErrConfigurationLocked
	}

	configured, err := buildHandlers(cfg)
	if err != nil {
		return err
	}
	oldResources := l.core.resources
	l.core.handler = configured.handler
	l.core.resources = configured.resources
	for _, resource := range oldResources {
		_ = resource.Close()
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
	return &Logger{core: l.core, logger: l.logger.With(fieldsToAny(fields)...)}
}

func (l *Logger) Trace(message string, fields ...Field) {
	l.Log(context.Background(), LevelTrace, message, fields...)
}

func (l *Logger) Debug(message string, fields ...Field) {
	l.logger.LogAttrs(context.Background(), slog.LevelDebug, message, fields...)
}

func (l *Logger) Info(message string, fields ...Field) {
	l.logger.LogAttrs(context.Background(), slog.LevelInfo, message, fields...)
}

func (l *Logger) Warn(message string, fields ...Field) {
	l.logger.LogAttrs(context.Background(), slog.LevelWarn, message, fields...)
}

func (l *Logger) Error(message string, fields ...Field) {
	l.logger.LogAttrs(context.Background(), slog.LevelError, message, fields...)
}

func (l *Logger) DebugContext(ctx context.Context, message string, fields ...Field) {
	l.logger.LogAttrs(ctx, slog.LevelDebug, message, fields...)
}

func (l *Logger) InfoContext(ctx context.Context, message string, fields ...Field) {
	l.logger.LogAttrs(ctx, slog.LevelInfo, message, fields...)
}

func (l *Logger) WarnContext(ctx context.Context, message string, fields ...Field) {
	l.logger.LogAttrs(ctx, slog.LevelWarn, message, fields...)
}

func (l *Logger) ErrorContext(ctx context.Context, message string, fields ...Field) {
	l.logger.LogAttrs(ctx, slog.LevelError, message, fields...)
}

func (l *Logger) Log(ctx context.Context, level slog.Level, message string, fields ...Field) {
	l.logger.LogAttrs(ctx, level, message, fields...)
}

func (l *Logger) StdLogger(level slog.Level) *log.Logger {
	return slog.NewLogLogger(&dynamicHandler{core: l.core}, level)
}

func (l *Logger) Flush() error {
	l.core.mu.RLock()
	defer l.core.mu.RUnlock()
	if l.core.closed {
		return nil
	}
	var result error
	for _, resource := range l.core.resources {
		result = errors.Join(result, resource.Sync())
	}
	return result
}

func (l *Logger) Shutdown() error {
	l.core.mu.Lock()
	defer l.core.mu.Unlock()
	if l.core.closed {
		return nil
	}
	l.core.closed = true
	var result error
	for _, resource := range l.core.resources {
		result = errors.Join(result, resource.Sync(), resource.Close())
	}
	l.core.resources = nil
	return result
}

func fieldsToAny(fields []Field) []any {
	values := make([]any, len(fields))
	for index := range fields {
		values[index] = fields[index]
	}
	return values
}
