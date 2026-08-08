// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: Apache-2.0
//
// Adapted from Mattermost server/public/model/channel_member.go for Proctor's
// time-bounded class enrollment.

package model

import (
	"fmt"
	"time"
)

// ClassMember is a student's enrollment in one Class and AcademicPeriod.
// AcademicPeriodID deliberately duplicates Class.AcademicPeriodID so the store
// can enforce at most one active class membership per user and period with a
// database constraint. The application/store must verify that both period IDs
// match. Teachers and staff reach classes through role inheritance or an
// explicit class RoleBinding; they are not ClassMember records.
type ClassMember struct {
	ID               ClassMemberID
	CreatedAt        time.Time
	UpdatedAt        time.Time
	ArchivedAt       OptionalTime
	Revision         int64
	ClassID          ClassID
	AcademicPeriodID AcademicPeriodID
	UserID           UserID
	StartsAt         time.Time
	EndsAt           OptionalTime
}

// PrepareCreate applies application-owned lifecycle fields before validation.
func (cm *ClassMember) PrepareCreate(id ClassMemberID, at time.Time) {
	if cm == nil {
		return
	}
	cm.ID = id
	at = TimeUTC(at)
	cm.CreatedAt = at
	cm.UpdatedAt = at
	cm.ArchivedAt = OptionalTime{}
	if cm.Revision <= 0 {
		cm.Revision = 1
	}
	if cm.StartsAt.IsZero() {
		cm.StartsAt = at
	} else {
		cm.StartsAt = TimeUTC(cm.StartsAt)
	}
}

// PrepareUpdate applies the application-selected transition time.
func (cm *ClassMember) PrepareUpdate(at time.Time) {
	if cm == nil {
		return
	}
	cm.UpdatedAt = TimeUTC(at)
	if cm.Revision <= 0 {
		cm.Revision = 1
	}
	cm.Revision++
}

// Validate checks rehydrated enrollment state.
func (cm *ClassMember) Validate() error {
	const where = "ClassMember.Validate"
	if cm == nil {
		return invalidModelError(where, "class_member", "value", "is required", "")
	}
	if !cm.ID.IsValid() {
		return invalidModelError(where, "class_member", "id", "must be a valid identifier", "")
	}
	details := "id=" + cm.ID.String()
	if cm.CreatedAt.IsZero() || cm.UpdatedAt.IsZero() {
		return invalidModelError(where, "class_member", "created_at", "must be set", details)
	}
	if cm.UpdatedAt.Before(cm.CreatedAt) {
		return invalidModelError(where, "class_member", "updated_at", "must not precede created_at", details)
	}
	if cm.Revision <= 0 {
		return invalidModelError(where, "class_member", "revision", "must be positive", details)
	}
	if !cm.ClassID.IsValid() {
		return invalidModelError(where, "class_member", "class_id", "must be a valid identifier", details)
	}
	if !cm.AcademicPeriodID.IsValid() {
		return invalidModelError(where, "class_member", "academic_period_id", "must be a valid identifier", details)
	}
	if !cm.UserID.IsValid() {
		return invalidModelError(where, "class_member", "user_id", "must be a valid identifier", details)
	}
	return validateEffectiveInterval(where, "class_member", details, cm.StartsAt, cm.EndsAt)
}

// IsActiveAt reports whether the enrollment covers the given instant.
func (cm *ClassMember) IsActiveAt(now time.Time) bool {
	if cm == nil || cm.IsArchived() {
		return false
	}
	now = TimeUTC(now)
	if cm.StartsAt.After(now) {
		return false
	}
	if cm.EndsAt.Valid && !now.Before(cm.EndsAt.Time) {
		return false
	}
	return true
}

// IsArchived reports soft archive state.
func (cm *ClassMember) IsArchived() bool {
	return cm != nil && cm.ArchivedAt.Valid
}

// End closes an open-ended enrollment at the exclusive end time.
func (cm *ClassMember) End(at time.Time) error {
	if cm == nil {
		return fmt.Errorf("model: class member is nil")
	}
	at = TimeUTC(at)
	if cm.EndsAt.Valid {
		return fmt.Errorf("model: class member already ended")
	}
	cm.EndsAt = OptionalTimeFrom(at)
	cm.UpdatedAt = at
	cm.Revision++
	return cm.Validate()
}

// Auditable returns a deliberately safe audit projection.
func (cm *ClassMember) Auditable() map[string]any {
	if cm == nil {
		return map[string]any{}
	}
	return map[string]any{
		"id":                 cm.ID.String(),
		"created_at":         MillisFromTime(cm.CreatedAt),
		"updated_at":         MillisFromTime(cm.UpdatedAt),
		"archived_at":        cm.ArchivedAt.Millis(),
		"revision":           cm.Revision,
		"class_id":           cm.ClassID.String(),
		"academic_period_id": cm.AcademicPeriodID.String(),
		"user_id":            cm.UserID.String(),
		"start_at":           MillisFromTime(cm.StartsAt),
		"end_at":             cm.EndsAt.Millis(),
	}
}

var _ Auditable = (*ClassMember)(nil)
