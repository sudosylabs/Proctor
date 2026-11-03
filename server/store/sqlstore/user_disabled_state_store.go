// ---------------------------------------------------------------------------------------------
// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// Modifications Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------
//
// Adapted from Mattermost server/channels/store/sqlstore/user_store.go.
// Proctor retains the per-model query builder, explicit columns, named writes,
// normalized identity lookups, model lifecycle, and translated store errors
// while using its credential-separated user model.

package sqlstore

import (
	"context"
	"fmt"
	"time"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

// SetDisabled disables or re-enables the User, fenced on ExpectedRevision, and
// enqueues the account Notice mail. Disabling revokes the User's Sessions and
// refuses to remove the last System Administrator. A change to the current
// state is a conflict without input.Command, and an audited no-op under a
// command idempotency key.
func (s SQLUserStore) SetDisabled(
	ctx context.Context,
	input *store.UserDisabledStateChange,
) (*store.UserDisabledStateResult, error) {
	plan, err := planUserDisabledStateChange(input)
	if err != nil {
		return nil, err
	}
	execute := func(ctx context.Context, tx *sqlxTxWrapper) (*userDisabledMutationResult, error) {
		return changeUserDisabledState(ctx, tx, input, plan)
	}
	if input.Command == nil {
		result, err := runSQLTransaction(ctx, s.GetMaster().Begin, "audited user disabled state change", execute)
		if err != nil {
			return nil, err
		}
		input.NoOp = result.NoOp
		return result.Value, nil
	}
	return s.setDisabledIdempotently(ctx, input, execute)
}

// userDisabledStatePlan carries what SetDisabled derives from its input before the transaction.
type userDisabledStatePlan struct {
	revocationReason model.SessionRevocationReason
	mailUnprepared   bool
	payloadKeyID     string
}

// planUserDisabledStateChange validates a disabled-state change and its prepared Notice mail.
func planUserDisabledStateChange(input *store.UserDisabledStateChange) (userDisabledStatePlan, error) {
	if input == nil || !input.ID.IsValid() || input.ExpectedRevision <= 0 ||
		input.ChangedAt.IsZero() || !input.AuditEventID.IsValid() || input.AuditAt.IsZero() ||
		!validAccessDeploymentCapabilities(input.Capabilities) {
		return userDisabledStatePlan{}, store.NewErrInvalidInput("user", "disabled_state_change", nil)
	}
	plan := userDisabledStatePlan{
		revocationReason: model.SessionRevocationReason(model.SanitizeUnicode(string(input.RevocationReason))),
	}
	if input.Disabled && !plan.revocationReason.IsValid() {
		return userDisabledStatePlan{}, store.NewErrInvalidInput("session", "revocation_reason", nil)
	}
	templateKey := model.MailTemplateIdentityAccountEnabled
	if input.Disabled {
		templateKey = model.MailTemplateIdentityAccountDisabled
	}
	plan.mailUnprepared = input.Occurrence == nil && input.Delivery == nil && input.DeliveryJob == nil
	if plan.mailUnprepared && input.Command == nil {
		return userDisabledStatePlan{}, store.NewErrInvalidInput("user", "disabled_state_notice", nil)
	}
	if !plan.mailUnprepared {
		var err error
		plan.payloadKeyID, err = validateSecurityNoticeMail(input.ID, input.Occurrence, input.Delivery, input.DeliveryJob, templateKey, input.ChangedAt)
		if err != nil {
			return userDisabledStatePlan{}, err
		}
	}
	return plan, nil
}

// setDisabledIdempotently runs the disabled-state change under its command idempotency key.
func (s SQLUserStore) setDisabledIdempotently(
	ctx context.Context,
	input *store.UserDisabledStateChange,
	execute func(context.Context, *sqlxTxWrapper) (*userDisabledMutationResult, error),
) (*store.UserDisabledStateResult, error) {
	result, err := runIdempotentMutation(ctx, s.SQLStore, "idempotent user disabled state change", idempotentMutation[*userDisabledMutationResult]{
		command: input.Command, auditEventID: input.AuditEventID, execute: execute,
		encode: encodeUserDisabledMutationOutcome, decode: decodeUserDisabledMutationOutcome,
		onboardingOutcome: func(value *userDisabledMutationResult) (onboardingImportCommandResult, error) {
			return administrativeOnboardingOutcome(value.Value.User.ID.String(), value.NoOp)
		},
		hydrateReplay: func(ctx context.Context, tx *sqlxTxWrapper, value *userDisabledMutationResult) (*userDisabledMutationResult, error) {
			user, hydrateErr := getUserByID(ctx, tx, value.Value.User.ID.String())
			if hydrateErr != nil {
				return nil, hydrateErr
			}
			value.Value = &store.UserDisabledStateResult{User: user, RevokedSessions: []*model.Session{}, RevokedTokenHashes: []string{}}
			return value, nil
		},
		completeReplay: func(ctx context.Context, tx *sqlxTxWrapper, value *userDisabledMutationResult, original string) error {
			return completeAdministrativeReplayAudit(ctx, tx, input.AuditEventID, input.AuditAt, "user_id", value.Value.User.ID.String(), value.NoOp, original)
		},
	})
	if err != nil {
		return nil, err
	}
	input.Replayed, input.NoOp = result.Replayed, result.Value.NoOp
	return result.Value.Value, nil
}

// changeUserDisabledState applies the disabled-state change inside its transaction.
func changeUserDisabledState(ctx context.Context, tx *sqlxTxWrapper, input *store.UserDisabledStateChange, plan userDisabledStatePlan) (*userDisabledMutationResult, error) {
	if err := lockUserDisabledStatePaths(ctx, tx, input, plan.payloadKeyID); err != nil {
		return nil, err
	}
	currentUser, err := getUserByID(ctx, tx, input.ID.String())
	if err != nil {
		return nil, err
	}
	if currentUser.DisabledAt.Valid == input.Disabled {
		if input.Command == nil {
			return nil, store.NewErrConflict("user", "disabled_state", nil)
		}
		if err = completeAdministrativeNoOpAudit(ctx, tx, input.AuditEventID, input.AuditAt, "user_id", currentUser.ID.String()); err != nil {
			return nil, err
		}
		return &userDisabledMutationResult{Value: &store.UserDisabledStateResult{User: currentUser, RevokedSessions: []*model.Session{}, RevokedTokenHashes: []string{}}, NoOp: true}, nil
	}
	if plan.mailUnprepared {
		return nil, store.NewErrInvalidInput("user", "disabled_state_notice", nil)
	}
	// Serialize disabling with login and refresh rotation before changing the
	// user row. A login that commits first is included in the revocation; one
	// that follows observes the disabled account.
	if input.Disabled {
		if err = requireRemainingSystemAdministrator(ctx, tx, input); err != nil {
			return nil, err
		}
	}
	user, err := persistUserDisabledState(ctx, tx, input)
	if err != nil {
		return nil, err
	}
	result := &store.UserDisabledStateResult{
		User:               user,
		RevokedSessions:    []*model.Session{},
		RevokedTokenHashes: []string{},
	}
	if input.Disabled {
		if err = revokeDisabledUserSessions(ctx, tx, input, plan.revocationReason, result); err != nil {
			return nil, err
		}
	}
	if err = insertSecurityNoticeMail(ctx, tx, input.Occurrence, input.Delivery, input.DeliveryJob, plan.payloadKeyID); err != nil {
		return nil, err
	}
	if err = completeUserDisabledStateAudit(ctx, tx, input, result.User); err != nil {
		return nil, err
	}
	return &userDisabledMutationResult{Value: result}, nil
}

// lockUserDisabledStatePaths checks the mail key and takes the authentication-path, Session and User locks in order.
func lockUserDisabledStatePaths(ctx context.Context, tx *sqlxTxWrapper, input *store.UserDisabledStateChange, payloadKeyID string) error {
	if payloadKeyID != "" {
		if err := requireMailPayloadPrimary(ctx, tx, payloadKeyID); err != nil {
			return err
		}
	}
	// Security-sensitive account disablement takes the installation-wide
	// authentication-path fence before any User row. User eligibility changes
	// then follow the shared User -> mail-eligibility singleton order.
	if input.Disabled {
		if err := lockSystemAdministratorAuthenticationPaths(ctx, tx); err != nil {
			return err
		}
		// Match every authentication and Session mutation: after the
		// installation-wide path fence, serialize this User's Sessions before
		// taking the User row. Reversing these two locks can deadlock with a
		// concurrent Desktop code exchange that already owns the Session fence.
		if err := lockUserSessions(ctx, tx, input.ID.String()); err != nil {
			return err
		}
	}
	var lockedUserID string
	if err := tx.Get(ctx, &lockedUserID, `SELECT id FROM users WHERE id=? AND archived_at IS NULL FOR UPDATE`, input.ID); err != nil {
		return translateError("user", input.ID.String(), err)
	}
	return nil
}

// requireRemainingSystemAdministrator refuses to disable the last System Administrator with a usable authentication path.
func requireRemainingSystemAdministrator(ctx context.Context, tx *sqlxTxWrapper, input *store.UserDisabledStateChange) error {
	policy, err := getAccessPolicy(ctx, tx, "FOR SHARE")
	if err != nil {
		return err
	}
	at := model.TimeUTC(input.ChangedAt)
	administrator, err := isActiveSystemAdministrator(ctx, tx, input.ID.String(), at)
	if err != nil {
		return err
	}
	if administrator {
		remaining, pathErr := hasUsableSystemAdministratorAuthenticationPath(
			ctx, tx, policy.Settings(), input.Capabilities, at,
			systemAdministratorAuthenticationPathScope{ExcludedUserID: input.ID.String()},
		)
		if pathErr != nil {
			return pathErr
		}
		if !remaining {
			return store.NewErrConflict("user", "users_last_system_admin", nil)
		}
	}
	return nil
}

// persistUserDisabledState advances the mail eligibility revision and writes the User's new disabled state.
func persistUserDisabledState(ctx context.Context, tx *sqlxTxWrapper, input *store.UserDisabledStateChange) (*model.User, error) {
	var disabledAt time.Time
	if input.Disabled {
		disabledAt = input.ChangedAt
	}
	mailEligibilityRevision, err := advanceUserMailEligibilityRevision(ctx, tx)
	if err != nil {
		return nil, err
	}
	row, err := setUserDisabled(
		ctx,
		tx,
		input.ID.String(),
		input.ExpectedRevision,
		disabledAt,
		input.ChangedAt,
		mailEligibilityRevision,
	)
	if err != nil {
		return nil, err
	}
	return row.model()
}

// revokeDisabledUserSessions revokes every Session of a disabled User and records them on result.
func revokeDisabledUserSessions(ctx context.Context, tx *sqlxTxWrapper, input *store.UserDisabledStateChange,
	revocationReason model.SessionRevocationReason, result *store.UserDisabledStateResult,
) error {
	sessionRows, hashes, err := revokeAllUserSessions(
		ctx,
		tx,
		input.ID.String(),
		input.ChangedAt,
		revocationReason,
	)
	if err != nil {
		return err
	}
	result.RevokedSessions, err = revokedSessionModels(
		sessionRows,
		input.ChangedAt,
		revocationReason,
	)
	if err != nil {
		return err
	}
	result.RevokedTokenHashes = hashes
	return nil
}

// completeUserDisabledStateAudit completes the attempted audit event with the changed User.
func completeUserDisabledStateAudit(ctx context.Context, tx *sqlxTxWrapper, input *store.UserDisabledStateChange, user *model.User) error {
	encoded, appErr := model.EncodeAuditData(user.Auditable())
	if appErr != nil {
		return appErr
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
		return fmt.Errorf("complete user disabled state audit: %w", err)
	}
	return nil
}

type userDisabledMutationResult struct {
	Value *store.UserDisabledStateResult
	NoOp  bool
}

type userDisabledCommandOutcome struct {
	UserID string `json:"user_id"`
	NoOp   bool   `json:"no_op,omitempty"`
}

func (userDisabledCommandOutcome) outcomeDocument() {}

func encodeUserDisabledMutationOutcome(value *userDisabledMutationResult) ([]byte, error) {
	if value == nil || value.Value == nil || value.Value.User == nil || !value.Value.User.ID.IsValid() {
		return nil, store.NewErrInvalidInput("user", "command_outcome", nil)
	}
	return encodeOutcome(userDisabledCommandOutcome{UserID: value.Value.User.ID.String(), NoOp: value.NoOp})
}
func decodeUserDisabledMutationOutcome(data []byte) (*userDisabledMutationResult, error) {
	var outcome userDisabledCommandOutcome
	if err := decodeOutcome(data, &outcome); err != nil {
		return nil, err
	}
	id, err := model.ParseUserID(outcome.UserID)
	if err != nil {
		return nil, invalidPersistedState("command_outcome", "user_id", err)
	}
	return &userDisabledMutationResult{Value: &store.UserDisabledStateResult{User: &model.User{ID: id}}, NoOp: outcome.NoOp}, nil
}

func setUserDisabled(
	ctx context.Context,
	tx *sqlxTxWrapper,
	id string,
	expectedRevision int64,
	disabledAt time.Time,
	updateAt time.Time,
	mailEligibilityRevision int64,
) (*userRow, error) {
	if expectedRevision <= 0 || updateAt.IsZero() ||
		(!disabledAt.IsZero() && !disabledAt.Equal(updateAt)) || mailEligibilityRevision < 1 {
		return nil, store.NewErrInvalidInput("user", "disabled_at", disabledAt)
	}
	updateTime := model.TimeUTC(updateAt)
	// Users can carry PostgreSQL-timed or another node's lifecycle instants;
	// the change never records a modification or disable before them.
	result, err := tx.Exec(ctx, `
		UPDATE users
		   SET updated_at = GREATEST(updated_at, ?),
		       disabled_at = CASE WHEN ? THEN GREATEST(created_at, ?) END,
		       mail_eligibility_revision = ?, revision = revision + 1
		 WHERE id = ? AND archived_at IS NULL AND revision = ?
		   AND ((? AND disabled_at IS NULL) OR (NOT ? AND disabled_at IS NOT NULL))`,
		updateTime, !disabledAt.IsZero(), updateTime, mailEligibilityRevision, id, expectedRevision, !disabledAt.IsZero(), !disabledAt.IsZero(),
	)
	if err != nil {
		return nil, fmt.Errorf(
			"set user disabled state: %w",
			translateError("user", id, err),
		)
	}
	if err := requireUserRevisionAffected(ctx, tx, result, id); err != nil {
		return nil, err
	}
	updated, err := getUserByID(ctx, tx, id)
	if err != nil {
		return nil, err
	}
	row := newUserRow(updated)
	return &row, nil
}
