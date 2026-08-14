// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package model

import (
	"fmt"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	ExamTitleMaxRunes                = 200
	ExamInstructionsMarkdownMaxBytes = 64 * 1024
)

// Exam is the stable authoring identity owned by one Academic Unit. Creator
// records immutable provenance; OwnerUserID records current responsibility.
type Exam struct {
	ID                ExamID
	AcademicUnitID    AcademicUnitID
	CreatorUserID     UserID
	OwnerUserID       UserID
	DefaultRevisionID ExamRevisionID
	CreatedAt         time.Time
	UpdatedAt         time.Time
	ArchivedAt        OptionalTime
	Revision          int64
}

func NewExam(id ExamID, academicUnitID AcademicUnitID, creatorUserID UserID, at time.Time) (*Exam, error) {
	at = TimeUTC(at)
	exam := &Exam{
		ID: id, AcademicUnitID: academicUnitID, CreatorUserID: creatorUserID,
		OwnerUserID: creatorUserID, CreatedAt: at, UpdatedAt: at, Revision: 1,
	}
	if err := exam.Validate(); err != nil {
		return nil, err
	}
	return exam, nil
}

func (e *Exam) Validate() error {
	const where = "Exam.Validate"
	if e == nil {
		return invalidModelError(where, "exam", "value", "is required", "")
	}
	if !e.ID.IsValid() {
		return invalidModelError(where, "exam", "id", "must be a valid identifier", "")
	}
	details := "id=" + e.ID.String()
	if !e.AcademicUnitID.IsValid() {
		return invalidModelError(where, "exam", "academic_unit_id", "must be a valid identifier", details)
	}
	if !e.CreatorUserID.IsValid() {
		return invalidModelError(where, "exam", "creator_user_id", "must be a valid identifier", details)
	}
	if !e.OwnerUserID.IsValid() {
		return invalidModelError(where, "exam", "owner_user_id", "must be a valid identifier", details)
	}
	if !e.DefaultRevisionID.IsZero() && !e.DefaultRevisionID.IsValid() {
		return invalidModelError(where, "exam", "default_revision_id", "must be empty or a valid identifier", details)
	}
	if e.CreatedAt.IsZero() || e.UpdatedAt.IsZero() || e.UpdatedAt.Before(e.CreatedAt) {
		return invalidModelError(where, "exam", "timestamps", "must be ordered and nonzero", details)
	}
	if e.ArchivedAt.Valid && e.ArchivedAt.Time.Before(e.CreatedAt) {
		return invalidModelError(where, "exam", "archived_at", "must not precede created_at", details)
	}
	if e.Revision < 1 {
		return invalidModelError(where, "exam", "revision", "must be positive", details)
	}
	return nil
}

func (e *Exam) IsArchived() bool { return e != nil && e.ArchivedAt.Valid }

func (e *Exam) Auditable() map[string]any {
	if e == nil {
		return map[string]any{}
	}
	return map[string]any{
		"id": e.ID.String(), "academic_unit_id": e.AcademicUnitID.String(),
		"creator_user_id": e.CreatorUserID.String(), "owner_user_id": e.OwnerUserID.String(),
		"default_revision_id": e.DefaultRevisionID.String(), "revision": e.Revision,
	}
}

func (e *Exam) ResourceID() string {
	if e == nil {
		return ""
	}
	return e.ID.String()
}

// ExamDraft is the single mutable authoring state addressed through its Exam.
// Published Exam Revisions are separate immutable snapshots.
type ExamDraft struct {
	ExamID               ExamID
	Title                string
	InstructionsMarkdown string
	Policy               ExamPolicySet
	BaseRevisionID       ExamRevisionID
	UpdatedAt            time.Time
	Revision             int64
}

func NewExamDraft(examID ExamID, title, instructionsMarkdown string, policy ExamPolicySet, at time.Time) (*ExamDraft, error) {
	draft := &ExamDraft{
		ExamID: examID, Title: strings.TrimSpace(title),
		InstructionsMarkdown: instructionsMarkdown, Policy: policy,
		UpdatedAt: TimeUTC(at), Revision: 1,
	}
	if err := draft.Validate(); err != nil {
		return nil, err
	}
	return draft, nil
}

func (d *ExamDraft) Validate() error {
	const where = "ExamDraft.Validate"
	if d == nil {
		return invalidModelError(where, "exam_draft", "value", "is required", "")
	}
	if !d.ExamID.IsValid() {
		return invalidModelError(where, "exam_draft", "exam_id", "must be a valid identifier", "")
	}
	details := "exam_id=" + d.ExamID.String()
	if d.Title == "" || strings.TrimSpace(d.Title) != d.Title || utf8.RuneCountInString(d.Title) > ExamTitleMaxRunes {
		return invalidModelError(where, "exam_draft", "title", "must contain 1 to 200 Unicode characters", details)
	}
	if !utf8.ValidString(d.InstructionsMarkdown) || len(d.InstructionsMarkdown) > ExamInstructionsMarkdownMaxBytes {
		return invalidModelError(where, "exam_draft", "instructions_markdown", "must be valid bounded UTF-8", details)
	}
	if !d.BaseRevisionID.IsZero() && !d.BaseRevisionID.IsValid() {
		return invalidModelError(where, "exam_draft", "base_revision_id", "must be empty or a valid identifier", details)
	}
	if d.UpdatedAt.IsZero() {
		return invalidModelError(where, "exam_draft", "updated_at", "must be set", details)
	}
	if d.Revision < 1 {
		return invalidModelError(where, "exam_draft", "revision", "must be positive", details)
	}
	if err := d.Policy.Validate(); err != nil {
		return fmt.Errorf("%s: policy: %w", where, err)
	}
	return nil
}

// ExamManager grants authoring responsibility independently of role grants.
type ExamManager struct {
	ExamID          ExamID
	UserID          UserID
	GrantedByUserID UserID
	GrantedAt       time.Time
}

func NewExamManager(examID ExamID, userID, grantedByUserID UserID, at time.Time) (*ExamManager, error) {
	manager := &ExamManager{ExamID: examID, UserID: userID, GrantedByUserID: grantedByUserID, GrantedAt: TimeUTC(at)}
	if err := manager.Validate(); err != nil {
		return nil, err
	}
	return manager, nil
}

func (m *ExamManager) Validate() error {
	const where = "ExamManager.Validate"
	if m == nil || !m.ExamID.IsValid() || !m.UserID.IsValid() || !m.GrantedByUserID.IsValid() || m.GrantedAt.IsZero() {
		return fmt.Errorf("%s: manager identity and grant time are required", where)
	}
	return nil
}

var _ Auditable = (*Exam)(nil)
