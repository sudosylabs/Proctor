// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

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

type SQLDesktopCompatibilityPolicyStore struct {
	*SQLStore
}

type desktopCompatibilityPolicyRow struct {
	InstitutionID          string     `db:"institution_id"`
	Revision               int64      `db:"revision"`
	MinimumDesktopRelease  string     `db:"minimum_desktop_release"`
	RevokedDesktopBuildIDs jsonValue  `db:"revoked_desktop_build_ids"`
	AdministratorMessage   string     `db:"administrator_message"`
	Availability           string     `db:"availability"`
	RetryAt                *time.Time `db:"retry_at"`
	CreatedAt              time.Time  `db:"created_at"`
	UpdatedAt              time.Time  `db:"updated_at"`
}

type desktopCompatibilityPolicyReplacementOutcome struct {
	Policy  *model.DesktopCompatibilityPolicy `json:"policy"`
	Changed bool                              `json:"changed"`
}

func newSQLDesktopCompatibilityPolicyStore(sqlStore *SQLStore) store.DesktopCompatibilityPolicyStore {
	return &SQLDesktopCompatibilityPolicyStore{SQLStore: sqlStore}
}

func (s SQLDesktopCompatibilityPolicyStore) Get(
	ctx context.Context,
) (*model.DesktopCompatibilityPolicy, error) {
	return getDesktopCompatibilityPolicy(ctx, s.GetMaster(), "")
}

func (s SQLDesktopCompatibilityPolicyStore) Replace(
	ctx context.Context,
	input *store.DesktopCompatibilityPolicyReplacement,
	command *store.CommandIdempotency,
) (*store.DesktopCompatibilityPolicyReplacementResult, error) {
	if err := validateDesktopCompatibilityPolicyReplacement(input, command); err != nil {
		return nil, err
	}
	mutation, err := runIdempotentMutation(
		ctx,
		s.SQLStore,
		"desktop compatibility policy replacement",
		idempotentMutation[desktopCompatibilityPolicyReplacementOutcome]{
			command:      command,
			auditEventID: input.AuditEventID,
			execute: func(
				ctx context.Context,
				tx *sqlxTxWrapper,
			) (desktopCompatibilityPolicyReplacementOutcome, error) {
				if err := lockSystemAdministratorAuthenticationPaths(ctx, tx); err != nil {
					return desktopCompatibilityPolicyReplacementOutcome{}, err
				}
				var databaseNow time.Time
				if err := tx.Get(ctx, &databaseNow, `SELECT CURRENT_TIMESTAMP`); err != nil {
					return desktopCompatibilityPolicyReplacementOutcome{}, fmt.Errorf(
						"read desktop compatibility policy mutation time: %w",
						err,
					)
				}
				administrator, err := isActiveSystemAdministrator(
					ctx,
					tx,
					input.ActorID.String(),
					databaseNow,
				)
				if err != nil {
					return desktopCompatibilityPolicyReplacementOutcome{}, err
				}
				if !administrator {
					return desktopCompatibilityPolicyReplacementOutcome{}, store.NewErrConflict(
						"desktop_compatibility_policy",
						"actor_not_system_administrator",
						nil,
					)
				}
				current, err := getDesktopCompatibilityPolicy(ctx, tx, "FOR UPDATE")
				if err != nil {
					return desktopCompatibilityPolicyReplacementOutcome{}, err
				}
				if current.Revision != input.ExpectedRevision {
					return desktopCompatibilityPolicyReplacementOutcome{},
						&store.ErrDesktopCompatibilityPolicyRevisionConflict{CurrentRevision: current.Revision}
				}
				candidate := current.Clone()
				if err := candidate.Replace(input.ExpectedRevision, input.Settings, databaseNow); err != nil {
					return desktopCompatibilityPolicyReplacementOutcome{}, store.NewErrInvalidInput(
						"desktop_compatibility_policy",
						"replacement",
						nil,
					).Wrap(err)
				}
				changed := candidate.Revision != current.Revision
				if changed {
					encodedBuildIDs, encodeErr := json.Marshal(candidate.RevokedDesktopBuildIDs)
					if encodeErr != nil {
						return desktopCompatibilityPolicyReplacementOutcome{}, store.NewErrInvalidInput(
							"desktop_compatibility_policy",
							"revoked_build_ids",
							nil,
						).Wrap(encodeErr)
					}
					result, updateErr := tx.Exec(ctx, `
						UPDATE desktop_compatibility_policies
						   SET revision=?, minimum_desktop_release=?, revoked_desktop_build_ids=?::jsonb,
						       administrator_message=?, availability=?, retry_at=?, updated_at=?
						 WHERE singleton=1 AND revision=?`,
						candidate.Revision,
						candidate.MinimumDesktopRelease,
						encodedBuildIDs,
						candidate.AdministratorMessage,
						candidate.Availability,
						candidate.RetryAt.Ptr(),
						candidate.UpdatedAt,
						current.Revision,
					)
					if updateErr != nil {
						return desktopCompatibilityPolicyReplacementOutcome{}, fmt.Errorf(
							"replace desktop compatibility policy: %w",
							translateError(
								"desktop_compatibility_policy",
								candidate.InstitutionID.String(),
								updateErr,
							),
						)
					}
					if err := requireDesktopCompatibilityPolicyRevisionAffected(result); err != nil {
						return desktopCompatibilityPolicyReplacementOutcome{}, err
					}
				}
				outcome := desktopCompatibilityPolicyReplacementOutcome{
					Policy:  candidate,
					Changed: changed,
				}
				if err := completeDesktopCompatibilityPolicyAudit(
					ctx,
					tx,
					input,
					outcome,
					false,
					"",
				); err != nil {
					return desktopCompatibilityPolicyReplacementOutcome{}, err
				}
				return outcome, nil
			},
			encode: func(outcome desktopCompatibilityPolicyReplacementOutcome) ([]byte, error) {
				return json.Marshal(outcome)
			},
			decode: decodeDesktopCompatibilityPolicyReplacementOutcome,
			completeReplay: func(
				ctx context.Context,
				tx *sqlxTxWrapper,
				outcome desktopCompatibilityPolicyReplacementOutcome,
				originalAuditID string,
			) error {
				return completeDesktopCompatibilityPolicyAudit(
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
	return &store.DesktopCompatibilityPolicyReplacementResult{
		Policy:   mutation.Value.Policy,
		Changed:  mutation.Value.Changed,
		Replayed: mutation.Replayed,
	}, nil
}

func requireDesktopCompatibilityPolicyRevisionAffected(result sql.Result) error {
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read desktop compatibility policy affected rows: %w", err)
	}
	if affected == 1 {
		return nil
	}
	return store.NewErrConflict(
		"desktop_compatibility_policy",
		"desktop_compatibility_policy_changed",
		nil,
	)
}

func getDesktopCompatibilityPolicy(
	ctx context.Context,
	executor sqlxExecutor,
	lock string,
) (*model.DesktopCompatibilityPolicy, error) {
	query := `SELECT institution_id, revision, minimum_desktop_release,
		revoked_desktop_build_ids, administrator_message, availability, retry_at, created_at, updated_at
		FROM desktop_compatibility_policies WHERE singleton=1`
	if lock != "" {
		query += " " + lock
	}
	var row desktopCompatibilityPolicyRow
	if err := executor.Get(ctx, &row, query); err != nil {
		return nil, translateError("desktop_compatibility_policy", "singleton", err)
	}
	return row.model()
}

func requireDesktopCompatibilityPolicyRevision(
	ctx context.Context,
	executor sqlxExecutor,
	expected int64,
) error {
	if expected < 1 {
		return store.NewErrInvalidInput("desktop_compatibility_policy", "revision", nil)
	}
	policy, err := getDesktopCompatibilityPolicy(ctx, executor, "FOR SHARE")
	if err != nil {
		return err
	}
	if policy.Revision != expected {
		return store.NewErrConflict("desktop_compatibility_policy", "desktop_compatibility_policy_revision", nil)
	}
	return nil
}

func (r desktopCompatibilityPolicyRow) model() (*model.DesktopCompatibilityPolicy, error) {
	institutionID, err := model.ParseInstitutionID(r.InstitutionID)
	if err != nil {
		return nil, invalidPersistedState("desktop_compatibility_policy", "institution_id", err)
	}
	revokedBuildIDs := []string{}
	if err := json.Unmarshal(r.RevokedDesktopBuildIDs, &revokedBuildIDs); err != nil {
		return nil, invalidPersistedState("desktop_compatibility_policy", "revoked_build_ids", err)
	}
	policy := &model.DesktopCompatibilityPolicy{
		InstitutionID:          institutionID,
		Revision:               r.Revision,
		MinimumDesktopRelease:  r.MinimumDesktopRelease,
		RevokedDesktopBuildIDs: revokedBuildIDs,
		AdministratorMessage:   r.AdministratorMessage,
		Availability:           model.DesktopAvailability(r.Availability),
		RetryAt:                model.OptionalTimeFromPtr(r.RetryAt),
		CreatedAt:              model.TimeUTC(r.CreatedAt),
		UpdatedAt:              model.TimeUTC(r.UpdatedAt),
	}
	if err := policy.Validate(); err != nil {
		return nil, invalidPersistedState("desktop_compatibility_policy", "value", err)
	}
	return policy, nil
}

func validateDesktopCompatibilityPolicyReplacement(
	input *store.DesktopCompatibilityPolicyReplacement,
	command *store.CommandIdempotency,
) error {
	if input == nil || !input.ActorID.IsValid() || input.ExpectedRevision < 1 ||
		input.Settings.Validate() != nil || !model.IsValidId(input.AuditEventID) || input.AuditAt <= 0 ||
		command == nil || command.UserID != input.ActorID ||
		command.Operation != "desktop_compatibility_policy.replace.v1" ||
		command.FingerprintVersion != 1 || command.OutcomeVersion != 1 ||
		command.Retention <= 0 || command.Wait <= 0 {
		return store.NewErrInvalidInput("desktop_compatibility_policy", "replacement", nil)
	}
	return nil
}

func decodeDesktopCompatibilityPolicyReplacementOutcome(
	version int,
	encoded []byte,
) (desktopCompatibilityPolicyReplacementOutcome, error) {
	if version != 1 {
		return desktopCompatibilityPolicyReplacementOutcome{}, invalidPersistedState(
			"command_outcome",
			"outcome_version",
			errors.New("unsupported desktop compatibility policy outcome version"),
		)
	}
	var outcome desktopCompatibilityPolicyReplacementOutcome
	if err := json.Unmarshal(encoded, &outcome); err != nil ||
		outcome.Policy == nil || outcome.Policy.Validate() != nil {
		return desktopCompatibilityPolicyReplacementOutcome{}, invalidPersistedState(
			"command_outcome",
			"outcome",
			errors.New("invalid desktop compatibility policy outcome"),
		)
	}
	return outcome, nil
}

func completeDesktopCompatibilityPolicyAudit(
	ctx context.Context,
	tx *sqlxTxWrapper,
	input *store.DesktopCompatibilityPolicyReplacement,
	outcome desktopCompatibilityPolicyReplacementOutcome,
	replayed bool,
	originalAuditID string,
) error {
	data := map[string]any{
		"operation":                 "replace",
		"expected_revision":         input.ExpectedRevision,
		"resulting_revision":        outcome.Policy.Revision,
		"changed":                   outcome.Changed,
		"minimum_desktop_release":   outcome.Policy.MinimumDesktopRelease,
		"revoked_build_count":       len(outcome.Policy.RevokedDesktopBuildIDs),
		"administrator_message_set": outcome.Policy.AdministratorMessage != "",
		"availability":              outcome.Policy.Availability,
		"retry_at_set":              outcome.Policy.RetryAt.Valid,
	}
	if replayed {
		data["idempotency_replayed"] = true
		data["original_audit_event_id"] = originalAuditID
	}
	encoded, err := model.EncodeAuditData(data)
	if err != nil {
		return store.NewErrInvalidInput("desktop_compatibility_policy", "audit", nil).Wrap(err)
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
		return fmt.Errorf("complete desktop compatibility policy audit: %w", err)
	}
	return nil
}

var _ store.DesktopCompatibilityPolicyStore = (*SQLDesktopCompatibilityPolicyStore)(nil)
