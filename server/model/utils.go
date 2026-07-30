// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: Apache-2.0
//
// Adapted from Mattermost server/public/model/utils.go. Proctor uses the same
// 26-character z-base-32 identifier representation with crypto/rand directly.

package model

import (
	"crypto/rand"
	"encoding/base32"
	"strings"
	"time"
)

const IdLength = 26

const IdAlphabet = "ybndrfg8ejkmcpqxot1uwisza345h769"

var idEncoding = base32.NewEncoding(IdAlphabet).WithPadding(base32.NoPadding)

// NewId returns a random 128-bit identifier encoded as 26 z-base-32
// characters. It panics only if the operating system random source fails.
func NewId() string {
	value := make([]byte, 16)
	if _, err := rand.Read(value); err != nil {
		panic("model: operating system random source failed: " + err.Error())
	}

	// Mark the random bytes as an RFC 4122 version 4 UUID before encoding,
	// matching Mattermost's identifier generation semantics.
	value[6] = (value[6] & 0x0f) | 0x40
	value[8] = (value[8] & 0x3f) | 0x80
	return idEncoding.EncodeToString(value)
}

// IsValidId reports whether value is a canonical Proctor identifier.
func IsValidId(value string) bool {
	if len(value) != IdLength {
		return false
	}
	for index := range value {
		if !strings.ContainsRune(IdAlphabet, rune(value[index])) {
			return false
		}
	}
	return true
}

// GetMillis returns milliseconds since the Unix epoch.
func GetMillis() int64 {
	return GetMillisForTime(time.Now())
}

// GetMillisForTime converts a time to milliseconds since the Unix epoch.
func GetMillisForTime(value time.Time) int64 {
	return value.UnixMilli()
}

// SanitizeUnicode removes Unicode code points that are unsuitable for storage
// or may alter the visual ordering of identifiers and audit output.
func SanitizeUnicode(value string) string {
	return strings.Map(filterBlocklist, value)
}

func filterBlocklist(value rune) rune {
	const drop = -1
	switch value {
	case '\u0340', '\u0341':
		return drop
	case '\u17A3', '\u17D3':
		return drop
	case '\u2028', '\u2029':
		return drop
	case '\u202A', '\u202B', '\u202C', '\u202D', '\u202E':
		return drop
	case '\u2066', '\u2067', '\u2068', '\u2069':
		return drop
	case '\uFFF9', '\uFFFA', '\uFFFB':
		return drop
	}
	return value
}
