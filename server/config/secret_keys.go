// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package config

import (
	"encoding/base64"
	"fmt"
	"strings"
)

func validateSecretKeySeparation(c Config, add func(string, string)) {
	memberlistMaterial := make(map[[32]byte]struct{}, 1+len(c.Cluster.Memberlist.DecryptionKeys))
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
			memberlistMaterial[material] = struct{}{}
		}
	}

	reserved := make(map[[32]byte]struct{},
		len(memberlistMaterial)+1+len(c.Authentication.MFA.DecryptionKeys))
	for material := range memberlistMaterial {
		reserved[material] = struct{}{}
	}
	mfaKeys := append(
		[]string{c.Authentication.MFA.EncryptionKey},
		c.Authentication.MFA.DecryptionKeys...,
	)
	for index, encoded := range mfaKeys {
		material, ok := decodeAES256Key(encoded)
		if !ok {
			continue
		}
		if _, reused := memberlistMaterial[material]; reused {
			field := "authentication.mfa.encryption_key"
			if index > 0 {
				field = fmt.Sprintf("authentication.mfa.decryption_keys[%d]", index-1)
			}
			add(field, "must not reuse Memberlist key material")
		}
		reserved[material] = struct{}{}
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
