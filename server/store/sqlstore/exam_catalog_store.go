// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package sqlstore

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/lib/pq"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

type examSummaryRow struct {
	ID                      string         `db:"id"`
	AcademicUnitID          string         `db:"academic_unit_id"`
	AcademicUnitDisplayName string         `db:"academic_unit_display_name"`
	CreatorUserID           string         `db:"creator_user_id"`
	OwnerUserID             string         `db:"owner_user_id"`
	Title                   string         `db:"title"`
	UpdatedAt               time.Time      `db:"updated_at"`
	ArchivedAt              sql.NullTime   `db:"archived_at"`
	Revision                int64          `db:"revision"`
	ManagerCount            int            `db:"manager_count"`
	SittingCount            int            `db:"sitting_count"`
	MatchingSittingCount    int            `db:"matching_sitting_count"`
	SittingID               sql.NullString `db:"sitting_id"`
	SittingExamRevisionID   sql.NullString `db:"sitting_exam_revision_id"`
	SittingClassID          sql.NullString `db:"sitting_class_id"`
	SittingClassDisplayName sql.NullString `db:"sitting_class_display_name"`
	SittingScheduledStartAt sql.NullTime   `db:"sitting_scheduled_start_at"`
	SittingScheduledEndAt   sql.NullTime   `db:"sitting_scheduled_end_at"`
	SittingState            sql.NullString `db:"sitting_state"`
	SittingRevision         sql.NullInt64  `db:"sitting_revision"`
}

type examCatalogReader interface {
	Select(context.Context, any, string, ...any) error
}

func (s SQLExamAuthoringStore) List(ctx context.Context, options store.ExamListOptions) ([]store.ExamSummary, error) {
	if err := validateExamListOptions(options); err != nil {
		return nil, err
	}
	query, args := examCatalogQuery(options)
	var rows []examSummaryRow
	reader := s.catalogReader
	if reader == nil {
		reader = s.GetMaster()
	}
	if err := reader.Select(ctx, &rows, query, args...); err != nil {
		return nil, fmt.Errorf("list Exam catalog: %w", err)
	}
	result := make([]store.ExamSummary, 0, len(rows))
	for _, row := range rows {
		summary, err := row.model()
		if err != nil {
			return nil, err
		}
		result = append(result, summary)
	}
	return result, nil
}

func examCatalogQuery(options store.ExamListOptions) (string, []any) {
	ordinaryRoots := append([]string(nil), options.Visibility.OrdinaryAcademicUnitRootIDs...)
	overrideRoots := append([]string(nil), options.Visibility.OverrideAcademicUnitRootIDs...)
	hasSittingFilters := len(options.SittingStates) > 0 || !options.EndsAfter.IsZero()
	query := `WITH RECURSIVE ordinary_units AS (
		SELECT id FROM academic_units WHERE id = ANY(?) AND archived_at IS NULL
		UNION ALL SELECT child.id FROM academic_units child JOIN ordinary_units parent ON child.parent_id = parent.id WHERE child.archived_at IS NULL
	), override_units AS (
		SELECT id FROM academic_units WHERE id = ANY(?) AND archived_at IS NULL
		UNION ALL SELECT child.id FROM academic_units child JOIN override_units parent ON child.parent_id = parent.id WHERE child.archived_at IS NULL
	), eligible_sittings AS (
		SELECT s.* FROM exam_sittings s WHERE TRUE`
	args := []any{pq.Array(ordinaryRoots), pq.Array(overrideRoots)}
	if len(options.SittingStates) > 0 {
		states := make([]string, len(options.SittingStates))
		for index, state := range options.SittingStates {
			states[index] = string(state)
		}
		query += ` AND s.state=ANY(?)`
		args = append(args, pq.Array(states))
	}
	if !options.EndsAfter.IsZero() {
		query += ` AND s.scheduled_end_at>? AND s.scheduled_start_at<?`
		args = append(args, model.TimeUTC(options.EndsAfter), model.TimeUTC(options.StartsBefore))
	}
	query += `)
	SELECT e.id, e.academic_unit_id, au.display_name AS academic_unit_display_name,
		e.creator_user_id, e.owner_user_id, d.title,
		e.updated_at, e.archived_at, e.revision,
		(SELECT COUNT(*) FROM exam_managers counted WHERE counted.exam_id=e.id) AS manager_count,
		(SELECT COUNT(*) FROM exam_sittings counted WHERE counted.exam_id=e.id) AS sitting_count,
		(SELECT COUNT(*) FROM eligible_sittings counted WHERE counted.exam_id=e.id) AS matching_sitting_count,
		selected.id AS sitting_id, selected.exam_revision_id AS sitting_exam_revision_id,
		selected.class_id AS sitting_class_id, selected.class_display_name AS sitting_class_display_name,
		selected.scheduled_start_at AS sitting_scheduled_start_at, selected.scheduled_end_at AS sitting_scheduled_end_at,
		selected.state AS sitting_state, selected.revision AS sitting_revision
	FROM exams e JOIN exam_drafts d ON d.exam_id=e.id JOIN academic_units au ON au.id=e.academic_unit_id
	LEFT JOIN LATERAL (
		SELECT s.id,s.exam_revision_id,s.class_id,c.display_name AS class_display_name,
			s.scheduled_start_at,s.scheduled_end_at,s.state,s.revision
		FROM eligible_sittings s JOIN classes c ON c.id=s.class_id WHERE s.exam_id=e.id
		ORDER BY CASE s.state WHEN 'paused' THEN 1 WHEN 'closing' THEN 2 WHEN 'open' THEN 3
			WHEN 'scheduled' THEN 4 ELSE 5 END,
			CASE WHEN s.state IN ('paused','closing','open') THEN s.scheduled_end_at END ASC,
			CASE WHEN s.state IN ('paused','closing','open') THEN s.scheduled_start_at END ASC,
			CASE WHEN s.state='scheduled' THEN s.scheduled_start_at END ASC,
			CASE WHEN s.state IN ('closed','canceled') THEN s.scheduled_end_at END DESC,
			CASE WHEN s.state IN ('closed','canceled') THEN s.id END DESC,
			s.id ASC LIMIT 1
	) selected ON TRUE
	WHERE (? = '' OR e.academic_unit_id = ?)
	`
	args = append(args, options.AcademicUnitID.String(), options.AcademicUnitID.String())
	if hasSittingFilters {
		query += ` AND EXISTS (SELECT 1 FROM eligible_sittings matching WHERE matching.exam_id=e.id)`
	}
	switch options.ArchiveFilter {
	case store.ExamArchiveActive:
		query += ` AND e.archived_at IS NULL`
	case store.ExamArchiveArchived:
		query += ` AND e.archived_at IS NOT NULL`
	case store.ExamArchiveAll:
	}
	if strings.TrimSpace(options.Query) != "" {
		query += ` AND d.title ILIKE ? ESCAPE '!'`
		args = append(args, directorySearchPattern(options.Query))
	}
	if !options.BeforeUpdatedAt.IsZero() {
		query += ` AND (e.updated_at, e.id) < (?, ?)`
		args = append(args, model.TimeUTC(options.BeforeUpdatedAt), options.BeforeExamID.String())
	}
	var visibilityPredicates []string
	if options.Visibility.OverrideInstitutionWide {
		visibilityPredicates = append(visibilityPredicates, `TRUE`)
	} else if len(overrideRoots) > 0 {
		visibilityPredicates = append(visibilityPredicates, `e.academic_unit_id IN (SELECT id FROM override_units)`)
	}
	if options.Visibility.OrdinaryInstitutionWide || len(ordinaryRoots) > 0 {
		visibilityPredicates = append(visibilityPredicates, `(
			EXISTS (SELECT 1 FROM exam_managers actor_manager WHERE actor_manager.exam_id = e.id AND actor_manager.user_id = ?)
			AND EXISTS (
				SELECT 1 FROM academic_unit_members actor_member
				WHERE actor_member.academic_unit_id = e.academic_unit_id AND actor_member.user_id = ?
					AND actor_member.archived_at IS NULL AND actor_member.start_at <= ?
					AND (actor_member.end_at IS NULL OR actor_member.end_at > ?)
			)
			AND (? OR e.academic_unit_id IN (SELECT id FROM ordinary_units))
		)`)
		membershipAt := model.TimeUTC(options.Visibility.OrdinaryMembershipAt)
		args = append(args, options.Visibility.ActorUserID.String(), options.Visibility.ActorUserID.String(), membershipAt, membershipAt,
			options.Visibility.OrdinaryInstitutionWide)
	}
	if len(visibilityPredicates) == 0 {
		visibilityPredicates = append(visibilityPredicates, `FALSE`)
	}
	query += ` AND (` + strings.Join(visibilityPredicates, ` OR `) + `)`
	query += ` ORDER BY e.updated_at DESC, e.id DESC LIMIT ?`
	args = append(args, options.Limit)
	return query, args
}

func (s SQLExamAuthoringStore) Archive(ctx context.Context, input *store.ExamArchive, command *store.CommandIdempotency) (*store.ExamArchiveCommandResult, error) {
	prepared, err := prepareExamArchive(input)
	if err != nil || command == nil {
		if err != nil {
			return nil, err
		}
		return nil, store.NewErrInvalidInput("exam", "idempotency", nil)
	}
	result, err := runIdempotentMutation(ctx, s.SQLStore, "exam archive", idempotentMutation[*model.Exam]{
		command: command, auditEventID: prepared.AuditEventID,
		execute: func(ctx context.Context, tx *sqlxTxWrapper) (*model.Exam, error) {
			return archiveExam(ctx, tx, prepared)
		},
		encode: func(exam *model.Exam) ([]byte, error) { return encodeCommandOutcome(examAccessRowFromModel(exam)) },
		decode: func(version int, data []byte) (*model.Exam, error) {
			if version != 1 {
				return nil, fmt.Errorf("unsupported exam archive outcome version %d", version)
			}
			var row examAccessRow
			if err := decodeCommandOutcome(data, &row); err != nil {
				return nil, err
			}
			return row.model()
		},
		completeReplay: func(ctx context.Context, tx *sqlxTxWrapper, exam *model.Exam, originalAuditID string) error {
			encoded, encodeErr := model.EncodeAuditData(map[string]any{
				"exam_id": exam.ID.String(), "exam_revision": exam.Revision,
				"idempotency_replayed": true, "original_audit_event_id": originalAuditID,
			})
			if encodeErr != nil {
				return encodeErr
			}
			_, completeErr := completeAuditEvent(ctx, tx, prepared.AuditEventID, model.AuditStatusSuccess, "", encoded, prepared.AuditAt)
			return completeErr
		},
	})
	if err != nil {
		return nil, err
	}
	return &store.ExamArchiveCommandResult{Value: result.Value, Replayed: result.Replayed}, nil
}

func validateExamListOptions(options store.ExamListOptions) error {
	hasOrdinary := options.Visibility.OrdinaryInstitutionWide || len(options.Visibility.OrdinaryAcademicUnitRootIDs) > 0
	if options.Limit < 1 || options.Limit > 200 || !options.Visibility.ActorUserID.IsValid() ||
		hasOrdinary && options.Visibility.OrdinaryMembershipAt.IsZero() ||
		(options.BeforeUpdatedAt.IsZero() != options.BeforeExamID.IsZero()) ||
		len(options.Visibility.OrdinaryAcademicUnitRootIDs)+len(options.Visibility.OverrideAcademicUnitRootIDs) > 256 ||
		!validVisibilityIDs(options.Visibility.OrdinaryAcademicUnitRootIDs) || !validVisibilityIDs(options.Visibility.OverrideAcademicUnitRootIDs) {
		return store.NewErrInvalidInput("exam", "list_options", nil)
	}
	switch options.ArchiveFilter {
	case store.ExamArchiveActive, store.ExamArchiveArchived, store.ExamArchiveAll:
	default:
		return store.NewErrInvalidInput("exam", "archive_filter", nil)
	}
	if !options.AcademicUnitID.IsZero() && !options.AcademicUnitID.IsValid() {
		return store.NewErrInvalidInput("exam", "academic_unit_id", nil)
	}
	if options.EndsAfter.IsZero() != options.StartsBefore.IsZero() ||
		(!options.EndsAfter.IsZero() && !options.EndsAfter.Before(options.StartsBefore)) {
		return store.NewErrInvalidInput("exam", "sitting_interval", nil)
	}
	seenStates := make(map[model.ExamSittingState]struct{}, len(options.SittingStates))
	for _, state := range options.SittingStates {
		if !state.IsValid() {
			return store.NewErrInvalidInput("exam", "sitting_state", nil)
		}
		if _, exists := seenStates[state]; exists {
			return store.NewErrInvalidInput("exam", "sitting_state", nil)
		}
		seenStates[state] = struct{}{}
	}
	return nil
}

func prepareExamArchive(input *store.ExamArchive) (*store.ExamArchive, error) {
	if input == nil || !input.ExamID.IsValid() || !input.ActorUserID.IsValid() || input.ExpectedRevision < 1 ||
		input.ArchivedAt <= 0 || !model.IsValidId(input.AuditEventID) || input.AuditAt <= 0 {
		return nil, store.NewErrInvalidInput("exam", "archive", nil)
	}
	prepared := *input
	return &prepared, nil
}

func archiveExam(ctx context.Context, tx *sqlxTxWrapper, input *store.ExamArchive) (*model.Exam, error) {
	var row examAccessRow
	if err := tx.Get(ctx, &row, examAccessSelect+` FOR UPDATE OF e`, input.ActorUserID.String(), input.ExamID.String()); err != nil {
		return nil, translateError("exam", input.ExamID.String(), err)
	}
	if !row.ActorIsManager && !input.ManagerOverride {
		return nil, store.NewErrNotFound("exam_manager", input.ActorUserID.String())
	}
	exam, err := row.model()
	if err != nil {
		return nil, err
	}
	if exam.IsArchived() {
		return nil, store.NewErrConflict("exam", "exam_archived", nil)
	}
	if exam.Revision != input.ExpectedRevision {
		return nil, store.NewErrConflict("exam", "exam_revision", nil)
	}
	if err := exam.Archive(model.TimeFromMillis(input.ArchivedAt)); err != nil {
		return nil, store.NewErrInvalidInput("exam", "archive", nil).Wrap(err)
	}
	result, err := tx.Exec(ctx, `UPDATE exams SET updated_at = ?, archived_at = ?, revision = ? WHERE id = ? AND revision = ? AND archived_at IS NULL`,
		exam.UpdatedAt, exam.ArchivedAt.Time, exam.Revision, exam.ID.String(), input.ExpectedRevision)
	if err != nil {
		return nil, fmt.Errorf("archive Exam: %w", translateError("exam", exam.ID.String(), err))
	}
	if affected, rowsErr := result.RowsAffected(); rowsErr != nil {
		return nil, fmt.Errorf("inspect Exam archive: %w", rowsErr)
	} else if affected != 1 {
		return nil, store.NewErrConflict("exam", "exam_revision", nil)
	}
	encoded, err := model.EncodeAuditData(map[string]any{"exam_id": exam.ID.String(), "exam_revision": exam.Revision})
	if err != nil {
		return nil, err
	}
	if _, err := completeAuditEvent(ctx, tx, input.AuditEventID, model.AuditStatusSuccess, "", encoded, input.AuditAt); err != nil {
		return nil, fmt.Errorf("complete Exam archive audit: %w", err)
	}
	return exam, nil
}

func (r examSummaryRow) model() (store.ExamSummary, error) {
	id, err := model.ParseExamID(r.ID)
	if err != nil {
		return store.ExamSummary{}, invalidPersistedState("exam", "id", err)
	}
	unitID, err := model.ParseAcademicUnitID(r.AcademicUnitID)
	if err != nil {
		return store.ExamSummary{}, invalidPersistedState("exam", "academic_unit_id", err)
	}
	creatorID, err := model.ParseUserID(r.CreatorUserID)
	if err != nil {
		return store.ExamSummary{}, invalidPersistedState("exam", "creator_user_id", err)
	}
	ownerID, err := model.ParseUserID(r.OwnerUserID)
	if err != nil {
		return store.ExamSummary{}, invalidPersistedState("exam", "owner_user_id", err)
	}
	if r.Title == "" || r.AcademicUnitDisplayName == "" || r.UpdatedAt.IsZero() || r.Revision < 1 || r.ManagerCount < 1 ||
		r.SittingCount < 0 || r.MatchingSittingCount < 0 || r.MatchingSittingCount > r.SittingCount {
		return store.ExamSummary{}, invalidPersistedState("exam", "summary", errors.New("invalid Exam summary"))
	}
	result := store.ExamSummary{ID: id, AcademicUnitID: unitID, AcademicUnitDisplayName: r.AcademicUnitDisplayName,
		CreatorUserID: creatorID, OwnerUserID: ownerID, Title: r.Title, UpdatedAt: model.TimeUTC(r.UpdatedAt),
		ArchivedAt: OptionalTimeFromNullTime(r.ArchivedAt), Revision: r.Revision, ManagerCount: r.ManagerCount,
		SittingCount: r.SittingCount, MatchingSittingCount: r.MatchingSittingCount}
	if r.SittingID.Valid {
		if !r.SittingExamRevisionID.Valid || !r.SittingClassID.Valid || !r.SittingClassDisplayName.Valid ||
			!r.SittingScheduledStartAt.Valid || !r.SittingScheduledEndAt.Valid || !r.SittingState.Valid || !r.SittingRevision.Valid {
			return store.ExamSummary{}, invalidPersistedState("exam", "sitting_summary", errors.New("partial Sitting summary"))
		}
		sittingID, parseErr := model.ParseExamSittingID(r.SittingID.String)
		if parseErr != nil {
			return store.ExamSummary{}, invalidPersistedState("exam", "sitting_id", parseErr)
		}
		revisionID, parseErr := model.ParseExamRevisionID(r.SittingExamRevisionID.String)
		if parseErr != nil {
			return store.ExamSummary{}, invalidPersistedState("exam", "sitting_exam_revision_id", parseErr)
		}
		classID, parseErr := model.ParseClassID(r.SittingClassID.String)
		state := model.ExamSittingState(r.SittingState.String)
		if parseErr != nil {
			return store.ExamSummary{}, invalidPersistedState("exam", "sitting_class_id", parseErr)
		}
		if !state.IsValid() || r.SittingClassDisplayName.String == "" || r.SittingRevision.Int64 < 1 ||
			!r.SittingScheduledEndAt.Time.After(r.SittingScheduledStartAt.Time) {
			return store.ExamSummary{}, invalidPersistedState("exam", "sitting_summary", errors.New("invalid Sitting summary"))
		}
		result.SittingSummary = &store.ExamCatalogSittingSummary{ID: sittingID, ExamRevisionID: revisionID,
			ClassID: classID, ClassDisplayName: r.SittingClassDisplayName.String,
			ScheduledStartAt: model.TimeUTC(r.SittingScheduledStartAt.Time), ScheduledEndAt: model.TimeUTC(r.SittingScheduledEndAt.Time),
			State: state, Revision: r.SittingRevision.Int64}
	} else if r.SittingExamRevisionID.Valid || r.SittingClassID.Valid || r.SittingClassDisplayName.Valid ||
		r.SittingScheduledStartAt.Valid || r.SittingScheduledEndAt.Valid || r.SittingState.Valid || r.SittingRevision.Valid {
		return store.ExamSummary{}, invalidPersistedState("exam", "sitting_summary", errors.New("orphan Sitting summary fields"))
	}
	return result, nil
}

func examAccessRowFromModel(exam *model.Exam) examAccessRow {
	row := examAccessRow{ID: exam.ID.String(), AcademicUnitID: exam.AcademicUnitID.String(), CreatorUserID: exam.CreatorUserID.String(),
		OwnerUserID: exam.OwnerUserID.String(), CreatedAt: exam.CreatedAt, UpdatedAt: exam.UpdatedAt, Revision: exam.Revision}
	if !exam.DefaultRevisionID.IsZero() {
		row.DefaultRevisionID = sql.NullString{String: exam.DefaultRevisionID.String(), Valid: true}
	}
	if exam.ArchivedAt.Valid {
		row.ArchivedAt = sql.NullTime{Time: exam.ArchivedAt.Time, Valid: true}
	}
	return row
}
