// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package model

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
)

const TokenHashLength = sha256.Size * 2

// NewCredentialToken returns a 256-bit opaque bearer credential. The raw value
// must be shown only when required and must never be persisted or logged.
func NewCredentialToken() string {
	value := make([]byte, 32)
	if _, err := rand.Read(value); err != nil {
		panic("model: operating system random source failed: " + err.Error())
	}
	return base64.RawURLEncoding.EncodeToString(value)
}

// HashToken produces the persistent representation of a high-entropy opaque
// credential. This unsalted SHA-256 digest is appropriate for random bearer
// tokens; human passwords require a password hashing algorithm instead.
func HashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func IsValidTokenHash(value string) bool {
	if len(value) != TokenHashLength {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}
