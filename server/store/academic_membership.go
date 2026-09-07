// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package store

import "github.com/sudosylabs/proctor/server/model"

// AcademicUnitMemberPageOptions selects a bounded page ordered by (UserID, ID).
// ActiveAt zero includes history; positive milliseconds select [start, end).
// The cursor tuple is exclusive and either entirely absent or entirely valid.
type AcademicUnitMemberPageOptions struct {
	AcademicUnitID model.AcademicUnitID
	ActiveAt       int64
	AfterUserID    model.UserID
	AfterID        model.AcademicUnitMemberID
	Limit          int
}

type AcademicUnitMemberPage struct {
	Members []*model.AcademicUnitMember
	HasMore bool
}

// ClassMemberPageOptions selects a bounded page ordered by (UserID, ID).
// ActiveAt zero includes history; positive milliseconds select [start, end).
// The cursor tuple is exclusive and either entirely absent or entirely valid.
type ClassMemberPageOptions struct {
	ClassID     model.ClassID
	ActiveAt    int64
	AfterUserID model.UserID
	AfterID     model.ClassMemberID
	Limit       int
}

type ClassMemberPage struct {
	Members []*model.ClassMember
	HasMore bool
}
