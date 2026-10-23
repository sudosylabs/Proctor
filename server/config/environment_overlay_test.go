// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package config

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"reflect"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestEnvironmentOverrideCatalogContracts(t *testing.T) {
	require.Len(t, environmentOverrideCatalog, 128, "environment override definitions")

	seen := make(map[string]struct{}, len(environmentOverrideCatalog))
	for _, definition := range environmentOverrideCatalog {
		definition := definition
		t.Run(definition.key, func(t *testing.T) {
			_, exists := seen[definition.key]
			require.False(t, exists, "environment override %q is duplicated", definition.key)
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
			require.NoError(t, err)
			require.Equal(t, []string{definition.key}, overlay.keys(), "applied overrides")
			require.NotEqual(t, persisted, candidate, "environment override did not change configuration")

			overlay.restore(&candidate, persisted)
			require.Equal(t, persisted, candidate, "environment override did not restore persisted configuration")
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
			require.NoError(t, err)
			value := validEnvironmentValue(t, definition, initial)
			store, err := NewStore(
				context.Background(),
				NewMemoryStore(data),
				StoreOptions{LookupEnv: func(key string) (string, bool) {
					return value, key == definition.key
				}},
			)
			require.NoError(t, err)
			t.Cleanup(func() { _ = store.Close() })

			require.Equal(t, []string{definition.key}, store.EnvironmentOverrides())
			candidate := store.Get()
			if definition.key == "PROCTOR_MAIL_FROM_NAME" {
				candidate.Authentication.Bootstrap.DevelopmentMode = false
				candidate.Server.PublicURL = "https://changed.example.edu"
			} else {
				candidate.Mail.FromName = "Changed by Store.Set"
			}
			_, _, err = store.Set(context.Background(), candidate)
			require.NoError(t, err)

			persisted := store.GetPersisted()
			want := initial.Clone()
			if definition.key == "PROCTOR_MAIL_FROM_NAME" {
				want.Authentication.Bootstrap.DevelopmentMode = false
				want.Server.PublicURL = "https://changed.example.edu"
			} else {
				want.Mail.FromName = "Changed by Store.Set"
			}
			require.Equal(t, want, persisted, "Store.Set persisted an environment-owned value")
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
	require.Error(t, err, "duplicate environment override was accepted")
	require.Nil(t, overlay.keys(), "applied overrides")
	require.Equal(t, want, candidate, "duplicate catalog mutated configuration")
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
	require.FailNow(t, fmt.Sprintf("no test value changes environment override %q", definition.key))
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
		"10.0.0.0/8,192.0.2.1",
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
	require.FailNow(t, fmt.Sprintf("no valid test value for environment override %q", definition.key))
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
	case "PROCTOR_AUTHENTICATION_MFA_ENABLED":
		base.Authentication.MFA.Enabled = true
		base.Authentication.MFA.EncryptionKey = base64.StdEncoding.EncodeToString(make([]byte, 32))
	case "PROCTOR_AUTHENTICATION_MFA_DECRYPTION_KEYS":
		base.Authentication.MFA.EncryptionKey = base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{1}, 32))
	}
	return base
}
