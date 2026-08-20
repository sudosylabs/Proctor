// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: Apache-2.0
//
// Adapted from Mattermost server/channels/store/sqlstore/session_store.go.
// Proctor retains a dedicated session store, indexed token resolution,
// transactional revocation, user-scoped session queries, and explicit store
// errors while separating bearer credentials from session metadata.

package sqlstore

import (
	"context"
	"database/sql"
	"fmt"
	"time"
	"unicode/utf8"

	sq "github.com/Masterminds/squirrel"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

type SQLSessionStore struct {
	*SQLStore
	sessionsQuery sq.SelectBuilder
}

type sessionRow struct {
	ID                       string         `db:"id"`
	CreatedAt                time.Time      `db:"created_at"`
	UpdatedAt                time.Time      `db:"updated_at"`
	ArchivedAt               sql.NullTime   `db:"archived_at"`
	UserID                   string         `db:"user_id"`
	ClientType               string         `db:"client_type"`
	DeviceID                 string         `db:"device_id"`
	DeviceName               string         `db:"device_name"`
	AuthenticationMethod     string         `db:"authentication_method"`
	AuthenticationProviderID string         `db:"authentication_provider_id"`
	ExternalIdentityID       sql.NullString `db:"external_identity_id"`
	AuthenticationStrength   string         `db:"authentication_strength"`
	AuthenticatedAt          time.Time      `db:"authenticated_at"`
	MFACompletedAt           sql.NullTime   `db:"mfa_completed_at"`
	LastActivityAt           time.Time      `db:"last_activity_at"`
	IdleExpiresAt            time.Time      `db:"idle_expires_at"`
	ExpiresAt                time.Time      `db:"expires_at"`
	RevokedAt                sql.NullTime   `db:"revoked_at"`
	RevocationReason         string         `db:"revocation_reason"`
}

type sessionSaveTransactionResult struct {
	session     *model.Session
	credentials []*model.SessionCredential
}

type userSessionRevocationTransactionResult struct {
	sessions []*model.Session
	hashes   []string
}

func sessionSliceColumns() []string {
	return []string{
		"sessions.id",
		"sessions.created_at",
		"sessions.updated_at",
		"sessions.archived_at",
		"sessions.user_id",
		"sessions.client_type",
		"sessions.device_id",
		"sessions.device_name",
		"sessions.authentication_method",
		"sessions.authentication_provider_id",
		"sessions.external_identity_id",
		"sessions.authentication_strength",
		"sessions.authenticated_at",
		"sessions.mfa_completed_at",
		"sessions.last_activity_at",
		"sessions.idle_expires_at",
		"sessions.expires_at",
		"sessions.revoked_at",
		"sessions.revocation_reason",
	}
}

func newSQLSessionStore(sqlStore *SQLStore) store.SessionStore {
	s := &SQLSessionStore{SQLStore: sqlStore}
	s.sessionsQuery = s.getQueryBuilder().Select(sessionSliceColumns()...).From("sessions")
	return s
}

func (s SQLSessionStore) Save(
	ctx context.Context,
	session *model.Session,
	credentials []*model.SessionCredential,
	maximumActive int,
) (*model.Session, []*model.SessionCredential, error) {
	if session == nil {
		return nil, nil, store.NewErrInvalidInput("session", "value", nil)
	}
	if !session.ID.IsZero() {
		return nil, nil, store.NewErrInvalidInput("session", "id", session.ID.String())
	}
	if maximumActive < 1 {
		return nil, nil, store.NewErrInvalidInput("session", "maximum_active", maximumActive)
	}

	candidate := *session
	at := model.NowUTC()
	candidate.PrepareCreate(model.NewSessionID(), at)
	if err := candidate.Validate(); err != nil {
		return nil, nil, err
	}
	prepared, err := prepareInitialSessionCredentials(&candidate, credentials, at)
	if err != nil {
		return nil, nil, err
	}

	result, err := runSQLTransaction(ctx, s.GetMaster().Begin, "session save", func(ctx context.Context, tx *sqlxTxWrapper) (*sessionSaveTransactionResult, error) {
		if err := requireCurrentAuthenticationMethod(
			ctx, tx, candidate.AuthenticationMethod, candidate.AuthenticationProviderID,
		); err != nil {
			return nil, err
		}
		if err := lockUserSessions(ctx, tx, candidate.UserID.String()); err != nil {
			return nil, err
		}
		if err := requireExactExternalIdentity(ctx, tx, candidate.UserID, candidate.AuthenticationProviderID, candidate.ExternalIdentityID); err != nil {
			return nil, err
		}
		var active int
		if err := tx.Get(ctx, &active, `
		SELECT COUNT(*)
		  FROM sessions
		 WHERE user_id = ?
		   AND archived_at IS NULL
		   AND revoked_at IS NULL
		   AND idle_expires_at > ?
		   AND expires_at > ?`,
			candidate.UserID.String(),
			candidate.CreatedAt,
			candidate.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("count active sessions: %w", err)
		}
		if active >= maximumActive {
			return nil, store.NewErrConflict("session", "sessions_maximum_per_user", nil)
		}

		if err := insertSession(ctx, tx, &candidate); err != nil {
			return nil, err
		}
		for _, credential := range prepared {
			if err := insertSessionCredential(ctx, tx, credential); err != nil {
				return nil, err
			}
		}
		userResult, err := tx.Exec(ctx, `
		UPDATE users
		   SET updated_at = GREATEST(updated_at, ?),
		       last_login_at = GREATEST(last_login_at, ?),
		       last_activity_at = GREATEST(last_activity_at, ?)
		 WHERE id = ? AND archived_at IS NULL AND disabled_at IS NULL`,
			candidate.CreatedAt,
			candidate.CreatedAt,
			candidate.CreatedAt,
			candidate.UserID.String(),
		)
		if err != nil {
			return nil, fmt.Errorf("update user login time: %w", err)
		}
		if err := requireAffected(userResult, "user", candidate.UserID.String()); err != nil {
			return nil, err
		}
		return &sessionSaveTransactionResult{session: &candidate, credentials: prepared}, nil
	})
	if err != nil {
		return nil, nil, err
	}
	return result.session, result.credentials, nil
}

func prepareInitialSessionCredentials(
	session *model.Session,
	credentials []*model.SessionCredential,
	at time.Time,
) ([]*model.SessionCredential, error) {
	if len(credentials) != 2 {
		return nil, store.NewErrInvalidInput("session", "credentials", len(credentials))
	}
	prepared := make([]*model.SessionCredential, 0, 2)
	kinds := map[model.SessionCredentialKind]bool{}
	for _, credential := range credentials {
		if credential == nil {
			return nil, store.NewErrInvalidInput("session_credential", "value", nil)
		}
		if !credential.ID.IsZero() {
			return nil, store.NewErrInvalidInput("session_credential", "id", credential.ID.String())
		}
		candidate := *credential
		candidate.SessionID = session.ID
		candidate.PrepareCreate(model.NewSessionCredentialID(), at)
		if err := candidate.Validate(); err != nil {
			return nil, err
		}
		if candidate.ExpiresAt.After(session.ExpiresAt) {
			return nil, store.NewErrInvalidInput(
				"session_credential",
				"expires_at",
				model.MillisFromTime(candidate.ExpiresAt),
			)
		}
		if kinds[candidate.Kind] {
			return nil, store.NewErrInvalidInput("session_credential", "kind", candidate.Kind)
		}
		kinds[candidate.Kind] = true
		prepared = append(prepared, &candidate)
	}
	if !kinds[model.SessionCredentialAccess] || !kinds[model.SessionCredentialRefresh] {
		return nil, store.NewErrInvalidInput("session", "credentials", "access_and_refresh_required")
	}
	return prepared, nil
}

func insertSession(ctx context.Context, executor sqlxExecutor, session *model.Session) error {
	row := newSessionRow(session)
	if _, err := executor.NamedExec(ctx, `
		INSERT INTO sessions (
			id, created_at, updated_at, archived_at, user_id, client_type,
			device_id, device_name, authentication_method, authentication_provider_id, external_identity_id,
			authentication_strength, authenticated_at, mfa_completed_at,
			last_activity_at, idle_expires_at, expires_at, revoked_at,
			revocation_reason
		) VALUES (
			:id, :created_at, :updated_at, :archived_at, :user_id, :client_type,
			:device_id, :device_name, :authentication_method, :authentication_provider_id, :external_identity_id,
			:authentication_strength, :authenticated_at, :mfa_completed_at,
			:last_activity_at, :idle_expires_at, :expires_at, :revoked_at,
			:revocation_reason
		)`, &row); err != nil {
		return fmt.Errorf("save session: %w", translateError("session", session.ID.String(), err))
	}
	return nil
}

func (s SQLSessionStore) Get(ctx context.Context, id string) (*model.Session, error) {
	var row sessionRow
	query := s.sessionsQuery.Where(sq.Eq{
		"sessions.id":          id,
		"sessions.archived_at": nil,
	})
	if err := s.GetMaster().GetBuilder(ctx, &row, query); err != nil {
		return nil, translateError("session", id, err)
	}
	return row.model()
}

func (s SQLSessionStore) ListByUser(ctx context.Context, userID string) ([]*model.Session, error) {
	query := s.sessionsQuery.
		Where(sq.Eq{
			"sessions.user_id":     userID,
			"sessions.archived_at": nil,
		}).
		OrderBy("sessions.created_at DESC", "sessions.id")
	rows := []sessionRow{}
	if err := s.GetMaster().SelectBuilder(ctx, &rows, query); err != nil {
		return nil, fmt.Errorf("list sessions by user: %w", err)
	}
	sessions := make([]*model.Session, 0, len(rows))
	for _, row := range rows {
		session, err := row.model()
		if err != nil {
			return nil, err
		}
		sessions = append(sessions, session)
	}
	return sessions, nil
}

func (s SQLSessionStore) ListActiveByUser(
	ctx context.Context,
	userID string,
	now int64,
) ([]*model.Session, error) {
	at := model.TimeFromMillis(now)
	query := s.sessionsQuery.
		Where(sq.Eq{
			"sessions.user_id":     userID,
			"sessions.archived_at": nil,
			"sessions.revoked_at":  nil,
		}).
		Where(sq.Gt{"sessions.idle_expires_at": at}).
		Where(sq.Gt{"sessions.expires_at": at}).
		OrderBy(
			"sessions.last_activity_at DESC",
			"sessions.created_at DESC",
			"sessions.id",
		)
	rows := []sessionRow{}
	if err := s.GetMaster().SelectBuilder(ctx, &rows, query); err != nil {
		return nil, fmt.Errorf("list active sessions by user: %w", err)
	}
	sessions := make([]*model.Session, 0, len(rows))
	for _, row := range rows {
		session, err := row.model()
		if err != nil {
			return nil, err
		}
		sessions = append(sessions, session)
	}
	return sessions, nil
}

func (s SQLSessionStore) UpdateActivity(
	ctx context.Context,
	id string,
	lastActivityAt int64,
	idleExpiresAt int64,
) error {
	activityAt := model.TimeFromMillis(lastActivityAt)
	idleAt := model.TimeFromMillis(idleExpiresAt)
	result, err := s.GetMaster().Exec(ctx, `
		UPDATE sessions
		   SET updated_at = GREATEST(updated_at, ?),
		       last_activity_at = ?,
		       idle_expires_at = LEAST(?, expires_at)
		 WHERE id = ?
		   AND archived_at IS NULL
		   AND revoked_at IS NULL
		   AND idle_expires_at > ?
		   AND expires_at > ?`,
		activityAt,
		activityAt,
		idleAt,
		id,
		activityAt,
		activityAt,
	)
	if err != nil {
		return fmt.Errorf("update session activity: %w", err)
	}
	return requireAffected(result, "session", id)
}

func (s SQLSessionStore) Revoke(
	ctx context.Context,
	id string,
	userID string,
	revokedAt int64,
	reason string,
) ([]string, error) {
	return runSQLTransaction(ctx, s.GetMaster().Begin, "session revocation", func(ctx context.Context, tx *sqlxTxWrapper) ([]string, error) {
		if err := lockUserSessions(ctx, tx, userID); err != nil {
			return nil, err
		}
		return revokeOneUserSession(ctx, tx, id, userID, revokedAt, reason)
	})
}

func (s SQLSessionStore) RevokeWithAudit(
	ctx context.Context,
	input *store.SessionRevocation,
) (*store.SessionRevocationResult, error) {
	if input == nil || !model.IsValidId(input.SessionID) || !model.IsValidId(input.UserID) ||
		input.RevokedAt <= 0 || !model.IsValidId(input.AuditEventID) || input.AuditAt <= 0 {
		return nil, store.NewErrInvalidInput("session", "revocation", nil)
	}
	reason := model.SanitizeUnicode(input.Reason)
	if utf8.RuneCountInString(reason) > model.SessionRevocationMaxRunes {
		return nil, store.NewErrInvalidInput("session", "revocation_reason", nil)
	}
	payloadKeyID, err := validateSecurityNoticeMail(model.UserID(input.UserID), input.Occurrence, input.Delivery, input.DeliveryJob, model.MailTemplateIdentitySessionsRevokedByAdmin, input.RevokedAt)
	if err != nil {
		return nil, err
	}
	return runSQLTransaction(ctx, s.GetMaster().Begin, "audited session revocation", func(ctx context.Context, tx *sqlxTxWrapper) (*store.SessionRevocationResult, error) {
		if payloadKeyID != "" {
			if err := requireMailPayloadPrimary(ctx, tx, payloadKeyID); err != nil {
				return nil, err
			}
		}
		if err := lockUserSessions(ctx, tx, input.UserID); err != nil {
			return nil, err
		}
		var row sessionRow
		if err := tx.Get(ctx, &row, `
		SELECT id, created_at, updated_at, archived_at, user_id, client_type,
		       device_id, device_name, authentication_method, authentication_provider_id, external_identity_id,
		       authentication_strength, authenticated_at, mfa_completed_at,
		       last_activity_at, idle_expires_at, expires_at, revoked_at,
		       revocation_reason
		  FROM sessions
		 WHERE id = ? AND user_id = ? AND archived_at IS NULL AND revoked_at IS NULL
		 FOR UPDATE`,
			input.SessionID,
			input.UserID,
		); err != nil {
			return nil, translateError("session", input.SessionID, err)
		}
		hashes, err := revokeOneUserSession(
			ctx, tx, input.SessionID, input.UserID, input.RevokedAt, reason,
		)
		if err != nil {
			return nil, err
		}
		session, err := row.model()
		if err != nil {
			return nil, err
		}
		revokedAt := model.TimeFromMillis(input.RevokedAt)
		if session.UpdatedAt.Before(revokedAt) {
			session.UpdatedAt = revokedAt
		}
		session.RevokedAt = model.OptionalTimeFrom(revokedAt)
		session.RevocationReason = reason
		if err := insertSecurityNoticeMail(ctx, tx, input.Occurrence, input.Delivery, input.DeliveryJob, payloadKeyID); err != nil {
			return nil, err
		}
		encoded, appErr := model.EncodeAuditData(session.Auditable())
		if appErr != nil {
			return nil, appErr
		}
		if _, err := completeAuditEvent(
			ctx, tx, input.AuditEventID, model.AuditStatusSuccess, "", encoded, input.AuditAt,
		); err != nil {
			return nil, fmt.Errorf("complete session revocation audit: %w", err)
		}
		return &store.SessionRevocationResult{Session: session, TokenHashes: hashes}, nil
	})
}

func (s SQLSessionStore) RevokeAllForUser(
	ctx context.Context,
	userID string,
	revokedAt int64,
	reason string,
) ([]*model.Session, []string, error) {
	result, err := runSQLTransaction(ctx, s.GetMaster().Begin, "user session revocation", func(ctx context.Context, tx *sqlxTxWrapper) (*userSessionRevocationTransactionResult, error) {
		if err := lockUserSessions(ctx, tx, userID); err != nil {
			return nil, err
		}
		rows, hashes, err := revokeAllUserSessions(
			ctx, tx, userID, revokedAt, reason,
		)
		if err != nil {
			return nil, err
		}
		sessions, err := revokedSessionModels(rows, revokedAt, reason)
		if err != nil {
			return nil, err
		}
		return &userSessionRevocationTransactionResult{sessions: sessions, hashes: hashes}, nil
	})
	if err != nil {
		return nil, nil, err
	}
	return result.sessions, result.hashes, nil
}

func (s SQLSessionStore) RevokeAllForUserWithAudit(
	ctx context.Context,
	input *store.UserSessionsRevocation,
) (*store.UserSessionsRevocationResult, error) {
	if input == nil || !model.IsValidId(input.UserID) || input.RevokedAt <= 0 ||
		!model.IsValidId(input.AuditEventID) || input.AuditAt <= 0 {
		return nil, store.NewErrInvalidInput("session", "user_revocation", nil)
	}
	reason := model.SanitizeUnicode(input.Reason)
	if utf8.RuneCountInString(reason) > model.SessionRevocationMaxRunes {
		return nil, store.NewErrInvalidInput("session", "revocation_reason", nil)
	}
	mailUnprepared := input.Occurrence == nil && input.Delivery == nil && input.DeliveryJob == nil
	if mailUnprepared && input.Command == nil {
		return nil, store.NewErrInvalidInput("session", "user_revocation_notice", nil)
	}
	payloadKeyID := ""
	if !mailUnprepared {
		var err error
		payloadKeyID, err = validateSecurityNoticeMail(model.UserID(input.UserID), input.Occurrence, input.Delivery, input.DeliveryJob, model.MailTemplateIdentitySessionsRevokedByAdmin, input.RevokedAt)
		if err != nil {
			return nil, err
		}
	}
	execute := func(ctx context.Context, tx *sqlxTxWrapper) (*userSessionsMutationResult, error) {
		if payloadKeyID != "" {
			if err := requireMailPayloadPrimary(ctx, tx, payloadKeyID); err != nil {
				return nil, err
			}
		}
		if err := lockUserSessions(ctx, tx, input.UserID); err != nil {
			return nil, err
		}
		rows, hashes, err := revokeAllUserSessions(
			ctx, tx, input.UserID, input.RevokedAt, reason,
		)
		if err != nil {
			return nil, err
		}
		sessions, err := revokedSessionModels(rows, input.RevokedAt, reason)
		if err != nil {
			return nil, err
		}
		if len(sessions) > 0 {
			if mailUnprepared {
				return nil, store.NewErrInvalidInput("session", "user_revocation_notice", nil)
			}
			if err := insertSecurityNoticeMail(ctx, tx, input.Occurrence, input.Delivery, input.DeliveryJob, payloadKeyID); err != nil {
				return nil, err
			}
		}
		encoded, appErr := model.EncodeAuditData(map[string]any{
			"user_id":               input.UserID,
			"revoked_session_count": len(sessions),
		})
		if appErr != nil {
			return nil, appErr
		}
		if _, err := completeAuditEvent(
			ctx, tx, input.AuditEventID, model.AuditStatusSuccess, "", encoded, input.AuditAt,
		); err != nil {
			return nil, fmt.Errorf("complete user session revocation audit: %w", err)
		}
		return &userSessionsMutationResult{Value: &store.UserSessionsRevocationResult{Sessions: sessions, TokenHashes: hashes}, NoOp: len(sessions) == 0}, nil
	}
	if input.Command == nil {
		result, err := runSQLTransaction(ctx, s.GetMaster().Begin, "audited user session revocation", execute)
		if err != nil {
			return nil, err
		}
		input.NoOp = result.NoOp
		return result.Value, nil
	}
	result, err := runIdempotentMutation(ctx, s.SQLStore, "idempotent user session revocation", idempotentMutation[*userSessionsMutationResult]{
		command: input.Command, auditEventID: input.AuditEventID, execute: execute,
		encode: func(value *userSessionsMutationResult) ([]byte, error) {
			return encodeCommandOutcome(userSessionsCommandOutcome{UserID: input.UserID, NoOp: value.NoOp})
		},
		decode: func(version int, data []byte) (*userSessionsMutationResult, error) {
			if version != 1 {
				return nil, fmt.Errorf("unsupported user sessions outcome version %d", version)
			}
			var outcome userSessionsCommandOutcome
			if decodeErr := decodeCommandOutcome(data, &outcome); decodeErr != nil {
				return nil, decodeErr
			}
			if !model.IsValidId(outcome.UserID) {
				return nil, invalidPersistedState("command_outcome", "user_id", nil)
			}
			return &userSessionsMutationResult{Value: &store.UserSessionsRevocationResult{Sessions: []*model.Session{}, TokenHashes: []string{}}, NoOp: outcome.NoOp, UserID: outcome.UserID}, nil
		},
		onboardingOutcome: func(value *userSessionsMutationResult) (onboardingImportCommandResult, error) {
			return administrativeOnboardingOutcome(input.UserID, value.NoOp)
		},
		completeReplay: func(ctx context.Context, tx *sqlxTxWrapper, value *userSessionsMutationResult, original string) error {
			return completeAdministrativeReplayAudit(ctx, tx, input.AuditEventID, input.AuditAt, "user_id", value.UserID, value.NoOp, original)
		},
	})
	if err != nil {
		return nil, err
	}
	input.Replayed, input.NoOp = result.Replayed, result.Value.NoOp
	return result.Value.Value, nil
}

type userSessionsMutationResult struct {
	Value  *store.UserSessionsRevocationResult
	UserID string
	NoOp   bool
}

type userSessionsCommandOutcome struct {
	UserID string `json:"user_id"`
	NoOp   bool   `json:"no_op,omitempty"`
}

func revokeOneUserSession(
	ctx context.Context,
	tx *sqlxTxWrapper,
	id string,
	userID string,
	revokedAt int64,
	reason string,
) ([]string, error) {
	at := model.TimeFromMillis(revokedAt)
	var matchedSessionID string
	if err := tx.Get(ctx, &matchedSessionID, `
		SELECT id
		  FROM sessions
		 WHERE id = ? AND user_id = ? AND archived_at IS NULL AND revoked_at IS NULL`,
		id,
		userID,
	); err != nil {
		return nil, translateError("session", id, err)
	}
	hashes, err := selectActiveTokenHashes(ctx, tx, id)
	if err != nil {
		return nil, err
	}
	result, err := tx.Exec(ctx, `
		UPDATE sessions
		   SET updated_at = GREATEST(updated_at, ?),
		       revoked_at = ?,
		       revocation_reason = ?
		 WHERE id = ? AND user_id = ? AND archived_at IS NULL AND revoked_at IS NULL`,
		at,
		at,
		model.SanitizeUnicode(reason),
		id,
		userID,
	)
	if err != nil {
		return nil, fmt.Errorf("revoke session: %w", err)
	}
	if err := requireAffected(result, "session", id); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, `
		UPDATE session_credentials
		   SET updated_at = GREATEST(updated_at, ?), revoked_at = ?
		 WHERE session_id = ? AND archived_at IS NULL AND revoked_at IS NULL`,
		at,
		at,
		id,
	); err != nil {
		return nil, fmt.Errorf("revoke session credentials: %w", err)
	}
	return hashes, nil
}

func revokeAllUserSessions(
	ctx context.Context,
	executor sqlxExecutor,
	userID string,
	revokedAt int64,
	reason string,
) ([]sessionRow, []string, error) {
	return revokeAllUserSessionsAt(ctx, executor, userID, model.TimeFromMillis(revokedAt), reason)
}

func revokeAllUserSessionsAt(
	ctx context.Context,
	executor sqlxExecutor,
	userID string,
	at time.Time,
	reason string,
) ([]sessionRow, []string, error) {
	at = model.TimeUTC(at)
	hashes := []string{}
	if err := executor.Select(ctx, &hashes, `
		SELECT credential.token_hash
		  FROM session_credentials credential
		  JOIN sessions session ON session.id = credential.session_id
		 WHERE session.user_id = ?
		   AND session.archived_at IS NULL
		   AND session.revoked_at IS NULL
		   AND credential.archived_at IS NULL
		   AND credential.revoked_at IS NULL
		 FOR UPDATE OF credential`,
		userID,
	); err != nil {
		return nil, nil, fmt.Errorf("select user session credentials: %w", err)
	}
	rows := []sessionRow{}
	if err := executor.Select(ctx, &rows, `
		SELECT id, created_at, updated_at, archived_at, user_id, client_type,
		       device_id, device_name, authentication_method, authentication_provider_id, external_identity_id,
		       authentication_strength, authenticated_at, mfa_completed_at,
		       last_activity_at, idle_expires_at, expires_at, revoked_at,
		       revocation_reason
		  FROM sessions
		 WHERE user_id = ? AND archived_at IS NULL AND revoked_at IS NULL
		 FOR UPDATE`,
		userID,
	); err != nil {
		return nil, nil, fmt.Errorf("select user sessions: %w", err)
	}
	if _, err := executor.Exec(ctx, `
		UPDATE session_credentials credential
		   SET updated_at = GREATEST(credential.updated_at, ?), revoked_at = ?
		  FROM sessions session
		 WHERE session.id = credential.session_id
		   AND session.user_id = ?
		   AND session.archived_at IS NULL
		   AND session.revoked_at IS NULL
		   AND credential.archived_at IS NULL
		   AND credential.revoked_at IS NULL`,
		at,
		at,
		userID,
	); err != nil {
		return nil, nil, fmt.Errorf("revoke user session credentials: %w", err)
	}
	if _, err := executor.Exec(ctx, `
		UPDATE sessions
		   SET updated_at = GREATEST(updated_at, ?),
		       revoked_at = ?,
		       revocation_reason = ?
		 WHERE user_id = ? AND archived_at IS NULL AND revoked_at IS NULL`,
		at,
		at,
		model.SanitizeUnicode(reason),
		userID,
	); err != nil {
		return nil, nil, fmt.Errorf("revoke user sessions: %w", err)
	}
	return rows, hashes, nil
}

func revokedSessionModels(
	rows []sessionRow,
	revokedAt int64,
	reason string,
) ([]*model.Session, error) {
	return revokedSessionModelsAt(rows, model.TimeFromMillis(revokedAt), reason)
}

func revokedSessionModelsAt(
	rows []sessionRow,
	at time.Time,
	reason string,
) ([]*model.Session, error) {
	sessions := make([]*model.Session, 0, len(rows))
	at = model.TimeUTC(at)
	for _, row := range rows {
		session, err := row.model()
		if err != nil {
			return nil, err
		}
		if session.UpdatedAt.Before(at) {
			session.UpdatedAt = at
		}
		session.RevokedAt = model.OptionalTimeFrom(at)
		session.RevocationReason = model.SanitizeUnicode(reason)
		sessions = append(sessions, session)
	}
	return sessions, nil
}

func lockUserSessions(
	ctx context.Context,
	executor sqlxExecutor,
	userID string,
) error {
	lockKey := "proctor:session-user:" + userID
	if _, err := executor.Exec(
		ctx,
		"SELECT pg_advisory_xact_lock(hashtextextended(?, 0))",
		lockKey,
	); err != nil {
		return fmt.Errorf("lock user sessions: %w", err)
	}
	return nil
}

func selectActiveTokenHashes(
	ctx context.Context,
	executor sqlxExecutor,
	sessionID string,
) ([]string, error) {
	hashes := []string{}
	if err := executor.Select(ctx, &hashes, `
		SELECT token_hash
		  FROM session_credentials
		 WHERE session_id = ? AND archived_at IS NULL AND revoked_at IS NULL
		 FOR UPDATE`,
		sessionID,
	); err != nil {
		return nil, fmt.Errorf("select session credential hashes: %w", err)
	}
	return hashes, nil
}

func newSessionRow(session *model.Session) sessionRow {
	return sessionRow{
		ID:                       session.ID.String(),
		CreatedAt:                UTCTime(session.CreatedAt),
		UpdatedAt:                UTCTime(session.UpdatedAt),
		ArchivedAt:               NullTimeFromOptional(session.ArchivedAt),
		UserID:                   session.UserID.String(),
		ClientType:               string(session.ClientType),
		DeviceID:                 session.DeviceID,
		DeviceName:               session.DeviceName,
		AuthenticationMethod:     session.AuthenticationMethod,
		AuthenticationProviderID: session.AuthenticationProviderID,
		ExternalIdentityID:       sql.NullString{String: session.ExternalIdentityID.String(), Valid: !session.ExternalIdentityID.IsZero()},
		AuthenticationStrength:   string(session.AuthenticationStrength),
		AuthenticatedAt:          UTCTime(session.AuthenticatedAt),
		MFACompletedAt:           NullTimeFromOptional(session.MFACompletedAt),
		LastActivityAt:           UTCTime(session.LastActivityAt),
		IdleExpiresAt:            UTCTime(session.IdleExpiresAt),
		ExpiresAt:                UTCTime(session.ExpiresAt),
		RevokedAt:                NullTimeFromOptional(session.RevokedAt),
		RevocationReason:         session.RevocationReason,
	}
}

func (row sessionRow) model() (*model.Session, error) {
	id, err := parsePersistedID("session", "id", row.ID, model.ParseSessionID)
	if err != nil {
		return nil, err
	}
	userID, err := parsePersistedID("session", "user_id", row.UserID, model.ParseUserID)
	if err != nil {
		return nil, err
	}
	session := &model.Session{
		ID:                       id,
		CreatedAt:                row.CreatedAt.UTC(),
		UpdatedAt:                row.UpdatedAt.UTC(),
		ArchivedAt:               OptionalTimeFromNullTime(row.ArchivedAt),
		UserID:                   userID,
		ClientType:               model.SessionClientType(row.ClientType),
		DeviceID:                 row.DeviceID,
		DeviceName:               row.DeviceName,
		AuthenticationMethod:     row.AuthenticationMethod,
		AuthenticationProviderID: row.AuthenticationProviderID,
		AuthenticationStrength:   model.AuthenticationStrength(row.AuthenticationStrength),
		AuthenticatedAt:          row.AuthenticatedAt.UTC(),
		MFACompletedAt:           OptionalTimeFromNullTime(row.MFACompletedAt),
		LastActivityAt:           row.LastActivityAt.UTC(),
		IdleExpiresAt:            row.IdleExpiresAt.UTC(),
		ExpiresAt:                row.ExpiresAt.UTC(),
		RevokedAt:                OptionalTimeFromNullTime(row.RevokedAt),
		RevocationReason:         row.RevocationReason,
	}
	if row.ExternalIdentityID.Valid {
		session.ExternalIdentityID, err = model.ParseExternalIdentityID(row.ExternalIdentityID.String)
		if err != nil {
			return nil, store.NewErrInvalidInput("session", "external_identity_id", row.ExternalIdentityID.String)
		}
	}
	if err := validatePersistedModel("session", session); err != nil {
		return nil, err
	}
	return session, nil
}

var _ store.SessionStore = (*SQLSessionStore)(nil)
