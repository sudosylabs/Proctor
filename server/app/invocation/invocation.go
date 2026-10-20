// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

// Package invocation owns the one immutable call context that every
// application module, parent or child, receives for an actor-sensitive use
// case, plus the System identity of actorless durable Job work. It sits below
// the parent app package so Exam child modules share the same type without
// importing their parent. It owns no authorization, audit, or transport
// policy and may depend only on model and the standard library.
package invocation

import "github.com/sudosylabs/proctor/server/model"

// Invocation is the immutable security and audit context for one
// actor-sensitive application use case. Transports, jobs, and tests construct
// it explicitly; it is never recovered from context.Context.
type Invocation struct {
	principal model.Principal
	metadata  model.RequestMetadata
}

// New builds an invocation from the acting principal and safe call metadata.
// Empty metadata fields are allowed when a caller has no request context (for
// example internal jobs and focused tests).
func New(principal model.Principal, metadata model.RequestMetadata) Invocation {
	principal.CredentialScopes = append([]string(nil), principal.CredentialScopes...)
	return Invocation{principal: principal, metadata: metadata}
}

// Principal returns a defensive copy of the acting principal.
func (i Invocation) Principal() model.Principal {
	principal := i.principal
	principal.CredentialScopes = append([]string(nil), i.principal.CredentialScopes...)
	return principal
}

// RequestMetadata returns the safe audit metadata carried by the invocation.
func (i Invocation) RequestMetadata() model.RequestMetadata { return i.metadata }

// System identifies one durable Job execution responsible for actorless work
// without manufacturing a user Principal. Both IDs enter only bounded system
// audit metadata.
type System struct {
	JobID     model.JobID
	AttemptID model.JobAttemptID
}

// Valid reports whether both Job identities are present and well formed.
func (s System) Valid() bool { return s.JobID.IsValid() && s.AttemptID.IsValid() }
