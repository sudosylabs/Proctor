// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package app

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/server/store"
)

type mfaSecretKeyStoreFake struct {
	usage    []store.MFASecretKeyUsage
	required string
	err      error
}

func (f mfaSecretKeyStoreFake) InspectKeyState(context.Context) (*store.MFAKeyState, error) {
	if f.err != nil {
		return nil, f.err
	}
	return &store.MFAKeyState{RequiredPrimaryKeyID: f.required, Active: f.usage}, nil
}

type mfaKeyRingFake map[string]bool

func (f mfaKeyRingFake) HasKey(keyID string) bool { return f[keyID] }

func TestMFASecretKeyStartupCheckRequiresEveryStoredKey(t *testing.T) {
	t.Parallel()
	ring := mfaKeyRingFake{"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa": true}
	usage := []store.MFASecretKeyUsage{{KeyID: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", Credentials: 3}}
	require.NoError(t, validateMFASecretKeys(context.Background(), mfaSecretKeyStoreFake{usage: usage}, ring))
	require.NoError(t, validateMFASecretKeys(context.Background(), mfaSecretKeyStoreFake{}, ring))

	stranded := append(usage, store.MFASecretKeyUsage{KeyID: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", Credentials: 2})
	err := validateMFASecretKeys(context.Background(), mfaSecretKeyStoreFake{usage: stranded}, ring)
	require.ErrorContains(t, err, "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb")
	require.ErrorContains(t, err, "2 stored credentials")

	fencedElsewhere := validateMFASecretKeys(context.Background(), mfaSecretKeyStoreFake{usage: usage, required: "cccccccccccccccccccccccccccccccc"}, ring)
	require.ErrorContains(t, fencedElsewhere, "cccccccccccccccccccccccccccccccc", "the ring must hold the primary the last rekey fenced")
	require.NoError(t, validateMFASecretKeys(context.Background(), mfaSecretKeyStoreFake{usage: usage, required: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}, ring))

	unavailable := errors.New("database unavailable")
	require.ErrorIs(t, validateMFASecretKeys(context.Background(), mfaSecretKeyStoreFake{err: unavailable}, ring), unavailable)
}
