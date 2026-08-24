// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"errors"
	"time"

	applicationidempotency "github.com/sudosylabs/proctor/server/app/idempotency"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

const (
	commandIdempotencyRetention = 24 * time.Hour
	commandIdempotencyWait      = 2 * time.Second
)

func newCommandIdempotency(invocation Invocation, operation, key string, semantic any) (*store.CommandIdempotency, error) {
	prepared, err := applicationidempotency.Prepare(invocation.Principal().UserID, operation, key, semantic)
	var semanticError *applicationidempotency.SemanticEncodingError
	switch {
	case errors.Is(err, applicationidempotency.ErrInvalidPrincipal):
		return nil, NewError("idempotency.invalid_key").Wrap(err)
	case errors.As(err, &semanticError):
		return nil, NewError("request.invalid").Wrap(err)
	default:
		return prepared, err
	}
}

func idempotencyError(err error) error {
	var conflict *store.ErrIdempotencyConflict
	var inProgress *store.ErrIdempotencyInProgress
	switch {
	case errors.As(err, &conflict):
		return NewError("idempotency.conflict").Wrap(err)
	case errors.As(err, &inProgress):
		return NewError("idempotency.in_progress").Wrap(err)
	default:
		return nil
	}
}

func replayAuditData(value map[string]any, originalAuditID string) map[string]any {
	result := make(map[string]any, len(value)+2)
	for key, item := range value {
		result[key] = item
	}
	result["idempotency_replayed"] = true
	if model.IsValidId(originalAuditID) {
		result["original_audit_event_id"] = originalAuditID
	}
	return result
}
