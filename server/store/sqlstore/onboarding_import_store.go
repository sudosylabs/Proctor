// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package sqlstore

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"fmt"
	"regexp"
	"time"

	"github.com/lib/pq"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

const onboardingImportRetention = 7 * 24 * time.Hour

type onboardingImportRow struct {
	ID                     string         `db:"id"`
	Mode                   string         `db:"mode"`
	State                  string         `db:"state"`
	ScopeType              string         `db:"scope_type"`
	ScopeID                string         `db:"scope_id"`
	RoleID                 sql.NullString `db:"role_id"`
	ActorUserID            string         `db:"actor_user_id"`
	Principal              []byte         `db:"principal"`
	PreviewDigest          string         `db:"preview_digest"`
	IgnoredHeaders         pq.StringArray `db:"ignored_headers"`
	TotalRows              int            `db:"total_rows"`
	ValidRows              int            `db:"valid_rows"`
	InvalidRows            int            `db:"invalid_rows"`
	SucceededRows          int            `db:"succeeded_rows"`
	NoOpRows               int            `db:"no_op_rows"`
	FailedRows             int            `db:"failed_rows"`
	SkippedRows            int            `db:"skipped_rows"`
	CommitPolicy           sql.NullString `db:"commit_policy"`
	CommitExpectedRevision sql.NullInt64  `db:"commit_expected_revision"`
	CommitAt               sql.NullTime   `db:"commit_at"`
	CommitKey              []byte         `db:"commit_key_digest"`
	ParseJobID             string         `db:"parse_job_id"`
	ExecutionJobID         sql.NullString `db:"execution_job_id"`
	FailureCode            string         `db:"failure_code"`
	CreatedAt              time.Time      `db:"created_at"`
	UpdatedAt              time.Time      `db:"updated_at"`
	ExpiresAt              time.Time      `db:"expires_at"`
	Revision               int64          `db:"revision"`
}

const onboardingImportColumns = `id,mode,state,scope_type,scope_id,role_id,actor_user_id,principal,preview_digest,ignored_headers,total_rows,valid_rows,invalid_rows,succeeded_rows,no_op_rows,failed_rows,skipped_rows,commit_policy,commit_expected_revision,commit_at,commit_key_digest,parse_job_id,execution_job_id,failure_code,created_at,updated_at,expires_at,revision`

type onboardingImportDetailRow struct {
	ImportID       string                          `db:"import_id"`
	RowNumber      int                             `db:"row_number"`
	Reference      string                          `db:"reference"`
	Operation      string                          `db:"operation"`
	ScopeType      model.RoleScopeType             `db:"scope_type"`
	ScopeID        string                          `db:"scope_id"`
	TargetRevision int64                           `db:"target_revision"`
	RoleID         sql.NullString                  `db:"role_id"`
	RoleRevision   int64                           `db:"role_revision"`
	Email          string                          `db:"target_email"`
	Username       string                          `db:"suggested_username"`
	DisplayName    string                          `db:"suggested_display_name"`
	FirstName      string                          `db:"suggested_first_name"`
	LastName       string                          `db:"suggested_last_name"`
	Locale         string                          `db:"suggested_locale"`
	Timezone       string                          `db:"suggested_timezone"`
	StartsAt       sql.NullTime                    `db:"intended_start_at"`
	EndsAt         sql.NullTime                    `db:"intended_end_at"`
	PreviewStatus  model.OnboardingImportRowStatus `db:"preview_status"`
	PreviewCode    string                          `db:"preview_code"`
	Status         model.OnboardingImportRowStatus `db:"status"`
	PublicCode     string                          `db:"public_code"`
	InvitationID   sql.NullString                  `db:"invitation_id"`
	UpdatedAt      time.Time                       `db:"updated_at"`
}

const onboardingImportDetailColumns = `import_id,row_number,reference,operation,scope_type,scope_id,target_revision,role_id,role_revision,target_email,suggested_username,suggested_display_name,suggested_first_name,suggested_last_name,suggested_locale,suggested_timezone,intended_start_at,intended_end_at,preview_status,preview_code,status,public_code,invitation_id,updated_at`

func (s SQLInvitationStore) CreateOnboardingImport(ctx context.Context, input *store.OnboardingImportCreation) (*store.OnboardingImport, error) {
	if input == nil || input.Import == nil || input.ParseJob == nil || !validOnboardingImport(input.Import) ||
		input.Import.State != model.OnboardingImportParsing || input.Import.ParseJobID != input.ParseJob.ID || !model.IsValidId(input.AuditEventID) || input.AuditAt <= 0 {
		return nil, store.NewErrInvalidInput("onboarding_import", "creation", nil)
	}
	principal, err := json.Marshal(input.Import.Principal)
	if err != nil || len(principal) > store.CommandOutcomeMaxBytes {
		return nil, store.NewErrInvalidInput("onboarding_import", "principal", err)
	}
	value := *input.Import
	auditData, err := model.EncodeAuditData(map[string]any{"onboarding_import_id": value.ID.String(), "mode": value.Mode, "scope_type": value.ScopeType,
		"scope_id": value.ScopeID, "parse_job_id": value.ParseJobID.String(), "state": value.State})
	if err != nil {
		return nil, err
	}
	return runSQLTransaction(ctx, s.GetMaster().Begin, "onboarding import creation", func(ctx context.Context, tx *sqlxTxWrapper) (*store.OnboardingImport, error) {
		if err := requireOnboardingImportAuthority(ctx, tx, value.Principal, value.ScopeType, value.ScopeID); err != nil {
			return nil, err
		}
		if _, err := insertQueuedJob(ctx, tx, input.ParseJob, false); err != nil {
			return nil, translateError("job", input.ParseJob.ID.String(), err)
		}
		_, err := tx.Exec(ctx, `INSERT INTO onboarding_imports (`+onboardingImportColumns+`) VALUES (?,?,?,?,?,?,?,?,?,'{}',0,0,0,0,0,0,0,NULL,NULL,NULL,NULL,?,NULL,'',?,?,?,1)`,
			value.ID.String(), string(value.Mode), string(value.State), string(value.ScopeType), value.ScopeID, nullableID(value.RoleID.String()),
			value.ActorUserID.String(), principal, "", value.ParseJobID.String(), value.CreatedAt, value.UpdatedAt, value.ExpiresAt)
		if err != nil {
			return nil, translateError("onboarding_import", value.ID.String(), err)
		}
		if _, err = completeAuditEvent(ctx, tx, input.AuditEventID, model.AuditStatusSuccess, "", auditData, input.AuditAt); err != nil {
			return nil, fmt.Errorf("complete onboarding import creation audit: %w", err)
		}
		return &value, nil
	})
}

func validOnboardingImport(value *store.OnboardingImport) bool {
	return value != nil && value.ID.IsValid() && value.ActorUserID.IsValid() && value.Principal.Validate() == nil &&
		model.ValidateOnboardingImportScope(value.Mode, value.ScopeType, value.ScopeID) == nil && value.ParseJobID.IsValid() &&
		!value.CreatedAt.IsZero() && value.UpdatedAt.Equal(value.CreatedAt) && value.ExpiresAt.Equal(value.CreatedAt.Add(onboardingImportRetention)) && value.Revision == 1 &&
		((value.Mode == model.OnboardingImportTeacherAcademicUnit && value.RoleID.IsValid()) || (value.Mode != model.OnboardingImportTeacherAcademicUnit && value.RoleID.IsZero()))
}

func (s SQLInvitationStore) GetOnboardingImport(ctx context.Context, id model.OnboardingImportID) (*store.OnboardingImport, error) {
	if !id.IsValid() {
		return nil, store.NewErrInvalidInput("onboarding_import", "id", nil)
	}
	var row onboardingImportRow
	if err := s.GetMaster().Get(ctx, &row, `SELECT `+onboardingImportColumns+` FROM onboarding_imports WHERE id=? AND expires_at>CURRENT_TIMESTAMP`, id.String()); err != nil {
		return nil, translateError("onboarding_import", id.String(), err)
	}
	return row.value()
}

func (row onboardingImportRow) value() (*store.OnboardingImport, error) {
	var principal model.Principal
	if err := json.Unmarshal(row.Principal, &principal); err != nil || principal.Validate() != nil {
		return nil, fmt.Errorf("decode onboarding import principal: %w", err)
	}
	value := &store.OnboardingImport{ID: model.OnboardingImportID(row.ID), Mode: model.OnboardingImportMode(row.Mode), State: model.OnboardingImportState(row.State),
		ScopeType: model.RoleScopeType(row.ScopeType), ScopeID: row.ScopeID, ActorUserID: model.UserID(row.ActorUserID), Principal: principal,
		PreviewDigest: row.PreviewDigest, IgnoredHeaders: append([]string(nil), row.IgnoredHeaders...), TotalRows: row.TotalRows, ValidRows: row.ValidRows,
		InvalidRows: row.InvalidRows, SucceededRows: row.SucceededRows, NoOpRows: row.NoOpRows, FailedRows: row.FailedRows, SkippedRows: row.SkippedRows,
		ParseJobID: model.JobID(row.ParseJobID), CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt, ExpiresAt: row.ExpiresAt, Revision: row.Revision,
		FailureCode: row.FailureCode}
	if row.RoleID.Valid {
		value.RoleID = model.RoleID(row.RoleID.String)
	}
	if row.CommitPolicy.Valid {
		value.CommitPolicy = model.OnboardingImportCommitPolicy(row.CommitPolicy.String)
	}
	if row.ExecutionJobID.Valid {
		value.ExecutionJobID = model.JobID(row.ExecutionJobID.String)
	}
	return value, nil
}

func (s SQLInvitationStore) CompleteOnboardingImportPreview(ctx context.Context, input *store.OnboardingImportPreviewCompletion) (*store.OnboardingImport, error) {
	if input == nil || !input.ID.IsValid() || input.ExpectedRevision < 1 || len(input.Digest) != sha256.Size*2 || len(input.Rows) > store.OnboardingImportMaximumRows || input.At.IsZero() {
		return nil, store.NewErrInvalidInput("onboarding_import", "preview", nil)
	}
	return runSQLTransaction(ctx, s.GetMaster().Begin, "complete onboarding import preview", func(ctx context.Context, tx *sqlxTxWrapper) (*store.OnboardingImport, error) {
		var current onboardingImportRow
		if err := tx.Get(ctx, &current, `SELECT `+onboardingImportColumns+` FROM onboarding_imports WHERE id=? FOR UPDATE`, input.ID.String()); err != nil {
			return nil, translateError("onboarding_import", input.ID.String(), err)
		}
		if current.State != string(model.OnboardingImportParsing) || current.Revision != input.ExpectedRevision {
			return nil, store.NewErrConflict("onboarding_import", "preview_changed", nil)
		}
		valid, invalid := 0, 0
		for index := range input.Rows {
			row := input.Rows[index]
			if row.ImportID != input.ID || row.RowNumber != index+1 || row.Reference == "" || !row.PreviewStatus.IsValid() ||
				(row.PreviewStatus == model.OnboardingImportRowValid && row.TargetRevision < 1) || row.TargetRevision < 0 {
				return nil, store.NewErrInvalidInput("onboarding_import_row", "preview", nil)
			}
			status := row.PreviewStatus
			if status == model.OnboardingImportRowValid {
				valid++
			} else {
				invalid++
			}
			if _, err := tx.Exec(ctx, `INSERT INTO onboarding_import_rows (`+onboardingImportDetailColumns+`) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,NULL,?)`,
				input.ID.String(), row.RowNumber, row.Reference, row.Operation, string(row.ScopeType), row.ScopeID, row.TargetRevision, nullableID(row.RoleID.String()), row.RoleRevision,
				row.Email, row.Username, row.DisplayName, row.FirstName, row.LastName, row.Locale, row.Timezone, nullableMillisTime(row.StartsAt), nullableMillisTime(row.EndsAt),
				string(row.PreviewStatus), row.PreviewCode, string(status), "", input.At); err != nil {
				return nil, translateError("onboarding_import_row", fmt.Sprintf("%s/%d", input.ID, row.RowNumber), err)
			}
		}
		var updated onboardingImportRow
		if err := tx.Get(ctx, &updated, `UPDATE onboarding_imports SET state='preview_ready',preview_digest=?,ignored_headers=?,total_rows=?,valid_rows=?,invalid_rows=?,updated_at=?,revision=revision+1 WHERE id=? AND revision=? RETURNING `+onboardingImportColumns,
			input.Digest, pq.Array(append([]string{}, input.IgnoredHeaders...)), len(input.Rows), valid, invalid, input.At, input.ID.String(), input.ExpectedRevision); err != nil {
			return nil, translateError("onboarding_import", input.ID.String(), err)
		}
		return updated.value()
	})
}

func (s SQLInvitationStore) CommitOnboardingImport(ctx context.Context, input *store.OnboardingImportCommit) (*store.OnboardingImport, error) {
	if input == nil || !input.ID.IsValid() || !input.ActorUserID.IsValid() || input.ExpectedRevision < 1 || len(input.PreviewDigest) != sha256.Size*2 ||
		!input.Policy.IsValid() || input.IdempotencyKey == ([sha256.Size]byte{}) || input.ExecutionJob == nil || input.At.IsZero() || !model.IsValidId(input.AuditEventID) || input.AuditAt <= 0 {
		return nil, store.NewErrInvalidInput("onboarding_import", "commit", nil)
	}
	return runSQLTransaction(ctx, s.GetMaster().Begin, "commit onboarding import", func(ctx context.Context, tx *sqlxTxWrapper) (*store.OnboardingImport, error) {
		var current onboardingImportRow
		if err := tx.Get(ctx, &current, `SELECT `+onboardingImportColumns+` FROM onboarding_imports WHERE id=?`, input.ID.String()); err != nil {
			return nil, translateError("onboarding_import", input.ID.String(), err)
		}
		value, err := current.value()
		if err != nil {
			return nil, err
		}
		if err = requireOnboardingImportAuthority(ctx, tx, value.Principal, value.ScopeType, value.ScopeID); err != nil {
			return nil, err
		}
		if err = tx.Get(ctx, &current, `SELECT `+onboardingImportColumns+` FROM onboarding_imports WHERE id=? FOR UPDATE`, input.ID.String()); err != nil {
			return nil, translateError("onboarding_import", input.ID.String(), err)
		}
		if current.ExecutionJobID.Valid {
			if current.ActorUserID == input.ActorUserID.String() && current.PreviewDigest == input.PreviewDigest && current.CommitPolicy.String == string(input.Policy) &&
				bytes.Equal(current.CommitKey, input.IdempotencyKey[:]) && current.CommitExpectedRevision.Valid && current.CommitExpectedRevision.Int64 == input.ExpectedRevision &&
				current.State != string(model.OnboardingImportPreviewReady) {
				committed, snapshotErr := onboardingImportCommittedSnapshot(current)
				if snapshotErr != nil {
					return nil, snapshotErr
				}
				auditData, encodeErr := onboardingImportAuditData(committed)
				if encodeErr != nil {
					return nil, encodeErr
				}
				if _, completeErr := completeAuditEvent(ctx, tx, input.AuditEventID, model.AuditStatusSuccess, "", auditData, input.AuditAt); completeErr != nil {
					return nil, fmt.Errorf("complete onboarding import commit replay audit: %w", completeErr)
				}
				return committed.value()
			}
			return nil, store.NewErrConflict("onboarding_import", "commit_changed", nil)
		}
		if current.State != string(model.OnboardingImportPreviewReady) || current.Revision != input.ExpectedRevision || current.PreviewDigest != input.PreviewDigest {
			return nil, store.NewErrConflict("onboarding_import", "preview_changed", nil)
		}
		if current.ActorUserID != input.ActorUserID.String() {
			return nil, store.NewErrConflict("onboarding_import", "actor_changed", nil)
		}
		if input.Policy == model.OnboardingImportRequireAllValid && current.InvalidRows != 0 {
			return nil, store.NewErrConflict("onboarding_import", "invalid_rows", nil)
		}
		if _, err := insertQueuedJob(ctx, tx, input.ExecutionJob, false); err != nil {
			return nil, translateError("job", input.ExecutionJob.ID.String(), err)
		}
		if _, err := tx.Exec(ctx, `UPDATE onboarding_import_rows SET status=CASE WHEN preview_status='valid' THEN 'pending' ELSE 'skipped' END,updated_at=? WHERE import_id=?`, input.At, input.ID.String()); err != nil {
			return nil, err
		}
		var updated onboardingImportRow
		if err := tx.Get(ctx, &updated, `UPDATE onboarding_imports SET state='executing',commit_policy=?,commit_expected_revision=?,commit_at=?,commit_key_digest=?,execution_job_id=?,skipped_rows=invalid_rows,updated_at=?,revision=revision+1 WHERE id=? RETURNING `+onboardingImportColumns,
			string(input.Policy), input.ExpectedRevision, input.At, input.IdempotencyKey[:], input.ExecutionJob.ID.String(), input.At, input.ID.String()); err != nil {
			return nil, translateError("onboarding_import", input.ID.String(), err)
		}
		auditData, err := onboardingImportAuditData(updated)
		if err != nil {
			return nil, err
		}
		if _, err = completeAuditEvent(ctx, tx, input.AuditEventID, model.AuditStatusSuccess, "", auditData, input.AuditAt); err != nil {
			return nil, fmt.Errorf("complete onboarding import commit audit: %w", err)
		}
		return updated.value()
	})
}

func (s SQLInvitationStore) ListOnboardingImportRows(ctx context.Context, id model.OnboardingImportID, after, limit int) (*store.OnboardingImportPage, error) {
	if !id.IsValid() || after < 0 || limit < 1 || limit > store.OnboardingImportPageSize {
		return nil, store.NewErrInvalidInput("onboarding_import", "rows", nil)
	}
	rows := make([]onboardingImportDetailRow, 0, limit+1)
	if err := s.GetMaster().Select(ctx, &rows, `SELECT `+onboardingImportDetailColumns+` FROM onboarding_import_rows WHERE import_id=? AND row_number>? ORDER BY row_number LIMIT ?`, id.String(), after, limit+1); err != nil {
		return nil, translateError("onboarding_import", id.String(), err)
	}
	page := &store.OnboardingImportPage{Rows: make([]store.OnboardingImportRow, 0, min(limit, len(rows))), More: len(rows) > limit}
	if page.More {
		rows = rows[:limit]
	}
	for _, row := range rows {
		page.Rows = append(page.Rows, row.value())
	}
	return page, nil
}

func (row onboardingImportDetailRow) value() store.OnboardingImportRow {
	value := store.OnboardingImportRow{ImportID: model.OnboardingImportID(row.ImportID), RowNumber: row.RowNumber, Reference: row.Reference, Operation: row.Operation,
		ScopeType: row.ScopeType, ScopeID: row.ScopeID, TargetRevision: row.TargetRevision, RoleRevision: row.RoleRevision, Email: row.Email,
		Username: row.Username, DisplayName: row.DisplayName, FirstName: row.FirstName, LastName: row.LastName, Locale: row.Locale, Timezone: row.Timezone,
		StartsAt: millisFromNullTime(row.StartsAt), EndsAt: millisFromNullTime(row.EndsAt), PreviewStatus: row.PreviewStatus, PreviewCode: row.PreviewCode, Status: row.Status,
		PublicCode: row.PublicCode, UpdatedAt: row.UpdatedAt}
	if row.RoleID.Valid {
		value.RoleID = model.RoleID(row.RoleID.String)
	}
	if row.InvitationID.Valid {
		value.InvitationID = model.InvitationID(row.InvitationID.String)
	}
	return value
}

func (s SQLInvitationStore) CompleteOnboardingImportRow(ctx context.Context, input *store.OnboardingImportRowCompletion) (*store.OnboardingImport, error) {
	if input == nil || !input.ID.IsValid() || input.RowNumber < 1 || input.At.IsZero() ||
		(input.Status != model.OnboardingImportRowSucceeded && input.Status != model.OnboardingImportRowNoOp && input.Status != model.OnboardingImportRowFailed) ||
		((input.Status == model.OnboardingImportRowFailed) != (input.PublicCode != "")) ||
		(input.Status == model.OnboardingImportRowSucceeded && !input.InvitationID.IsValid()) {
		return nil, store.NewErrInvalidInput("onboarding_import_row", "completion", nil)
	}
	return runSQLTransaction(ctx, s.GetMaster().Begin, "complete onboarding import row", func(ctx context.Context, tx *sqlxTxWrapper) (*store.OnboardingImport, error) {
		var aggregateState model.OnboardingImportState
		if err := tx.Get(ctx, &aggregateState, `SELECT state FROM onboarding_imports WHERE id=? FOR UPDATE`, input.ID.String()); err != nil {
			return nil, translateError("onboarding_import", input.ID.String(), err)
		}
		var currentStatus model.OnboardingImportRowStatus
		if err := tx.Get(ctx, &currentStatus, `SELECT status FROM onboarding_import_rows WHERE import_id=? AND row_number=? FOR UPDATE`, input.ID.String(), input.RowNumber); err != nil {
			return nil, translateError("onboarding_import_row", fmt.Sprintf("%s/%d", input.ID, input.RowNumber), err)
		}
		if currentStatus == model.OnboardingImportRowPending {
			if _, err := tx.Exec(ctx, `UPDATE onboarding_import_rows SET status=?,public_code=?,invitation_id=?,updated_at=? WHERE import_id=? AND row_number=?`,
				string(input.Status), input.PublicCode, nullableID(input.InvitationID.String()), input.At, input.ID.String(), input.RowNumber); err != nil {
				return nil, err
			}
		} else if currentStatus != input.Status {
			return nil, store.NewErrConflict("onboarding_import_row", "row_changed", nil)
		}
		return refreshOnboardingImportCounts(ctx, tx, input.ID, input.At)
	})
}

func (s SQLInvitationStore) FinishOnboardingImport(ctx context.Context, id model.OnboardingImportID, at time.Time) (*store.OnboardingImport, error) {
	if !id.IsValid() || at.IsZero() {
		return nil, store.NewErrInvalidInput("onboarding_import", "finish", nil)
	}
	return runSQLTransaction(ctx, s.GetMaster().Begin, "finish onboarding import", func(ctx context.Context, tx *sqlxTxWrapper) (*store.OnboardingImport, error) {
		var current onboardingImportRow
		if err := tx.Get(ctx, &current, `SELECT `+onboardingImportColumns+` FROM onboarding_imports WHERE id=? FOR UPDATE`, id.String()); err != nil {
			return nil, translateError("onboarding_import", id.String(), err)
		}
		if current.State == string(model.OnboardingImportCompleted) || current.State == string(model.OnboardingImportCompletedWithErrors) ||
			current.State == string(model.OnboardingImportCanceled) || current.State == string(model.OnboardingImportFailed) {
			return current.value()
		}
		if current.State != string(model.OnboardingImportExecuting) {
			return nil, store.NewErrConflict("onboarding_import", "state", nil)
		}
		var pending int
		if err := tx.Get(ctx, &pending, `SELECT count(*) FROM onboarding_import_rows WHERE import_id=? AND status='pending'`, id.String()); err != nil {
			return nil, err
		}
		if pending != 0 {
			return nil, store.NewErrConflict("onboarding_import", "rows_pending", nil)
		}
		return refreshOnboardingImportCounts(ctx, tx, id, at)
	})
}

func refreshOnboardingImportCounts(ctx context.Context, tx *sqlxTxWrapper, id model.OnboardingImportID, at time.Time) (*store.OnboardingImport, error) {
	var counts struct {
		Pending   int `db:"pending"`
		Succeeded int `db:"succeeded"`
		NoOp      int `db:"no_op"`
		Failed    int `db:"failed"`
		Skipped   int `db:"skipped"`
	}
	if err := tx.Get(ctx, &counts, `SELECT count(*) FILTER (WHERE status='pending') AS pending,count(*) FILTER (WHERE status='succeeded') AS succeeded,count(*) FILTER (WHERE status='no_op') AS no_op,count(*) FILTER (WHERE status='failed') AS failed,count(*) FILTER (WHERE status IN ('skipped','canceled')) AS skipped FROM onboarding_import_rows WHERE import_id=?`, id.String()); err != nil {
		return nil, err
	}
	stateSQL := `state`
	if counts.Pending == 0 {
		stateSQL = `CASE WHEN ` + fmt.Sprint(counts.Failed) + `>0 THEN 'completed_with_errors' ELSE 'completed' END`
	}
	var updated onboardingImportRow
	query := `UPDATE onboarding_imports SET succeeded_rows=?,no_op_rows=?,failed_rows=?,skipped_rows=?,state=` + stateSQL + `,updated_at=?,revision=revision+1 WHERE id=? RETURNING ` + onboardingImportColumns
	if err := tx.Get(ctx, &updated, query, counts.Succeeded, counts.NoOp, counts.Failed, counts.Skipped, at, id.String()); err != nil {
		return nil, translateError("onboarding_import", id.String(), err)
	}
	return updated.value()
}

func lockOnboardingImportCommand(ctx context.Context, tx *sqlxTxWrapper, command *store.CommandIdempotency) error {
	if command == nil || (command.OnboardingImportID.IsZero() && command.OnboardingImportRowNumber == 0) {
		return nil
	}
	if !command.OnboardingImportID.IsValid() || command.OnboardingImportRowNumber < 1 {
		return store.NewErrInvalidInput("onboarding_import_row", "command", nil)
	}
	var projected onboardingImportRow
	if err := tx.Get(ctx, &projected, `SELECT `+onboardingImportColumns+` FROM onboarding_imports WHERE id=?`, command.OnboardingImportID.String()); err != nil {
		return translateError("onboarding_import", command.OnboardingImportID.String(), err)
	}
	value, err := projected.value()
	if err != nil {
		return err
	}
	if err = requireOnboardingImportAuthority(ctx, tx, value.Principal, value.ScopeType, value.ScopeID); err != nil {
		return err
	}
	var aggregate onboardingImportRow
	if err = tx.Get(ctx, &aggregate, `SELECT `+onboardingImportColumns+` FROM onboarding_imports WHERE id=? FOR UPDATE`, command.OnboardingImportID.String()); err != nil {
		return translateError("onboarding_import", command.OnboardingImportID.String(), err)
	}
	value, err = aggregate.value()
	if err != nil {
		return err
	}
	var row onboardingImportDetailRow
	if err = tx.Get(ctx, &row, `SELECT `+onboardingImportDetailColumns+` FROM onboarding_import_rows WHERE import_id=? AND row_number=? FOR UPDATE`, command.OnboardingImportID.String(), command.OnboardingImportRowNumber); err != nil {
		return translateError("onboarding_import_row", fmt.Sprintf("%s/%d", command.OnboardingImportID, command.OnboardingImportRowNumber), err)
	}
	switch row.Status {
	case model.OnboardingImportRowPending:
		if value.State != model.OnboardingImportExecuting {
			return store.NewErrConflict("onboarding_import", "canceled", nil)
		}
		return validateOnboardingImportFrozenTarget(ctx, tx, row)
	case model.OnboardingImportRowSucceeded, model.OnboardingImportRowNoOp:
		return nil
	default:
		return store.NewErrConflict("onboarding_import_row", "row_changed", nil)
	}
}

func validateOnboardingImportFrozenTarget(ctx context.Context, tx *sqlxTxWrapper, row onboardingImportDetailRow) error {
	var revision int64
	var err error
	switch row.Operation {
	case "student_class.create":
		err = tx.Get(ctx, &revision, `SELECT revision FROM classes WHERE id=? AND archived_at IS NULL FOR SHARE`, row.ScopeID)
	case "teacher_academic_unit.create", "academic_unit_role.create":
		err = tx.Get(ctx, &revision, `SELECT revision FROM academic_units WHERE id=? AND archived_at IS NULL FOR SHARE`, row.ScopeID)
	case "institution_role.create":
		err = tx.Get(ctx, &revision, `SELECT revision FROM institutions WHERE id=? AND archived_at IS NULL FOR SHARE`, row.ScopeID)
	default:
		return store.NewErrConflict("onboarding_import_row", "operation", nil)
	}
	if isNoRows(err) || err == nil && revision != row.TargetRevision {
		return store.NewErrConflict("onboarding_import_row", "target_changed", nil)
	}
	if err != nil {
		return err
	}
	if row.RoleID.Valid {
		var updatedAt time.Time
		if err = tx.Get(ctx, &updatedAt, `SELECT updated_at FROM roles WHERE id=? AND archived_at IS NULL FOR SHARE`, row.RoleID.String); isNoRows(err) || err == nil && updatedAt.UnixMicro() != row.RoleRevision {
			return store.NewErrConflict("onboarding_import_row", "role_changed", nil)
		}
		if err != nil {
			return err
		}
	}
	return nil
}

func completeOnboardingImportCommand[T any](ctx context.Context, tx *sqlxTxWrapper, command *store.CommandIdempotency, value T,
	resolve func(T) (model.OnboardingImportRowStatus, model.InvitationID, error),
) error {
	if command == nil || command.OnboardingImportID.IsZero() {
		return nil
	}
	if resolve == nil {
		return store.NewErrInvalidInput("onboarding_import_row", "outcome", nil)
	}
	status, invitationID, err := resolve(value)
	if err != nil {
		return err
	}
	if (status != model.OnboardingImportRowSucceeded && status != model.OnboardingImportRowNoOp) ||
		(status == model.OnboardingImportRowSucceeded && !invitationID.IsValid()) {
		return store.NewErrInvalidInput("onboarding_import_row", "outcome", nil)
	}
	var current struct {
		Status       model.OnboardingImportRowStatus `db:"status"`
		InvitationID sql.NullString                  `db:"invitation_id"`
	}
	if err = tx.Get(ctx, &current, `SELECT status,invitation_id FROM onboarding_import_rows WHERE import_id=? AND row_number=? FOR UPDATE`,
		command.OnboardingImportID.String(), command.OnboardingImportRowNumber); err != nil {
		return translateError("onboarding_import_row", fmt.Sprintf("%s/%d", command.OnboardingImportID, command.OnboardingImportRowNumber), err)
	}
	if current.Status != model.OnboardingImportRowPending {
		if current.Status == status && current.InvitationID.String == invitationID.String() {
			return nil
		}
		return store.NewErrConflict("onboarding_import_row", "row_changed", nil)
	}
	at, err := jobDatabaseNow(ctx, tx)
	if err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `UPDATE onboarding_import_rows SET status=?,public_code='',invitation_id=?,updated_at=? WHERE import_id=? AND row_number=?`,
		string(status), nullableID(invitationID.String()), at, command.OnboardingImportID.String(), command.OnboardingImportRowNumber); err != nil {
		return err
	}
	_, err = refreshOnboardingImportCounts(ctx, tx, command.OnboardingImportID, at)
	return err
}

func (s SQLInvitationStore) CancelOnboardingImport(ctx context.Context, input *store.OnboardingImportCancellation) (*store.OnboardingImport, error) {
	if input == nil || !input.ID.IsValid() || !input.ActorUserID.IsValid() || input.At.IsZero() || !model.IsValidId(input.AuditEventID) || input.AuditAt <= 0 {
		return nil, store.NewErrInvalidInput("onboarding_import", "cancel", nil)
	}
	id, actor, at := input.ID, input.ActorUserID, input.At
	return runSQLTransaction(ctx, s.GetMaster().Begin, "cancel onboarding import", func(ctx context.Context, tx *sqlxTxWrapper) (*store.OnboardingImport, error) {
		var current onboardingImportRow
		if err := tx.Get(ctx, &current, `SELECT `+onboardingImportColumns+` FROM onboarding_imports WHERE id=?`, id.String()); err != nil {
			return nil, translateError("onboarding_import", id.String(), err)
		}
		value, err := current.value()
		if err != nil {
			return nil, err
		}
		if err = requireOnboardingImportAuthority(ctx, tx, value.Principal, value.ScopeType, value.ScopeID); err != nil {
			return nil, err
		}
		if err = tx.Get(ctx, &current, `SELECT `+onboardingImportColumns+` FROM onboarding_imports WHERE id=? FOR UPDATE`, id.String()); err != nil {
			return nil, translateError("onboarding_import", id.String(), err)
		}
		if current.ActorUserID != actor.String() {
			return nil, store.NewErrConflict("onboarding_import", "actor_changed", nil)
		}
		if current.State == string(model.OnboardingImportCanceled) {
			auditData, encodeErr := onboardingImportAuditData(current)
			if encodeErr != nil {
				return nil, encodeErr
			}
			if _, completeErr := completeAuditEvent(ctx, tx, input.AuditEventID, model.AuditStatusSuccess, "", auditData, input.AuditAt); completeErr != nil {
				return nil, fmt.Errorf("complete onboarding import cancel replay audit: %w", completeErr)
			}
			return current.value()
		}
		if current.State == string(model.OnboardingImportCompleted) || current.State == string(model.OnboardingImportCompletedWithErrors) || current.State == string(model.OnboardingImportFailed) {
			return nil, store.NewErrConflict("onboarding_import", "terminal", nil)
		}
		jobID := current.ParseJobID
		if current.ExecutionJobID.Valid {
			jobID = current.ExecutionJobID.String
		}
		if _, err := tx.Exec(ctx, `UPDATE jobs SET status=CASE WHEN status='queued' THEN 'canceled' WHEN status='running' THEN 'cancel_requested' ELSE status END,completed_at=CASE WHEN status='queued' THEN ? ELSE completed_at END,public_error_code=CASE WHEN status='queued' THEN 'job.canceled' ELSE public_error_code END,updated_at=?,revision=revision+1 WHERE id=? AND status IN ('queued','running')`, at, at, jobID); err != nil {
			return nil, err
		}
		if _, err := tx.Exec(ctx, `UPDATE onboarding_import_rows SET status='canceled',updated_at=? WHERE import_id=? AND status='pending'`, at, id.String()); err != nil {
			return nil, err
		}
		var updated onboardingImportRow
		if err := tx.Get(ctx, &updated, `UPDATE onboarding_imports SET state='canceled',skipped_rows=(SELECT count(*) FROM onboarding_import_rows WHERE import_id=? AND status IN ('skipped','canceled')),updated_at=?,revision=revision+1 WHERE id=? RETURNING `+onboardingImportColumns, id.String(), at, id.String()); err != nil {
			return nil, err
		}
		auditData, err := onboardingImportAuditData(updated)
		if err != nil {
			return nil, err
		}
		if _, err = completeAuditEvent(ctx, tx, input.AuditEventID, model.AuditStatusSuccess, "", auditData, input.AuditAt); err != nil {
			return nil, fmt.Errorf("complete onboarding import cancel audit: %w", err)
		}
		return updated.value()
	})
}

func (s SQLInvitationStore) FailOnboardingImport(ctx context.Context, id model.OnboardingImportID, code string, at time.Time) (*store.OnboardingImport, error) {
	if !id.IsValid() || !onboardingImportPublicCode.MatchString(code) || at.IsZero() {
		return nil, store.NewErrInvalidInput("onboarding_import", "failure", nil)
	}
	var row onboardingImportRow
	if err := s.GetMaster().Get(ctx, &row, `UPDATE onboarding_imports SET state='failed',failure_code=?,updated_at=?,revision=revision+1 WHERE id=? AND state IN ('uploading','parsing','executing') RETURNING `+onboardingImportColumns, code, at, id.String()); err != nil {
		return nil, translateError("onboarding_import", id.String(), err)
	}
	return row.value()
}

func (s SQLInvitationStore) ListExpiredOnboardingImports(ctx context.Context, limit int, at time.Time) ([]model.OnboardingImportID, error) {
	if limit < 1 || limit > 500 || at.IsZero() {
		return nil, store.NewErrInvalidInput("onboarding_import", "retention", nil)
	}
	rows := make([]string, 0, limit)
	if err := s.GetMaster().Select(ctx, &rows, `SELECT id FROM onboarding_imports WHERE expires_at<=? ORDER BY expires_at,id LIMIT ?`, at, limit); err != nil {
		return nil, fmt.Errorf("list expired onboarding imports: %w", err)
	}
	result := make([]model.OnboardingImportID, 0, len(rows))
	for _, id := range rows {
		result = append(result, model.OnboardingImportID(id))
	}
	return result, nil
}

func (s SQLInvitationStore) PurgeOnboardingImport(ctx context.Context, id model.OnboardingImportID, at time.Time) (bool, error) {
	if !id.IsValid() || at.IsZero() {
		return false, store.NewErrInvalidInput("onboarding_import", "retention", nil)
	}
	result, err := s.GetMaster().Exec(ctx, `DELETE FROM onboarding_imports WHERE id=? AND expires_at<=?`, id.String(), at)
	if err != nil {
		return false, fmt.Errorf("purge expired onboarding import: %w", err)
	}
	count, err := result.RowsAffected()
	return count == 1, err
}

func nullableMillisTime(value int64) any {
	if value == 0 {
		return nil
	}
	return model.TimeFromMillis(value)
}

func millisFromNullTime(value sql.NullTime) int64 {
	if !value.Valid {
		return 0
	}
	return model.MillisFromTime(value.Time)
}

func onboardingImportAuditData(value onboardingImportRow) ([]byte, error) {
	return model.EncodeAuditData(map[string]any{"onboarding_import_id": value.ID, "mode": value.Mode, "scope_type": value.ScopeType, "scope_id": value.ScopeID,
		"state": value.State, "total_rows": value.TotalRows, "valid_rows": value.ValidRows, "invalid_rows": value.InvalidRows,
		"succeeded_rows": value.SucceededRows, "no_op_rows": value.NoOpRows, "failed_rows": value.FailedRows, "skipped_rows": value.SkippedRows})
}

func onboardingImportCommittedSnapshot(current onboardingImportRow) (onboardingImportRow, error) {
	if !current.CommitExpectedRevision.Valid || !current.CommitAt.Valid || !current.ExecutionJobID.Valid {
		return onboardingImportRow{}, store.NewErrInvalidInput("onboarding_import", "commit_snapshot", nil)
	}
	committed := current
	committed.State = string(model.OnboardingImportExecuting)
	committed.SucceededRows, committed.NoOpRows, committed.FailedRows = 0, 0, 0
	committed.SkippedRows = committed.InvalidRows
	committed.FailureCode = ""
	committed.UpdatedAt = committed.CommitAt.Time
	committed.Revision = committed.CommitExpectedRevision.Int64 + 1
	return committed, nil
}

func requireOnboardingImportAuthority(ctx context.Context, tx *sqlxTxWrapper, principal model.Principal, scopeType model.RoleScopeType, scopeID string) error {
	if principal.Validate() != nil || !scopeType.IsValid() || !model.IsValidId(scopeID) {
		return store.NewErrInvalidInput("onboarding_import", "authority", nil)
	}
	at, err := jobDatabaseNow(ctx, tx)
	if err != nil {
		return err
	}
	var active bool
	if err = tx.Get(ctx, &active, `SELECT true FROM users WHERE id=? AND archived_at IS NULL AND disabled_at IS NULL FOR SHARE`, principal.UserID.String()); err != nil {
		if isNoRows(err) {
			return store.NewErrConflict("onboarding_import", "authority", nil)
		}
		return err
	}
	switch principal.CredentialType {
	case model.CredentialSessionAccess:
		err = tx.Get(ctx, &active, `SELECT true FROM sessions s JOIN session_credentials c ON c.session_id=s.id
			WHERE s.id=? AND s.user_id=? AND s.archived_at IS NULL AND s.revoked_at IS NULL AND s.expires_at>?
			AND c.id=? AND c.kind='access' AND c.archived_at IS NULL AND c.revoked_at IS NULL AND c.expires_at>? FOR SHARE OF s,c`,
			principal.SessionID.String(), principal.UserID.String(), at, principal.CredentialID.String(), at)
	case model.CredentialPersonalAccessToken:
		err = tx.Get(ctx, &active, `SELECT true FROM personal_access_tokens WHERE id=? AND user_id=? AND archived_at IS NULL AND disabled_at IS NULL
			AND revoked_at IS NULL AND expires_at>? AND scopes=? AND academic_unit_id IS NOT DISTINCT FROM ? FOR SHARE`, principal.CredentialID.String(), principal.UserID.String(), at,
			pq.Array(principal.CredentialScopes), nullableID(principal.AcademicUnitID.String()))
	default:
		return store.NewErrConflict("onboarding_import", "credential", nil)
	}
	if isNoRows(err) {
		return store.NewErrConflict("onboarding_import", "credential", nil)
	}
	if err != nil {
		return err
	}
	if scopeType == model.RoleScopeAcademicUnit || scopeType == model.RoleScopeClass {
		if err = lockAcademicUnitHierarchy(ctx, tx); err != nil {
			return err
		}
	}
	action := string(model.ActionOnboardingBatchManage)
	switch scopeType {
	case model.RoleScopeInstitution:
		err = tx.Get(ctx, &active, `SELECT true FROM role_bindings rb JOIN roles r ON r.id=rb.role_id AND r.archived_at IS NULL
			WHERE rb.user_id=? AND rb.archived_at IS NULL AND rb.start_at<=? AND (rb.end_at IS NULL OR rb.end_at>?)
			AND rb.scope_type='institution' AND rb.scope_id=? AND ?=ANY(r.permissions) LIMIT 1 FOR SHARE OF rb,r`, principal.UserID.String(), at, at, scopeID, action)
	case model.RoleScopeAcademicUnit:
		err = tx.Get(ctx, &active, `WITH RECURSIVE ancestors AS (SELECT id,parent_id FROM academic_units WHERE id=? AND archived_at IS NULL
			UNION ALL SELECT au.id,au.parent_id FROM academic_units au JOIN ancestors child ON au.id=child.parent_id WHERE au.archived_at IS NULL)
			SELECT true FROM role_bindings rb JOIN roles r ON r.id=rb.role_id AND r.archived_at IS NULL WHERE rb.user_id=? AND rb.archived_at IS NULL
			AND rb.start_at<=? AND (rb.end_at IS NULL OR rb.end_at>?) AND ?=ANY(r.permissions)
			AND (rb.scope_type='institution' OR (rb.scope_type='academic_unit' AND rb.scope_id IN (SELECT id FROM ancestors))) LIMIT 1 FOR SHARE OF rb,r`, scopeID, principal.UserID.String(), at, at, action)
	case model.RoleScopeClass:
		err = tx.Get(ctx, &active, `WITH RECURSIVE class_unit AS (SELECT p.academic_unit_id FROM classes c JOIN programme_levels pl ON pl.id=c.programme_level_id
			JOIN programmes p ON p.id=pl.programme_id WHERE c.id=?), ancestors AS (SELECT academic_unit_id id FROM class_unit
			UNION ALL SELECT au.parent_id FROM academic_units au JOIN ancestors child ON au.id=child.id WHERE au.parent_id IS NOT NULL AND au.archived_at IS NULL)
			SELECT true FROM role_bindings rb JOIN roles r ON r.id=rb.role_id AND r.archived_at IS NULL WHERE rb.user_id=? AND rb.archived_at IS NULL
			AND rb.start_at<=? AND (rb.end_at IS NULL OR rb.end_at>?) AND ?=ANY(r.permissions) AND (rb.scope_type='institution'
			OR (rb.scope_type='class' AND rb.scope_id=?) OR (rb.scope_type='academic_unit' AND rb.scope_id IN (SELECT id FROM ancestors))) LIMIT 1 FOR SHARE OF rb,r`,
			scopeID, principal.UserID.String(), at, at, action, scopeID)
	}
	if isNoRows(err) {
		return store.NewErrConflict("onboarding_import", "authority", nil)
	}
	return err
}

var onboardingImportPublicCode = regexp.MustCompile(`^[a-z][a-z0-9_.-]{0,127}$`)
