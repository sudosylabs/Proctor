// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package config

import (
	"fmt"
	"strings"
	"time"
)

type LogTarget struct {
	Name       string `json:"Name"`
	Type       string `json:"Type"`
	Level      string `json:"Level"`
	Format     string `json:"Format"`
	File       string `json:"File"`
	QueueSize  int    `json:"QueueSize"`
	MaxSizeMB  int    `json:"MaxSizeMB"`
	MaxAgeDays int    `json:"MaxAgeDays"`
	MaxBackups int    `json:"MaxBackups"`
	Compress   bool   `json:"Compress"`
}

type Log struct {
	MaxFieldBytes   int         `json:"MaxFieldBytes"`
	QueueSize       int         `json:"QueueSize"`
	EnqueueTimeout  Duration    `json:"EnqueueTimeout"`
	FlushTimeout    Duration    `json:"FlushTimeout"`
	ShutdownTimeout Duration    `json:"ShutdownTimeout"`
	Targets         []LogTarget `json:"Targets"`
}

// validateLog checks the logging queue, timeouts, and every target.
func validateLog(log Log, add func(string, string)) {
	if log.MaxFieldBytes < 256 || log.MaxFieldBytes > 1<<20 {
		add("log.max_field_bytes", "must be between 256 and 1048576")
	}
	if log.QueueSize < 1 || log.QueueSize > 1<<20 {
		add("log.queue_size", "must be between 1 and 1048576")
	}
	for _, timeout := range []struct {
		path  string
		value time.Duration
	}{
		{path: "log.enqueue_timeout", value: log.EnqueueTimeout.Duration},
		{path: "log.flush_timeout", value: log.FlushTimeout.Duration},
		{path: "log.shutdown_timeout", value: log.ShutdownTimeout.Duration},
	} {
		if timeout.value <= 0 || timeout.value > time.Minute {
			add(timeout.path, "must be positive and no greater than one minute")
		}
	}
	if len(log.Targets) == 0 {
		add("log.targets", "must contain at least one target")
	}
	names := make(map[string]struct{}, len(log.Targets))
	for index, target := range log.Targets {
		prefix := fmt.Sprintf("log.targets[%d]", index)
		if target.Name == "" {
			add(prefix+".name", "is required")
		} else if _, exists := names[target.Name]; exists {
			add(prefix+".name", "must be unique")
		} else {
			names[target.Name] = struct{}{}
		}
		if target.QueueSize < 1 || target.QueueSize > 1<<20 {
			add(prefix+".queue_size", "must be between 1 and 1048576")
		}
		switch strings.ToLower(target.Type) {
		case "console":
			if target.File != "" {
				add(prefix+".file", "must be empty for a console target")
			}
		case "file":
			if target.File == "" {
				add(prefix+".file", "is required for a file target")
			} else if strings.ContainsAny(target.File, "\x00\r\n") {
				add(prefix+".file", "contains invalid characters")
			}
			if target.MaxSizeMB < 1 || target.MaxSizeMB > 10240 {
				add(prefix+".max_size_mb", "must be between 1 and 10240")
			}
			if target.MaxAgeDays < 0 || target.MaxAgeDays > 3650 {
				add(prefix+".max_age_days", "must be between 0 and 3650")
			}
			if target.MaxBackups < 0 || target.MaxBackups > 10000 {
				add(prefix+".max_backups", "must be between 0 and 10000")
			}
		default:
			add(prefix+".type", "must be console or file")
		}
		switch strings.ToLower(target.Level) {
		case "trace", "debug", "info", "warn", "error":
		default:
			add(prefix+".level", "must be one of trace, debug, info, warn, error")
		}
		switch strings.ToLower(target.Format) {
		case "text", "json":
		default:
			add(prefix+".format", "must be text or json")
		}
	}
}
