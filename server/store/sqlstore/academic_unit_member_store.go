// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only
//
// Adapted from Mattermost server/channels/store/sqlstore/team_store.go member
// operations for Proctor's time-bounded academic-unit membership.

package sqlstore

import (
	"context"
	"fmt"

	sq "github.com/Masterminds/squirrel"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

type SqlAcademicUnitMemberStore struct {
	*SqlStore
	query sq.SelectBuilder
}

type academicUnitMemberRow struct {
	ID             string `db:"id"`
	CreateAt       int64  `db:"create_at"`
	UpdateAt       int64  `db:"update_at"`
	DeleteAt       int64  `db:"delete_at"`
	AcademicUnitID string `db:"academic_unit_id"`
	UserID         string `db:"user_id"`
	StartAt        int64  `db:"start_at"`
	EndAt          int64  `db:"end_at"`
}

func academicUnitMemberColumns() []string {
	return []string{
		"academic_unit_members.id", "academic_unit_members.create_at",
		"academic_unit_members.update_at", "academic_unit_members.delete_at",
		"academic_unit_members.academic_unit_id", "academic_unit_members.user_id",
		"academic_unit_members.start_at", "academic_unit_members.end_at",
	}
}

func newSqlAcademicUnitMemberStore(ss *SqlStore) store.AcademicUnitMemberStore {
	s := &SqlAcademicUnitMemberStore{SqlStore: ss}
	s.query = s.getQueryBuilder().
		Select(academicUnitMemberColumns()...).
		From("academic_unit_members")
	return s
}

func (s SqlAcademicUnitMemberStore) Save(
	ctx context.Context,
	member *model.AcademicUnitMember,
) (*model.AcademicUnitMember, error) {
	if member == nil || member.Id != "" {
		return nil, store.NewErrInvalidInput("academic_unit_member", "value", nil)
	}
	candidate := *member
	candidate.PreSave()
	if appErr := candidate.IsValid(); appErr != nil {
		return nil, appErr
	}
	row := newAcademicUnitMemberRow(&candidate)
	tx, err := s.GetMaster().Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin academic unit member save: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.Exec(
		ctx,
		"SELECT pg_advisory_xact_lock(hashtext(?))",
		"proctor:academic-unit-member:"+candidate.AcademicUnitId+":"+candidate.UserId,
	); err != nil {
		return nil, fmt.Errorf("lock academic unit member: %w", err)
	}
	var overlaps bool
	if err := tx.Get(ctx, &overlaps, `
		SELECT EXISTS (
			SELECT 1
			  FROM academic_unit_members
			 WHERE academic_unit_id = ? AND user_id = ? AND delete_at = 0
			   AND (end_at = 0 OR end_at > ?)
			   AND (? = 0 OR start_at < ?)
		)`,
		candidate.AcademicUnitId,
		candidate.UserId,
		candidate.StartAt,
		candidate.EndAt,
		candidate.EndAt,
	); err != nil {
		return nil, fmt.Errorf("check academic unit member overlap: %w", err)
	}
	if overlaps {
		return nil, store.NewErrConflict(
			"academic_unit_member",
			"academic_unit_members_effective_range_overlap",
			nil,
		)
	}
	if _, err := tx.NamedExec(ctx, `
		INSERT INTO academic_unit_members (
			id, create_at, update_at, delete_at, academic_unit_id,
			user_id, start_at, end_at
		) VALUES (
			:id, :create_at, :update_at, :delete_at, :academic_unit_id,
			:user_id, :start_at, :end_at
		)`, &row); err != nil {
		return nil, fmt.Errorf(
			"save academic unit member: %w",
			translateError("academic_unit_member", candidate.Id, err),
		)
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit academic unit member save: %w", err)
	}
	return &candidate, nil
}

func (s SqlAcademicUnitMemberStore) Get(
	ctx context.Context,
	id string,
) (*model.AcademicUnitMember, error) {
	var row academicUnitMemberRow
	if err := s.GetMaster().GetBuilder(ctx, &row, s.query.Where(sq.Eq{
		"academic_unit_members.id":        id,
		"academic_unit_members.delete_at": int64(0),
	})); err != nil {
		return nil, translateError("academic_unit_member", id, err)
	}
	return row.model(), nil
}

func (s SqlAcademicUnitMemberStore) ListByUser(
	ctx context.Context,
	userID string,
) ([]*model.AcademicUnitMember, error) {
	return s.selectMembers(ctx, s.query.Where(sq.Eq{
		"academic_unit_members.user_id":   userID,
		"academic_unit_members.delete_at": int64(0),
	}).OrderBy("academic_unit_members.start_at DESC", "academic_unit_members.id"))
}

func (s SqlAcademicUnitMemberStore) ListByAcademicUnit(
	ctx context.Context,
	unitID string,
	at int64,
) ([]*model.AcademicUnitMember, error) {
	query := s.query.Where(sq.Eq{
		"academic_unit_members.academic_unit_id": unitID,
		"academic_unit_members.delete_at":        int64(0),
	})
	if at > 0 {
		query = query.Where(sq.LtOrEq{"academic_unit_members.start_at": at}).
			Where("(academic_unit_members.end_at = 0 OR academic_unit_members.end_at > ?)", at)
	}
	return s.selectMembers(ctx, query.OrderBy("academic_unit_members.user_id", "academic_unit_members.id"))
}

func (s SqlAcademicUnitMemberStore) ListActiveByUser(
	ctx context.Context,
	userID string,
	at int64,
) ([]*model.AcademicUnitMember, error) {
	return s.selectMembers(ctx, s.query.Where(sq.Eq{
		"academic_unit_members.user_id":   userID,
		"academic_unit_members.delete_at": int64(0),
	}).Where(sq.LtOrEq{"academic_unit_members.start_at": at}).
		Where("(academic_unit_members.end_at = 0 OR academic_unit_members.end_at > ?)", at).
		OrderBy("academic_unit_members.academic_unit_id", "academic_unit_members.id"))
}

func (s SqlAcademicUnitMemberStore) End(
	ctx context.Context,
	id string,
	endAt int64,
) (*model.AcademicUnitMember, error) {
	if endAt <= 0 {
		return nil, store.NewErrInvalidInput("academic_unit_member", "end_at", endAt)
	}
	result, err := s.GetMaster().Exec(ctx, `
		UPDATE academic_unit_members
		   SET update_at = ?, end_at = ?
		 WHERE id = ? AND delete_at = 0 AND start_at < ?
		   AND (end_at = 0 OR end_at > ?)`,
		endAt, endAt, id, endAt, endAt,
	)
	if err != nil {
		return nil, fmt.Errorf(
			"end academic unit member: %w",
			translateError("academic_unit_member", id, err),
		)
	}
	if err := requireAffected(result, "academic_unit_member", id); err != nil {
		return nil, err
	}
	return s.Get(ctx, id)
}

func (s SqlAcademicUnitMemberStore) selectMembers(
	ctx context.Context,
	query sq.SelectBuilder,
) ([]*model.AcademicUnitMember, error) {
	rows := []academicUnitMemberRow{}
	if err := s.GetMaster().SelectBuilder(ctx, &rows, query); err != nil {
		return nil, fmt.Errorf("list academic unit members: %w", err)
	}
	result := make([]*model.AcademicUnitMember, 0, len(rows))
	for _, row := range rows {
		result = append(result, row.model())
	}
	return result, nil
}

func newAcademicUnitMemberRow(m *model.AcademicUnitMember) academicUnitMemberRow {
	return academicUnitMemberRow{
		ID: m.Id, CreateAt: m.CreateAt, UpdateAt: m.UpdateAt,
		DeleteAt: m.DeleteAt, AcademicUnitID: m.AcademicUnitId,
		UserID: m.UserId, StartAt: m.StartAt, EndAt: m.EndAt,
	}
}

func (r academicUnitMemberRow) model() *model.AcademicUnitMember {
	return &model.AcademicUnitMember{
		Id: r.ID, CreateAt: r.CreateAt, UpdateAt: r.UpdateAt,
		DeleteAt: r.DeleteAt, AcademicUnitId: r.AcademicUnitID,
		UserId: r.UserID, StartAt: r.StartAt, EndAt: r.EndAt,
	}
}

var _ store.AcademicUnitMemberStore = (*SqlAcademicUnitMemberStore)(nil)
