// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package mlog

import (
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"unicode/utf8"
)

type Target struct {
	Name      string
	Type      string
	Level     string
	Format    string
	File      string
	Writer    io.Writer
	AddSource bool
}

type Config struct {
	MaxFieldBytes int
	Targets       []Target
}

type configuredHandlers struct {
	handler   slog.Handler
	resources []*os.File
}

func buildHandlers(cfg Config) (configuredHandlers, error) {
	if cfg.MaxFieldBytes < 256 {
		return configuredHandlers{}, errors.New("max field bytes must be at least 256")
	}
	if len(cfg.Targets) == 0 {
		return configuredHandlers{}, errors.New("at least one log target is required")
	}

	names := make(map[string]struct{}, len(cfg.Targets))
	handlers := make([]slog.Handler, 0, len(cfg.Targets))
	var resources []*os.File
	closeResources := func() {
		for _, resource := range resources {
			_ = resource.Close()
		}
	}

	for _, target := range cfg.Targets {
		if target.Name == "" {
			closeResources()
			return configuredHandlers{}, errors.New("log target name is required")
		}
		if _, exists := names[target.Name]; exists {
			closeResources()
			return configuredHandlers{}, fmt.Errorf("duplicate log target %q", target.Name)
		}
		names[target.Name] = struct{}{}

		level, err := parseLevel(target.Level)
		if err != nil {
			closeResources()
			return configuredHandlers{}, fmt.Errorf("target %q: %w", target.Name, err)
		}
		writer, resource, err := targetWriter(target)
		if err != nil {
			closeResources()
			return configuredHandlers{}, fmt.Errorf("target %q: %w", target.Name, err)
		}
		if resource != nil {
			resources = append(resources, resource)
		}

		options := &slog.HandlerOptions{
			AddSource:   target.AddSource,
			Level:       level,
			ReplaceAttr: limitAttribute(cfg.MaxFieldBytes),
		}
		var handler slog.Handler
		switch strings.ToLower(target.Format) {
		case "text":
			handler = slog.NewTextHandler(writer, options)
		case "json":
			handler = slog.NewJSONHandler(writer, options)
		default:
			closeResources()
			return configuredHandlers{}, fmt.Errorf("target %q has unsupported format %q", target.Name, target.Format)
		}
		handlers = append(handlers, handler)
	}

	return configuredHandlers{
		handler:   fanoutHandler(handlers),
		resources: resources,
	}, nil
}

func targetWriter(target Target) (io.Writer, *os.File, error) {
	if target.Writer != nil {
		if target.File != "" {
			return nil, nil, errors.New("writer and file cannot both be configured")
		}
		return target.Writer, nil, nil
	}
	switch strings.ToLower(target.Type) {
	case "console":
		return os.Stderr, nil, nil
	case "file":
		if target.File == "" {
			return nil, nil, errors.New("file path is required")
		}
		file, err := os.OpenFile(target.File, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
		if err != nil {
			return nil, nil, fmt.Errorf("open log file: %w", err)
		}
		return file, file, nil
	default:
		return nil, nil, fmt.Errorf("unsupported target type %q", target.Type)
	}
}

func parseLevel(value string) (slog.Level, error) {
	switch strings.ToLower(value) {
	case "trace":
		return LevelTrace, nil
	case "debug":
		return slog.LevelDebug, nil
	case "info":
		return slog.LevelInfo, nil
	case "warn":
		return slog.LevelWarn, nil
	case "error":
		return slog.LevelError, nil
	default:
		return 0, fmt.Errorf("unsupported log level %q", value)
	}
}

func limitAttribute(maxBytes int) func([]string, slog.Attr) slog.Attr {
	return func(_ []string, attribute slog.Attr) slog.Attr {
		attribute.Value = attribute.Value.Resolve()
		switch attribute.Value.Kind() {
		case slog.KindString:
			attribute.Value = slog.StringValue(truncate(attribute.Value.String(), maxBytes))
		case slog.KindAny:
			switch value := attribute.Value.Any().(type) {
			case error:
				attribute.Value = slog.StringValue(truncate(value.Error(), maxBytes))
			case []byte:
				attribute.Value = slog.StringValue(truncate(string(value), maxBytes))
			}
		}
		return attribute
	}
}

func truncate(value string, maxBytes int) string {
	if len(value) <= maxBytes {
		return value
	}
	const suffix = "…"
	limit := maxBytes - len(suffix)
	if limit <= 0 {
		return ""
	}
	for limit > 0 && !utf8.RuneStart(value[limit]) {
		limit--
	}
	return value[:limit] + suffix
}
