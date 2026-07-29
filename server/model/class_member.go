// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: Apache-2.0
//
// Adapted from Mattermost server/public/model/channel_member.go for Proctor's
// time-bounded class enrollment.

package model

// ClassMember is a student's enrollment in one Class and AcademicPeriod.
// AcademicPeriodId deliberately duplicates Class.AcademicPeriodId so the store
// can enforce at most one active class membership per user and period with a
// database constraint. The application/store must verify that both period IDs
// match. Teachers and staff reach classes through role inheritance or an
// explicit class RoleBinding; they are not ClassMember records.
type ClassMember struct {
	Id               string `json:"id"`
	CreateAt         int64  `json:"create_at"`
	UpdateAt         int64  `json:"update_at"`
	DeleteAt         int64  `json:"delete_at"`
	ClassId          string `json:"class_id"`
	AcademicPeriodId string `json:"academic_period_id"`
	UserId           string `json:"user_id"`
	StartAt          int64  `json:"start_at"`
	EndAt            int64  `json:"end_at,omitempty"`
}

func (cm *ClassMember) PreSave() {
	preSaveMembership(&cm.Id, &cm.CreateAt, &cm.UpdateAt, &cm.StartAt)
}

func (cm *ClassMember) PreUpdate() {
	preUpdate(&cm.UpdateAt)
}

func (cm *ClassMember) IsValid() *AppError {
	const where = "ClassMember.IsValid"
	if appErr := validatePersistentFields(
		where,
		"class_member",
		cm.Id,
		cm.CreateAt,
		cm.UpdateAt,
	); appErr != nil {
		return appErr
	}
	details := "id=" + cm.Id
	if !IsValidId(cm.ClassId) {
		return invalidModelError(where, "class_member", "class_id", "must be a valid identifier", details)
	}
	if !IsValidId(cm.AcademicPeriodId) {
		return invalidModelError(
			where,
			"class_member",
			"academic_period_id",
			"must be a valid identifier",
			details,
		)
	}
	if !IsValidId(cm.UserId) {
		return invalidModelError(where, "class_member", "user_id", "must be a valid identifier", details)
	}
	return validateEffectiveTimes(where, "class_member", details, cm.StartAt, cm.EndAt)
}

func (cm *ClassMember) IsActiveAt(now int64) bool {
	return cm != nil && cm.DeleteAt == 0 && cm.StartAt <= now && (cm.EndAt == 0 || now < cm.EndAt)
}

func (cm *ClassMember) Auditable() map[string]any {
	fields := auditFields(cm.Id, cm.CreateAt, cm.UpdateAt, cm.DeleteAt)
	fields["class_id"] = cm.ClassId
	fields["academic_period_id"] = cm.AcademicPeriodId
	fields["user_id"] = cm.UserId
	fields["start_at"] = cm.StartAt
	fields["end_at"] = cm.EndAt
	return fields
}

var _ Auditable = (*ClassMember)(nil)
