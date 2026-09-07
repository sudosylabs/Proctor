// ---------------------------------------------------------------------------------------------
// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// Modifications Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------
//
// Adapted from Mattermost server/channels/store/sqlstore/team_store.go. Proctor
// retains the per-model SQL store, reusable select builder, named writes,
// model lifecycle, and store-error boundary while implementing curriculum
// levels scoped to programmes.

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

type SQLProgrammeLevelStore struct {
	*SQLStore
	programmeLevelsQuery sq.SelectBuilder
}

const programmeLevelLifecycleLock = "proctor:programme-level-lifecycle"

type programmeLevelRow struct {
	ID          string       `db:"id"`
	CreatedAt   time.Time    `db:"created_at"`
	UpdatedAt   time.Time    `db:"updated_at"`
	ArchivedAt  sql.NullTime `db:"archived_at"`
	Revision    int64        `db:"revision"`
	ProgrammeID string       `db:"programme_id"`
	Name        string       `db:"name"`
	DisplayName string       `db:"display_name"`
	Description string       `db:"description"`
}

func programmeLevelSliceColumns() []string {
	return []string{
		"programme_levels.id",
		"programme_levels.created_at",
		"programme_levels.updated_at",
		"programme_levels.archived_at",
		"programme_levels.revision",
		"programme_levels.programme_id",
		"programme_levels.name",
		"programme_levels.display_name",
		"programme_levels.description",
	}
}

func newSQLProgrammeLevelStore(sqlStore *SQLStore) store.ProgrammeLevelStore {
	s := &SQLProgrammeLevelStore{SQLStore: sqlStore}
	s.programmeLevelsQuery = s.getQueryBuilder().
		Select(programmeLevelSliceColumns()...).
		From("programme_levels")
	return s
}

func (s SQLProgrammeLevelStore) Create(ctx context.Context, input *store.ProgrammeLevelCreation) (*model.ProgrammeLevel, error) {
	if input == nil || input.Level == nil || !model.IsValidId(input.AuditEventID) || input.AuditAt <= 0 {
		return nil, store.NewErrInvalidInput("programme_level", "creation", nil)
	}
	if !input.Level.ID.IsValid() {
		return nil, store.NewErrInvalidInput("programme_level", "id", input.Level.ID.String())
	}
	candidate := *input.Level
	if err := candidate.Validate(); err != nil {
		return nil, store.NewErrInvalidInput("programme_level", "value", nil).Wrap(err)
	}
	return s.createProgrammeLevel(ctx, &candidate, &academicMutationAudit{eventID: input.AuditEventID, at: input.AuditAt})
}

func (s SQLProgrammeLevelStore) createProgrammeLevel(ctx context.Context, candidate *model.ProgrammeLevel, audit *academicMutationAudit) (*model.ProgrammeLevel, error) {
	return runSQLTransaction(ctx, s.GetMaster().Begin, "programme level creation", func(ctx context.Context, tx *sqlxTxWrapper) (*model.ProgrammeLevel, error) {
		if err := lockProgrammeLifecycle(ctx, tx); err != nil {
			return nil, err
		}
		if err := validateActiveProgramme(ctx, tx, candidate.ProgrammeID.String()); err != nil {
			return nil, err
		}
		row := newProgrammeLevelRow(candidate)
		if _, err := tx.NamedExec(ctx, `
			INSERT INTO programme_levels (
				id, created_at, updated_at, archived_at, revision, programme_id,
				name, display_name, description
			) VALUES (
				:id, :created_at, :updated_at, :archived_at, :revision, :programme_id,
				:name, :display_name, :description
			)`, &row); err != nil {
			return nil, fmt.Errorf("create programme level: %w", translateError("programme_level", candidate.ID.String(), err))
		}
		if err := audit.complete(ctx, tx, candidate.Auditable()); err != nil {
			return nil, err
		}
		return candidate, nil
	})
}

func (s SQLProgrammeLevelStore) Save(
	ctx context.Context,
	level *model.ProgrammeLevel,
) (*model.ProgrammeLevel, error) {
	if level == nil {
		return nil, store.NewErrInvalidInput("programme_level", "value", nil)
	}
	if !level.ID.IsZero() {
		return nil, store.NewErrInvalidInput("programme_level", "id", level.ID.String())
	}

	id, err := model.ParseProgrammeLevelID(model.NewId())
	if err != nil {
		return nil, err
	}
	candidate := *level
	candidate.PrepareCreate(id, model.NowUTC())
	if err := candidate.Validate(); err != nil {
		return nil, store.NewErrInvalidInput("programme_level", "value", nil).Wrap(err)
	}

	return s.createProgrammeLevel(ctx, &candidate, nil)
}

func (s SQLProgrammeLevelStore) Get(ctx context.Context, id string) (*model.ProgrammeLevel, error) {
	var row programmeLevelRow
	query := s.programmeLevelsQuery.Where(sq.Eq{
		"programme_levels.id":          id,
		"programme_levels.archived_at": nil,
	})
	if err := s.GetMaster().GetBuilder(ctx, &row, query); err != nil {
		return nil, translateError("programme_level", id, err)
	}
	return row.model()
}

func (s SQLProgrammeLevelStore) GetByName(
	ctx context.Context,
	programmeID string,
	name string,
) (*model.ProgrammeLevel, error) {
	var row programmeLevelRow
	query := s.programmeLevelsQuery.Where(sq.Eq{
		"programme_levels.programme_id": programmeID,
		"programme_levels.name":         name,
		"programme_levels.archived_at":  nil,
	})
	if err := s.GetMaster().GetBuilder(ctx, &row, query); err != nil {
		return nil, translateError("programme_level", programmeID+"/"+name, err)
	}
	return row.model()
}

func (s SQLProgrammeLevelStore) ListByProgramme(
	ctx context.Context,
	programmeID string,
) ([]*model.ProgrammeLevel, error) {
	query := s.programmeLevelsQuery.
		Where(sq.Eq{
			"programme_levels.programme_id": programmeID,
			"programme_levels.archived_at":  nil,
		}).
		OrderBy("programme_levels.name", "programme_levels.id")

	rows := []programmeLevelRow{}
	if err := s.GetMaster().SelectBuilder(ctx, &rows, query); err != nil {
		return nil, fmt.Errorf("list programme levels by programme: %w", err)
	}
	levels := make([]*model.ProgrammeLevel, 0, len(rows))
	for _, row := range rows {
		level, err := row.model()
		if err != nil {
			return nil, err
		}
		levels = append(levels, level)
	}
	return levels, nil
}

func (s SQLProgrammeLevelStore) SearchByProgramme(
	ctx context.Context,
	programmeID string,
	term string,
	limit int,
) ([]*model.ProgrammeLevel, error) {
	if limit < 1 || limit > 200 {
		return nil, store.NewErrInvalidInput("programme_level", "limit", limit)
	}
	query := s.programmeLevelsQuery.Where(sq.Eq{
		"programme_levels.programme_id": programmeID,
		"programme_levels.archived_at":  nil,
	}).Where("(programme_levels.name ILIKE ? ESCAPE '!' OR programme_levels.display_name ILIKE ? ESCAPE '!')",
		directorySearchPattern(term), directorySearchPattern(term)).
		OrderBy("programme_levels.name", "programme_levels.id").Limit(uint64(limit))
	rows := []programmeLevelRow{}
	if err := s.GetMaster().SelectBuilder(ctx, &rows, query); err != nil {
		return nil, fmt.Errorf("search programme levels: %w", err)
	}
	result := make([]*model.ProgrammeLevel, 0, len(rows))
	for _, row := range rows {
		level, err := row.model()
		if err != nil {
			return nil, err
		}
		result = append(result, level)
	}
	return result, nil
}

func (s SQLProgrammeLevelStore) Update(
	ctx context.Context,
	level *model.ProgrammeLevel,
) (*model.ProgrammeLevel, error) {
	if level == nil {
		return nil, store.NewErrInvalidInput("programme_level", "value", nil)
	}

	candidate := *level
	candidate.PrepareUpdate(model.NowUTC())
	if err := candidate.Validate(); err != nil {
		return nil, store.NewErrInvalidInput("programme_level", "value", nil).Wrap(err)
	}

	return s.updateProgrammeLevel(ctx, &candidate, "", nil)
}

func (s SQLProgrammeLevelStore) updateProgrammeLevel(ctx context.Context, candidate *model.ProgrammeLevel, expectedOwner string, audit *academicMutationAudit) (*model.ProgrammeLevel, error) {
	return runSQLTransaction(ctx, s.GetMaster().Begin, "programme level update", func(ctx context.Context, tx *sqlxTxWrapper) (*model.ProgrammeLevel, error) {
		if err := lockProgrammeLifecycle(ctx, tx); err != nil {
			return nil, err
		}
		if err := validateActiveProgramme(ctx, tx, candidate.ProgrammeID.String()); err != nil {
			return nil, err
		}
		row := newProgrammeLevelRow(candidate)
		result, err := tx.NamedExec(ctx, `
			UPDATE programme_levels
			   SET updated_at = :updated_at,
			       revision = :revision,
			       programme_id = :programme_id,
			       name = :name,
			       display_name = :display_name,
			       description = :description
			 WHERE id = :id AND archived_at IS NULL
			   AND (:expected_owner = '' OR programme_id = :expected_owner)
			   AND revision = :expected_revision`, map[string]any{
			"expected_owner": expectedOwner, "id": candidate.ID.String(), "updated_at": row.UpdatedAt,
			"revision": candidate.Revision, "programme_id": row.ProgrammeID,
			"name": row.Name, "display_name": row.DisplayName,
			"description": row.Description, "expected_revision": candidate.Revision - 1,
		})
		if err != nil {
			return nil, fmt.Errorf(
				"update programme level: %w",
				translateError("programme_level", candidate.ID.String(), err),
			)
		}
		if expectedOwner != "" {
			if err := requireOwnedRevisionAffected(ctx, tx, result, "programme_level", "programme_levels", "programme_id", candidate.ID.String(), expectedOwner); err != nil {
				return nil, err
			}
		} else if err := requireRevisionAffected(ctx, tx, result, "programme_level", "programme_levels", candidate.ID.String()); err != nil {
			return nil, err
		}
		if err := audit.complete(ctx, tx, candidate.Auditable()); err != nil {
			return nil, err
		}
		return candidate, nil
	})
}

func (s SQLProgrammeLevelStore) UpdateWithAudit(ctx context.Context, input *store.ProgrammeLevelUpdate) (*model.ProgrammeLevel, error) {
	if input == nil || input.Level == nil || !model.IsValidId(input.AuditEventID) || input.AuditAt <= 0 {
		return nil, store.NewErrInvalidInput("programme_level", "update", nil)
	}
	candidate := *input.Level
	if err := candidate.Validate(); err != nil {
		return nil, store.NewErrInvalidInput("programme_level", "value", nil).Wrap(err)
	}
	return s.updateProgrammeLevel(ctx, &candidate, candidate.ProgrammeID.String(), &academicMutationAudit{eventID: input.AuditEventID, at: input.AuditAt})
}

func (s SQLProgrammeLevelStore) Archive(
	ctx context.Context,
	id string,
	archiveAt int64,
) (*model.ProgrammeLevel, error) {
	if archiveAt <= 0 {
		return nil, store.NewErrInvalidInput("programme_level", "archived_at", archiveAt)
	}
	return s.archiveProgrammeLevel(ctx, id, archiveAt, 0, nil)
}

func (s SQLProgrammeLevelStore) ArchiveWithAudit(ctx context.Context, input *store.ProgrammeLevelArchive) (*model.ProgrammeLevel, error) {
	if input == nil || !model.IsValidId(input.ID) || input.ArchiveAt <= 0 || input.ExpectedRevision < 0 || !model.IsValidId(input.AuditEventID) || input.AuditAt <= 0 {
		return nil, store.NewErrInvalidInput("programme_level", "archive", nil)
	}
	return s.archiveProgrammeLevel(ctx, input.ID, input.ArchiveAt, input.ExpectedRevision, &academicMutationAudit{eventID: input.AuditEventID, at: input.AuditAt})
}

func (s SQLProgrammeLevelStore) archiveProgrammeLevel(ctx context.Context, id string, archiveAt, expectedRevision int64, audit *academicMutationAudit) (*model.ProgrammeLevel, error) {
	return runSQLTransaction(ctx, s.GetMaster().Begin, "programme level archive", func(ctx context.Context, tx *sqlxTxWrapper) (*model.ProgrammeLevel, error) {
		if err := lockProgrammeLevelLifecycle(ctx, tx); err != nil {
			return nil, err
		}
		var row programmeLevelRow
		query := s.programmeLevelsQuery.Where(sq.Eq{"programme_levels.id": id, "programme_levels.archived_at": nil})
		if err := tx.GetBuilder(ctx, &row, query); err != nil {
			return nil, translateError("programme_level", id, err)
		}
		if err := requireAcademicRevision("programme_level", expectedRevision, row.Revision); err != nil {
			return nil, err
		}
		var dependent bool
		if err := tx.Get(ctx, &dependent, `SELECT EXISTS (SELECT 1 FROM classes WHERE programme_level_id = ? AND archived_at IS NULL)`, id); err != nil {
			return nil, fmt.Errorf("check programme level archive dependencies: %w", err)
		}
		if dependent {
			return nil, store.NewErrConflict("programme_level", "programme_level_has_active_classes", nil)
		}
		result, err := tx.Exec(ctx, `UPDATE programme_levels SET updated_at = GREATEST(created_at, ?), archived_at = GREATEST(created_at, ?), revision = revision + 1 WHERE id = ? AND archived_at IS NULL AND revision = ?`, model.TimeFromMillis(archiveAt), model.TimeFromMillis(archiveAt), id, row.Revision)
		if err != nil {
			return nil, fmt.Errorf("archive programme level: %w", err)
		}
		if err := requireRevisionAffected(ctx, tx, result, "programme_level", "programme_levels", id); err != nil {
			return nil, err
		}
		if err := tx.GetBuilder(ctx, &row, s.programmeLevelsQuery.Where(sq.Eq{"programme_levels.id": id})); err != nil {
			return nil, fmt.Errorf("read archived programme level: %w", err)
		}
		level, err := row.model()
		if err != nil {
			return nil, err
		}
		if err := audit.complete(ctx, tx, level.Auditable()); err != nil {
			return nil, err
		}
		return level, nil
	})
}

func lockProgrammeLevelLifecycle(ctx context.Context, tx sqlxExecutor) error {
	if _, err := tx.Exec(ctx, "SELECT pg_advisory_xact_lock(hashtext(?))", programmeLevelLifecycleLock); err != nil {
		return fmt.Errorf("lock programme level lifecycle: %w", err)
	}
	return nil
}

func validateActiveProgrammeLevel(ctx context.Context, executor sqlxExecutor, id string) error {
	var exists bool
	if err := executor.Get(ctx, &exists, `SELECT EXISTS (SELECT 1 FROM programme_levels WHERE id = ? AND archived_at IS NULL)`, id); err != nil {
		return fmt.Errorf("validate class programme level: %w", err)
	}
	if !exists {
		return store.NewErrReference("class", "classes_programme_level_id_fkey", nil)
	}
	return nil
}

func newProgrammeLevelRow(level *model.ProgrammeLevel) programmeLevelRow {
	return programmeLevelRow{
		ID:          level.ID.String(),
		CreatedAt:   UTCTime(level.CreatedAt),
		UpdatedAt:   UTCTime(level.UpdatedAt),
		ArchivedAt:  NullTimeFromOptional(level.ArchivedAt),
		Revision:    level.Revision,
		ProgrammeID: level.ProgrammeID.String(),
		Name:        level.Name,
		DisplayName: level.DisplayName,
		Description: level.Description,
	}
}

func (row programmeLevelRow) model() (*model.ProgrammeLevel, error) {
	id, err := parsePersistedID(
		"programme_level", "id", row.ID, model.ParseProgrammeLevelID,
	)
	if err != nil {
		return nil, err
	}
	programmeID, err := parsePersistedID(
		"programme_level", "programme_id", row.ProgrammeID, model.ParseProgrammeID,
	)
	if err != nil {
		return nil, err
	}
	level := &model.ProgrammeLevel{
		ID:          id,
		CreatedAt:   row.CreatedAt.UTC(),
		UpdatedAt:   row.UpdatedAt.UTC(),
		ArchivedAt:  OptionalTimeFromNullTime(row.ArchivedAt),
		Revision:    row.Revision,
		ProgrammeID: programmeID,
		Name:        row.Name,
		DisplayName: row.DisplayName,
		Description: row.Description,
	}
	if err := validatePersistedModel("programme_level", level); err != nil {
		return nil, err
	}
	return level, nil
}

var _ store.ProgrammeLevelStore = (*SQLProgrammeLevelStore)(nil)
