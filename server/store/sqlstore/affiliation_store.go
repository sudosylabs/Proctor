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
	UserID   string                `db:"user_id"`
	Kind     model.AffiliationKind `db:"kind"`
	StartAt  int64                 `db:"start_at"`
	EndAt    int64                 `db:"end_at"`
}

func affiliationColumns() []string {
	return []string{
		"affiliations.id", "affiliations.create_at", "affiliations.update_at",
		"affiliations.delete_at", "affiliations.user_id", "affiliations.kind",
		"affiliations.start_at", "affiliations.end_at",
	}
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
	if _, err := tx.Exec(
		ctx,
		"SELECT pg_advisory_xact_lock(hashtext(?))",
		"proctor:affiliation:"+candidate.UserId+":"+string(candidate.Kind),
	); err != nil {
		return nil, fmt.Errorf("lock affiliation: %w", err)
	}
	var overlaps bool
	if err := tx.Get(ctx, &overlaps, `
		SELECT EXISTS (
			SELECT 1
			  FROM affiliations
			 WHERE user_id = ? AND kind = ? AND delete_at = 0
			   AND (end_at = 0 OR end_at > ?)
			   AND (? = 0 OR start_at < ?)
		)`,
		candidate.UserId,
		candidate.Kind,
		candidate.StartAt,
		candidate.EndAt,
		candidate.EndAt,
	); err != nil {
		return nil, fmt.Errorf("check affiliation overlap: %w", err)
	}
	if overlaps {
		return nil, store.NewErrConflict(
			"affiliation", "affiliations_effective_range_overlap", nil,
		)
	}
	if _, err := tx.NamedExec(ctx, `
		INSERT INTO affiliations (
			id, create_at, update_at, delete_at, user_id, kind, start_at, end_at
		) VALUES (
			:id, :create_at, :update_at, :delete_at, :user_id, :kind, :start_at, :end_at
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
	endAt int64,
) (*model.Affiliation, error) {
	if endAt <= 0 {
		return nil, store.NewErrInvalidInput("affiliation", "end_at", endAt)
	}
	result, err := s.GetMaster().Exec(ctx, `
		UPDATE affiliations
		   SET update_at = ?, end_at = ?
		 WHERE id = ? AND delete_at = 0 AND start_at < ?
		   AND (end_at = 0 OR end_at > ?)`,
		endAt, endAt, id, endAt, endAt,
	)
	if err != nil {
		return nil, fmt.Errorf(
			"end affiliation: %w",
			translateError("affiliation", id, err),
		)
	}
	if err := requireAffected(result, "affiliation", id); err != nil {
		return nil, err
	}
	return s.Get(ctx, id)
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
		StartAt: a.StartAt, EndAt: a.EndAt,
	}
}

func (r affiliationRow) model() *model.Affiliation {
	return &model.Affiliation{
		Id: r.ID, CreateAt: r.CreateAt, UpdateAt: r.UpdateAt,
		DeleteAt: r.DeleteAt, UserId: r.UserID, Kind: r.Kind,
		StartAt: r.StartAt, EndAt: r.EndAt,
	}
}

var _ store.AffiliationStore = (*SqlAffiliationStore)(nil)
