// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
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
	setString("PROCTOR_CACHE_BACKEND", &cfg.Cache.Backend)
	setString("PROCTOR_CACHE_NAMESPACE", &cfg.Cache.Namespace)
	setString("PROCTOR_CACHE_REDIS_USERNAME", &cfg.Cache.Redis.Username)
	setString("PROCTOR_CACHE_REDIS_PASSWORD", &cfg.Cache.Redis.Password)
	if value, ok := lookup("PROCTOR_CACHE_REDIS_ADDRESSES"); ok {
		cfg.Cache.Redis.Addresses = splitList(value)
		applied = append(applied, "PROCTOR_CACHE_REDIS_ADDRESSES")
	}
	setString("PROCTOR_MAIL_BACKEND", &cfg.Mail.Backend)
	setString("PROCTOR_MAIL_FROM_ADDRESS", &cfg.Mail.FromAddress)
	setString("PROCTOR_MAIL_FROM_NAME", &cfg.Mail.FromName)
	setString("PROCTOR_MAIL_SMTP_ADDRESS", &cfg.Mail.SMTP.Address)
	setString("PROCTOR_MAIL_SMTP_SERVER_NAME", &cfg.Mail.SMTP.ServerName)
	setString("PROCTOR_MAIL_SMTP_LOCAL_NAME", &cfg.Mail.SMTP.LocalName)
	setString("PROCTOR_MAIL_SMTP_SECURITY", &cfg.Mail.SMTP.Security)
	setString("PROCTOR_MAIL_SMTP_USERNAME", &cfg.Mail.SMTP.Username)
	setString("PROCTOR_MAIL_SMTP_PASSWORD", &cfg.Mail.SMTP.Password)
	setString("PROCTOR_MAIL_SMTP_AUTHENTICATION", &cfg.Mail.SMTP.Authentication)
	setString("PROCTOR_MAIL_SMTP_MESSAGE_ID_DOMAIN", &cfg.Mail.SMTP.MessageIDDomain)
	setString("PROCTOR_VFS_BACKEND", &cfg.VFS.Backend)
	setString("PROCTOR_VFS_LOCAL_ROOT", &cfg.VFS.Local.Root)
	setString("PROCTOR_VFS_S3_ENDPOINT", &cfg.VFS.S3.Endpoint)
	setString("PROCTOR_VFS_S3_ACCESS_KEY", &cfg.VFS.S3.AccessKey)
	setString("PROCTOR_VFS_S3_SECRET_KEY", &cfg.VFS.S3.SecretKey)
	setString("PROCTOR_VFS_S3_SESSION_TOKEN", &cfg.VFS.S3.SessionToken)
	setString("PROCTOR_VFS_S3_BUCKET", &cfg.VFS.S3.Bucket)
	setString("PROCTOR_VFS_S3_PREFIX", &cfg.VFS.S3.Prefix)
	setString("PROCTOR_VFS_S3_REGION", &cfg.VFS.S3.Region)
	for _, item := range []struct {
		key    string
		target *Duration
	}{
		{"PROCTOR_SERVER_READ_HEADER_TIMEOUT", &cfg.Server.ReadHeaderTimeout},
		{"PROCTOR_SERVER_READ_TIMEOUT", &cfg.Server.ReadTimeout},
		{"PROCTOR_SERVER_WRITE_TIMEOUT", &cfg.Server.WriteTimeout},
		{"PROCTOR_SERVER_IDLE_TIMEOUT", &cfg.Server.IdleTimeout},
		{"PROCTOR_SERVER_SHUTDOWN_TIMEOUT", &cfg.Server.ShutdownTimeout},
		{"PROCTOR_CACHE_REDIS_CONNECT_TIMEOUT", &cfg.Cache.Redis.ConnectTimeout},
		{"PROCTOR_MAIL_SMTP_TIMEOUT", &cfg.Mail.SMTP.Timeout},
		{"PROCTOR_AUTHENTICATION_SESSIONS_ACCESS_TTL", &cfg.Authentication.Sessions.AccessTTL},
		{"PROCTOR_AUTHENTICATION_SESSIONS_REFRESH_TTL", &cfg.Authentication.Sessions.RefreshTTL},
		{"PROCTOR_AUTHENTICATION_SESSIONS_IDLE_TTL", &cfg.Authentication.Sessions.IdleTTL},
		{"PROCTOR_AUTHENTICATION_SESSIONS_ABSOLUTE_TTL", &cfg.Authentication.Sessions.AbsoluteTTL},
		{
			"PROCTOR_AUTHENTICATION_SESSIONS_ACTIVITY_UPDATE_INTERVAL",
			&cfg.Authentication.Sessions.ActivityUpdateInterval,
		},
		{
			"PROCTOR_AUTHENTICATION_LOGIN_RATE_LIMIT_WINDOW",
			&cfg.Authentication.LoginRateLimit.Window,
		},
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
	for _, item := range []struct {
		key    string
		target *int
	}{
		{"PROCTOR_CACHE_REDIS_DATABASE", &cfg.Cache.Redis.Database},
		{"PROCTOR_MAIL_SMTP_MAX_RECIPIENTS", &cfg.Mail.SMTP.MaxRecipients},
		{"PROCTOR_AUTHENTICATION_PASSWORD_MINIMUM_LENGTH", &cfg.Authentication.Password.MinimumLength},
		{"PROCTOR_AUTHENTICATION_PASSWORD_MAXIMUM_LENGTH", &cfg.Authentication.Password.MaximumLength},
		{"PROCTOR_AUTHENTICATION_PASSWORD_ARGON_MEMORY_KIB", &cfg.Authentication.Password.ArgonMemoryKiB},
		{"PROCTOR_AUTHENTICATION_PASSWORD_ARGON_ITERATIONS", &cfg.Authentication.Password.ArgonIterations},
		{"PROCTOR_AUTHENTICATION_PASSWORD_ARGON_PARALLELISM", &cfg.Authentication.Password.ArgonParallelism},
		{"PROCTOR_AUTHENTICATION_PASSWORD_ARGON_SALT_BYTES", &cfg.Authentication.Password.ArgonSaltBytes},
		{"PROCTOR_AUTHENTICATION_PASSWORD_ARGON_KEY_BYTES", &cfg.Authentication.Password.ArgonKeyBytes},
		{"PROCTOR_AUTHENTICATION_SESSIONS_MAXIMUM_PER_USER", &cfg.Authentication.Sessions.MaximumPerUser},
		{"PROCTOR_AUTHENTICATION_LOGIN_RATE_LIMIT_MAXIMUM_ATTEMPTS", &cfg.Authentication.LoginRateLimit.MaximumAttempts},
		{
			"PROCTOR_AUTHENTICATION_LOGIN_RATE_LIMIT_MAXIMUM_SOURCE_ATTEMPTS",
			&cfg.Authentication.LoginRateLimit.MaximumSourceAttempts,
		},
	} {
		if err := setInt(item.key, lookup, item.target, &applied); err != nil {
			return nil, err
		}
	}
	if err := setInt64(
		"PROCTOR_MAIL_SMTP_MAX_MESSAGE_BYTES",
		lookup,
		&cfg.Mail.SMTP.MaxMessageBytes,
		&applied,
	); err != nil {
		return nil, err
	}
	for _, item := range []struct {
		key    string
		target *bool
	}{
		{"PROCTOR_CACHE_REDIS_TLS", &cfg.Cache.Redis.TLS},
		{"PROCTOR_MAIL_ENABLED", &cfg.Mail.Enabled},
		{"PROCTOR_VFS_S3_SECURE", &cfg.VFS.S3.Secure},
	} {
		if err := setBool(item.key, lookup, item.target, &applied); err != nil {
			return nil, err
		}
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

func setBool(key string, lookup LookupEnv, target *bool, applied *[]string) error {
	value, ok := lookup(key)
	if !ok {
		return nil
	}
	parsed, err := strconv.ParseBool(value)
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
		case "PROCTOR_CACHE_BACKEND":
			candidate.Cache.Backend = persisted.Cache.Backend
		case "PROCTOR_CACHE_NAMESPACE":
			candidate.Cache.Namespace = persisted.Cache.Namespace
		case "PROCTOR_CACHE_REDIS_ADDRESSES":
			candidate.Cache.Redis.Addresses = append([]string(nil), persisted.Cache.Redis.Addresses...)
		case "PROCTOR_CACHE_REDIS_USERNAME":
			candidate.Cache.Redis.Username = persisted.Cache.Redis.Username
		case "PROCTOR_CACHE_REDIS_PASSWORD":
			candidate.Cache.Redis.Password = persisted.Cache.Redis.Password
		case "PROCTOR_CACHE_REDIS_DATABASE":
			candidate.Cache.Redis.Database = persisted.Cache.Redis.Database
		case "PROCTOR_CACHE_REDIS_TLS":
			candidate.Cache.Redis.TLS = persisted.Cache.Redis.TLS
		case "PROCTOR_CACHE_REDIS_CONNECT_TIMEOUT":
			candidate.Cache.Redis.ConnectTimeout = persisted.Cache.Redis.ConnectTimeout
		case "PROCTOR_MAIL_ENABLED":
			candidate.Mail.Enabled = persisted.Mail.Enabled
		case "PROCTOR_MAIL_BACKEND":
			candidate.Mail.Backend = persisted.Mail.Backend
		case "PROCTOR_MAIL_FROM_ADDRESS":
			candidate.Mail.FromAddress = persisted.Mail.FromAddress
		case "PROCTOR_MAIL_FROM_NAME":
			candidate.Mail.FromName = persisted.Mail.FromName
		case "PROCTOR_MAIL_SMTP_ADDRESS":
			candidate.Mail.SMTP.Address = persisted.Mail.SMTP.Address
		case "PROCTOR_MAIL_SMTP_SERVER_NAME":
			candidate.Mail.SMTP.ServerName = persisted.Mail.SMTP.ServerName
		case "PROCTOR_MAIL_SMTP_LOCAL_NAME":
			candidate.Mail.SMTP.LocalName = persisted.Mail.SMTP.LocalName
		case "PROCTOR_MAIL_SMTP_SECURITY":
			candidate.Mail.SMTP.Security = persisted.Mail.SMTP.Security
		case "PROCTOR_MAIL_SMTP_USERNAME":
			candidate.Mail.SMTP.Username = persisted.Mail.SMTP.Username
		case "PROCTOR_MAIL_SMTP_PASSWORD":
			candidate.Mail.SMTP.Password = persisted.Mail.SMTP.Password
		case "PROCTOR_MAIL_SMTP_AUTHENTICATION":
			candidate.Mail.SMTP.Authentication = persisted.Mail.SMTP.Authentication
		case "PROCTOR_MAIL_SMTP_TIMEOUT":
			candidate.Mail.SMTP.Timeout = persisted.Mail.SMTP.Timeout
		case "PROCTOR_MAIL_SMTP_MESSAGE_ID_DOMAIN":
			candidate.Mail.SMTP.MessageIDDomain = persisted.Mail.SMTP.MessageIDDomain
		case "PROCTOR_MAIL_SMTP_MAX_MESSAGE_BYTES":
			candidate.Mail.SMTP.MaxMessageBytes = persisted.Mail.SMTP.MaxMessageBytes
		case "PROCTOR_MAIL_SMTP_MAX_RECIPIENTS":
			candidate.Mail.SMTP.MaxRecipients = persisted.Mail.SMTP.MaxRecipients
		case "PROCTOR_VFS_BACKEND":
			candidate.VFS.Backend = persisted.VFS.Backend
		case "PROCTOR_VFS_LOCAL_ROOT":
			candidate.VFS.Local.Root = persisted.VFS.Local.Root
		case "PROCTOR_VFS_S3_ENDPOINT":
			candidate.VFS.S3.Endpoint = persisted.VFS.S3.Endpoint
		case "PROCTOR_VFS_S3_ACCESS_KEY":
			candidate.VFS.S3.AccessKey = persisted.VFS.S3.AccessKey
		case "PROCTOR_VFS_S3_SECRET_KEY":
			candidate.VFS.S3.SecretKey = persisted.VFS.S3.SecretKey
		case "PROCTOR_VFS_S3_SESSION_TOKEN":
			candidate.VFS.S3.SessionToken = persisted.VFS.S3.SessionToken
		case "PROCTOR_VFS_S3_BUCKET":
			candidate.VFS.S3.Bucket = persisted.VFS.S3.Bucket
		case "PROCTOR_VFS_S3_PREFIX":
			candidate.VFS.S3.Prefix = persisted.VFS.S3.Prefix
		case "PROCTOR_VFS_S3_REGION":
			candidate.VFS.S3.Region = persisted.VFS.S3.Region
		case "PROCTOR_VFS_S3_SECURE":
			candidate.VFS.S3.Secure = persisted.VFS.S3.Secure
		case "PROCTOR_AUTHENTICATION_PASSWORD_MINIMUM_LENGTH":
			candidate.Authentication.Password.MinimumLength = persisted.Authentication.Password.MinimumLength
		case "PROCTOR_AUTHENTICATION_PASSWORD_MAXIMUM_LENGTH":
			candidate.Authentication.Password.MaximumLength = persisted.Authentication.Password.MaximumLength
		case "PROCTOR_AUTHENTICATION_PASSWORD_ARGON_MEMORY_KIB":
			candidate.Authentication.Password.ArgonMemoryKiB = persisted.Authentication.Password.ArgonMemoryKiB
		case "PROCTOR_AUTHENTICATION_PASSWORD_ARGON_ITERATIONS":
			candidate.Authentication.Password.ArgonIterations = persisted.Authentication.Password.ArgonIterations
		case "PROCTOR_AUTHENTICATION_PASSWORD_ARGON_PARALLELISM":
			candidate.Authentication.Password.ArgonParallelism = persisted.Authentication.Password.ArgonParallelism
		case "PROCTOR_AUTHENTICATION_PASSWORD_ARGON_SALT_BYTES":
			candidate.Authentication.Password.ArgonSaltBytes = persisted.Authentication.Password.ArgonSaltBytes
		case "PROCTOR_AUTHENTICATION_PASSWORD_ARGON_KEY_BYTES":
			candidate.Authentication.Password.ArgonKeyBytes = persisted.Authentication.Password.ArgonKeyBytes
		case "PROCTOR_AUTHENTICATION_SESSIONS_ACCESS_TTL":
			candidate.Authentication.Sessions.AccessTTL = persisted.Authentication.Sessions.AccessTTL
		case "PROCTOR_AUTHENTICATION_SESSIONS_REFRESH_TTL":
			candidate.Authentication.Sessions.RefreshTTL = persisted.Authentication.Sessions.RefreshTTL
		case "PROCTOR_AUTHENTICATION_SESSIONS_IDLE_TTL":
			candidate.Authentication.Sessions.IdleTTL = persisted.Authentication.Sessions.IdleTTL
		case "PROCTOR_AUTHENTICATION_SESSIONS_ABSOLUTE_TTL":
			candidate.Authentication.Sessions.AbsoluteTTL = persisted.Authentication.Sessions.AbsoluteTTL
		case "PROCTOR_AUTHENTICATION_SESSIONS_ACTIVITY_UPDATE_INTERVAL":
			candidate.Authentication.Sessions.ActivityUpdateInterval =
				persisted.Authentication.Sessions.ActivityUpdateInterval
		case "PROCTOR_AUTHENTICATION_SESSIONS_MAXIMUM_PER_USER":
			candidate.Authentication.Sessions.MaximumPerUser =
				persisted.Authentication.Sessions.MaximumPerUser
		case "PROCTOR_AUTHENTICATION_LOGIN_RATE_LIMIT_WINDOW":
			candidate.Authentication.LoginRateLimit.Window =
				persisted.Authentication.LoginRateLimit.Window
		case "PROCTOR_AUTHENTICATION_LOGIN_RATE_LIMIT_MAXIMUM_ATTEMPTS":
			candidate.Authentication.LoginRateLimit.MaximumAttempts =
				persisted.Authentication.LoginRateLimit.MaximumAttempts
		case "PROCTOR_AUTHENTICATION_LOGIN_RATE_LIMIT_MAXIMUM_SOURCE_ATTEMPTS":
			candidate.Authentication.LoginRateLimit.MaximumSourceAttempts =
				persisted.Authentication.LoginRateLimit.MaximumSourceAttempts
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
