// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package config

import (
	"encoding/base64"
	"strings"
	"testing"
)

func memberlistTestKey(fill byte) string {
	return base64.StdEncoding.EncodeToString([]byte(strings.Repeat(string(fill), 32)))
}

func TestMemberlistKeyringValidationAndRedaction(t *testing.T) {
	t.Parallel()

	cfg := Default()
	cfg.Cache.Backend = "redis"
	cfg.Cluster.Backend = "memberlist"
	cfg.Cluster.NodeID = "node-a"
	cfg.Cluster.Memberlist.EncryptionKey = memberlistTestKey('a')
	cfg.Cluster.Memberlist.DecryptionKeys = []string{memberlistTestKey('b')}
	cfg.VFS.Backend = "s3"
	cfg.VFS.S3.Endpoint = "127.0.0.1:9000"
	cfg.VFS.S3.Bucket = "proctor"
	if err := cfg.Validate(); err != nil {
		t.Fatalf("valid keyring: %v", err)
	}

	cloned := cfg.Clone()
	cfg.Cluster.Memberlist.DecryptionKeys[0] = memberlistTestKey('c')
	if cloned.Cluster.Memberlist.DecryptionKeys[0] != memberlistTestKey('b') {
		t.Fatal("Clone aliased Memberlist decryption keys")
	}
	redacted := cloned.Redacted()
	if redacted.Cluster.Memberlist.EncryptionKey != "[redacted]" ||
		redacted.Cluster.Memberlist.DecryptionKeys[0] != "[redacted]" {
		t.Fatalf("redacted keyring = %#v", redacted.Cluster.Memberlist)
	}
}

func TestMemberlistKeyringRejectsDuplicatesAndExcessFallbacks(t *testing.T) {
	t.Parallel()

	cfg := Default()
	cfg.Cluster.Backend = "memberlist"
	cfg.Cluster.Memberlist.EncryptionKey = memberlistTestKey('a')
	cfg.Cluster.Memberlist.DecryptionKeys = []string{memberlistTestKey('a')}
	err := cfg.Validate()
	if err == nil || !strings.Contains(err.Error(), "duplicate decoded keys") {
		t.Fatalf("duplicate keyring error = %v", err)
	}

	cfg.Cluster.Memberlist.DecryptionKeys = cfg.Cluster.Memberlist.DecryptionKeys[:0]
	for index := range 9 {
		cfg.Cluster.Memberlist.DecryptionKeys = append(
			cfg.Cluster.Memberlist.DecryptionKeys,
			memberlistTestKey(byte('b'+index)),
		)
	}
	err = cfg.Validate()
	if err == nil || !strings.Contains(err.Error(), "at most 8 fallback keys") {
		t.Fatalf("oversized keyring error = %v", err)
	}
}
