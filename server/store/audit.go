// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package store

import (
	"context"

	"github.com/sudosylabs/proctor/server/model"
)

type AuditListOptions struct {
	ActorID    model.UserID
	Action     string
	Resource   *model.Resource
	BeforeTime int64
	BeforeID   model.AuditEventID
	Limit      int
	Visibility AuditVisibilityScope
}

// AuditVisibilityScope constrains audit history in persistence. InstitutionWide
// explicitly retains operator visibility; its zero value denies all rows.
// AcademicInstitutionWide permits only events whose scope resolves to an
// Academic Unit or Class anywhere in the Institution and still requires the
// closed application-selected AllowedActions catalog. AcademicUnitRootIDs name
// current authorized subtrees. A subtree projection requires roots and actions.
type AuditVisibilityScope struct {
	InstitutionWide         bool
	AcademicInstitutionWide bool
	AcademicUnitRootIDs     []model.AcademicUnitID
	AllowedActions          []string
}

// AuditStore owns authoritative security records. Events are append-oriented;
// Complete permits only the terminal transition from attempt to success/fail.
type AuditStore interface {
	Save(context.Context, *model.AuditEvent) (*model.AuditEvent, error)
	Get(context.Context, model.AuditEventID) (*model.AuditEvent, error)
	Complete(context.Context, model.AuditEventID, model.AuditStatus, string, []byte, int64) (*model.AuditEvent, error)
	List(context.Context, AuditListOptions) ([]*model.AuditEvent, error)
}
