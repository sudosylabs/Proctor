// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package sqlstore

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"math"
	"strings"
	"time"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

type SQLRetentionStore struct{ *SQLStore }

func newSQLRetentionStore(s *SQLStore) store.RetentionStore { return &SQLRetentionStore{s} }

type retentionControlRow struct {
	InstitutionID  string         `db:"institution_id"`
	Revision       int64          `db:"revision"`
	State          string         `db:"state"`
	PolicyRevision sql.NullInt64  `db:"approved_policy_revision"`
	PreviewID      sql.NullString `db:"approved_preview_id"`
	ActorID        sql.NullString `db:"changed_by_user_id"`
	UpdatedAt      time.Time      `db:"updated_at"`
}

func getRetentionControl(ctx context.Context, executor sqlxExecutor, institution model.InstitutionID, lock bool) (*model.RetentionControl, error) {
	query := `SELECT institution_id,revision,state,approved_policy_revision,approved_preview_id,changed_by_user_id,updated_at
		FROM retention_controls WHERE institution_id=?`
	if lock {
		query += " FOR UPDATE"
	}
	var row retentionControlRow
	if err := executor.Get(ctx, &row, query, institution.String()); err != nil {
		return nil, translateError("retention_control", institution.String(), err)
	}
	value := &model.RetentionControl{InstitutionID: model.InstitutionID(row.InstitutionID), Revision: row.Revision,
		State: model.RetentionControlState(row.State), ApprovedPolicyRevision: row.PolicyRevision.Int64,
		ApprovedPreviewID: model.RetentionPreviewID(row.PreviewID.String), ChangedByUserID: model.UserID(row.ActorID.String), UpdatedAt: model.TimeUTC(row.UpdatedAt)}
	if err := value.Validate(); err != nil {
		return nil, invalidPersistedState("retention_control", "value", err)
	}
	return value, nil
}

func (s *SQLRetentionStore) GetControl(ctx context.Context) (*model.RetentionControl, error) {
	policy, err := getRetentionPolicy(ctx, s.GetMaster(), "")
	if err != nil {
		return nil, err
	}
	return getRetentionControl(ctx, s.GetMaster(), policy.InstitutionID, false)
}

type retentionPreviewCounts struct {
	Work      model.RetentionPreviewCounts `json:"work"`
	Integrity model.RetentionPreviewCounts `json:"integrity"`
	Audit     model.RetentionExpiryCounts  `json:"audit"`
	Receipts  model.RetentionExpiryCounts  `json:"receipts"`
}

func (s *SQLRetentionStore) GetPreview(ctx context.Context, id model.RetentionPreviewID) (*model.RetentionPreview, error) {
	if !id.IsValid() {
		return nil, store.NewErrInvalidInput("retention_preview", "id", nil)
	}
	return getRetentionPreview(ctx, s.GetMaster(), id)
}

func getRetentionPreview(ctx context.Context, executor sqlxExecutor, id model.RetentionPreviewID) (*model.RetentionPreview, error) {
	var row struct {
		ID             string    `db:"id"`
		InstitutionID  string    `db:"institution_id"`
		PolicyRevision int64     `db:"policy_revision"`
		ActorID        string    `db:"created_by_user_id"`
		CreatedAt      time.Time `db:"created_at"`
		ExpiresAt      time.Time `db:"expires_at"`
		Counts         []byte    `db:"counts"`
	}
	err := executor.Get(ctx, &row, `SELECT p.id,p.institution_id,p.policy_revision,p.created_by_user_id,p.created_at,p.expires_at,p.counts
		FROM retention_previews p JOIN institutions i ON i.id=p.institution_id WHERE p.id=? AND i.archived_at IS NULL`, id.String())
	if err != nil {
		return nil, translateError("retention_preview", id.String(), err)
	}
	var counts retentionPreviewCounts
	if err = json.Unmarshal(row.Counts, &counts); err != nil {
		return nil, invalidPersistedState("retention_preview", "counts", err)
	}
	p := &model.RetentionPreview{ID: model.RetentionPreviewID(row.ID), InstitutionID: model.InstitutionID(row.InstitutionID),
		PolicyRevision: row.PolicyRevision, CreatedByUserID: model.UserID(row.ActorID), CreatedAt: model.TimeUTC(row.CreatedAt),
		ExpiresAt: model.TimeUTC(row.ExpiresAt), Work: counts.Work, Integrity: counts.Integrity, Audit: counts.Audit, Receipts: counts.Receipts}
	if err = p.Validate(); err != nil {
		return nil, invalidPersistedState("retention_preview", "value", err)
	}
	return p, nil
}

func validRetentionMutation(input store.RetentionMutation, command *store.CommandIdempotency, operation string) bool {
	return input.Principal.Validate() == nil && input.Principal.CredentialType == model.CredentialSessionAccess &&
		model.IsValidId(input.AuditEventID) && input.AuditAt > 0 && command != nil &&
		command.UserID == input.Principal.UserID && command.Operation == operation &&
		command.Authorization == nil && command.Batch == nil
}

// Current assurance comes from the locked Session, not the request's earlier
// Principal snapshot. Time is read after all potentially waiting row locks.
func lockRetentionAuthority(ctx context.Context, tx *sqlxTxWrapper, input store.RetentionMutation, action model.Action) (*model.RetentionPolicy, time.Time, error) {
	if err := lockSystemAdministratorAuthenticationPaths(ctx, tx); err != nil {
		return nil, time.Time{}, err
	}
	if _, err := requireCurrentPrincipalCredential(ctx, tx, input.Principal); err != nil {
		return nil, time.Time{}, err
	}
	policy, err := getRetentionPolicy(ctx, tx, "FOR UPDATE OF p,i")
	if err != nil {
		return nil, time.Time{}, err
	}
	if _, err = getRetentionControl(ctx, tx, policy.InstitutionID, true); err != nil {
		return nil, time.Time{}, err
	}
	var at time.Time
	if err = tx.Get(ctx, &at, `SELECT clock_timestamp()`); err != nil {
		return nil, time.Time{}, err
	}
	if err = requirePrincipalActionAtScope(ctx, tx, input.Principal, action, model.RoleScopeInstitution, policy.InstitutionID.String(), at, false); err != nil {
		return nil, time.Time{}, err
	}
	if err = tx.Get(ctx, &at, `SELECT clock_timestamp()`); err != nil {
		return nil, time.Time{}, err
	}
	at = model.TimeUTC(at)
	if err = requirePrincipalActionAtScope(ctx, tx, input.Principal, action, model.RoleScopeInstitution, policy.InstitutionID.String(), at, false); err != nil {
		return nil, time.Time{}, err
	}
	var active bool
	err = tx.Get(ctx, &active, `SELECT true FROM sessions s JOIN session_credentials c ON c.session_id=s.id
		WHERE s.id=? AND s.user_id=? AND c.id=? AND c.kind='access' AND s.archived_at IS NULL AND s.revoked_at IS NULL
		AND c.archived_at IS NULL AND c.revoked_at IS NULL AND s.expires_at>? AND s.idle_expires_at>? AND c.expires_at>?
		AND NOT s.mfa_recovery_required AND s.authentication_generation=COALESCE((SELECT generation FROM user_mfa_recovery WHERE user_id=s.user_id),0)`,
		input.Principal.SessionID.String(), input.Principal.UserID.String(), input.Principal.CredentialID.String(), at, at, at)
	if isNoRows(err) {
		return nil, time.Time{}, store.NewErrConflict("authorization", "credential", nil)
	}
	if err != nil {
		return nil, time.Time{}, err
	}
	if action == model.ActionRetentionCleanupManage || action == model.ActionRetentionPolicyManage {
		admin, adminErr := isActiveSystemAdministrator(ctx, tx, input.Principal.UserID.String(), at)
		if adminErr != nil {
			return nil, time.Time{}, adminErr
		}
		if !admin {
			return nil, time.Time{}, store.NewErrConflict("authorization", "authority", nil)
		}
		if err = requireStrongRecentSessionAt(ctx, tx, input.Principal.SessionID, at, input.RecentAuthenticationTTL); err != nil {
			return nil, time.Time{}, err
		}
	}
	return policy, at, nil
}

func completeRetentionAudit(ctx context.Context, tx *sqlxTxWrapper, input store.RetentionMutation, data map[string]any, original string) error {
	if original != "" {
		data["idempotency_replayed"], data["original_audit_event_id"] = true, original
	}
	encoded, err := model.EncodeAuditData(data)
	if err != nil {
		return err
	}
	_, err = completeAuditEvent(ctx, tx, input.AuditEventID, model.AuditStatusSuccess, "", encoded, input.AuditAt)
	return err
}

func (s *SQLRetentionStore) CreatePreview(ctx context.Context, input *store.RetentionPreviewCreation, command *store.CommandIdempotency) (*model.RetentionPreview, error) {
	if input == nil || !validRetentionMutation(input.RetentionMutation, command, store.RetentionPreviewOperation) ||
		!input.PreviewID.IsValid() || input.ExpectedPolicyRevision < 1 {
		return nil, store.NewErrInvalidInput("retention_preview", "creation", nil)
	}
	result, err := runIdempotentMutation(ctx, s.SQLStore, "retention preview", idempotentMutation[*model.RetentionPreview]{
		command: command, auditEventID: input.AuditEventID,
		execute: func(ctx context.Context, tx *sqlxTxWrapper) (*model.RetentionPreview, error) {
			policy, at, err := lockRetentionAuthority(ctx, tx, input.RetentionMutation, model.ActionRetentionPolicyView)
			if err != nil {
				return nil, err
			}
			if policy.Revision != input.ExpectedPolicyRevision {
				return nil, &store.ErrRetentionPolicyRevisionConflict{CurrentRevision: policy.Revision}
			}
			counts, err := countRetentionRecords(ctx, tx, policy, at)
			if err != nil {
				return nil, err
			}
			// The scan can outlive a short credential. Recheck at the commit
			// boundary instead of extending authority for the scan's duration.
			if _, _, err = lockRetentionAuthority(ctx, tx, input.RetentionMutation, model.ActionRetentionPolicyView); err != nil {
				return nil, err
			}
			p := &model.RetentionPreview{ID: input.PreviewID, InstitutionID: policy.InstitutionID, PolicyRevision: policy.Revision,
				CreatedByUserID: input.Principal.UserID, CreatedAt: at, ExpiresAt: at.Add(model.RetentionPreviewLifetime), Work: counts.Work, Integrity: counts.Integrity, Audit: counts.Audit, Receipts: counts.Receipts}
			encoded, err := json.Marshal(counts)
			if err != nil {
				return nil, err
			}
			_, err = tx.Exec(ctx, `INSERT INTO retention_previews(id,institution_id,policy_revision,created_by_user_id,created_at,expires_at,counts) VALUES (?,?,?,?,?,?,?)`,
				p.ID.String(), p.InstitutionID.String(), p.PolicyRevision, p.CreatedByUserID.String(), p.CreatedAt, p.ExpiresAt, string(encoded))
			if err != nil {
				return nil, err
			}
			if err = completeRetentionAudit(ctx, tx, input.RetentionMutation, map[string]any{"preview_id": p.ID.String(), "policy_revision": p.PolicyRevision}, ""); err != nil {
				return nil, err
			}
			return p, nil
		},
		encode: func(p *model.RetentionPreview) ([]byte, error) { return json.Marshal(p) },
		decode: func(version int, data []byte) (*model.RetentionPreview, error) {
			var p model.RetentionPreview
			if version != 1 {
				return nil, errors.New("unsupported retention preview outcome")
			}
			if err := json.Unmarshal(data, &p); err != nil {
				return nil, err
			}
			return &p, p.Validate()
		},
		hydrateReplay: func(ctx context.Context, tx *sqlxTxWrapper, p *model.RetentionPreview) (*model.RetentionPreview, error) {
			policy, _, err := lockRetentionAuthority(ctx, tx, input.RetentionMutation, model.ActionRetentionPolicyView)
			if err != nil {
				return nil, err
			}
			if policy.InstitutionID != p.InstitutionID {
				return nil, store.NewErrNotFound("retention_preview", p.ID.String())
			}
			return p, nil
		},
		completeReplay: func(ctx context.Context, tx *sqlxTxWrapper, p *model.RetentionPreview, original string) error {
			return completeRetentionAudit(ctx, tx, input.RetentionMutation, map[string]any{"preview_id": p.ID.String(), "policy_revision": p.PolicyRevision}, original)
		},
	})
	if err != nil {
		return nil, err
	}
	return result.Value, nil
}

func (s *SQLRetentionStore) ChangeControl(ctx context.Context, input *store.RetentionControlChange, command *store.CommandIdempotency) (*store.RetentionControlResult, error) {
	if input == nil || !validRetentionMutation(input.RetentionMutation, command, store.RetentionControlOperation) || input.ExpectedRevision < 1 ||
		input.ExpectedPolicyRevision < 1 || input.RecentAuthenticationTTL <= 0 ||
		(input.State != model.RetentionControlEnabled && input.State != model.RetentionControlPaused) ||
		(input.State == model.RetentionControlEnabled && !input.PreviewID.IsValid()) || (input.State == model.RetentionControlPaused && !input.PreviewID.IsZero()) {
		return nil, store.NewErrInvalidInput("retention_control", "change", nil)
	}
	result, err := runIdempotentMutation(ctx, s.SQLStore, "retention control", idempotentMutation[*model.RetentionControl]{
		command: command, auditEventID: input.AuditEventID,
		execute: func(ctx context.Context, tx *sqlxTxWrapper) (*model.RetentionControl, error) {
			policy, at, err := lockRetentionAuthority(ctx, tx, input.RetentionMutation, model.ActionRetentionCleanupManage)
			if err != nil {
				return nil, err
			}
			current, err := getRetentionControl(ctx, tx, policy.InstitutionID, true)
			if err != nil {
				return nil, err
			}
			if policy.Revision != input.ExpectedPolicyRevision {
				return nil, &store.ErrRetentionPolicyRevisionConflict{CurrentRevision: policy.Revision}
			}
			if current.Revision != input.ExpectedRevision {
				return nil, store.NewErrConflict("retention_control", "revision", nil)
			}
			if current.Revision == math.MaxInt64 {
				return nil, store.NewErrConflict("retention_control", "revision_exhausted", nil)
			}
			if input.State == model.RetentionControlEnabled {
				preview, err := getRetentionPreview(ctx, tx, input.PreviewID)
				if err != nil {
					return nil, err
				}
				if preview.InstitutionID != policy.InstitutionID || preview.PolicyRevision != policy.Revision ||
					preview.CreatedAt.After(at) || !preview.ExpiresAt.After(at) {
					return nil, store.NewErrConflict("retention_control", "preview_stale", nil)
				}
				if policy.DeletionGraceDays <= 0 || (policy.SubmissionRetentionDays == 0 && policy.IntegrityRetentionDays == 0 && policy.AuditRetentionDays == 0) {
					return nil, store.NewErrConflict("retention_control", "policy_unconfigured", nil)
				}
				current.ApprovedPolicyRevision, current.ApprovedPreviewID = policy.Revision, input.PreviewID
			} else if current.State == model.RetentionControlDisabled {
				return nil, store.NewErrConflict("retention_control", "not_enabled", nil)
			}
			current.Revision++
			current.State, current.ChangedByUserID = input.State, input.Principal.UserID
			if at.After(current.UpdatedAt) {
				current.UpdatedAt = at
			}
			if current.UpdatedAt.Before(policy.UpdatedAt) {
				current.UpdatedAt = policy.UpdatedAt
			}
			_, err = tx.Exec(ctx, `UPDATE retention_controls SET revision=?,state=?,approved_policy_revision=?,approved_preview_id=?,changed_by_user_id=?,updated_at=? WHERE institution_id=?`,
				current.Revision, current.State, current.ApprovedPolicyRevision, current.ApprovedPreviewID.String(), current.ChangedByUserID.String(), current.UpdatedAt, current.InstitutionID.String())
			if err != nil {
				return nil, err
			}
			if err = cancelInstitutionRetirements(ctx, tx, current.InstitutionID, at, "control_changed"); err != nil {
				return nil, err
			}
			if err = completeRetentionAudit(ctx, tx, input.RetentionMutation, retentionControlAudit(current), ""); err != nil {
				return nil, err
			}
			return current, nil
		},
		encode: func(c *model.RetentionControl) ([]byte, error) { return json.Marshal(c) },
		decode: func(version int, data []byte) (*model.RetentionControl, error) {
			var c model.RetentionControl
			if version != 1 {
				return nil, errors.New("unsupported retention control outcome")
			}
			if err := json.Unmarshal(data, &c); err != nil {
				return nil, err
			}
			return &c, c.Validate()
		},
		hydrateReplay: func(ctx context.Context, tx *sqlxTxWrapper, c *model.RetentionControl) (*model.RetentionControl, error) {
			policy, _, err := lockRetentionAuthority(ctx, tx, input.RetentionMutation, model.ActionRetentionCleanupManage)
			if err != nil {
				return nil, err
			}
			if policy.InstitutionID != c.InstitutionID {
				return nil, store.NewErrNotFound("retention_control", c.InstitutionID.String())
			}
			return c, nil
		},
		completeReplay: func(ctx context.Context, tx *sqlxTxWrapper, c *model.RetentionControl, original string) error {
			return completeRetentionAudit(ctx, tx, input.RetentionMutation, retentionControlAudit(c), original)
		},
	})
	if err != nil {
		return nil, err
	}
	return &store.RetentionControlResult{Control: result.Value, Replayed: result.Replayed}, nil
}

func retentionControlAudit(c *model.RetentionControl) map[string]any {
	return map[string]any{"institution_id": c.InstitutionID.String(), "control_revision": c.Revision,
		"state": string(c.State), "policy_revision": c.ApprovedPolicyRevision, "preview_id": c.ApprovedPreviewID.String()}
}

func cancelInstitutionRetirements(ctx context.Context, tx *sqlxTxWrapper, id model.InstitutionID, at time.Time, reason string) error {
	_, err := tx.Exec(ctx, `WITH cancelled AS (UPDATE retention_retirements SET state='cancelled',cancelled_at=GREATEST(scheduled_at,?),cancellation_reason=?
		WHERE institution_id=? AND state='grace' RETURNING id)
		UPDATE retention_notices SET cancelled_at=GREATEST(created_at,?),delivery_state=CASE WHEN mail_delivery_id IS NULL AND delivery_state='pending' THEN 'suppressed' ELSE delivery_state END WHERE retirement_id IN (SELECT id FROM cancelled) AND cancelled_at IS NULL`, at, reason, id.String(), at)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `DELETE FROM retention_expiry_schedules`)
	return err
}

type retentionFactsRow struct {
	SubmissionID        string       `db:"submission_id"`
	ExamID              string       `db:"exam_id"`
	SittingID           string       `db:"sitting_id"`
	CompletionRevision  int64        `db:"completion_revision"`
	CompletedAt         sql.NullTime `db:"completed_at"`
	CompletionCurrent   bool         `db:"completion_current"`
	HasIntegrity        bool         `db:"has_integrity"`
	Held                bool         `db:"held"`
	WorkProtection      sql.NullTime `db:"work_protection"`
	IntegrityProtection sql.NullTime `db:"integrity_protection"`
	WorkRetiredAt       sql.NullTime `db:"work_retired_at"`
	IntegrityRetiredAt  sql.NullTime `db:"integrity_retired_at"`
	SharedObjects       int64        `db:"shared_objects"`
}

// One SELECT supplies a consistent snapshot, including current holds and source
// protections. No student-authored value crosses this eligibility projection.
const retentionFactsQuery = `SELECT sub.id AS submission_id,a.exam_id,a.exam_sitting_id AS sitting_id,
	COALESCE(c.revision,0) AS completion_revision,c.completed_at,
	(c.completed_at IS NOT NULL AND c.stale_at IS NULL AND c.completed_evidence_revision=c.evidence_revision AND sit.state='closed') AS completion_current,
	(sub.integrity_retired_at IS NULL AND (sub.browser_activity_source_session_id IS NOT NULL OR
	 EXISTS(SELECT 1 FROM integrity_flags WHERE exam_attempt_id=a.id) OR EXISTS(SELECT 1 FROM integrity_discrepancies WHERE submission_id=sub.id) OR
	 EXISTS(SELECT 1 FROM submission_reviews WHERE submission_id=sub.id) OR EXISTS(SELECT 1 FROM submission_review_waivers WHERE submission_id=sub.id) OR
	 EXISTS(SELECT 1 FROM exam_attempt_suspensions WHERE exam_attempt_id=a.id) OR EXISTS(SELECT 1 FROM exam_attempt_manager_end_actions WHERE exam_attempt_id=a.id))) AS has_integrity,
	EXISTS(SELECT 1 FROM retention_holds h WHERE h.exam_id=a.exam_id AND h.released_at IS NULL AND
	 (h.exam_sitting_id IS NULL OR h.exam_sitting_id=a.exam_sitting_id) AND (h.submission_id IS NULL OR h.submission_id=sub.id)) AS held,
	(SELECT max(expires_at) FROM retention_source_protections WHERE submission_id=sub.id AND category='work') AS work_protection,
	(SELECT max(expires_at) FROM retention_source_protections WHERE submission_id=sub.id AND category='integrity') AS integrity_protection,
	sub.work_retired_at,sub.integrity_retired_at,
	(SELECT count(*) FROM exam_submission_manifest_entries WHERE submission_id=sub.id AND storage_origin='starter') AS shared_objects
	FROM exam_submissions sub JOIN exam_attempts a ON a.id=sub.exam_attempt_id
	JOIN exam_sittings sit ON sit.id=a.exam_sitting_id JOIN exams e ON e.id=a.exam_id JOIN academic_units u ON u.id=e.academic_unit_id
	LEFT JOIN exam_sitting_records_completions c ON c.exam_sitting_id=sit.id WHERE sub.sealed=true AND u.institution_id=?`

func (r retentionFactsRow) records() [2]model.RetentionRecord {
	base := model.RetentionRecord{Scope: model.RetentionHoldScope{ExamID: model.ExamID(r.ExamID), SittingID: model.ExamSittingID(r.SittingID), SubmissionID: model.SubmissionID(r.SubmissionID)},
		CompletionRevision: r.CompletionRevision, CompletedAt: optionalTime(r.CompletedAt), CompletionCurrent: r.CompletionCurrent,
		HasIntegrity: r.HasIntegrity, Held: r.Held}
	work, integrity := base, base
	work.Category, work.ExportProtectedUntil, work.RetiredAt, work.SharedPublishedObjects = model.RetentionCategoryWork, optionalTime(r.WorkProtection), optionalTime(r.WorkRetiredAt), r.SharedObjects
	integrity.Category, integrity.ExportProtectedUntil, integrity.RetiredAt = model.RetentionCategoryIntegrity, optionalTime(r.IntegrityProtection), optionalTime(r.IntegrityRetiredAt)
	return [2]model.RetentionRecord{work, integrity}
}

func countRetentionRecords(ctx context.Context, tx *sqlxTxWrapper, policy *model.RetentionPolicy, at time.Time) (retentionPreviewCounts, error) {
	var counts retentionPreviewCounts
	queryCtx, cancel := tx.queryContext(ctx)
	defer cancel()
	rows, err := tx.tx.QueryxContext(queryCtx, tx.rebind(retentionFactsQuery), policy.InstitutionID.String())
	if err != nil {
		return counts, err
	}
	defer rows.Close()
	for rows.Next() {
		var row retentionFactsRow
		if err := rows.StructScan(&row); err != nil {
			return counts, err
		}
		for _, record := range row.records() {
			eligibility, err := record.Eligibility(policy, at)
			if err != nil {
				return counts, invalidPersistedState("retention_preview", "record", err)
			}
			counter := &counts.Work
			if record.Category == model.RetentionCategoryIntegrity {
				counter = &counts.Integrity
			}
			counter.Total++
			switch eligibility.Blocker {
			case model.RetentionBlockerNone:
				counter.Eligible++
			case model.RetentionBlockerRetired:
				counter.Retired++
			case model.RetentionBlockerIncomplete:
				counter.Incomplete++
			case model.RetentionBlockerHold:
				counter.Held++
			case model.RetentionBlockerUnconfigured:
				counter.Unconfigured++
			case model.RetentionBlockerSupportingWork:
				counter.SupportingWork++
			case model.RetentionBlockerExport:
				counter.ExportProtected++
			case model.RetentionBlockerDeadline:
				counter.AwaitingDeadline++
			default:
				return counts, errors.New("unsupported retention blocker")
			}
		}
	}
	if err := rows.Err(); err != nil {
		return counts, err
	}
	if err := rows.Close(); err != nil {
		return counts, err
	}
	counts.Audit, counts.Receipts, err = countRetentionExpiry(ctx, tx, policy, at)
	return counts, err
}

func (s *SQLRetentionStore) ListRecords(ctx context.Context, options store.RetentionRecordListOptions) (*store.RetentionRecordPage, error) {
	if options.Limit < 1 || options.Limit > model.RetentionMaximumPageSize || !options.AfterSubmissionID.IsZero() && !options.AfterSubmissionID.IsValid() {
		return nil, store.NewErrInvalidInput("retention_records", "page", nil)
	}
	return runSQLTransaction(ctx, s.GetMaster().Begin, "retention eligibility page", func(ctx context.Context, tx *sqlxTxWrapper) (*store.RetentionRecordPage, error) {
		// All rows and their policy use one read-only transaction snapshot.
		if _, err := tx.Exec(ctx, `SET TRANSACTION ISOLATION LEVEL REPEATABLE READ, READ ONLY`); err != nil {
			return nil, err
		}
		policy, err := getRetentionPolicy(ctx, tx, "")
		if err != nil {
			return nil, err
		}
		var at time.Time
		if err = tx.Get(ctx, &at, `SELECT clock_timestamp()`); err != nil {
			return nil, err
		}
		var rows []retentionFactsRow
		err = tx.Select(ctx, &rows, retentionFactsQuery+` AND sub.id>? ORDER BY sub.id LIMIT ?`, policy.InstitutionID.String(), options.AfterSubmissionID.String(), options.Limit+1)
		if err != nil {
			return nil, err
		}
		page := &store.RetentionRecordPage{PolicyRevision: policy.Revision, AsOf: model.TimeUTC(at), Items: make([]store.RetentionRecordItem, 0, len(rows)*2), HasMore: len(rows) > options.Limit}
		if page.HasMore {
			rows = rows[:options.Limit]
		}
		retirements := make(map[string]*model.RetentionRetirement, len(rows)*2)
		if len(rows) > 0 {
			placeholders, args := make([]string, len(rows)), make([]any, len(rows))
			for i, row := range rows {
				placeholders[i], args[i] = "?", row.SubmissionID
			}
			var values []retentionRetirementRow
			if err := tx.Select(ctx, &values, retentionRetirementQuery+` WHERE r.state<>'cancelled' AND r.submission_id IN (`+strings.Join(placeholders, ",")+`)`, args...); err != nil {
				return nil, err
			}
			for _, row := range values {
				value, err := row.retirement()
				if err != nil {
					return nil, err
				}
				retirements[row.SubmissionID+":"+row.Category] = value
			}
		}
		for _, row := range rows {
			for _, record := range row.records() {
				eligibility, err := record.Eligibility(policy, at)
				if err != nil {
					return nil, err
				}
				retirement := retirements[record.Scope.SubmissionID.String()+":"+string(record.Category)]
				page.Items = append(page.Items, store.RetentionRecordItem{Record: record, Eligibility: eligibility, Retirement: retirement})
			}
		}
		return page, nil
	})
}
