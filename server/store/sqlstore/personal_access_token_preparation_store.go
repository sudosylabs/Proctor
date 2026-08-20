// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package sqlstore

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

const (
	personalAccessTokenPreparationMinimumLifetime = 30 * time.Second
	personalAccessTokenPreparationMaximumLifetime = 15 * time.Minute
	personalAccessTokenPreparationFailureCode     = "personal_access_token.preparation_expired"
)

type personalAccessTokenPreparationRow struct {
	ID           string                                `db:"id"`
	CreatedAt    time.Time                             `db:"created_at"`
	ExpiresAt    time.Time                             `db:"expires_at"`
	UserID       string                                `db:"user_id"`
	TokenID      sql.NullString                        `db:"token_id"`
	Kind         store.PersonalAccessTokenMutationKind `db:"kind"`
	ActorID      sql.NullString                        `db:"actor_id"`
	SessionID    sql.NullString                        `db:"session_id"`
	Action       string                                `db:"action"`
	ResourceType model.ResourceType                    `db:"resource_type"`
	ResourceID   string                                `db:"resource_id"`
	ScopeType    model.RoleScopeType                   `db:"scope_type"`
	ScopeID      string                                `db:"scope_id"`
	RequestID    string                                `db:"request_id"`
	NodeID       string                                `db:"node_id"`
	ClientType   string                                `db:"client_type"`
	AuthMethod   string                                `db:"authentication_method"`
	IPAddress    string                                `db:"ip_address"`
	UserAgent    string                                `db:"user_agent"`
	Parameters   jsonValue                             `db:"parameters"`
	PriorState   jsonValue                             `db:"prior_state"`
}

func (s SQLPersonalAccessTokenStore) PrepareMutation(ctx context.Context, input *store.PersonalAccessTokenMutationPreparation) (*store.PreparedPersonalAccessTokenMutation, error) {
	if err := validatePersonalAccessTokenPreparationInput(input); err != nil {
		return nil, err
	}
	return runSQLTransaction(ctx, s.GetMaster().Begin, "prepare personal access token mutation", func(ctx context.Context, tx *sqlxTxWrapper) (*store.PreparedPersonalAccessTokenMutation, error) {
		// Serialize first, then derive the bounded preparation lifecycle from
		// PostgreSQL time. A contender must not persist an already-stale attempt
		// after waiting behind another transition for this user.
		if err := lockPersonalAccessTokensForUser(ctx, tx, input.UserID); err != nil {
			return nil, err
		}
		at, err := personalAccessTokenDatabaseNow(ctx, tx)
		if err != nil {
			return nil, err
		}
		candidate := input.Audit.Clone()
		candidate.PrepareCreate(model.NewAuditEventID(), at)
		if err := candidate.Validate(); err != nil {
			return nil, store.NewErrInvalidInput("personal_access_token", "audit", nil)
		}
		if err := validatePersonalAccessTokenPreparationAudit(input, candidate); err != nil {
			return nil, err
		}
		if input.Kind == store.PersonalAccessTokenMutationCreate {
			var active bool
			if err := tx.Get(ctx, &active, `SELECT archived_at IS NULL AND disabled_at IS NULL FROM users WHERE id=? FOR SHARE`, input.UserID); err != nil {
				return nil, translateError("user", input.UserID, err)
			}
			if !active {
				return nil, store.NewErrNotFound("user", input.UserID)
			}
		} else {
			var exists bool
			if err := tx.Get(ctx, &exists, `SELECT TRUE FROM personal_access_tokens WHERE id=? AND user_id=? AND archived_at IS NULL FOR SHARE`, input.TokenID, input.UserID); err != nil {
				return nil, translateError("personal_access_token", input.TokenID, err)
			}
		}
		id := model.NewId()
		expiresAt := at.Add(input.Lifetime)
		if _, err := tx.Exec(ctx, `
			INSERT INTO personal_access_token_mutation_preparations (
				id, created_at, expires_at, user_id, token_id, kind,
				actor_id, session_id, action, resource_type, resource_id,
				scope_type, scope_id, request_id, node_id, client_type,
				authentication_method, ip_address, user_agent, parameters, prior_state
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			id, at, expiresAt, input.UserID, nullablePersonalAccessTokenPreparationID(input.TokenID), input.Kind,
			nullableAuditString(candidate.ActorID.String()), nullableAuditString(candidate.SessionID.String()), candidate.Action,
			candidate.Resource.Type, candidate.Resource.ID, candidate.ScopeType, candidate.ScopeID,
			candidate.RequestID, candidate.NodeID, candidate.ClientType, candidate.AuthMethod,
			candidate.IPAddress, candidate.UserAgent, nullableJSON(candidate.Parameters), nullableJSON(candidate.PriorState)); err != nil {
			return nil, fmt.Errorf("insert personal access token mutation preparation: %w", translateError("personal_access_token_mutation_preparation", id, err))
		}
		return &store.PreparedPersonalAccessTokenMutation{ID: id, ActionAt: at, ExpiresAt: expiresAt}, nil
	})
}

func (s SQLPersonalAccessTokenStore) FailMutation(ctx context.Context, input *store.PersonalAccessTokenMutationFailure) error {
	if input == nil || !model.IsValidId(input.PreparationID) || strings.TrimSpace(input.ErrorCode) == "" || len(input.ErrorCode) > 128 {
		return store.NewErrInvalidInput("personal_access_token", "mutation_failure", nil)
	}
	_, err := runSQLTransaction(ctx, s.GetMaster().Begin, "fail personal access token mutation preparation", func(ctx context.Context, tx *sqlxTxWrapper) (struct{}, error) {
		row, _, err := lockPersonalAccessTokenPreparation(ctx, tx, input.PreparationID)
		if err != nil {
			return struct{}{}, err
		}
		if err := terminalizePersonalAccessTokenPreparation(ctx, tx, row, model.AuditStatusFail, strings.TrimSpace(input.ErrorCode), nil); err != nil {
			return struct{}{}, err
		}
		return struct{}{}, nil
	})
	return err
}

func (s SQLPersonalAccessTokenStore) MaintainMutationPreparations(ctx context.Context, limit int) (*store.PersonalAccessTokenPreparationMaintenanceResult, error) {
	if limit < 1 || limit > 1000 {
		return nil, store.NewErrInvalidInput("personal_access_token", "maintenance_limit", limit)
	}
	return runSQLTransaction(ctx, s.GetMaster().Begin, "maintain personal access token mutation preparations", func(ctx context.Context, tx *sqlxTxWrapper) (*store.PersonalAccessTokenPreparationMaintenanceResult, error) {
		databaseAt, err := personalAccessTokenDatabaseNow(ctx, tx)
		if err != nil {
			return nil, err
		}
		var rows []personalAccessTokenPreparationRow
		if err := tx.Select(ctx, &rows, `SELECT `+personalAccessTokenPreparationColumns+`
			FROM personal_access_token_mutation_preparations
			WHERE expires_at<=? ORDER BY expires_at,id FOR UPDATE SKIP LOCKED LIMIT ?`, databaseAt, limit); err != nil {
			return nil, fmt.Errorf("select expired personal access token mutation preparations: %w", err)
		}
		for i := range rows {
			if err := terminalizePersonalAccessTokenPreparation(ctx, tx, &rows[i], model.AuditStatusFail, personalAccessTokenPreparationFailureCode, nil); err != nil {
				return nil, err
			}
		}
		var more bool
		if err := tx.Get(ctx, &more, `SELECT EXISTS (SELECT 1 FROM personal_access_token_mutation_preparations WHERE expires_at<=?)`, databaseAt); err != nil {
			return nil, fmt.Errorf("inspect personal access token mutation preparation continuation: %w", err)
		}
		return &store.PersonalAccessTokenPreparationMaintenanceResult{Failed: len(rows), More: more}, nil
	})
}

const personalAccessTokenPreparationColumns = `id,created_at,expires_at,user_id,token_id,kind,
	actor_id,session_id,action,resource_type,resource_id,scope_type,scope_id,
	request_id,node_id,client_type,authentication_method,ip_address,user_agent,parameters,prior_state`

func lockPersonalAccessTokenPreparation(ctx context.Context, tx *sqlxTxWrapper, id string) (*personalAccessTokenPreparationRow, time.Time, error) {
	var row personalAccessTokenPreparationRow
	if err := tx.Get(ctx, &row, `SELECT `+personalAccessTokenPreparationColumns+` FROM personal_access_token_mutation_preparations WHERE id=? FOR UPDATE`, id); err != nil {
		return nil, time.Time{}, translateError("personal_access_token_mutation_preparation", id, err)
	}
	// Sample authoritative time only after acquiring the preparation lock. A
	// contender may have waited until the bounded preparation expired.
	databaseAt, err := personalAccessTokenDatabaseNow(ctx, tx)
	if err != nil {
		return nil, time.Time{}, err
	}
	return &row, databaseAt, nil
}

func requirePersonalAccessTokenPreparation(row *personalAccessTokenPreparationRow, kind store.PersonalAccessTokenMutationKind, userID, tokenID string, databaseAt time.Time) error {
	if row == nil || row.Kind != kind || row.UserID != userID || row.TokenID.String != tokenID || row.TokenID.Valid != (tokenID != "") {
		return store.NewErrInvalidInput("personal_access_token_mutation_preparation", "binding", nil)
	}
	if !databaseAt.Before(row.ExpiresAt) {
		return store.NewErrConflict("personal_access_token_mutation_preparation", "expired", nil)
	}
	return nil
}

func terminalizePersonalAccessTokenPreparation(ctx context.Context, tx *sqlxTxWrapper, row *personalAccessTokenPreparationRow, status model.AuditStatus, errorCode string, result json.RawMessage) error {
	if row == nil || (status != model.AuditStatusSuccess && status != model.AuditStatusFail) {
		return store.NewErrInvalidInput("personal_access_token_mutation_preparation", "terminal", nil)
	}
	terminalAt, err := personalAccessTokenDatabaseNow(ctx, tx)
	if err != nil {
		return err
	}
	event, err := row.auditEvent(status, errorCode, result)
	if err != nil {
		return err
	}
	if _, err := insertAuditEventBetween(ctx, tx, event, row.CreatedAt, terminalAt); err != nil {
		return fmt.Errorf("insert personal access token terminal audit: %w", err)
	}
	return deletePersonalAccessTokenPreparation(ctx, tx, row.ID)
}

func deletePersonalAccessTokenPreparation(ctx context.Context, tx *sqlxTxWrapper, id string) error {
	result, err := tx.Exec(ctx, `DELETE FROM personal_access_token_mutation_preparations WHERE id=?`, id)
	if err != nil {
		return fmt.Errorf("delete personal access token mutation preparation: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil || affected != 1 {
		return store.NewErrConflict("personal_access_token_mutation_preparation", "already_consumed", err)
	}
	return nil
}

func (row personalAccessTokenPreparationRow) auditEvent(status model.AuditStatus, errorCode string, result json.RawMessage) (*model.AuditEvent, error) {
	audit := auditRow{
		ID: row.ID, CreatedAt: row.CreatedAt, UpdatedAt: row.CreatedAt,
		ActorID: row.ActorID, SessionID: row.SessionID, Action: row.Action,
		ResourceType: row.ResourceType, ResourceID: row.ResourceID,
		ScopeType: row.ScopeType, ScopeID: row.ScopeID, Status: status,
		RequestID: row.RequestID, NodeID: row.NodeID, ClientType: row.ClientType,
		AuthMethod: row.AuthMethod, IPAddress: row.IPAddress, UserAgent: row.UserAgent,
		ErrorCode: errorCode, Parameters: row.Parameters, PriorState: row.PriorState, Result: jsonValue(result),
	}
	event, err := audit.model()
	if err != nil {
		return nil, err
	}
	event.ID = ""
	return event, nil
}

func validatePersonalAccessTokenPreparationInput(input *store.PersonalAccessTokenMutationPreparation) error {
	if input == nil || !model.IsValidId(input.UserID) || input.Audit == nil || input.Lifetime < personalAccessTokenPreparationMinimumLifetime ||
		input.Lifetime > personalAccessTokenPreparationMaximumLifetime || input.Lifetime%time.Millisecond != 0 {
		return store.NewErrInvalidInput("personal_access_token", "mutation_preparation", nil)
	}
	switch input.Kind {
	case store.PersonalAccessTokenMutationCreate:
		if input.TokenID != "" {
			return store.NewErrInvalidInput("personal_access_token", "mutation_target", nil)
		}
	case store.PersonalAccessTokenMutationEnable, store.PersonalAccessTokenMutationDisable, store.PersonalAccessTokenMutationRevoke:
		if !model.IsValidId(input.TokenID) {
			return store.NewErrInvalidInput("personal_access_token", "mutation_target", nil)
		}
	default:
		return store.NewErrInvalidInput("personal_access_token", "mutation_kind", input.Kind)
	}
	return nil
}

func validatePersonalAccessTokenPreparationAudit(input *store.PersonalAccessTokenMutationPreparation, event *model.AuditEvent) error {
	expectedAction := map[store.PersonalAccessTokenMutationKind]string{
		store.PersonalAccessTokenMutationCreate:  "personal_access_token.create",
		store.PersonalAccessTokenMutationEnable:  "personal_access_token.enable",
		store.PersonalAccessTokenMutationDisable: "personal_access_token.disable",
		store.PersonalAccessTokenMutationRevoke:  "personal_access_token.revoke",
	}[input.Kind]
	if event.ActorID.String() != input.UserID || !event.SessionID.IsValid() || event.Status != model.AuditStatusAttempt || event.Action != expectedAction ||
		event.Resource.Type != model.ResourceInstitution || event.ScopeType != model.RoleScopeInstitution || event.Resource.ID != event.ScopeID ||
		event.ErrorCode != "" || len(event.Result) != 0 {
		return store.NewErrInvalidInput("personal_access_token", "audit", nil)
	}
	return nil
}

func nullablePersonalAccessTokenPreparationID(id string) any {
	if id == "" {
		return nil
	}
	return id
}
