// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package app

import (
	"errors"

	"github.com/sudosylabs/proctor/server/store"
)

// OperationalEvent is a bounded application outcome. Its values are selected
// only by application code; it contains no resource, principal, request, or
// error data.
type OperationalEvent struct {
	subsystem string
	event     string
	outcome   string
}

func (e OperationalEvent) Subsystem() string { return e.subsystem }
func (e OperationalEvent) Event() string     { return e.event }
func (e OperationalEvent) Outcome() string   { return e.outcome }

// OperationalRecorder is the single application observability seam. The
// interface stays small while named events give operations product-level
// visibility without a Prometheus dependency or global service locator.
type OperationalRecorder interface {
	RecordOperationalEvent(OperationalEvent)
}

type nopOperationalRecorder struct{}

func (nopOperationalRecorder) RecordOperationalEvent(OperationalEvent) {}

func operationalEvent(subsystem, event string, err error) OperationalEvent {
	return OperationalEvent{subsystem: subsystem, event: event, outcome: operationalOutcome(err)}
}

func operationalOutcome(err error) string {
	if err == nil {
		return "success"
	}
	failure, ok := As(err)
	if !ok {
		return "error"
	}
	switch failure.Code() {
	case "authorization.denied":
		return "denied"
	case "authentication.invalid_credentials", "authentication.invalid_token":
		return "rejected"
	case "authentication.rate_limited":
		return "rate_limited"
	case "resource.not_found", "user.not_found":
		return "not_found"
	}
	var conflict *store.ErrConflict
	if errors.As(err, &conflict) {
		return "conflict"
	}
	return "error"
}
