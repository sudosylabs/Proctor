// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package config

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"reflect"
	"testing"
)

func TestEnvironmentOverrideCatalogContracts(t *testing.T) {
	if len(environmentOverrideCatalog) != 122 {
		t.Fatalf("environment override definitions = %d, want 122", len(environmentOverrideCatalog))
	}

	seen := make(map[string]struct{}, len(environmentOverrideCatalog))
	for _, definition := range environmentOverrideCatalog {
		definition := definition
		t.Run(definition.key, func(t *testing.T) {
			if _, exists := seen[definition.key]; exists {
				t.Fatalf("environment override %q is duplicated", definition.key)
			}
			seen[definition.key] = struct{}{}

			persisted := Default()
			value := environmentValueThatChanges(t, definition, persisted)
			candidate := persisted.Clone()
			overlay, err := applyEnvironmentCatalog(
				&candidate,
				func(key string) (string, bool) {
					return value, key == definition.key
				},
				[]environmentOverride{definition},
			)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(overlay.keys(), []string{definition.key}) {
				t.Fatalf("applied overrides = %v, want %q", overlay.keys(), definition.key)
			}
			if reflect.DeepEqual(candidate, persisted) {
				t.Fatal("environment override did not change configuration")
			}

			overlay.restore(&candidate, persisted)
			if !reflect.DeepEqual(candidate, persisted) {
				t.Fatal("environment override did not restore persisted configuration")
			}
		})
	}
}

func TestEveryEnvironmentOverrideRoundTripsThroughStoreSet(t *testing.T) {
	for _, definition := range environmentOverrideCatalog {
		definition := definition
		t.Run(definition.key, func(t *testing.T) {
			t.Parallel()

			initial := validBaseForEnvironmentOverride(definition.key)
			data, err := json.Marshal(initial)
			if err != nil {
				t.Fatal(err)
			}
			value := validEnvironmentValue(t, definition, initial)
			store, err := NewStore(
				context.Background(),
				NewMemoryStore(data),
				StoreOptions{LookupEnv: func(key string) (string, bool) {
					return value, key == definition.key
				}},
			)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = store.Close() })

			if !reflect.DeepEqual(store.EnvironmentOverrides(), []string{definition.key}) {
				t.Fatalf("environment overrides = %v", store.EnvironmentOverrides())
			}
			candidate := store.Get()
			if definition.key == "PROCTOR_MAIL_FROM_NAME" {
				candidate.Authentication.Bootstrap.DevelopmentMode = false
				candidate.Server.PublicURL = "https://changed.example.edu"
			} else {
				candidate.Mail.FromName = "Changed by Store.Set"
			}
			if _, _, err := store.Set(context.Background(), candidate); err != nil {
				t.Fatal(err)
			}

			persisted := store.GetPersisted()
			want := initial.Clone()
			if definition.key == "PROCTOR_MAIL_FROM_NAME" {
				want.Authentication.Bootstrap.DevelopmentMode = false
				want.Server.PublicURL = "https://changed.example.edu"
			} else {
				want.Mail.FromName = "Changed by Store.Set"
			}
			if !reflect.DeepEqual(persisted, want) {
				t.Fatal("Store.Set persisted an environment-owned value")
			}
		})
	}
}

func TestEnvironmentOverrideCatalogRejectsDuplicatesWithoutMutation(t *testing.T) {
	catalog := append([]environmentOverride(nil), environmentOverrideCatalog...)
	catalog = append(catalog, environmentOverrideCatalog[0])
	candidate := Default()
	want := candidate.Clone()

	overlay, err := applyEnvironmentCatalog(
		&candidate,
		func(string) (string, bool) { return "changed", true },
		catalog,
	)
	if err == nil {
		t.Fatal("duplicate environment override was accepted")
	}
	if overlay.keys() != nil {
		t.Fatalf("applied overrides = %v, want nil", overlay.keys())
	}
	if !reflect.DeepEqual(candidate, want) {
		t.Fatal("duplicate catalog mutated configuration")
	}
}

func environmentValueThatChanges(
	t *testing.T,
	definition environmentOverride,
	persisted Config,
) string {
	t.Helper()
	for _, value := range []string{
		"changed",
		"2",
		"3",
		"2s",
		"false",
		"true",
		"one.example:1,two.example:2",
	} {
		candidate := persisted.Clone()
		if err := definition.apply(&candidate, value); err == nil &&
			!reflect.DeepEqual(candidate, persisted) {
			return value
		}
	}
	t.Fatalf("no test value changes environment override %q", definition.key)
	return ""
}

func validEnvironmentValue(
	t *testing.T,
	definition environmentOverride,
	base Config,
) string {
	t.Helper()
	values := []string{
		"changed",
		"127.0.0.1:9000",
		"https://environment.example.edu",
		"postgres://proctor@localhost/proctor?sslmode=disable",
		"memory",
		"local",
		"smtp",
		"none",
		"disabled",
		"static",
		"lets_encrypt",
		"localhost",
		"operator@example.edu",
		"./changed",
		"bucket",
		"eu-west-1",
		base64.StdEncoding.EncodeToString(make([]byte, 32)),
		"debug",
		"json",
		"127.0.0.1:6379,127.0.0.2:6379",
		"1s",
		"5s",
		"10s",
		"20s",
		"45s",
		"2m",
		"10m",
		"15m",
		"2h",
		"24h",
		"48h",
		"4320h",
		"1",
		"2",
		"3",
		"10",
		"12",
		"16",
		"32",
		"64",
		"75",
		"1024",
		"65536",
		"16777216",
		"32768",
		"false",
		"true",
	}
	for _, value := range values {
		candidate := base.Clone()
		if err := definition.apply(&candidate, value); err == nil &&
			candidate.Validate() == nil &&
			(!reflect.DeepEqual(candidate, base) ||
				definition.key == "PROCTOR_MAIL_BACKEND") {
			return value
		}
	}
	t.Fatalf("no valid test value for environment override %q", definition.key)
	return ""
}

func validBaseForEnvironmentOverride(key string) Config {
	base := Default()
	switch key {
	case "PROCTOR_METRICS_TLS_CERTIFICATE_FILE":
		base.Metrics.TLS.PrivateKeyFile = "./metrics-key.pem"
	case "PROCTOR_METRICS_TLS_PRIVATE_KEY_FILE":
		base.Metrics.TLS.CertificateFile = "./metrics-certificate.pem"
	case "PROCTOR_SERVER_PUBLIC_URL":
		base.Authentication.Bootstrap.DevelopmentMode = false
	case "PROCTOR_SERVER_TLS_MODE", "PROCTOR_SERVER_TLS_FORWARD_HTTP_TO_HTTPS":
		base.Server.PublicURL = "https://localhost:8065"
		base.Server.TLS.Mode = "static"
		base.Server.TLS.CertificateFile = "./certificate.pem"
		base.Server.TLS.PrivateKeyFile = "./private-key.pem"
	case "PROCTOR_AUTHENTICATION_BOOTSTRAP_DEVELOPMENT_MODE":
		base.Authentication.Bootstrap.Secret = "operator-provided-bootstrap-secret-32-bytes"
	case "PROCTOR_CACHE_BACKEND":
		base.Cache.Backend = "redis"
	case "PROCTOR_CLUSTER_BACKEND":
		base.Cache.Backend = "redis"
		base.Cluster.Backend = "memberlist"
		base.Cluster.Memberlist.EncryptionKey = base64.StdEncoding.EncodeToString(make([]byte, 32))
		base.VFS.Backend = "s3"
		base.VFS.S3.Endpoint = "127.0.0.1:9000"
		base.VFS.S3.Bucket = "proctor"
	case "PROCTOR_VFS_BACKEND":
		base.VFS.Backend = "s3"
		base.VFS.S3.Endpoint = "127.0.0.1:9000"
		base.VFS.S3.Bucket = "proctor"
	case "PROCTOR_MAIL_ENABLED":
		base.Mail.SecretSealing.EncryptionKey = base64.StdEncoding.EncodeToString(
			[]byte("mail-primary-key-material-32-byt"),
		)
	case "PROCTOR_MAIL_SECRET_SEALING_DECRYPTION_KEYS":
		base.Mail.SecretSealing.EncryptionKey = base64.StdEncoding.EncodeToString(
			[]byte("mail-primary-key-material-32-byt"),
		)
	case "PROCTOR_EXECUTION_ENABLED":
		base.Execution.Hosts = []ExecutionHost{{
			ID: "local-1", Address: "127.0.0.1:9443",
			Security: "insecure_local", Token: "development-token",
		}}
	case "PROCTOR_AUTHENTICATION_MFA_ENABLED":
		base.Authentication.MFA.Enabled = true
		base.Authentication.MFA.EncryptionKey = base64.StdEncoding.EncodeToString(make([]byte, 32))
	case "PROCTOR_AUTHENTICATION_MFA_DECRYPTION_KEYS":
		base.Authentication.MFA.EncryptionKey = base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{1}, 32))
	}
	return base
}
