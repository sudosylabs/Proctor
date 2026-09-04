// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package logging

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	logr "github.com/mattermost/logr/v2"
	"github.com/mattermost/logr/v2/formatters"
	"github.com/mattermost/logr/v2/targets"
)

const (
	defaultQueueSize       = 1024
	defaultTargetQueueSize = 256
	defaultEnqueueTimeout  = 250 * time.Millisecond
	defaultFlushTimeout    = 5 * time.Second
	defaultShutdownTimeout = 10 * time.Second
)

type Target struct {
	Name       string
	Type       string
	Level      string
	Format     string
	File       string
	Writer     io.Writer
	AddSource  bool
	QueueSize  int
	MaxSizeMB  int
	MaxAgeDays int
	MaxBackups int
	Compress   bool
}

type Config struct {
	MaxFieldBytes   int
	QueueSize       int
	EnqueueTimeout  time.Duration
	FlushTimeout    time.Duration
	ShutdownTimeout time.Duration
	Targets         []Target
}

type backend struct {
	engine        *logr.Logr
	logger        logr.Logger
	maxFieldBytes int
}

func buildBackend(cfg Config, diagnostics *diagnostics) (*backend, error) {
	cfg = withDefaults(cfg)
	if cfg.MaxFieldBytes < 256 {
		return nil, errors.New("max field bytes must be at least 256")
	}
	if cfg.QueueSize < 1 || cfg.EnqueueTimeout <= 0 || cfg.FlushTimeout <= 0 || cfg.ShutdownTimeout <= 0 {
		return nil, errors.New("logging queue size and timeouts must be positive")
	}
	if len(cfg.Targets) == 0 {
		return nil, errors.New("at least one log target is required")
	}
	engine, err := logr.New(
		logr.MaxQueueSize(cfg.QueueSize),
		logr.MaxFieldLen(0),
		logr.EnqueueTimeout(cfg.EnqueueTimeout),
		logr.FlushTimeout(cfg.FlushTimeout),
		logr.ShutdownTimeout(cfg.ShutdownTimeout),
		logr.StackFilter("github.com/sudosylabs/proctor/server/logging"),
		logr.OnLoggerError(func(err error) { diagnostics.report(err) }),
		logr.OnQueueFull(func(record *logr.LogRec, _ int) bool {
			if droppable(record.Level()) {
				diagnostics.dropped.Add(1)
				return true
			}
			return false
		}),
		logr.OnTargetQueueFull(func(_ logr.Target, record *logr.LogRec, _ int) bool {
			if droppable(record.Level()) {
				diagnostics.dropped.Add(1)
				return true
			}
			return false
		}),
	)
	if err != nil {
		return nil, fmt.Errorf("construct logging engine: %w", err)
	}
	cleanup := func(cause error) (*backend, error) {
		_ = engine.Shutdown()
		return nil, cause
	}
	names := make(map[string]struct{}, len(cfg.Targets))
	for _, target := range cfg.Targets {
		if strings.TrimSpace(target.Name) == "" {
			return cleanup(errors.New("log target name is required"))
		}
		if _, exists := names[target.Name]; exists {
			return cleanup(fmt.Errorf("duplicate log target %q", target.Name))
		}
		names[target.Name] = struct{}{}
		level, parseErr := parseLevel(target.Level)
		if parseErr != nil {
			return cleanup(fmt.Errorf("target %q: %w", target.Name, parseErr))
		}
		output, outputErr := targetOutput(target)
		if outputErr != nil {
			return cleanup(fmt.Errorf("target %q: %w", target.Name, outputErr))
		}
		formatter, formatErr := targetFormatter(target)
		if formatErr != nil {
			return cleanup(fmt.Errorf("target %q: %w", target.Name, formatErr))
		}
		queueSize := target.QueueSize
		if queueSize == 0 {
			queueSize = defaultTargetQueueSize
		}
		if queueSize < 1 {
			return cleanup(fmt.Errorf("target %q queue size must be positive", target.Name))
		}
		if addErr := engine.AddTarget(output, target.Name, &logr.StdFilter{Lvl: level}, formatter, queueSize); addErr != nil {
			return cleanup(fmt.Errorf("target %q: %w", target.Name, addErr))
		}
	}
	for _, target := range cfg.Targets {
		if buffer, ok := target.Writer.(*Buffer); ok {
			buffer.bindFlush(engine.Flush)
		}
	}
	return &backend{engine: engine, logger: engine.NewLogger(), maxFieldBytes: cfg.MaxFieldBytes}, nil
}

func withDefaults(cfg Config) Config {
	if cfg.QueueSize == 0 {
		cfg.QueueSize = defaultQueueSize
	}
	if cfg.EnqueueTimeout == 0 {
		cfg.EnqueueTimeout = defaultEnqueueTimeout
	}
	if cfg.FlushTimeout == 0 {
		cfg.FlushTimeout = defaultFlushTimeout
	}
	if cfg.ShutdownTimeout == 0 {
		cfg.ShutdownTimeout = defaultShutdownTimeout
	}
	return cfg
}

func targetOutput(target Target) (logr.Target, error) {
	if target.Writer != nil {
		if target.File != "" || strings.EqualFold(target.Type, "file") {
			return nil, errors.New("writer and file cannot both be configured")
		}
		return targets.NewWriterTarget(target.Writer), nil
	}
	switch strings.ToLower(target.Type) {
	case "console":
		return targets.NewWriterTarget(os.Stderr), nil
	case "file":
		if strings.TrimSpace(target.File) == "" {
			return nil, errors.New("file path is required")
		}
		if target.MaxSizeMB < 0 || target.MaxAgeDays < 0 || target.MaxBackups < 0 {
			return nil, errors.New("file rotation limits cannot be negative")
		}
		return targets.NewFileTarget(targets.FileOptions{
			Filename: target.File, MaxSize: target.MaxSizeMB, MaxAge: target.MaxAgeDays,
			MaxBackups: target.MaxBackups, Compress: target.Compress,
		}), nil
	default:
		return nil, fmt.Errorf("unsupported target type %q", target.Type)
	}
}

func targetFormatter(target Target) (logr.Formatter, error) {
	switch strings.ToLower(target.Format) {
	case "text":
		return &formatters.Plain{EnableCaller: target.AddSource}, nil
	case "json":
		return &formatters.JSON{EnableCaller: target.AddSource}, nil
	default:
		return nil, fmt.Errorf("unsupported format %q", target.Format)
	}
}

func parseLevel(value string) (logr.Level, error) {
	switch strings.ToLower(value) {
	case "trace":
		return logr.Trace, nil
	case "debug":
		return logr.Debug, nil
	case "info":
		return logr.Info, nil
	case "warn":
		return logr.Warn, nil
	case "error":
		return logr.Error, nil
	default:
		return logr.Level{}, fmt.Errorf("unsupported log level %q", value)
	}
}

func droppable(level logr.Level) bool {
	return level.ID >= logr.Info.ID
}
