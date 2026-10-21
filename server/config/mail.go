// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package config

import (
	"fmt"
	"strings"
)

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
