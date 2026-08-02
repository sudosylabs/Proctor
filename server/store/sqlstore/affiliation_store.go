// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only
//
// Adapted from Mattermost server/channels/store/sqlstore/team_member_store.go
// for Proctor's non-exclusive, time-bounded institution affiliations.

package sqlstore

import (
	"context"
	"fmt"

	sq "github.com/Masterminds/squirrel"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

type SqlAffiliationStore struct {
	*SqlStore
	query sq.SelectBuilder
}

type affiliationRow struct {
	ID       string                `db:"id"`
	CreateAt int64                 `db:"create_at"`
	UpdateAt int64                 `db:"update_at"`
	DeleteAt int64                 `db:"delete_at"`
	Revision int64                 `db:"revision"`
	UserID   string                `db:"user_id"`
	Kind     model.AffiliationKind `db:"kind"`
	StartAt  int64                 `db:"start_at"`
	EndAt    int64                 `db:"end_at"`
}

func affiliationColumns() []string {
	return []string{
		"affiliations.id", "affiliations.create_at", "affiliations.update_at",
		"affiliations.delete_at", "affiliations.user_id", "affiliations.kind",
		"affiliations.revision",
		"affiliations.start_at", "affiliations.end_at",
	}
}

const affiliationLifecycleLock = "proctor:affiliation-lifecycle"

func (s SqlAffiliationStore) Create(ctx context.Context, input *store.AffiliationCreation) (*model.Affiliation, error) {
	if input == nil || input.Affiliation == nil || !model.IsValidId(input.AuditEventID) || input.AuditAt <= 0 {
		return nil, store.NewErrInvalidInput("affiliation", "creation", nil)
	}
	candidate := *input.Affiliation
	if appErr := candidate.IsValid(); appErr != nil {
		return nil, store.NewErrInvalidInput("affiliation", "value", nil).Wrap(appErr)
	}
	encoded, appErr := model.EncodeAuditData(candidate.Auditable())
	if appErr != nil {
		return nil, appErr
	}
	tx, err := s.GetMaster().Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin affiliation creation: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if err := lockAffiliationLifecycle(ctx, tx); err != nil {
		return nil, err
	}
	if err := lockAffiliationKind(ctx, tx, candidate.UserId, candidate.Kind); err != nil {
		return nil, err
	}
	if err := ensureAffiliationRangeAvailable(ctx, tx, &candidate); err != nil {
		return nil, err
	}
	row := newAffiliationRow(&candidate)
	if _, err := tx.NamedExec(ctx, `INSERT INTO affiliations (
		id, create_at, update_at, delete_at, revision, user_id, kind, start_at, end_at
	) VALUES (
		:id, :create_at, :update_at, :delete_at, :revision, :user_id, :kind, :start_at, :end_at
	)`, &row); err != nil {
		return nil, fmt.Errorf("create affiliation: %w", translateError("affiliation", candidate.Id, err))
	}
	if _, err := completeAuditEvent(ctx, tx, input.AuditEventID, model.AuditStatusSuccess, "", encoded, input.AuditAt); err != nil {
		return nil, fmt.Errorf("complete affiliation creation audit: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit affiliation creation: %w", err)
	}
	return &candidate, nil
}

func newSqlAffiliationStore(ss *SqlStore) store.AffiliationStore {
	s := &SqlAffiliationStore{SqlStore: ss}
	s.query = s.getQueryBuilder().Select(affiliationColumns()...).From("affiliations")
	return s
}

func (s SqlAffiliationStore) Save(
	ctx context.Context,
	affiliation *model.Affiliation,
) (*model.Affiliation, error) {
	if affiliation == nil || affiliation.Id != "" {
		return nil, store.NewErrInvalidInput("affiliation", "value", nil)
	}
	candidate := *affiliation
	candidate.PreSave()
	if appErr := candidate.IsValid(); appErr != nil {
		return nil, appErr
	}
	row := newAffiliationRow(&candidate)
	tx, err := s.GetMaster().Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin affiliation save: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if err := lockAffiliationLifecycle(ctx, tx); err != nil {
		return nil, err
	}
	if err := lockAffiliationKind(ctx, tx, candidate.UserId, candidate.Kind); err != nil {
		return nil, err
	}
	if err := ensureAffiliationRangeAvailable(ctx, tx, &candidate); err != nil {
		return nil, err
	}
	if _, err := tx.NamedExec(ctx, `
		INSERT INTO affiliations (
			id, create_at, update_at, delete_at, revision, user_id, kind, start_at, end_at
		) VALUES (
			:id, :create_at, :update_at, :delete_at, :revision, :user_id, :kind, :start_at, :end_at
		)`, &row); err != nil {
		return nil, fmt.Errorf(
			"save affiliation: %w",
			translateError("affiliation", candidate.Id, err),
		)
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit affiliation save: %w", err)
	}
	return &candidate, nil
}

func (s SqlAffiliationStore) Get(ctx context.Context, id string) (*model.Affiliation, error) {
	var row affiliationRow
	if err := s.GetMaster().GetBuilder(ctx, &row, s.query.Where(sq.Eq{
		"affiliations.id": id, "affiliations.delete_at": int64(0),
	})); err != nil {
		return nil, translateError("affiliation", id, err)
	}
	return row.model(), nil
}

func (s SqlAffiliationStore) ListByUser(
	ctx context.Context,
	userID string,
) ([]*model.Affiliation, error) {
	return s.selectAffiliations(ctx, s.query.Where(sq.Eq{
		"affiliations.user_id": userID, "affiliations.delete_at": int64(0),
	}).OrderBy("affiliations.start_at DESC", "affiliations.id"))
}

func (s SqlAffiliationStore) ListActiveByUser(
	ctx context.Context,
	userID string,
	at int64,
) ([]*model.Affiliation, error) {
	return s.selectAffiliations(ctx, s.query.Where(sq.Eq{
		"affiliations.user_id": userID, "affiliations.delete_at": int64(0),
	}).Where(sq.LtOrEq{"affiliations.start_at": at}).
		Where("(affiliations.end_at = 0 OR affiliations.end_at > ?)", at).
		OrderBy("affiliations.kind", "affiliations.id"))
}

func (s SqlAffiliationStore) End(
	ctx context.Context,
	id string,
	expectedRevision int64,
	endAt int64,
) (*model.Affiliation, error) {
	if !model.IsValidId(id) || expectedRevision <= 0 || endAt <= 0 {
		return nil, store.NewErrInvalidInput("affiliation", "end", nil)
	}
	tx, err := s.GetMaster().Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin affiliation end: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if err := lockAffiliationLifecycle(ctx, tx); err != nil {
		return nil, err
	}
	ended, err := s.endAffiliation(ctx, tx, id, expectedRevision, endAt)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit affiliation end: %w", err)
	}
	return ended, nil
}

func (s SqlAffiliationStore) EndWithAudit(ctx context.Context, input *store.AffiliationEnd) (*model.Affiliation, error) {
	if input == nil || !model.IsValidId(input.ID) || input.ExpectedRevision <= 0 || input.EndAt <= 0 || !model.IsValidId(input.AuditEventID) || input.AuditAt <= 0 {
		return nil, store.NewErrInvalidInput("affiliation", "end", nil)
	}
	tx, err := s.GetMaster().Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin affiliation end: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if err := lockAffiliationLifecycle(ctx, tx); err != nil {
		return nil, err
	}
	current, err := s.endAffiliation(ctx, tx, input.ID, input.ExpectedRevision, input.EndAt)
	if err != nil {
		return nil, err
	}
	encoded, appErr := model.EncodeAuditData(current.Auditable())
	if appErr != nil {
		return nil, appErr
	}
	if _, err := completeAuditEvent(ctx, tx, input.AuditEventID, model.AuditStatusSuccess, "", encoded, input.AuditAt); err != nil {
		return nil, fmt.Errorf("complete affiliation end audit: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit affiliation end: %w", err)
	}
	return current, nil
}

func (s SqlAffiliationStore) endAffiliation(ctx context.Context, tx sqlxExecutor, id string, expectedRevision, endAt int64) (*model.Affiliation, error) {
	var row affiliationRow
	if err := tx.GetBuilder(ctx, &row, s.query.Where(sq.Eq{"affiliations.id": id, "affiliations.delete_at": int64(0)})); err != nil {
		return nil, translateError("affiliation", id, err)
	}
	current := row.model()
	if current.Revision != expectedRevision {
		return nil, store.NewErrConflict("affiliation", "affiliation_changed", nil)
	}
	if endAt <= current.StartAt || (current.EndAt != 0 && endAt >= current.EndAt) {
		return nil, store.NewErrConflict("affiliation", "affiliation_end_time", nil)
	}
	if current.Kind == model.AffiliationStudent {
		var activeEnrollment bool
		if err := tx.Get(ctx, &activeEnrollment, `SELECT EXISTS (SELECT 1 FROM class_members WHERE user_id = ? AND delete_at = 0 AND end_at = 0)`, current.UserId); err != nil {
			return nil, fmt.Errorf("check affiliation enrollment dependencies: %w", err)
		}
		if activeEnrollment {
			return nil, store.NewErrConflict("affiliation", "affiliation_student_has_active_enrollment", nil)
		}
	}
	result, err := tx.Exec(ctx, `UPDATE affiliations SET update_at = ?, end_at = ?, revision = revision + 1 WHERE id = ? AND delete_at = 0 AND revision = ?`, endAt, endAt, id, expectedRevision)
	if err != nil {
		return nil, fmt.Errorf("end affiliation: %w", err)
	}
	if err := requireAffected(result, "affiliation", id); err != nil {
		return nil, err
	}
	current.UpdateAt, current.EndAt, current.Revision = endAt, endAt, expectedRevision+1
	return current, nil
}

func lockAffiliationLifecycle(ctx context.Context, executor sqlxExecutor) error {
	if _, err := executor.Exec(ctx, "SELECT pg_advisory_xact_lock(hashtext(?))", affiliationLifecycleLock); err != nil {
		return fmt.Errorf("lock affiliation lifecycle: %w", err)
	}
	return nil
}

func lockAffiliationKind(ctx context.Context, executor sqlxExecutor, userID string, kind model.AffiliationKind) error {
	if _, err := executor.Exec(ctx, "SELECT pg_advisory_xact_lock(hashtext(?))", "proctor:affiliation:"+userID+":"+string(kind)); err != nil {
		return fmt.Errorf("lock affiliation: %w", err)
	}
	return nil
}

func ensureAffiliationRangeAvailable(ctx context.Context, executor sqlxExecutor, candidate *model.Affiliation) error {
	var overlaps bool
	if err := executor.Get(ctx, &overlaps, `SELECT EXISTS (
		SELECT 1 FROM affiliations WHERE user_id = ? AND kind = ? AND delete_at = 0
		 AND (end_at = 0 OR end_at > ?) AND (? = 0 OR start_at < ?)
	)`, candidate.UserId, candidate.Kind, candidate.StartAt, candidate.EndAt, candidate.EndAt); err != nil {
		return fmt.Errorf("check affiliation overlap: %w", err)
	}
	if overlaps {
		return store.NewErrConflict("affiliation", "affiliations_effective_range_overlap", nil)
	}
	return nil
}

func (s SqlAffiliationStore) selectAffiliations(
	ctx context.Context,
	query sq.SelectBuilder,
) ([]*model.Affiliation, error) {
	rows := []affiliationRow{}
	if err := s.GetMaster().SelectBuilder(ctx, &rows, query); err != nil {
		return nil, fmt.Errorf("list affiliations: %w", err)
	}
	result := make([]*model.Affiliation, 0, len(rows))
	for _, row := range rows {
		result = append(result, row.model())
	}
	return result, nil
}

func newAffiliationRow(a *model.Affiliation) affiliationRow {
	return affiliationRow{
		ID: a.Id, CreateAt: a.CreateAt, UpdateAt: a.UpdateAt,
		DeleteAt: a.DeleteAt, UserID: a.UserId, Kind: a.Kind,
		Revision: a.Revision,
		StartAt:  a.StartAt, EndAt: a.EndAt,
	}
}

func (r affiliationRow) model() *model.Affiliation {
	return &model.Affiliation{
		Id: r.ID, CreateAt: r.CreateAt, UpdateAt: r.UpdateAt,
		DeleteAt: r.DeleteAt, UserId: r.UserID, Kind: r.Kind,
		Revision: r.Revision,
		StartAt:  r.StartAt, EndAt: r.EndAt,
	}
}

var _ store.AffiliationStore = (*SqlAffiliationStore)(nil)
