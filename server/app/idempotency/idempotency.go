// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

// Package idempotency prepares the durable identity of retryable application
// commands. Command owners remain responsible for requiring keys and defining
// their operation-specific semantic documents. This leaf may depend only on
// the standard library and the inward model and store contracts.
package idempotency

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"time"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

const (
	retention = 24 * time.Hour
	wait      = 2 * time.Second
)

var ErrInvalidPrincipal = errors.New("idempotency requires an authenticated user")

type SemanticEncodingError struct {
	Err error
}

func (e *SemanticEncodingError) Error() string {
	return "encode idempotency semantics: " + e.Err.Error()
}

func (e *SemanticEncodingError) Unwrap() error { return e.Err }

func Prepare(userID model.UserID, operation, key string, semantic any) (*store.CommandIdempotency, error) {
	if key == "" {
		return nil, nil
	}
	if !userID.IsValid() {
		return nil, ErrInvalidPrincipal
	}
	canonical, err := json.Marshal(semantic)
	if err != nil {
		return nil, &SemanticEncodingError{Err: err}
	}
	keyDigest := sha256.Sum256([]byte(key))
	fingerprintInput := append([]byte(operation+"\x00v1\x00"), canonical...)
	fingerprint := sha256.Sum256(fingerprintInput)
	return &store.CommandIdempotency{
		UserID: userID, Operation: operation, KeyDigest: keyDigest,
		FingerprintVersion: 1, Fingerprint: fingerprint, OutcomeVersion: 1,
		Retention: retention, Wait: wait,
	}, nil
}
