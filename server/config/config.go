// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	netmail "net/mail"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const SchemaVersion = 1

type Duration struct {
	time.Duration
}

func (d Duration) MarshalJSON() ([]byte, error) {
	return json.Marshal(d.String())
}

func (d *Duration) UnmarshalJSON(data []byte) error {
	var value string
	if err := json.Unmarshal(data, &value); err != nil {
		return errors.New("duration must be a string such as \"15s\"")
	}
	parsed, err := time.ParseDuration(value)
	if err != nil {
		return fmt.Errorf("parse duration %q: %w", value, err)
	}
	d.Duration = parsed
	return nil
}

type Config struct {
	Version        int            `json:"Version"`
	Server         Server         `json:"Server"`
	Metrics        Metrics        `json:"Metrics"`
	Database       Database       `json:"Database"`
	Cache          Cache          `json:"Cache"`
	Cluster        Cluster        `json:"Cluster"`
	Mail           Mail           `json:"Mail"`
	VFS            VFS            `json:"VFS"`
	FileContent    FileContent    `json:"FileContent"`
	Authentication Authentication `json:"Authentication"`
	Localization   Localization   `json:"Localization"`
	Log            Log            `json:"Log"`
}

func Default() Config {
	return Config{
		Version: SchemaVersion,
		Server: Server{
			ListenAddress:   "127.0.0.1:8065",
			PublicURL:       "http://localhost:8065",
			WebappDirectory: "./webapp/dist",
			TLS: ServerTLS{
				Mode:                          ServerTLSModeDisabled,
				HTTPListenAddress:             "127.0.0.1:8080",
				StrictTransportSecurityMaxAge: Duration{Duration: 365 * 24 * time.Hour},
				LetsEncrypt: ServerLetsEncrypt{
					CacheDirectory: "./data/acme",
				},
			},
			ReadHeaderTimeout: Duration{Duration: 10 * time.Second},
			ReadTimeout:       Duration{Duration: 30 * time.Second},
			WriteTimeout:      Duration{Duration: 30 * time.Second},
			IdleTimeout:       Duration{Duration: 2 * time.Minute},
			ShutdownTimeout:   Duration{Duration: 15 * time.Second},
			MaxHeaderBytes:    1 << 20,
			MaxBodyBytes:      1 << 20,
			TrustedProxies:    []string{},
		},
		Metrics: Metrics{
			ListenAddress:            "127.0.0.1:8067",
			ReadHeaderTimeout:        Duration{Duration: 5 * time.Second},
			ReadTimeout:              Duration{Duration: 10 * time.Second},
			WriteTimeout:             Duration{Duration: 30 * time.Second},
			IdleTimeout:              Duration{Duration: time.Minute},
			ShutdownTimeout:          Duration{Duration: 5 * time.Second},
			MaximumConcurrentScrapes: 2,
		},
		Database: Database{
			DataSource:            "postgres://proctor:proctor@127.0.0.1:15432/proctor?sslmode=disable",
			MaxOpenConnections:    50,
			MaxIdleConnections:    10,
			ConnectionMaxLifetime: Duration{Duration: time.Hour},
			ConnectionMaxIdleTime: Duration{Duration: 5 * time.Minute},
			QueryTimeout:          Duration{Duration: 10 * time.Second},
			MigrationTimeout:      Duration{Duration: 60 * time.Second},
		},
		Cache: Cache{
			Backend:   "memory",
			Namespace: "proctor",
			Memory: CacheMemory{
				MaxEntries: 100_000,
				MaxBytes:   64 << 20,
			},
			Redis: CacheRedis{
				Addresses:      []string{"127.0.0.1:6379"},
				ConnectTimeout: Duration{Duration: 5 * time.Second},
			},
		},
		Cluster: Cluster{
			Backend: "local",
			NodeID:  "local",
			Memberlist: ClusterMemberlist{
				BindAddress:        "127.0.0.1:7946",
				AdvertiseAddress:   "127.0.0.1:7946",
				DecryptionKeys:     []string{},
				SeedAddresses:      []string{},
				DiscoveryTTL:       Duration{Duration: 30 * time.Second},
				DiscoveryHeartbeat: Duration{Duration: 10 * time.Second},
			},
		},
		Mail: Mail{
			Enabled:     false,
			Backend:     "smtp",
			FromAddress: "no-reply@localhost",
			FromName:    "Proctor",
			SMTP: MailSMTP{
				Address:         "127.0.0.1:1025",
				Security:        "none",
				Authentication:  "none",
				Timeout:         Duration{Duration: 10 * time.Second},
				MessageIDDomain: "localhost",
				MaxMessageBytes: 25 << 20,
				MaxRecipients:   100,
			},
			SecretSealing: SecretSealing{DecryptionKeys: []string{}},
		},
		VFS: VFS{
			Backend: "local",
			Local:   VFSLocal{Root: "./data"},
			S3:      VFSS3{Secure: true},
		},
		FileContent: FileContent{MaximumConcurrentOperations: 2},
		Authentication: Authentication{
			Bootstrap: Bootstrap{DevelopmentMode: true},
			Password: Password{
				MaximumConcurrentOperations: 2,
				MinimumLength:               12,
				MaximumLength:               128,
				ArgonMemoryKiB:              64 * 1024,
				ArgonIterations:             3,
				ArgonParallelism:            2,
				ArgonSaltBytes:              16,
				ArgonKeyBytes:               32,
			},
			Sessions: Sessions{
				AccessTTL:              Duration{Duration: 15 * time.Minute},
				RefreshTTL:             Duration{Duration: 30 * 24 * time.Hour},
				IdleTTL:                Duration{Duration: 7 * 24 * time.Hour},
				AbsoluteTTL:            Duration{Duration: 30 * 24 * time.Hour},
				ActivityUpdateInterval: Duration{Duration: 5 * time.Minute},
				MaximumPerUser:         10,
				RetiredRetention:       Duration{Duration: 30 * 24 * time.Hour},
			},
			RecentAuthenticationTTL: Duration{Duration: 15 * time.Minute},
			LoginRateLimit: LoginRateLimit{
				Window:                     Duration{Duration: time.Minute},
				MaximumAttempts:            10,
				MaximumSourceAttempts:      1000,
				AccountFailuresBeforeDelay: 5,
				MaximumAccountFailureDelay: Duration{Duration: 4 * time.Second},
			},
			AccountRecovery: AccountRecovery{
				EmailVerificationTTL: Duration{Duration: 24 * time.Hour},
				PasswordResetTTL:     Duration{Duration: time.Hour},
				RateLimit: AttemptRateLimit{
					Window:                Duration{Duration: 15 * time.Minute},
					MaximumAttempts:       3,
					MaximumSourceAttempts: 100,
				},
			},
			PersonalAccessTokens: PersonalAccessTokens{
				MinimumLifetime:        Duration{Duration: time.Hour},
				MaximumLifetime:        Duration{Duration: 90 * 24 * time.Hour},
				LastUsedUpdateInterval: Duration{Duration: 5 * time.Minute},
				MaximumPerUser:         50,
			},
			MFA: MFA{
				Enabled:           false,
				Issuer:            "Proctor",
				DecryptionKeys:    []string{},
				SetupTTL:          Duration{Duration: 10 * time.Minute},
				RecoveryCodeCount: 10,
			},
			External: ExternalAuthentication{
				LoginStateTTL: Duration{Duration: 10 * time.Minute},
				Providers:     []ExternalAuthenticationProvider{},
			},
		},
		Localization: Localization{DefaultLocale: "en"},
		Log: Log{
			MaxFieldBytes:   16 << 10,
			QueueSize:       1024,
			EnqueueTimeout:  Duration{Duration: 250 * time.Millisecond},
			FlushTimeout:    Duration{Duration: 5 * time.Second},
			ShutdownTimeout: Duration{Duration: 10 * time.Second},
			Targets: []LogTarget{{
				Name:      "console",
				Type:      "console",
				Level:     "info",
				Format:    "text",
				QueueSize: 256,
			}},
		},
	}
}

func (c Config) Clone() Config {
	cloned := c
	cloned.Log.Targets = cloneSlice(c.Log.Targets)
	cloned.Server.TrustedProxies = cloneSlice(c.Server.TrustedProxies)
	cloned.Cache.Redis.Addresses = cloneSlice(c.Cache.Redis.Addresses)
	cloned.Cluster.Memberlist.SeedAddresses = cloneSlice(c.Cluster.Memberlist.SeedAddresses)
	cloned.Cluster.Memberlist.DecryptionKeys = cloneSlice(c.Cluster.Memberlist.DecryptionKeys)
	cloned.Mail.SecretSealing.DecryptionKeys = cloneSlice(c.Mail.SecretSealing.DecryptionKeys)
	cloned.Authentication.MFA.DecryptionKeys = cloneSlice(c.Authentication.MFA.DecryptionKeys)
	if c.Authentication.External.Providers != nil {
		cloned.Authentication.External.Providers = append(
			make([]ExternalAuthenticationProvider, 0, len(c.Authentication.External.Providers)),
			c.Authentication.External.Providers...,
		)
	}
	for index := range cloned.Authentication.External.Providers {
		provider := &cloned.Authentication.External.Providers[index]
		sourceProvider := c.Authentication.External.Providers[index]
		source := sourceProvider.Claims
		if source.AllowedHomeOrganizations != nil {
			provider.Claims.AllowedHomeOrganizations = append(
				make([]string, 0, len(source.AllowedHomeOrganizations)),
				source.AllowedHomeOrganizations...,
			)
		}
		if source.MultiFactorValues != nil {
			provider.Claims.MultiFactorValues = append(
				make([]string, 0, len(source.MultiFactorValues)),
				source.MultiFactorValues...,
			)
		}
		if sourceProvider.CAS != nil {
			cas := *sourceProvider.CAS
			provider.CAS = &cas
		}
		if sourceProvider.OIDC != nil {
			oidc := *sourceProvider.OIDC
			oidc.Scopes = cloneSlice(sourceProvider.OIDC.Scopes)
			provider.OIDC = &oidc
		}
	}
	return cloned
}

func cloneSlice[T any](values []T) []T {
	if values == nil {
		return nil
	}
	return append(make([]T, 0, len(values)), values...)
}

// Redacted returns a safe copy for display.
func (c Config) Redacted() Config {
	redacted := c.Clone()
	if redacted.Database.DataSource != "" {
		redacted.Database.DataSource = "[redacted]"
	}
	redacted.Cache.Redis.Password = redactSecret(redacted.Cache.Redis.Password)
	redacted.Metrics.BearerToken = redactSecret(redacted.Metrics.BearerToken)
	redacted.Cluster.Memberlist.EncryptionKey = redactSecret(redacted.Cluster.Memberlist.EncryptionKey)
	for index := range redacted.Cluster.Memberlist.DecryptionKeys {
		redacted.Cluster.Memberlist.DecryptionKeys[index] = redactSecret(
			redacted.Cluster.Memberlist.DecryptionKeys[index],
		)
	}
	redacted.Mail.SMTP.Password = redactSecret(redacted.Mail.SMTP.Password)
	redacted.Mail.SecretSealing.EncryptionKey = redactSecret(
		redacted.Mail.SecretSealing.EncryptionKey,
	)
	for index := range redacted.Mail.SecretSealing.DecryptionKeys {
		redacted.Mail.SecretSealing.DecryptionKeys[index] = redactSecret(
			redacted.Mail.SecretSealing.DecryptionKeys[index],
		)
	}
	redacted.VFS.S3.AccessKey = redactSecret(redacted.VFS.S3.AccessKey)
	redacted.VFS.S3.SecretKey = redactSecret(redacted.VFS.S3.SecretKey)
	redacted.VFS.S3.SessionToken = redactSecret(redacted.VFS.S3.SessionToken)
	redacted.Authentication.MFA.EncryptionKey = redactSecret(
		redacted.Authentication.MFA.EncryptionKey,
	)
	for index := range redacted.Authentication.MFA.DecryptionKeys {
		redacted.Authentication.MFA.DecryptionKeys[index] = redactSecret(
			redacted.Authentication.MFA.DecryptionKeys[index],
		)
	}
	redacted.Authentication.Bootstrap.Secret = redactSecret(
		redacted.Authentication.Bootstrap.Secret,
	)
	for index := range redacted.Authentication.External.Providers {
		provider := &redacted.Authentication.External.Providers[index]
		if provider.OIDC != nil {
			provider.OIDC.ClientSecret = redactSecret(
				provider.OIDC.ClientSecret,
			)
		}
	}
	return redacted
}

func redactSecret(value string) string {
	if value == "" {
		return ""
	}
	return "[redacted]"
}

func (c Config) RedactedJSON() ([]byte, error) {
	return json.MarshalIndent(c.Redacted(), "", "  ")
}

type FieldError struct {
	Field   string
	Message string
}

type ValidationError struct {
	Fields []FieldError
}

func (e *ValidationError) Error() string {
	var builder strings.Builder
	builder.WriteString("configuration is invalid")
	for _, field := range e.Fields {
		builder.WriteString("; ")
		builder.WriteString(field.Field)
		builder.WriteString(": ")
		builder.WriteString(field.Message)
	}
	return builder.String()
}

func (c Config) Validate() error {
	var fields []FieldError
	add := func(field, message string) {
		fields = append(fields, FieldError{Field: field, Message: message})
	}

	if c.Version != SchemaVersion {
		add("version", fmt.Sprintf("must be %d", SchemaVersion))
	}
	validateListenAddress(c.Server.ListenAddress, add)
	validatePublicURL(c.Server.PublicURL, add)
	if strings.TrimSpace(c.Server.WebappDirectory) == "" || strings.ContainsRune(c.Server.WebappDirectory, '\x00') {
		add("server.webapp_directory", "must be a non-empty path without null bytes")
	}
	validateServerTLS(c.Server, c.Cluster, add)
	validateMetrics(c.Metrics, c.Server, add)
	validateServerLimits(c.Server, add)

	validateDatabase(c.Database, add)
	validateCache(c.Cache, add)
	validateCluster(c.Cluster, add)
	validateMail(c.Mail, add)
	validateVFS(c.VFS, add)
	if c.FileContent.MaximumConcurrentOperations < 1 {
		add("file_content.maximum_concurrent_operations", "must be greater than zero")
	}
	if c.Cluster.Backend == "memberlist" {
		if c.Cache.Backend != "redis" {
			add("cache.backend", "must be redis when cluster.backend is memberlist")
		}
		if c.VFS.Backend == "local" {
			add("vfs.backend", "must be shared when cluster.backend is multi-node")
		}
	}
	validateAuthentication(c.Authentication, add)
	validateBootstrap(c.Server, c.Authentication.Bootstrap, add)
	validateSecretKeySeparation(c, add)
	if !validLocaleIdentifier(c.Localization.DefaultLocale) {
		add("localization.default_locale", "must be a valid locale identifier")
	}

	validateLog(c.Log, add)

	if len(fields) != 0 {
		return &ValidationError{Fields: fields}
	}
	return nil
}

func validHostPort(address string) bool {
	host, portText, err := net.SplitHostPort(address)
	if err != nil || host == "" {
		return false
	}
	port, err := strconv.Atoi(portText)
	return err == nil && port >= 1 && port <= 65535
}

func validMailbox(value string) bool {
	address, err := netmail.ParseAddress(value)
	return err == nil && address.Name == "" && address.Address == value
}

func validateListenAddress(address string, add func(string, string)) {
	validateTCPListenAddress("server.listen_address", address, true, add)
}

func validateTCPListenAddress(
	field string,
	address string,
	allowEphemeralPort bool,
	add func(string, string),
) {
	host, portText, err := net.SplitHostPort(address)
	if err != nil {
		add(field, "must be a host:port TCP address")
		return
	}
	if strings.ContainsAny(host, "\r\n") {
		add(field, "host contains invalid characters")
	}
	port, err := strconv.Atoi(portText)
	minimumPort := 1
	if allowEphemeralPort {
		minimumPort = 0
	}
	if err != nil || port < minimumPort || port > 65535 {
		add(field, fmt.Sprintf("port must be between %d and 65535", minimumPort))
	}
}

func isLiteralLoopbackListenAddress(address string) bool {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return false
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func isLoopbackPublicURL(raw string) bool {
	parsed, err := url.Parse(raw)
	if err != nil {
		return false
	}
	host := parsed.Hostname()
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
