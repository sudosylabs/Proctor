// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package config

import (
	"os"
	"strings"
)

type LookupEnv func(string) (string, bool)

func systemEnvironment(key string) (string, bool) {
	return os.LookupEnv(key)
}

func splitList(value string) []string {
	parts := strings.Split(value, ",")
	result := make([]string, 0, len(parts))
	for _, part := range parts {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			result = append(result, trimmed)
		}
	}
	return result
}

func consoleTarget(cfg *Config) *LogTarget {
	for index := range cfg.Log.Targets {
		if cfg.Log.Targets[index].Name == "console" {
			return &cfg.Log.Targets[index]
		}
	}
	cfg.Log.Targets = append(cfg.Log.Targets, LogTarget{
		Name:   "console",
		Type:   "console",
		Level:  "info",
		Format: "text",
	})
	return &cfg.Log.Targets[len(cfg.Log.Targets)-1]
}

func restoreConsoleField(candidate *Config, persisted Config, restore func(*LogTarget, *LogTarget)) {
	var previous *LogTarget
	for index := range persisted.Log.Targets {
		if persisted.Log.Targets[index].Name == "console" {
			previous = &persisted.Log.Targets[index]
			break
		}
	}
	if previous == nil {
		for index := range candidate.Log.Targets {
			if candidate.Log.Targets[index].Name == "console" {
				candidate.Log.Targets = append(candidate.Log.Targets[:index], candidate.Log.Targets[index+1:]...)
				return
			}
		}
		return
	}
	restore(consoleTarget(candidate), previous)
}
