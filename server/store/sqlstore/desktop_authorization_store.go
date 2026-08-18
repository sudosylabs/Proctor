// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package sqlstore

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

type SQLDesktopAuthorizationStore struct{ *SQLStore }

type desktopAuthorizationRow struct {
	ID                           string         `db:"id"`
	CreatedAt                    time.Time      `db:"created_at"`
	UpdatedAt                    time.Time      `db:"updated_at"`
	Purpose                      string         `db:"purpose"`
	State                        string         `db:"state"`
	InstitutionID                string         `db:"institution_id"`
	Issuer                       string         `db:"issuer"`
	HandleHash                   sql.NullString `db:"handle_hash"`
	BrowserProofHash             sql.NullString `db:"browser_proof_hash"`
	StateHash                    sql.NullString `db:"state_hash"`
	CallbackURL                  sql.NullString `db:"callback_url"`
	CodeChallenge                sql.NullString `db:"code_challenge"`
	ExpectedAuthenticationMethod string         `db:"expected_authentication_method"`
	ExpectedProviderID           sql.NullString `db:"expected_provider_id"`
	ClientType                   string         `db:"client_type"`
	DeviceID                     string         `db:"device_id"`
	DeviceName                   string         `db:"device_name"`
	ExpiresAt                    time.Time      `db:"expires_at"`
	UserID                       sql.NullString `db:"user_id"`
	AuthenticationMethod         sql.NullString `db:"authentication_method"`
	AuthenticationProviderID     sql.NullString `db:"authentication_provider_id"`
	AuthenticationStrength       sql.NullString `db:"authentication_strength"`
	AuthenticatedAt              sql.NullTime   `db:"authenticated_at"`
	MFACompletedAt               sql.NullTime   `db:"mfa_completed_at"`
	CodeHash                     sql.NullString `db:"code_hash"`
	CodeExpiresAt                sql.NullTime   `db:"code_expires_at"`
	CancelledAt                  sql.NullTime   `db:"cancelled_at"`
	ExchangedAt                  sql.NullTime   `db:"exchanged_at"`
	ExpiredAt                    sql.NullTime   `db:"expired_at"`
}

const desktopAuthorizationColumns = `id, created_at, updated_at, purpose, state, institution_id, issuer,
handle_hash, browser_proof_hash, state_hash, callback_url, code_challenge,
expected_authentication_method, expected_provider_id, client_type, device_id, device_name, expires_at,
user_id, authentication_method, authentication_provider_id, authentication_strength, authenticated_at,
mfa_completed_at, code_hash, code_expires_at, cancelled_at, exchanged_at, expired_at`

func newSQLDesktopAuthorizationStore(sqlStore *SQLStore) store.DesktopAuthorizationStore {
	return &SQLDesktopAuthorizationStore{SQLStore: sqlStore}
}

func (s SQLDesktopAuthorizationStore) Create(ctx context.Context, transaction *model.BrowserAuthenticationTransaction) (*model.BrowserAuthenticationTransaction, error) {
	if transaction == nil || !transaction.ID.IsValid() || transaction.Validate() != nil ||
		transaction.State != model.BrowserAuthenticationStatePending {
		return nil, store.NewErrInvalidInput("browser_authentication_transaction", "value", nil)
	}
	lifetime := transaction.ExpiresAt.Sub(transaction.CreatedAt)
	if lifetime <= 0 || lifetime > model.BrowserAuthenticationTransactionLifetime {
		return nil, store.NewErrInvalidInput("browser_authentication_transaction", "lifetime", nil)
	}
	candidate := *transaction
	return runSQLTransaction(ctx, s.GetMaster().Begin, "create browser authentication transaction", func(ctx context.Context, tx *sqlxTxWrapper) (*model.BrowserAuthenticationTransaction, error) {
		var now time.Time
		if err := tx.Get(ctx, &now, `SELECT clock_timestamp()`); err != nil {
			return nil, fmt.Errorf("read browser authentication creation time: %w", err)
		}
		candidate.ExpiresAt = model.TimeUTC(now).Add(lifetime)
		candidate.PrepareCreate(candidate.ID, now)
		_, err := tx.Exec(ctx, `INSERT INTO browser_authentication_transactions (
			id, created_at, updated_at, purpose, state, institution_id, issuer, handle_hash, browser_proof_hash,
			state_hash, callback_url, code_challenge, expected_authentication_method, expected_provider_id,
			client_type, device_id, device_name, expires_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, NULLIF(?, ''), ?, ?, ?, ?)`,
			candidate.ID.String(), candidate.CreatedAt, candidate.UpdatedAt, candidate.Purpose, candidate.State,
			candidate.InstitutionID.String(), candidate.Issuer, candidate.HandleHash, candidate.BrowserProofHash,
			candidate.StateHash, candidate.CallbackURL, candidate.CodeChallenge, candidate.ExpectedAuthenticationMethod,
			candidate.ExpectedProviderID, candidate.ClientType, candidate.DeviceID, candidate.DeviceName, candidate.ExpiresAt)
		if err != nil {
			return nil, translateError("browser_authentication_transaction", candidate.ID.String(), err)
		}
		var row desktopAuthorizationRow
		if err = tx.Get(ctx, &row, `SELECT `+desktopAuthorizationColumns+` FROM browser_authentication_transactions WHERE id = ?`, candidate.ID.String()); err != nil {
			return nil, translateError("browser_authentication_transaction", candidate.ID.String(), err)
		}
		return row.model()
	})
}

func (s SQLDesktopAuthorizationStore) IssueCode(ctx context.Context, input *store.DesktopAuthorizationCodeIssue) (*model.BrowserAuthenticationTransaction, error) {
	if err := validateDesktopAuthorizationCodeIssue(input); err != nil {
		return nil, err
	}
	return runSQLTransaction(ctx, s.GetMaster().Begin, "issue desktop authorization code", func(ctx context.Context, tx *sqlxTxWrapper) (*model.BrowserAuthenticationTransaction, error) {
		// Match User disablement's global-admin then per-User session lock order.
		// Whichever mutation commits first is therefore authoritative: a later
		// IssueCode observes the disabled User, while a later disablement follows
		// and invalidates the already-issued code path before exchange.
		if err := lockSystemAdministratorAuthenticationPaths(ctx, tx); err != nil {
			return nil, err
		}
		if err := requireDesktopAuthenticationPath(ctx, tx, input.AuthenticationMethod, input.AuthenticationProviderID, input.Capabilities); err != nil {
			return nil, err
		}
		if err := lockUserSessions(ctx, tx, input.UserID.String()); err != nil {
			return nil, err
		}
		var activeUserID string
		if err := tx.Get(ctx, &activeUserID, `SELECT id FROM users
			WHERE id = ? AND archived_at IS NULL AND disabled_at IS NULL FOR UPDATE`, input.UserID.String()); err != nil {
			return nil, translateError("user", input.UserID.String(), err)
		}
		var row desktopAuthorizationRow
		err := tx.Get(ctx, &row, `UPDATE browser_authentication_transactions
		   SET updated_at = terminal.at, state = 'code_issued', handle_hash = NULL, browser_proof_hash = NULL,
		       user_id = ?, authentication_method = ?, authentication_provider_id = NULLIF(?, ''),
		       authentication_strength = ?, authenticated_at = ?, mfa_completed_at = ?, code_hash = ?,
		       code_expires_at = LEAST(terminal.at + (? * interval '1 millisecond'), expires_at)
		  FROM (SELECT clock_timestamp() AS at) AS terminal
		 WHERE handle_hash = ? AND browser_proof_hash = ? AND state_hash = ? AND state = 'pending'
		   AND purpose = 'desktop_authorization' AND client_type = 'desktop'
		   AND expected_authentication_method = ? AND expected_provider_id IS NOT DISTINCT FROM NULLIF(?, '')
		   AND created_at <= terminal.at AND expires_at > terminal.at
		 RETURNING `+desktopAuthorizationColumns,
			input.UserID.String(), input.AuthenticationMethod, input.AuthenticationProviderID,
			input.AuthenticationStrength, model.TimeFromMillis(input.AuthenticatedAt), NullTimeFromOptional(model.OptionalTimeFromMillis(input.MFACompletedAt)), input.CodeHash,
			input.CodeLifetime.Milliseconds(), input.HandleHash, input.BrowserProofHash, input.StateHash,
			input.AuthenticationMethod, input.AuthenticationProviderID)
		if err != nil {
			return nil, translateError("browser_authentication_transaction", "", err)
		}
		value, err := row.model()
		if err != nil {
			return nil, err
		}
		encoded, err := model.EncodeAuditData(value.Auditable())
		if err != nil {
			return nil, err
		}
		if _, err = completeAuditEvent(ctx, tx, input.AuditEventID, model.AuditStatusSuccess, "", encoded, input.AuditAt); err != nil {
			return nil, err
		}
		return value, nil
	})
}

func (s SQLDesktopAuthorizationStore) Cancel(ctx context.Context, input *store.DesktopAuthorizationCancellation) (*model.BrowserAuthenticationTransaction, error) {
	if input == nil || !model.IsValidTokenHash(input.HandleHash) || !model.IsValidTokenHash(input.BrowserProofHash) ||
		!model.IsValidTokenHash(input.StateHash) || input.CancelledAt <= 0 {
		return nil, store.NewErrInvalidInput("browser_authentication_transaction", "cancellation", nil)
	}
	return runSQLTransaction(ctx, s.GetMaster().Begin, "cancel desktop authorization", func(ctx context.Context, tx *sqlxTxWrapper) (*model.BrowserAuthenticationTransaction, error) {
		var row desktopAuthorizationRow
		err := tx.Get(ctx, &row, `UPDATE browser_authentication_transactions
	   SET updated_at = terminal.at, state = 'cancelled', handle_hash = NULL, browser_proof_hash = NULL,
	       state_hash = NULL, callback_url = NULL, code_challenge = NULL, cancelled_at = terminal.at
	  FROM (SELECT clock_timestamp() AS at) AS terminal
	 WHERE handle_hash = ? AND browser_proof_hash = ? AND state_hash = ? AND state = 'pending'
	   AND created_at <= clock_timestamp() AND expires_at > clock_timestamp()
	 RETURNING `+desktopAuthorizationColumns, input.HandleHash, input.BrowserProofHash, input.StateHash)
		if err != nil {
			return nil, translateError("browser_authentication_transaction", "", err)
		}
		return row.model()
	})
}

func (s SQLDesktopAuthorizationStore) Exchange(ctx context.Context, input *store.DesktopAuthorizationExchange) (*store.DesktopAuthorizationExchangeResult, error) {
	if err := validateDesktopAuthorizationExchange(input); err != nil {
		return nil, err
	}
	return runSQLTransaction(ctx, s.GetMaster().Begin, "exchange desktop authorization code", func(ctx context.Context, tx *sqlxTxWrapper) (*store.DesktopAuthorizationExchangeResult, error) {
		var row desktopAuthorizationRow
		if err := tx.Get(ctx, &row, `SELECT `+desktopAuthorizationColumns+` FROM browser_authentication_transactions
		 WHERE code_hash = ? AND state_hash = ? AND code_challenge = ? AND issuer = ?
		   AND state = 'code_issued' AND created_at <= clock_timestamp() AND expires_at > clock_timestamp()
		   AND code_expires_at > clock_timestamp() FOR UPDATE`, input.CodeHash, input.StateHash,
			input.CodeChallenge, input.Issuer); err != nil {
			return nil, translateError("browser_authentication_transaction", "", err)
		}
		transaction, err := row.model()
		if err != nil {
			return nil, err
		}
		if err = requireDesktopAuthenticationPath(ctx, tx, transaction.AuthenticationMethod, transaction.AuthenticationProviderID, input.Capabilities); err != nil {
			return nil, err
		}
		// Match every user-session mutation's lock order. If disable commits
		// first the active-user lock below rejects exchange; if exchange commits
		// first disable observes and revokes the new Session.
		if err = lockUserSessions(ctx, tx, transaction.UserID.String()); err != nil {
			return nil, err
		}
		var activeUserID string
		if err = tx.Get(ctx, &activeUserID, `SELECT id FROM users
			WHERE id = ? AND archived_at IS NULL AND disabled_at IS NULL FOR UPDATE`, transaction.UserID.String()); err != nil {
			return nil, translateError("user", transaction.UserID.String(), err)
		}
		var now time.Time
		if err = tx.Get(ctx, &now, "SELECT clock_timestamp()"); err != nil {
			return nil, fmt.Errorf("read desktop exchange time: %w", err)
		}
		now = model.TimeUTC(now)
		candidate := model.Session{
			UserID: transaction.UserID, ClientType: model.SessionClientDesktop,
			DeviceID: transaction.DeviceID, DeviceName: transaction.DeviceName,
			AuthenticationMethod: transaction.AuthenticationMethod, AuthenticationProviderID: transaction.AuthenticationProviderID,
			AuthenticationStrength: transaction.AuthenticationStrength, AuthenticatedAt: transaction.AuthenticatedAt.Time,
			MFACompletedAt: transaction.MFACompletedAt, LastActivityAt: now,
			IdleExpiresAt: now.Add(input.IdleLifetime), ExpiresAt: now.Add(input.AbsoluteLifetime),
		}
		candidate.PrepareCreate(model.NewSessionID(), now)
		if err = candidate.Validate(); err != nil {
			return nil, err
		}
		credentials, err := prepareInitialSessionCredentials(&candidate, []*model.SessionCredential{
			{Kind: model.SessionCredentialAccess, TokenHash: input.AccessTokenHash, ExpiresAt: now.Add(input.AccessLifetime)},
			{Kind: model.SessionCredentialRefresh, TokenHash: input.RefreshTokenHash, ExpiresAt: now.Add(input.RefreshLifetime)},
		}, now)
		if err != nil {
			return nil, err
		}
		var active int
		if err = tx.Get(ctx, &active, `SELECT COUNT(*) FROM sessions WHERE user_id = ? AND archived_at IS NULL
			AND revoked_at IS NULL AND idle_expires_at > clock_timestamp() AND expires_at > clock_timestamp()`, candidate.UserID.String()); err != nil {
			return nil, fmt.Errorf("count desktop sessions: %w", err)
		}
		if active >= input.MaximumActive {
			return nil, store.NewErrConflict("session", "sessions_maximum_per_user", nil)
		}
		if err = insertSession(ctx, tx, &candidate); err != nil {
			return nil, err
		}
		for _, credential := range credentials {
			if err = insertSessionCredential(ctx, tx, credential); err != nil {
				return nil, err
			}
		}
		userUpdate, err := tx.Exec(ctx, `UPDATE users SET updated_at = GREATEST(updated_at, ?),
			last_login_at = GREATEST(last_login_at, ?), last_activity_at = GREATEST(last_activity_at, ?)
			WHERE id = ? AND archived_at IS NULL AND disabled_at IS NULL`, now, now, now, candidate.UserID.String())
		if err != nil {
			return nil, fmt.Errorf("update desktop user login time: %w", err)
		}
		if affected, affectedErr := userUpdate.RowsAffected(); affectedErr != nil || affected != 1 {
			if affectedErr != nil {
				return nil, fmt.Errorf("read desktop user login update: %w", affectedErr)
			}
			return nil, store.NewErrNotFound("user", candidate.UserID.String())
		}
		var terminal desktopAuthorizationRow
		if err = tx.Get(ctx, &terminal, `UPDATE browser_authentication_transactions
			SET updated_at = ?, state = 'exchanged', state_hash = NULL, callback_url = NULL,
			    code_challenge = NULL, code_hash = NULL, code_expires_at = NULL, exchanged_at = ?
			WHERE id = ? AND state = 'code_issued' RETURNING `+desktopAuthorizationColumns,
			now, now, transaction.ID.String()); err != nil {
			return nil, translateError("browser_authentication_transaction", transaction.ID.String(), err)
		}
		terminalTransaction, err := terminal.model()
		if err != nil {
			return nil, err
		}
		encoded, err := model.EncodeAuditData(terminalTransaction.Auditable())
		if err != nil {
			return nil, err
		}
		if _, err = completeAuditEvent(ctx, tx, input.AuditEventID, model.AuditStatusSuccess, "", encoded, input.AuditAt); err != nil {
			return nil, err
		}
		return &store.DesktopAuthorizationExchangeResult{Transaction: terminalTransaction, Session: &candidate, Credentials: credentials}, nil
	})
}

func requireDesktopAuthenticationPath(ctx context.Context, executor sqlxExecutor, method, providerID string, capabilities store.AccessDeploymentCapabilities) error {
	if !validAccessDeploymentCapabilities(capabilities) {
		return store.NewErrInvalidInput("browser_authentication_transaction", "capabilities", nil)
	}
	if providerID != "" {
		if _, available := capabilities.Providers[providerID]; !available {
			return store.ErrAuthenticationMethodDisabled
		}
	}
	return requireCurrentAuthenticationMethod(ctx, executor, method, providerID)
}

func validateDesktopAuthorizationCodeIssue(input *store.DesktopAuthorizationCodeIssue) error {
	if input == nil || !model.IsValidTokenHash(input.HandleHash) || !model.IsValidTokenHash(input.BrowserProofHash) ||
		!model.IsValidTokenHash(input.StateHash) || !input.UserID.IsValid() || !input.AuthenticationStrength.IsValid() ||
		input.AuthenticatedAt <= 0 || !model.IsValidTokenHash(input.CodeHash) || input.CodeLifetime <= 0 ||
		input.CodeLifetime > model.DesktopAuthorizationCodeLifetime ||
		!model.IsValidId(input.AuditEventID) || input.AuditAt <= 0 || !validAccessDeploymentCapabilities(input.Capabilities) {
		return store.NewErrInvalidInput("browser_authentication_transaction", "code_issue", nil)
	}
	if (input.AuthenticationStrength == model.AuthenticationSingleFactor && input.MFACompletedAt != 0) ||
		(input.AuthenticationStrength == model.AuthenticationMultiFactor &&
			(input.MFACompletedAt < input.AuthenticatedAt || input.MFACompletedAt > input.AuditAt)) {
		return store.NewErrInvalidInput("browser_authentication_transaction", "mfa_completed_at", nil)
	}
	return nil
}

func validateDesktopAuthorizationExchange(input *store.DesktopAuthorizationExchange) error {
	if input == nil || !model.IsValidTokenHash(input.CodeHash) || !model.IsValidTokenHash(input.StateHash) ||
		!model.IsValidCredentialToken(input.CodeChallenge) || input.Issuer == "" ||
		!model.IsValidTokenHash(input.AccessTokenHash) || !model.IsValidTokenHash(input.RefreshTokenHash) ||
		input.AccessTokenHash == input.RefreshTokenHash || input.AccessLifetime <= 0 || input.RefreshLifetime <= 0 ||
		input.IdleLifetime <= 0 || input.AbsoluteLifetime <= 0 || input.AccessLifetime > input.IdleLifetime ||
		input.IdleLifetime > input.AbsoluteLifetime || input.RefreshLifetime > input.AbsoluteLifetime ||
		input.MaximumActive < 1 || !model.IsValidId(input.AuditEventID) ||
		input.AuditAt <= 0 || !validAccessDeploymentCapabilities(input.Capabilities) {
		return store.NewErrInvalidInput("browser_authentication_transaction", "exchange", nil)
	}
	return nil
}

func (row desktopAuthorizationRow) model() (*model.BrowserAuthenticationTransaction, error) {
	id, err := model.ParseBrowserAuthenticationTransactionID(row.ID)
	if err != nil {
		return nil, store.NewErrInvalidInput("browser_authentication_transaction", "id", row.ID)
	}
	institutionID, err := model.ParseInstitutionID(row.InstitutionID)
	if err != nil {
		return nil, store.NewErrInvalidInput("browser_authentication_transaction", "institution_id", row.InstitutionID)
	}
	value := &model.BrowserAuthenticationTransaction{
		ID: id, CreatedAt: row.CreatedAt.UTC(), UpdatedAt: row.UpdatedAt.UTC(),
		Purpose: model.BrowserAuthenticationPurpose(row.Purpose), State: model.BrowserAuthenticationState(row.State),
		InstitutionID: institutionID, Issuer: row.Issuer, HandleHash: row.HandleHash.String,
		BrowserProofHash: row.BrowserProofHash.String, StateHash: row.StateHash.String,
		CallbackURL: row.CallbackURL.String, CodeChallenge: row.CodeChallenge.String,
		ExpectedAuthenticationMethod: row.ExpectedAuthenticationMethod, ExpectedProviderID: row.ExpectedProviderID.String,
		ClientType: model.SessionClientType(row.ClientType), DeviceID: row.DeviceID, DeviceName: row.DeviceName,
		ExpiresAt: row.ExpiresAt.UTC(), AuthenticationMethod: row.AuthenticationMethod.String,
		AuthenticationProviderID: row.AuthenticationProviderID.String,
		AuthenticationStrength:   model.AuthenticationStrength(row.AuthenticationStrength.String),
		AuthenticatedAt:          OptionalTimeFromNullTime(row.AuthenticatedAt), MFACompletedAt: OptionalTimeFromNullTime(row.MFACompletedAt), CodeHash: row.CodeHash.String,
		CodeExpiresAt: OptionalTimeFromNullTime(row.CodeExpiresAt), CancelledAt: OptionalTimeFromNullTime(row.CancelledAt),
		ExchangedAt: OptionalTimeFromNullTime(row.ExchangedAt),
		ExpiredAt:   OptionalTimeFromNullTime(row.ExpiredAt),
	}
	if row.UserID.Valid {
		value.UserID, err = model.ParseUserID(row.UserID.String)
		if err != nil {
			return nil, store.NewErrInvalidInput("browser_authentication_transaction", "user_id", row.UserID.String)
		}
	}
	if err = validatePersistedModel("browser_authentication_transaction", value); err != nil {
		return nil, err
	}
	return value, nil
}

func (s SQLDesktopAuthorizationStore) Maintain(ctx context.Context, limit int) (*store.DesktopAuthorizationMaintenanceResult, error) {
	if limit < 1 || limit > 1000 {
		return nil, store.NewErrInvalidInput("browser_authentication_transaction", "maintenance_limit", nil)
	}
	return runSQLTransaction(ctx, s.GetMaster().Begin, "maintain desktop authorizations", func(ctx context.Context, tx *sqlxTxWrapper) (*store.DesktopAuthorizationMaintenanceResult, error) {
		var expired int
		if err := tx.Get(ctx, &expired, `WITH candidates AS (
			SELECT id, CASE WHEN state = 'code_issued' THEN LEAST(expires_at, code_expires_at) ELSE expires_at END AS deadline
			  FROM browser_authentication_transactions
			 WHERE (state = 'pending' AND expires_at <= clock_timestamp())
			    OR (state = 'code_issued' AND LEAST(expires_at, code_expires_at) <= clock_timestamp())
			 ORDER BY deadline, id LIMIT ? FOR UPDATE SKIP LOCKED
		), changed AS (
			UPDATE browser_authentication_transactions AS transaction
			   SET state = 'expired', updated_at = candidates.deadline,
			       handle_hash = NULL, browser_proof_hash = NULL, state_hash = NULL,
			       callback_url = NULL, code_challenge = NULL, code_hash = NULL,
			       code_expires_at = NULL, expired_at = candidates.deadline
			  FROM candidates WHERE transaction.id = candidates.id RETURNING 1
		) SELECT COUNT(*) FROM changed`, limit); err != nil {
			return nil, fmt.Errorf("expire browser authentication transactions: %w", err)
		}
		var purged int
		if err := tx.Get(ctx, &purged, `WITH candidates AS (
			SELECT id FROM browser_authentication_transactions
			 WHERE state IN ('cancelled', 'exchanged', 'expired')
			   AND updated_at <= clock_timestamp() - (? * interval '1 millisecond')
			 ORDER BY updated_at, id LIMIT ? FOR UPDATE SKIP LOCKED
		), removed AS (
			DELETE FROM browser_authentication_transactions AS transaction
			 USING candidates WHERE transaction.id = candidates.id RETURNING 1
		) SELECT COUNT(*) FROM removed`, model.BrowserAuthenticationRetention.Milliseconds(), limit); err != nil {
			return nil, fmt.Errorf("purge browser authentication transactions: %w", err)
		}
		var more bool
		if err := tx.Get(ctx, &more, `SELECT
			EXISTS (SELECT 1 FROM browser_authentication_transactions
			 WHERE (state = 'pending' AND expires_at <= clock_timestamp())
			    OR (state = 'code_issued' AND LEAST(expires_at, code_expires_at) <= clock_timestamp()))
			OR EXISTS (SELECT 1 FROM browser_authentication_transactions
			 WHERE state IN ('cancelled', 'exchanged', 'expired')
			   AND updated_at <= clock_timestamp() - (? * interval '1 millisecond'))`,
			model.BrowserAuthenticationRetention.Milliseconds()); err != nil {
			return nil, fmt.Errorf("inspect remaining browser authentication maintenance: %w", err)
		}
		return &store.DesktopAuthorizationMaintenanceResult{Expired: expired, Purged: purged, More: more}, nil
	})
}

var _ store.DesktopAuthorizationStore = (*SQLDesktopAuthorizationStore)(nil)
