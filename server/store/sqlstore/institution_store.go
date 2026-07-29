// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: Apache-2.0
//
// Adapted from Mattermost server/channels/store/sqlstore/team_store.go. Proctor
// retains the per-model Sql<Model>Store, embedded root store, reusable select
// builder, named writes, model lifecycle, and store-error boundary while
// implementing Proctor's singleton-institution semantics.

package sqlstore

import (
	"context"
	"database/sql"
	"fmt"

	sq "github.com/Masterminds/squirrel"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

type SqlInstitutionStore struct {
	*SqlStore
	institutionsQuery sq.SelectBuilder
}

type institutionRow struct {
	ID          string `db:"id"`
	CreateAt    int64  `db:"create_at"`
	UpdateAt    int64  `db:"update_at"`
	DeleteAt    int64  `db:"delete_at"`
	Name        string `db:"name"`
	DisplayName string `db:"display_name"`
	Description string `db:"description"`
}

func institutionSliceColumns() []string {
	return []string{
		"institutions.id",
		"institutions.create_at",
		"institutions.update_at",
		"institutions.delete_at",
		"institutions.name",
		"institutions.display_name",
		"institutions.description",
	}
}

func newSqlInstitutionStore(sqlStore *SqlStore) store.InstitutionStore {
	s := &SqlInstitutionStore{SqlStore: sqlStore}
	s.institutionsQuery = s.getQueryBuilder().
		Select(institutionSliceColumns()...).
		From("institutions")
	return s
}

func (s SqlInstitutionStore) Save(ctx context.Context, institution *model.Institution) (*model.Institution, error) {
	if institution == nil {
		return nil, store.NewErrInvalidInput("institution", "value", nil)
	}
	if institution.Id != "" {
		return nil, store.NewErrInvalidInput("institution", "id", institution.Id)
	}

	candidate := *institution
	candidate.PreSave()
	if appErr := candidate.IsValid(); appErr != nil {
		return nil, appErr
	}

	row := newInstitutionRow(&candidate)
	if _, err := s.GetMaster().NamedExec(ctx, `
		INSERT INTO institutions (
			id, create_at, update_at, delete_at, name, display_name, description
		) VALUES (
			:id, :create_at, :update_at, :delete_at, :name, :display_name, :description
		)`, &row); err != nil {
		return nil, fmt.Errorf("save institution: %w", translateError("institution", candidate.Id, err))
	}
	return &candidate, nil
}

func (s SqlInstitutionStore) Get(ctx context.Context, id string) (*model.Institution, error) {
	var row institutionRow
	query := s.institutionsQuery.Where(sq.Eq{
		"institutions.id":        id,
		"institutions.delete_at": int64(0),
	})
	if err := s.GetMaster().GetBuilder(ctx, &row, query); err != nil {
		return nil, translateError("institution", id, err)
	}
	return row.model(), nil
}

func (s SqlInstitutionStore) GetSingleton(ctx context.Context) (*model.Institution, error) {
	var row institutionRow
	query := s.institutionsQuery.Where(sq.Eq{
		"institutions.singleton": true,
		"institutions.delete_at": int64(0),
	})
	if err := s.GetMaster().GetBuilder(ctx, &row, query); err != nil {
		return nil, translateError("institution", "singleton", err)
	}
	return row.model(), nil
}

func (s SqlInstitutionStore) Update(ctx context.Context, institution *model.Institution) (*model.Institution, error) {
	if institution == nil {
		return nil, store.NewErrInvalidInput("institution", "value", nil)
	}
	candidate := *institution
	candidate.PreUpdate()
	if appErr := candidate.IsValid(); appErr != nil {
		return nil, appErr
	}

	row := newInstitutionRow(&candidate)
	result, err := s.GetMaster().NamedExec(ctx, `
		UPDATE institutions
		   SET update_at = :update_at,
		       name = :name,
		       display_name = :display_name,
		       description = :description
		 WHERE id = :id AND delete_at = 0`, &row)
	if err != nil {
		return nil, fmt.Errorf("update institution: %w", translateError("institution", candidate.Id, err))
	}
	if err := requireAffected(result, "institution", candidate.Id); err != nil {
		return nil, err
	}
	return &candidate, nil
}

func (s SqlInstitutionStore) Delete(ctx context.Context, id string, deleteAt int64) error {
	if deleteAt <= 0 {
		return store.NewErrInvalidInput("institution", "delete_at", deleteAt)
	}
	result, err := s.GetMaster().Exec(
		ctx,
		"UPDATE institutions SET update_at = ?, delete_at = ? WHERE id = ? AND delete_at = 0",
		deleteAt,
		deleteAt,
		id,
	)
	if err != nil {
		return fmt.Errorf("delete institution: %w", translateError("institution", id, err))
	}
	return requireAffected(result, "institution", id)
}

func newInstitutionRow(institution *model.Institution) institutionRow {
	return institutionRow{
		ID:          institution.Id,
		CreateAt:    institution.CreateAt,
		UpdateAt:    institution.UpdateAt,
		DeleteAt:    institution.DeleteAt,
		Name:        institution.Name,
		DisplayName: institution.DisplayName,
		Description: institution.Description,
	}
}

func (row institutionRow) model() *model.Institution {
	return &model.Institution{
		Id:          row.ID,
		CreateAt:    row.CreateAt,
		UpdateAt:    row.UpdateAt,
		DeleteAt:    row.DeleteAt,
		Name:        row.Name,
		DisplayName: row.DisplayName,
		Description: row.Description,
	}
}

func requireAffected(result sql.Result, resource, id string) error {
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read %s affected rows: %w", resource, err)
	}
	if affected == 0 {
		return store.NewErrNotFound(resource, id).Wrap(sql.ErrNoRows)
	}
	return nil
}

var _ store.InstitutionStore = (*SqlInstitutionStore)(nil)
