// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: Apache-2.0
//
// Adapted from Mattermost server/channels/store/sqlstore/team_store.go. Proctor
// retains the per-model SQL store, reusable select builder, named writes,
// model lifecycle, and store-error boundary while implementing institution-wide
// academic periods.

package sqlstore

import (
	"context"
	"fmt"

	sq "github.com/Masterminds/squirrel"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

type SqlAcademicPeriodStore struct {
	*SqlStore
	academicPeriodsQuery sq.SelectBuilder
}

type academicPeriodRow struct {
	ID            string `db:"id"`
	CreateAt      int64  `db:"create_at"`
	UpdateAt      int64  `db:"update_at"`
	DeleteAt      int64  `db:"delete_at"`
	InstitutionID string `db:"institution_id"`
	Name          string `db:"name"`
	DisplayName   string `db:"display_name"`
	Description   string `db:"description"`
	StartAt       int64  `db:"start_at"`
	EndAt         int64  `db:"end_at"`
}

func academicPeriodSliceColumns() []string {
	return []string{
		"academic_periods.id",
		"academic_periods.create_at",
		"academic_periods.update_at",
		"academic_periods.delete_at",
		"academic_periods.institution_id",
		"academic_periods.name",
		"academic_periods.display_name",
		"academic_periods.description",
		"academic_periods.start_at",
		"academic_periods.end_at",
	}
}

func newSqlAcademicPeriodStore(sqlStore *SqlStore) store.AcademicPeriodStore {
	s := &SqlAcademicPeriodStore{SqlStore: sqlStore}
	s.academicPeriodsQuery = s.getQueryBuilder().
		Select(academicPeriodSliceColumns()...).
		From("academic_periods")
	return s
}

func (s SqlAcademicPeriodStore) Save(
	ctx context.Context,
	period *model.AcademicPeriod,
) (*model.AcademicPeriod, error) {
	if period == nil {
		return nil, store.NewErrInvalidInput("academic_period", "value", nil)
	}
	if period.Id != "" {
		return nil, store.NewErrInvalidInput("academic_period", "id", period.Id)
	}

	candidate := *period
	candidate.PreSave()
	if appErr := candidate.IsValid(); appErr != nil {
		return nil, appErr
	}

	row := newAcademicPeriodRow(&candidate)
	if _, err := s.GetMaster().NamedExec(ctx, `
		INSERT INTO academic_periods (
			id, create_at, update_at, delete_at, institution_id,
			name, display_name, description, start_at, end_at
		) VALUES (
			:id, :create_at, :update_at, :delete_at, :institution_id,
			:name, :display_name, :description, :start_at, :end_at
		)`, &row); err != nil {
		return nil, fmt.Errorf(
			"save academic period: %w",
			translateError("academic_period", candidate.Id, err),
		)
	}
	return &candidate, nil
}

func (s SqlAcademicPeriodStore) Get(ctx context.Context, id string) (*model.AcademicPeriod, error) {
	var row academicPeriodRow
	query := s.academicPeriodsQuery.Where(sq.Eq{
		"academic_periods.id":        id,
		"academic_periods.delete_at": int64(0),
	})
	if err := s.GetMaster().GetBuilder(ctx, &row, query); err != nil {
		return nil, translateError("academic_period", id, err)
	}
	return row.model(), nil
}

func (s SqlAcademicPeriodStore) GetByName(
	ctx context.Context,
	institutionID string,
	name string,
) (*model.AcademicPeriod, error) {
	var row academicPeriodRow
	query := s.academicPeriodsQuery.Where(sq.Eq{
		"academic_periods.institution_id": institutionID,
		"academic_periods.name":           name,
		"academic_periods.delete_at":      int64(0),
	})
	if err := s.GetMaster().GetBuilder(ctx, &row, query); err != nil {
		return nil, translateError("academic_period", institutionID+"/"+name, err)
	}
	return row.model(), nil
}

func (s SqlAcademicPeriodStore) ListByInstitution(
	ctx context.Context,
	institutionID string,
) ([]*model.AcademicPeriod, error) {
	query := s.academicPeriodsQuery.
		Where(sq.Eq{
			"academic_periods.institution_id": institutionID,
			"academic_periods.delete_at":      int64(0),
		}).
		OrderBy("academic_periods.start_at", "academic_periods.name", "academic_periods.id")

	rows := []academicPeriodRow{}
	if err := s.GetMaster().SelectBuilder(ctx, &rows, query); err != nil {
		return nil, fmt.Errorf("list academic periods by institution: %w", err)
	}
	periods := make([]*model.AcademicPeriod, 0, len(rows))
	for _, row := range rows {
		periods = append(periods, row.model())
	}
	return periods, nil
}

func (s SqlAcademicPeriodStore) Update(
	ctx context.Context,
	period *model.AcademicPeriod,
) (*model.AcademicPeriod, error) {
	if period == nil {
		return nil, store.NewErrInvalidInput("academic_period", "value", nil)
	}

	candidate := *period
	candidate.PreUpdate()
	if appErr := candidate.IsValid(); appErr != nil {
		return nil, appErr
	}

	row := newAcademicPeriodRow(&candidate)
	result, err := s.GetMaster().NamedExec(ctx, `
		UPDATE academic_periods
		   SET update_at = :update_at,
		       institution_id = :institution_id,
		       name = :name,
		       display_name = :display_name,
		       description = :description,
		       start_at = :start_at,
		       end_at = :end_at
		 WHERE id = :id AND delete_at = 0`, &row)
	if err != nil {
		return nil, fmt.Errorf(
			"update academic period: %w",
			translateError("academic_period", candidate.Id, err),
		)
	}
	if err := requireAffected(result, "academic_period", candidate.Id); err != nil {
		return nil, err
	}
	return &candidate, nil
}

func newAcademicPeriodRow(period *model.AcademicPeriod) academicPeriodRow {
	return academicPeriodRow{
		ID:            period.Id,
		CreateAt:      period.CreateAt,
		UpdateAt:      period.UpdateAt,
		DeleteAt:      period.DeleteAt,
		InstitutionID: period.InstitutionId,
		Name:          period.Name,
		DisplayName:   period.DisplayName,
		Description:   period.Description,
		StartAt:       period.StartAt,
		EndAt:         period.EndAt,
	}
}

func (row academicPeriodRow) model() *model.AcademicPeriod {
	return &model.AcademicPeriod{
		Id:            row.ID,
		CreateAt:      row.CreateAt,
		UpdateAt:      row.UpdateAt,
		DeleteAt:      row.DeleteAt,
		InstitutionId: row.InstitutionID,
		Name:          row.Name,
		DisplayName:   row.DisplayName,
		Description:   row.Description,
		StartAt:       row.StartAt,
		EndAt:         row.EndAt,
	}
}

var _ store.AcademicPeriodStore = (*SqlAcademicPeriodStore)(nil)
