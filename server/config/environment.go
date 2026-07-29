// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package config

import (
	"fmt"
	"os"
	"strconv"
	"time"
)

type LookupEnv func(string) (string, bool)

func systemEnvironment(key string) (string, bool) {
	return os.LookupEnv(key)
}

func applyEnvironment(cfg *Config, lookup LookupEnv) ([]string, error) {
	var applied []string
	setString := func(key string, target *string) {
		if value, ok := lookup(key); ok {
			*target = value
			applied = append(applied, key)
		}
	}
	setDuration := func(key string, target *Duration) error {
		value, ok := lookup(key)
		if !ok {
			return nil
		}
		parsed, err := time.ParseDuration(value)
		if err != nil {
			return fmt.Errorf("parse environment %s=%q: %w", key, value, err)
		}
		target.Duration = parsed
		applied = append(applied, key)
		return nil
	}

	setString("PROCTOR_SERVER_LISTEN_ADDRESS", &cfg.Server.ListenAddress)
	setString("PROCTOR_SERVER_PUBLIC_URL", &cfg.Server.PublicURL)
	setString("PROCTOR_DATABASE_DATA_SOURCE", &cfg.Database.DataSource)
	for _, item := range []struct {
		key    string
		target *Duration
	}{
		{"PROCTOR_SERVER_READ_HEADER_TIMEOUT", &cfg.Server.ReadHeaderTimeout},
		{"PROCTOR_SERVER_READ_TIMEOUT", &cfg.Server.ReadTimeout},
		{"PROCTOR_SERVER_WRITE_TIMEOUT", &cfg.Server.WriteTimeout},
		{"PROCTOR_SERVER_IDLE_TIMEOUT", &cfg.Server.IdleTimeout},
		{"PROCTOR_SERVER_SHUTDOWN_TIMEOUT", &cfg.Server.ShutdownTimeout},
	} {
		if err := setDuration(item.key, item.target); err != nil {
			return nil, err
		}
	}
	if err := setInt("PROCTOR_SERVER_MAX_HEADER_BYTES", lookup, &cfg.Server.MaxHeaderBytes, &applied); err != nil {
		return nil, err
	}
	if err := setInt64("PROCTOR_SERVER_MAX_BODY_BYTES", lookup, &cfg.Server.MaxBodyBytes, &applied); err != nil {
		return nil, err
	}
	if err := setInt("PROCTOR_LOG_MAX_FIELD_BYTES", lookup, &cfg.Log.MaxFieldBytes, &applied); err != nil {
		return nil, err
	}
	if err := setInt("PROCTOR_DATABASE_MAX_OPEN_CONNECTIONS", lookup, &cfg.Database.MaxOpenConnections, &applied); err != nil {
		return nil, err
	}
	if err := setInt("PROCTOR_DATABASE_MAX_IDLE_CONNECTIONS", lookup, &cfg.Database.MaxIdleConnections, &applied); err != nil {
		return nil, err
	}
	for _, item := range []struct {
		key    string
		target *Duration
	}{
		{"PROCTOR_DATABASE_CONNECTION_MAX_LIFETIME", &cfg.Database.ConnectionMaxLifetime},
		{"PROCTOR_DATABASE_CONNECTION_MAX_IDLE_TIME", &cfg.Database.ConnectionMaxIdleTime},
		{"PROCTOR_DATABASE_QUERY_TIMEOUT", &cfg.Database.QueryTimeout},
		{"PROCTOR_DATABASE_MIGRATION_TIMEOUT", &cfg.Database.MigrationTimeout},
	} {
		if err := setDuration(item.key, item.target); err != nil {
			return nil, err
		}
	}

	console := consoleTarget(cfg)
	setString("PROCTOR_LOG_LEVEL", &console.Level)
	setString("PROCTOR_LOG_FORMAT", &console.Format)
	return applied, nil
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

func setInt(key string, lookup LookupEnv, target *int, applied *[]string) error {
	value, ok := lookup(key)
	if !ok {
		return nil
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return fmt.Errorf("parse environment %s=%q: %w", key, value, err)
	}
	*target = parsed
	*applied = append(*applied, key)
	return nil
}

func setInt64(key string, lookup LookupEnv, target *int64, applied *[]string) error {
	value, ok := lookup(key)
	if !ok {
		return nil
	}
	parsed, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		return fmt.Errorf("parse environment %s=%q: %w", key, value, err)
	}
	*target = parsed
	*applied = append(*applied, key)
	return nil
}

func removeEnvironmentOverrides(candidate *Config, persisted Config, keys []string) {
	for _, key := range keys {
		switch key {
		case "PROCTOR_SERVER_LISTEN_ADDRESS":
			candidate.Server.ListenAddress = persisted.Server.ListenAddress
		case "PROCTOR_SERVER_PUBLIC_URL":
			candidate.Server.PublicURL = persisted.Server.PublicURL
		case "PROCTOR_SERVER_READ_HEADER_TIMEOUT":
			candidate.Server.ReadHeaderTimeout = persisted.Server.ReadHeaderTimeout
		case "PROCTOR_SERVER_READ_TIMEOUT":
			candidate.Server.ReadTimeout = persisted.Server.ReadTimeout
		case "PROCTOR_SERVER_WRITE_TIMEOUT":
			candidate.Server.WriteTimeout = persisted.Server.WriteTimeout
		case "PROCTOR_SERVER_IDLE_TIMEOUT":
			candidate.Server.IdleTimeout = persisted.Server.IdleTimeout
		case "PROCTOR_SERVER_SHUTDOWN_TIMEOUT":
			candidate.Server.ShutdownTimeout = persisted.Server.ShutdownTimeout
		case "PROCTOR_SERVER_MAX_HEADER_BYTES":
			candidate.Server.MaxHeaderBytes = persisted.Server.MaxHeaderBytes
		case "PROCTOR_SERVER_MAX_BODY_BYTES":
			candidate.Server.MaxBodyBytes = persisted.Server.MaxBodyBytes
		case "PROCTOR_DATABASE_DATA_SOURCE":
			candidate.Database.DataSource = persisted.Database.DataSource
		case "PROCTOR_DATABASE_MAX_OPEN_CONNECTIONS":
			candidate.Database.MaxOpenConnections = persisted.Database.MaxOpenConnections
		case "PROCTOR_DATABASE_MAX_IDLE_CONNECTIONS":
			candidate.Database.MaxIdleConnections = persisted.Database.MaxIdleConnections
		case "PROCTOR_DATABASE_CONNECTION_MAX_LIFETIME":
			candidate.Database.ConnectionMaxLifetime = persisted.Database.ConnectionMaxLifetime
		case "PROCTOR_DATABASE_CONNECTION_MAX_IDLE_TIME":
			candidate.Database.ConnectionMaxIdleTime = persisted.Database.ConnectionMaxIdleTime
		case "PROCTOR_DATABASE_QUERY_TIMEOUT":
			candidate.Database.QueryTimeout = persisted.Database.QueryTimeout
		case "PROCTOR_DATABASE_MIGRATION_TIMEOUT":
			candidate.Database.MigrationTimeout = persisted.Database.MigrationTimeout
		case "PROCTOR_LOG_MAX_FIELD_BYTES":
			candidate.Log.MaxFieldBytes = persisted.Log.MaxFieldBytes
		case "PROCTOR_LOG_LEVEL":
			restoreConsoleField(candidate, persisted, func(target, previous *LogTarget) {
				target.Level = previous.Level
			})
		case "PROCTOR_LOG_FORMAT":
			restoreConsoleField(candidate, persisted, func(target, previous *LogTarget) {
				target.Format = previous.Format
			})
		}
	}
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
