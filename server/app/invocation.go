// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package app

import (
	"github.com/sudosylabs/proctor/server/app/invocation"
	"github.com/sudosylabs/proctor/server/model"
)

// Invocation is the application-wide immutable call context owned by the
// app/invocation leaf. The parent and every Exam child module share this one
// type; transports name it through this alias.
type Invocation = invocation.Invocation

// NewInvocation builds an invocation from the acting principal and safe call
// metadata. Empty metadata fields are allowed when a caller has no request
// context (for example internal jobs and focused tests).
func NewInvocation(principal model.Principal, metadata model.RequestMetadata) Invocation {
	return invocation.New(principal, metadata)
}
