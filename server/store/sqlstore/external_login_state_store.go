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

const externalLoginStateRetention = 24 * time.Hour

type SQLExternalLoginStateStore struct {
	*SQLStore
	statesQuery sq.SelectBuilder
}

type externalLoginStateRow struct {
	ID          string       `db:"id"`
	CreatedAt   time.Time    `db:"created_at"`
	UpdatedAt   time.Time    `db:"updated_at"`
	Provider    string       `db:"provider"`
	StateHash   string       `db:"state_hash"`
	BindingHash string       `db:"binding_hash"`
	ReturnTo    string       `db:"return_to"`
	ClientType  string       `db:"client_type"`
	DeviceID    string       `db:"device_id"`
	DeviceName  string       `db:"device_name"`
	ExpiresAt   time.Time    `db:"expires_at"`
	ConsumedAt  sql.NullTime `db:"consumed_at"`
}

func externalLoginStateSliceColumns() []string {
	return []string{
		"external_login_states.id",
		"external_login_states.created_at",
		"external_login_states.updated_at",
		"external_login_states.provider",
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
) (*model.ExternalLoginState, error) {
	if state == nil {
		return nil, store.NewErrInvalidInput("external_login_state", "value", nil)
	}
	if !state.ID.IsZero() {
		return nil, store.NewErrInvalidInput("external_login_state", "id", state.ID.String())
	}
	candidate := *state
	candidate.PrepareCreate(model.NewExternalLoginStateID(), model.NowUTC())
	if err := candidate.Validate(); err != nil {
		return nil, err
	}
	return runSQLTransaction(ctx, s.GetMaster().Begin, "external login state save", func(ctx context.Context, tx *sqlxTxWrapper) (*model.ExternalLoginState, error) {
		if _, err := tx.Exec(
			ctx,
			"DELETE FROM external_login_states WHERE expires_at < ?",
			candidate.CreatedAt.Add(-externalLoginStateRetention),
		); err != nil {
			return nil, fmt.Errorf("prune external login states: %w", err)
		}
		row := newExternalLoginStateRow(&candidate)
		if _, err := tx.NamedExec(ctx, `
			INSERT INTO external_login_states (
				id, created_at, updated_at, provider, state_hash, binding_hash,
				return_to, client_type, device_id, device_name, expires_at,
				consumed_at
			) VALUES (
				:id, :created_at, :updated_at, :provider, :state_hash, :binding_hash,
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
	consumedAt int64,
) (*model.ExternalLoginState, error) {
	at := model.TimeFromMillis(consumedAt)
	var row externalLoginStateRow
	err := s.GetMaster().Get(ctx, &row, `
		UPDATE external_login_states
		   SET updated_at = GREATEST(updated_at, ?), consumed_at = ?
		 WHERE provider = ?
		   AND state_hash = ?
		   AND binding_hash = ?
		   AND consumed_at IS NULL
		   AND created_at <= ?
		   AND expires_at > ?
		RETURNING id, created_at, updated_at, provider, state_hash, binding_hash,
		          return_to, client_type, device_id, device_name, expires_at,
		          consumed_at`,
		at,
		at,
		provider,
		stateHash,
		bindingHash,
		at,
		at,
	)
	if err != nil {
		return nil, translateError("external_login_state", "", err)
	}
	return row.model()
}

func newExternalLoginStateRow(
	state *model.ExternalLoginState,
) externalLoginStateRow {
	return externalLoginStateRow{
		ID:          state.ID.String(),
		CreatedAt:   UTCTime(state.CreatedAt),
		UpdatedAt:   UTCTime(state.UpdatedAt),
		Provider:    state.Provider,
		StateHash:   state.StateHash,
		BindingHash: state.BindingHash,
		ReturnTo:    state.ReturnTo,
		ClientType:  string(state.ClientType),
		DeviceID:    state.DeviceID,
		DeviceName:  state.DeviceName,
		ExpiresAt:   UTCTime(state.ExpiresAt),
		ConsumedAt:  NullTimeFromOptional(state.ConsumedAt),
	}
}

func (row externalLoginStateRow) model() (*model.ExternalLoginState, error) {
	id, err := parsePersistedID("external_login_state", "id", row.ID, model.ParseExternalLoginStateID)
	if err != nil {
		return nil, err
	}
	value := &model.ExternalLoginState{
		ID:          id,
		CreatedAt:   row.CreatedAt.UTC(),
		UpdatedAt:   row.UpdatedAt.UTC(),
		Provider:    row.Provider,
		StateHash:   row.StateHash,
		BindingHash: row.BindingHash,
		ReturnTo:    row.ReturnTo,
		ClientType:  model.SessionClientType(row.ClientType),
		DeviceID:    row.DeviceID,
		DeviceName:  row.DeviceName,
		ExpiresAt:   row.ExpiresAt.UTC(),
		ConsumedAt:  OptionalTimeFromNullTime(row.ConsumedAt),
	}
	if err := validatePersistedModel("external_login_state", value); err != nil {
		return nil, err
	}
	return value, nil
}

var _ store.ExternalLoginStateStore = (*SQLExternalLoginStateStore)(nil)
