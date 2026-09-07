// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package sqlstore

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"encoding/json"
	"errors"
	"slices"
	"time"

	"github.com/lib/pq"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

type SQLExamExportStore struct{ *SQLStore }

func NewSQLExamExportStore(s *SQLStore) store.ExamExportStore { return &SQLExamExportStore{s} }

const examExportColumns = `id,exam_id,exam_sitting_id,submission_id,requester_user_id,categories,state,policy_revision,created_at,expires_at,source_expires_at,submission_count,file_count,source_bytes,job_id,archive_attempt_id,archive_size_bytes,archive_sha256,ready_at`

type examExportRow struct {
	ID               string         `db:"id"`
	ExamID           string         `db:"exam_id"`
	SittingID        string         `db:"exam_sitting_id"`
	SubmissionID     sql.NullString `db:"submission_id"`
	RequesterID      string         `db:"requester_user_id"`
	Categories       pq.StringArray `db:"categories"`
	State            string         `db:"state"`
	PolicyRevision   int64          `db:"policy_revision"`
	CreatedAt        time.Time      `db:"created_at"`
	ExpiresAt        time.Time      `db:"expires_at"`
	SourceExpiresAt  time.Time      `db:"source_expires_at"`
	SubmissionCount  int            `db:"submission_count"`
	FileCount        int            `db:"file_count"`
	SourceBytes      int64          `db:"source_bytes"`
	JobID            string         `db:"job_id"`
	ArchiveAttemptID sql.NullString `db:"archive_attempt_id"`
	ArchiveSizeBytes sql.NullInt64  `db:"archive_size_bytes"`
	ArchiveSHA256    sql.NullString `db:"archive_sha256"`
	ReadyAt          sql.NullTime   `db:"ready_at"`
}

func (r examExportRow) value(at time.Time) (*model.ExamExport, error) {
	e := &model.ExamExport{ID: model.ExamExportID(r.ID), Scope: model.RetentionHoldScope{ExamID: model.ExamID(r.ExamID), SittingID: model.ExamSittingID(r.SittingID), SubmissionID: model.SubmissionID(r.SubmissionID.String)},
		RequesterUserID: model.UserID(r.RequesterID), State: model.ExamExportState(r.State), PolicyRevision: r.PolicyRevision, CreatedAt: model.TimeUTC(r.CreatedAt), ExpiresAt: model.TimeUTC(r.ExpiresAt), SourceExpiresAt: model.TimeUTC(r.SourceExpiresAt),
		SubmissionCount: r.SubmissionCount, FileCount: r.FileCount, SourceBytes: r.SourceBytes, ArchiveSizeBytes: r.ArchiveSizeBytes.Int64, ArchiveSHA256: r.ArchiveSHA256.String, ReadyAt: optionalTime(r.ReadyAt)}
	for _, c := range r.Categories {
		e.Categories = append(e.Categories, model.RetentionCategory(c))
	}
	if e.Validate() != nil || !model.JobID(r.JobID).IsValid() || (e.State == model.ExamExportReady && !model.JobAttemptID(r.ArchiveAttemptID.String).IsValid()) {
		return nil, invalidPersistedState("exam_export", "value", errors.New("invalid export metadata"))
	}
	if !at.Before(e.ExpiresAt) {
		e.State = model.ExamExportExpired
		e.ArchiveSizeBytes = 0
		e.ArchiveSHA256 = ""
		e.ReadyAt = model.OptionalTime{}
	}
	return e, nil
}

func getExamExport(ctx context.Context, executor sqlxExecutor, id model.ExamExportID, lock bool) (examExportRow, error) {
	var row examExportRow
	q := `SELECT ` + examExportColumns + ` FROM exam_exports WHERE id=?`
	if lock {
		q += ` FOR UPDATE`
	}
	err := executor.Get(ctx, &row, q, id.String())
	return row, translateError("exam_export", id.String(), err)
}

func (s *SQLExamExportStore) ListScope(ctx context.Context, scope model.RetentionHoldScope) ([]store.ExamSubmissionAuthorization, error) {
	if scope.Validate() != nil || !scope.SittingID.IsValid() {
		return nil, store.NewErrInvalidInput("exam_export", "scope", nil)
	}
	return listExamExportScope(ctx, s.GetMaster(), scope, "", false)
}

func (s *SQLExamExportStore) ListCreationScope(ctx context.Context, scope model.RetentionHoldScope, command *store.CommandIdempotency) ([]store.ExamSubmissionAuthorization, error) {
	if scope.Validate() != nil || !scope.SittingID.IsValid() || command == nil || !command.UserID.IsValid() || command.Operation != store.ExamExportCreateOperation ||
		command.KeyDigest == ([sha256.Size]byte{}) || command.FingerprintVersion < 1 || command.OutcomeVersion != 1 || command.Wait <= 0 || command.Retention <= 0 || command.Batch != nil {
		return nil, store.NewErrInvalidInput("exam_export", "creation_scope", nil)
	}
	return runSQLTransaction(ctx, s.GetMaster().Begin, "resolve Exam export creation scope", func(ctx context.Context, tx *sqlxTxWrapper) ([]store.ExamSubmissionAuthorization, error) {
		// Share the command's normal advisory fence so an in-flight original
		// creation finishes before deciding whether this is a fresh capture.
		lockInput := append([]byte(command.UserID.String()+"\x00"+command.Operation+"\x00"), command.KeyDigest[:]...)
		lockDigest := sha256.Sum256(lockInput)
		lockID := int64(binary.BigEndian.Uint64(lockDigest[:8]))
		lockCtx, cancel := context.WithTimeout(ctx, command.Wait)
		defer cancel()
		var locked bool
		if err := tx.Get(lockCtx, &locked, `SELECT true FROM pg_advisory_xact_lock(?)`, lockID); err != nil {
			if errors.Is(lockCtx.Err(), context.DeadlineExceeded) {
				return nil, &store.ErrIdempotencyInProgress{}
			}
			return nil, err
		}
		var outcome commandOutcomeRow
		err := tx.Get(ctx, &outcome, `SELECT fingerprint_version,fingerprint,outcome_version,outcome FROM command_outcomes WHERE user_id=? AND operation=? AND key_digest=? FOR SHARE`, command.UserID.String(), command.Operation, command.KeyDigest[:])
		if errors.Is(err, sql.ErrNoRows) {
			return listExamExportScope(ctx, tx, scope, "", false)
		}
		if err != nil {
			return nil, err
		}
		if outcome.FingerprintVersion != command.FingerprintVersion || !bytes.Equal(outcome.Fingerprint, command.Fingerprint[:]) {
			return nil, &store.ErrIdempotencyConflict{}
		}
		var id model.ExamExportID
		if outcome.OutcomeVersion != 1 || json.Unmarshal(outcome.Outcome, &id) != nil || !id.IsValid() {
			return nil, invalidPersistedState("exam_export", "outcome", errors.New("invalid export outcome"))
		}
		row, err := getExamExport(ctx, tx, id, false)
		if err != nil {
			return nil, err
		}
		if row.RequesterID != command.UserID.String() || row.ExamID != scope.ExamID.String() || row.SittingID != scope.SittingID.String() || row.SubmissionID.String != scope.SubmissionID.String() {
			return nil, store.NewErrNotFound("exam_export", "scope")
		}
		return listExamExportScope(ctx, tx, scope, id, false)
	})
}

func listExamExportScope(ctx context.Context, ex sqlxExecutor, scope model.RetentionHoldScope, exportID model.ExamExportID, lock bool) ([]store.ExamSubmissionAuthorization, error) {
	var rows []struct {
		ID          string `db:"id"`
		AttemptID   string `db:"exam_attempt_id"`
		CandidateID string `db:"candidate_user_id"`
		UnitID      string `db:"academic_unit_id"`
	}
	q := `SELECT sub.id,sub.exam_attempt_id,a.candidate_user_id,e.academic_unit_id FROM exam_submissions sub JOIN exam_attempts a ON a.id=sub.exam_attempt_id JOIN exams e ON e.id=a.exam_id
        WHERE sub.sealed=true AND a.exam_id=? AND a.exam_sitting_id=? AND (?='' OR sub.id=?)`
	args := []any{scope.ExamID.String(), scope.SittingID.String(), scope.SubmissionID.String(), scope.SubmissionID.String()}
	if exportID.IsValid() {
		q += ` AND EXISTS(SELECT 1 FROM exam_export_submissions es WHERE es.export_id=? AND es.submission_id=sub.id)`
		args = append(args, exportID.String())
	}
	q += ` ORDER BY sub.id LIMIT ?`
	args = append(args, model.ExamExportMaximumSubmissions+1)
	if lock {
		q += ` FOR UPDATE OF sub`
	}
	if err := ex.Select(ctx, &rows, q, args...); err != nil {
		return nil, err
	}
	if len(rows) < 1 {
		return nil, store.NewErrNotFound("exam_export", "submissions")
	}
	if len(rows) > model.ExamExportMaximumSubmissions {
		return nil, store.NewErrConflict("exam_export", "limit", nil)
	}
	result := make([]store.ExamSubmissionAuthorization, 0, len(rows))
	for _, r := range rows {
		a := store.ExamSubmissionAuthorization{SubmissionID: model.SubmissionID(r.ID), ExamID: scope.ExamID, SittingID: scope.SittingID, AttemptID: model.ExamAttemptID(r.AttemptID), CandidateUserID: model.UserID(r.CandidateID), AcademicUnitID: model.AcademicUnitID(r.UnitID)}
		if !a.SubmissionID.IsValid() || !a.AttemptID.IsValid() || !a.CandidateUserID.IsValid() || !a.AcademicUnitID.IsValid() {
			return nil, invalidPersistedState("exam_export", "scope", errors.New("invalid lineage"))
		}
		result = append(result, a)
	}
	return result, nil
}

func validExamExportAccess(a *store.ExamExportAccess) bool {
	return a != nil && a.Scope.Validate() == nil && a.Scope.SittingID.IsValid() && a.Principal.Validate() == nil && a.Principal.CredentialType == model.CredentialSessionAccess &&
		(a.Action == model.ActionExamRecordsExport || a.Action == model.ActionExamRecordsExportOverride)
}

func lockExamExportAuthority(ctx context.Context, tx *sqlxTxWrapper, a store.ExamExportAccess, selected []store.ExamSubmissionAuthorization) (time.Time, error) {
	scope, at, err := lockExamRecordsAuthority(ctx, tx, a.ExamRecordsMutation, 0)
	if err != nil {
		return time.Time{}, err
	}
	ordinary := a.Action == model.ActionExamRecordsExport
	if ordinary {
		var allowed bool
		err = tx.Get(ctx, &allowed, `SELECT true FROM exam_managers m JOIN academic_unit_members u ON u.user_id=m.user_id WHERE m.exam_id=? AND m.user_id=? AND u.academic_unit_id=? AND u.archived_at IS NULL AND u.start_at<=? AND (u.end_at IS NULL OR u.end_at>?) LIMIT 1 FOR SHARE OF m,u`, a.Scope.ExamID.String(), a.Principal.UserID.String(), scope.AcademicUnitID.String(), at, at)
		if errors.Is(err, sql.ErrNoRows) {
			return time.Time{}, store.NewErrConflict("authorization", "authority", nil)
		}
		if err != nil {
			return time.Time{}, err
		}
	}
	if err = tx.Get(ctx, &at, `SELECT clock_timestamp()`); err != nil {
		return time.Time{}, err
	}
	view := model.ActionSubmissionViewOverride
	sittingView := model.ActionExamSittingViewOverride
	browser := model.ActionExamAttemptBrowserActivityViewOverride
	if ordinary {
		view = model.ActionSubmissionView
		sittingView = model.ActionExamSittingView
		browser = model.ActionExamAttemptBrowserActivityView
	}
	actions := []model.Action{a.Action, view}
	if a.Scope.SubmissionID.IsZero() {
		actions = append(actions, sittingView)
	}
	if slices.Contains(a.Categories, model.RetentionCategoryIntegrity) {
		actions = append(actions, browser)
	}
	for _, action := range actions {
		if err = requirePrincipalActionAtScope(ctx, tx, a.Principal, action, model.RoleScopeAcademicUnit, scope.AcademicUnitID.String(), at, false); err != nil {
			return time.Time{}, err
		}
	}
	for _, sub := range selected {
		if sub.CandidateUserID == a.Principal.UserID {
			return time.Time{}, store.NewErrNotFound("exam_export", a.ExportID.String())
		}
	}
	if ordinary {
		var current bool
		if err = tx.Get(ctx, &current, `SELECT EXISTS(SELECT 1 FROM academic_unit_members WHERE user_id=? AND academic_unit_id=? AND archived_at IS NULL AND start_at<=? AND (end_at IS NULL OR end_at>?))`, a.Principal.UserID.String(), scope.AcademicUnitID.String(), at, at); err != nil {
			return time.Time{}, err
		}
		if !current {
			return time.Time{}, store.NewErrConflict("authorization", "authority", nil)
		}
	}
	return model.TimeUTC(at), nil
}

func (s *SQLExamExportStore) Create(ctx context.Context, input *store.ExamExportCreation, command *store.CommandIdempotency) (*model.ExamExport, error) {
	if input == nil || !validExamExportAccess(&input.ExamExportAccess) || !input.ExportID.IsValid() || !input.JobID.IsValid() || !model.ValidExamExportCategories(input.Categories) ||
		!validExamRecordsMutation(input.ExamRecordsMutation, command, store.ExamExportCreateOperation, model.ActionExamRecordsExport, model.ActionExamRecordsExportOverride) || len(input.SubmissionIDs) < 1 || len(input.SubmissionIDs) > model.ExamExportMaximumSubmissions {
		return nil, store.NewErrInvalidInput("exam_export", "creation", nil)
	}
	result, err := runIdempotentMutation(ctx, s.SQLStore, "create Exam export", idempotentMutation[*model.ExamExport]{command: command, auditEventID: input.AuditEventID,
		execute: func(ctx context.Context, tx *sqlxTxWrapper) (*model.ExamExport, error) {
			return createExamExport(ctx, tx, input)
		},
		encode: func(e *model.ExamExport) ([]byte, error) { return json.Marshal(e.ID) },
		decode: func(version int, b []byte) (*model.ExamExport, error) {
			var id model.ExamExportID
			if version != 1 || json.Unmarshal(b, &id) != nil || !id.IsValid() {
				return nil, errors.New("invalid export outcome")
			}
			return &model.ExamExport{ID: id}, nil
		},
		hydrateReplay: func(ctx context.Context, tx *sqlxTxWrapper, e *model.ExamExport) (*model.ExamExport, error) {
			a := input.ExamExportAccess
			a.ExportID = e.ID
			value, err := getAuthorizedExamExport(ctx, tx, a)
			if err != nil {
				return nil, err
			}
			return value.Export, nil
		},
		completeReplay: func(ctx context.Context, tx *sqlxTxWrapper, e *model.ExamExport, original string) error {
			return completeExamRecordsAudit(ctx, tx, input.ExamRecordsMutation, map[string]any{"exam_export_id": e.ID.String()}, original)
		},
	})
	if err != nil {
		return nil, err
	}
	return result.Value, nil
}

func createExamExport(ctx context.Context, tx *sqlxTxWrapper, input *store.ExamExportCreation) (*model.ExamExport, error) {
	if err := lockSystemAdministratorAuthenticationPaths(ctx, tx); err != nil {
		return nil, err
	}
	policy, err := getRetentionPolicy(ctx, tx, "FOR SHARE OF p,i")
	if err != nil {
		return nil, err
	}
	if policy.ExportRetentionDays < 1 || policy.ExportRetentionDays > model.ExamExportMaximumDays {
		return nil, store.NewErrConflict("exam_export", "policy_unconfigured", nil)
	}
	if _, _, err = lockExamRecordsAuthority(ctx, tx, input.ExamRecordsMutation, 0); err != nil {
		return nil, err
	}
	selected, err := listExamExportScope(ctx, tx, input.Scope, "", true)
	if err != nil {
		return nil, err
	}
	ids := make([]model.SubmissionID, len(selected))
	for i, a := range selected {
		ids[i] = a.SubmissionID
	}
	if !slices.Equal(ids, input.SubmissionIDs) {
		return nil, store.NewErrConflict("exam_export", "scope_changed", nil)
	}
	at, err := lockExamExportAuthority(ctx, tx, input.ExamExportAccess, selected)
	if err != nil {
		return nil, err
	}
	e := &model.ExamExport{ID: input.ExportID, Scope: input.Scope, RequesterUserID: input.Principal.UserID, Categories: slices.Clone(input.Categories), State: model.ExamExportQueued, PolicyRevision: policy.Revision, CreatedAt: at, ExpiresAt: at.Add(time.Duration(policy.ExportRetentionDays) * 24 * time.Hour), SourceExpiresAt: at.Add(model.ExamExportSourceLifetime), SubmissionCount: len(selected)}
	snapshot, err := captureExamExportSnapshot(ctx, tx, e, selected)
	if err != nil {
		return nil, err
	}
	categories := make([]string, len(e.Categories))
	for i, c := range e.Categories {
		categories[i] = string(c)
	}
	var submission any
	if e.Scope.SubmissionID.IsValid() {
		submission = e.Scope.SubmissionID.String()
	}
	_, err = tx.Exec(ctx, `INSERT INTO exam_exports (id,exam_id,exam_sitting_id,submission_id,requester_user_id,categories,state,policy_revision,created_at,expires_at,source_expires_at,submission_count,file_count,source_bytes,job_id,snapshot) VALUES (?,?,?,?,?,?,'queued',?,?,?,?,?,?,?,?,?::jsonb)`, e.ID.String(), e.Scope.ExamID.String(), e.Scope.SittingID.String(), submission, e.RequesterUserID.String(), pq.Array(categories), e.PolicyRevision, e.CreatedAt, e.ExpiresAt, e.SourceExpiresAt, e.SubmissionCount, e.FileCount, e.SourceBytes, input.JobID.String(), string(snapshot))
	if err != nil {
		return nil, err
	}
	for _, a := range selected {
		if _, err = tx.Exec(ctx, `INSERT INTO exam_export_submissions (export_id,submission_id) VALUES (?,?)`, e.ID.String(), a.SubmissionID.String()); err != nil {
			return nil, err
		}
		for _, c := range e.Categories {
			if _, err = tx.Exec(ctx, `INSERT INTO retention_source_protections(export_id,submission_id,category,created_at,expires_at) VALUES (?,?,?,?,?)`, e.ID.String(), a.SubmissionID.String(), string(c), e.CreatedAt, e.SourceExpiresAt); err != nil {
				return nil, err
			}
			retirement, err := getActiveRetirement(ctx, tx, a.SubmissionID, c)
			if err != nil {
				return nil, err
			}
			if retirement != nil && retirement.State == model.RetentionRetirementGrace {
				if err := cancelOneRetirement(ctx, tx, retirement.ID, at, "source_protected"); err != nil {
					return nil, err
				}
			}
		}
	}
	// A construction reference can be reserved and released between scheduler
	// scans. Cancel grace now so resumed eligibility always receives fresh grace.
	// Exact idempotent replay never enters this creation transaction again.
	if err := cancelScopeExpirySchedules(ctx, tx, e.Scope); err != nil {
		return nil, err
	}
	payload, _ := json.Marshal(struct {
		ExportID model.ExamExportID `json:"export_id"`
	}{e.ID})
	job, err := model.NewJob(input.JobID, model.JobTypeExamExportBuild, 1, payload, e.ID.String(), at, at, model.ExamExportMaximumAttempts)
	if err != nil {
		return nil, err
	}
	if _, err = insertQueuedJob(ctx, tx, job, false); err != nil {
		return nil, err
	}
	if err = completeExamRecordsAudit(ctx, tx, input.ExamRecordsMutation, map[string]any{"exam_export_id": e.ID.String(), "submission_count": e.SubmissionCount, "category_count": len(e.Categories)}, ""); err != nil {
		return nil, err
	}
	return e, e.Validate()
}

func (s *SQLExamExportStore) Get(ctx context.Context, a *store.ExamExportAccess) (*store.ExamExportDownload, error) {
	if !validExamExportAccess(a) || !a.ExportID.IsValid() {
		return nil, store.NewErrInvalidInput("exam_export", "access", nil)
	}
	return runSQLTransaction(ctx, s.GetMaster().Begin, "read Exam export", func(ctx context.Context, tx *sqlxTxWrapper) (*store.ExamExportDownload, error) {
		return getAuthorizedExamExport(ctx, tx, *a)
	})
}

func getAuthorizedExamExport(ctx context.Context, tx *sqlxTxWrapper, a store.ExamExportAccess) (*store.ExamExportDownload, error) {
	// Read immutable ownership before any private snapshot is hydrated.
	row, err := getExamExport(ctx, tx, a.ExportID, false)
	if err != nil {
		return nil, err
	}
	if row.RequesterID != a.Principal.UserID.String() || row.ExamID != a.Scope.ExamID.String() || row.SittingID != a.Scope.SittingID.String() || row.SubmissionID.String != a.Scope.SubmissionID.String() {
		return nil, store.NewErrNotFound("exam_export", a.ExportID.String())
	}
	a.Categories = nil
	for _, c := range row.Categories {
		a.Categories = append(a.Categories, model.RetentionCategory(c))
	}
	selected, err := listExamExportScope(ctx, tx, a.Scope, a.ExportID, false)
	if err != nil {
		return nil, err
	}
	at, err := lockExamExportAuthority(ctx, tx, a, selected)
	if err != nil {
		return nil, err
	}
	// Expiry and publication may have changed while authority locks waited.
	row, err = getExamExport(ctx, tx, a.ExportID, false)
	if err != nil {
		return nil, err
	}
	if err = tx.Get(ctx, &at, `SELECT clock_timestamp()`); err != nil {
		return nil, err
	}
	e, err := row.value(at)
	if err != nil {
		return nil, err
	}
	result := &store.ExamExportDownload{Export: e, Submissions: selected}
	if e.State == model.ExamExportReady {
		result.Artifact = store.ExamExportArtifact{ExportID: e.ID, AttemptID: model.JobAttemptID(row.ArchiveAttemptID.String)}
	}
	return result, nil
}
