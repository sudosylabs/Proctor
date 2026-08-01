// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package app

import "github.com/sudosylabs/proctor/server/model"

// Invocation is the immutable security and audit context for one
// actor-sensitive application use case. Transports, jobs, and tests construct
// it explicitly; it is never recovered from context.Context.
type Invocation struct {
	principal model.Principal
	requestID string
	ipAddress string
	userAgent string
}

// NewInvocation builds an invocation from the acting principal and safe call
// metadata. Empty metadata fields are allowed when a caller has no request
// context (for example internal jobs and focused tests).
func NewInvocation(principal model.Principal, metadata model.RequestMetadata) Invocation {
	principal.CredentialScopes = append([]string(nil), principal.CredentialScopes...)
	return Invocation{
		principal: principal,
		requestID: metadata.RequestId,
		ipAddress: metadata.IPAddress,
		userAgent: metadata.UserAgent,
	}
}

// Principal returns the acting principal.
func (i Invocation) Principal() model.Principal {
	principal := i.principal
	principal.CredentialScopes = append([]string(nil), i.principal.CredentialScopes...)
	return principal
}

// RequestMetadata returns the safe audit metadata carried by the invocation.
func (i Invocation) RequestMetadata() model.RequestMetadata {
	return model.RequestMetadata{
		RequestId: i.requestID,
		IPAddress: i.ipAddress,
		UserAgent: i.userAgent,
	}
}
