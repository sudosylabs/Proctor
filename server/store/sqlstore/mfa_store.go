// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only
//
// Adapted from Mattermost server/channels/store/sqlstore/user_store.go MFA
// persistence. Proctor uses a dedicated encrypted credential aggregate,
// hashed single-use recovery codes, transactionally serialized replay
// prevention, and session-assurance updates.

package sqlstore

import (
	"context"
	"fmt"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

type SqlMFAStore struct {
	*SqlStore
}

type mfaCredentialRow struct {
	ID               string         `db:"id"`
	CreateAt         int64          `db:"create_at"`
	UpdateAt         int64          `db:"update_at"`
	DeleteAt         int64          `db:"delete_at"`
	UserID           string         `db:"user_id"`
	State            model.MFAState `db:"state"`
	EncryptedSecret  string         `db:"encrypted_secret"`
	EncryptionKeyID  string         `db:"encryption_key_id"`
	PendingExpiresAt int64          `db:"pending_expires_at"`
	EnabledAt        int64          `db:"enabled_at"`
	LastUsedTimeStep int64          `db:"last_used_time_step"`
}

func newSqlMFAStore(sqlStore *SqlStore) store.MFAStore {
	return &SqlMFAStore{SqlStore: sqlStore}
}

func (s SqlMFAStore) SavePending(
	ctx context.Context,
	credential *model.MFACredential,
) (*model.MFACredential, error) {
	if credential == nil || credential.Id != "" {
		return nil, store.NewErrInvalidInput("mfa_credential", "value", nil)
	}
	candidate := *credential
	candidate.PreSave()
	if appErr := candidate.IsValid(); appErr != nil {
		return nil, appErr
	}
	tx, err := s.GetMaster().Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin MFA setup: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if err := lockMFAUser(ctx, tx, candidate.UserId); err != nil {
		return nil, err
	}
	var active int
	if err := tx.Get(ctx, &active, `
		SELECT COUNT(*)
		  FROM mfa_credentials
		 WHERE user_id = ? AND delete_at = 0 AND state = 'active'`,
		candidate.UserId,
	); err != nil {
		return nil, fmt.Errorf("count active MFA credentials: %w", err)
	}
	if active != 0 {
		return nil, store.NewErrConflict("mfa_credential", "mfa_already_enabled", nil)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE mfa_credentials
		   SET update_at = GREATEST(update_at, ?), delete_at = ?
		 WHERE user_id = ? AND delete_at = 0`,
		candidate.CreateAt, candidate.CreateAt, candidate.UserId,
	); err != nil {
		return nil, fmt.Errorf("replace pending MFA credential: %w", err)
	}
	if err := insertMFACredential(ctx, tx, &candidate); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit MFA setup: %w", err)
	}
	return &candidate, nil
}

func (s SqlMFAStore) GetByUser(
	ctx context.Context,
	userID string,
) (*model.MFACredential, error) {
	if !model.IsValidId(userID) {
		return nil, store.NewErrInvalidInput("mfa_credential", "user_id", userID)
	}
	var row mfaCredentialRow
	if err := s.GetMaster().Get(ctx, &row, mfaCredentialSelect+`
		 WHERE user_id = ? AND delete_at = 0`,
		userID,
	); err != nil {
		return nil, translateError("mfa_credential", userID, err)
	}
	return row.model(), nil
}

func (s SqlMFAStore) Activate(
	ctx context.Context,
	credentialID string,
	userID string,
	timeStep int64,
	recoveryCodes []*model.MFARecoveryCode,
	sessionID string,
	now int64,
) (*store.MFAActivationResult, error) {
	if !model.IsValidId(credentialID) || !model.IsValidId(userID) ||
		!model.IsValidId(sessionID) || timeStep <= 0 || now <= 0 ||
		len(recoveryCodes) == 0 || len(recoveryCodes) > model.MFARecoveryCodeMaxCount {
		return nil, store.NewErrInvalidInput("mfa_credential", "activate", nil)
	}
	prepared, err := prepareMFARecoveryCodes(userID, recoveryCodes)
	if err != nil {
		return nil, err
	}
	tx, err := s.GetMaster().Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin MFA activation: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if err := lockMFAUser(ctx, tx, userID); err != nil {
		return nil, err
	}
	if err := lockUserSessions(ctx, tx, userID); err != nil {
		return nil, err
	}
	var row mfaCredentialRow
	if err := tx.Get(ctx, &row, `
		UPDATE mfa_credentials
		   SET update_at = GREATEST(update_at, ?), state = 'active',
		       pending_expires_at = 0, enabled_at = ?,
		       last_used_time_step = ?
		 WHERE id = ? AND user_id = ? AND delete_at = 0
		   AND state = 'pending' AND pending_expires_at > ?
		 RETURNING id, create_at, update_at, delete_at, user_id, state,
		           encrypted_secret, encryption_key_id, pending_expires_at,
		           enabled_at, last_used_time_step`,
		now, now, timeStep, credentialID, userID, now,
	); err != nil {
		return nil, translateError("mfa_credential", credentialID, err)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE mfa_recovery_codes
		   SET update_at = GREATEST(update_at, ?), delete_at = ?
		 WHERE user_id = ? AND delete_at = 0`,
		now, now, userID,
	); err != nil {
		return nil, fmt.Errorf("replace MFA recovery codes: %w", err)
	}
	for _, code := range prepared {
		if err := insertMFARecoveryCode(ctx, tx, code); err != nil {
			return nil, err
		}
	}
	session, err := upgradeSessionAuthentication(
		ctx, tx, sessionID, userID, now,
	)
	if err != nil {
		return nil, err
	}
	hashes, err := selectActiveAccessTokenHashes(ctx, tx, sessionID)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit MFA activation: %w", err)
	}
	return &store.MFAActivationResult{
		Credential:        row.model(),
		Session:           session,
		AccessTokenHashes: hashes,
	}, nil
}

func (s SqlMFAStore) ConsumeSecondFactor(
	ctx context.Context,
	userID string,
	timeStep int64,
	recoveryCodeHash string,
	now int64,
) error {
	if !model.IsValidId(userID) || now <= 0 ||
		((timeStep <= 0) == (recoveryCodeHash == "")) ||
		(recoveryCodeHash != "" && !model.IsValidTokenHash(recoveryCodeHash)) {
		return store.NewErrInvalidInput("mfa_credential", "consume", nil)
	}
	tx, err := s.GetMaster().Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin MFA consumption: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if err := lockMFAUser(ctx, tx, userID); err != nil {
		return err
	}
	if timeStep > 0 {
		result, err := tx.Exec(ctx, `
			UPDATE mfa_credentials
			   SET update_at = GREATEST(update_at, ?),
			       last_used_time_step = ?
			 WHERE user_id = ? AND delete_at = 0 AND state = 'active'
			   AND last_used_time_step < ?`,
			now, timeStep, userID, timeStep,
		)
		if err != nil {
			return fmt.Errorf("consume MFA time step: %w", err)
		}
		if err := requireAffected(result, "mfa_credential", userID); err != nil {
			return err
		}
	} else {
		var credentialID string
		if err := tx.Get(ctx, &credentialID, `
			SELECT id FROM mfa_credentials
			 WHERE user_id = ? AND delete_at = 0 AND state = 'active'
			 FOR UPDATE`,
			userID,
		); err != nil {
			return translateError("mfa_credential", userID, err)
		}
		result, err := tx.Exec(ctx, `
			UPDATE mfa_recovery_codes
			   SET update_at = GREATEST(update_at, ?), used_at = ?
			 WHERE user_id = ? AND code_hash = ?
			   AND delete_at = 0 AND used_at = 0`,
			now, now, userID, recoveryCodeHash,
		)
		if err != nil {
			return fmt.Errorf("consume MFA recovery code: %w", err)
		}
		if err := requireAffected(result, "mfa_recovery_code", ""); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit MFA consumption: %w", err)
	}
	return nil
}

func (s SqlMFAStore) UpgradeSession(
	ctx context.Context,
	sessionID string,
	userID string,
	now int64,
) ([]string, error) {
	if !model.IsValidId(sessionID) || !model.IsValidId(userID) || now <= 0 {
		return nil, store.NewErrInvalidInput("session", "upgrade_mfa", nil)
	}
	tx, err := s.GetMaster().Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin session MFA upgrade: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if err := lockUserSessions(ctx, tx, userID); err != nil {
		return nil, err
	}
	if _, err := upgradeSessionAuthentication(ctx, tx, sessionID, userID, now); err != nil {
		return nil, err
	}
	hashes, err := selectActiveAccessTokenHashes(ctx, tx, sessionID)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit session MFA upgrade: %w", err)
	}
	return hashes, nil
}

func (s SqlMFAStore) ReplaceRecoveryCodes(
	ctx context.Context,
	userID string,
	recoveryCodes []*model.MFARecoveryCode,
	now int64,
) error {
	if !model.IsValidId(userID) || now <= 0 ||
		len(recoveryCodes) == 0 || len(recoveryCodes) > model.MFARecoveryCodeMaxCount {
		return store.NewErrInvalidInput("mfa_recovery_code", "replace", nil)
	}
	prepared, err := prepareMFARecoveryCodes(userID, recoveryCodes)
	if err != nil {
		return err
	}
	tx, err := s.GetMaster().Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin recovery-code replacement: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if err := lockMFAUser(ctx, tx, userID); err != nil {
		return err
	}
	var credentialID string
	if err := tx.Get(ctx, &credentialID, `
		SELECT id FROM mfa_credentials
		 WHERE user_id = ? AND delete_at = 0 AND state = 'active'
		 FOR UPDATE`,
		userID,
	); err != nil {
		return translateError("mfa_credential", userID, err)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE mfa_recovery_codes
		   SET update_at = GREATEST(update_at, ?), delete_at = ?
		 WHERE user_id = ? AND delete_at = 0`,
		now, now, userID,
	); err != nil {
		return fmt.Errorf("invalidate MFA recovery codes: %w", err)
	}
	for _, code := range prepared {
		if err := insertMFARecoveryCode(ctx, tx, code); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit recovery-code replacement: %w", err)
	}
	return nil
}

func (s SqlMFAStore) CountRecoveryCodes(
	ctx context.Context,
	userID string,
) (int, error) {
	if !model.IsValidId(userID) {
		return 0, store.NewErrInvalidInput("mfa_recovery_code", "user_id", userID)
	}
	var count int
	if err := s.GetMaster().Get(ctx, &count, `
		SELECT COUNT(*) FROM mfa_recovery_codes
		 WHERE user_id = ? AND delete_at = 0 AND used_at = 0`,
		userID,
	); err != nil {
		return 0, fmt.Errorf("count MFA recovery codes: %w", err)
	}
	return count, nil
}

func (s SqlMFAStore) Disable(
	ctx context.Context,
	userID string,
	now int64,
) (*store.MFADisableResult, error) {
	if !model.IsValidId(userID) || now <= 0 {
		return nil, store.NewErrInvalidInput("mfa_credential", "disable", nil)
	}
	tx, err := s.GetMaster().Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin MFA disable: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if err := lockMFAUser(ctx, tx, userID); err != nil {
		return nil, err
	}
	if err := lockUserSessions(ctx, tx, userID); err != nil {
		return nil, err
	}
	result, err := tx.Exec(ctx, `
		UPDATE mfa_credentials
		   SET update_at = GREATEST(update_at, ?), delete_at = ?
		 WHERE user_id = ? AND delete_at = 0 AND state = 'active'`,
		now, now, userID,
	)
	if err != nil {
		return nil, fmt.Errorf("disable MFA credential: %w", err)
	}
	if err := requireAffected(result, "mfa_credential", userID); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, `
		UPDATE mfa_recovery_codes
		   SET update_at = GREATEST(update_at, ?), delete_at = ?
		 WHERE user_id = ? AND delete_at = 0`,
		now, now, userID,
	); err != nil {
		return nil, fmt.Errorf("invalidate MFA recovery codes: %w", err)
	}
	hashes := []string{}
	if err := tx.Select(ctx, &hashes, `
		SELECT credential.token_hash
		  FROM session_credentials credential
		  JOIN sessions session ON session.id = credential.session_id
		 WHERE session.user_id = ?
		   AND session.delete_at = 0 AND session.revoked_at = 0
		   AND credential.kind = 'access'
		   AND credential.delete_at = 0 AND credential.revoked_at = 0
		 FOR UPDATE OF credential`,
		userID,
	); err != nil {
		return nil, fmt.Errorf("select MFA session credentials: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE sessions
		   SET update_at = GREATEST(update_at, ?),
		       authentication_strength = 'single_factor',
		       mfa_completed_at = 0
		 WHERE user_id = ? AND delete_at = 0 AND revoked_at = 0`,
		now, userID,
	); err != nil {
		return nil, fmt.Errorf("downgrade MFA sessions: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit MFA disable: %w", err)
	}
	return &store.MFADisableResult{AccessTokenHashes: hashes}, nil
}

const mfaCredentialSelect = `
	SELECT id, create_at, update_at, delete_at, user_id, state,
	       encrypted_secret, encryption_key_id, pending_expires_at,
	       enabled_at, last_used_time_step
	  FROM mfa_credentials`

func lockMFAUser(ctx context.Context, executor sqlxExecutor, userID string) error {
	if _, err := executor.Exec(
		ctx,
		"SELECT pg_advisory_xact_lock(hashtextextended(?, 0))",
		"proctor:mfa-user:"+userID,
	); err != nil {
		return fmt.Errorf("lock MFA user: %w", err)
	}
	return nil
}

func insertMFACredential(
	ctx context.Context,
	executor sqlxExecutor,
	credential *model.MFACredential,
) error {
	if _, err := executor.Exec(ctx, `
		INSERT INTO mfa_credentials (
			id, create_at, update_at, delete_at, user_id, state,
			encrypted_secret, encryption_key_id, pending_expires_at,
			enabled_at, last_used_time_step
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		credential.Id, credential.CreateAt, credential.UpdateAt,
		credential.DeleteAt, credential.UserId, credential.State,
		credential.EncryptedSecret, credential.EncryptionKeyId,
		credential.PendingExpiresAt, credential.EnabledAt,
		credential.LastUsedTimeStep,
	); err != nil {
		return translateError("mfa_credential", credential.Id, err)
	}
	return nil
}

func prepareMFARecoveryCodes(
	userID string,
	codes []*model.MFARecoveryCode,
) ([]*model.MFARecoveryCode, error) {
	prepared := make([]*model.MFARecoveryCode, 0, len(codes))
	seen := make(map[string]struct{}, len(codes))
	for _, code := range codes {
		if code == nil || code.Id != "" {
			return nil, store.NewErrInvalidInput("mfa_recovery_code", "value", nil)
		}
		candidate := *code
		candidate.UserId = userID
		candidate.PreSave()
		if appErr := candidate.IsValid(); appErr != nil {
			return nil, appErr
		}
		if _, exists := seen[candidate.CodeHash]; exists {
			return nil, store.NewErrInvalidInput("mfa_recovery_code", "code_hash", nil)
		}
		seen[candidate.CodeHash] = struct{}{}
		prepared = append(prepared, &candidate)
	}
	return prepared, nil
}

func insertMFARecoveryCode(
	ctx context.Context,
	executor sqlxExecutor,
	code *model.MFARecoveryCode,
) error {
	if _, err := executor.Exec(ctx, `
		INSERT INTO mfa_recovery_codes (
			id, create_at, update_at, delete_at, user_id, code_hash, used_at
		) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		code.Id, code.CreateAt, code.UpdateAt, code.DeleteAt,
		code.UserId, code.CodeHash, code.UsedAt,
	); err != nil {
		return translateError("mfa_recovery_code", code.Id, err)
	}
	return nil
}

func upgradeSessionAuthentication(
	ctx context.Context,
	executor sqlxExecutor,
	sessionID string,
	userID string,
	now int64,
) (*model.Session, error) {
	var row sessionRow
	if err := executor.Get(ctx, &row, `
		UPDATE sessions
		   SET update_at = GREATEST(update_at, ?),
		       authentication_strength = 'multi_factor',
		       mfa_completed_at = ?
		 WHERE id = ? AND user_id = ? AND delete_at = 0 AND revoked_at = 0
		   AND idle_expires_at > ? AND expires_at > ?
		 RETURNING id, create_at, update_at, delete_at, user_id, client_type,
		           device_id, device_name, authentication_method,
		           authentication_strength, authenticated_at, mfa_completed_at,
		           last_activity_at, idle_expires_at, expires_at, revoked_at,
		           revocation_reason`,
		now, now, sessionID, userID, now, now,
	); err != nil {
		return nil, translateError("session", sessionID, err)
	}
	return row.model(), nil
}

func selectActiveAccessTokenHashes(
	ctx context.Context,
	executor sqlxExecutor,
	sessionID string,
) ([]string, error) {
	hashes := []string{}
	if err := executor.Select(ctx, &hashes, `
		SELECT token_hash
		  FROM session_credentials
		 WHERE session_id = ? AND kind = 'access'
		   AND delete_at = 0 AND revoked_at = 0
		 FOR UPDATE`,
		sessionID,
	); err != nil {
		return nil, fmt.Errorf("select MFA access credential hashes: %w", err)
	}
	return hashes, nil
}

func (row mfaCredentialRow) model() *model.MFACredential {
	return &model.MFACredential{
		Id: row.ID, CreateAt: row.CreateAt, UpdateAt: row.UpdateAt,
		DeleteAt: row.DeleteAt, UserId: row.UserID, State: row.State,
		EncryptedSecret: row.EncryptedSecret, EncryptionKeyId: row.EncryptionKeyID,
		PendingExpiresAt: row.PendingExpiresAt, EnabledAt: row.EnabledAt,
		LastUsedTimeStep: row.LastUsedTimeStep,
	}
}

var _ store.MFAStore = (*SqlMFAStore)(nil)
