// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only
//
// Adapted from Mattermost server/channels/store/sqlstore/channel_store.go
// member operations. Proctor adds serialized enrollment transfer and durable
// history with one active class per user and academic period.

package sqlstore

import (
	"context"
	"database/sql"
	"fmt"

	sq "github.com/Masterminds/squirrel"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

type SqlClassMemberStore struct {
	*SqlStore
	query sq.SelectBuilder
}

type classMemberRow struct {
	ID               string `db:"id"`
	CreateAt         int64  `db:"create_at"`
	UpdateAt         int64  `db:"update_at"`
	DeleteAt         int64  `db:"delete_at"`
	Revision         int64  `db:"revision"`
	ClassID          string `db:"class_id"`
	AcademicPeriodID string `db:"academic_period_id"`
	UserID           string `db:"user_id"`
	StartAt          int64  `db:"start_at"`
	EndAt            int64  `db:"end_at"`
}

func classMemberColumns() []string {
	return []string{
		"class_members.id", "class_members.create_at", "class_members.update_at",
		"class_members.delete_at", "class_members.revision", "class_members.class_id",
		"class_members.academic_period_id", "class_members.user_id",
		"class_members.start_at", "class_members.end_at",
	}
}

func newSqlClassMemberStore(ss *SqlStore) store.ClassMemberStore {
	s := &SqlClassMemberStore{SqlStore: ss}
	s.query = s.getQueryBuilder().Select(classMemberColumns()...).From("class_members")
	return s
}

func (s SqlClassMemberStore) Enroll(
	ctx context.Context,
	member *model.ClassMember,
) (*store.ClassEnrollmentResult, error) {
	if member == nil || member.Id != "" || !model.IsValidId(member.ClassId) ||
		!model.IsValidId(member.UserId) {
		return nil, store.NewErrInvalidInput("class_member", "value", nil)
	}
	candidate := *member
	candidate.PreSave()
	return s.enroll(ctx, &candidate, "", 0)
}

func (s SqlClassMemberStore) EnrollWithAudit(
	ctx context.Context,
	input *store.ClassMemberEnrollment,
) (*store.ClassEnrollmentResult, error) {
	if input == nil || input.Member == nil || !model.IsValidId(input.AuditEventID) || input.AuditAt <= 0 {
		return nil, store.NewErrInvalidInput("class_member", "enrollment", nil)
	}
	candidate := *input.Member
	if !model.IsValidId(candidate.Id) || candidate.CreateAt <= 0 || candidate.UpdateAt <= 0 || candidate.Revision <= 0 ||
		!model.IsValidId(candidate.ClassId) || !model.IsValidId(candidate.UserId) {
		return nil, store.NewErrInvalidInput("class_member", "value", nil)
	}
	return s.enroll(ctx, &candidate, input.AuditEventID, input.AuditAt)
}

func (s SqlClassMemberStore) enroll(
	ctx context.Context,
	candidate *model.ClassMember,
	auditEventID string,
	auditAt int64,
) (*store.ClassEnrollmentResult, error) {
	tx, err := s.GetMaster().Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin class enrollment: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if err := lockAffiliationLifecycle(ctx, tx); err != nil {
		return nil, err
	}
	if err := lockClassLifecycle(ctx, tx); err != nil {
		return nil, err
	}
	if err := tx.Get(ctx, &candidate.AcademicPeriodId, `
		SELECT academic_period_id FROM classes WHERE id = ? AND delete_at = 0`,
		candidate.ClassId,
	); err != nil {
		return nil, translateError("class", candidate.ClassId, err)
	}
	if err := lockClassEnrollment(ctx, tx, candidate.UserId, candidate.AcademicPeriodId); err != nil {
		return nil, err
	}
	if appErr := candidate.IsValid(); appErr != nil {
		return nil, appErr
	}
	var student bool
	if err := tx.Get(ctx, &student, `SELECT EXISTS (
		SELECT 1 FROM affiliations WHERE user_id = ? AND kind = ? AND delete_at = 0
		 AND start_at <= ? AND end_at = 0
	)`, candidate.UserId, model.AffiliationStudent, candidate.StartAt); err != nil {
		return nil, fmt.Errorf("validate student affiliation: %w", err)
	}
	if !student {
		return nil, store.NewErrConflict("class_member", "class_member_student_affiliation_required", nil)
	}

	var previousRow classMemberRow
	err = tx.Get(ctx, &previousRow, `
		SELECT id, create_at, update_at, delete_at, revision, class_id,
		       academic_period_id, user_id, start_at, end_at
		  FROM class_members
		 WHERE user_id = ? AND academic_period_id = ?
		   AND delete_at = 0 AND end_at = 0
		 ORDER BY start_at DESC, id
		 LIMIT 1
		 FOR UPDATE`,
		candidate.UserId, candidate.AcademicPeriodId,
	)
	var previous *model.ClassMember
	switch {
	case err == nil:
		previous = previousRow.model()
		if previous.ClassId == candidate.ClassId {
			return nil, store.NewErrConflict(
				"class_member",
				"class_members_one_active_class_per_period_key",
				nil,
			)
		}
		if candidate.StartAt <= previous.StartAt {
			return nil, store.NewErrConflict(
				"class_member",
				"class_members_transfer_time",
				nil,
			)
		}
		if _, err := tx.Exec(ctx, `
			UPDATE class_members
			SET update_at = ?, end_at = ?, revision = revision + 1
			 WHERE id = ? AND delete_at = 0 AND end_at = 0`,
			candidate.StartAt, candidate.StartAt, previous.Id,
		); err != nil {
			return nil, fmt.Errorf("end previous class enrollment: %w", err)
		}
		previous.UpdateAt = candidate.StartAt
		previous.EndAt = candidate.StartAt
		previous.Revision++
	case err != nil && !isNoRows(err):
		return nil, fmt.Errorf("find current class enrollment: %w", err)
	}
	var overlaps bool
	excludedID := ""
	if previous != nil {
		excludedID = previous.Id
	}
	if err := tx.Get(ctx, &overlaps, `
		SELECT EXISTS (
			SELECT 1
			  FROM class_members
			 WHERE user_id = ? AND academic_period_id = ? AND delete_at = 0
			   AND (? = '' OR id <> ?)
			   AND (end_at = 0 OR end_at > ?)
			   AND (? = 0 OR start_at < ?)
		)`,
		candidate.UserId,
		candidate.AcademicPeriodId,
		excludedID,
		excludedID,
		candidate.StartAt,
		candidate.EndAt,
		candidate.EndAt,
	); err != nil {
		return nil, fmt.Errorf("check class enrollment overlap: %w", err)
	}
	if overlaps {
		return nil, store.NewErrConflict(
			"class_member", "class_members_effective_range_overlap", nil,
		)
	}

	row := newClassMemberRow(candidate)
	if _, err := tx.NamedExec(ctx, `
		INSERT INTO class_members (
			id, create_at, update_at, delete_at, revision, class_id,
			academic_period_id, user_id, start_at, end_at
		) VALUES (
			:id, :create_at, :update_at, :delete_at, :revision, :class_id,
			:academic_period_id, :user_id, :start_at, :end_at
		)`, &row); err != nil {
		return nil, fmt.Errorf(
			"save class enrollment: %w",
			translateError("class_member", candidate.Id, err),
		)
	}
	result := &store.ClassEnrollmentResult{Membership: candidate, Previous: previous}
	if auditEventID != "" {
		enrollment := &model.ClassEnrollment{Membership: candidate, Previous: previous}
		encoded, appErr := model.EncodeAuditData(enrollment.Auditable())
		if appErr != nil {
			return nil, appErr
		}
		if _, err := completeAuditEvent(ctx, tx, auditEventID, model.AuditStatusSuccess, "", encoded, auditAt); err != nil {
			return nil, fmt.Errorf("complete class enrollment audit: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit class enrollment: %w", err)
	}
	return result, nil
}

func isNoRows(err error) bool {
	return err == sql.ErrNoRows
}

func (s SqlClassMemberStore) Get(
	ctx context.Context,
	id string,
) (*model.ClassMember, error) {
	var row classMemberRow
	if err := s.GetMaster().GetBuilder(ctx, &row, s.query.Where(sq.Eq{
		"class_members.id": id, "class_members.delete_at": int64(0),
	})); err != nil {
		return nil, translateError("class_member", id, err)
	}
	return row.model(), nil
}

func (s SqlClassMemberStore) ListByUser(
	ctx context.Context,
	userID string,
) ([]*model.ClassMember, error) {
	return s.selectMembers(ctx, s.query.Where(sq.Eq{
		"class_members.user_id": userID, "class_members.delete_at": int64(0),
	}).OrderBy("class_members.start_at DESC", "class_members.id"))
}

func (s SqlClassMemberStore) ListByClass(
	ctx context.Context,
	classID string,
	at int64,
) ([]*model.ClassMember, error) {
	query := s.query.Where(sq.Eq{
		"class_members.class_id": classID, "class_members.delete_at": int64(0),
	})
	if at > 0 {
		query = query.Where(sq.LtOrEq{"class_members.start_at": at}).
			Where("(class_members.end_at = 0 OR class_members.end_at > ?)", at)
	}
	return s.selectMembers(ctx, query.OrderBy("class_members.user_id", "class_members.id"))
}

func (s SqlClassMemberStore) ListActiveByUser(
	ctx context.Context,
	userID string,
	at int64,
) ([]*model.ClassMember, error) {
	return s.selectMembers(ctx, s.query.Where(sq.Eq{
		"class_members.user_id": userID, "class_members.delete_at": int64(0),
	}).Where(sq.LtOrEq{"class_members.start_at": at}).
		Where("(class_members.end_at = 0 OR class_members.end_at > ?)", at).
		OrderBy("class_members.academic_period_id", "class_members.id"))
}

func (s SqlClassMemberStore) End(
	ctx context.Context,
	id string,
	expectedRevision int64,
	endAt int64,
) (*model.ClassMember, error) {
	if !model.IsValidId(id) || expectedRevision <= 0 || endAt <= 0 {
		return nil, store.NewErrInvalidInput("class_member", "end", nil)
	}
	tx, err := s.GetMaster().Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin class member end: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	var row classMemberRow
	if err := tx.GetBuilder(ctx, &row, s.query.Where(sq.Eq{
		"class_members.id": id, "class_members.delete_at": int64(0),
	})); err != nil {
		return nil, translateError("class_member", id, err)
	}
	if err := lockClassEnrollment(ctx, tx, row.UserID, row.AcademicPeriodID); err != nil {
		return nil, err
	}
	ended, err := s.endClassMember(ctx, tx, id, expectedRevision, endAt)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit class member end: %w", err)
	}
	return ended, nil
}

func (s SqlClassMemberStore) EndWithAudit(
	ctx context.Context,
	input *store.ClassMemberEnd,
) (*model.ClassMember, error) {
	if input == nil || !model.IsValidId(input.ID) || input.ExpectedRevision <= 0 ||
		input.EndAt <= 0 || !model.IsValidId(input.AuditEventID) || input.AuditAt <= 0 {
		return nil, store.NewErrInvalidInput("class_member", "end", nil)
	}
	tx, err := s.GetMaster().Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin class member audited end: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	var row classMemberRow
	if err := tx.GetBuilder(ctx, &row, s.query.Where(sq.Eq{
		"class_members.id": input.ID, "class_members.delete_at": int64(0),
	})); err != nil {
		return nil, translateError("class_member", input.ID, err)
	}
	if err := lockClassEnrollment(ctx, tx, row.UserID, row.AcademicPeriodID); err != nil {
		return nil, err
	}
	ended, err := s.endClassMember(ctx, tx, input.ID, input.ExpectedRevision, input.EndAt)
	if err != nil {
		return nil, err
	}
	encoded, appErr := model.EncodeAuditData(ended.Auditable())
	if appErr != nil {
		return nil, appErr
	}
	if _, err := completeAuditEvent(ctx, tx, input.AuditEventID, model.AuditStatusSuccess, "", encoded, input.AuditAt); err != nil {
		return nil, fmt.Errorf("complete class member end audit: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit class member audited end: %w", err)
	}
	return ended, nil
}

func (s SqlClassMemberStore) endClassMember(
	ctx context.Context,
	executor sqlxExecutor,
	id string,
	expectedRevision int64,
	endAt int64,
) (*model.ClassMember, error) {
	var row classMemberRow
	if err := executor.GetBuilder(ctx, &row, s.query.Where(sq.Eq{
		"class_members.id": id, "class_members.delete_at": int64(0),
	})); err != nil {
		return nil, translateError("class_member", id, err)
	}
	current := row.model()
	if current.Revision != expectedRevision {
		return nil, store.NewErrConflict("class_member", "class_member_changed", nil)
	}
	if endAt <= current.StartAt || (current.EndAt != 0 && endAt >= current.EndAt) {
		return nil, store.NewErrConflict("class_member", "class_member_end_time", nil)
	}
	result, err := executor.Exec(ctx, `
		UPDATE class_members
		   SET update_at = ?, end_at = ?, revision = revision + 1
		 WHERE id = ? AND delete_at = 0 AND revision = ?`,
		endAt, endAt, id, expectedRevision,
	)
	if err != nil {
		return nil, fmt.Errorf("end class member: %w", err)
	}
	if err := requireAffected(result, "class_member", id); err != nil {
		return nil, err
	}
	current.UpdateAt, current.EndAt, current.Revision = endAt, endAt, expectedRevision+1
	return current, nil
}

func lockClassEnrollment(ctx context.Context, executor sqlxExecutor, userID, periodID string) error {
	if _, err := executor.Exec(
		ctx,
		"SELECT pg_advisory_xact_lock(hashtext(?))",
		"proctor:class-enrollment:"+userID+":"+periodID,
	); err != nil {
		return fmt.Errorf("lock class enrollment: %w", err)
	}
	return nil
}

func (s SqlClassMemberStore) selectMembers(
	ctx context.Context,
	query sq.SelectBuilder,
) ([]*model.ClassMember, error) {
	rows := []classMemberRow{}
	if err := s.GetMaster().SelectBuilder(ctx, &rows, query); err != nil {
		return nil, fmt.Errorf("list class members: %w", err)
	}
	result := make([]*model.ClassMember, 0, len(rows))
	for _, row := range rows {
		result = append(result, row.model())
	}
	return result, nil
}

func newClassMemberRow(m *model.ClassMember) classMemberRow {
	return classMemberRow{
		ID: m.Id, CreateAt: m.CreateAt, UpdateAt: m.UpdateAt,
		DeleteAt: m.DeleteAt, ClassID: m.ClassId,
		Revision:         m.Revision,
		AcademicPeriodID: m.AcademicPeriodId, UserID: m.UserId,
		StartAt: m.StartAt, EndAt: m.EndAt,
	}
}

func (r classMemberRow) model() *model.ClassMember {
	return &model.ClassMember{
		Id: r.ID, CreateAt: r.CreateAt, UpdateAt: r.UpdateAt,
		DeleteAt: r.DeleteAt, ClassId: r.ClassID,
		Revision:         r.Revision,
		AcademicPeriodId: r.AcademicPeriodID, UserId: r.UserID,
		StartAt: r.StartAt, EndAt: r.EndAt,
	}
}

var _ store.ClassMemberStore = (*SqlClassMemberStore)(nil)
