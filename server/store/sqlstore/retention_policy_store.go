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
	"fmt"
	"time"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

type SQLRetentionPolicyStore struct {
	*SQLStore
}

type retentionPolicyRow struct {
	InstitutionID            string    `db:"institution_id"`
	Revision                 int64     `db:"revision"`
	SubmissionRetentionDays  int       `db:"submission_retention_days"`
	IntegrityRetentionDays   int       `db:"integrity_retention_days"`
	AuditRetentionDays       int       `db:"audit_retention_days"`
	ExportRetentionDays      int       `db:"export_retention_days"`
	DeletionGraceDays        int       `db:"deletion_grace_days"`
	CandidateNotices         bool      `db:"candidate_notices"`
	AutomaticDeletionEnabled bool      `db:"automatic_deletion_enabled"`
	CreatedAt                time.Time `db:"created_at"`
	UpdatedAt                time.Time `db:"updated_at"`
}

type retentionPolicyReplacementOutcome struct {
	Policy  *model.RetentionPolicy `json:"policy"`
	Changed bool                   `json:"changed"`
}

func newSQLRetentionPolicyStore(sqlStore *SQLStore) store.RetentionPolicyStore {
	return &SQLRetentionPolicyStore{SQLStore: sqlStore}
}

func (s SQLRetentionPolicyStore) Get(
	ctx context.Context,
) (*model.RetentionPolicy, error) {
	return getRetentionPolicy(ctx, s.GetMaster(), "")
}

func (s SQLRetentionPolicyStore) Replace(
	ctx context.Context,
	input *store.RetentionPolicyReplacement,
	command *store.CommandIdempotency,
) (*store.RetentionPolicyReplacementResult, error) {
	if err := validateRetentionPolicyReplacement(input, command); err != nil {
		return nil, err
	}
	mutation, err := runIdempotentMutation(
		ctx,
		s.SQLStore,
		"retention policy replacement",
		idempotentMutation[retentionPolicyReplacementOutcome]{
			command:      command,
			auditEventID: input.AuditEventID,
			execute: func(
				ctx context.Context,
				tx *sqlxTxWrapper,
			) (retentionPolicyReplacementOutcome, error) {
				current, databaseNow, err := lockRetentionAuthority(ctx, tx, input.RetentionMutation, model.ActionRetentionPolicyManage)
				if err != nil {
					return retentionPolicyReplacementOutcome{}, err
				}
				if current.Revision != input.ExpectedRevision {
					return retentionPolicyReplacementOutcome{},
						&store.ErrRetentionPolicyRevisionConflict{CurrentRevision: current.Revision}
				}
				candidate := current.Clone()
				// Bootstrap can carry a host clock slightly ahead of PostgreSQL.
				// Keep metadata monotonic without extending authorization validity.
				mutationAt := model.TimeUTC(databaseNow)
				if mutationAt.Before(current.UpdatedAt) {
					mutationAt = current.UpdatedAt
				}
				if err := candidate.Replace(input.ExpectedRevision, input.Settings, mutationAt); err != nil {
					return retentionPolicyReplacementOutcome{}, store.NewErrInvalidInput(
						"retention_policy",
						"replacement",
						nil,
					).Wrap(err)
				}
				changed := candidate.Revision != current.Revision
				if changed {
					result, updateErr := tx.Exec(ctx, `
                        UPDATE retention_policies
                           SET revision=?, submission_retention_days=?, integrity_retention_days=?,
                               audit_retention_days=?, export_retention_days=?, deletion_grace_days=?, candidate_notices=?, updated_at=?
                         WHERE institution_id=? AND revision=?`,
						candidate.Revision, candidate.SubmissionRetentionDays, candidate.IntegrityRetentionDays,
						candidate.AuditRetentionDays, candidate.ExportRetentionDays, candidate.DeletionGraceDays,
						candidate.CandidateNotices, candidate.UpdatedAt, current.InstitutionID.String(), current.Revision,
					)
					if updateErr != nil {
						return retentionPolicyReplacementOutcome{}, fmt.Errorf(
							"replace retention policy: %w",
							translateError(
								"retention_policy",
								candidate.InstitutionID.String(),
								updateErr,
							),
						)
					}
					if err := requireRetentionPolicyRevisionAffected(result); err != nil {
						return retentionPolicyReplacementOutcome{}, err
					}
					// A new configuration revision cannot inherit deletion approval.
					if _, err := tx.Exec(ctx, `UPDATE retention_controls SET state='paused',revision=revision+1,
						changed_by_user_id=?,updated_at=GREATEST(updated_at,?) WHERE institution_id=? AND state='enabled'`,
						input.Principal.UserID.String(), mutationAt, current.InstitutionID.String()); err != nil {
						return retentionPolicyReplacementOutcome{}, err
					}
					if err := cancelInstitutionRetirements(ctx, tx, current.InstitutionID, mutationAt, "policy_changed"); err != nil {
						return retentionPolicyReplacementOutcome{}, err
					}
					candidate.AutomaticDeletionEnabled = false
				}
				outcome := retentionPolicyReplacementOutcome{
					Policy:  candidate,
					Changed: changed,
				}
				if err := completeRetentionPolicyAudit(
					ctx,
					tx,
					input,
					outcome,
					false,
					"",
				); err != nil {
					return retentionPolicyReplacementOutcome{}, err
				}
				return outcome, nil
			},
			encode: func(outcome retentionPolicyReplacementOutcome) ([]byte, error) {
				return json.Marshal(outcome)
			},
			decode: decodeRetentionPolicyReplacementOutcome,
			hydrateReplay: func(ctx context.Context, tx *sqlxTxWrapper, outcome retentionPolicyReplacementOutcome) (retentionPolicyReplacementOutcome, error) {
				_, _, err := lockRetentionAuthority(ctx, tx, input.RetentionMutation, model.ActionRetentionPolicyManage)
				return outcome, err
			},
			completeReplay: func(
				ctx context.Context,
				tx *sqlxTxWrapper,
				outcome retentionPolicyReplacementOutcome,
				originalAuditID string,
			) error {
				return completeRetentionPolicyAudit(
					ctx,
					tx,
					input,
					outcome,
					true,
					originalAuditID,
				)
			},
		},
	)
	if err != nil {
		return nil, err
	}
	return &store.RetentionPolicyReplacementResult{
		Policy:   mutation.Value.Policy,
		Changed:  mutation.Value.Changed,
		Replayed: mutation.Replayed,
	}, nil
}

func requireRetentionPolicyRevisionAffected(result sql.Result) error {
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read retention policy affected rows: %w", err)
	}
	if affected == 1 {
		return nil
	}
	return store.NewErrConflict(
		"retention_policy",
		"retention_policy_changed",
		nil,
	)
}

func getRetentionPolicy(
	ctx context.Context,
	executor sqlxExecutor,
	lock string,
) (*model.RetentionPolicy, error) {
	query := `SELECT p.institution_id, p.revision, p.submission_retention_days, p.integrity_retention_days,
  p.audit_retention_days, p.export_retention_days, p.deletion_grace_days, p.candidate_notices, p.created_at, p.updated_at,
  EXISTS(SELECT 1 FROM retention_controls c WHERE c.institution_id=p.institution_id AND c.state='enabled'
    AND c.approved_policy_revision=p.revision AND p.deletion_grace_days>0) AS automatic_deletion_enabled
  FROM retention_policies p JOIN institutions i ON i.id=p.institution_id
  WHERE i.archived_at IS NULL`
	if lock != "" {
		query += " " + lock
	}
	var row retentionPolicyRow
	if err := executor.Get(ctx, &row, query); err != nil {
		return nil, translateError("retention_policy", "singleton", err)
	}
	return row.model()
}

func (r retentionPolicyRow) model() (*model.RetentionPolicy, error) {
	institutionID, err := model.ParseInstitutionID(r.InstitutionID)
	if err != nil {
		return nil, invalidPersistedState("retention_policy", "institution_id", err)
	}
	policy := &model.RetentionPolicy{
		InstitutionID: institutionID, Revision: r.Revision,
		RetentionPolicySettings: model.RetentionPolicySettings{
			SubmissionRetentionDays: r.SubmissionRetentionDays, IntegrityRetentionDays: r.IntegrityRetentionDays,
			AuditRetentionDays: r.AuditRetentionDays, ExportRetentionDays: r.ExportRetentionDays,
			DeletionGraceDays: r.DeletionGraceDays, CandidateNotices: r.CandidateNotices,
		},
		CreatedAt: model.TimeUTC(r.CreatedAt), UpdatedAt: model.TimeUTC(r.UpdatedAt),
		AutomaticDeletionEnabled: r.AutomaticDeletionEnabled,
	}
	if err := policy.Validate(); err != nil {
		return nil, invalidPersistedState("retention_policy", "value", err)
	}
	return policy, nil
}

func validateRetentionPolicyReplacement(
	input *store.RetentionPolicyReplacement,
	command *store.CommandIdempotency,
) error {
	if input == nil || !validRetentionMutation(input.RetentionMutation, command, "retention_policy.replace.v1") ||
		input.ExpectedRevision < 1 || input.Settings.Validate() != nil || input.RecentAuthenticationTTL <= 0 ||
		command.FingerprintVersion != 1 || command.OutcomeVersion != 1 ||
		command.Retention <= 0 || command.Wait <= 0 {
		return store.NewErrInvalidInput("retention_policy", "replacement", nil)
	}
	return nil
}

func decodeRetentionPolicyReplacementOutcome(
	version int,
	encoded []byte,
) (retentionPolicyReplacementOutcome, error) {
	if version != 1 {
		return retentionPolicyReplacementOutcome{}, invalidPersistedState(
			"command_outcome",
			"outcome_version",
			errors.New("unsupported retention policy outcome version"),
		)
	}
	var outcome retentionPolicyReplacementOutcome
	if err := json.Unmarshal(encoded, &outcome); err != nil ||
		outcome.Policy == nil || outcome.Policy.Validate() != nil {
		return retentionPolicyReplacementOutcome{}, invalidPersistedState(
			"command_outcome",
			"outcome",
			errors.New("invalid retention policy outcome"),
		)
	}
	return outcome, nil
}

func completeRetentionPolicyAudit(
	ctx context.Context,
	tx *sqlxTxWrapper,
	input *store.RetentionPolicyReplacement,
	outcome retentionPolicyReplacementOutcome,
	replayed bool,
	originalAuditID string,
) error {
	data := outcome.Policy.Auditable()
	data["operation"] = "replace"
	data["expected_revision"] = input.ExpectedRevision
	data["resulting_revision"] = outcome.Policy.Revision
	data["changed"] = outcome.Changed
	if replayed {
		data["idempotency_replayed"] = true
		data["original_audit_event_id"] = originalAuditID
	}
	encoded, err := model.EncodeAuditData(data)
	if err != nil {
		return store.NewErrInvalidInput("retention_policy", "audit", nil).Wrap(err)
	}
	if _, err := completeAuditEvent(
		ctx,
		tx,
		input.AuditEventID,
		model.AuditStatusSuccess,
		"",
		encoded,
		input.AuditAt,
	); err != nil {
		return fmt.Errorf("complete retention policy audit: %w", err)
	}
	return nil
}

var _ store.RetentionPolicyStore = (*SQLRetentionPolicyStore)(nil)

func insertInitialRetentionPolicy(ctx context.Context, executor sqlxExecutor, policy *model.RetentionPolicy) error {
	if policy.Validate() != nil || policy.Revision != 1 || policy.RetentionPolicySettings != (model.RetentionPolicySettings{}) {
		return store.NewErrInvalidInput("retention_policy", "initial", nil)
	}
	_, err := executor.Exec(ctx, `INSERT INTO retention_policies (
        institution_id, revision, submission_retention_days, integrity_retention_days,
        audit_retention_days, export_retention_days, deletion_grace_days, created_at, updated_at
    ) VALUES (?, 1, 0, 0, 0, 0, 0, ?, ?)`, policy.InstitutionID.String(), policy.CreatedAt, policy.UpdatedAt)
	if err != nil {
		return fmt.Errorf("save initial retention policy: %w", translateError("retention_policy", policy.InstitutionID.String(), err))
	}
	_, err = executor.Exec(ctx, `INSERT INTO retention_controls(institution_id,revision,state,updated_at) VALUES (?,1,'disabled',?)`, policy.InstitutionID.String(), policy.UpdatedAt)
	return err
}
