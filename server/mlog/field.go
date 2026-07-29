// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

// Package mlog provides Proctor's structured operational logger.
package mlog

import (
	"log/slog"
	"time"
)

type Field = slog.Attr

func String(key, value string) Field {
	return slog.String(key, value)
}

func Int(key string, value int) Field {
	return slog.Int(key, value)
}

func Int64(key string, value int64) Field {
	return slog.Int64(key, value)
}

func Bool(key string, value bool) Field {
	return slog.Bool(key, value)
}

func Duration(key string, value time.Duration) Field {
	return slog.Duration(key, value)
}

func Time(key string, value time.Time) Field {
	return slog.Time(key, value)
}

func Any(key string, value any) Field {
	return slog.Any(key, value)
}

func Err(err error) Field {
	if err == nil {
		return slog.String("error", "<nil>")
	}
	return slog.String("error", err.Error())
}
