// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package config

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestMailSecretSealingValidationIsStrictAndIndependent(t *testing.T) {
	t.Parallel()

	key := func(seed byte) string {
		return base64.StdEncoding.EncodeToString([]byte(strings.Repeat(string([]byte{seed}), 32)))
	}
	tests := []struct {
		name  string
		setup func(*Config)
		field string
	}{
		{
			name:  "fallback requires primary even while disabled",
			setup: func(cfg *Config) { cfg.Mail.SecretSealing.DecryptionKeys = []string{key(1)} },
			field: "mail.secret_sealing.encryption_key",
		},
		{
			name:  "primary must be canonical base64",
			setup: func(cfg *Config) { cfg.Mail.SecretSealing.EncryptionKey = key(1) + "\n" },
			field: "mail.secret_sealing.encryption_key",
		},
		{
			name: "primary input is length bounded before decoding",
			setup: func(cfg *Config) {
				cfg.Mail.SecretSealing.EncryptionKey = strings.Repeat("A", 1<<20)
			},
			field: "mail.secret_sealing.encryption_key",
		},
		{
			name: "fallback must be 32 bytes",
			setup: func(cfg *Config) {
				cfg.Mail.SecretSealing.EncryptionKey = key(1)
				cfg.Mail.SecretSealing.DecryptionKeys = []string{base64.StdEncoding.EncodeToString(make([]byte, 31))}
			},
			field: "mail.secret_sealing.decryption_keys[0]",
		},
		{
			name: "decoded material must be unique",
			setup: func(cfg *Config) {
				cfg.Mail.SecretSealing.EncryptionKey = key(1)
				cfg.Mail.SecretSealing.DecryptionKeys = []string{key(1)}
			},
			field: "mail.secret_sealing.decryption_keys",
		},
		{
			name: "fallback ring is bounded",
			setup: func(cfg *Config) {
				cfg.Mail.SecretSealing.EncryptionKey = key(1)
				cfg.Mail.SecretSealing.DecryptionKeys = make([]string, 9)
				for index := range cfg.Mail.SecretSealing.DecryptionKeys {
					cfg.Mail.SecretSealing.DecryptionKeys[index] = key(byte(index + 2))
				}
			},
			field: "mail.secret_sealing.decryption_keys",
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			cfg := Default()
			test.setup(&cfg)
			err := cfg.Validate()
			var validation *ValidationError
			if !errors.As(err, &validation) {
				t.Fatalf("Validate() error = %v, want ValidationError", err)
			}
			for _, failure := range validation.Fields {
				if failure.Field == test.field {
					return
				}
			}
			t.Fatalf("Validate() fields = %#v, want %q", validation.Fields, test.field)
		})
	}

	valid := Default()
	valid.Mail.Enabled = true
	if err := valid.Validate(); err == nil || !strings.Contains(err.Error(), "mail.secret_sealing.encryption_key") {
		t.Fatalf("Validate(enabled mail without durable key ring) = %v", err)
	}
	valid.Mail.SecretSealing.EncryptionKey = key(1)
	valid.Mail.SecretSealing.DecryptionKeys = []string{key(2)}
	if err := valid.Validate(); err != nil {
		t.Fatalf("Validate(valid mail key ring) = %v", err)
	}
}

func TestMailSecretSealingRejectsDecodedKeyReuseAcrossDomains(t *testing.T) {
	t.Parallel()

	key := func(seed byte) string {
		return base64.StdEncoding.EncodeToString([]byte(strings.Repeat(string([]byte{seed}), 32)))
	}
	tests := []struct {
		name  string
		setup func(*Config)
		field string
	}{
		{
			name: "MFA primary",
			setup: func(cfg *Config) {
				cfg.Mail.SecretSealing.EncryptionKey = key(1)
				cfg.Authentication.MFA.EncryptionKey = key(1)
			},
			field: "mail.secret_sealing.encryption_key",
		},
		{
			name: "MFA fallback",
			setup: func(cfg *Config) {
				cfg.Mail.SecretSealing.EncryptionKey = key(1)
				cfg.Mail.SecretSealing.DecryptionKeys = []string{key(2)}
				cfg.Authentication.MFA.DecryptionKeys = []string{key(2)}
			},
			field: "mail.secret_sealing.decryption_keys[0]",
		},
		{
			name: "Memberlist",
			setup: func(cfg *Config) {
				cfg.Mail.SecretSealing.EncryptionKey = key(3)
				cfg.Cache.Backend = "redis"
				cfg.Cluster.Backend = "memberlist"
				cfg.Cluster.Memberlist.EncryptionKey = key(3)
				cfg.VFS.Backend = "s3"
				cfg.VFS.S3.Endpoint = "127.0.0.1:9000"
				cfg.VFS.S3.Bucket = "proctor"
			},
			field: "mail.secret_sealing.encryption_key",
		},
		{
			name: "whitespace-padded Memberlist",
			setup: func(cfg *Config) {
				cfg.Mail.SecretSealing.EncryptionKey = key(4)
				cfg.Cache.Backend = "redis"
				cfg.Cluster.Backend = "memberlist"
				cfg.Cluster.Memberlist.EncryptionKey = "  \n" + key(4) + "\t "
				cfg.VFS.Backend = "s3"
				cfg.VFS.S3.Endpoint = "127.0.0.1:9000"
				cfg.VFS.S3.Bucket = "proctor"
			},
			field: "mail.secret_sealing.encryption_key",
		},
		{
			name: "newline-bearing MFA",
			setup: func(cfg *Config) {
				cfg.Mail.SecretSealing.EncryptionKey = key(5)
				encoded := key(5)
				cfg.Authentication.MFA.EncryptionKey = encoded[:20] + "\r\n" + encoded[20:]
			},
			field: "mail.secret_sealing.encryption_key",
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			cfg := Default()
			test.setup(&cfg)
			err := cfg.Validate()
			var validation *ValidationError
			if !errors.As(err, &validation) {
				t.Fatalf("Validate() error = %v, want ValidationError", err)
			}
			for _, failure := range validation.Fields {
				if failure.Field == test.field && strings.Contains(failure.Message, "must not reuse") {
					return
				}
			}
			t.Fatalf("Validate() fields = %#v, want reuse failure for %q", validation.Fields, test.field)
		})
	}
}

func TestMailSecretSealingKeysAreClonedAndRedacted(t *testing.T) {
	t.Parallel()

	primary := base64.StdEncoding.EncodeToString([]byte(strings.Repeat("p", 32)))
	fallback := base64.StdEncoding.EncodeToString([]byte(strings.Repeat("f", 32)))
	cfg := Default()
	cfg.Mail.SecretSealing.EncryptionKey = primary
	cfg.Mail.SecretSealing.DecryptionKeys = []string{fallback}

	clone := cfg.Clone()
	clone.Mail.SecretSealing.DecryptionKeys[0] = "changed"
	if cfg.Mail.SecretSealing.DecryptionKeys[0] != fallback {
		t.Fatal("Clone() exposed the mail decryption-key slice")
	}
	redacted, err := cfg.RedactedJSON()
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{primary, fallback} {
		if strings.Contains(string(redacted), forbidden) {
			t.Fatalf("RedactedJSON() exposed key material: %s", redacted)
		}
	}
	var projected Config
	if err := json.Unmarshal(redacted, &projected); err != nil {
		t.Fatal(err)
	}
	if projected.Mail.SecretSealing.EncryptionKey != "[redacted]" ||
		len(projected.Mail.SecretSealing.DecryptionKeys) != 1 ||
		projected.Mail.SecretSealing.DecryptionKeys[0] != "[redacted]" {
		t.Fatalf("redacted mail key ring = %#v", projected.Mail.SecretSealing)
	}
}
