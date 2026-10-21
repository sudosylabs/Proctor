// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package store

import (
	"context"
	"time"

	"github.com/sudosylabs/proctor/server/model"
)

// UserSettingsStore owns the exact current User Settings Document. Mutations
// are added only through named atomic contracts; it is not a generic file or
// configuration store.
type UserSettingsStore interface {
	Get(context.Context, model.UserID) (*model.UserSettingsDocument, error)
	Replace(context.Context, *UserSettingsReplacement, *CommandIdempotency) (*UserSettingsReplacementResult, error)
}

type UserSettingsReplacement struct {
	UserID           model.UserID
	Source           string
	FormatVersion    int
	ExpectedRevision model.UserSettingsRevision
	NextRevision     model.UserSettingsRevision
	UpdatedAt        time.Time
	AuditEvent       *model.AuditEvent
}

type UserSettingsReplacementResult struct {
	Revision      model.UserSettingsRevision `json:"revision"`
	FormatVersion int                        `json:"format_version"`
	UpdatedAt     time.Time                  `json:"updated_at"`
	Changed       bool                       `json:"changed"`
	Replayed      bool                       `json:"-"`
}
