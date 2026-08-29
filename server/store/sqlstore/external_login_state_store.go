// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package sqlstore

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	sq "github.com/Masterminds/squirrel"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

const (
	externalLoginStateRetention       = 24 * time.Hour
	externalLoginStateLifetimeMinimum = time.Minute
	externalLoginStateLifetimeMaximum = 30 * time.Minute
)

type SQLExternalLoginStateStore struct {
	*SQLStore
	statesQuery sq.SelectBuilder
}

type externalLoginStateRow struct {
	ID                                 string         `db:"id"`
	CreatedAt                          time.Time      `db:"created_at"`
	UpdatedAt                          time.Time      `db:"updated_at"`
	Provider                           string         `db:"provider"`
	Purpose                            string         `db:"purpose"`
	TargetUserID                       sql.NullString `db:"target_user_id"`
	InvitationID                       sql.NullString `db:"invitation_id"`
	BrowserAuthenticationTransactionID sql.NullString `db:"browser_authentication_transaction_id"`
	AuditEventID                       sql.NullString `db:"audit_event_id"`
	StateHash                          string         `db:"state_hash"`
	BindingHash                        string         `db:"binding_hash"`
	ReturnTo                           string         `db:"return_to"`
	ClientType                         string         `db:"client_type"`
	DeviceID                           string         `db:"device_id"`
	DeviceName                         string         `db:"device_name"`
	ExpiresAt                          time.Time      `db:"expires_at"`
	ConsumedAt                         sql.NullTime   `db:"consumed_at"`
}

func externalLoginStateSliceColumns() []string {
	return []string{
		"external_login_states.id",
		"external_login_states.created_at",
		"external_login_states.updated_at",
		"external_login_states.provider",
		"external_login_states.purpose",
		"external_login_states.target_user_id",
		"external_login_states.invitation_id",
		"external_login_states.browser_authentication_transaction_id",
		"external_login_states.audit_event_id",
		"external_login_states.state_hash",
		"external_login_states.binding_hash",
		"external_login_states.return_to",
		"external_login_states.client_type",
		"external_login_states.device_id",
		"external_login_states.device_name",
		"external_login_states.expires_at",
		"external_login_states.consumed_at",
	}
}

func newSQLExternalLoginStateStore(sqlStore *SQLStore) store.ExternalLoginStateStore {
	s := &SQLExternalLoginStateStore{SQLStore: sqlStore}
	s.statesQuery = s.getQueryBuilder().
		Select(externalLoginStateSliceColumns()...).
		From("external_login_states")
	return s
}

func (s SQLExternalLoginStateStore) Save(
	ctx context.Context,
	state *model.ExternalLoginState,
	lifetime time.Duration,
) (*model.ExternalLoginState, error) {
	if state != nil && state.Purpose == model.ExternalAuthenticationPurposeInvitationAdmission {
		return nil, store.NewErrInvalidInput("external_login_state", "purpose", nil)
	}
	return s.save(ctx, state, lifetime, "external login state save", nil)
}

func (s SQLExternalLoginStateStore) SaveInvitationAdmission(
	ctx context.Context,
	state *model.ExternalLoginState,
	lifetime time.Duration,
	claimHash string,
) (*model.ExternalLoginState, error) {
	if state == nil || state.Purpose != model.ExternalAuthenticationPurposeInvitationAdmission ||
		!state.InvitationID.IsZero() || !model.IsValidTokenHash(claimHash) {
		return nil, store.NewErrInvalidInput("external_login_state", "invitation_admission", nil)
	}
	return s.save(ctx, state, lifetime, "external invitation admission state save",
		func(ctx context.Context, tx *sqlxTxWrapper, candidate *model.ExternalLoginState, databaseNow time.Time) error {
			var invitationID string
			if err := tx.Get(ctx, &invitationID, `
				SELECT id FROM invitations
				 WHERE claim_hash=? AND state='pending' AND created_at<=? AND expires_at>?
				   AND (intended_end_at IS NULL OR intended_end_at>?)
				 FOR SHARE`, claimHash, databaseNow, databaseNow, databaseNow); err != nil {
				return translateError("invitation", "claim", err)
			}
			parsed, err := parsePersistedID("invitation", "id", invitationID, model.ParseInvitationID)
			if err != nil {
				return err
			}
			candidate.InvitationID = parsed
			return nil
		})
}

func (s SQLExternalLoginStateStore) save(
	ctx context.Context,
	state *model.ExternalLoginState,
	lifetime time.Duration,
	operation string,
	prepare func(context.Context, *sqlxTxWrapper, *model.ExternalLoginState, time.Time) error,
) (*model.ExternalLoginState, error) {
	if state == nil || lifetime < externalLoginStateLifetimeMinimum || lifetime > externalLoginStateLifetimeMaximum ||
		lifetime%time.Millisecond != 0 {
		return nil, store.NewErrInvalidInput("external_login_state", "value", nil)
	}
	if !state.ID.IsZero() || !state.CreatedAt.IsZero() || !state.UpdatedAt.IsZero() ||
		!state.ExpiresAt.IsZero() || state.ConsumedAt.Valid {
		return nil, store.NewErrInvalidInput("external_login_state", "id", state.ID.String())
	}
	candidate := *state
	return runSQLTransaction(ctx, s.GetMaster().Begin, operation, func(ctx context.Context, tx *sqlxTxWrapper) (*model.ExternalLoginState, error) {
		var databaseNow time.Time
		if err := tx.Get(ctx, &databaseNow, `SELECT clock_timestamp()`); err != nil {
			return nil, fmt.Errorf("read external login state creation time: %w", err)
		}
		databaseNow = model.TimeUTC(databaseNow)
		if prepare != nil {
			if err := prepare(ctx, tx, &candidate, databaseNow); err != nil {
				return nil, err
			}
		}
		candidate.ExpiresAt = databaseNow.Add(lifetime)
		candidate.PrepareCreate(model.NewExternalLoginStateID(), databaseNow)
		if err := candidate.Validate(); err != nil {
			return nil, err
		}
		row := newExternalLoginStateRow(&candidate)
		if _, err := tx.NamedExec(ctx, `
			INSERT INTO external_login_states (
				id, created_at, updated_at, provider, purpose, target_user_id, invitation_id, browser_authentication_transaction_id, audit_event_id, state_hash, binding_hash,
				return_to, client_type, device_id, device_name, expires_at,
				consumed_at
			) VALUES (
				:id, :created_at, :updated_at, :provider, :purpose, :target_user_id, :invitation_id, :browser_authentication_transaction_id, :audit_event_id, :state_hash, :binding_hash,
				:return_to, :client_type, :device_id, :device_name, :expires_at,
				:consumed_at
			)`, &row); err != nil {
			return nil, fmt.Errorf(
				"save external login state: %w",
				translateError("external_login_state", candidate.ID.String(), err),
			)
		}
		return &candidate, nil
	})
}

func (s SQLExternalLoginStateStore) GetByStateHash(
	ctx context.Context,
	stateHash string,
) (*model.ExternalLoginState, error) {
	var row externalLoginStateRow
	query := s.statesQuery.Where(sq.Eq{
		"external_login_states.state_hash": stateHash,
	})
	if err := s.GetMaster().GetBuilder(ctx, &row, query); err != nil {
		return nil, translateError("external_login_state", "", err)
	}
	return row.model()
}

func (s SQLExternalLoginStateStore) Consume(
	ctx context.Context,
	provider string,
	stateHash string,
	bindingHash string,
) (*model.ExternalLoginState, error) {
	var row externalLoginStateRow
	err := s.GetMaster().Get(ctx, &row, `
		UPDATE external_login_states
		   SET updated_at = consumed.at, consumed_at = consumed.at
		  FROM (SELECT clock_timestamp() AS at) AS consumed
		 WHERE provider = ?
		   AND state_hash = ?
		   AND binding_hash = ?
		   AND consumed_at IS NULL
		   AND created_at <= consumed.at
		   AND expires_at > consumed.at
		RETURNING id, created_at, updated_at, provider, purpose, target_user_id, invitation_id, browser_authentication_transaction_id, audit_event_id, state_hash, binding_hash,
		          return_to, client_type, device_id, device_name, expires_at,
		          consumed_at`,
		provider,
		stateHash,
		bindingHash,
	)
	if err != nil {
		return nil, translateError("external_login_state", "", err)
	}
	return row.model()
}

func (s SQLExternalLoginStateStore) Maintain(ctx context.Context, limit int) (*store.ExternalLoginStateMaintenanceResult, error) {
	if limit < 1 || limit > 1000 {
		return nil, store.NewErrInvalidInput("external_login_state", "maintenance_limit", limit)
	}
	return runSQLTransaction(ctx, s.GetMaster().Begin, "maintain external login states", func(ctx context.Context, tx *sqlxTxWrapper) (*store.ExternalLoginStateMaintenanceResult, error) {
		var terminalized int
		if err := tx.Get(ctx, &terminalized, `WITH candidates AS (
			SELECT state.id, state.audit_event_id
			  FROM external_login_states state
			  JOIN audit_events audit ON audit.id=state.audit_event_id AND audit.status='attempt'
			 WHERE state.purpose='connect' AND state.expires_at<=clock_timestamp()
			 ORDER BY state.expires_at, state.id
			 FOR UPDATE OF state, audit SKIP LOCKED LIMIT ?
		), completed AS (
			UPDATE audit_events audit
			   SET updated_at=GREATEST(audit.updated_at, clock_timestamp()), status='fail',
			       error_code='authentication.external.expired'
			  FROM candidates candidate
			 WHERE audit.id=candidate.audit_event_id AND audit.status='attempt'
			 RETURNING audit.id
		) SELECT COUNT(*) FROM completed`, limit); err != nil {
			return nil, fmt.Errorf("terminalize abandoned external provider connections: %w", err)
		}
		var purged int
		if err := tx.Get(ctx, &purged, `WITH candidates AS (
			SELECT id FROM external_login_states
			 WHERE expires_at<=clock_timestamp()-(? * interval '1 millisecond')
			 ORDER BY expires_at, id FOR UPDATE SKIP LOCKED LIMIT ?
		), removed AS (
			DELETE FROM external_login_states state USING candidates candidate
			 WHERE state.id=candidate.id RETURNING state.id
		) SELECT COUNT(*) FROM removed`, externalLoginStateRetention.Milliseconds(), limit); err != nil {
			return nil, fmt.Errorf("purge retained external login states: %w", err)
		}
		var more bool
		if err := tx.Get(ctx, &more, `SELECT EXISTS (
			SELECT 1 FROM external_login_states state JOIN audit_events audit ON audit.id=state.audit_event_id
			 WHERE state.purpose='connect' AND state.expires_at<=clock_timestamp() AND audit.status='attempt'
		) OR EXISTS (
			SELECT 1 FROM external_login_states WHERE expires_at<=clock_timestamp()-(? * interval '1 millisecond')
		)`, externalLoginStateRetention.Milliseconds()); err != nil {
			return nil, fmt.Errorf("inspect external login state maintenance: %w", err)
		}
		return &store.ExternalLoginStateMaintenanceResult{Terminalized: terminalized, Purged: purged, More: more}, nil
	})
}

func newExternalLoginStateRow(
	state *model.ExternalLoginState,
) externalLoginStateRow {
	return externalLoginStateRow{
		ID:                                 state.ID.String(),
		CreatedAt:                          UTCTime(state.CreatedAt),
		UpdatedAt:                          UTCTime(state.UpdatedAt),
		Provider:                           state.Provider,
		Purpose:                            string(state.Purpose),
		TargetUserID:                       sql.NullString{String: state.TargetUserID.String(), Valid: !state.TargetUserID.IsZero()},
		InvitationID:                       sql.NullString{String: state.InvitationID.String(), Valid: !state.InvitationID.IsZero()},
		BrowserAuthenticationTransactionID: sql.NullString{String: state.BrowserAuthenticationTransactionID.String(), Valid: !state.BrowserAuthenticationTransactionID.IsZero()},
		AuditEventID:                       sql.NullString{String: state.AuditEventID, Valid: state.AuditEventID != ""},
		StateHash:                          state.StateHash,
		BindingHash:                        state.BindingHash,
		ReturnTo:                           state.ReturnTo,
		ClientType:                         string(state.ClientType),
		DeviceID:                           state.DeviceID,
		DeviceName:                         state.DeviceName,
		ExpiresAt:                          UTCTime(state.ExpiresAt),
		ConsumedAt:                         NullTimeFromOptional(state.ConsumedAt),
	}
}

func (row externalLoginStateRow) model() (*model.ExternalLoginState, error) {
	id, err := parsePersistedID("external_login_state", "id", row.ID, model.ParseExternalLoginStateID)
	if err != nil {
		return nil, err
	}
	value := &model.ExternalLoginState{
		ID:           id,
		CreatedAt:    row.CreatedAt.UTC(),
		UpdatedAt:    row.UpdatedAt.UTC(),
		Provider:     row.Provider,
		Purpose:      model.ExternalAuthenticationPurpose(row.Purpose),
		AuditEventID: row.AuditEventID.String,
		StateHash:    row.StateHash,
		BindingHash:  row.BindingHash,
		ReturnTo:     row.ReturnTo,
		ClientType:   model.SessionClientType(row.ClientType),
		DeviceID:     row.DeviceID,
		DeviceName:   row.DeviceName,
		ExpiresAt:    row.ExpiresAt.UTC(),
		ConsumedAt:   OptionalTimeFromNullTime(row.ConsumedAt),
	}
	target, err := parseNullablePersistedID("external_login_state", "target_user_id", row.TargetUserID, model.ParseUserID)
	if err != nil {
		return nil, err
	}
	value.TargetUserID = target
	invitationID, err := parseNullablePersistedID("external_login_state", "invitation_id", row.InvitationID, model.ParseInvitationID)
	if err != nil {
		return nil, err
	}
	value.InvitationID = invitationID
	browserTransactionID, err := parseNullablePersistedID("external_login_state", "browser_authentication_transaction_id", row.BrowserAuthenticationTransactionID, model.ParseBrowserAuthenticationTransactionID)
	if err != nil {
		return nil, err
	}
	value.BrowserAuthenticationTransactionID = browserTransactionID
	if err := validatePersistedModel("external_login_state", value); err != nil {
		return nil, err
	}
	return value, nil
}

var _ store.ExternalLoginStateStore = (*SQLExternalLoginStateStore)(nil)
