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
	"unicode"

	"golang.org/x/net/idna"
)

const SchemaVersion = 1

type ServerTLSMode string

const (
	ServerTLSModeDisabled    ServerTLSMode = "disabled"
	ServerTLSModeStatic      ServerTLSMode = "static"
	ServerTLSModeLetsEncrypt ServerTLSMode = "lets_encrypt"
)

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
	ListenAddress     string    `json:"ListenAddress"`
	PublicURL         string    `json:"PublicURL"`
	TLS               ServerTLS `json:"TLS"`
	ReadHeaderTimeout Duration  `json:"ReadHeaderTimeout"`
	ReadTimeout       Duration  `json:"ReadTimeout"`
	WriteTimeout      Duration  `json:"WriteTimeout"`
	IdleTimeout       Duration  `json:"IdleTimeout"`
	ShutdownTimeout   Duration  `json:"ShutdownTimeout"`
	MaxHeaderBytes    int       `json:"MaxHeaderBytes"`
	MaxBodyBytes      int64     `json:"MaxBodyBytes"`
}

// ServerTLS configures the client-facing listener. Mode is one of disabled,
// static, or lets_encrypt. HTTP forwarding is owned by the same runtime so
// ACME HTTP-01 challenges and redirects share one lifecycle.
type ServerTLS struct {
	Mode               ServerTLSMode     `json:"Mode"`
	CertificateFile    string            `json:"CertificateFile"`
	PrivateKeyFile     string            `json:"PrivateKeyFile"`
	LetsEncrypt        ServerLetsEncrypt `json:"LetsEncrypt"`
	ForwardHTTPToHTTPS bool              `json:"ForwardHTTPToHTTPS"`
	HTTPListenAddress  string            `json:"HTTPListenAddress"`
}

type ServerLetsEncrypt struct {
	Email          string `json:"Email"`
	CacheDirectory string `json:"CacheDirectory"`
}

// Metrics configures the node-local Prometheus scrape listener. A listener
// reachable beyond loopback must use both TLS and bearer authentication.
type Metrics struct {
	Enabled                  bool       `json:"Enabled"`
	ListenAddress            string     `json:"ListenAddress"`
	BearerToken              string     `json:"BearerToken"`
	TLS                      MetricsTLS `json:"TLS"`
	ReadHeaderTimeout        Duration   `json:"ReadHeaderTimeout"`
	ReadTimeout              Duration   `json:"ReadTimeout"`
	WriteTimeout             Duration   `json:"WriteTimeout"`
	IdleTimeout              Duration   `json:"IdleTimeout"`
	ShutdownTimeout          Duration   `json:"ShutdownTimeout"`
	MaximumConcurrentScrapes int        `json:"MaximumConcurrentScrapes"`
}

type MetricsTLS struct {
	CertificateFile string `json:"CertificateFile"`
	PrivateKeyFile  string `json:"PrivateKeyFile"`
}

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

type Database struct {
	DataSource            string   `json:"DataSource"`
	MaxOpenConnections    int      `json:"MaxOpenConnections"`
	MaxIdleConnections    int      `json:"MaxIdleConnections"`
	ConnectionMaxLifetime Duration `json:"ConnectionMaxLifetime"`
	ConnectionMaxIdleTime Duration `json:"ConnectionMaxIdleTime"`
	QueryTimeout          Duration `json:"QueryTimeout"`
	MigrationTimeout      Duration `json:"MigrationTimeout"`
}

type CacheRedis struct {
	Addresses      []string `json:"Addresses"`
	Username       string   `json:"Username"`
	Password       string   `json:"Password"`
	Database       int      `json:"Database"`
	TLS            bool     `json:"TLS"`
	ConnectTimeout Duration `json:"ConnectTimeout"`
}

// CacheMemory bounds the process-local disposable cache. MaxBytes accounts for
// retained key and encoded-value bytes; implementation overhead is additional.
type CacheMemory struct {
	MaxEntries int   `json:"MaxEntries"`
	MaxBytes   int64 `json:"MaxBytes"`
}

type Cache struct {
	Backend   string      `json:"Backend"`
	Namespace string      `json:"Namespace"`
	Memory    CacheMemory `json:"Memory"`
	Redis     CacheRedis  `json:"Redis"`
}

// ClusterMemberlist configures the built-in multi-node gossip transport.
type ClusterMemberlist struct {
	BindAddress        string   `json:"BindAddress"`
	AdvertiseAddress   string   `json:"AdvertiseAddress"`
	EncryptionKey      string   `json:"EncryptionKey"`
	DecryptionKeys     []string `json:"DecryptionKeys"`
	SeedAddresses      []string `json:"SeedAddresses"`
	DiscoveryTTL       Duration `json:"DiscoveryTTL"`
	DiscoveryHeartbeat Duration `json:"DiscoveryHeartbeat"`
	AllowPublicBind    bool     `json:"AllowPublicBind"`
}

// Cluster selects the inter-node transport and gives this process its stable
// runtime identity. "local" is the single-node degenerate transport.
// "memberlist" is the built-in multi-node backend and requires Redis so
// installation-wide disposable security counters remain coherent.
type Cluster struct {
	Backend    string            `json:"Backend"`
	NodeID     string            `json:"NodeID"`
	Memberlist ClusterMemberlist `json:"Memberlist"`
}

type MailSMTP struct {
	Address         string   `json:"Address"`
	ServerName      string   `json:"ServerName"`
	LocalName       string   `json:"LocalName"`
	Security        string   `json:"Security"`
	Username        string   `json:"Username"`
	Password        string   `json:"Password"`
	Authentication  string   `json:"Authentication"`
	Timeout         Duration `json:"Timeout"`
	MessageIDDomain string   `json:"MessageIDDomain"`
	MaxMessageBytes int64    `json:"MaxMessageBytes"`
	MaxRecipients   int      `json:"MaxRecipients"`
}

// SecretSealing configures a primary AES-256 encryption key and the bounded
// fallback ring used to read values written before rotation.
type SecretSealing struct {
	EncryptionKey  string   `json:"EncryptionKey"`
	DecryptionKeys []string `json:"DecryptionKeys"`
}

type Mail struct {
	Enabled       bool          `json:"Enabled"`
	Backend       string        `json:"Backend"`
	FromAddress   string        `json:"FromAddress"`
	FromName      string        `json:"FromName"`
	SMTP          MailSMTP      `json:"SMTP"`
	SecretSealing SecretSealing `json:"SecretSealing"`
}

type VFSLocal struct {
	Root string `json:"Root"`
}

type VFSS3 struct {
	Endpoint     string `json:"Endpoint"`
	AccessKey    string `json:"AccessKey"`
	SecretKey    string `json:"SecretKey"`
	SessionToken string `json:"SessionToken"`
	Bucket       string `json:"Bucket"`
	Prefix       string `json:"Prefix"`
	Region       string `json:"Region"`
	Secure       bool   `json:"Secure"`
}

type VFS struct {
	Backend string   `json:"Backend"`
	Local   VFSLocal `json:"Local"`
	S3      VFSS3    `json:"S3"`
}

// ExecutionHost is one operator-configured outbound execenv endpoint. ID is
// the stable placement identity persisted with grants; changing Address does
// not change that identity. Token and client-key material never enter
// application state.
type ExecutionHost struct {
	ID                    string `json:"ID"`
	Address               string `json:"Address"`
	Security              string `json:"Security"`
	Token                 string `json:"Token"`
	ServerName            string `json:"ServerName"`
	CAFile                string `json:"CAFile"`
	ClientCertificateFile string `json:"ClientCertificateFile"`
	ClientKeyFile         string `json:"ClientKeyFile"`
}

// Execution configures the installation's bounded set of execenv hosts.
// Host changes require a node restart so every node uses one immutable
// placement catalog for its lifetime.
type Execution struct {
	Enabled          bool            `json:"Enabled"`
	DialTimeout      Duration        `json:"DialTimeout"`
	OperationTimeout Duration        `json:"OperationTimeout"`
	Hosts            []ExecutionHost `json:"Hosts"`
}

type Password struct {
	MinimumLength    int `json:"MinimumLength"`
	MaximumLength    int `json:"MaximumLength"`
	ArgonMemoryKiB   int `json:"ArgonMemoryKiB"`
	ArgonIterations  int `json:"ArgonIterations"`
	ArgonParallelism int `json:"ArgonParallelism"`
	ArgonSaltBytes   int `json:"ArgonSaltBytes"`
	ArgonKeyBytes    int `json:"ArgonKeyBytes"`
}

type Sessions struct {
	AccessTTL              Duration `json:"AccessTTL"`
	RefreshTTL             Duration `json:"RefreshTTL"`
	IdleTTL                Duration `json:"IdleTTL"`
	AbsoluteTTL            Duration `json:"AbsoluteTTL"`
	ActivityUpdateInterval Duration `json:"ActivityUpdateInterval"`
	MaximumPerUser         int      `json:"MaximumPerUser"`
}

type LoginRateLimit struct {
	Window                Duration `json:"Window"`
	MaximumAttempts       int      `json:"MaximumAttempts"`
	MaximumSourceAttempts int      `json:"MaximumSourceAttempts"`
}

type AccountRecovery struct {
	EmailVerificationTTL Duration       `json:"EmailVerificationTTL"`
	PasswordResetTTL     Duration       `json:"PasswordResetTTL"`
	RateLimit            LoginRateLimit `json:"RateLimit"`
}

type PersonalAccessTokens struct {
	MinimumLifetime        Duration `json:"MinimumLifetime"`
	MaximumLifetime        Duration `json:"MaximumLifetime"`
	LastUsedUpdateInterval Duration `json:"LastUsedUpdateInterval"`
	MaximumPerUser         int      `json:"MaximumPerUser"`
}

// Bootstrap protects the one-time public installation initialization route.
// DevelopmentMode permits process-generated secret material only while the
// installation is pristine and both listener and public origin are loopback-only.
type Bootstrap struct {
	Secret          string `json:"Secret"`
	DevelopmentMode bool   `json:"DevelopmentMode"`
}

// MFA contains operator-owned cryptographic and policy settings. The primary
// key encrypts new TOTP secrets; decryption_keys permits online key rotation
// while existing credentials are re-encrypted.
type MFA struct {
	Enabled           bool     `json:"Enabled"`
	Issuer            string   `json:"Issuer"`
	EncryptionKey     string   `json:"EncryptionKey"`
	DecryptionKeys    []string `json:"DecryptionKeys"`
	SetupTTL          Duration `json:"SetupTTL"`
	RecoveryCodeCount int      `json:"RecoveryCodeCount"`
}

type Authentication struct {
	Bootstrap               Bootstrap              `json:"Bootstrap"`
	Password                Password               `json:"Password"`
	Sessions                Sessions               `json:"Sessions"`
	RecentAuthenticationTTL Duration               `json:"RecentAuthenticationTTL"`
	LoginRateLimit          LoginRateLimit         `json:"LoginRateLimit"`
	AccountRecovery         AccountRecovery        `json:"AccountRecovery"`
	PersonalAccessTokens    PersonalAccessTokens   `json:"PersonalAccessTokens"`
	MFA                     MFA                    `json:"MFA"`
	External                ExternalAuthentication `json:"External"`
}

// Localization selects the installation-wide fallback locale. Catalog
// availability is validated by the i18n module during root composition.
type Localization struct {
	DefaultLocale string `json:"DefaultLocale"`
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
	Execution      Execution      `json:"Execution"`
	Authentication Authentication `json:"Authentication"`
	Localization   Localization   `json:"Localization"`
	Log            Log            `json:"Log"`
}

func Default() Config {
	return Config{
		Version: SchemaVersion,
		Server: Server{
			ListenAddress: "127.0.0.1:8065",
			PublicURL:     "http://localhost:8065",
			TLS: ServerTLS{
				Mode:              ServerTLSModeDisabled,
				HTTPListenAddress: "127.0.0.1:8080",
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
		Execution: Execution{
			Enabled:          false,
			DialTimeout:      Duration{Duration: 10 * time.Second},
			OperationTimeout: Duration{Duration: 30 * time.Second},
			Hosts:            []ExecutionHost{},
		},
		Authentication: Authentication{
			Bootstrap: Bootstrap{DevelopmentMode: true},
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
	cloned.Cache.Redis.Addresses = cloneSlice(c.Cache.Redis.Addresses)
	cloned.Execution.Hosts = cloneSlice(c.Execution.Hosts)
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
	for index := range redacted.Execution.Hosts {
		redacted.Execution.Hosts[index].Token = redactSecret(redacted.Execution.Hosts[index].Token)
	}
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
	validateServerTLS(c.Server, c.Cluster, add)
	validateMetrics(c.Metrics, c.Server, add)

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
	validateExecution(c.Execution, add)
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

	if c.Log.MaxFieldBytes < 256 || c.Log.MaxFieldBytes > 1<<20 {
		add("log.max_field_bytes", "must be between 256 and 1048576")
	}
	if c.Log.QueueSize < 1 || c.Log.QueueSize > 1<<20 {
		add("log.queue_size", "must be between 1 and 1048576")
	}
	for _, timeout := range []struct {
		path  string
		value time.Duration
	}{
		{path: "log.enqueue_timeout", value: c.Log.EnqueueTimeout.Duration},
		{path: "log.flush_timeout", value: c.Log.FlushTimeout.Duration},
		{path: "log.shutdown_timeout", value: c.Log.ShutdownTimeout.Duration},
	} {
		if timeout.value <= 0 || timeout.value > time.Minute {
			add(timeout.path, "must be positive and no greater than one minute")
		}
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

	if len(fields) != 0 {
		return &ValidationError{Fields: fields}
	}
	return nil
}

func validateMetrics(metrics Metrics, server Server, add func(string, string)) {
	if !validHostPort(metrics.ListenAddress) {
		add("metrics.listen_address", "must be a host:port TCP address")
	} else {
		metricsHost, _, _ := net.SplitHostPort(metrics.ListenAddress)
		if metrics.Enabled && listenAddressesConflict(metrics.ListenAddress, server.ListenAddress) {
			add("metrics.listen_address", "must differ from server.listen_address")
		}
		if metrics.Enabled && server.TLS.ForwardHTTPToHTTPS &&
			listenAddressesConflict(metrics.ListenAddress, server.TLS.HTTPListenAddress) {
			add("metrics.listen_address", "must differ from server.tls.http_listen_address")
		}
		ip := net.ParseIP(metricsHost)
		loopback := strings.EqualFold(metricsHost, "localhost") || (ip != nil && ip.IsLoopback())
		hasCertificate := strings.TrimSpace(metrics.TLS.CertificateFile) != ""
		hasKey := strings.TrimSpace(metrics.TLS.PrivateKeyFile) != ""
		if metrics.Enabled && hasCertificate != hasKey {
			add("metrics.tls", "certificate_file and private_key_file must be configured together")
		}
		if metrics.Enabled && !loopback && (!hasCertificate || !hasKey || len(metrics.BearerToken) < 32) {
			add("metrics.listen_address", "non-loopback metrics require TLS and a bearer token of at least 32 bytes")
		}
	}
	if metrics.BearerToken != "" && (len(metrics.BearerToken) < 32 || len(metrics.BearerToken) > 512) {
		add("metrics.bearer_token", "must contain between 32 and 512 bytes when configured")
	}
	for _, timeout := range []struct {
		field string
		value time.Duration
	}{
		{"metrics.read_header_timeout", metrics.ReadHeaderTimeout.Duration},
		{"metrics.read_timeout", metrics.ReadTimeout.Duration},
		{"metrics.write_timeout", metrics.WriteTimeout.Duration},
		{"metrics.idle_timeout", metrics.IdleTimeout.Duration},
		{"metrics.shutdown_timeout", metrics.ShutdownTimeout.Duration},
	} {
		if timeout.value <= 0 {
			add(timeout.field, "must be greater than zero")
		}
	}
	if metrics.MaximumConcurrentScrapes < 1 || metrics.MaximumConcurrentScrapes > 32 {
		add("metrics.maximum_concurrent_scrapes", "must be between 1 and 32")
	}
}

func listenAddressesConflict(first, second string) bool {
	firstHost, firstPort, firstErr := net.SplitHostPort(first)
	secondHost, secondPort, secondErr := net.SplitHostPort(second)
	if firstErr != nil || secondErr != nil || firstPort != secondPort {
		return false
	}
	firstHost = strings.TrimSuffix(strings.ToLower(firstHost), ".")
	secondHost = strings.TrimSuffix(strings.ToLower(secondHost), ".")
	if firstHost == secondHost {
		return true
	}
	firstIP, secondIP := net.ParseIP(firstHost), net.ParseIP(secondHost)
	if (firstIP != nil && firstIP.IsUnspecified()) || (secondIP != nil && secondIP.IsUnspecified()) ||
		firstHost == "" || secondHost == "" {
		return true
	}
	firstLoopback := firstHost == "localhost" || (firstIP != nil && firstIP.IsLoopback())
	secondLoopback := secondHost == "localhost" || (secondIP != nil && secondIP.IsLoopback())
	return firstLoopback && secondLoopback
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
		validateMemberlistKeyring(cluster.Memberlist, add)
		if cluster.Memberlist.DiscoveryTTL.Duration < 3*time.Second {
			add("cluster.memberlist.discovery_ttl", "must be at least 3s")
		}
		if cluster.Memberlist.DiscoveryHeartbeat.Duration <= 0 ||
			cluster.Memberlist.DiscoveryHeartbeat.Duration*2 >= cluster.Memberlist.DiscoveryTTL.Duration {
			add("cluster.memberlist.discovery_heartbeat", "must be greater than zero and less than half the discovery TTL")
		}
		if len(cluster.Memberlist.SeedAddresses) > 32 {
			add("cluster.memberlist.seed_addresses", "must contain at most 32 entries")
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

func validateMemberlistKeyring(settings ClusterMemberlist, add func(string, string)) {
	const maximumFallbackKeys = 8
	if len(settings.DecryptionKeys) > maximumFallbackKeys {
		add("cluster.memberlist.decryption_keys", "must contain at most 8 fallback keys")
	}
	if strings.TrimSpace(settings.EncryptionKey) == "" {
		add("cluster.memberlist.encryption_key", "is required")
	}
	keys := append([]string{settings.EncryptionKey}, settings.DecryptionKeys...)
	seen := make(map[string]struct{}, len(keys))
	for index, encoded := range keys {
		field := "cluster.memberlist.encryption_key"
		if index > 0 {
			field = fmt.Sprintf("cluster.memberlist.decryption_keys[%d]", index-1)
		}
		key, err := decodeMemberlistKey(encoded)
		if err != nil {
			add(field, err.Error())
			continue
		}
		if _, duplicate := seen[string(key)]; duplicate {
			add("cluster.memberlist.decryption_keys", "must not contain duplicate decoded keys")
			continue
		}
		seen[string(key)] = struct{}{}
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
		if cache.Memory.MaxEntries < 1 || cache.Memory.MaxEntries > 1_000_000 {
			add("cache.memory.max_entries", "must be between 1 and 1000000")
		}
		if cache.Memory.MaxBytes < 1<<20 || cache.Memory.MaxBytes > 1<<30 {
			add("cache.memory.max_bytes", "must be between 1048576 and 1073741824")
		}
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
	if mailConfig.SecretSealing.EncryptionKey == "" {
		add("mail.secret_sealing.encryption_key", "is required when mail is enabled")
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
	memberlistKeys := append(
		[]string{c.Cluster.Memberlist.EncryptionKey},
		c.Cluster.Memberlist.DecryptionKeys...,
	)
	for _, memberlistKey := range memberlistKeys {
		memberlistKey = strings.TrimSpace(memberlistKey)
		if len(memberlistKey) != base64.StdEncoding.EncodedLen(32) &&
			len(memberlistKey) != base64.RawStdEncoding.EncodedLen(32) {
			continue
		}
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

func validateExecution(execution Execution, add func(string, string)) {
	if execution.DialTimeout.Duration <= 0 || execution.DialTimeout.Duration > time.Minute {
		add("execution.dial_timeout", "must be positive and no greater than one minute")
	}
	if execution.OperationTimeout.Duration <= 0 || execution.OperationTimeout.Duration > 5*time.Minute {
		add("execution.operation_timeout", "must be positive and no greater than five minutes")
	}
	if len(execution.Hosts) > 64 {
		add("execution.hosts", "must contain at most 64 hosts")
	}
	if execution.Enabled && len(execution.Hosts) == 0 {
		add("execution.hosts", "must contain at least one host when execution is enabled")
	}
	seen := make(map[string]struct{}, len(execution.Hosts))
	for index, host := range execution.Hosts {
		prefix := fmt.Sprintf("execution.hosts[%d]", index)
		if !validExecutionHostID(host.ID) {
			add(prefix+".id", "must contain 1 to 64 URL-safe identifier characters")
		} else if _, exists := seen[host.ID]; exists {
			add(prefix+".id", "must be unique")
		} else {
			seen[host.ID] = struct{}{}
		}
		if !validHostPort(host.Address) {
			add(prefix+".address", "must be a host:port TCP address")
		}
		if strings.ContainsAny(host.Token, "\x00\r\n") || len(host.Token) > 512 {
			add(prefix+".token", "must contain at most 512 bytes without control characters")
		}
		for field, value := range map[string]string{
			"server_name": host.ServerName, "ca_file": host.CAFile,
			"client_certificate_file": host.ClientCertificateFile, "client_key_file": host.ClientKeyFile,
		} {
			if strings.ContainsAny(value, "\x00\r\n") {
				add(prefix+"."+field, "must not contain control characters")
			}
		}
		clientCertificate := host.ClientCertificateFile != "" || host.ClientKeyFile != ""
		if (host.ClientCertificateFile == "") != (host.ClientKeyFile == "") {
			add(prefix+".client_certificate_file", "must be configured together with client_key_file")
		}
		switch host.Security {
		case "tls":
			if host.ServerName == "" {
				add(prefix+".server_name", "is required for TLS hostname verification")
			}
			if host.Token == "" && !clientCertificate {
				add(prefix+".token", "or a client certificate is required for TLS authentication")
			}
		case "insecure_local":
			if !loopbackHostPort(host.Address) {
				add(prefix+".address", "must be loopback when security is insecure_local")
			}
			if host.Token == "" {
				add(prefix+".token", "is required for insecure_local authentication")
			}
			if host.ServerName != "" || host.CAFile != "" || clientCertificate {
				add(prefix+".security", "insecure_local cannot configure TLS material")
			}
		default:
			add(prefix+".security", "must be tls or insecure_local")
		}
	}
}

func validExecutionHostID(value string) bool {
	if len(value) == 0 || len(value) > 64 {
		return false
	}
	for _, character := range value {
		if (character < 'a' || character > 'z') && (character < 'A' || character > 'Z') &&
			(character < '0' || character > '9') && character != '.' && character != '_' && character != '-' {
			return false
		}
	}
	return true
}

func loopbackHostPort(address string) bool {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return false
	}
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
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
	if database.MaxOpenConnections < 2 {
		add("database.max_open_connections", "must be at least 2 for locked migrations")
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

func validateServerTLS(server Server, cluster Cluster, add func(string, string)) {
	settings := server.TLS
	parsedPublicURL, _ := url.Parse(server.PublicURL)

	for _, item := range []struct {
		field string
		value string
	}{
		{"server.tls.certificate_file", settings.CertificateFile},
		{"server.tls.private_key_file", settings.PrivateKeyFile},
		{"server.tls.lets_encrypt.cache_directory", settings.LetsEncrypt.CacheDirectory},
	} {
		if strings.ContainsRune(item.value, '\x00') {
			add(item.field, "contains a null byte")
		}
	}
	if settings.LetsEncrypt.Email != "" && !validMailbox(settings.LetsEncrypt.Email) {
		add("server.tls.lets_encrypt.email", "must be a plain email address")
	}

	switch settings.Mode {
	case ServerTLSModeDisabled:
	case ServerTLSModeStatic:
		if strings.TrimSpace(settings.CertificateFile) == "" {
			add("server.tls.certificate_file", "is required when TLS mode is static")
		}
		if strings.TrimSpace(settings.PrivateKeyFile) == "" {
			add("server.tls.private_key_file", "is required when TLS mode is static")
		}
	case ServerTLSModeLetsEncrypt:
		if strings.TrimSpace(settings.LetsEncrypt.CacheDirectory) == "" {
			add("server.tls.lets_encrypt.cache_directory", "is required when TLS mode is lets_encrypt")
		}
		if server.LetsEncryptHostname() == "" {
			add("server.public_url", "must use a fully qualified DNS hostname for Let's Encrypt")
		}
		if cluster.Backend == "memberlist" {
			add("server.tls.mode", "lets_encrypt is single-node only; terminate public TLS at the load balancer in a cluster")
		}
		if !settings.ForwardHTTPToHTTPS {
			add("server.tls.forward_http_to_https", "must be enabled for Let's Encrypt HTTP-01 challenges")
		}
	default:
		add("server.tls.mode", "must be disabled, static, or lets_encrypt")
	}

	if settings.Mode != ServerTLSModeDisabled && (parsedPublicURL == nil || parsedPublicURL.Scheme != "https") {
		add("server.public_url", "scheme must be https when built-in TLS is enabled")
	}
	if settings.ForwardHTTPToHTTPS {
		if settings.Mode == ServerTLSModeDisabled {
			add("server.tls.forward_http_to_https", "requires static or lets_encrypt TLS mode")
		}
		validateTCPListenAddress("server.tls.http_listen_address", settings.HTTPListenAddress, false, add)
		if settings.HTTPListenAddress == server.ListenAddress {
			add("server.tls.http_listen_address", "must differ from server.listen_address")
		}
	}
}

// LetsEncryptHostname returns the canonical exact DNS hostname eligible for
// automatic certificate issuance, or an empty string when PublicURL cannot
// identify one safely.
func (server Server) LetsEncryptHostname() string {
	parsed, err := url.Parse(server.PublicURL)
	if err != nil {
		return ""
	}
	hostname := strings.TrimSuffix(strings.ToLower(parsed.Hostname()), ".")
	if !validACMEDNSHostname(hostname) {
		return ""
	}
	return hostname
}

func validACMEDNSHostname(hostname string) bool {
	if len(hostname) == 0 || len(hostname) > 253 || hostname == "localhost" ||
		net.ParseIP(hostname) != nil || !strings.Contains(hostname, ".") {
		return false
	}
	canonical, err := idna.Lookup.ToASCII(hostname)
	if err != nil || canonical != hostname {
		return false
	}
	for _, label := range strings.Split(hostname, ".") {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, character := range label {
			if (character < 'a' || character > 'z') &&
				(character < '0' || character > '9') && character != '-' {
				return false
			}
		}
	}
	return true
}

func validateBootstrap(server Server, bootstrap Bootstrap, add func(string, string)) {
	secret := bootstrap.Secret
	if secret != "" {
		if strings.TrimSpace(secret) != secret {
			add("authentication.bootstrap.secret", "must not contain surrounding whitespace")
		}
		if len(secret) < 32 || len(secret) > 512 {
			add("authentication.bootstrap.secret", "must be between 32 and 512 bytes")
		}
		if strings.ContainsFunc(secret, unicode.IsControl) {
			add("authentication.bootstrap.secret", "must not contain control characters")
		}
	}

	loopbackDevelopment := bootstrap.DevelopmentMode &&
		isLiteralLoopbackListenAddress(server.ListenAddress) &&
		isLoopbackPublicURL(server.PublicURL)
	if bootstrap.DevelopmentMode && !loopbackDevelopment {
		add("authentication.bootstrap.development_mode", "requires a loopback listener and loopback public URL")
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

func validLocaleIdentifier(value string) bool {
	if value == "" || len(value) > 35 || strings.TrimSpace(value) != value {
		return false
	}
	for index, part := range strings.Split(value, "-") {
		if part == "" || len(part) > 8 {
			return false
		}
		for _, character := range part {
			if character < 'A' || character > 'Z' {
				if character < 'a' || character > 'z' {
					if index == 0 || character < '0' || character > '9' {
						return false
					}
				}
			}
		}
	}
	return true
}
