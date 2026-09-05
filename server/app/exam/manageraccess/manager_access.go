// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package manageraccess

import (
	"context"
	"time"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

// Memberships supplies current organizational membership, independently of
// role permissions and the Exam Manager relationship. Decision times use UTC
// microsecond precision and include the membership start but exclude its end.
type Memberships interface {
	ListActiveByUser(context.Context, string, time.Time) ([]*model.AcademicUnitMember, error)
}

// SelectAction chooses the caller's ordinary action only for a current Exam
// Manager with current membership in the Exam's exact Academic Unit. Otherwise
// it chooses the explicit override action, which the caller must authorize.
// The caller validates its inputs and supplies non-nil access, access.Exam,
// and memberships.
// Lookup failures are returned unchanged for the owning use case to present.
func SelectAction(ctx context.Context, memberships Memberships, userID model.UserID, access *store.ExamAccessSnapshot,
	at time.Time, ordinaryAction, overrideAction model.Action,
) (model.Action, error) {
	if access.ActorIsManager {
		ordinary, err := HasCurrentMembership(ctx, memberships, userID, access.Exam.AcademicUnitID, at)
		if err != nil {
			return "", err
		}
		if ordinary {
			return ordinaryAction, nil
		}
	}
	return overrideAction, nil
}

// HasCurrentMembership checks the exact Academic Unit at the supplied time.
// It grants no role permission and implies no Exam Manager relationship.
func HasCurrentMembership(ctx context.Context, memberships Memberships, userID model.UserID, unitID model.AcademicUnitID, at time.Time) (bool, error) {
	items, err := memberships.ListActiveByUser(ctx, userID.String(), model.TimeUTC(at))
	if err != nil {
		return false, err
	}
	for _, item := range items {
		if item != nil && item.AcademicUnitID == unitID {
			return true, nil
		}
	}
	return false, nil
}
