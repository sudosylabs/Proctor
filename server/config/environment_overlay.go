// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package config

import (
	"fmt"
	"strconv"
	"time"
)

type environmentOverride struct {
	key     string
	apply   func(*Config, string) error
	restore func(*Config, Config)
}

type appliedEnvironment struct {
	definitions []environmentOverride
}

func (overlay appliedEnvironment) clone() appliedEnvironment {
	return appliedEnvironment{
		definitions: append([]environmentOverride(nil), overlay.definitions...),
	}
}

func (overlay appliedEnvironment) keys() []string {
	if len(overlay.definitions) == 0 {
		return nil
	}
	keys := make([]string, 0, len(overlay.definitions))
	for _, definition := range overlay.definitions {
		keys = append(keys, definition.key)
	}
	return keys
}

func (overlay appliedEnvironment) restore(candidate *Config, persisted Config) {
	for _, definition := range overlay.definitions {
		definition.restore(candidate, persisted)
	}
}

func scalarEnvironmentOverride[T any](
	key string,
	field func(*Config) *T,
	parse func(string) (T, error),
) environmentOverride {
	return environmentOverride{
		key: key,
		apply: func(cfg *Config, value string) error {
			parsed, err := parse(value)
			if err != nil {
				return fmt.Errorf("parse environment %s=%q: %w", key, value, err)
			}
			*field(cfg) = parsed
			return nil
		},
		restore: func(candidate *Config, persisted Config) {
			*field(candidate) = *field(&persisted)
		},
	}
}

func stringEnvironmentOverride(
	key string,
	field func(*Config) *string,
) environmentOverride {
	return scalarEnvironmentOverride(key, field, func(value string) (string, error) {
		return value, nil
	})
}

func durationEnvironmentOverride(
	key string,
	field func(*Config) *Duration,
) environmentOverride {
	return scalarEnvironmentOverride(key, field, func(value string) (Duration, error) {
		parsed, err := time.ParseDuration(value)
		return Duration{Duration: parsed}, err
	})
}

func intEnvironmentOverride(
	key string,
	field func(*Config) *int,
) environmentOverride {
	return scalarEnvironmentOverride(key, field, strconv.Atoi)
}

func int64EnvironmentOverride(
	key string,
	field func(*Config) *int64,
) environmentOverride {
	return scalarEnvironmentOverride(key, field, func(value string) (int64, error) {
		return strconv.ParseInt(value, 10, 64)
	})
}

func boolEnvironmentOverride(
	key string,
	field func(*Config) *bool,
) environmentOverride {
	return scalarEnvironmentOverride(key, field, strconv.ParseBool)
}

func stringListEnvironmentOverride(
	key string,
	field func(*Config) *[]string,
) environmentOverride {
	return environmentOverride{
		key: key,
		apply: func(cfg *Config, value string) error {
			*field(cfg) = splitList(value)
			return nil
		},
		restore: func(candidate *Config, persisted Config) {
			*field(candidate) = cloneSlice(*field(&persisted))
		},
	}
}

func consoleEnvironmentOverride(
	key string,
	field func(*LogTarget) *string,
) environmentOverride {
	return environmentOverride{
		key: key,
		apply: func(cfg *Config, value string) error {
			*field(consoleTarget(cfg)) = value
			return nil
		},
		restore: func(candidate *Config, persisted Config) {
			restoreConsoleField(candidate, persisted, func(target, previous *LogTarget) {
				*field(target) = *field(previous)
			})
		},
	}
}

var environmentOverrideCatalog = []environmentOverride{
	stringEnvironmentOverride("PROCTOR_SERVER_LISTEN_ADDRESS", func(cfg *Config) *string {
		return &cfg.Server.ListenAddress
	}),
	stringEnvironmentOverride("PROCTOR_SERVER_PUBLIC_URL", func(cfg *Config) *string {
		return &cfg.Server.PublicURL
	}),
	scalarEnvironmentOverride("PROCTOR_SERVER_TLS_MODE", func(cfg *Config) *ServerTLSMode {
		return &cfg.Server.TLS.Mode
	}, func(value string) (ServerTLSMode, error) {
		return ServerTLSMode(value), nil
	}),
	stringEnvironmentOverride("PROCTOR_SERVER_TLS_CERTIFICATE_FILE", func(cfg *Config) *string {
		return &cfg.Server.TLS.CertificateFile
	}),
	stringEnvironmentOverride("PROCTOR_SERVER_TLS_PRIVATE_KEY_FILE", func(cfg *Config) *string {
		return &cfg.Server.TLS.PrivateKeyFile
	}),
	stringEnvironmentOverride("PROCTOR_SERVER_TLS_LETS_ENCRYPT_EMAIL", func(cfg *Config) *string {
		return &cfg.Server.TLS.LetsEncrypt.Email
	}),
	stringEnvironmentOverride("PROCTOR_SERVER_TLS_LETS_ENCRYPT_CACHE_DIRECTORY", func(cfg *Config) *string {
		return &cfg.Server.TLS.LetsEncrypt.CacheDirectory
	}),
	stringEnvironmentOverride("PROCTOR_SERVER_TLS_HTTP_LISTEN_ADDRESS", func(cfg *Config) *string {
		return &cfg.Server.TLS.HTTPListenAddress
	}),
	stringEnvironmentOverride("PROCTOR_AUTHENTICATION_BOOTSTRAP_SECRET", func(cfg *Config) *string {
		return &cfg.Authentication.Bootstrap.Secret
	}),
	stringEnvironmentOverride("PROCTOR_DATABASE_DATA_SOURCE", func(cfg *Config) *string {
		return &cfg.Database.DataSource
	}),
	stringEnvironmentOverride("PROCTOR_CACHE_BACKEND", func(cfg *Config) *string {
		return &cfg.Cache.Backend
	}),
	stringEnvironmentOverride("PROCTOR_CACHE_NAMESPACE", func(cfg *Config) *string {
		return &cfg.Cache.Namespace
	}),
	stringEnvironmentOverride("PROCTOR_CACHE_REDIS_USERNAME", func(cfg *Config) *string {
		return &cfg.Cache.Redis.Username
	}),
	stringEnvironmentOverride("PROCTOR_CACHE_REDIS_PASSWORD", func(cfg *Config) *string {
		return &cfg.Cache.Redis.Password
	}),
	stringListEnvironmentOverride("PROCTOR_CACHE_REDIS_ADDRESSES", func(cfg *Config) *[]string {
		return &cfg.Cache.Redis.Addresses
	}),
	stringEnvironmentOverride("PROCTOR_CLUSTER_BACKEND", func(cfg *Config) *string {
		return &cfg.Cluster.Backend
	}),
	stringEnvironmentOverride("PROCTOR_CLUSTER_NODE_ID", func(cfg *Config) *string {
		return &cfg.Cluster.NodeID
	}),
	stringEnvironmentOverride("PROCTOR_CLUSTER_MEMBERLIST_BIND_ADDRESS", func(cfg *Config) *string {
		return &cfg.Cluster.Memberlist.BindAddress
	}),
	stringEnvironmentOverride("PROCTOR_CLUSTER_MEMBERLIST_ADVERTISE_ADDRESS", func(cfg *Config) *string {
		return &cfg.Cluster.Memberlist.AdvertiseAddress
	}),
	stringEnvironmentOverride("PROCTOR_CLUSTER_MEMBERLIST_ENCRYPTION_KEY", func(cfg *Config) *string {
		return &cfg.Cluster.Memberlist.EncryptionKey
	}),
	stringListEnvironmentOverride("PROCTOR_CLUSTER_MEMBERLIST_DECRYPTION_KEYS", func(cfg *Config) *[]string {
		return &cfg.Cluster.Memberlist.DecryptionKeys
	}),
	stringListEnvironmentOverride("PROCTOR_CLUSTER_MEMBERLIST_SEED_ADDRESSES", func(cfg *Config) *[]string {
		return &cfg.Cluster.Memberlist.SeedAddresses
	}),
	stringEnvironmentOverride("PROCTOR_MAIL_BACKEND", func(cfg *Config) *string {
		return &cfg.Mail.Backend
	}),
	stringEnvironmentOverride("PROCTOR_MAIL_FROM_ADDRESS", func(cfg *Config) *string {
		return &cfg.Mail.FromAddress
	}),
	stringEnvironmentOverride("PROCTOR_MAIL_FROM_NAME", func(cfg *Config) *string {
		return &cfg.Mail.FromName
	}),
	stringEnvironmentOverride("PROCTOR_MAIL_SMTP_ADDRESS", func(cfg *Config) *string {
		return &cfg.Mail.SMTP.Address
	}),
	stringEnvironmentOverride("PROCTOR_MAIL_SMTP_SERVER_NAME", func(cfg *Config) *string {
		return &cfg.Mail.SMTP.ServerName
	}),
	stringEnvironmentOverride("PROCTOR_MAIL_SMTP_LOCAL_NAME", func(cfg *Config) *string {
		return &cfg.Mail.SMTP.LocalName
	}),
	stringEnvironmentOverride("PROCTOR_MAIL_SMTP_SECURITY", func(cfg *Config) *string {
		return &cfg.Mail.SMTP.Security
	}),
	stringEnvironmentOverride("PROCTOR_MAIL_SMTP_USERNAME", func(cfg *Config) *string {
		return &cfg.Mail.SMTP.Username
	}),
	stringEnvironmentOverride("PROCTOR_MAIL_SMTP_PASSWORD", func(cfg *Config) *string {
		return &cfg.Mail.SMTP.Password
	}),
	stringEnvironmentOverride("PROCTOR_MAIL_SECRET_SEALING_ENCRYPTION_KEY", func(cfg *Config) *string {
		return &cfg.Mail.SecretSealing.EncryptionKey
	}),
	stringListEnvironmentOverride("PROCTOR_MAIL_SECRET_SEALING_DECRYPTION_KEYS", func(cfg *Config) *[]string {
		return &cfg.Mail.SecretSealing.DecryptionKeys
	}),
	stringEnvironmentOverride("PROCTOR_MAIL_SMTP_AUTHENTICATION", func(cfg *Config) *string {
		return &cfg.Mail.SMTP.Authentication
	}),
	stringEnvironmentOverride("PROCTOR_MAIL_SMTP_MESSAGE_ID_DOMAIN", func(cfg *Config) *string {
		return &cfg.Mail.SMTP.MessageIDDomain
	}),
	stringEnvironmentOverride("PROCTOR_VFS_BACKEND", func(cfg *Config) *string {
		return &cfg.VFS.Backend
	}),
	stringEnvironmentOverride("PROCTOR_VFS_LOCAL_ROOT", func(cfg *Config) *string {
		return &cfg.VFS.Local.Root
	}),
	stringEnvironmentOverride("PROCTOR_VFS_S3_ENDPOINT", func(cfg *Config) *string {
		return &cfg.VFS.S3.Endpoint
	}),
	stringEnvironmentOverride("PROCTOR_VFS_S3_ACCESS_KEY", func(cfg *Config) *string {
		return &cfg.VFS.S3.AccessKey
	}),
	stringEnvironmentOverride("PROCTOR_VFS_S3_SECRET_KEY", func(cfg *Config) *string {
		return &cfg.VFS.S3.SecretKey
	}),
	stringEnvironmentOverride("PROCTOR_VFS_S3_SESSION_TOKEN", func(cfg *Config) *string {
		return &cfg.VFS.S3.SessionToken
	}),
	stringEnvironmentOverride("PROCTOR_VFS_S3_BUCKET", func(cfg *Config) *string {
		return &cfg.VFS.S3.Bucket
	}),
	stringEnvironmentOverride("PROCTOR_VFS_S3_PREFIX", func(cfg *Config) *string {
		return &cfg.VFS.S3.Prefix
	}),
	stringEnvironmentOverride("PROCTOR_VFS_S3_REGION", func(cfg *Config) *string {
		return &cfg.VFS.S3.Region
	}),
	stringEnvironmentOverride("PROCTOR_AUTHENTICATION_MFA_ENCRYPTION_KEY", func(cfg *Config) *string {
		return &cfg.Authentication.MFA.EncryptionKey
	}),
	stringEnvironmentOverride("PROCTOR_AUTHENTICATION_MFA_ISSUER", func(cfg *Config) *string {
		return &cfg.Authentication.MFA.Issuer
	}),
	stringListEnvironmentOverride("PROCTOR_AUTHENTICATION_MFA_DECRYPTION_KEYS", func(cfg *Config) *[]string {
		return &cfg.Authentication.MFA.DecryptionKeys
	}),
	durationEnvironmentOverride("PROCTOR_SERVER_READ_HEADER_TIMEOUT", func(cfg *Config) *Duration {
		return &cfg.Server.ReadHeaderTimeout
	}),
	durationEnvironmentOverride("PROCTOR_SERVER_READ_TIMEOUT", func(cfg *Config) *Duration {
		return &cfg.Server.ReadTimeout
	}),
	durationEnvironmentOverride("PROCTOR_SERVER_WRITE_TIMEOUT", func(cfg *Config) *Duration {
		return &cfg.Server.WriteTimeout
	}),
	durationEnvironmentOverride("PROCTOR_SERVER_IDLE_TIMEOUT", func(cfg *Config) *Duration {
		return &cfg.Server.IdleTimeout
	}),
	durationEnvironmentOverride("PROCTOR_SERVER_SHUTDOWN_TIMEOUT", func(cfg *Config) *Duration {
		return &cfg.Server.ShutdownTimeout
	}),
	durationEnvironmentOverride("PROCTOR_CACHE_REDIS_CONNECT_TIMEOUT", func(cfg *Config) *Duration {
		return &cfg.Cache.Redis.ConnectTimeout
	}),
	durationEnvironmentOverride("PROCTOR_CLUSTER_MEMBERLIST_DISCOVERY_TTL", func(cfg *Config) *Duration {
		return &cfg.Cluster.Memberlist.DiscoveryTTL
	}),
	durationEnvironmentOverride("PROCTOR_CLUSTER_MEMBERLIST_DISCOVERY_HEARTBEAT", func(cfg *Config) *Duration {
		return &cfg.Cluster.Memberlist.DiscoveryHeartbeat
	}),
	durationEnvironmentOverride("PROCTOR_MAIL_SMTP_TIMEOUT", func(cfg *Config) *Duration {
		return &cfg.Mail.SMTP.Timeout
	}),
	durationEnvironmentOverride("PROCTOR_EXECUTION_DIAL_TIMEOUT", func(cfg *Config) *Duration {
		return &cfg.Execution.DialTimeout
	}),
	durationEnvironmentOverride("PROCTOR_EXECUTION_OPERATION_TIMEOUT", func(cfg *Config) *Duration {
		return &cfg.Execution.OperationTimeout
	}),
	durationEnvironmentOverride("PROCTOR_AUTHENTICATION_SESSIONS_ACCESS_TTL", func(cfg *Config) *Duration {
		return &cfg.Authentication.Sessions.AccessTTL
	}),
	durationEnvironmentOverride("PROCTOR_AUTHENTICATION_SESSIONS_REFRESH_TTL", func(cfg *Config) *Duration {
		return &cfg.Authentication.Sessions.RefreshTTL
	}),
	durationEnvironmentOverride("PROCTOR_AUTHENTICATION_SESSIONS_IDLE_TTL", func(cfg *Config) *Duration {
		return &cfg.Authentication.Sessions.IdleTTL
	}),
	durationEnvironmentOverride("PROCTOR_AUTHENTICATION_SESSIONS_ABSOLUTE_TTL", func(cfg *Config) *Duration {
		return &cfg.Authentication.Sessions.AbsoluteTTL
	}),
	durationEnvironmentOverride("PROCTOR_AUTHENTICATION_SESSIONS_ACTIVITY_UPDATE_INTERVAL", func(cfg *Config) *Duration {
		return &cfg.Authentication.Sessions.ActivityUpdateInterval
	}),
	durationEnvironmentOverride("PROCTOR_AUTHENTICATION_LOGIN_RATE_LIMIT_WINDOW", func(cfg *Config) *Duration {
		return &cfg.Authentication.LoginRateLimit.Window
	}),
	durationEnvironmentOverride("PROCTOR_AUTHENTICATION_RECENT_AUTHENTICATION_TTL", func(cfg *Config) *Duration {
		return &cfg.Authentication.RecentAuthenticationTTL
	}),
	durationEnvironmentOverride("PROCTOR_AUTHENTICATION_ACCOUNT_RECOVERY_EMAIL_VERIFICATION_TTL", func(cfg *Config) *Duration {
		return &cfg.Authentication.AccountRecovery.EmailVerificationTTL
	}),
	durationEnvironmentOverride("PROCTOR_AUTHENTICATION_ACCOUNT_RECOVERY_PASSWORD_RESET_TTL", func(cfg *Config) *Duration {
		return &cfg.Authentication.AccountRecovery.PasswordResetTTL
	}),
	durationEnvironmentOverride("PROCTOR_AUTHENTICATION_ACCOUNT_RECOVERY_RATE_LIMIT_WINDOW", func(cfg *Config) *Duration {
		return &cfg.Authentication.AccountRecovery.RateLimit.Window
	}),
	durationEnvironmentOverride("PROCTOR_AUTHENTICATION_PERSONAL_ACCESS_TOKENS_MINIMUM_LIFETIME", func(cfg *Config) *Duration {
		return &cfg.Authentication.PersonalAccessTokens.MinimumLifetime
	}),
	durationEnvironmentOverride("PROCTOR_AUTHENTICATION_PERSONAL_ACCESS_TOKENS_MAXIMUM_LIFETIME", func(cfg *Config) *Duration {
		return &cfg.Authentication.PersonalAccessTokens.MaximumLifetime
	}),
	durationEnvironmentOverride("PROCTOR_AUTHENTICATION_PERSONAL_ACCESS_TOKENS_LAST_USED_UPDATE_INTERVAL", func(cfg *Config) *Duration {
		return &cfg.Authentication.PersonalAccessTokens.LastUsedUpdateInterval
	}),
	durationEnvironmentOverride("PROCTOR_AUTHENTICATION_MFA_SETUP_TTL", func(cfg *Config) *Duration {
		return &cfg.Authentication.MFA.SetupTTL
	}),
	intEnvironmentOverride("PROCTOR_SERVER_MAX_HEADER_BYTES", func(cfg *Config) *int {
		return &cfg.Server.MaxHeaderBytes
	}),
	int64EnvironmentOverride("PROCTOR_SERVER_MAX_BODY_BYTES", func(cfg *Config) *int64 {
		return &cfg.Server.MaxBodyBytes
	}),
	intEnvironmentOverride("PROCTOR_LOG_MAX_FIELD_BYTES", func(cfg *Config) *int {
		return &cfg.Log.MaxFieldBytes
	}),
	intEnvironmentOverride("PROCTOR_CACHE_MEMORY_MAX_ENTRIES", func(cfg *Config) *int {
		return &cfg.Cache.Memory.MaxEntries
	}),
	int64EnvironmentOverride("PROCTOR_CACHE_MEMORY_MAX_BYTES", func(cfg *Config) *int64 {
		return &cfg.Cache.Memory.MaxBytes
	}),
	intEnvironmentOverride("PROCTOR_CACHE_REDIS_DATABASE", func(cfg *Config) *int {
		return &cfg.Cache.Redis.Database
	}),
	intEnvironmentOverride("PROCTOR_MAIL_SMTP_MAX_RECIPIENTS", func(cfg *Config) *int {
		return &cfg.Mail.SMTP.MaxRecipients
	}),
	intEnvironmentOverride("PROCTOR_AUTHENTICATION_PASSWORD_MINIMUM_LENGTH", func(cfg *Config) *int {
		return &cfg.Authentication.Password.MinimumLength
	}),
	intEnvironmentOverride("PROCTOR_AUTHENTICATION_PASSWORD_MAXIMUM_LENGTH", func(cfg *Config) *int {
		return &cfg.Authentication.Password.MaximumLength
	}),
	intEnvironmentOverride("PROCTOR_AUTHENTICATION_PASSWORD_ARGON_MEMORY_KIB", func(cfg *Config) *int {
		return &cfg.Authentication.Password.ArgonMemoryKiB
	}),
	intEnvironmentOverride("PROCTOR_AUTHENTICATION_PASSWORD_ARGON_ITERATIONS", func(cfg *Config) *int {
		return &cfg.Authentication.Password.ArgonIterations
	}),
	intEnvironmentOverride("PROCTOR_AUTHENTICATION_PASSWORD_ARGON_PARALLELISM", func(cfg *Config) *int {
		return &cfg.Authentication.Password.ArgonParallelism
	}),
	intEnvironmentOverride("PROCTOR_AUTHENTICATION_PASSWORD_ARGON_SALT_BYTES", func(cfg *Config) *int {
		return &cfg.Authentication.Password.ArgonSaltBytes
	}),
	intEnvironmentOverride("PROCTOR_AUTHENTICATION_PASSWORD_ARGON_KEY_BYTES", func(cfg *Config) *int {
		return &cfg.Authentication.Password.ArgonKeyBytes
	}),
	intEnvironmentOverride("PROCTOR_AUTHENTICATION_SESSIONS_MAXIMUM_PER_USER", func(cfg *Config) *int {
		return &cfg.Authentication.Sessions.MaximumPerUser
	}),
	intEnvironmentOverride("PROCTOR_AUTHENTICATION_LOGIN_RATE_LIMIT_MAXIMUM_ATTEMPTS", func(cfg *Config) *int {
		return &cfg.Authentication.LoginRateLimit.MaximumAttempts
	}),
	intEnvironmentOverride("PROCTOR_AUTHENTICATION_LOGIN_RATE_LIMIT_MAXIMUM_SOURCE_ATTEMPTS", func(cfg *Config) *int {
		return &cfg.Authentication.LoginRateLimit.MaximumSourceAttempts
	}),
	intEnvironmentOverride("PROCTOR_AUTHENTICATION_ACCOUNT_RECOVERY_RATE_LIMIT_MAXIMUM_ATTEMPTS", func(cfg *Config) *int {
		return &cfg.Authentication.AccountRecovery.RateLimit.MaximumAttempts
	}),
	intEnvironmentOverride("PROCTOR_AUTHENTICATION_ACCOUNT_RECOVERY_RATE_LIMIT_MAXIMUM_SOURCE_ATTEMPTS", func(cfg *Config) *int {
		return &cfg.Authentication.AccountRecovery.RateLimit.MaximumSourceAttempts
	}),
	intEnvironmentOverride("PROCTOR_AUTHENTICATION_PERSONAL_ACCESS_TOKENS_MAXIMUM_PER_USER", func(cfg *Config) *int {
		return &cfg.Authentication.PersonalAccessTokens.MaximumPerUser
	}),
	intEnvironmentOverride("PROCTOR_AUTHENTICATION_MFA_RECOVERY_CODE_COUNT", func(cfg *Config) *int {
		return &cfg.Authentication.MFA.RecoveryCodeCount
	}),
	int64EnvironmentOverride("PROCTOR_MAIL_SMTP_MAX_MESSAGE_BYTES", func(cfg *Config) *int64 {
		return &cfg.Mail.SMTP.MaxMessageBytes
	}),
	boolEnvironmentOverride("PROCTOR_CACHE_REDIS_TLS", func(cfg *Config) *bool {
		return &cfg.Cache.Redis.TLS
	}),
	boolEnvironmentOverride("PROCTOR_CLUSTER_MEMBERLIST_ALLOW_PUBLIC_BIND", func(cfg *Config) *bool {
		return &cfg.Cluster.Memberlist.AllowPublicBind
	}),
	boolEnvironmentOverride("PROCTOR_MAIL_ENABLED", func(cfg *Config) *bool {
		return &cfg.Mail.Enabled
	}),
	boolEnvironmentOverride("PROCTOR_EXECUTION_ENABLED", func(cfg *Config) *bool {
		return &cfg.Execution.Enabled
	}),
	boolEnvironmentOverride("PROCTOR_VFS_S3_SECURE", func(cfg *Config) *bool {
		return &cfg.VFS.S3.Secure
	}),
	boolEnvironmentOverride("PROCTOR_AUTHENTICATION_MFA_ENABLED", func(cfg *Config) *bool {
		return &cfg.Authentication.MFA.Enabled
	}),
	boolEnvironmentOverride("PROCTOR_AUTHENTICATION_BOOTSTRAP_DEVELOPMENT_MODE", func(cfg *Config) *bool {
		return &cfg.Authentication.Bootstrap.DevelopmentMode
	}),
	boolEnvironmentOverride("PROCTOR_SERVER_TLS_FORWARD_HTTP_TO_HTTPS", func(cfg *Config) *bool {
		return &cfg.Server.TLS.ForwardHTTPToHTTPS
	}),
	intEnvironmentOverride("PROCTOR_DATABASE_MAX_OPEN_CONNECTIONS", func(cfg *Config) *int {
		return &cfg.Database.MaxOpenConnections
	}),
	intEnvironmentOverride("PROCTOR_DATABASE_MAX_IDLE_CONNECTIONS", func(cfg *Config) *int {
		return &cfg.Database.MaxIdleConnections
	}),
	durationEnvironmentOverride("PROCTOR_DATABASE_CONNECTION_MAX_LIFETIME", func(cfg *Config) *Duration {
		return &cfg.Database.ConnectionMaxLifetime
	}),
	durationEnvironmentOverride("PROCTOR_DATABASE_CONNECTION_MAX_IDLE_TIME", func(cfg *Config) *Duration {
		return &cfg.Database.ConnectionMaxIdleTime
	}),
	durationEnvironmentOverride("PROCTOR_DATABASE_QUERY_TIMEOUT", func(cfg *Config) *Duration {
		return &cfg.Database.QueryTimeout
	}),
	durationEnvironmentOverride("PROCTOR_DATABASE_MIGRATION_TIMEOUT", func(cfg *Config) *Duration {
		return &cfg.Database.MigrationTimeout
	}),
	consoleEnvironmentOverride("PROCTOR_LOG_LEVEL", func(target *LogTarget) *string {
		return &target.Level
	}),
	consoleEnvironmentOverride("PROCTOR_LOG_FORMAT", func(target *LogTarget) *string {
		return &target.Format
	}),
}

func applyEnvironment(cfg *Config, lookup LookupEnv) (appliedEnvironment, error) {
	return applyEnvironmentCatalog(cfg, lookup, environmentOverrideCatalog)
}

func applyEnvironmentCatalog(
	cfg *Config,
	lookup LookupEnv,
	catalog []environmentOverride,
) (appliedEnvironment, error) {
	seen := make(map[string]struct{}, len(catalog))
	for _, definition := range catalog {
		if _, exists := seen[definition.key]; exists {
			return appliedEnvironment{}, fmt.Errorf(
				"environment override %q is duplicated",
				definition.key,
			)
		}
		seen[definition.key] = struct{}{}
	}

	candidate := cfg.Clone()
	applied := make([]environmentOverride, 0, len(catalog))
	for _, definition := range catalog {
		value, ok := lookup(definition.key)
		if !ok {
			continue
		}
		if err := definition.apply(&candidate, value); err != nil {
			return appliedEnvironment{}, err
		}
		applied = append(applied, definition)
	}
	*cfg = candidate
	return appliedEnvironment{definitions: applied}, nil
}
