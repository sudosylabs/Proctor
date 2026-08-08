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
	"time"

	sq "github.com/Masterminds/squirrel"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

type SqlClassMemberStore struct {
	*SqlStore
	query sq.SelectBuilder
}

// classMemberRow is the legacy integer-millisecond column layout.
type classMemberRow struct {
	ID               string       `db:"id"`
	CreatedAt        time.Time    `db:"created_at"`
	UpdatedAt        time.Time    `db:"updated_at"`
	ArchivedAt       sql.NullTime `db:"archived_at"`
	Revision         int64        `db:"revision"`
	ClassID          string       `db:"class_id"`
	AcademicPeriodID string       `db:"academic_period_id"`
	UserID           string       `db:"user_id"`
	StartAt          time.Time    `db:"start_at"`
	EndAt            sql.NullTime `db:"end_at"`
}

func classMemberColumns() []string {
	return []string{
		"class_members.id", "class_members.created_at", "class_members.updated_at",
		"class_members.archived_at", "class_members.revision", "class_members.class_id",
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
	if member == nil || !member.ID.IsZero() || !member.ClassID.IsValid() || !member.UserID.IsValid() {
		return nil, store.NewErrInvalidInput("class_member", "value", nil)
	}
	id, err := model.ParseClassMemberID(model.NewId())
	if err != nil {
		return nil, err
	}
	candidate := *member
	// AcademicPeriodID is filled from the class inside enroll before Validate.
	candidate.PrepareCreate(id, model.NowUTC())
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
	if !candidate.ID.IsValid() || candidate.CreatedAt.IsZero() || candidate.UpdatedAt.IsZero() ||
		candidate.Revision <= 0 || !candidate.ClassID.IsValid() || !candidate.UserID.IsValid() {
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
	var periodRaw string
	if err := tx.Get(ctx, &periodRaw, `
		SELECT academic_period_id FROM classes WHERE id = ? AND archived_at IS NULL`,
		candidate.ClassID.String(),
	); err != nil {
		return nil, translateError("class", candidate.ClassID.String(), err)
	}
	periodID, err := model.ParseAcademicPeriodID(periodRaw)
	if err != nil {
		return nil, store.NewErrInvalidInput("class_member", "academic_period_id", periodRaw).Wrap(err)
	}
	candidate.AcademicPeriodID = periodID
	if err := lockClassEnrollment(ctx, tx, candidate.UserID.String(), candidate.AcademicPeriodID.String()); err != nil {
		return nil, err
	}
	if err := candidate.Validate(); err != nil {
		return nil, store.NewErrInvalidInput("class_member", "value", nil).Wrap(err)
	}
	startAt := candidate.StartsAt
	endAt := NullTimeFromOptional(candidate.EndsAt)
	var student bool
	if err := tx.Get(ctx, &student, `SELECT EXISTS (
		SELECT 1 FROM affiliations WHERE user_id = ? AND kind = ? AND archived_at IS NULL
		 AND start_at <= ? AND end_at IS NULL
	)`, candidate.UserID.String(), model.AffiliationStudent, startAt); err != nil {
		return nil, fmt.Errorf("validate student affiliation: %w", err)
	}
	if !student {
		return nil, store.NewErrConflict("class_member", "class_member_student_affiliation_required", nil)
	}

	var previousRow classMemberRow
	err = tx.Get(ctx, &previousRow, `
		SELECT id, created_at, updated_at, archived_at, revision, class_id,
		       academic_period_id, user_id, start_at, end_at
		  FROM class_members
		 WHERE user_id = ? AND academic_period_id = ?
		   AND archived_at IS NULL AND end_at IS NULL
		 ORDER BY start_at DESC, id
		 LIMIT 1
		 FOR UPDATE`,
		candidate.UserID.String(), candidate.AcademicPeriodID.String(),
	)
	var previous *model.ClassMember
	switch {
	case err == nil:
		previous = previousRow.model()
		if previous.ClassID == candidate.ClassID {
			return nil, store.NewErrConflict(
				"class_member",
				"class_members_one_active_class_per_period_key",
				nil,
			)
		}
		if !candidate.StartsAt.After(previous.StartsAt) {
			return nil, store.NewErrConflict(
				"class_member",
				"class_members_transfer_time",
				nil,
			)
		}
		if _, err := tx.Exec(ctx, `
			UPDATE class_members
			SET updated_at = ?, end_at = ?, revision = revision + 1
			 WHERE id = ? AND archived_at IS NULL AND end_at IS NULL`,
			candidate.UpdatedAt, startAt, previous.ID.String(),
		); err != nil {
			return nil, fmt.Errorf("end previous class enrollment: %w", err)
		}
		previous.UpdatedAt = candidate.UpdatedAt
		previous.EndsAt = model.OptionalTimeFrom(candidate.StartsAt)
		previous.Revision++
	case err != nil && !isNoRows(err):
		return nil, fmt.Errorf("find current class enrollment: %w", err)
	}
	var overlaps bool
	excludedID := ""
	if previous != nil {
		excludedID = previous.ID.String()
	}
	if err := tx.Get(ctx, &overlaps, `
		SELECT EXISTS (
			SELECT 1
			  FROM class_members
			 WHERE user_id = ? AND academic_period_id = ? AND archived_at IS NULL
			   AND (? = '' OR id <> ?)
			   AND (end_at IS NULL OR end_at > ?)
			   AND (CAST(? AS timestamptz) IS NULL OR start_at < ?)
		)`,
		candidate.UserID.String(),
		candidate.AcademicPeriodID.String(),
		excludedID,
		excludedID,
		startAt,
		endAt,
		endAt,
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
			id, created_at, updated_at, archived_at, revision, class_id,
			academic_period_id, user_id, start_at, end_at
		) VALUES (
			:id, :created_at, :updated_at, :archived_at, :revision, :class_id,
			:academic_period_id, :user_id, :start_at, :end_at
		)`, &row); err != nil {
		return nil, fmt.Errorf(
			"save class enrollment: %w",
			translateError("class_member", candidate.ID.String(), err),
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
		"class_members.id": id, "class_members.archived_at": nil,
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
		"class_members.user_id": userID, "class_members.archived_at": nil,
	}).OrderBy("class_members.start_at DESC", "class_members.id"))
}

func (s SqlClassMemberStore) ListByClass(
	ctx context.Context,
	classID string,
	at int64,
) ([]*model.ClassMember, error) {
	query := s.query.Where(sq.Eq{
		"class_members.class_id": classID, "class_members.archived_at": nil,
	})
	if at > 0 {
		activeAt := model.TimeFromMillis(at)
		query = query.Where(sq.LtOrEq{"class_members.start_at": activeAt}).
			Where("(class_members.end_at IS NULL OR class_members.end_at > ?)", activeAt)
	}
	return s.selectMembers(ctx, query.OrderBy("class_members.user_id", "class_members.id"))
}

func (s SqlClassMemberStore) ListActiveByUser(
	ctx context.Context,
	userID string,
	at int64,
) ([]*model.ClassMember, error) {
	activeAt := model.TimeFromMillis(at)
	return s.selectMembers(ctx, s.query.Where(sq.Eq{
		"class_members.user_id": userID, "class_members.archived_at": nil,
	}).Where(sq.LtOrEq{"class_members.start_at": activeAt}).
		Where("(class_members.end_at IS NULL OR class_members.end_at > ?)", activeAt).
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
		"class_members.id": id, "class_members.archived_at": nil,
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
		"class_members.id": input.ID, "class_members.archived_at": nil,
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
		"class_members.id": id, "class_members.archived_at": nil,
	})); err != nil {
		return nil, translateError("class_member", id, err)
	}
	current := row.model()
	if current.Revision != expectedRevision {
		return nil, store.NewErrConflict("class_member", "class_member_changed", nil)
	}
	startMillis := model.MillisFromTime(current.StartsAt)
	endMillis := current.EndsAt.Millis()
	if endAt <= startMillis || (endMillis != 0 && endAt >= endMillis) {
		return nil, store.NewErrConflict("class_member", "class_member_end_time", nil)
	}
	at := model.TimeFromMillis(endAt)
	result, err := executor.Exec(ctx, `
		UPDATE class_members
		   SET updated_at = ?, end_at = ?, revision = revision + 1
		 WHERE id = ? AND archived_at IS NULL AND revision = ?`,
		at, at, id, expectedRevision,
	)
	if err != nil {
		return nil, fmt.Errorf("end class member: %w", err)
	}
	if err := requireAffected(result, "class_member", id); err != nil {
		return nil, err
	}
	current.UpdatedAt = at
	current.EndsAt = model.OptionalTimeFromMillis(endAt)
	current.Revision = expectedRevision + 1
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
		ID:               m.ID.String(),
		CreatedAt:        UTCTime(m.CreatedAt),
		UpdatedAt:        UTCTime(m.UpdatedAt),
		ArchivedAt:       NullTimeFromOptional(m.ArchivedAt),
		ClassID:          m.ClassID.String(),
		Revision:         m.Revision,
		AcademicPeriodID: m.AcademicPeriodID.String(),
		UserID:           m.UserID.String(),
		StartAt:          UTCTime(m.StartsAt),
		EndAt:            NullTimeFromOptional(m.EndsAt),
	}
}

func (r classMemberRow) model() *model.ClassMember {
	id, err := model.ParseClassMemberID(r.ID)
	if err != nil {
		id = model.ClassMemberID(r.ID)
	}
	classID, err := model.ParseClassID(r.ClassID)
	if err != nil {
		classID = model.ClassID(r.ClassID)
	}
	periodID, err := model.ParseAcademicPeriodID(r.AcademicPeriodID)
	if err != nil {
		periodID = model.AcademicPeriodID(r.AcademicPeriodID)
	}
	userID, err := model.ParseUserID(r.UserID)
	if err != nil {
		userID = model.UserID(r.UserID)
	}
	return &model.ClassMember{
		ID:               id,
		CreatedAt:        r.CreatedAt.UTC(),
		UpdatedAt:        r.UpdatedAt.UTC(),
		ArchivedAt:       OptionalTimeFromNullTime(r.ArchivedAt),
		ClassID:          classID,
		Revision:         r.Revision,
		AcademicPeriodID: periodID,
		UserID:           userID,
		StartsAt:         r.StartAt.UTC(),
		EndsAt:           OptionalTimeFromNullTime(r.EndAt),
	}
}

var _ store.ClassMemberStore = (*SqlClassMemberStore)(nil)
