// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package app

import (
	"errors"
	"strings"
	"testing"

	"github.com/sudosylabs/proctor/server/config"
)

func testPasswordPolicy() PasswordPolicy {
	settings := config.Default().Authentication.Password
	return PasswordPolicy{
		MinimumLength:    settings.MinimumLength,
		MaximumLength:    settings.MaximumLength,
		ArgonMemoryKiB:   19 * 1024,
		ArgonIterations:  1,
		ArgonParallelism: 1,
		ArgonSaltBytes:   settings.ArgonSaltBytes,
		ArgonKeyBytes:    settings.ArgonKeyBytes,
	}
}

func TestPasswordHasherRoundTripAndRehash(t *testing.T) {
	settings := testPasswordPolicy()
	hasher, err := newPasswordHasher(settings)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := hasher.Hash("correct horse battery staple")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(encoded, "$argon2id$v=19$") {
		t.Fatalf("Hash() = %q", encoded)
	}
	if err := hasher.Verify(encoded, "correct horse battery staple"); err != nil {
		t.Fatalf("Verify(correct) error = %v", err)
	}
	if err := hasher.Verify(encoded, "wrong password"); !errors.Is(err, ErrPasswordMismatch) {
		t.Fatalf("Verify(wrong) error = %v", err)
	}
	if hasher.NeedsRehash(encoded) {
		t.Fatal("fresh hash needs rehash")
	}

	stronger := settings
	stronger.ArgonIterations = 2
	strongerHasher, err := newPasswordHasher(stronger)
	if err != nil {
		t.Fatal(err)
	}
	if !strongerHasher.NeedsRehash(encoded) {
		t.Fatal("changed parameters did not require rehash")
	}
}

func TestPasswordHasherRejectsUnsafeInputsAndParameters(t *testing.T) {
	settings := testPasswordPolicy()
	hasher, err := newPasswordHasher(settings)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := hasher.Hash("short"); err == nil {
		t.Fatal("Hash(short) succeeded")
	}
	oversized := strings.Repeat("a", settings.MaximumLength+1)
	if _, err := hasher.Hash(oversized); err == nil {
		t.Fatal("Hash(oversized) succeeded")
	}
	hostile := "$argon2id$v=19$m=4294967295,t=20,p=64$c2FsdHNhbHRzYWx0c2FsdA$a2V5a2V5a2V5a2V5a2V5a2V5a2V5"
	if err := hasher.Verify(hostile, "password"); !errors.Is(err, ErrPasswordMismatch) {
		t.Fatalf("Verify(hostile) error = %v", err)
	}
}
