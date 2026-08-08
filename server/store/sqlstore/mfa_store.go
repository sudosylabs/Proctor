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
	"database/sql"
	"fmt"
	"time"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

type SqlMFAStore struct {
	*SqlStore
}

type mfaCredentialRow struct {
	ID               string         `db:"id"`
	CreatedAt        time.Time      `db:"created_at"`
	UpdatedAt        time.Time      `db:"updated_at"`
	ArchivedAt       sql.NullTime   `db:"archived_at"`
	UserID           string         `db:"user_id"`
	State            model.MFAState `db:"state"`
	EncryptedSecret  string         `db:"encrypted_secret"`
	EncryptionKeyID  string         `db:"encryption_key_id"`
	PendingExpiresAt sql.NullTime   `db:"pending_expires_at"`
	ActivatedAt      sql.NullTime   `db:"activated_at"`
	LastUsedTimeStep int64          `db:"last_used_time_step"`
}

func newSqlMFAStore(sqlStore *SqlStore) store.MFAStore {
	return &SqlMFAStore{SqlStore: sqlStore}
}

func (s SqlMFAStore) SavePending(
	ctx context.Context,
	credential *model.MFACredential,
) (*model.MFACredential, error) {
	if credential == nil {
		return nil, store.NewErrInvalidInput("mfa_credential", "value", nil)
	}
	if !credential.ID.IsZero() {
		return nil, store.NewErrInvalidInput("mfa_credential", "id", credential.ID.String())
	}
	candidate := *credential
	at := model.NowUTC()
	if !candidate.CreatedAt.IsZero() {
		at = model.TimeUTC(candidate.CreatedAt)
	}
	candidate.PrepareCreate(model.NewMFACredentialID(), at)
	if err := candidate.Validate(); err != nil {
		return nil, err
	}
	tx, err := s.GetMaster().Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin MFA setup: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	userID := candidate.UserID.String()
	if err := lockMFAUser(ctx, tx, userID); err != nil {
		return nil, err
	}
	var active int
	if err := tx.Get(ctx, &active, `
		SELECT COUNT(*)
		  FROM mfa_credentials
		 WHERE user_id = ? AND archived_at IS NULL AND state = 'active'`,
		userID,
	); err != nil {
		return nil, fmt.Errorf("count active MFA credentials: %w", err)
	}
	if active != 0 {
		return nil, store.NewErrConflict("mfa_credential", "mfa_already_enabled", nil)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE mfa_credentials
		   SET updated_at = GREATEST(updated_at, ?), archived_at = ?
		 WHERE user_id = ? AND archived_at IS NULL`,
		candidate.CreatedAt, candidate.CreatedAt, userID,
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
		 WHERE user_id = ? AND archived_at IS NULL`,
		userID,
	); err != nil {
		return nil, translateError("mfa_credential", userID, err)
	}
	credential, err := row.model()
	if err != nil {
		return nil, err
	}
	return credential, nil
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
	at := model.TimeFromMillis(now)
	prepared, err := prepareMFARecoveryCodes(userID, recoveryCodes, at)
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
		   SET updated_at = GREATEST(updated_at, ?), state = 'active',
		       pending_expires_at = NULL, activated_at = ?,
		       last_used_time_step = ?
		 WHERE id = ? AND user_id = ? AND archived_at IS NULL
		   AND state = 'pending' AND pending_expires_at > ?
		 RETURNING id, created_at, updated_at, archived_at, user_id, state,
		           encrypted_secret, encryption_key_id, pending_expires_at,
		           activated_at, last_used_time_step`,
		at, at, timeStep, credentialID, userID, at,
	); err != nil {
		return nil, translateError("mfa_credential", credentialID, err)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE mfa_recovery_codes
		   SET updated_at = GREATEST(updated_at, ?), archived_at = ?
		 WHERE user_id = ? AND archived_at IS NULL`,
		at, at, userID,
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
	credential, err := row.model()
	if err != nil {
		return nil, err
	}
	return &store.MFAActivationResult{
		Credential:        credential,
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
	at := model.TimeFromMillis(now)
	if err := lockMFAUser(ctx, tx, userID); err != nil {
		return err
	}
	if timeStep > 0 {
		result, err := tx.Exec(ctx, `
			UPDATE mfa_credentials
			   SET updated_at = GREATEST(updated_at, ?),
			       last_used_time_step = ?
			 WHERE user_id = ? AND archived_at IS NULL AND state = 'active'
			   AND last_used_time_step < ?`,
			at, timeStep, userID, timeStep,
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
			 WHERE user_id = ? AND archived_at IS NULL AND state = 'active'
			 FOR UPDATE`,
			userID,
		); err != nil {
			return translateError("mfa_credential", userID, err)
		}
		result, err := tx.Exec(ctx, `
			UPDATE mfa_recovery_codes
			   SET updated_at = GREATEST(updated_at, ?), consumed_at = ?
			 WHERE user_id = ? AND code_hash = ?
			   AND archived_at IS NULL AND consumed_at IS NULL`,
			at, at, userID, recoveryCodeHash,
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
	at := model.TimeFromMillis(now)
	prepared, err := prepareMFARecoveryCodes(userID, recoveryCodes, at)
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
		 WHERE user_id = ? AND archived_at IS NULL AND state = 'active'
		 FOR UPDATE`,
		userID,
	); err != nil {
		return translateError("mfa_credential", userID, err)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE mfa_recovery_codes
		   SET updated_at = GREATEST(updated_at, ?), archived_at = ?
		 WHERE user_id = ? AND archived_at IS NULL`,
		at, at, userID,
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
		 WHERE user_id = ? AND archived_at IS NULL AND consumed_at IS NULL`,
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
	at := model.TimeFromMillis(now)
	if err := lockMFAUser(ctx, tx, userID); err != nil {
		return nil, err
	}
	if err := lockUserSessions(ctx, tx, userID); err != nil {
		return nil, err
	}
	result, err := tx.Exec(ctx, `
		UPDATE mfa_credentials
		   SET updated_at = GREATEST(updated_at, ?), archived_at = ?
		 WHERE user_id = ? AND archived_at IS NULL AND state = 'active'`,
		at, at, userID,
	)
	if err != nil {
		return nil, fmt.Errorf("disable MFA credential: %w", err)
	}
	if err := requireAffected(result, "mfa_credential", userID); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, `
		UPDATE mfa_recovery_codes
		   SET updated_at = GREATEST(updated_at, ?), archived_at = ?
		 WHERE user_id = ? AND archived_at IS NULL`,
		at, at, userID,
	); err != nil {
		return nil, fmt.Errorf("invalidate MFA recovery codes: %w", err)
	}
	hashes := []string{}
	if err := tx.Select(ctx, &hashes, `
		SELECT credential.token_hash
		  FROM session_credentials credential
		  JOIN sessions session ON session.id = credential.session_id
		 WHERE session.user_id = ?
		   AND session.archived_at IS NULL AND session.revoked_at IS NULL
		   AND credential.kind = 'access'
		   AND credential.archived_at IS NULL AND credential.revoked_at IS NULL
		 FOR UPDATE OF credential`,
		userID,
	); err != nil {
		return nil, fmt.Errorf("select MFA session credentials: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE sessions
		   SET updated_at = GREATEST(updated_at, ?),
		       authentication_strength = 'single_factor',
		       mfa_completed_at = NULL
		 WHERE user_id = ? AND archived_at IS NULL AND revoked_at IS NULL`,
		at, userID,
	); err != nil {
		return nil, fmt.Errorf("downgrade MFA sessions: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit MFA disable: %w", err)
	}
	return &store.MFADisableResult{AccessTokenHashes: hashes}, nil
}

const mfaCredentialSelect = `
	SELECT id, created_at, updated_at, archived_at, user_id, state,
	       encrypted_secret, encryption_key_id, pending_expires_at,
	       activated_at, last_used_time_step
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
	row := newMFACredentialRow(credential)
	if _, err := executor.Exec(ctx, `
		INSERT INTO mfa_credentials (
			id, created_at, updated_at, archived_at, user_id, state,
			encrypted_secret, encryption_key_id, pending_expires_at,
			activated_at, last_used_time_step
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		row.ID, row.CreatedAt, row.UpdatedAt,
		row.ArchivedAt, row.UserID, row.State,
		row.EncryptedSecret, row.EncryptionKeyID,
		row.PendingExpiresAt, row.ActivatedAt,
		row.LastUsedTimeStep,
	); err != nil {
		return translateError("mfa_credential", credential.ID.String(), err)
	}
	return nil
}

func prepareMFARecoveryCodes(
	userID string,
	codes []*model.MFARecoveryCode,
	at time.Time,
) ([]*model.MFARecoveryCode, error) {
	parsedUserID, err := model.ParseUserID(userID)
	if err != nil {
		return nil, store.NewErrInvalidInput("mfa_recovery_code", "user_id", userID)
	}
	prepared := make([]*model.MFARecoveryCode, 0, len(codes))
	seen := make(map[string]struct{}, len(codes))
	for _, code := range codes {
		if code == nil || !code.ID.IsZero() {
			return nil, store.NewErrInvalidInput("mfa_recovery_code", "value", nil)
		}
		candidate := *code
		candidate.UserID = parsedUserID
		candidate.PrepareCreate(model.NewMFARecoveryCodeID(), at)
		if err := candidate.Validate(); err != nil {
			return nil, err
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
			id, created_at, updated_at, archived_at, user_id, code_hash, consumed_at
		) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		code.ID.String(),
		code.CreatedAt,
		code.UpdatedAt,
		NullTimeFromOptional(code.ArchivedAt),
		code.UserID.String(),
		code.CodeHash,
		NullTimeFromOptional(code.ConsumedAt),
	); err != nil {
		return translateError("mfa_recovery_code", code.ID.String(), err)
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
	at := model.TimeFromMillis(now)
	var row sessionRow
	if err := executor.Get(ctx, &row, `
		UPDATE sessions
		   SET updated_at = GREATEST(updated_at, ?),
		       authentication_strength = 'multi_factor',
		       mfa_completed_at = ?
		 WHERE id = ? AND user_id = ? AND archived_at IS NULL AND revoked_at IS NULL
		   AND idle_expires_at > ? AND expires_at > ?
		 RETURNING id, created_at, updated_at, archived_at, user_id, client_type,
		           device_id, device_name, authentication_method,
		           authentication_strength, authenticated_at, mfa_completed_at,
		           last_activity_at, idle_expires_at, expires_at, revoked_at,
		           revocation_reason`,
		at, at, sessionID, userID, at, at,
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
		   AND archived_at IS NULL AND revoked_at IS NULL
		 FOR UPDATE`,
		sessionID,
	); err != nil {
		return nil, fmt.Errorf("select MFA access credential hashes: %w", err)
	}
	return hashes, nil
}

func newMFACredentialRow(credential *model.MFACredential) mfaCredentialRow {
	return mfaCredentialRow{
		ID:               credential.ID.String(),
		CreatedAt:        UTCTime(credential.CreatedAt),
		UpdatedAt:        UTCTime(credential.UpdatedAt),
		ArchivedAt:       NullTimeFromOptional(credential.ArchivedAt),
		UserID:           credential.UserID.String(),
		State:            credential.State,
		EncryptedSecret:  credential.EncryptedSecret,
		EncryptionKeyID:  credential.EncryptionKeyID,
		PendingExpiresAt: NullTimeFromOptional(credential.PendingExpiresAt),
		ActivatedAt:      NullTimeFromOptional(credential.ActivatedAt),
		LastUsedTimeStep: credential.LastUsedTimeStep,
	}
}

func (row mfaCredentialRow) model() (*model.MFACredential, error) {
	credential := &model.MFACredential{
		ID:               model.MFACredentialID(row.ID),
		CreatedAt:        row.CreatedAt.UTC(),
		UpdatedAt:        row.UpdatedAt.UTC(),
		ArchivedAt:       OptionalTimeFromNullTime(row.ArchivedAt),
		UserID:           model.UserID(row.UserID),
		State:            row.State,
		EncryptedSecret:  row.EncryptedSecret,
		EncryptionKeyID:  row.EncryptionKeyID,
		PendingExpiresAt: OptionalTimeFromNullTime(row.PendingExpiresAt),
		ActivatedAt:      OptionalTimeFromNullTime(row.ActivatedAt),
		LastUsedTimeStep: row.LastUsedTimeStep,
	}
	if err := credential.Validate(); err != nil {
		return nil, fmt.Errorf("rehydrate mfa_credential %s: %w", row.ID, err)
	}
	return credential, nil
}

var _ store.MFAStore = (*SqlMFAStore)(nil)
