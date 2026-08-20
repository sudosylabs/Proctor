// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only
//
// Adapted from Mattermost server/channels/store/sqlstore/team_member_store.go
// for Proctor's non-exclusive, time-bounded institution affiliations.

package sqlstore

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	sq "github.com/Masterminds/squirrel"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

type SQLAffiliationStore struct {
	*SQLStore
	query sq.SelectBuilder
}

// affiliationRow is the legacy integer-millisecond column layout. Domain
// Affiliation uses time.Time / OptionalTime; conversion is at this boundary.
type affiliationRow struct {
	ID         string                `db:"id"`
	CreatedAt  time.Time             `db:"created_at"`
	UpdatedAt  time.Time             `db:"updated_at"`
	ArchivedAt sql.NullTime          `db:"archived_at"`
	Revision   int64                 `db:"revision"`
	UserID     string                `db:"user_id"`
	Kind       model.AffiliationKind `db:"kind"`
	StartAt    time.Time             `db:"start_at"`
	EndAt      sql.NullTime          `db:"end_at"`
}

type affiliationMutationResult struct {
	Value *model.Affiliation
	NoOp  bool
}

type affiliationCommandOutcome struct {
	ID   string `json:"id"`
	NoOp bool   `json:"no_op,omitempty"`
}

func affiliationColumns() []string {
	return []string{
		"affiliations.id", "affiliations.created_at", "affiliations.updated_at",
		"affiliations.archived_at", "affiliations.user_id", "affiliations.kind",
		"affiliations.revision",
		"affiliations.start_at", "affiliations.end_at",
	}
}

const affiliationLifecycleLock = "proctor:affiliation-lifecycle"

func (s SQLAffiliationStore) Create(ctx context.Context, input *store.AffiliationCreation) (*model.Affiliation, error) {
	if input == nil || input.Affiliation == nil || !model.IsValidId(input.AuditEventID) || input.AuditAt <= 0 {
		return nil, store.NewErrInvalidInput("affiliation", "creation", nil)
	}
	if !input.Affiliation.ID.IsValid() {
		return nil, store.NewErrInvalidInput("affiliation", "id", input.Affiliation.ID.String())
	}
	candidate := *input.Affiliation
	if err := candidate.Validate(); err != nil {
		return nil, store.NewErrInvalidInput("affiliation", "value", nil).Wrap(err)
	}
	encoded, appErr := model.EncodeAuditData(candidate.Auditable())
	if appErr != nil {
		return nil, appErr
	}
	execute := func(ctx context.Context, tx *sqlxTxWrapper) (*affiliationMutationResult, error) {
		if err := lockAffiliationLifecycle(ctx, tx); err != nil {
			return nil, err
		}
		if err := lockAffiliationKind(ctx, tx, candidate.UserID.String(), candidate.Kind); err != nil {
			return nil, err
		}
		existing, err := findExactAffiliation(ctx, tx, &candidate)
		if err != nil {
			return nil, err
		}
		if existing != nil && input.Command != nil {
			if err = completeAdministrativeNoOpAudit(ctx, tx, input.AuditEventID, input.AuditAt, "affiliation_id", existing.ID.String()); err != nil {
				return nil, err
			}
			return &affiliationMutationResult{Value: existing, NoOp: true}, nil
		}
		if err := ensureAffiliationRangeAvailable(ctx, tx, &candidate); err != nil {
			return nil, err
		}
		row := newAffiliationRow(&candidate)
		if _, err := tx.NamedExec(ctx, `INSERT INTO affiliations (
			id, created_at, updated_at, archived_at, revision, user_id, kind, start_at, end_at
		) VALUES (
			:id, :created_at, :updated_at, :archived_at, :revision, :user_id, :kind, :start_at, :end_at
		)`, &row); err != nil {
			return nil, fmt.Errorf("create affiliation: %w", translateError("affiliation", candidate.ID.String(), err))
		}
		if _, err := completeAuditEvent(ctx, tx, input.AuditEventID, model.AuditStatusSuccess, "", encoded, input.AuditAt); err != nil {
			return nil, fmt.Errorf("complete affiliation creation audit: %w", err)
		}
		return &affiliationMutationResult{Value: &candidate}, nil
	}
	if input.Command == nil {
		result, err := runSQLTransaction(ctx, s.GetMaster().Begin, "affiliation creation", execute)
		if err != nil {
			return nil, err
		}
		input.NoOp = result.NoOp
		return result.Value, nil
	}
	result, err := runIdempotentMutation(ctx, s.SQLStore, "idempotent affiliation creation", idempotentMutation[*affiliationMutationResult]{
		command: input.Command, auditEventID: input.AuditEventID, execute: execute,
		encode: encodeAffiliationMutationOutcome, decode: decodeAffiliationMutationOutcome,
		hydrateReplay: s.hydrateAffiliationMutationOutcome,
		onboardingOutcome: func(value *affiliationMutationResult) (onboardingImportCommandResult, error) {
			return administrativeOnboardingOutcome(value.Value.ID.String(), value.NoOp)
		},
		completeReplay: func(ctx context.Context, tx *sqlxTxWrapper, value *affiliationMutationResult, original string) error {
			return completeAdministrativeReplayAudit(ctx, tx, input.AuditEventID, input.AuditAt, "affiliation_id", value.Value.ID.String(), value.NoOp, original)
		},
	})
	if err != nil {
		return nil, err
	}
	input.Replayed, input.NoOp = result.Replayed, result.Value.NoOp
	return result.Value.Value, nil
}

func newSQLAffiliationStore(ss *SQLStore) store.AffiliationStore {
	s := &SQLAffiliationStore{SQLStore: ss}
	s.query = s.getQueryBuilder().Select(affiliationColumns()...).From("affiliations")
	return s
}

func (s SQLAffiliationStore) Save(
	ctx context.Context,
	affiliation *model.Affiliation,
) (*model.Affiliation, error) {
	if affiliation == nil {
		return nil, store.NewErrInvalidInput("affiliation", "value", nil)
	}
	if !affiliation.ID.IsZero() {
		return nil, store.NewErrInvalidInput("affiliation", "id", affiliation.ID.String())
	}
	id, err := model.ParseAffiliationID(model.NewId())
	if err != nil {
		return nil, err
	}
	candidate := *affiliation
	candidate.PrepareCreate(id, model.NowUTC())
	if err := candidate.Validate(); err != nil {
		return nil, store.NewErrInvalidInput("affiliation", "value", nil).Wrap(err)
	}
	row := newAffiliationRow(&candidate)
	return runSQLTransaction(ctx, s.GetMaster().Begin, "affiliation save", func(ctx context.Context, tx *sqlxTxWrapper) (*model.Affiliation, error) {
		if err := lockAffiliationLifecycle(ctx, tx); err != nil {
			return nil, err
		}
		if err := lockAffiliationKind(ctx, tx, candidate.UserID.String(), candidate.Kind); err != nil {
			return nil, err
		}
		if err := ensureAffiliationRangeAvailable(ctx, tx, &candidate); err != nil {
			return nil, err
		}
		if _, err := tx.NamedExec(ctx, `
			INSERT INTO affiliations (
				id, created_at, updated_at, archived_at, revision, user_id, kind, start_at, end_at
			) VALUES (
				:id, :created_at, :updated_at, :archived_at, :revision, :user_id, :kind, :start_at, :end_at
			)`, &row); err != nil {
			return nil, fmt.Errorf(
				"save affiliation: %w",
				translateError("affiliation", candidate.ID.String(), err),
			)
		}
		return &candidate, nil
	})
}

func (s SQLAffiliationStore) Get(ctx context.Context, id string) (*model.Affiliation, error) {
	var row affiliationRow
	if err := s.GetMaster().GetBuilder(ctx, &row, s.query.Where(sq.Eq{
		"affiliations.id": id, "affiliations.archived_at": nil,
	})); err != nil {
		return nil, translateError("affiliation", id, err)
	}
	return row.model()
}

func (s SQLAffiliationStore) ListByUser(
	ctx context.Context,
	userID string,
) ([]*model.Affiliation, error) {
	return s.selectAffiliations(ctx, s.query.Where(sq.Eq{
		"affiliations.user_id": userID, "affiliations.archived_at": nil,
	}).OrderBy("affiliations.start_at DESC", "affiliations.id"))
}

func (s SQLAffiliationStore) ListActiveByUser(
	ctx context.Context,
	userID string,
	at int64,
) ([]*model.Affiliation, error) {
	activeAt := model.TimeFromMillis(at)
	return s.selectAffiliations(ctx, s.query.Where(sq.Eq{
		"affiliations.user_id": userID, "affiliations.archived_at": nil,
	}).Where(sq.LtOrEq{"affiliations.start_at": activeAt}).
		Where("(affiliations.end_at IS NULL OR affiliations.end_at > ?)", activeAt).
		OrderBy("affiliations.kind", "affiliations.id"))
}

func (s SQLAffiliationStore) End(
	ctx context.Context,
	id string,
	expectedRevision int64,
	endAt int64,
) (*model.Affiliation, error) {
	if !model.IsValidId(id) || expectedRevision <= 0 || endAt <= 0 {
		return nil, store.NewErrInvalidInput("affiliation", "end", nil)
	}
	return runSQLTransaction(ctx, s.GetMaster().Begin, "affiliation end", func(ctx context.Context, tx *sqlxTxWrapper) (*model.Affiliation, error) {
		if err := lockAffiliationLifecycle(ctx, tx); err != nil {
			return nil, err
		}
		return s.endAffiliation(ctx, tx, id, expectedRevision, endAt)
	})
}

func (s SQLAffiliationStore) EndWithAudit(ctx context.Context, input *store.AffiliationEnd) (*model.Affiliation, error) {
	if input == nil || !model.IsValidId(input.ID) || input.ExpectedRevision <= 0 || input.EndAt <= 0 || !model.IsValidId(input.AuditEventID) || input.AuditAt <= 0 {
		return nil, store.NewErrInvalidInput("affiliation", "end", nil)
	}
	execute := func(ctx context.Context, tx *sqlxTxWrapper) (*affiliationMutationResult, error) {
		if err := lockAffiliationLifecycle(ctx, tx); err != nil {
			return nil, err
		}
		before, err := s.getAffiliationInTransaction(ctx, tx, input.ID)
		if err != nil {
			return nil, err
		}
		if before.EndsAt.Valid {
			if input.Command == nil {
				return nil, store.NewErrConflict("affiliation", "revision", nil)
			}
			if err = completeAdministrativeNoOpAudit(ctx, tx, input.AuditEventID, input.AuditAt, "affiliation_id", before.ID.String()); err != nil {
				return nil, err
			}
			return &affiliationMutationResult{Value: before, NoOp: true}, nil
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
		return &affiliationMutationResult{Value: current}, nil
	}
	if input.Command == nil {
		result, err := runSQLTransaction(ctx, s.GetMaster().Begin, "affiliation end", execute)
		if err != nil {
			return nil, err
		}
		input.NoOp = result.NoOp
		return result.Value, nil
	}
	result, err := runIdempotentMutation(ctx, s.SQLStore, "idempotent affiliation end", idempotentMutation[*affiliationMutationResult]{
		command: input.Command, auditEventID: input.AuditEventID, execute: execute,
		encode: encodeAffiliationMutationOutcome, decode: decodeAffiliationMutationOutcome,
		hydrateReplay: s.hydrateAffiliationMutationOutcome,
		onboardingOutcome: func(value *affiliationMutationResult) (onboardingImportCommandResult, error) {
			return administrativeOnboardingOutcome(value.Value.ID.String(), value.NoOp)
		},
		completeReplay: func(ctx context.Context, tx *sqlxTxWrapper, value *affiliationMutationResult, original string) error {
			return completeAdministrativeReplayAudit(ctx, tx, input.AuditEventID, input.AuditAt, "affiliation_id", value.Value.ID.String(), value.NoOp, original)
		},
	})
	if err != nil {
		return nil, err
	}
	input.Replayed, input.NoOp = result.Replayed, result.Value.NoOp
	return result.Value.Value, nil
}

func findExactAffiliation(ctx context.Context, tx sqlxExecutor, candidate *model.Affiliation) (*model.Affiliation, error) {
	var endAt any
	if candidate.EndsAt.Valid {
		endAt = candidate.EndsAt.Time
	}
	var row affiliationRow
	err := tx.Get(ctx, &row, `SELECT id,created_at,updated_at,archived_at,revision,user_id,kind,start_at,end_at
		FROM affiliations WHERE user_id=? AND kind=? AND start_at=? AND end_at IS NOT DISTINCT FROM ? AND archived_at IS NULL
		ORDER BY id LIMIT 1`, candidate.UserID.String(), candidate.Kind, candidate.StartsAt, endAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("find exact affiliation: %w", err)
	}
	return row.model()
}

func encodeAffiliationMutationOutcome(value *affiliationMutationResult) ([]byte, error) {
	if value == nil || value.Value == nil || !value.Value.ID.IsValid() {
		return nil, store.NewErrInvalidInput("affiliation", "command_outcome", nil)
	}
	return encodeCommandOutcome(affiliationCommandOutcome{ID: value.Value.ID.String(), NoOp: value.NoOp})
}

func decodeAffiliationMutationOutcome(version int, data []byte) (*affiliationMutationResult, error) {
	if version != 1 {
		return nil, fmt.Errorf("unsupported affiliation outcome version %d", version)
	}
	var outcome affiliationCommandOutcome
	if err := decodeCommandOutcome(data, &outcome); err != nil {
		return nil, err
	}
	id, err := model.ParseAffiliationID(outcome.ID)
	if err != nil {
		return nil, invalidPersistedState("command_outcome", "affiliation_id", err)
	}
	return &affiliationMutationResult{Value: &model.Affiliation{ID: id}, NoOp: outcome.NoOp}, nil
}

func (s SQLAffiliationStore) hydrateAffiliationMutationOutcome(ctx context.Context, tx *sqlxTxWrapper, value *affiliationMutationResult) (*affiliationMutationResult, error) {
	if value == nil || value.Value == nil {
		return nil, store.NewErrInvalidInput("affiliation", "command_outcome", nil)
	}
	hydrated, err := s.getAffiliationInTransaction(ctx, tx, value.Value.ID.String())
	if err != nil {
		return nil, err
	}
	value.Value = hydrated
	return value, nil
}

func (s SQLAffiliationStore) getAffiliationInTransaction(ctx context.Context, tx sqlxExecutor, id string) (*model.Affiliation, error) {
	var row affiliationRow
	if err := tx.GetBuilder(ctx, &row, s.query.Where(sq.Eq{"affiliations.id": id, "affiliations.archived_at": nil})); err != nil {
		return nil, translateError("affiliation", id, err)
	}
	return row.model()
}

func (s SQLAffiliationStore) endAffiliation(ctx context.Context, tx sqlxExecutor, id string, expectedRevision, endAt int64) (*model.Affiliation, error) {
	var row affiliationRow
	if err := tx.GetBuilder(ctx, &row, s.query.Where(sq.Eq{"affiliations.id": id, "affiliations.archived_at": nil})); err != nil {
		return nil, translateError("affiliation", id, err)
	}
	current, err := row.model()
	if err != nil {
		return nil, err
	}
	if current.Revision != expectedRevision {
		return nil, store.NewErrConflict("affiliation", "affiliation_changed", nil)
	}
	startMillis := model.MillisFromTime(current.StartsAt)
	endMillis := current.EndsAt.Millis()
	if endAt <= startMillis || (endMillis != 0 && endAt >= endMillis) {
		return nil, store.NewErrConflict("affiliation", "affiliation_end_time", nil)
	}
	if current.Kind == model.AffiliationStudent {
		var activeEnrollment bool
		if err := tx.Get(ctx, &activeEnrollment, `SELECT EXISTS (SELECT 1 FROM class_members WHERE user_id = ? AND archived_at IS NULL AND end_at IS NULL)`, current.UserID.String()); err != nil {
			return nil, fmt.Errorf("check affiliation enrollment dependencies: %w", err)
		}
		if activeEnrollment {
			return nil, store.NewErrConflict("affiliation", "affiliation_student_has_active_enrollment", nil)
		}
	}
	at := model.TimeFromMillis(endAt)
	result, err := tx.Exec(ctx, `UPDATE affiliations SET updated_at = ?, end_at = ?, revision = revision + 1 WHERE id = ? AND archived_at IS NULL AND revision = ?`, at, at, id, expectedRevision)
	if err != nil {
		return nil, fmt.Errorf("end affiliation: %w", err)
	}
	if err := requireAffected(result, "affiliation", id); err != nil {
		return nil, err
	}
	current.UpdatedAt = at
	current.EndsAt = model.OptionalTimeFromMillis(endAt)
	current.Revision = expectedRevision + 1
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
	startAt := candidate.StartsAt
	endAt := NullTimeFromOptional(candidate.EndsAt)
	var overlaps bool
	if err := executor.Get(ctx, &overlaps, `SELECT EXISTS (
		SELECT 1 FROM affiliations WHERE user_id = ? AND kind = ? AND archived_at IS NULL
		 AND (end_at IS NULL OR end_at > ?) AND (CAST(? AS timestamptz) IS NULL OR start_at < ?)
	)`, candidate.UserID.String(), candidate.Kind, startAt, endAt, endAt); err != nil {
		return fmt.Errorf("check affiliation overlap: %w", err)
	}
	if overlaps {
		return store.NewErrConflict("affiliation", "affiliations_effective_range_overlap", nil)
	}
	return nil
}

func (s SQLAffiliationStore) selectAffiliations(
	ctx context.Context,
	query sq.SelectBuilder,
) ([]*model.Affiliation, error) {
	rows := []affiliationRow{}
	if err := s.GetMaster().SelectBuilder(ctx, &rows, query); err != nil {
		return nil, fmt.Errorf("list affiliations: %w", err)
	}
	result := make([]*model.Affiliation, 0, len(rows))
	for _, row := range rows {
		value, err := row.model()
		if err != nil {
			return nil, err
		}
		result = append(result, value)
	}
	return result, nil
}

func newAffiliationRow(a *model.Affiliation) affiliationRow {
	return affiliationRow{
		ID:         a.ID.String(),
		CreatedAt:  UTCTime(a.CreatedAt),
		UpdatedAt:  UTCTime(a.UpdatedAt),
		ArchivedAt: NullTimeFromOptional(a.ArchivedAt),
		UserID:     a.UserID.String(),
		Kind:       a.Kind,
		Revision:   a.Revision,
		StartAt:    UTCTime(a.StartsAt),
		EndAt:      NullTimeFromOptional(a.EndsAt),
	}
}

func (r affiliationRow) model() (*model.Affiliation, error) {
	id, err := parsePersistedID("affiliation", "id", r.ID, model.ParseAffiliationID)
	if err != nil {
		return nil, err
	}
	userID, err := parsePersistedID("affiliation", "user_id", r.UserID, model.ParseUserID)
	if err != nil {
		return nil, err
	}
	value := &model.Affiliation{
		ID:         id,
		CreatedAt:  r.CreatedAt.UTC(),
		UpdatedAt:  r.UpdatedAt.UTC(),
		ArchivedAt: OptionalTimeFromNullTime(r.ArchivedAt),
		UserID:     userID,
		Kind:       r.Kind,
		Revision:   r.Revision,
		StartsAt:   r.StartAt.UTC(),
		EndsAt:     OptionalTimeFromNullTime(r.EndAt),
	}
	if err := validatePersistedModel("affiliation", value); err != nil {
		return nil, err
	}
	return value, nil
}

var _ store.AffiliationStore = (*SQLAffiliationStore)(nil)
