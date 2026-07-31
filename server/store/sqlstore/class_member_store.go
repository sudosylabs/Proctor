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
	ClassID          string `db:"class_id"`
	AcademicPeriodID string `db:"academic_period_id"`
	UserID           string `db:"user_id"`
	StartAt          int64  `db:"start_at"`
	EndAt            int64  `db:"end_at"`
}

func classMemberColumns() []string {
	return []string{
		"class_members.id", "class_members.create_at", "class_members.update_at",
		"class_members.delete_at", "class_members.class_id",
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
	tx, err := s.GetMaster().Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin class enrollment: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if err := tx.Get(ctx, &candidate.AcademicPeriodId, `
		SELECT academic_period_id FROM classes WHERE id = ? AND delete_at = 0`,
		candidate.ClassId,
	); err != nil {
		return nil, translateError("class", candidate.ClassId, err)
	}
	if _, err := tx.Exec(
		ctx,
		"SELECT pg_advisory_xact_lock(hashtext(?))",
		"proctor:class-enrollment:"+candidate.UserId+":"+candidate.AcademicPeriodId,
	); err != nil {
		return nil, fmt.Errorf("lock class enrollment: %w", err)
	}
	candidate.PreSave()
	if appErr := candidate.IsValid(); appErr != nil {
		return nil, appErr
	}

	var previousRow classMemberRow
	err = tx.Get(ctx, &previousRow, `
		SELECT id, create_at, update_at, delete_at, class_id,
		       academic_period_id, user_id, start_at, end_at
		  FROM class_members
		 WHERE user_id = ? AND academic_period_id = ?
		   AND delete_at = 0 AND end_at = 0
		 FOR UPDATE`,
		candidate.UserId,
		candidate.AcademicPeriodId,
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
			   SET update_at = ?, end_at = ?
			 WHERE id = ? AND delete_at = 0 AND end_at = 0`,
			candidate.StartAt, candidate.StartAt, previous.Id,
		); err != nil {
			return nil, fmt.Errorf("end previous class enrollment: %w", err)
		}
		previous.UpdateAt = candidate.StartAt
		previous.EndAt = candidate.StartAt
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

	row := newClassMemberRow(&candidate)
	if _, err := tx.NamedExec(ctx, `
		INSERT INTO class_members (
			id, create_at, update_at, delete_at, class_id,
			academic_period_id, user_id, start_at, end_at
		) VALUES (
			:id, :create_at, :update_at, :delete_at, :class_id,
			:academic_period_id, :user_id, :start_at, :end_at
		)`, &row); err != nil {
		return nil, fmt.Errorf(
			"save class enrollment: %w",
			translateError("class_member", candidate.Id, err),
		)
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit class enrollment: %w", err)
	}
	return &store.ClassEnrollmentResult{Membership: &candidate, Previous: previous}, nil
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
	endAt int64,
) (*model.ClassMember, error) {
	if endAt <= 0 {
		return nil, store.NewErrInvalidInput("class_member", "end_at", endAt)
	}
	result, err := s.GetMaster().Exec(ctx, `
		UPDATE class_members
		   SET update_at = ?, end_at = ?
		 WHERE id = ? AND delete_at = 0 AND start_at < ?
		   AND (end_at = 0 OR end_at > ?)`,
		endAt, endAt, id, endAt, endAt,
	)
	if err != nil {
		return nil, fmt.Errorf(
			"end class enrollment: %w",
			translateError("class_member", id, err),
		)
	}
	if err := requireAffected(result, "class_member", id); err != nil {
		return nil, err
	}
	return s.Get(ctx, id)
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
		AcademicPeriodID: m.AcademicPeriodId, UserID: m.UserId,
		StartAt: m.StartAt, EndAt: m.EndAt,
	}
}

func (r classMemberRow) model() *model.ClassMember {
	return &model.ClassMember{
		Id: r.ID, CreateAt: r.CreateAt, UpdateAt: r.UpdateAt,
		DeleteAt: r.DeleteAt, ClassId: r.ClassID,
		AcademicPeriodId: r.AcademicPeriodID, UserId: r.UserID,
		StartAt: r.StartAt, EndAt: r.EndAt,
	}
}

var _ store.ClassMemberStore = (*SqlClassMemberStore)(nil)
