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
// model lifecycle, and store-error boundary while implementing concrete class
// rosters scoped by programme level and academic period.

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

type SQLClassStore struct {
	*SQLStore
	classesQuery sq.SelectBuilder
}

const classLifecycleLock = "proctor:class-lifecycle"

type classRow struct {
	ID               string       `db:"id"`
	CreatedAt        time.Time    `db:"created_at"`
	UpdatedAt        time.Time    `db:"updated_at"`
	ArchivedAt       sql.NullTime `db:"archived_at"`
	Revision         int64        `db:"revision"`
	ProgrammeLevelID string       `db:"programme_level_id"`
	AcademicPeriodID string       `db:"academic_period_id"`
	Name             string       `db:"name"`
	DisplayName      string       `db:"display_name"`
	Description      string       `db:"description"`
}

func classSliceColumns() []string {
	return []string{
		"classes.id",
		"classes.created_at",
		"classes.updated_at",
		"classes.archived_at",
		"classes.revision",
		"classes.programme_level_id",
		"classes.academic_period_id",
		"classes.name",
		"classes.display_name",
		"classes.description",
	}
}

func newSQLClassStore(sqlStore *SQLStore) store.ClassStore {
	s := &SQLClassStore{SQLStore: sqlStore}
	s.classesQuery = s.getQueryBuilder().
		Select(classSliceColumns()...).
		From("classes")
	return s
}

func (s SQLClassStore) Create(ctx context.Context, input *store.ClassCreation) (*model.Class, error) {
	if input == nil || input.Class == nil || !model.IsValidId(input.AuditEventID) || input.AuditAt <= 0 {
		return nil, store.NewErrInvalidInput("class", "creation", nil)
	}
	if !input.Class.ID.IsValid() {
		return nil, store.NewErrInvalidInput("class", "id", input.Class.ID.String())
	}
	candidate := *input.Class
	if err := candidate.Validate(); err != nil {
		return nil, store.NewErrInvalidInput("class", "value", nil).Wrap(err)
	}
	if err := s.validateInstitution(ctx, &candidate); err != nil {
		return nil, err
	}
	encoded, appErr := model.EncodeAuditData(candidate.Auditable())
	if appErr != nil {
		return nil, appErr
	}
	return runSQLTransaction(ctx, s.GetMaster().Begin, "class creation", func(ctx context.Context, tx *sqlxTxWrapper) (*model.Class, error) {
		if err := lockProgrammeLevelLifecycle(ctx, tx); err != nil {
			return nil, err
		}
		if err := lockAcademicPeriodLifecycle(ctx, tx); err != nil {
			return nil, err
		}
		if err := validateActiveProgrammeLevel(ctx, tx, candidate.ProgrammeLevelID.String()); err != nil {
			return nil, err
		}
		if err := validateAcademicPeriodApplicable(ctx, tx, candidate.ProgrammeLevelID.String(), candidate.AcademicPeriodID.String()); err != nil {
			return nil, err
		}
		row := newClassRow(&candidate)
		if _, err := tx.NamedExec(ctx, `INSERT INTO classes (
			id, created_at, updated_at, archived_at, revision, programme_level_id,
			academic_period_id, name, display_name, description
		) VALUES (
			:id, :created_at, :updated_at, :archived_at, :revision, :programme_level_id,
			:academic_period_id, :name, :display_name, :description
		)`, &row); err != nil {
			return nil, fmt.Errorf("create class: %w", translateError("class", candidate.ID.String(), err))
		}
		if _, err := completeAuditEvent(ctx, tx, input.AuditEventID, model.AuditStatusSuccess, "", encoded, input.AuditAt); err != nil {
			return nil, fmt.Errorf("complete class creation audit: %w", err)
		}
		return &candidate, nil
	})
}

func (s SQLClassStore) Save(ctx context.Context, class *model.Class) (*model.Class, error) {
	if class == nil {
		return nil, store.NewErrInvalidInput("class", "value", nil)
	}
	if !class.ID.IsZero() {
		return nil, store.NewErrInvalidInput("class", "id", class.ID.String())
	}

	id, err := model.ParseClassID(model.NewId())
	if err != nil {
		return nil, err
	}
	candidate := *class
	candidate.PrepareCreate(id, model.NowUTC())
	if err := candidate.Validate(); err != nil {
		return nil, store.NewErrInvalidInput("class", "value", nil).Wrap(err)
	}
	if err := s.validateInstitution(ctx, &candidate); err != nil {
		return nil, err
	}
	return runSQLTransaction(ctx, s.GetMaster().Begin, "class save", func(ctx context.Context, tx *sqlxTxWrapper) (*model.Class, error) {
		if err := lockProgrammeLevelLifecycle(ctx, tx); err != nil {
			return nil, err
		}
		if err := lockAcademicPeriodLifecycle(ctx, tx); err != nil {
			return nil, err
		}
		if err := validateActiveProgrammeLevel(ctx, tx, candidate.ProgrammeLevelID.String()); err != nil {
			return nil, err
		}
		if err := validateAcademicPeriodApplicable(ctx, tx, candidate.ProgrammeLevelID.String(), candidate.AcademicPeriodID.String()); err != nil {
			return nil, err
		}
		row := newClassRow(&candidate)
		if _, err := tx.NamedExec(ctx, `
			INSERT INTO classes (
				id, created_at, updated_at, archived_at, revision, programme_level_id,
				academic_period_id, name, display_name, description
			) VALUES (
				:id, :created_at, :updated_at, :archived_at, :revision, :programme_level_id,
				:academic_period_id, :name, :display_name, :description
			)`, &row); err != nil {
			return nil, fmt.Errorf("save class: %w", translateError("class", candidate.ID.String(), err))
		}
		return &candidate, nil
	})
}

func (s SQLClassStore) Get(ctx context.Context, id string) (*model.Class, error) {
	var row classRow
	query := s.classesQuery.Where(sq.Eq{
		"classes.id":          id,
		"classes.archived_at": nil,
	})
	if err := s.GetMaster().GetBuilder(ctx, &row, query); err != nil {
		return nil, translateError("class", id, err)
	}
	return row.model()
}

func (s SQLClassStore) GetByName(
	ctx context.Context,
	programmeLevelID string,
	academicPeriodID string,
	name string,
) (*model.Class, error) {
	var row classRow
	query := s.classesQuery.Where(sq.Eq{
		"classes.programme_level_id": programmeLevelID,
		"classes.academic_period_id": academicPeriodID,
		"classes.name":               name,
		"classes.archived_at":        nil,
	})
	if err := s.GetMaster().GetBuilder(ctx, &row, query); err != nil {
		key := programmeLevelID + "/" + academicPeriodID + "/" + name
		return nil, translateError("class", key, err)
	}
	return row.model()
}

func (s SQLClassStore) ListByProgrammeLevel(
	ctx context.Context,
	programmeLevelID string,
) ([]*model.Class, error) {
	query := s.classesQuery.
		Join("academic_periods ON academic_periods.id = classes.academic_period_id").
		Where(sq.Eq{
			"classes.programme_level_id": programmeLevelID,
			"classes.archived_at":        nil,
		}).
		OrderBy("academic_periods.start_at", "classes.name", "classes.id")
	return s.selectClasses(ctx, query, "list classes by programme level")
}

func (s SQLClassStore) ListByAcademicPeriod(
	ctx context.Context,
	academicPeriodID string,
) ([]*model.Class, error) {
	query := s.classesQuery.
		Where(sq.Eq{
			"classes.academic_period_id": academicPeriodID,
			"classes.archived_at":        nil,
		}).
		OrderBy("classes.programme_level_id", "classes.name", "classes.id")
	return s.selectClasses(ctx, query, "list classes by academic period")
}

func (s SQLClassStore) SearchByAcademicUnit(
	ctx context.Context,
	academicUnitID string,
	term string,
	limit int,
) ([]*model.Class, error) {
	if limit < 1 || limit > 200 {
		return nil, store.NewErrInvalidInput("class", "limit", limit)
	}
	query := s.classesQuery.
		Join("programme_levels ON programme_levels.id = classes.programme_level_id").
		Join("programmes ON programmes.id = programme_levels.programme_id").
		Where(sq.Eq{
			"programmes.academic_unit_id":  academicUnitID,
			"programmes.archived_at":       nil,
			"programme_levels.archived_at": nil,
			"classes.archived_at":          nil,
		}).
		Where("(classes.name ILIKE ? ESCAPE '!' OR classes.display_name ILIKE ? ESCAPE '!')",
			directorySearchPattern(term), directorySearchPattern(term)).
		OrderBy("classes.name", "classes.id").
		Limit(uint64(limit))
	return s.selectClasses(ctx, query, "search classes by academic unit")
}

func (s SQLClassStore) GetAcademicUnitId(ctx context.Context, id string) (string, error) {
	var academicUnitID string
	if err := s.GetMaster().Get(ctx, &academicUnitID, `
		SELECT programmes.academic_unit_id
		  FROM classes
		  JOIN programme_levels ON programme_levels.id = classes.programme_level_id
		  JOIN programmes ON programmes.id = programme_levels.programme_id
		 WHERE classes.id = $1
		   AND classes.archived_at IS NULL
		   AND programme_levels.archived_at IS NULL
		   AND programmes.archived_at IS NULL`, id); err != nil {
		return "", translateError("class", id, err)
	}
	parsed, err := parsePersistedID("class", "academic_unit_id", academicUnitID, model.ParseAcademicUnitID)
	if err != nil {
		return "", err
	}
	return parsed.String(), nil
}

func (s SQLClassStore) selectClasses(
	ctx context.Context,
	query sq.SelectBuilder,
	operation string,
) ([]*model.Class, error) {
	rows := []classRow{}
	if err := s.GetMaster().SelectBuilder(ctx, &rows, query); err != nil {
		return nil, fmt.Errorf("%s: %w", operation, err)
	}
	classes := make([]*model.Class, 0, len(rows))
	for _, row := range rows {
		class, err := row.model()
		if err != nil {
			return nil, err
		}
		classes = append(classes, class)
	}
	return classes, nil
}

func (s SQLClassStore) Update(ctx context.Context, class *model.Class) (*model.Class, error) {
	if class == nil {
		return nil, store.NewErrInvalidInput("class", "value", nil)
	}

	candidate := *class
	expectedRevision := candidate.Revision
	candidate.PrepareUpdate(model.NowUTC())
	if err := candidate.Validate(); err != nil {
		return nil, store.NewErrInvalidInput("class", "value", nil).Wrap(err)
	}
	if err := s.validateInstitution(ctx, &candidate); err != nil {
		return nil, err
	}
	return runSQLTransaction(ctx, s.GetMaster().Begin, "class update", func(ctx context.Context, tx *sqlxTxWrapper) (*model.Class, error) {
		if err := lockProgrammeLifecycle(ctx, tx); err != nil {
			return nil, err
		}
		if err := lockProgrammeLevelLifecycle(ctx, tx); err != nil {
			return nil, err
		}
		if err := lockAcademicPeriodLifecycle(ctx, tx); err != nil {
			return nil, err
		}
		if err := lockClassLifecycle(ctx, tx); err != nil {
			return nil, err
		}
		if err := validateActiveProgrammeLevel(ctx, tx, candidate.ProgrammeLevelID.String()); err != nil {
			return nil, err
		}
		if err := validateAcademicPeriodApplicable(ctx, tx, candidate.ProgrammeLevelID.String(), candidate.AcademicPeriodID.String()); err != nil {
			return nil, err
		}
		result, err := tx.NamedExec(ctx, `
		UPDATE classes
		   SET updated_at = :updated_at,
		       revision = :revision,
		       programme_level_id = :programme_level_id,
		       academic_period_id = :academic_period_id,
		       name = :name,
		       display_name = :display_name,
		       description = :description
		 WHERE id = :id AND archived_at IS NULL AND revision = :expected_revision`, map[string]any{
			"id": candidate.ID.String(), "updated_at": candidate.UpdatedAt, "revision": candidate.Revision,
			"programme_level_id": candidate.ProgrammeLevelID.String(), "academic_period_id": candidate.AcademicPeriodID.String(),
			"name": candidate.Name, "display_name": candidate.DisplayName, "description": candidate.Description,
			"expected_revision": expectedRevision,
		})
		if err != nil {
			return nil, fmt.Errorf("update class: %w", translateError("class", candidate.ID.String(), err))
		}
		if err := requireClassRevisionAffected(ctx, tx, result, candidate.ID.String()); err != nil {
			return nil, err
		}
		return &candidate, nil
	})
}

func (s SQLClassStore) UpdateWithAudit(ctx context.Context, input *store.ClassUpdate) (*model.Class, error) {
	if input == nil || input.Class == nil || !model.IsValidId(input.ExpectedAcademicUnitID) ||
		input.ExpectedRevision <= 0 || !model.IsValidId(input.AuditEventID) || input.AuditAt <= 0 {
		return nil, store.NewErrInvalidInput("class", "update", nil)
	}
	candidate := *input.Class
	candidate.Revision = input.ExpectedRevision + 1
	if err := candidate.Validate(); err != nil {
		return nil, store.NewErrInvalidInput("class", "value", nil).Wrap(err)
	}
	if err := s.validateInstitution(ctx, &candidate); err != nil {
		return nil, err
	}
	encoded, appErr := model.EncodeAuditData(candidate.Auditable())
	if appErr != nil {
		return nil, appErr
	}
	return runSQLTransaction(ctx, s.GetMaster().Begin, "class audited update", func(ctx context.Context, tx *sqlxTxWrapper) (*model.Class, error) {
		if err := lockProgrammeLifecycle(ctx, tx); err != nil {
			return nil, err
		}
		if err := lockProgrammeLevelLifecycle(ctx, tx); err != nil {
			return nil, err
		}
		if err := lockAcademicPeriodLifecycle(ctx, tx); err != nil {
			return nil, err
		}
		if err := lockClassLifecycle(ctx, tx); err != nil {
			return nil, err
		}
		if err := requireExpectedClassSnapshot(ctx, tx, candidate.ID.String(), input.ExpectedAcademicUnitID, input.ExpectedRevision); err != nil {
			return nil, err
		}
		if err := validateActiveProgrammeLevel(ctx, tx, candidate.ProgrammeLevelID.String()); err != nil {
			return nil, err
		}
		if err := validateAcademicPeriodApplicable(ctx, tx, candidate.ProgrammeLevelID.String(), candidate.AcademicPeriodID.String()); err != nil {
			return nil, err
		}
		result, err := tx.NamedExec(ctx, `UPDATE classes SET
		updated_at = :updated_at, programme_level_id = :programme_level_id,
		academic_period_id = :academic_period_id, revision = :revision, name = :name,
		display_name = :display_name, description = :description
	 WHERE id = :id AND archived_at IS NULL AND revision = :expected_revision`, map[string]any{
			"id": candidate.ID.String(), "updated_at": candidate.UpdatedAt, "programme_level_id": candidate.ProgrammeLevelID.String(),
			"academic_period_id": candidate.AcademicPeriodID.String(), "revision": candidate.Revision,
			"name": candidate.Name, "display_name": candidate.DisplayName, "description": candidate.Description,
			"expected_revision": input.ExpectedRevision,
		})
		if err != nil {
			return nil, fmt.Errorf("update class: %w", translateError("class", candidate.ID.String(), err))
		}
		if err := requireClassRevisionAffected(ctx, tx, result, candidate.ID.String()); err != nil {
			return nil, err
		}
		if _, err := completeAuditEvent(ctx, tx, input.AuditEventID, model.AuditStatusSuccess, "", encoded, input.AuditAt); err != nil {
			return nil, fmt.Errorf("complete class update audit: %w", err)
		}
		return &candidate, nil
	})
}

func (s SQLClassStore) Archive(
	ctx context.Context,
	id string,
	archiveAt int64,
) (*model.Class, error) {
	if archiveAt <= 0 {
		return nil, store.NewErrInvalidInput("class", "archived_at", archiveAt)
	}
	return runSQLTransaction(ctx, s.GetMaster().Begin, "class archive", func(ctx context.Context, tx *sqlxTxWrapper) (*model.Class, error) {
		if err := lockClassLifecycle(ctx, tx); err != nil {
			return nil, err
		}
		var row classRow
		query := s.classesQuery.Where(sq.Eq{"classes.id": id, "classes.archived_at": nil})
		if err := tx.GetBuilder(ctx, &row, query); err != nil {
			return nil, translateError("class", id, err)
		}
		current, err := row.model()
		if err != nil {
			return nil, err
		}
		var dependent bool
		if err := tx.Get(ctx, &dependent, `
		SELECT EXISTS (
			SELECT 1 FROM class_members
			 WHERE class_id = ? AND archived_at IS NULL AND end_at IS NULL
			UNION ALL
			SELECT 1 FROM role_bindings
			 WHERE scope_type = 'class' AND scope_id = ?
			   AND archived_at IS NULL AND end_at IS NULL
		)`, id, id); err != nil {
			return nil, fmt.Errorf("check class archive dependencies: %w", err)
		}
		if dependent {
			return nil, store.NewErrConflict("class", "class_has_active_dependents", nil)
		}
		result, err := tx.Exec(ctx, `
		UPDATE classes SET updated_at = GREATEST(created_at, ?), archived_at = GREATEST(created_at, ?), revision = revision + 1
		 WHERE id = ? AND archived_at IS NULL`, model.TimeFromMillis(archiveAt), model.TimeFromMillis(archiveAt), id)
		if err != nil {
			return nil, fmt.Errorf("archive class: %w", err)
		}
		if err := requireAffected(result, "class", id); err != nil {
			return nil, err
		}
		at := model.TimeFromMillis(archiveAt)
		current.UpdatedAt = at
		current.ArchivedAt = model.OptionalTimeFromMillis(archiveAt)
		current.Revision++
		return current, nil
	})
}

func (s SQLClassStore) ArchiveWithAudit(ctx context.Context, input *store.ClassArchive) (*model.Class, error) {
	if input == nil || !model.IsValidId(input.ID) || !model.IsValidId(input.ExpectedAcademicUnitID) ||
		input.ExpectedRevision <= 0 || input.ArchiveAt <= 0 || !model.IsValidId(input.AuditEventID) || input.AuditAt <= 0 {
		return nil, store.NewErrInvalidInput("class", "archive", nil)
	}
	return runSQLTransaction(ctx, s.GetMaster().Begin, "class archive", func(ctx context.Context, tx *sqlxTxWrapper) (*model.Class, error) {
		if err := lockProgrammeLifecycle(ctx, tx); err != nil {
			return nil, err
		}
		if err := lockClassLifecycle(ctx, tx); err != nil {
			return nil, err
		}
		if err := requireExpectedClassSnapshot(ctx, tx, input.ID, input.ExpectedAcademicUnitID, input.ExpectedRevision); err != nil {
			return nil, err
		}
		var row classRow
		query := s.classesQuery.Where(sq.Eq{"classes.id": input.ID, "classes.archived_at": nil})
		if err := tx.GetBuilder(ctx, &row, query); err != nil {
			return nil, translateError("class", input.ID, err)
		}
		var dependent bool
		if err := tx.Get(ctx, &dependent, `SELECT EXISTS (
		SELECT 1 FROM class_members WHERE class_id = ? AND archived_at IS NULL AND end_at IS NULL
		UNION ALL SELECT 1 FROM role_bindings WHERE scope_type = 'class' AND scope_id = ? AND archived_at IS NULL AND end_at IS NULL
	)`, input.ID, input.ID); err != nil {
			return nil, fmt.Errorf("check class archive dependencies: %w", err)
		}
		if dependent {
			return nil, store.NewErrConflict("class", "class_has_active_dependents", nil)
		}
		result, err := tx.Exec(ctx, `UPDATE classes SET updated_at = GREATEST(created_at, ?), archived_at = GREATEST(created_at, ?), revision = revision + 1 WHERE id = ? AND archived_at IS NULL AND revision = ?`, model.TimeFromMillis(input.ArchiveAt), model.TimeFromMillis(input.ArchiveAt), input.ID, input.ExpectedRevision)
		if err != nil {
			return nil, fmt.Errorf("archive class: %w", err)
		}
		if err := requireClassRevisionAffected(ctx, tx, result, input.ID); err != nil {
			return nil, err
		}
		class, err := row.model()
		if err != nil {
			return nil, err
		}
		at := model.TimeFromMillis(input.ArchiveAt)
		class.UpdatedAt = at
		class.ArchivedAt = model.OptionalTimeFromMillis(input.ArchiveAt)
		class.Revision = input.ExpectedRevision + 1
		encoded, appErr := model.EncodeAuditData(class.Auditable())
		if appErr != nil {
			return nil, appErr
		}
		if _, err := completeAuditEvent(ctx, tx, input.AuditEventID, model.AuditStatusSuccess, "", encoded, input.AuditAt); err != nil {
			return nil, fmt.Errorf("complete class archive audit: %w", err)
		}
		return class, nil
	})
}

func lockClassLifecycle(ctx context.Context, tx sqlxExecutor) error {
	if _, err := tx.Exec(ctx, "SELECT pg_advisory_xact_lock(hashtext(?))", classLifecycleLock); err != nil {
		return fmt.Errorf("lock class lifecycle: %w", err)
	}
	return nil
}

func requireExpectedClassSnapshot(ctx context.Context, executor sqlxExecutor, id, expectedAcademicUnitID string, expectedRevision int64) error {
	var snapshot struct {
		AcademicUnitID string `db:"academic_unit_id"`
		Revision       int64  `db:"revision"`
	}
	if err := executor.Get(ctx, &snapshot, `
		SELECT academic_units.id AS academic_unit_id, classes.revision
		  FROM classes
		  JOIN programme_levels ON programme_levels.id = classes.programme_level_id
		  JOIN programmes ON programmes.id = programme_levels.programme_id
		  JOIN academic_units ON academic_units.id = programmes.academic_unit_id
		 WHERE classes.id = ?
		   AND classes.archived_at IS NULL
		   AND programme_levels.archived_at IS NULL
		   AND programmes.archived_at IS NULL
		   AND academic_units.archived_at IS NULL`, id); err != nil {
		return translateError("class", id, err)
	}
	academicUnitID, err := parsePersistedID("class", "academic_unit_id", snapshot.AcademicUnitID, model.ParseAcademicUnitID)
	if err != nil {
		return err
	}
	if academicUnitID.String() != expectedAcademicUnitID || snapshot.Revision != expectedRevision {
		return store.NewErrConflict("class", "class_changed", nil)
	}
	return nil
}

func requireClassRevisionAffected(ctx context.Context, executor sqlxExecutor, result sql.Result, id string) error {
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read class affected rows: %w", err)
	}
	if affected != 0 {
		return nil
	}
	var exists bool
	if err := executor.Get(ctx, &exists, `SELECT EXISTS (SELECT 1 FROM classes WHERE id = ? AND archived_at IS NULL)`, id); err != nil {
		return fmt.Errorf("check class revision conflict: %w", err)
	}
	if exists {
		return store.NewErrConflict("class", "class_changed", nil)
	}
	return store.NewErrNotFound("class", id).Wrap(sql.ErrNoRows)
}

func (s SQLClassStore) validateInstitution(
	ctx context.Context,
	class *model.Class,
) error {
	return validateAcademicPeriodApplicable(
		ctx, s.GetMaster(), class.ProgrammeLevelID.String(), class.AcademicPeriodID.String(),
	)
}

func validateAcademicPeriodApplicable(ctx context.Context, executor sqlxExecutor, levelID, periodID string) error {
	var applicable bool
	if err := executor.Get(ctx, &applicable, `WITH RECURSIVE lineage AS (
		SELECT au.id, au.parent_id, au.institution_id
		  FROM programme_levels pl
		  JOIN programmes p ON p.id = pl.programme_id AND p.archived_at IS NULL
		  JOIN academic_units au ON au.id = p.academic_unit_id AND au.archived_at IS NULL
		 WHERE pl.id = ? AND pl.archived_at IS NULL
		UNION ALL
		SELECT parent.id, parent.parent_id, parent.institution_id
		  FROM academic_units parent JOIN lineage child ON child.parent_id = parent.id
		 WHERE parent.archived_at IS NULL
	)
	SELECT EXISTS (
		SELECT 1 FROM academic_periods ap
		 WHERE ap.id = ? AND ap.archived_at IS NULL
		   AND (
			(ap.owner_type = 'institution' AND ap.institution_id = (SELECT institution_id FROM lineage LIMIT 1))
			OR (ap.owner_type = 'academic_unit' AND ap.academic_unit_id IN (SELECT id FROM lineage))
		   )
	)`, levelID, periodID); err != nil {
		return fmt.Errorf("validate class academic period applicability: %w", err)
	}
	if !applicable {
		var levelExists, periodExists bool
		if err := executor.Get(ctx, &levelExists, "SELECT EXISTS (SELECT 1 FROM programme_levels WHERE id = ?)", levelID); err != nil {
			return fmt.Errorf("check class programme level reference: %w", err)
		}
		if !levelExists {
			return store.NewErrReference("class", "classes_programme_level_id_fkey", nil)
		}
		if err := executor.Get(ctx, &periodExists, "SELECT EXISTS (SELECT 1 FROM academic_periods WHERE id = ?)", periodID); err != nil {
			return fmt.Errorf("check class academic period reference: %w", err)
		}
		if !periodExists {
			return store.NewErrReference("class", "classes_academic_period_id_fkey", nil)
		}
		return store.NewErrReference("class", "classes_academic_period_not_applicable", nil)
	}
	return nil
}

func newClassRow(class *model.Class) classRow {
	return classRow{
		ID:               class.ID.String(),
		CreatedAt:        UTCTime(class.CreatedAt),
		UpdatedAt:        UTCTime(class.UpdatedAt),
		ArchivedAt:       NullTimeFromOptional(class.ArchivedAt),
		Revision:         class.Revision,
		ProgrammeLevelID: class.ProgrammeLevelID.String(),
		AcademicPeriodID: class.AcademicPeriodID.String(),
		Name:             class.Name,
		DisplayName:      class.DisplayName,
		Description:      class.Description,
	}
}

func (row classRow) model() (*model.Class, error) {
	id, err := parsePersistedID("class", "id", row.ID, model.ParseClassID)
	if err != nil {
		return nil, err
	}
	levelID, err := parsePersistedID("class", "programme_level_id", row.ProgrammeLevelID, model.ParseProgrammeLevelID)
	if err != nil {
		return nil, err
	}
	periodID, err := parsePersistedID("class", "academic_period_id", row.AcademicPeriodID, model.ParseAcademicPeriodID)
	if err != nil {
		return nil, err
	}
	class := &model.Class{
		ID:               id,
		CreatedAt:        row.CreatedAt.UTC(),
		UpdatedAt:        row.UpdatedAt.UTC(),
		ArchivedAt:       OptionalTimeFromNullTime(row.ArchivedAt),
		Revision:         row.Revision,
		ProgrammeLevelID: levelID,
		AcademicPeriodID: periodID,
		Name:             row.Name,
		DisplayName:      row.DisplayName,
		Description:      row.Description,
	}
	if err := validatePersistedModel("class", class); err != nil {
		return nil, err
	}
	return class, nil
}

var _ store.ClassStore = (*SQLClassStore)(nil)
