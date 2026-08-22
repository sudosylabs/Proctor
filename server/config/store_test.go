// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package config

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestStoreLoadsDefaultsAndReturnsClones(t *testing.T) {
	t.Parallel()

	store, err := NewStore(context.Background(), NewMemoryStore(nil), StoreOptions{LookupEnv: noEnvironment})
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	got := store.Get()
	if !reflect.DeepEqual(got, Default()) {
		t.Fatalf("Get() = %#v, want defaults %#v", got, Default())
	}
	got.Log.Targets[0].Level = "error"
	got.Cache.Redis.Addresses[0] = "mutated:6379"
	got.Authentication.MFA.DecryptionKeys = []string{"mutated"}
	if store.Get().Log.Targets[0].Level != "info" {
		t.Fatal("Get exposed mutable store state")
	}
	if store.Get().Cache.Redis.Addresses[0] == "mutated:6379" {
		t.Fatal("Get exposed mutable cache address state")
	}
	if len(store.Get().Authentication.MFA.DecryptionKeys) != 0 {
		t.Fatal("Get exposed mutable MFA decryption keys")
	}
}

func TestStoreSetPersistsAndNotifies(t *testing.T) {
	t.Parallel()

	backing := NewMemoryStore(nil)
	store, err := NewStore(context.Background(), backing, StoreOptions{LookupEnv: noEnvironment})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })

	var notifiedOld Config
	var notifiedCurrent Config
	listenerID := store.AddListener(func(old, current Config) {
		notifiedOld = old
		notifiedCurrent = current
	})
	candidate := store.Get()
	candidate.Authentication.Bootstrap.DevelopmentMode = false
	candidate.Server.PublicURL = "https://proctor.example.edu"
	old, current, err := store.Set(context.Background(), candidate)
	if err != nil {
		t.Fatalf("Set() error = %v", err)
	}

	if old.Server.PublicURL == current.Server.PublicURL {
		t.Fatal("Set did not return distinct old and current values")
	}
	if notifiedOld.Server.PublicURL != old.Server.PublicURL ||
		notifiedCurrent.Server.PublicURL != current.Server.PublicURL {
		t.Fatalf("listener received old=%q current=%q", notifiedOld.Server.PublicURL, notifiedCurrent.Server.PublicURL)
	}
	store.RemoveListener(listenerID)

	data, err := backing.Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "https://proctor.example.edu") {
		t.Fatalf("persisted configuration = %s", data)
	}
}

func TestEnvironmentOverridesAreEffectiveButNeverPersisted(t *testing.T) {
	t.Parallel()

	initial := Default()
	initial.Authentication.Bootstrap.DevelopmentMode = false
	initial.Server.ListenAddress = "127.0.0.1:8000"
	data, err := json.Marshal(initial)
	if err != nil {
		t.Fatal(err)
	}
	backing := NewMemoryStore(data)
	environment := map[string]string{
		"PROCTOR_SERVER_LISTEN_ADDRESS":                              "127.0.0.1:9000",
		"PROCTOR_LOG_LEVEL":                                          "debug",
		"PROCTOR_DATABASE_DATA_SOURCE":                               "postgres://runtime:secret@db.example/proctor?sslmode=require",
		"PROCTOR_CACHE_REDIS_PASSWORD":                               "runtime-cache-secret",
		"PROCTOR_CLUSTER_NODE_ID":                                    "runtime-node",
		"PROCTOR_MAIL_SMTP_PASSWORD":                                 "runtime-mail-secret",
		"PROCTOR_VFS_S3_SECRET_KEY":                                  "runtime-vfs-secret",
		"PROCTOR_AUTHENTICATION_RECENT_AUTHENTICATION_TTL":           "10m",
		"PROCTOR_AUTHENTICATION_ACCOUNT_RECOVERY_PASSWORD_RESET_TTL": "30m",
		"PROCTOR_AUTHENTICATION_MFA_ENABLED":                         "true",
		"PROCTOR_AUTHENTICATION_MFA_ENCRYPTION_KEY": base64.StdEncoding.
			EncodeToString(make([]byte, 32)),
		"PROCTOR_AUTHENTICATION_MFA_DECRYPTION_KEYS": base64.StdEncoding.
			EncodeToString([]byte("01234567890123456789012345678901")),
		"PROCTOR_AUTHENTICATION_MFA_SETUP_TTL":           "15m",
		"PROCTOR_AUTHENTICATION_MFA_RECOVERY_CODE_COUNT": "12",
	}
	store, err := NewStore(context.Background(), backing, StoreOptions{
		LookupEnv: func(key string) (string, bool) {
			value, ok := environment[key]
			return value, ok
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })

	effective := store.Get()
	if effective.Server.ListenAddress != "127.0.0.1:9000" {
		t.Fatalf("effective listen address = %q", effective.Server.ListenAddress)
	}
	if effective.Log.Targets[0].Level != "debug" {
		t.Fatalf("effective log level = %q", effective.Log.Targets[0].Level)
	}
	if effective.Database.DataSource != environment["PROCTOR_DATABASE_DATA_SOURCE"] {
		t.Fatalf("effective database data source = %q", effective.Database.DataSource)
	}
	if effective.Cluster.NodeID != environment["PROCTOR_CLUSTER_NODE_ID"] {
		t.Fatalf("effective cluster node ID = %q", effective.Cluster.NodeID)
	}
	if effective.Cache.Redis.Password != environment["PROCTOR_CACHE_REDIS_PASSWORD"] ||
		effective.Mail.SMTP.Password != environment["PROCTOR_MAIL_SMTP_PASSWORD"] ||
		effective.VFS.S3.SecretKey != environment["PROCTOR_VFS_S3_SECRET_KEY"] {
		t.Fatal("effective configuration did not apply infrastructure secrets")
	}
	if effective.Authentication.RecentAuthenticationTTL.Duration != 10*time.Minute ||
		effective.Authentication.AccountRecovery.PasswordResetTTL.Duration !=
			30*time.Minute {
		t.Fatal("effective configuration did not apply authentication durations")
	}
	if !effective.Authentication.MFA.Enabled ||
		effective.Authentication.MFA.EncryptionKey !=
			environment["PROCTOR_AUTHENTICATION_MFA_ENCRYPTION_KEY"] ||
		len(effective.Authentication.MFA.DecryptionKeys) != 1 ||
		effective.Authentication.MFA.SetupTTL.Duration != 15*time.Minute ||
		effective.Authentication.MFA.RecoveryCodeCount != 12 {
		t.Fatal("effective configuration did not apply MFA settings")
	}
	effective.Authentication.Bootstrap.DevelopmentMode = false
	effective.Server.PublicURL = "https://proctor.example.edu"
	if _, _, err := store.Set(context.Background(), effective); err != nil {
		t.Fatal(err)
	}

	persisted := store.GetPersisted()
	if persisted.Server.ListenAddress != "127.0.0.1:8000" {
		t.Fatalf("environment value was persisted: %q", persisted.Server.ListenAddress)
	}
	if persisted.Log.Targets[0].Level != "info" {
		t.Fatalf("environment log level was persisted: %q", persisted.Log.Targets[0].Level)
	}
	if persisted.Database.DataSource != initial.Database.DataSource {
		t.Fatalf("environment database data source was persisted: %q", persisted.Database.DataSource)
	}
	if persisted.Cluster.NodeID != initial.Cluster.NodeID {
		t.Fatalf("environment cluster node ID was persisted: %q", persisted.Cluster.NodeID)
	}
	if persisted.Cache.Redis.Password != "" ||
		persisted.Mail.SMTP.Password != "" ||
		persisted.VFS.S3.SecretKey != "" {
		t.Fatal("environment infrastructure secrets were persisted")
	}
	if persisted.Authentication.RecentAuthenticationTTL !=
		initial.Authentication.RecentAuthenticationTTL ||
		persisted.Authentication.AccountRecovery.PasswordResetTTL !=
			initial.Authentication.AccountRecovery.PasswordResetTTL {
		t.Fatal("environment authentication durations were persisted")
	}
	if persisted.Authentication.MFA.Enabled ||
		persisted.Authentication.MFA.EncryptionKey != "" ||
		len(persisted.Authentication.MFA.DecryptionKeys) != 0 {
		t.Fatal("environment MFA settings were persisted")
	}
	if persisted.Server.PublicURL != "https://proctor.example.edu" {
		t.Fatalf("non-overridden change was lost: %q", persisted.Server.PublicURL)
	}
}

func TestPersonalAccessTokenEnvironmentOverridesAreNeverPersisted(t *testing.T) {
	t.Parallel()

	initial := Default()
	data, err := json.Marshal(initial)
	if err != nil {
		t.Fatal(err)
	}
	environment := map[string]string{
		"PROCTOR_AUTHENTICATION_PERSONAL_ACCESS_TOKENS_MINIMUM_LIFETIME":          "2h",
		"PROCTOR_AUTHENTICATION_PERSONAL_ACCESS_TOKENS_MAXIMUM_LIFETIME":          "4320h",
		"PROCTOR_AUTHENTICATION_PERSONAL_ACCESS_TOKENS_LAST_USED_UPDATE_INTERVAL": "10m",
		"PROCTOR_AUTHENTICATION_PERSONAL_ACCESS_TOKENS_MAXIMUM_PER_USER":          "75",
	}
	store, err := NewStore(
		context.Background(),
		NewMemoryStore(data),
		StoreOptions{LookupEnv: func(key string) (string, bool) {
			value, ok := environment[key]
			return value, ok
		}},
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })

	effective := store.Get()
	if effective.Authentication.PersonalAccessTokens.MinimumLifetime.Duration != 2*time.Hour ||
		effective.Authentication.PersonalAccessTokens.MaximumLifetime.Duration != 180*24*time.Hour ||
		effective.Authentication.PersonalAccessTokens.LastUsedUpdateInterval.Duration != 10*time.Minute ||
		effective.Authentication.PersonalAccessTokens.MaximumPerUser != 75 {
		t.Fatalf(
			"effective personal access token settings = %#v",
			effective.Authentication.PersonalAccessTokens,
		)
	}
	effective.Authentication.Bootstrap.DevelopmentMode = false
	effective.Server.PublicURL = "https://proctor.example.edu"
	if _, _, err := store.Set(context.Background(), effective); err != nil {
		t.Fatal(err)
	}

	persisted := store.GetPersisted()
	if !reflect.DeepEqual(
		persisted.Authentication.PersonalAccessTokens,
		initial.Authentication.PersonalAccessTokens,
	) {
		t.Fatalf(
			"persisted personal access token settings = %#v, want %#v",
			persisted.Authentication.PersonalAccessTokens,
			initial.Authentication.PersonalAccessTokens,
		)
	}
	if persisted.Server.PublicURL != "https://proctor.example.edu" {
		t.Fatalf("non-overridden public URL = %q", persisted.Server.PublicURL)
	}
}

func TestStoreReevaluatesEnvironmentAfterProtectingPriorOverrides(t *testing.T) {
	t.Parallel()

	initial := Default()
	initial.Authentication.Bootstrap.DevelopmentMode = false
	initial.Server.ListenAddress = "127.0.0.1:8000"
	data, err := json.Marshal(initial)
	if err != nil {
		t.Fatal(err)
	}
	environment := map[string]string{
		"PROCTOR_SERVER_LISTEN_ADDRESS": "127.0.0.1:9000",
		"PROCTOR_SERVER_READ_TIMEOUT":   "40s",
	}
	store, err := NewStore(
		context.Background(),
		NewMemoryStore(data),
		StoreOptions{LookupEnv: func(key string) (string, bool) {
			value, ok := environment[key]
			return value, ok
		}},
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })

	candidate := store.Get()
	candidate.Server.WriteTimeout.Duration = 45 * time.Second
	delete(environment, "PROCTOR_SERVER_LISTEN_ADDRESS")
	environment["PROCTOR_SERVER_READ_TIMEOUT"] = "50s"
	environment["PROCTOR_SERVER_PUBLIC_URL"] = "https://environment.example.edu"
	if _, _, err := store.Set(context.Background(), candidate); err != nil {
		t.Fatal(err)
	}

	persisted := store.GetPersisted()
	if persisted.Server.ListenAddress != initial.Server.ListenAddress {
		t.Fatalf("persisted listen address = %q", persisted.Server.ListenAddress)
	}
	if persisted.Server.PublicURL != initial.Server.PublicURL {
		t.Fatalf("persisted public URL = %q", persisted.Server.PublicURL)
	}
	if persisted.Server.ReadTimeout != initial.Server.ReadTimeout {
		t.Fatalf("persisted read timeout = %s", persisted.Server.ReadTimeout.Duration)
	}
	if persisted.Server.WriteTimeout.Duration != 45*time.Second {
		t.Fatalf("persisted write timeout = %s", persisted.Server.WriteTimeout.Duration)
	}
	effective := store.Get()
	if effective.Server.ListenAddress != initial.Server.ListenAddress ||
		effective.Server.PublicURL != environment["PROCTOR_SERVER_PUBLIC_URL"] ||
		effective.Server.ReadTimeout.Duration != 50*time.Second {
		t.Fatalf("effective server configuration = %#v", effective.Server)
	}
	overrides := store.EnvironmentOverrides()
	wantOverrides := []string{
		"PROCTOR_SERVER_PUBLIC_URL",
		"PROCTOR_SERVER_READ_TIMEOUT",
	}
	if !reflect.DeepEqual(overrides, wantOverrides) {
		t.Fatalf("environment overrides = %v", overrides)
	}
	overrides[0] = "mutated"
	if !reflect.DeepEqual(
		store.EnvironmentOverrides(),
		wantOverrides,
	) {
		t.Fatal("EnvironmentOverrides exposed mutable store state")
	}
}

func TestSetFailuresDoNotPublishPartialState(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		environment map[string]string
		candidate   func(*Config)
		failSave    bool
	}{
		{
			name:      "candidate validation",
			candidate: func(candidate *Config) { candidate.Server.ReadTimeout.Duration = 0 },
		},
		{
			name: "effective validation",
			environment: map[string]string{
				"PROCTOR_SERVER_MAX_HEADER_BYTES": "0",
			},
		},
		{
			name:     "backing save",
			failSave: true,
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			environment := map[string]string{}
			initialData, err := json.Marshal(Default())
			if err != nil {
				t.Fatal(err)
			}
			memory := NewMemoryStore(initialData)
			var backing BackingStore = memory
			if test.failSave {
				backing = failingSaveBacking{BackingStore: memory}
			}
			store, err := NewStore(
				context.Background(),
				backing,
				StoreOptions{LookupEnv: func(key string) (string, bool) {
					value, ok := environment[key]
					return value, ok
				}},
			)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = store.Close() })
			for key, value := range test.environment {
				environment[key] = value
			}

			beforePersisted := store.GetPersisted()
			beforeEffective := store.Get()
			var notifications atomic.Int32
			store.AddListener(func(Config, Config) { notifications.Add(1) })
			candidate := store.Get()
			candidate.Server.PublicURL = "https://should-not-persist.example.edu"
			if test.candidate != nil {
				test.candidate(&candidate)
			}
			if _, _, err := store.Set(context.Background(), candidate); err == nil {
				t.Fatal("Set succeeded")
			}

			if !reflect.DeepEqual(store.GetPersisted(), beforePersisted) {
				t.Fatal("failed Set changed persisted configuration")
			}
			if !reflect.DeepEqual(store.Get(), beforeEffective) {
				t.Fatal("failed Set changed effective configuration")
			}
			if notifications.Load() != 0 {
				t.Fatalf("failed Set sent %d listener notifications", notifications.Load())
			}
			data, err := memory.Load(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(data, initialData) {
				t.Fatal("failed Set changed backing configuration")
			}
		})
	}
	// Config is a closed concrete JSON shape whose marshalers have no
	// data-dependent error outcome, so encoding has no inducible failure case.
}

func TestInvalidEnvironmentDuringSetDoesNotPublishPartialState(t *testing.T) {
	t.Parallel()

	environment := map[string]string{}
	initialData, err := json.Marshal(Default())
	if err != nil {
		t.Fatal(err)
	}
	backing := NewMemoryStore(initialData)
	store, err := NewStore(
		context.Background(),
		backing,
		StoreOptions{LookupEnv: func(key string) (string, bool) {
			value, ok := environment[key]
			return value, ok
		}},
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })

	beforePersisted := store.GetPersisted()
	beforeEffective := store.Get()
	var notifications atomic.Int32
	store.AddListener(func(Config, Config) {
		notifications.Add(1)
	})
	environment["PROCTOR_SERVER_MAX_HEADER_BYTES"] = "invalid"
	candidate := store.Get()
	candidate.Server.PublicURL = "https://should-not-persist.example.edu"
	if _, _, err := store.Set(context.Background(), candidate); err == nil {
		t.Fatal("Set accepted an invalid environment value")
	}

	if !reflect.DeepEqual(store.GetPersisted(), beforePersisted) {
		t.Fatal("failed Set changed persisted configuration")
	}
	if !reflect.DeepEqual(store.Get(), beforeEffective) {
		t.Fatal("failed Set changed effective configuration")
	}
	if notifications.Load() != 0 {
		t.Fatalf("failed Set sent %d listener notifications", notifications.Load())
	}
	data, err := backing.Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var saved Config
	if err := json.Unmarshal(data, &saved); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(saved, beforePersisted) {
		t.Fatal("failed Set changed backing configuration")
	}
}

func TestRedactedConfigurationHidesInfrastructureCredentials(t *testing.T) {
	t.Parallel()

	cfg := Default()
	cfg.Database.DataSource = "postgres://proctor:secret@db.example/proctor?sslmode=require"
	cfg.Cache.Redis.Password = "cache-secret"
	cfg.Cluster.Memberlist.EncryptionKey = "dGVzdC1tZW1iZXJsaXN0LWtleS0zMmJ5dGVzISE="
	cfg.Cluster.Memberlist.DecryptionKeys = []string{base64.StdEncoding.
		EncodeToString([]byte("cluster-fallback-key-material-32b"))}
	cfg.Mail.SMTP.Password = "mail-secret"
	cfg.VFS.S3.AccessKey = "vfs-access-key"
	cfg.VFS.S3.SecretKey = "vfs-secret-key"
	cfg.VFS.S3.SessionToken = "vfs-session-token"
	cfg.Authentication.MFA.EncryptionKey = base64.StdEncoding.
		EncodeToString([]byte("01234567890123456789012345678901"))
	cfg.Authentication.MFA.DecryptionKeys = []string{base64.StdEncoding.
		EncodeToString([]byte("abcdefghijklmnopqrstuvwxyzABCDEF"))}
	data, err := cfg.RedactedJSON()
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{
		"cache-secret",
		"dGVzdC1tZW1iZXJsaXN0LWtleS0zMmJ5dGVzISE=",
		cfg.Cluster.Memberlist.DecryptionKeys[0],
		"mail-secret",
		"db.example",
		"vfs-access-key",
		"vfs-secret-key",
		"vfs-session-token",
		cfg.Authentication.MFA.EncryptionKey,
		cfg.Authentication.MFA.DecryptionKeys[0],
	} {
		if strings.Contains(string(data), forbidden) {
			t.Fatalf("redacted configuration exposed %q: %s", forbidden, data)
		}
	}
	if cfg.Database.DataSource == "[redacted]" {
		t.Fatal("Redacted mutated the original configuration")
	}
}

func TestInfrastructureAndAuthenticationValidationIsAggregated(t *testing.T) {
	t.Parallel()

	cfg := Default()
	cfg.Cache.Backend = "redis"
	cfg.Cache.Redis.Addresses = []string{"missing-port"}
	cfg.Mail.Enabled = true
	cfg.Mail.FromAddress = "not-an-address"
	cfg.Mail.SMTP.Authentication = "plain"
	cfg.Mail.SMTP.Security = "none"
	cfg.Mail.SMTP.Username = ""
	cfg.VFS.Backend = "s3"
	cfg.VFS.S3.Endpoint = ""
	cfg.VFS.S3.Bucket = ""
	cfg.Authentication.Password.ArgonMemoryKiB = 1
	cfg.Authentication.MFA.Enabled = true
	cfg.Authentication.MFA.EncryptionKey = "not-base64"
	cfg.Authentication.MFA.RecoveryCodeCount = 2
	cfg.Authentication.Sessions.AccessTTL.Duration = cfg.Authentication.Sessions.IdleTTL.Duration + time.Second
	cfg.Cluster.Backend = "memberlist"
	cfg.Cluster.NodeID = "invalid node"
	cfg.Cluster.Memberlist.EncryptionKey = ""

	err := cfg.Validate()
	var validationError *ValidationError
	if !errors.As(err, &validationError) {
		t.Fatalf("Validate() error = %v, want ValidationError", err)
	}
	if len(validationError.Fields) < 10 {
		t.Fatalf("Validate() fields = %#v, want aggregate infrastructure failures", validationError.Fields)
	}
}

func TestDatabaseRequiresTwoConnectionsForLockedMigrations(t *testing.T) {
	t.Parallel()

	cfg := Default()
	cfg.Database.MaxOpenConnections = 1
	cfg.Database.MaxIdleConnections = 1
	err := cfg.Validate()
	var validationError *ValidationError
	if !errors.As(err, &validationError) {
		t.Fatalf("Validate() error = %v, want ValidationError", err)
	}
	for _, field := range validationError.Fields {
		if field.Field == "database.max_open_connections" {
			return
		}
	}
	t.Fatalf("Validate() fields = %#v, want database connection minimum", validationError.Fields)
}

func TestMemberlistClusterRequiresRedisAndSharedVFS(t *testing.T) {
	t.Parallel()

	cfg := Default()
	cfg.Cluster.Backend = "memberlist"
	cfg.Cluster.Memberlist.EncryptionKey = base64.StdEncoding.EncodeToString(make([]byte, 32))
	err := cfg.Validate()
	var validationError *ValidationError
	if !errors.As(err, &validationError) {
		t.Fatalf("Validate() error = %v, want ValidationError", err)
	}
	fields := make(map[string]string, len(validationError.Fields))
	for _, field := range validationError.Fields {
		fields[field.Field] = field.Message
	}
	if fields["cache.backend"] == "" {
		t.Fatalf("cluster cache field = %#v, want Redis requirement", validationError.Fields)
	}
	if fields["vfs.backend"] == "" {
		t.Fatalf("cluster shared-infrastructure fields = %#v", validationError.Fields)
	}
}

func TestRedisClusterBackendIsRejected(t *testing.T) {
	t.Parallel()

	cfg := Default()
	cfg.Cluster.Backend = "redis"
	err := cfg.Validate()
	var validationError *ValidationError
	if !errors.As(err, &validationError) {
		t.Fatalf("Validate() error = %v, want ValidationError", err)
	}
	found := false
	for _, field := range validationError.Fields {
		if field.Field == "cluster.backend" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("Validate() fields = %#v, want cluster.backend rejection", validationError.Fields)
	}
}

func TestReloadPublishesExternalBackingChanges(t *testing.T) {
	t.Parallel()

	backing := NewMemoryStore(nil)
	store, err := NewStore(context.Background(), backing, StoreOptions{LookupEnv: noEnvironment})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })

	changed := make(chan Config, 1)
	store.AddListener(func(_, current Config) {
		changed <- current
	})
	external := Default()
	external.Server.ReadTimeout.Duration = 45 * time.Second
	data, err := json.Marshal(external)
	if err != nil {
		t.Fatal(err)
	}
	if err := backing.Save(context.Background(), data); err != nil {
		t.Fatal(err)
	}
	if err := store.Reload(context.Background()); err != nil {
		t.Fatal(err)
	}

	select {
	case current := <-changed:
		if current.Server.ReadTimeout.Duration != 45*time.Second {
			t.Fatalf("listener timeout = %s", current.Server.ReadTimeout.Duration)
		}
	case <-time.After(time.Second):
		t.Fatal("reload listener was not called")
	}
}

func TestListenerCanSafelyPerformAnotherConfigurationChange(t *testing.T) {
	t.Parallel()

	store, err := NewStore(context.Background(), NewMemoryStore(nil), StoreOptions{LookupEnv: noEnvironment})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })

	var nested atomic.Bool
	store.AddListener(func(_, current Config) {
		if !nested.CompareAndSwap(false, true) {
			return
		}
		current.Authentication.Bootstrap.DevelopmentMode = false
		current.Server.PublicURL = "https://nested.example.edu"
		if _, _, err := store.Set(context.Background(), current); err != nil {
			t.Errorf("nested Set() error = %v", err)
		}
	})

	done := make(chan error, 1)
	go func() {
		cfg := store.Get()
		cfg.Server.ReadTimeout.Duration = 40 * time.Second
		_, _, err := store.Set(context.Background(), cfg)
		done <- err
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("listener-triggered Set deadlocked")
	}
	if store.Get().Server.PublicURL != "https://nested.example.edu" {
		t.Fatal("nested configuration change was not applied")
	}
}

func TestStoreRejectsUnknownAndInvalidConfiguration(t *testing.T) {
	t.Parallel()

	_, err := NewStore(
		context.Background(),
		NewMemoryStore([]byte(`{"Version":1,"mystery":true}`)),
		StoreOptions{LookupEnv: noEnvironment},
	)
	if err == nil || !strings.Contains(err.Error(), `unknown field "mystery"`) {
		t.Fatalf("NewStore() error = %v", err)
	}

	store, err := NewStore(context.Background(), NewMemoryStore(nil), StoreOptions{LookupEnv: noEnvironment})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	invalid := store.Get()
	invalid.Server.ReadTimeout.Duration = 0
	invalid.Log.Targets[0].Format = "xml"
	_, _, err = store.Set(context.Background(), invalid)
	var validationError *ValidationError
	if !errors.As(err, &validationError) || len(validationError.Fields) != 2 {
		t.Fatalf("Set() error = %v, want two validation fields", err)
	}
}

func TestDiffReportsStableConfigurationPaths(t *testing.T) {
	t.Parallel()

	old := Default()
	current := old.Clone()
	current.Server.PublicURL = "https://proctor.example.edu"
	current.Log.Targets[0].Level = "debug"

	changes := Diff(old, current)
	want := []Change{{Path: "Log.Targets"}, {Path: "Server.PublicURL"}}
	if !reflect.DeepEqual(changes, want) {
		t.Fatalf("Diff() = %#v, want %#v", changes, want)
	}
}

func noEnvironment(string) (string, bool) {
	return "", false
}

type failingSaveBacking struct {
	BackingStore
}

func (failingSaveBacking) Save(context.Context, []byte) error {
	return errors.New("save failed")
}
