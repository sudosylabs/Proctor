// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only
//
// Adapted from Mattermost server/channels/store/sqlstore/team_store.go member
// operations for Proctor's time-bounded academic-unit membership.

package sqlstore

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	sq "github.com/Masterminds/squirrel"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

type SqlAcademicUnitMemberStore struct {
	*SqlStore
	query sq.SelectBuilder
}

// academicUnitMemberRow is the legacy integer-millisecond column layout.
type academicUnitMemberRow struct {
	ID             string       `db:"id"`
	CreatedAt      time.Time    `db:"created_at"`
	UpdatedAt      time.Time    `db:"updated_at"`
	ArchivedAt     sql.NullTime `db:"archived_at"`
	Revision       int64        `db:"revision"`
	AcademicUnitID string       `db:"academic_unit_id"`
	UserID         string       `db:"user_id"`
	StartAt        time.Time    `db:"start_at"`
	EndAt          sql.NullTime `db:"end_at"`
}

func academicUnitMemberColumns() []string {
	return []string{
		"academic_unit_members.id", "academic_unit_members.created_at",
		"academic_unit_members.updated_at", "academic_unit_members.archived_at",
		"academic_unit_members.revision",
		"academic_unit_members.academic_unit_id", "academic_unit_members.user_id",
		"academic_unit_members.start_at", "academic_unit_members.end_at",
	}
}

const academicUnitMemberLifecycleLock = "proctor:academic-unit-member-lifecycle"

func (s SqlAcademicUnitMemberStore) Create(ctx context.Context, input *store.AcademicUnitMemberCreation) (*model.AcademicUnitMember, error) {
	if input == nil || input.Member == nil || !model.IsValidId(input.AuditEventID) || input.AuditAt <= 0 {
		return nil, store.NewErrInvalidInput("academic_unit_member", "creation", nil)
	}
	if !input.Member.ID.IsValid() {
		return nil, store.NewErrInvalidInput("academic_unit_member", "id", input.Member.ID.String())
	}
	candidate := *input.Member
	if err := candidate.Validate(); err != nil {
		return nil, store.NewErrInvalidInput("academic_unit_member", "value", nil).Wrap(err)
	}
	encoded, appErr := model.EncodeAuditData(candidate.Auditable())
	if appErr != nil {
		return nil, appErr
	}
	tx, err := s.GetMaster().Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin academic unit member creation: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if err := lockAcademicUnitMemberLifecycle(ctx, tx); err != nil {
		return nil, err
	}
	if err := lockAcademicUnitMember(ctx, tx, candidate.AcademicUnitID.String(), candidate.UserID.String()); err != nil {
		return nil, err
	}
	if err := ensureAcademicUnitMemberRangeAvailable(ctx, tx, &candidate); err != nil {
		return nil, err
	}
	row := newAcademicUnitMemberRow(&candidate)
	if _, err := tx.NamedExec(ctx, `INSERT INTO academic_unit_members (
		id, created_at, updated_at, archived_at, revision, academic_unit_id, user_id, start_at, end_at
	) VALUES (
		:id, :created_at, :updated_at, :archived_at, :revision, :academic_unit_id, :user_id, :start_at, :end_at
	)`, &row); err != nil {
		return nil, fmt.Errorf("create academic unit member: %w", translateError("academic_unit_member", candidate.ID.String(), err))
	}
	if _, err := completeAuditEvent(ctx, tx, input.AuditEventID, model.AuditStatusSuccess, "", encoded, input.AuditAt); err != nil {
		return nil, fmt.Errorf("complete academic unit member creation audit: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit academic unit member creation: %w", err)
	}
	return &candidate, nil
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
	if member == nil {
		return nil, store.NewErrInvalidInput("academic_unit_member", "value", nil)
	}
	if !member.ID.IsZero() {
		return nil, store.NewErrInvalidInput("academic_unit_member", "id", member.ID.String())
	}
	id, err := model.ParseAcademicUnitMemberID(model.NewId())
	if err != nil {
		return nil, err
	}
	candidate := *member
	candidate.PrepareCreate(id, model.NowUTC())
	if err := candidate.Validate(); err != nil {
		return nil, store.NewErrInvalidInput("academic_unit_member", "value", nil).Wrap(err)
	}
	row := newAcademicUnitMemberRow(&candidate)
	tx, err := s.GetMaster().Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin academic unit member save: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if err := lockAcademicUnitMemberLifecycle(ctx, tx); err != nil {
		return nil, err
	}
	if err := lockAcademicUnitMember(ctx, tx, candidate.AcademicUnitID.String(), candidate.UserID.String()); err != nil {
		return nil, err
	}
	if err := ensureAcademicUnitMemberRangeAvailable(ctx, tx, &candidate); err != nil {
		return nil, err
	}
	if _, err := tx.NamedExec(ctx, `
		INSERT INTO academic_unit_members (
			id, created_at, updated_at, archived_at, revision, academic_unit_id,
			user_id, start_at, end_at
		) VALUES (
			:id, :created_at, :updated_at, :archived_at, :revision, :academic_unit_id,
			:user_id, :start_at, :end_at
		)`, &row); err != nil {
		return nil, fmt.Errorf(
			"save academic unit member: %w",
			translateError("academic_unit_member", candidate.ID.String(), err),
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
		"academic_unit_members.id":          id,
		"academic_unit_members.archived_at": nil,
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
		"academic_unit_members.user_id":     userID,
		"academic_unit_members.archived_at": nil,
	}).OrderBy("academic_unit_members.start_at DESC", "academic_unit_members.id"))
}

func (s SqlAcademicUnitMemberStore) ListByAcademicUnit(
	ctx context.Context,
	unitID string,
	at int64,
) ([]*model.AcademicUnitMember, error) {
	query := s.query.Where(sq.Eq{
		"academic_unit_members.academic_unit_id": unitID,
		"academic_unit_members.archived_at":      nil,
	})
	if at > 0 {
		activeAt := model.TimeFromMillis(at)
		query = query.Where(sq.LtOrEq{"academic_unit_members.start_at": activeAt}).
			Where("(academic_unit_members.end_at IS NULL OR academic_unit_members.end_at > ?)", activeAt)
	}
	return s.selectMembers(ctx, query.OrderBy("academic_unit_members.user_id", "academic_unit_members.id"))
}

func (s SqlAcademicUnitMemberStore) ListActiveByUser(
	ctx context.Context,
	userID string,
	at int64,
) ([]*model.AcademicUnitMember, error) {
	activeAt := model.TimeFromMillis(at)
	return s.selectMembers(ctx, s.query.Where(sq.Eq{
		"academic_unit_members.user_id":     userID,
		"academic_unit_members.archived_at": nil,
	}).Where(sq.LtOrEq{"academic_unit_members.start_at": activeAt}).
		Where("(academic_unit_members.end_at IS NULL OR academic_unit_members.end_at > ?)", activeAt).
		OrderBy("academic_unit_members.academic_unit_id", "academic_unit_members.id"))
}

func (s SqlAcademicUnitMemberStore) End(
	ctx context.Context,
	id string,
	expectedRevision int64,
	endAt int64,
) (*model.AcademicUnitMember, error) {
	if !model.IsValidId(id) || expectedRevision <= 0 || endAt <= 0 {
		return nil, store.NewErrInvalidInput("academic_unit_member", "end", nil)
	}
	tx, err := s.GetMaster().Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin academic unit member end: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if err := lockAcademicUnitMemberLifecycle(ctx, tx); err != nil {
		return nil, err
	}
	ended, err := s.endAcademicUnitMember(ctx, tx, id, expectedRevision, endAt)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit academic unit member end: %w", err)
	}
	return ended, nil
}

func (s SqlAcademicUnitMemberStore) EndWithAudit(ctx context.Context, input *store.AcademicUnitMemberEnd) (*model.AcademicUnitMember, error) {
	if input == nil || !model.IsValidId(input.ID) || input.ExpectedRevision <= 0 || input.EndAt <= 0 || !model.IsValidId(input.AuditEventID) || input.AuditAt <= 0 {
		return nil, store.NewErrInvalidInput("academic_unit_member", "end", nil)
	}
	tx, err := s.GetMaster().Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin academic unit member audited end: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if err := lockAcademicUnitMemberLifecycle(ctx, tx); err != nil {
		return nil, err
	}
	ended, err := s.endAcademicUnitMember(ctx, tx, input.ID, input.ExpectedRevision, input.EndAt)
	if err != nil {
		return nil, err
	}
	encoded, appErr := model.EncodeAuditData(ended.Auditable())
	if appErr != nil {
		return nil, appErr
	}
	if _, err := completeAuditEvent(ctx, tx, input.AuditEventID, model.AuditStatusSuccess, "", encoded, input.AuditAt); err != nil {
		return nil, fmt.Errorf("complete academic unit member end audit: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit academic unit member audited end: %w", err)
	}
	return ended, nil
}

func (s SqlAcademicUnitMemberStore) endAcademicUnitMember(ctx context.Context, tx sqlxExecutor, id string, expectedRevision, endAt int64) (*model.AcademicUnitMember, error) {
	var row academicUnitMemberRow
	if err := tx.GetBuilder(ctx, &row, s.query.Where(sq.Eq{"academic_unit_members.id": id, "academic_unit_members.archived_at": nil})); err != nil {
		return nil, translateError("academic_unit_member", id, err)
	}
	current := row.model()
	if current.Revision != expectedRevision {
		return nil, store.NewErrConflict("academic_unit_member", "academic_unit_member_changed", nil)
	}
	startMillis := model.MillisFromTime(current.StartsAt)
	endMillis := current.EndsAt.Millis()
	if endAt <= startMillis || (endMillis != 0 && endAt >= endMillis) {
		return nil, store.NewErrConflict("academic_unit_member", "academic_unit_member_end_time", nil)
	}
	at := model.TimeFromMillis(endAt)
	result, err := tx.Exec(ctx, `UPDATE academic_unit_members SET updated_at = ?, end_at = ?, revision = revision + 1 WHERE id = ? AND archived_at IS NULL AND revision = ?`, at, at, id, expectedRevision)
	if err != nil {
		return nil, fmt.Errorf("end academic unit member: %w", err)
	}
	if err := requireAffected(result, "academic_unit_member", id); err != nil {
		return nil, err
	}
	current.UpdatedAt = at
	current.EndsAt = model.OptionalTimeFromMillis(endAt)
	current.Revision = expectedRevision + 1
	return current, nil
}

func lockAcademicUnitMemberLifecycle(ctx context.Context, executor sqlxExecutor) error {
	if _, err := executor.Exec(ctx, "SELECT pg_advisory_xact_lock(hashtext(?))", academicUnitMemberLifecycleLock); err != nil {
		return fmt.Errorf("lock academic unit member lifecycle: %w", err)
	}
	return nil
}

func lockAcademicUnitMember(ctx context.Context, executor sqlxExecutor, unitID, userID string) error {
	if _, err := executor.Exec(ctx, "SELECT pg_advisory_xact_lock(hashtext(?))", "proctor:academic-unit-member:"+unitID+":"+userID); err != nil {
		return fmt.Errorf("lock academic unit member: %w", err)
	}
	return nil
}

func ensureAcademicUnitMemberRangeAvailable(ctx context.Context, executor sqlxExecutor, candidate *model.AcademicUnitMember) error {
	startAt := candidate.StartsAt
	endAt := NullTimeFromOptional(candidate.EndsAt)
	var overlaps bool
	if err := executor.Get(ctx, &overlaps, `SELECT EXISTS (
		SELECT 1 FROM academic_unit_members WHERE academic_unit_id = ? AND user_id = ? AND archived_at IS NULL
		 AND (end_at IS NULL OR end_at > ?) AND (CAST(? AS timestamptz) IS NULL OR start_at < ?)
	)`, candidate.AcademicUnitID.String(), candidate.UserID.String(), startAt, endAt, endAt); err != nil {
		return fmt.Errorf("check academic unit member overlap: %w", err)
	}
	if overlaps {
		return store.NewErrConflict("academic_unit_member", "academic_unit_members_effective_range_overlap", nil)
	}
	return nil
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
		ID:             m.ID.String(),
		CreatedAt:      UTCTime(m.CreatedAt),
		UpdatedAt:      UTCTime(m.UpdatedAt),
		ArchivedAt:     NullTimeFromOptional(m.ArchivedAt),
		AcademicUnitID: m.AcademicUnitID.String(),
		Revision:       m.Revision,
		UserID:         m.UserID.String(),
		StartAt:        UTCTime(m.StartsAt),
		EndAt:          NullTimeFromOptional(m.EndsAt),
	}
}

func (r academicUnitMemberRow) model() *model.AcademicUnitMember {
	id, err := model.ParseAcademicUnitMemberID(r.ID)
	if err != nil {
		id = model.AcademicUnitMemberID(r.ID)
	}
	unitID, err := model.ParseAcademicUnitID(r.AcademicUnitID)
	if err != nil {
		unitID = model.AcademicUnitID(r.AcademicUnitID)
	}
	userID, err := model.ParseUserID(r.UserID)
	if err != nil {
		userID = model.UserID(r.UserID)
	}
	return &model.AcademicUnitMember{
		ID:             id,
		CreatedAt:      r.CreatedAt.UTC(),
		UpdatedAt:      r.UpdatedAt.UTC(),
		ArchivedAt:     OptionalTimeFromNullTime(r.ArchivedAt),
		AcademicUnitID: unitID,
		Revision:       r.Revision,
		UserID:         userID,
		StartsAt:       r.StartAt.UTC(),
		EndsAt:         OptionalTimeFromNullTime(r.EndAt),
	}
}

var _ store.AcademicUnitMemberStore = (*SqlAcademicUnitMemberStore)(nil)
