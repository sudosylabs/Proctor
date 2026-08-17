// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package config

import (
	"encoding/base64"
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

type Server struct {
	ListenAddress     string   `json:"listen_address"`
	PublicURL         string   `json:"public_url"`
	ReadHeaderTimeout Duration `json:"read_header_timeout"`
	ReadTimeout       Duration `json:"read_timeout"`
	WriteTimeout      Duration `json:"write_timeout"`
	IdleTimeout       Duration `json:"idle_timeout"`
	ShutdownTimeout   Duration `json:"shutdown_timeout"`
	MaxHeaderBytes    int      `json:"max_header_bytes"`
	MaxBodyBytes      int64    `json:"max_body_bytes"`
}

type LogTarget struct {
	Name   string `json:"name"`
	Type   string `json:"type"`
	Level  string `json:"level"`
	Format string `json:"format"`
	File   string `json:"file,omitempty"`
}

type Log struct {
	MaxFieldBytes int         `json:"max_field_bytes"`
	Targets       []LogTarget `json:"targets"`
}

type Database struct {
	DataSource            string   `json:"data_source"`
	MaxOpenConnections    int      `json:"max_open_connections"`
	MaxIdleConnections    int      `json:"max_idle_connections"`
	ConnectionMaxLifetime Duration `json:"connection_max_lifetime"`
	ConnectionMaxIdleTime Duration `json:"connection_max_idle_time"`
	QueryTimeout          Duration `json:"query_timeout"`
	MigrationTimeout      Duration `json:"migration_timeout"`
}

type CacheRedis struct {
	Addresses      []string `json:"addresses"`
	Username       string   `json:"username,omitempty"`
	Password       string   `json:"password,omitempty"`
	Database       int      `json:"database"`
	TLS            bool     `json:"tls"`
	ConnectTimeout Duration `json:"connect_timeout"`
}

type Cache struct {
	Backend   string     `json:"backend"`
	Namespace string     `json:"namespace"`
	Redis     CacheRedis `json:"redis"`
}

// ClusterMemberlist configures the built-in multi-node gossip transport.
type ClusterMemberlist struct {
	BindAddress        string   `json:"bind_address"`
	AdvertiseAddress   string   `json:"advertise_address"`
	EncryptionKey      string   `json:"encryption_key,omitempty"`
	SeedAddresses      []string `json:"seed_addresses,omitempty"`
	DiscoveryTTL       Duration `json:"discovery_ttl"`
	DiscoveryHeartbeat Duration `json:"discovery_heartbeat"`
	ProtocolMin        int      `json:"protocol_min"`
	ProtocolMax        int      `json:"protocol_max"`
	AllowPublicBind    bool     `json:"allow_public_bind"`
}

// Cluster selects the inter-node transport and gives this process its stable
// runtime identity. "local" is the single-node degenerate transport.
// "memberlist" is the built-in multi-node backend and requires no Redis service.
type Cluster struct {
	Backend    string            `json:"backend"`
	NodeID     string            `json:"node_id"`
	Memberlist ClusterMemberlist `json:"memberlist"`
}

type MailSMTP struct {
	Address         string   `json:"address"`
	ServerName      string   `json:"server_name,omitempty"`
	LocalName       string   `json:"local_name,omitempty"`
	Security        string   `json:"security"`
	Username        string   `json:"username,omitempty"`
	Password        string   `json:"password,omitempty"`
	Authentication  string   `json:"authentication"`
	Timeout         Duration `json:"timeout"`
	MessageIDDomain string   `json:"message_id_domain"`
	MaxMessageBytes int64    `json:"max_message_bytes"`
	MaxRecipients   int      `json:"max_recipients"`
}

// SecretSealing configures a primary AES-256 encryption key and the bounded
// fallback ring used to read values written before rotation.
type SecretSealing struct {
	EncryptionKey  string   `json:"encryption_key,omitempty"`
	DecryptionKeys []string `json:"decryption_keys,omitempty"`
}

type Mail struct {
	Enabled       bool          `json:"enabled"`
	Backend       string        `json:"backend"`
	FromAddress   string        `json:"from_address"`
	FromName      string        `json:"from_name,omitempty"`
	SMTP          MailSMTP      `json:"smtp"`
	SecretSealing SecretSealing `json:"secret_sealing"`
}

type VFSLocal struct {
	Root string `json:"root"`
}

type VFSS3 struct {
	Endpoint     string `json:"endpoint"`
	AccessKey    string `json:"access_key,omitempty"`
	SecretKey    string `json:"secret_key,omitempty"`
	SessionToken string `json:"session_token,omitempty"`
	Bucket       string `json:"bucket"`
	Prefix       string `json:"prefix,omitempty"`
	Region       string `json:"region,omitempty"`
	Secure       bool   `json:"secure"`
}

type VFS struct {
	Backend string   `json:"backend"`
	Local   VFSLocal `json:"local"`
	S3      VFSS3    `json:"s3"`
}

type Password struct {
	MinimumLength    int `json:"minimum_length"`
	MaximumLength    int `json:"maximum_length"`
	ArgonMemoryKiB   int `json:"argon_memory_kib"`
	ArgonIterations  int `json:"argon_iterations"`
	ArgonParallelism int `json:"argon_parallelism"`
	ArgonSaltBytes   int `json:"argon_salt_bytes"`
	ArgonKeyBytes    int `json:"argon_key_bytes"`
}

type Sessions struct {
	AccessTTL              Duration `json:"access_ttl"`
	RefreshTTL             Duration `json:"refresh_ttl"`
	IdleTTL                Duration `json:"idle_ttl"`
	AbsoluteTTL            Duration `json:"absolute_ttl"`
	ActivityUpdateInterval Duration `json:"activity_update_interval"`
	MaximumPerUser         int      `json:"maximum_per_user"`
}

type LoginRateLimit struct {
	Window                Duration `json:"window"`
	MaximumAttempts       int      `json:"maximum_attempts"`
	MaximumSourceAttempts int      `json:"maximum_source_attempts"`
}

type AccountRecovery struct {
	EmailVerificationTTL Duration       `json:"email_verification_ttl"`
	PasswordResetTTL     Duration       `json:"password_reset_ttl"`
	RateLimit            LoginRateLimit `json:"rate_limit"`
}

type PersonalAccessTokens struct {
	MinimumLifetime        Duration `json:"minimum_lifetime"`
	MaximumLifetime        Duration `json:"maximum_lifetime"`
	LastUsedUpdateInterval Duration `json:"last_used_update_interval"`
	MaximumPerUser         int      `json:"maximum_per_user"`
}

// MFA contains operator-owned cryptographic and policy settings. The primary
// key encrypts new TOTP secrets; decryption_keys permits online key rotation
// while existing credentials are re-encrypted.
type MFA struct {
	Enabled           bool     `json:"enabled"`
	Issuer            string   `json:"issuer"`
	EncryptionKey     string   `json:"encryption_key,omitempty"`
	DecryptionKeys    []string `json:"decryption_keys,omitempty"`
	SetupTTL          Duration `json:"setup_ttl"`
	RecoveryCodeCount int      `json:"recovery_code_count"`
}

type Authentication struct {
	Password                Password               `json:"password"`
	Sessions                Sessions               `json:"sessions"`
	RecentAuthenticationTTL Duration               `json:"recent_authentication_ttl"`
	LoginRateLimit          LoginRateLimit         `json:"login_rate_limit"`
	AccountRecovery         AccountRecovery        `json:"account_recovery"`
	PersonalAccessTokens    PersonalAccessTokens   `json:"personal_access_tokens"`
	MFA                     MFA                    `json:"mfa"`
	External                ExternalAuthentication `json:"external"`
}

type Config struct {
	Version        int            `json:"version"`
	Server         Server         `json:"server"`
	Database       Database       `json:"database"`
	Cache          Cache          `json:"cache"`
	Cluster        Cluster        `json:"cluster"`
	Mail           Mail           `json:"mail"`
	VFS            VFS            `json:"vfs"`
	Authentication Authentication `json:"authentication"`
	Log            Log            `json:"log"`
}

func Default() Config {
	return Config{
		Version: SchemaVersion,
		Server: Server{
			ListenAddress:     "127.0.0.1:8065",
			PublicURL:         "http://localhost:8065",
			ReadHeaderTimeout: Duration{Duration: 10 * time.Second},
			ReadTimeout:       Duration{Duration: 30 * time.Second},
			WriteTimeout:      Duration{Duration: 30 * time.Second},
			IdleTimeout:       Duration{Duration: 2 * time.Minute},
			ShutdownTimeout:   Duration{Duration: 15 * time.Second},
			MaxHeaderBytes:    1 << 20,
			MaxBodyBytes:      1 << 20,
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
				DiscoveryTTL:       Duration{Duration: 30 * time.Second},
				DiscoveryHeartbeat: Duration{Duration: 10 * time.Second},
				ProtocolMin:        1,
				ProtocolMax:        1,
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
		},
		VFS: VFS{
			Backend: "local",
			Local:   VFSLocal{Root: "./data"},
			S3:      VFSS3{Secure: true},
		},
		Authentication: Authentication{
			Password: Password{
				MinimumLength:    12,
				MaximumLength:    128,
				ArgonMemoryKiB:   64 * 1024,
				ArgonIterations:  3,
				ArgonParallelism: 2,
				ArgonSaltBytes:   16,
				ArgonKeyBytes:    32,
			},
			Sessions: Sessions{
				AccessTTL:              Duration{Duration: 15 * time.Minute},
				RefreshTTL:             Duration{Duration: 30 * 24 * time.Hour},
				IdleTTL:                Duration{Duration: 7 * 24 * time.Hour},
				AbsoluteTTL:            Duration{Duration: 30 * 24 * time.Hour},
				ActivityUpdateInterval: Duration{Duration: 5 * time.Minute},
				MaximumPerUser:         10,
			},
			RecentAuthenticationTTL: Duration{Duration: 15 * time.Minute},
			LoginRateLimit: LoginRateLimit{
				Window:                Duration{Duration: time.Minute},
				MaximumAttempts:       10,
				MaximumSourceAttempts: 1000,
			},
			AccountRecovery: AccountRecovery{
				EmailVerificationTTL: Duration{Duration: 24 * time.Hour},
				PasswordResetTTL:     Duration{Duration: time.Hour},
				RateLimit: LoginRateLimit{
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
				SetupTTL:          Duration{Duration: 10 * time.Minute},
				RecoveryCodeCount: 10,
			},
			External: ExternalAuthentication{
				LoginStateTTL: Duration{Duration: 10 * time.Minute},
				Providers:     []ExternalAuthenticationProvider{},
			},
		},
		Log: Log{
			MaxFieldBytes: 16 << 10,
			Targets: []LogTarget{{
				Name:   "console",
				Type:   "console",
				Level:  "info",
				Format: "text",
			}},
		},
	}
}

func (c Config) Clone() Config {
	cloned := c
	cloned.Log.Targets = append([]LogTarget(nil), c.Log.Targets...)
	cloned.Cache.Redis.Addresses = append([]string(nil), c.Cache.Redis.Addresses...)
	cloned.Cluster.Memberlist.SeedAddresses = append(
		[]string(nil),
		c.Cluster.Memberlist.SeedAddresses...,
	)
	cloned.Mail.SecretSealing.DecryptionKeys = append(
		[]string(nil),
		c.Mail.SecretSealing.DecryptionKeys...,
	)
	cloned.Authentication.MFA.DecryptionKeys = append(
		[]string(nil),
		c.Authentication.MFA.DecryptionKeys...,
	)
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
			oidc.Scopes = append([]string(nil), sourceProvider.OIDC.Scopes...)
			provider.OIDC = &oidc
		}
	}
	return cloned
}

// Redacted returns a safe copy for display.
func (c Config) Redacted() Config {
	redacted := c.Clone()
	if redacted.Database.DataSource != "" {
		redacted.Database.DataSource = "[redacted]"
	}
	redacted.Cache.Redis.Password = redactSecret(redacted.Cache.Redis.Password)
	redacted.Cluster.Memberlist.EncryptionKey = redactSecret(redacted.Cluster.Memberlist.EncryptionKey)
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

	durations := []struct {
		field string
		value time.Duration
	}{
		{"server.read_header_timeout", c.Server.ReadHeaderTimeout.Duration},
		{"server.read_timeout", c.Server.ReadTimeout.Duration},
		{"server.write_timeout", c.Server.WriteTimeout.Duration},
		{"server.idle_timeout", c.Server.IdleTimeout.Duration},
		{"server.shutdown_timeout", c.Server.ShutdownTimeout.Duration},
	}
	for _, item := range durations {
		if item.value <= 0 {
			add(item.field, "must be greater than zero")
		}
	}
	if c.Server.MaxHeaderBytes < 1024 || c.Server.MaxHeaderBytes > 16<<20 {
		add("server.max_header_bytes", "must be between 1024 and 16777216")
	}
	if c.Server.MaxBodyBytes < 1024 || c.Server.MaxBodyBytes > 100<<20 {
		add("server.max_body_bytes", "must be between 1024 and 104857600")
	}

	validateDatabase(c.Database, add)
	validateCache(c.Cache, add)
	validateCluster(c.Cluster, add)
	validateMail(c.Mail, add)
	validateVFS(c.VFS, add)
	if c.Cluster.Backend == "memberlist" {
		if c.VFS.Backend == "local" {
			add("vfs.backend", "must be shared when cluster.backend is multi-node")
		}
	}
	validateAuthentication(c.Authentication, add)
	validateSecretKeySeparation(c, add)

	if c.Log.MaxFieldBytes < 256 || c.Log.MaxFieldBytes > 1<<20 {
		add("log.max_field_bytes", "must be between 256 and 1048576")
	}
	if len(c.Log.Targets) == 0 {
		add("log.targets", "must contain at least one target")
	}
	names := make(map[string]struct{}, len(c.Log.Targets))
	for index, target := range c.Log.Targets {
		prefix := fmt.Sprintf("log.targets[%d]", index)
		if target.Name == "" {
			add(prefix+".name", "is required")
		} else if _, exists := names[target.Name]; exists {
			add(prefix+".name", "must be unique")
		} else {
			names[target.Name] = struct{}{}
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

	if len(fields) != 0 {
		return &ValidationError{Fields: fields}
	}
	return nil
}

func validateCluster(cluster Cluster, add func(string, string)) {
	switch cluster.Backend {
	case "local":
	case "memberlist":
		if !validHostPort(cluster.Memberlist.BindAddress) {
			add("cluster.memberlist.bind_address", "must be a host:port TCP address")
		}
		if !validHostPort(cluster.Memberlist.AdvertiseAddress) {
			add("cluster.memberlist.advertise_address", "must be a host:port TCP address")
		}
		if strings.TrimSpace(cluster.Memberlist.EncryptionKey) == "" {
			add("cluster.memberlist.encryption_key", "is required")
		} else if _, err := decodeMemberlistKey(cluster.Memberlist.EncryptionKey); err != nil {
			add("cluster.memberlist.encryption_key", err.Error())
		}
		if cluster.Memberlist.DiscoveryTTL.Duration < 3*time.Second {
			add("cluster.memberlist.discovery_ttl", "must be at least 3s")
		}
		if cluster.Memberlist.DiscoveryHeartbeat.Duration <= 0 ||
			cluster.Memberlist.DiscoveryHeartbeat.Duration*2 >= cluster.Memberlist.DiscoveryTTL.Duration {
			add("cluster.memberlist.discovery_heartbeat", "must be greater than zero and less than half the discovery TTL")
		}
		if cluster.Memberlist.ProtocolMin <= 0 ||
			cluster.Memberlist.ProtocolMax < cluster.Memberlist.ProtocolMin {
			add("cluster.memberlist.protocol_min", "must form a valid inclusive protocol range with protocol_max")
		}
		for index, address := range cluster.Memberlist.SeedAddresses {
			if !validHostPort(address) {
				add(
					fmt.Sprintf("cluster.memberlist.seed_addresses[%d]", index),
					"must be a host:port TCP address",
				)
			}
		}
		if !cluster.Memberlist.AllowPublicBind {
			if isPublicClusterBind(cluster.Memberlist.BindAddress) {
				add("cluster.memberlist.bind_address", "public-interface binding is rejected by default")
			}
		}
	default:
		add("cluster.backend", "must be local or memberlist")
	}
	if len(cluster.NodeID) == 0 || len(cluster.NodeID) > 128 {
		add("cluster.node_id", "must contain between 1 and 128 characters")
		return
	}
	for _, character := range cluster.NodeID {
		if (character < 'a' || character > 'z') &&
			(character < 'A' || character > 'Z') &&
			(character < '0' || character > '9') &&
			character != '.' && character != '_' && character != '-' {
			add("cluster.node_id", "contains an invalid character")
			return
		}
	}
}

func validateNamespace(field, namespace string, add func(string, string)) {
	if len(namespace) == 0 || len(namespace) > 128 {
		add(field, "must contain between 1 and 128 characters")
		return
	}
	for _, character := range namespace {
		if (character < 'a' || character > 'z') &&
			(character < 'A' || character > 'Z') &&
			(character < '0' || character > '9') &&
			character != '.' && character != '_' && character != '-' {
			add(field, "contains an invalid character")
			return
		}
	}
}

func validateCache(cache Cache, add func(string, string)) {
	switch cache.Backend {
	case "memory":
	case "redis":
		if len(cache.Redis.Addresses) == 0 {
			add("cache.redis.addresses", "must contain at least one address")
		}
		for index, address := range cache.Redis.Addresses {
			if !validHostPort(address) {
				add(fmt.Sprintf("cache.redis.addresses[%d]", index), "must be a host:port TCP address")
			}
		}
		if cache.Redis.Database < 0 {
			add("cache.redis.database", "must not be negative")
		}
		if cache.Redis.ConnectTimeout.Duration <= 0 {
			add("cache.redis.connect_timeout", "must be greater than zero")
		}
	default:
		add("cache.backend", "must be memory or redis")
	}
	validateNamespace("cache.namespace", cache.Namespace, add)
}

func validateMail(mailConfig Mail, add func(string, string)) {
	if mailConfig.Backend != "smtp" {
		add("mail.backend", "must be smtp")
	}
	validateSecretSealing(
		"mail.secret_sealing",
		mailConfig.SecretSealing,
		add,
	)
	if !mailConfig.Enabled {
		return
	}
	if !validMailbox(mailConfig.FromAddress) {
		add("mail.from_address", "must be a plain email address")
	}
	if strings.ContainsAny(mailConfig.FromName, "\x00\r\n") {
		add("mail.from_name", "contains invalid characters")
	}
	if !validHostPort(mailConfig.SMTP.Address) {
		add("mail.smtp.address", "must be a host:port TCP address")
	}
	if strings.ContainsAny(mailConfig.SMTP.ServerName, "\x00\r\n") {
		add("mail.smtp.server_name", "contains invalid characters")
	}
	if strings.ContainsAny(mailConfig.SMTP.LocalName, "\x00\r\n") {
		add("mail.smtp.local_name", "contains invalid characters")
	}
	switch mailConfig.SMTP.Security {
	case "none", "starttls", "tls":
	default:
		add("mail.smtp.security", "must be none, starttls, or tls")
	}
	switch mailConfig.SMTP.Authentication {
	case "none":
		if mailConfig.SMTP.Username != "" || mailConfig.SMTP.Password != "" {
			add("mail.smtp.authentication", "cannot be none when credentials are configured")
		}
	case "auto", "plain", "login":
		if mailConfig.SMTP.Username == "" {
			add("mail.smtp.username", "is required when authentication is enabled")
		}
		if mailConfig.SMTP.Security == "none" {
			add("mail.smtp.security", "must use TLS when authentication is enabled")
		}
	default:
		add("mail.smtp.authentication", "must be none, auto, plain, or login")
	}
	if mailConfig.SMTP.Timeout.Duration <= 0 {
		add("mail.smtp.timeout", "must be greater than zero")
	}
	if mailConfig.SMTP.MessageIDDomain == "" ||
		strings.ContainsAny(mailConfig.SMTP.MessageIDDomain, "\x00\r\n @") {
		add("mail.smtp.message_id_domain", "has an invalid format")
	}
	if mailConfig.SMTP.MaxMessageBytes < 1024 || mailConfig.SMTP.MaxMessageBytes > 100<<20 {
		add("mail.smtp.max_message_bytes", "must be between 1024 and 104857600")
	}
	if mailConfig.SMTP.MaxRecipients < 1 || mailConfig.SMTP.MaxRecipients > 1000 {
		add("mail.smtp.max_recipients", "must be between 1 and 1000")
	}
}

func validateSecretSealing(
	prefix string,
	settings SecretSealing,
	add func(string, string),
) {
	const maximumFallbackKeys = 8
	if len(settings.DecryptionKeys) > maximumFallbackKeys {
		add(
			prefix+".decryption_keys",
			"must contain at most 8 fallback keys",
		)
	}
	if settings.EncryptionKey == "" && len(settings.DecryptionKeys) > 0 {
		add(prefix+".encryption_key", "is required")
	}
	keys := make([]string, 0, 1+len(settings.DecryptionKeys))
	keys = append(keys, settings.EncryptionKey)
	keys = append(keys, settings.DecryptionKeys...)
	seen := make(map[[32]byte]struct{}, len(keys))
	for index, encoded := range keys {
		if encoded == "" && index == 0 {
			continue
		}
		field := prefix + ".encryption_key"
		if index > 0 {
			field = fmt.Sprintf("%s.decryption_keys[%d]", prefix, index-1)
		}
		material, ok := decodeCanonicalAES256Key(encoded)
		if !ok {
			add(field, "must be canonical standard base64 encoding of exactly 32 bytes")
			continue
		}
		if _, duplicate := seen[material]; duplicate {
			add(prefix+".decryption_keys", "must not contain duplicate decoded keys")
			continue
		}
		seen[material] = struct{}{}
	}
}

func validateSecretKeySeparation(c Config, add func(string, string)) {
	reserved := make(map[[32]byte]struct{}, 2+len(c.Authentication.MFA.DecryptionKeys))
	for _, encoded := range append(
		[]string{c.Authentication.MFA.EncryptionKey},
		c.Authentication.MFA.DecryptionKeys...,
	) {
		if material, ok := decodeAES256Key(encoded); ok {
			reserved[material] = struct{}{}
		}
	}
	memberlistKey := strings.TrimSpace(c.Cluster.Memberlist.EncryptionKey)
	if len(memberlistKey) == base64.StdEncoding.EncodedLen(32) ||
		len(memberlistKey) == base64.RawStdEncoding.EncodedLen(32) {
		decoded, err := decodeMemberlistKey(memberlistKey)
		if err == nil && len(decoded) == 32 {
			var material [32]byte
			copy(material[:], decoded)
			reserved[material] = struct{}{}
		}
	}
	mailKeys := make([]string, 0, 1+len(c.Mail.SecretSealing.DecryptionKeys))
	mailKeys = append(mailKeys, c.Mail.SecretSealing.EncryptionKey)
	mailKeys = append(mailKeys, c.Mail.SecretSealing.DecryptionKeys...)
	for index, encoded := range mailKeys {
		material, ok := decodeCanonicalAES256Key(encoded)
		if !ok {
			continue
		}
		if _, reused := reserved[material]; !reused {
			continue
		}
		field := "mail.secret_sealing.encryption_key"
		if index > 0 {
			field = fmt.Sprintf("mail.secret_sealing.decryption_keys[%d]", index-1)
		}
		add(field, "must not reuse MFA or Memberlist key material")
	}
}

func decodeCanonicalAES256Key(encoded string) ([32]byte, bool) {
	var material [32]byte
	if len(encoded) != base64.StdEncoding.EncodedLen(len(material)) {
		return material, false
	}
	decoded, err := base64.StdEncoding.Strict().DecodeString(encoded)
	if err != nil || len(decoded) != len(material) ||
		base64.StdEncoding.EncodeToString(decoded) != encoded {
		return material, false
	}
	copy(material[:], decoded)
	return material, true
}

func decodeAES256Key(encoded string) ([32]byte, bool) {
	var material [32]byte
	var compact [44]byte
	compactLength := 0
	for index := range encoded {
		character := encoded[index]
		if character == '\r' || character == '\n' {
			continue
		}
		if compactLength == len(compact) {
			return material, false
		}
		compact[compactLength] = character
		compactLength++
	}
	if compactLength != base64.StdEncoding.EncodedLen(len(material)) {
		return material, false
	}
	decoded, err := base64.StdEncoding.DecodeString(string(compact[:compactLength]))
	if err != nil || len(decoded) != len(material) {
		return material, false
	}
	copy(material[:], decoded)
	return material, true
}

func validateVFS(vfsConfig VFS, add func(string, string)) {
	switch vfsConfig.Backend {
	case "local":
		if vfsConfig.Local.Root == "" || strings.ContainsRune(vfsConfig.Local.Root, '\x00') {
			add("vfs.local.root", "must be a non-empty filesystem path")
		}
	case "s3":
		if vfsConfig.S3.Endpoint == "" ||
			strings.ContainsAny(vfsConfig.S3.Endpoint, "\x00\r\n") {
			add("vfs.s3.endpoint", "is required and must not contain control characters")
		}
		if vfsConfig.S3.Bucket == "" || strings.ContainsAny(vfsConfig.S3.Bucket, "\x00\r\n/") {
			add("vfs.s3.bucket", "is required and must be a bucket name")
		}
		if strings.ContainsAny(vfsConfig.S3.Prefix, "\x00\r\n") {
			add("vfs.s3.prefix", "contains invalid characters")
		}
	default:
		add("vfs.backend", "must be local or s3")
	}
}

func validateAuthentication(authentication Authentication, add func(string, string)) {
	password := authentication.Password
	if password.MinimumLength < 8 || password.MinimumLength > 128 {
		add("authentication.password.minimum_length", "must be between 8 and 128")
	}
	if password.MaximumLength < password.MinimumLength || password.MaximumLength > 1024 {
		add("authentication.password.maximum_length", "must be between minimum_length and 1024")
	}
	if password.ArgonMemoryKiB < 19*1024 || password.ArgonMemoryKiB > 1024*1024 {
		add("authentication.password.argon_memory_kib", "must be between 19456 and 1048576")
	}
	if password.ArgonIterations < 1 || password.ArgonIterations > 20 {
		add("authentication.password.argon_iterations", "must be between 1 and 20")
	}
	if password.ArgonParallelism < 1 || password.ArgonParallelism > 64 {
		add("authentication.password.argon_parallelism", "must be between 1 and 64")
	}
	if password.ArgonSaltBytes < 16 || password.ArgonSaltBytes > 64 {
		add("authentication.password.argon_salt_bytes", "must be between 16 and 64")
	}
	if password.ArgonKeyBytes < 16 || password.ArgonKeyBytes > 64 {
		add("authentication.password.argon_key_bytes", "must be between 16 and 64")
	}

	sessions := authentication.Sessions
	for _, item := range []struct {
		field string
		value time.Duration
	}{
		{"authentication.sessions.access_ttl", sessions.AccessTTL.Duration},
		{"authentication.sessions.refresh_ttl", sessions.RefreshTTL.Duration},
		{"authentication.sessions.idle_ttl", sessions.IdleTTL.Duration},
		{"authentication.sessions.absolute_ttl", sessions.AbsoluteTTL.Duration},
		{"authentication.sessions.activity_update_interval", sessions.ActivityUpdateInterval.Duration},
	} {
		if item.value <= 0 {
			add(item.field, "must be greater than zero")
		}
	}
	if sessions.AccessTTL.Duration > sessions.IdleTTL.Duration {
		add("authentication.sessions.access_ttl", "must not exceed idle_ttl")
	}
	if sessions.IdleTTL.Duration > sessions.AbsoluteTTL.Duration {
		add("authentication.sessions.idle_ttl", "must not exceed absolute_ttl")
	}
	if sessions.RefreshTTL.Duration > sessions.AbsoluteTTL.Duration {
		add("authentication.sessions.refresh_ttl", "must not exceed absolute_ttl")
	}
	if sessions.ActivityUpdateInterval.Duration >= sessions.IdleTTL.Duration {
		add("authentication.sessions.activity_update_interval", "must be less than idle_ttl")
	}
	if sessions.MaximumPerUser < 1 || sessions.MaximumPerUser > 1000 {
		add("authentication.sessions.maximum_per_user", "must be between 1 and 1000")
	}
	if authentication.RecentAuthenticationTTL.Duration < time.Minute ||
		authentication.RecentAuthenticationTTL.Duration > 24*time.Hour {
		add(
			"authentication.recent_authentication_ttl",
			"must be between 1m and 24h",
		)
	}
	if authentication.RecentAuthenticationTTL.Duration >
		sessions.AbsoluteTTL.Duration {
		add(
			"authentication.recent_authentication_ttl",
			"must not exceed sessions.absolute_ttl",
		)
	}

	rateLimit := authentication.LoginRateLimit
	validateAuthenticationRateLimit(
		"authentication.login_rate_limit", rateLimit, add,
	)

	recovery := authentication.AccountRecovery
	if recovery.EmailVerificationTTL.Duration < 5*time.Minute ||
		recovery.EmailVerificationTTL.Duration > 30*24*time.Hour {
		add(
			"authentication.account_recovery.email_verification_ttl",
			"must be between 5m and 720h",
		)
	}
	if recovery.PasswordResetTTL.Duration < 5*time.Minute ||
		recovery.PasswordResetTTL.Duration > 24*time.Hour {
		add(
			"authentication.account_recovery.password_reset_ttl",
			"must be between 5m and 24h",
		)
	}
	validateAuthenticationRateLimit(
		"authentication.account_recovery.rate_limit",
		recovery.RateLimit,
		add,
	)

	tokens := authentication.PersonalAccessTokens
	if tokens.MinimumLifetime.Duration < 5*time.Minute ||
		tokens.MinimumLifetime.Duration > 24*time.Hour {
		add(
			"authentication.personal_access_tokens.minimum_lifetime",
			"must be between 5m and 24h",
		)
	}
	if tokens.MaximumLifetime.Duration < tokens.MinimumLifetime.Duration ||
		tokens.MaximumLifetime.Duration > 365*24*time.Hour {
		add(
			"authentication.personal_access_tokens.maximum_lifetime",
			"must be between minimum_lifetime and 8760h",
		)
	}
	if tokens.LastUsedUpdateInterval.Duration <= 0 ||
		tokens.LastUsedUpdateInterval.Duration >= tokens.MinimumLifetime.Duration {
		add(
			"authentication.personal_access_tokens.last_used_update_interval",
			"must be greater than zero and less than minimum_lifetime",
		)
	}
	if tokens.MaximumPerUser < 1 || tokens.MaximumPerUser > 1000 {
		add(
			"authentication.personal_access_tokens.maximum_per_user",
			"must be between 1 and 1000",
		)
	}

	mfa := authentication.MFA
	if len(mfa.Issuer) == 0 || len(mfa.Issuer) > 128 ||
		strings.ContainsAny(mfa.Issuer, "\x00\r\n") {
		add("authentication.mfa.issuer", "must contain between 1 and 128 safe characters")
	}
	if mfa.SetupTTL.Duration < time.Minute || mfa.SetupTTL.Duration > time.Hour {
		add("authentication.mfa.setup_ttl", "must be between 1m and 1h")
	}
	if mfa.RecoveryCodeCount < 5 || mfa.RecoveryCodeCount > 20 {
		add("authentication.mfa.recovery_code_count", "must be between 5 and 20")
	}
	keys := append([]string{mfa.EncryptionKey}, mfa.DecryptionKeys...)
	seenKeys := make(map[string]struct{}, len(keys))
	for index, key := range keys {
		if key == "" {
			if index == 0 && mfa.Enabled {
				add(
					"authentication.mfa.encryption_key",
					"is required when MFA is enabled",
				)
			}
			continue
		}
		decoded, err := base64.StdEncoding.DecodeString(key)
		if err != nil || len(decoded) != 32 {
			field := "authentication.mfa.encryption_key"
			if index > 0 {
				field = fmt.Sprintf(
					"authentication.mfa.decryption_keys[%d]",
					index-1,
				)
			}
			add(field, "must be standard base64 encoding of exactly 32 bytes")
			continue
		}
		if _, exists := seenKeys[key]; exists {
			add("authentication.mfa.decryption_keys", "must not contain duplicate keys")
			continue
		}
		seenKeys[key] = struct{}{}
	}

	validateExternalAuthentication(authentication.External, add)
}

func validateAuthenticationRateLimit(
	prefix string,
	rateLimit LoginRateLimit,
	add func(string, string),
) {
	if rateLimit.Window.Duration < time.Second || rateLimit.Window.Duration > 24*time.Hour {
		add(prefix+".window", "must be between 1s and 24h")
	}
	if rateLimit.MaximumAttempts < 1 || rateLimit.MaximumAttempts > 10000 {
		add(prefix+".maximum_attempts", "must be between 1 and 10000")
	}
	if rateLimit.MaximumSourceAttempts < rateLimit.MaximumAttempts ||
		rateLimit.MaximumSourceAttempts > 1_000_000 {
		add(prefix+".maximum_source_attempts", "must be between maximum_attempts and 1000000")
	}
}

func validHostPort(address string) bool {
	host, portText, err := net.SplitHostPort(address)
	if err != nil || host == "" {
		return false
	}
	port, err := strconv.Atoi(portText)
	return err == nil && port >= 1 && port <= 65535
}

func decodeMemberlistKey(encoded string) ([]byte, error) {
	trimmed := strings.TrimSpace(encoded)
	switch len(trimmed) {
	case base64.StdEncoding.EncodedLen(16),
		base64.StdEncoding.EncodedLen(24),
		base64.StdEncoding.EncodedLen(32):
	default:
		return nil, errors.New("must decode to 16, 24, or 32 bytes")
	}
	key, err := base64.StdEncoding.DecodeString(trimmed)
	if err != nil {
		return nil, errors.New("must be base64-encoded")
	}
	if len(key) != 16 && len(key) != 24 && len(key) != 32 {
		return nil, errors.New("must decode to 16, 24, or 32 bytes")
	}
	return key, nil
}

func isPublicClusterBind(address string) bool {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return false
	}
	if host == "0.0.0.0" || host == "::" || host == "[::]" {
		return true
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return false
	}
	return !(ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast())
}

func validMailbox(value string) bool {
	address, err := netmail.ParseAddress(value)
	return err == nil && address.Name == "" && address.Address == value
}

func validateDatabase(database Database, add func(string, string)) {
	if strings.ContainsAny(database.DataSource, "\x00\r\n") {
		add("database.data_source", "contains invalid characters")
	} else {
		parsed, err := url.Parse(database.DataSource)
		if err != nil || parsed.Scheme != "postgres" || parsed.Host == "" || strings.Trim(parsed.Path, "/") == "" {
			add("database.data_source", "must be a PostgreSQL URL with host and database name")
		}
	}
	if database.MaxOpenConnections <= 0 {
		add("database.max_open_connections", "must be greater than zero")
	}
	if database.MaxIdleConnections < 0 || database.MaxIdleConnections > database.MaxOpenConnections {
		add("database.max_idle_connections", "must be between zero and max_open_connections")
	}
	for _, item := range []struct {
		field string
		value time.Duration
	}{
		{"database.connection_max_lifetime", database.ConnectionMaxLifetime.Duration},
		{"database.connection_max_idle_time", database.ConnectionMaxIdleTime.Duration},
		{"database.query_timeout", database.QueryTimeout.Duration},
		{"database.migration_timeout", database.MigrationTimeout.Duration},
	} {
		if item.value <= 0 {
			add(item.field, "must be greater than zero")
		}
	}
}

func validateListenAddress(address string, add func(string, string)) {
	host, portText, err := net.SplitHostPort(address)
	if err != nil {
		add("server.listen_address", "must be a host:port TCP address")
		return
	}
	if strings.ContainsAny(host, "\r\n") {
		add("server.listen_address", "host contains invalid characters")
	}
	port, err := strconv.Atoi(portText)
	if err != nil || port < 0 || port > 65535 {
		add("server.listen_address", "port must be between 0 and 65535")
	}
}

func validatePublicURL(raw string, add func(string, string)) {
	parsed, err := url.Parse(raw)
	if err != nil {
		add("server.public_url", "must be an absolute HTTP or HTTPS URL")
		return
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		add("server.public_url", "scheme must be http or https")
	}
	if parsed.Host == "" {
		add("server.public_url", "host is required")
	}
	if parsed.User != nil {
		add("server.public_url", "user information is forbidden")
	}
	if parsed.RawQuery != "" || parsed.Fragment != "" {
		add("server.public_url", "query and fragment are forbidden")
	}
}
