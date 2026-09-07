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
	"time"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

type mfaRecoveryRow struct {
	UserID               string       `db:"user_id"`
	Generation           int64        `db:"generation"`
	ResetAt              sql.NullTime `db:"reset_at"`
	ReenrollmentRequired bool         `db:"reenrollment_required"`
	UpdatedAt            sql.NullTime `db:"updated_at"`
}

func (s SQLMFAStore) GetRecoveryState(ctx context.Context, userID model.UserID) (*model.UserMFARecovery, error) {
	return getMFARecoveryState(ctx, s.GetMaster(), userID)
}

func getMFARecoveryState(ctx context.Context, executor sqlxExecutor, userID model.UserID) (*model.UserMFARecovery, error) {
	if !userID.IsValid() {
		return nil, store.NewErrInvalidInput("user_mfa_recovery", "user_id", nil)
	}
	var row mfaRecoveryRow
	if err := executor.Get(ctx, &row, `SELECT u.id user_id,COALESCE(r.generation,0) generation,r.reset_at,COALESCE(r.reenrollment_required,false) reenrollment_required,r.updated_at FROM users u LEFT JOIN user_mfa_recovery r ON r.user_id=u.id WHERE u.id=? AND u.archived_at IS NULL AND u.disabled_at IS NULL`, userID.String()); err != nil {
		return nil, translateError("user_mfa_recovery", userID.String(), err)
	}
	state := &model.UserMFARecovery{UserID: userID, Generation: row.Generation, ResetAt: OptionalTimeFromNullTime(row.ResetAt), ReenrollmentRequired: row.ReenrollmentRequired}
	if row.UpdatedAt.Valid {
		state.UpdatedAt = model.TimeUTC(row.UpdatedAt.Time)
	}
	if err := state.Validate(); err != nil {
		return nil, err
	}
	return state, nil
}

// All authority-creating callers hold the per-User Session fence before this
// read. Reset holds that same fence and advances a generation permanently.
func requireSessionAuthenticationGeneration(ctx context.Context, executor sqlxExecutor, session *model.Session) error {
	state, err := getMFARecoveryState(ctx, executor, session.UserID)
	if err != nil {
		return err
	}
	if session.AuthenticationGeneration != state.Generation || session.MFARecoveryRequired != state.ReenrollmentRequired {
		return store.ErrAuthenticationGenerationChanged
	}
	return nil
}

func requireSessionIssuanceRecovery(ctx context.Context, executor sqlxExecutor, session *model.Session, stateID model.ExternalLoginStateID) error {
	if err := requireSessionAuthenticationGeneration(ctx, executor, session); err != nil {
		return err
	}
	if session.MFARecoveryRequired && session.ClientType != model.SessionClientWeb {
		return store.ErrMFAReenrollmentRequired
	}
	if session.AuthenticationMethod == "password" {
		if !stateID.IsZero() {
			return store.NewErrInvalidInput("session", "external_login_state_id", nil)
		}
		return nil
	}
	if stateID.IsZero() && session.AuthenticationGeneration == 0 && !session.MFARecoveryRequired {
		return nil
	}
	if !stateID.IsValid() {
		return store.NewErrInvalidInput("session", "external_login_state_id", nil)
	}
	state, err := getMFARecoveryState(ctx, executor, session.UserID)
	if err != nil {
		return err
	}
	var proof struct {
		CreatedAt    time.Time      `db:"created_at"`
		Purpose      string         `db:"purpose"`
		TargetUserID sql.NullString `db:"target_user_id"`
	}
	if err := executor.Get(ctx, &proof, `SELECT created_at,purpose,target_user_id FROM external_login_states WHERE id=? AND provider=? AND consumed_at IS NOT NULL AND purpose IN ('login','mfa_recovery') FOR SHARE`, stateID.String(), session.AuthenticationProviderID); err != nil {
		return translateError("external_login_state", stateID.String(), err)
	}
	if state.ResetAt.Valid && proof.CreatedAt.Before(state.ResetAt.Time) {
		return store.ErrAuthenticationGenerationChanged
	}
	if proof.Purpose == "mfa_recovery" && proof.TargetUserID.String != session.UserID.String() {
		return store.ErrAuthenticationGenerationChanged
	}
	if state.ReenrollmentRequired && proof.Purpose != "mfa_recovery" {
		return store.ErrMFAReenrollmentRequired
	}
	return nil
}

// currentSessionRecoveryPredicate is included in authoritative access and
// refresh resolution. A recovery Session remains distinguishable and can only
// reach explicitly recovery-aware operations; a stale generation never resolves.
const currentSessionRecoveryPredicate = `sessions.authentication_generation=COALESCE((SELECT generation FROM user_mfa_recovery WHERE user_id=sessions.user_id),0)
AND sessions.mfa_recovery_required=COALESCE((SELECT reenrollment_required FROM user_mfa_recovery WHERE user_id=sessions.user_id),false)`

// Legacy PAT audit preparations carry their source Session rather than a raw
// access credential. Once recovery exists, that Session must still belong to
// the current generation; an old request cannot create new PAT authority after
// reenrollment clears the restriction.
func requireMFARecoveryPATSource(ctx context.Context, executor sqlxExecutor, userID, sessionID string, at time.Time) error {
	state, err := getMFARecoveryState(ctx, executor, model.UserID(userID))
	if err != nil {
		return err
	}
	if state.ReenrollmentRequired {
		return store.ErrMFAReenrollmentRequired
	}
	if state.Generation == 0 {
		return nil
	}
	var active bool
	if err := executor.Get(ctx, &active, `SELECT true FROM sessions WHERE id=? AND user_id=? AND authentication_generation=? AND NOT mfa_recovery_required AND archived_at IS NULL AND revoked_at IS NULL AND idle_expires_at>? AND expires_at>? FOR SHARE`, sessionID, userID, state.Generation, at, at); err != nil {
		return translateError("session", "pat_preparation", err)
	}
	return nil
}

// Authentication-method commands already reserve an audit carrying their
// source Session. After any reset, that Session must still have current
// authority, even if the old request reserved its audit after the reset.
// The caller holds the User Session fence until the credential write commits.
func requireMFARecoveryAuthenticationMethodSource(ctx context.Context, executor sqlxExecutor, userID model.UserID, auditID string) error {
	state, err := getMFARecoveryState(ctx, executor, userID)
	if err != nil {
		return err
	}
	if state.ReenrollmentRequired {
		return store.ErrMFAReenrollmentRequired
	}
	if state.Generation == 0 {
		return nil
	}
	var source struct {
		SessionID sql.NullString `db:"session_id"`
		At        time.Time      `db:"at"`
	}
	if err := executor.Get(ctx, &source, `SELECT session_id,clock_timestamp() AS at FROM audit_events WHERE id=? AND actor_id=? AND status='attempt' FOR SHARE`, auditID, userID.String()); err != nil {
		return translateError("audit_event", auditID, err)
	}
	return requireMFARecoveryPATSource(ctx, executor, userID.String(), source.SessionID.String, source.At)
}
