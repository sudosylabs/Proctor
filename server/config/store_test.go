// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package config

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStoreLoadsDefaultsAndReturnsClones(t *testing.T) {
	t.Parallel()

	store, err := NewStore(context.Background(), NewMemoryStore(nil), StoreOptions{LookupEnv: noEnvironment})
	require.NoError(t, err)
	t.Cleanup(func() { _ = store.Close() })

	got := store.Get()
	require.Equal(t, Default(), got, "Get()")
	got.Log.Targets[0].Level = "error"
	got.Cache.Redis.Addresses[0] = "mutated:6379"
	got.Authentication.MFA.DecryptionKeys = []string{"mutated"}
	require.Equal(t, "info", store.Get().Log.Targets[0].Level, "Get exposed mutable store state")
	require.NotEqual(t, "mutated:6379", store.Get().Cache.Redis.Addresses[0], "Get exposed mutable cache address state")
	require.Empty(t, store.Get().Authentication.MFA.DecryptionKeys, "Get exposed mutable MFA decryption keys")
}

func TestStoreSetPersistsAndNotifies(t *testing.T) {
	t.Parallel()

	backing := NewMemoryStore(nil)
	store, err := NewStore(context.Background(), backing, StoreOptions{LookupEnv: noEnvironment})
	require.NoError(t, err)
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
	require.NoError(t, err)

	require.NotEqual(t, current.Server.PublicURL, old.Server.PublicURL, "Set did not return distinct old and current values")
	require.Equal(t, old.Server.PublicURL, notifiedOld.Server.PublicURL, "listener received old")
	require.Equal(t, current.Server.PublicURL, notifiedCurrent.Server.PublicURL, "listener received old")
	store.RemoveListener(listenerID)

	data, err := backing.Load(context.Background())
	require.NoError(t, err)
	require.Contains(t, string(data), "https://proctor.example.edu", "persisted configuration")
}

func TestEnvironmentOverridesAreEffectiveButNeverPersisted(t *testing.T) {
	t.Parallel()

	initial := Default()
	initial.Authentication.Bootstrap.DevelopmentMode = false
	initial.Server.ListenAddress = "127.0.0.1:8000"
	data, err := json.Marshal(initial)
	require.NoError(t, err)
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
	require.NoError(t, err)
	t.Cleanup(func() { _ = store.Close() })

	effective := store.Get()
	require.Equal(t, "127.0.0.1:9000", effective.Server.ListenAddress)
	require.Equal(t, "debug", effective.Log.Targets[0].Level)
	require.Equal(t, environment["PROCTOR_DATABASE_DATA_SOURCE"], effective.Database.DataSource)
	require.Equal(t, environment["PROCTOR_CLUSTER_NODE_ID"], effective.Cluster.NodeID)
	require.Equal(t, environment["PROCTOR_CACHE_REDIS_PASSWORD"], effective.Cache.Redis.Password, "effective configuration did not apply infrastructure secrets")
	require.Equal(t, environment["PROCTOR_MAIL_SMTP_PASSWORD"], effective.Mail.SMTP.Password, "effective configuration did not apply infrastructure secrets")
	require.Equal(t, environment["PROCTOR_VFS_S3_SECRET_KEY"], effective.VFS.S3.SecretKey, "effective configuration did not apply infrastructure secrets")
	require.Equal(t, 10*time.Minute, effective.Authentication.RecentAuthenticationTTL.Duration, "effective configuration did not apply authentication durations")
	require.Equal(t, 30*time.Minute, effective.Authentication.AccountRecovery.PasswordResetTTL.Duration, "effective configuration did not apply authentication durations")
	require.True(t, effective.Authentication.MFA.Enabled, "effective configuration did not apply MFA settings")
	require.Equal(t, environment["PROCTOR_AUTHENTICATION_MFA_ENCRYPTION_KEY"], effective.Authentication.MFA.EncryptionKey, "effective configuration did not apply MFA settings")
	require.Len(t, effective.Authentication.MFA.DecryptionKeys, 1, "effective configuration did not apply MFA settings")
	require.Equal(t, 15*time.Minute, effective.Authentication.MFA.SetupTTL.Duration, "effective configuration did not apply MFA settings")
	require.Equal(t, 12, effective.Authentication.MFA.RecoveryCodeCount, "effective configuration did not apply MFA settings")
	effective.Authentication.Bootstrap.DevelopmentMode = false
	effective.Server.PublicURL = "https://proctor.example.edu"
	_, _, err = store.Set(context.Background(), effective)
	require.NoError(t, err)

	persisted := store.GetPersisted()
	require.Equal(t, "127.0.0.1:8000", persisted.Server.ListenAddress)
	require.Equal(t, "info", persisted.Log.Targets[0].Level)
	require.Equal(t, initial.Database.DataSource, persisted.Database.DataSource)
	require.Equal(t, initial.Cluster.NodeID, persisted.Cluster.NodeID)
	require.Empty(t, persisted.Cache.Redis.Password, "environment infrastructure secrets were persisted")
	require.Empty(t, persisted.Mail.SMTP.Password, "environment infrastructure secrets were persisted")
	require.Empty(t, persisted.VFS.S3.SecretKey, "environment infrastructure secrets were persisted")
	require.Equal(t, initial.Authentication.RecentAuthenticationTTL, persisted.Authentication.RecentAuthenticationTTL, "environment authentication durations were persisted")
	require.Equal(t, initial.Authentication.AccountRecovery.PasswordResetTTL, persisted.Authentication.AccountRecovery.PasswordResetTTL, "environment authentication durations were persisted")
	require.False(t, persisted.Authentication.MFA.Enabled, "environment MFA settings were persisted")
	require.Empty(t, persisted.Authentication.MFA.EncryptionKey, "environment MFA settings were persisted")
	require.Empty(t, persisted.Authentication.MFA.DecryptionKeys, "environment MFA settings were persisted")
	require.Equal(t, "https://proctor.example.edu", persisted.Server.PublicURL)
}

func TestPersonalAccessTokenEnvironmentOverridesAreNeverPersisted(t *testing.T) {
	t.Parallel()

	initial := Default()
	data, err := json.Marshal(initial)
	require.NoError(t, err)
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
	require.NoError(t, err)
	t.Cleanup(func() { _ = store.Close() })

	effective := store.Get()
	require.Equal(t, 2*time.Hour, effective.Authentication.PersonalAccessTokens.MinimumLifetime.Duration)
	require.Equal(t, 180*24*time.Hour, effective.Authentication.PersonalAccessTokens.MaximumLifetime.Duration)
	require.Equal(t, 10*time.Minute, effective.Authentication.PersonalAccessTokens.LastUsedUpdateInterval.Duration)
	require.Equal(t, 75, effective.Authentication.PersonalAccessTokens.MaximumPerUser)
	effective.Authentication.Bootstrap.DevelopmentMode = false
	effective.Server.PublicURL = "https://proctor.example.edu"
	_, _, err = store.Set(context.Background(), effective)
	require.NoError(t, err)

	persisted := store.GetPersisted()
	require.Equal(t, initial.Authentication.PersonalAccessTokens, persisted.Authentication.PersonalAccessTokens, "persisted personal access token settings")
	require.Equal(t, "https://proctor.example.edu", persisted.Server.PublicURL)
}

func TestStoreReevaluatesEnvironmentAfterProtectingPriorOverrides(t *testing.T) {
	t.Parallel()

	initial := Default()
	initial.Authentication.Bootstrap.DevelopmentMode = false
	initial.Server.ListenAddress = "127.0.0.1:8000"
	data, err := json.Marshal(initial)
	require.NoError(t, err)
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
	require.NoError(t, err)
	t.Cleanup(func() { _ = store.Close() })

	candidate := store.Get()
	candidate.Server.WriteTimeout.Duration = 45 * time.Second
	delete(environment, "PROCTOR_SERVER_LISTEN_ADDRESS")
	environment["PROCTOR_SERVER_READ_TIMEOUT"] = "50s"
	environment["PROCTOR_SERVER_PUBLIC_URL"] = "https://environment.example.edu"
	_, _, err = store.Set(context.Background(), candidate)
	require.NoError(t, err)

	persisted := store.GetPersisted()
	require.Equal(t, initial.Server.ListenAddress, persisted.Server.ListenAddress)
	require.Equal(t, initial.Server.PublicURL, persisted.Server.PublicURL)
	require.Equal(t, initial.Server.ReadTimeout, persisted.Server.ReadTimeout, "persisted read timeout = %s", persisted.Server.ReadTimeout.Duration)
	require.Equal(t, 45*time.Second, persisted.Server.WriteTimeout.Duration)
	effective := store.Get()
	require.Equal(t, initial.Server.ListenAddress, effective.Server.ListenAddress)
	require.Equal(t, environment["PROCTOR_SERVER_PUBLIC_URL"], effective.Server.PublicURL)
	require.Equal(t, 50*time.Second, effective.Server.ReadTimeout.Duration)
	overrides := store.EnvironmentOverrides()
	wantOverrides := []string{
		"PROCTOR_SERVER_PUBLIC_URL",
		"PROCTOR_SERVER_READ_TIMEOUT",
	}
	require.Equal(t, wantOverrides, overrides)
	overrides[0] = "mutated"
	require.Equal(t, wantOverrides, store.EnvironmentOverrides(), "EnvironmentOverrides exposed mutable store state")
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
			require.NoError(t, err)
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
			require.NoError(t, err)
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
			_, _, err = store.Set(context.Background(), candidate)
			require.Error(t, err, "Set succeeded")

			require.Equal(t, beforePersisted, store.GetPersisted(), "failed Set changed persisted configuration")
			require.Equal(t, beforeEffective, store.Get(), "failed Set changed effective configuration")
			require.Equal(t, int32(0), notifications.Load(), "failed Set sent")
			data, err := memory.Load(context.Background())
			require.NoError(t, err)
			require.True(t, reflect.DeepEqual(data, initialData), "failed Set changed backing configuration")
		})
	}
	// Config is a closed concrete JSON shape whose marshalers have no
	// data-dependent error outcome, so encoding has no inducible failure case.
}

func TestInvalidEnvironmentDuringSetDoesNotPublishPartialState(t *testing.T) {
	t.Parallel()

	environment := map[string]string{}
	initialData, err := json.Marshal(Default())
	require.NoError(t, err)
	backing := NewMemoryStore(initialData)
	store, err := NewStore(
		context.Background(),
		backing,
		StoreOptions{LookupEnv: func(key string) (string, bool) {
			value, ok := environment[key]
			return value, ok
		}},
	)
	require.NoError(t, err)
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
	_, _, err = store.Set(context.Background(), candidate)
	require.Error(t, err, "Set accepted an invalid environment value")

	require.Equal(t, beforePersisted, store.GetPersisted(), "failed Set changed persisted configuration")
	require.Equal(t, beforeEffective, store.Get(), "failed Set changed effective configuration")
	require.Equal(t, int32(0), notifications.Load(), "failed Set sent")
	data, err := backing.Load(context.Background())
	require.NoError(t, err)
	var saved Config
	require.NoError(t, json.Unmarshal(data, &saved))
	require.Equal(t, beforePersisted, saved, "failed Set changed backing configuration")
}

func TestRedactedConfigurationHidesInfrastructureCredentials(t *testing.T) {
	t.Parallel()

	cfg := Default()
	cfg.Database.DataSource = "postgres://proctor:secret@db.example/proctor?sslmode=require"
	cfg.Cache.Redis.Password = "cache-secret"
	cfg.Metrics.BearerToken = "metrics-secret-must-be-at-least-32-bytes"
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
	require.NoError(t, err)
	for _, forbidden := range []string{
		"cache-secret",
		"metrics-secret-must-be-at-least-32-bytes",
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
		require.NotContains(t, string(data), forbidden, "redacted configuration exposed")
	}
	require.NotEqual(t, "[redacted]", cfg.Database.DataSource, "Redacted mutated the original configuration")
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
	require.ErrorAs(t, err, &validationError, "Validate() error")
	require.GreaterOrEqual(t, len(validationError.Fields), 10, "Validate() fields")
}

func TestDatabaseRequiresTwoConnectionsForLockedMigrations(t *testing.T) {
	t.Parallel()

	cfg := Default()
	cfg.Database.MaxOpenConnections = 1
	cfg.Database.MaxIdleConnections = 1
	err := cfg.Validate()
	var validationError *ValidationError
	require.ErrorAs(t, err, &validationError, "Validate() error")
	for _, field := range validationError.Fields {
		if field.Field == "database.max_open_connections" {
			return
		}
	}
	require.FailNow(t, fmt.Sprintf("Validate() fields = %#v, want database connection minimum", validationError.Fields))
}

func TestMemberlistClusterRequiresRedisAndSharedVFS(t *testing.T) {
	t.Parallel()

	cfg := Default()
	cfg.Cluster.Backend = "memberlist"
	cfg.Cluster.Memberlist.EncryptionKey = base64.StdEncoding.EncodeToString(make([]byte, 32))
	err := cfg.Validate()
	var validationError *ValidationError
	require.ErrorAs(t, err, &validationError, "Validate() error")
	fields := make(map[string]string, len(validationError.Fields))
	for _, field := range validationError.Fields {
		fields[field.Field] = field.Message
	}
	require.NotEmpty(t, fields["cache.backend"], "cluster cache field = %#v, want Redis requirement", validationError.Fields)
	require.NotEmpty(t, fields["vfs.backend"], "cluster shared-infrastructure fields = %#v", validationError.Fields)
}

func TestRedisClusterBackendIsRejected(t *testing.T) {
	t.Parallel()

	cfg := Default()
	cfg.Cluster.Backend = "redis"
	err := cfg.Validate()
	var validationError *ValidationError
	require.ErrorAs(t, err, &validationError, "Validate() error")
	found := false
	for _, field := range validationError.Fields {
		if field.Field == "cluster.backend" {
			found = true
			break
		}
	}
	require.True(t, found, "Validate() fields = %#v, want cluster.backend rejection", validationError.Fields)
}

func TestReloadPublishesExternalBackingChanges(t *testing.T) {
	t.Parallel()

	backing := NewMemoryStore(nil)
	store, err := NewStore(context.Background(), backing, StoreOptions{LookupEnv: noEnvironment})
	require.NoError(t, err)
	t.Cleanup(func() { _ = store.Close() })

	changed := make(chan Config, 1)
	store.AddListener(func(_, current Config) {
		changed <- current
	})
	external := Default()
	external.Server.ReadTimeout.Duration = 45 * time.Second
	data, err := json.Marshal(external)
	require.NoError(t, err)
	require.NoError(t, backing.Save(context.Background(), data))
	require.NoError(t, store.Reload(context.Background()))

	select {
	case current := <-changed:
		require.Equal(t, 45*time.Second, current.Server.ReadTimeout.Duration)
	case <-time.After(time.Second):
		require.FailNow(t, "reload listener was not called")
	}
}

func TestListenerCanSafelyPerformAnotherConfigurationChange(t *testing.T) {
	t.Parallel()

	store, err := NewStore(context.Background(), NewMemoryStore(nil), StoreOptions{LookupEnv: noEnvironment})
	require.NoError(t, err)
	t.Cleanup(func() { _ = store.Close() })

	var nested atomic.Bool
	store.AddListener(func(_, current Config) {
		if !nested.CompareAndSwap(false, true) {
			return
		}
		current.Authentication.Bootstrap.DevelopmentMode = false
		current.Server.PublicURL = "https://nested.example.edu"
		_, _, err := store.Set(context.Background(), current)
		assert.NoError(t, err)
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
		require.NoError(t, err)
	case <-time.After(time.Second):
		require.FailNow(t, "listener-triggered Set deadlocked")
	}
	require.Equal(t, "https://nested.example.edu", store.Get().Server.PublicURL, "nested configuration change was not applied")
}

func TestStoreRejectsUnknownAndInvalidConfiguration(t *testing.T) {
	t.Parallel()

	_, err := NewStore(
		context.Background(),
		NewMemoryStore([]byte(`{"Version":1,"mystery":true}`)),
		StoreOptions{LookupEnv: noEnvironment},
	)
	require.Error(t, err)
	require.Contains(t, err.Error(), `unknown field "mystery"`)

	store, err := NewStore(context.Background(), NewMemoryStore(nil), StoreOptions{LookupEnv: noEnvironment})
	require.NoError(t, err)
	t.Cleanup(func() { _ = store.Close() })
	invalid := store.Get()
	invalid.Server.ReadTimeout.Duration = 0
	invalid.Log.Targets[0].Format = "xml"
	_, _, err = store.Set(context.Background(), invalid)
	var validationError *ValidationError
	require.ErrorAs(t, err, &validationError, "Set() error")
	require.Len(t, validationError.Fields, 2, "Set() error")
}

func TestDiffReportsStableConfigurationPaths(t *testing.T) {
	t.Parallel()

	old := Default()
	current := old.Clone()
	current.Server.PublicURL = "https://proctor.example.edu"
	current.Log.Targets[0].Level = "debug"

	changes := Diff(old, current)
	want := []Change{{Path: "Log.Targets"}, {Path: "Server.PublicURL"}}
	require.Equal(t, want, changes, "Diff()")
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
