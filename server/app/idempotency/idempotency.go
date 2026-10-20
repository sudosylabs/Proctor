// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

// Package idempotency prepares the durable identity of retryable application
// commands for every application module, parent or child, and reports its
// failures as application errors. Command owners remain responsible for
// requiring keys and defining their operation-specific semantic documents.
// This leaf may depend only on the standard library, the inward model and
// store contracts, and the app/apperror and app/invocation leaves.
package idempotency

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/sudosylabs/proctor/server/app/apperror"
	"github.com/sudosylabs/proctor/server/app/invocation"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

const (
	// Retention is how long a durable command outcome remains replayable.
	Retention = 24 * time.Hour
	// Wait bounds how long a concurrent retry waits for an in-flight outcome.
	Wait = 2 * time.Second
)

// Require prepares the durable identity of a command whose owner demands an
// idempotency key; an empty key is rejected with idempotency.key_required.
func Require(call invocation.Invocation, operation, key string, semantic any) (*store.CommandIdempotency, error) {
	if key == "" {
		return nil, apperror.New("idempotency.key_required")
	}
	return Prepare(call, operation, key, semantic)
}

// Prepare prepares the durable identity of a command whose key is optional;
// an empty key yields no identity. An invalid acting user is
// idempotency.key_invalid and an unencodable semantic document is
// request.invalid.
func Prepare(call invocation.Invocation, operation, key string, semantic any) (*store.CommandIdempotency, error) {
	if key == "" {
		return nil, nil
	}
	userID := call.Principal().UserID
	if !userID.IsValid() {
		return nil, apperror.New("idempotency.key_invalid").Wrap(errors.New("idempotency requires an authenticated user"))
	}
	canonical, err := json.Marshal(semantic)
	if err != nil {
		return nil, apperror.New("request.invalid").Wrap(fmt.Errorf("encode idempotency semantics: %w", err))
	}
	return identity(userID, operation, key, canonical), nil
}

func identity(userID model.UserID, operation, key string, canonical []byte) *store.CommandIdempotency {
	keyDigest := sha256.Sum256([]byte(key))
	fingerprintInput := append([]byte(operation+"\x00v1\x00"), canonical...)
	fingerprint := sha256.Sum256(fingerprintInput)
	return &store.CommandIdempotency{
		UserID: userID, Operation: operation, KeyDigest: keyDigest,
		FingerprintVersion: 1, Fingerprint: fingerprint, OutcomeVersion: 1,
		Retention: Retention, Wait: Wait,
	}
}
