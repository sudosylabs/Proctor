// ---------------------------------------------------------------------------------------------
// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// Modifications Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------
//
// Adapted from Mattermost server/channels/store/sqlstore/team_store.go member
// operations for Proctor's time-bounded academic-unit membership.

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

type SQLAcademicUnitMemberStore struct {
	*SQLStore
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

type academicUnitMemberMutationResult struct {
	Value *model.AcademicUnitMember
	NoOp  bool
}

type academicUnitMemberCommandOutcome struct {
	ID   string `json:"id"`
	NoOp bool   `json:"no_op,omitempty"`
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

func (s SQLAcademicUnitMemberStore) Create(ctx context.Context, input *store.AcademicUnitMemberCreation) (*model.AcademicUnitMember, error) {
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
	payloadKeyID := ""
	if input.Notice != nil {
		var err error
		payloadKeyID, err = validateRelationshipTransitionMail("academic_unit_member", input.Notice, candidate.UserID,
			candidate.CreatedAt, model.MailTemplateAcademicUnitAssigned)
		if err != nil {
			return nil, err
		}
	}
	encoded, appErr := model.EncodeAuditData(candidate.Auditable())
	if appErr != nil {
		return nil, appErr
	}
	execute := func(ctx context.Context, tx *sqlxTxWrapper) (*academicUnitMemberMutationResult, error) {
		if err := lockAcademicUnitMemberLifecycle(ctx, tx); err != nil {
			return nil, err
		}
		if err := lockAcademicUnitMember(ctx, tx, candidate.AcademicUnitID.String(), candidate.UserID.String()); err != nil {
			return nil, err
		}
		existing, err := findExactAcademicUnitMember(ctx, tx, &candidate)
		if err != nil {
			return nil, err
		}
		if existing != nil && input.Command != nil {
			if err = completeAdministrativeNoOpAudit(ctx, tx, input.AuditEventID, input.AuditAt, "academic_unit_member_id", existing.ID.String()); err != nil {
				return nil, err
			}
			return &academicUnitMemberMutationResult{Value: existing, NoOp: true}, nil
		}
		if input.Notice != nil {
			if err = lockPreparedMailRecipient(ctx, tx, "academic_unit_member", candidate.UserID,
				input.ExpectedRecipientRevision, input.Notice); err != nil {
				return nil, err
			}
			if payloadKeyID != "" {
				if err = requireMailPayloadPrimary(ctx, tx, payloadKeyID); err != nil {
					return nil, err
				}
			}
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
		if input.Notice != nil {
			if err = insertRecoveryMail(ctx, tx, input.Notice.Occurrence, input.Notice.Delivery, input.Notice.Job, payloadKeyID); err != nil {
				return nil, fmt.Errorf("insert Academic Unit assignment mail: %w", err)
			}
		}
		if _, err := completeAuditEvent(ctx, tx, input.AuditEventID, model.AuditStatusSuccess, "", encoded, input.AuditAt); err != nil {
			return nil, fmt.Errorf("complete academic unit member creation audit: %w", err)
		}
		return &academicUnitMemberMutationResult{Value: &candidate}, nil
	}
	return s.runAcademicUnitMemberMutation(ctx, "academic unit member creation", input.Command, input.AuditEventID, input.AuditAt,
		func(replayed, noOp bool) { input.Replayed, input.NoOp = replayed, noOp }, execute)
}

func newSQLAcademicUnitMemberStore(ss *SQLStore) store.AcademicUnitMemberStore {
	s := &SQLAcademicUnitMemberStore{SQLStore: ss}
	s.query = s.getQueryBuilder().
		Select(academicUnitMemberColumns()...).
		From("academic_unit_members")
	return s
}

func (s SQLAcademicUnitMemberStore) Save(
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
	return runSQLTransaction(ctx, s.GetMaster().Begin, "academic unit member save", func(ctx context.Context, tx *sqlxTxWrapper) (*model.AcademicUnitMember, error) {
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
		return &candidate, nil
	})
}

func (s SQLAcademicUnitMemberStore) Get(
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
	return row.model()
}

func (s SQLAcademicUnitMemberStore) ListByUser(
	ctx context.Context,
	userID string,
) ([]*model.AcademicUnitMember, error) {
	return s.selectMembers(ctx, s.query.Where(sq.Eq{
		"academic_unit_members.user_id":     userID,
		"academic_unit_members.archived_at": nil,
	}).OrderBy("academic_unit_members.start_at DESC", "academic_unit_members.id"))
}

func (s SQLAcademicUnitMemberStore) ListByAcademicUnit(
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

func (s SQLAcademicUnitMemberStore) ListActiveByUser(
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

func (s SQLAcademicUnitMemberStore) End(
	ctx context.Context,
	id string,
	expectedRevision int64,
	endAt int64,
) (*model.AcademicUnitMember, error) {
	if !model.IsValidId(id) || expectedRevision <= 0 || endAt <= 0 {
		return nil, store.NewErrInvalidInput("academic_unit_member", "end", nil)
	}
	return runSQLTransaction(ctx, s.GetMaster().Begin, "academic unit member end", func(ctx context.Context, tx *sqlxTxWrapper) (*model.AcademicUnitMember, error) {
		if err := lockAcademicUnitMemberLifecycle(ctx, tx); err != nil {
			return nil, err
		}
		return s.endAcademicUnitMember(ctx, tx, id, expectedRevision, endAt)
	})
}

func (s SQLAcademicUnitMemberStore) EndWithAudit(ctx context.Context, input *store.AcademicUnitMemberEnd) (*model.AcademicUnitMember, error) {
	if input == nil || !model.IsValidId(input.ID) || input.ExpectedRevision <= 0 || input.EndAt <= 0 || !model.IsValidId(input.AuditEventID) || input.AuditAt <= 0 {
		return nil, store.NewErrInvalidInput("academic_unit_member", "end", nil)
	}
	execute := func(ctx context.Context, tx *sqlxTxWrapper) (*academicUnitMemberMutationResult, error) {
		if err := lockAcademicUnitMemberLifecycle(ctx, tx); err != nil {
			return nil, err
		}
		before, err := s.getAcademicUnitMemberInTransaction(ctx, tx, input.ID)
		if err != nil {
			return nil, err
		}
		if before.EndsAt.Valid {
			if input.Command == nil {
				return nil, store.NewErrConflict("academic_unit_member", "revision", nil)
			}
			if err = completeAdministrativeNoOpAudit(ctx, tx, input.AuditEventID, input.AuditAt, "academic_unit_member_id", before.ID.String()); err != nil {
				return nil, err
			}
			return &academicUnitMemberMutationResult{Value: before, NoOp: true}, nil
		}
		payloadKeyID := ""
		if input.Notice != nil {
			if err = lockPreparedMailRecipient(ctx, tx, "academic_unit_member", before.UserID,
				input.ExpectedRecipientRevision, input.Notice); err != nil {
				return nil, err
			}
			payloadKeyID, err = validateRelationshipTransitionMail("academic_unit_member", input.Notice, before.UserID,
				model.TimeFromMillis(input.EndAt), model.MailTemplateAcademicUnitAssignmentEnded)
			if err != nil {
				return nil, err
			}
			if payloadKeyID != "" {
				if err = requireMailPayloadPrimary(ctx, tx, payloadKeyID); err != nil {
					return nil, err
				}
			}
		}
		ended, err := s.endAcademicUnitMember(ctx, tx, input.ID, input.ExpectedRevision, input.EndAt)
		if err != nil {
			return nil, err
		}
		if input.Notice != nil {
			if err = insertRecoveryMail(ctx, tx, input.Notice.Occurrence, input.Notice.Delivery, input.Notice.Job, payloadKeyID); err != nil {
				return nil, fmt.Errorf("insert Academic Unit assignment-ended mail: %w", err)
			}
		}
		encoded, appErr := model.EncodeAuditData(ended.Auditable())
		if appErr != nil {
			return nil, appErr
		}
		if _, err := completeAuditEvent(ctx, tx, input.AuditEventID, model.AuditStatusSuccess, "", encoded, input.AuditAt); err != nil {
			return nil, fmt.Errorf("complete academic unit member end audit: %w", err)
		}
		return &academicUnitMemberMutationResult{Value: ended}, nil
	}
	return s.runAcademicUnitMemberMutation(ctx, "academic unit member audited end", input.Command, input.AuditEventID, input.AuditAt,
		func(replayed, noOp bool) { input.Replayed, input.NoOp = replayed, noOp }, execute)
}

func (s SQLAcademicUnitMemberStore) runAcademicUnitMemberMutation(ctx context.Context, operation string, command *store.CommandIdempotency, auditID string, auditAt int64, setOutput func(bool, bool), execute func(context.Context, *sqlxTxWrapper) (*academicUnitMemberMutationResult, error)) (*model.AcademicUnitMember, error) {
	if command == nil {
		result, err := runSQLTransaction(ctx, s.GetMaster().Begin, operation, execute)
		if err != nil {
			return nil, err
		}
		setOutput(false, result.NoOp)
		return result.Value, nil
	}
	result, err := runIdempotentMutation(ctx, s.SQLStore, "idempotent "+operation, idempotentMutation[*academicUnitMemberMutationResult]{
		command: command, auditEventID: auditID, execute: execute,
		encode: encodeAcademicUnitMemberMutationOutcome, decode: decodeAcademicUnitMemberMutationOutcome,
		hydrateReplay: s.hydrateAcademicUnitMemberMutationOutcome,
		onboardingOutcome: func(value *academicUnitMemberMutationResult) (onboardingImportCommandResult, error) {
			return administrativeOnboardingOutcome(value.Value.ID.String(), value.NoOp)
		},
		completeReplay: func(ctx context.Context, tx *sqlxTxWrapper, value *academicUnitMemberMutationResult, original string) error {
			return completeAdministrativeReplayAudit(ctx, tx, auditID, auditAt, "academic_unit_member_id", value.Value.ID.String(), value.NoOp, original)
		},
	})
	if err != nil {
		return nil, err
	}
	setOutput(result.Replayed, result.Value.NoOp)
	return result.Value.Value, nil
}

func findExactAcademicUnitMember(ctx context.Context, tx sqlxExecutor, candidate *model.AcademicUnitMember) (*model.AcademicUnitMember, error) {
	var row academicUnitMemberRow
	err := tx.Get(ctx, &row, `SELECT id,created_at,updated_at,archived_at,revision,academic_unit_id,user_id,start_at,end_at
		FROM academic_unit_members WHERE academic_unit_id=? AND user_id=? AND start_at=? AND end_at IS NULL AND archived_at IS NULL ORDER BY id LIMIT 1`, candidate.AcademicUnitID.String(), candidate.UserID.String(), candidate.StartsAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("find exact academic unit member: %w", err)
	}
	return row.model()
}

func encodeAcademicUnitMemberMutationOutcome(value *academicUnitMemberMutationResult) ([]byte, error) {
	if value == nil || value.Value == nil || !value.Value.ID.IsValid() {
		return nil, store.NewErrInvalidInput("academic_unit_member", "command_outcome", nil)
	}
	return encodeCommandOutcome(academicUnitMemberCommandOutcome{ID: value.Value.ID.String(), NoOp: value.NoOp})
}
func decodeAcademicUnitMemberMutationOutcome(version int, data []byte) (*academicUnitMemberMutationResult, error) {
	if version != 1 {
		return nil, fmt.Errorf("unsupported academic unit member outcome version %d", version)
	}
	var outcome academicUnitMemberCommandOutcome
	if err := decodeCommandOutcome(data, &outcome); err != nil {
		return nil, err
	}
	id, err := model.ParseAcademicUnitMemberID(outcome.ID)
	if err != nil {
		return nil, invalidPersistedState("command_outcome", "academic_unit_member_id", err)
	}
	return &academicUnitMemberMutationResult{Value: &model.AcademicUnitMember{ID: id}, NoOp: outcome.NoOp}, nil
}
func (s SQLAcademicUnitMemberStore) hydrateAcademicUnitMemberMutationOutcome(ctx context.Context, tx *sqlxTxWrapper, value *academicUnitMemberMutationResult) (*academicUnitMemberMutationResult, error) {
	hydrated, err := s.getAcademicUnitMemberInTransaction(ctx, tx, value.Value.ID.String())
	if err != nil {
		return nil, err
	}
	value.Value = hydrated
	return value, nil
}
func (s SQLAcademicUnitMemberStore) getAcademicUnitMemberInTransaction(ctx context.Context, tx sqlxExecutor, id string) (*model.AcademicUnitMember, error) {
	var row academicUnitMemberRow
	if err := tx.GetBuilder(ctx, &row, s.query.Where(sq.Eq{"academic_unit_members.id": id, "academic_unit_members.archived_at": nil})); err != nil {
		return nil, translateError("academic_unit_member", id, err)
	}
	return row.model()
}

func (s SQLAcademicUnitMemberStore) endAcademicUnitMember(ctx context.Context, tx sqlxExecutor, id string, expectedRevision, endAt int64) (*model.AcademicUnitMember, error) {
	var row academicUnitMemberRow
	if err := tx.GetBuilder(ctx, &row, s.query.Where(sq.Eq{"academic_unit_members.id": id, "academic_unit_members.archived_at": nil})); err != nil {
		return nil, translateError("academic_unit_member", id, err)
	}
	current, err := row.model()
	if err != nil {
		return nil, err
	}
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
	if _, err = tx.Exec(ctx, `UPDATE role_bindings rb SET updated_at=?,end_at=?
		WHERE rb.origin_academic_unit_member_id=? AND rb.origin_invitation_id IS NOT NULL
		AND rb.archived_at IS NULL AND rb.start_at<? AND (rb.end_at IS NULL OR rb.end_at>?)`, at, at, id, at, at); err != nil {
		return nil, fmt.Errorf("end Invitation-origin Role Bindings: %w", err)
	}
	if _, err = tx.Exec(ctx, `UPDATE role_bindings rb SET updated_at=?,archived_at=?
		WHERE rb.origin_academic_unit_member_id=? AND rb.origin_invitation_id IS NOT NULL
		AND rb.archived_at IS NULL AND rb.start_at>=? AND (rb.end_at IS NULL OR rb.end_at>?)`, at, at, id, at, at); err != nil {
		return nil, fmt.Errorf("archive future Invitation-origin Role Bindings: %w", err)
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

func (s SQLAcademicUnitMemberStore) selectMembers(
	ctx context.Context,
	query sq.SelectBuilder,
) ([]*model.AcademicUnitMember, error) {
	rows := []academicUnitMemberRow{}
	if err := s.GetMaster().SelectBuilder(ctx, &rows, query); err != nil {
		return nil, fmt.Errorf("list academic unit members: %w", err)
	}
	result := make([]*model.AcademicUnitMember, 0, len(rows))
	for _, row := range rows {
		member, err := row.model()
		if err != nil {
			return nil, err
		}
		result = append(result, member)
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

func (r academicUnitMemberRow) model() (*model.AcademicUnitMember, error) {
	id, err := parsePersistedID("academic_unit_member", "id", r.ID, model.ParseAcademicUnitMemberID)
	if err != nil {
		return nil, err
	}
	unitID, err := parsePersistedID("academic_unit_member", "academic_unit_id", r.AcademicUnitID, model.ParseAcademicUnitID)
	if err != nil {
		return nil, err
	}
	userID, err := parsePersistedID("academic_unit_member", "user_id", r.UserID, model.ParseUserID)
	if err != nil {
		return nil, err
	}
	value := &model.AcademicUnitMember{
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
	if err := validatePersistedModel("academic_unit_member", value); err != nil {
		return nil, err
	}
	return value, nil
}

var _ store.AcademicUnitMemberStore = (*SQLAcademicUnitMemberStore)(nil)
