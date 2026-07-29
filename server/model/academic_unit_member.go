// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: Apache-2.0
//
// Adapted from Mattermost server/public/model/team_member.go for Proctor's
// hierarchical academic-unit membership.

package model

// AcademicUnitMember records that a User belongs to one AcademicUnit during a
// time range. It does not contain roles: a teacher may hold different
// RoleBinding values in different units, and membership alone grants no
// permission.
type AcademicUnitMember struct {
	Id             string `json:"id"`
	CreateAt       int64  `json:"create_at"`
	UpdateAt       int64  `json:"update_at"`
	DeleteAt       int64  `json:"delete_at"`
	AcademicUnitId string `json:"academic_unit_id"`
	UserId         string `json:"user_id"`
	StartAt        int64  `json:"start_at"`
	EndAt          int64  `json:"end_at,omitempty"`
}

func (m *AcademicUnitMember) PreSave() {
	preSaveMembership(&m.Id, &m.CreateAt, &m.UpdateAt, &m.StartAt)
}

func (m *AcademicUnitMember) PreUpdate() {
	preUpdate(&m.UpdateAt)
}

func (m *AcademicUnitMember) IsValid() *AppError {
	const where = "AcademicUnitMember.IsValid"
	if appErr := validatePersistentFields(
		where,
		"academic_unit_member",
		m.Id,
		m.CreateAt,
		m.UpdateAt,
	); appErr != nil {
		return appErr
	}
	details := "id=" + m.Id
	if !IsValidId(m.AcademicUnitId) {
		return invalidModelError(
			where,
			"academic_unit_member",
			"academic_unit_id",
			"must be a valid identifier",
			details,
		)
	}
	if !IsValidId(m.UserId) {
		return invalidModelError(
			where,
			"academic_unit_member",
			"user_id",
			"must be a valid identifier",
			details,
		)
	}
	return validateEffectiveTimes(
		where,
		"academic_unit_member",
		details,
		m.StartAt,
		m.EndAt,
	)
}

func (m *AcademicUnitMember) IsActiveAt(now int64) bool {
	return m != nil && m.DeleteAt == 0 && m.StartAt <= now && (m.EndAt == 0 || now < m.EndAt)
}

func (m *AcademicUnitMember) Auditable() map[string]any {
	fields := auditFields(m.Id, m.CreateAt, m.UpdateAt, m.DeleteAt)
	fields["academic_unit_id"] = m.AcademicUnitId
	fields["user_id"] = m.UserId
	fields["start_at"] = m.StartAt
	fields["end_at"] = m.EndAt
	return fields
}

var _ Auditable = (*AcademicUnitMember)(nil)
