// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package sqlstore

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

func (s SQLMFAStore) ResetWithAudit(ctx context.Context, input *store.MFAAssistedReset) (*store.MFAResetResult, error) {
	if input == nil || input.Principal.Validate() != nil || input.Principal.CredentialType != model.CredentialSessionAccess || input.UserID == input.Principal.UserID || !input.UserID.IsValid() || !input.IdentityVerified || strings.TrimSpace(input.Reason) == "" || utf8.RuneCountInString(input.Reason) > 512 || strings.TrimSpace(input.VerificationReference) == "" || utf8.RuneCountInString(input.VerificationReference) > 128 || input.RecentAuthenticationTTL <= 0 || !input.AuditEventID.IsValid() {
		return nil, store.NewErrInvalidInput("user_mfa_recovery", "reset", nil)
	}
	payloadKeyID, err := validateSecurityNoticeMail(input.UserID, input.Notice.Occurrence, input.Notice.Delivery, input.Notice.Job, model.MailTemplateIdentityMFAReset, input.NoticeAt.UnixMilli())
	if err != nil {
		return nil, err
	}
	return runSQLTransaction(ctx, s.GetMaster().Begin, "administrator MFA reset", func(ctx context.Context, tx *sqlxTxWrapper) (*store.MFAResetResult, error) {
		if payloadKeyID != "" {
			if err := requireMailPayloadPrimary(ctx, tx, payloadKeyID); err != nil {
				return nil, err
			}
		}
		if err := lockSystemAdministratorAuthenticationPaths(ctx, tx); err != nil {
			return nil, err
		}
		ids := []string{input.Principal.UserID.String(), input.UserID.String()}
		sort.Strings(ids)
		for _, id := range ids {
			if err := lockUserSessions(ctx, tx, id); err != nil {
				return nil, err
			}
		}
		if err := lockMFAUser(ctx, tx, input.UserID.String()); err != nil {
			return nil, err
		}
		if err := lockPersonalAccessTokensForUser(ctx, tx, input.UserID.String()); err != nil {
			return nil, err
		}
		_, at, err := currentMFASession(ctx, tx, input.Principal, false, input.RecentAuthenticationTTL, true)
		if err != nil {
			return nil, err
		}
		administrator, err := isActiveSystemAdministrator(ctx, tx, input.Principal.UserID.String(), at)
		if err != nil {
			return nil, err
		}
		if !administrator {
			return nil, store.NewErrConflict("authorization", "authority", nil)
		}
		policy, err := getAccessPolicy(ctx, tx, "FOR SHARE")
		if err != nil {
			return nil, err
		}
		usable, err := hasUsableSystemAdministratorAuthenticationPath(ctx, tx, policy.Settings(), input.Capabilities, at, systemAdministratorAuthenticationPathScope{ExcludedUserID: input.UserID.String()})
		if err != nil {
			return nil, err
		}
		if !usable {
			return nil, store.NewErrConflict("user_mfa_recovery", "last_administrator_path", nil)
		}
		result, err := resetMFAUserAccess(ctx, tx, input.UserID, at)
		if err != nil {
			return nil, err
		}
		if err := insertSecurityNoticeMail(ctx, tx, input.Notice.Occurrence, input.Notice.Delivery, input.Notice.Job, payloadKeyID); err != nil {
			return nil, err
		}
		encoded, err := model.EncodeAuditData(result.Recovery.Auditable())
		if err != nil {
			return nil, err
		}
		if _, err := completeAuditEvent(ctx, tx, input.AuditEventID.String(), model.AuditStatusSuccess, "", encoded, at.UnixMilli()); err != nil {
			return nil, err
		}
		return result, nil
	})
}

// resetMFAUserAccess is the complete credential/recovery portion of the online
// and separately fenced offline reset aggregates. The caller holds this User's
// Session, MFA and PAT fences and commits its required audit/notice evidence in
// the same transaction. No primary credential is created or changed here.
func resetMFAUserAccess(ctx context.Context, tx *sqlxTxWrapper, userID model.UserID, at time.Time) (*store.MFAResetResult, error) {
	prior, err := getMFARecoveryState(ctx, tx, userID)
	if err != nil {
		return nil, err
	}
	at = model.TimeUTC(at)
	if prior.ResetAt.Valid && !at.After(prior.ResetAt.Time) {
		return nil, store.NewErrConflict("user_mfa_recovery", "reset_time", nil)
	}
	for _, table := range []string{"mfa_credentials", "mfa_recovery_codes"} {
		if _, err := tx.Exec(ctx, `UPDATE `+table+` SET updated_at=GREATEST(updated_at,?),archived_at=? WHERE user_id=? AND archived_at IS NULL`, at, at, userID.String()); err != nil {
			return nil, fmt.Errorf("retire reset MFA material: %w", err)
		}
	}
	if _, err := tx.Exec(ctx, `INSERT INTO user_mfa_recovery(user_id,generation,reset_at,reenrollment_required,updated_at) VALUES(?,1,?,true,?) ON CONFLICT(user_id) DO UPDATE SET generation=user_mfa_recovery.generation+1,reset_at=EXCLUDED.reset_at,reenrollment_required=true,updated_at=EXCLUDED.updated_at`, userID.String(), at, at); err != nil {
		return nil, err
	}
	rows, hashes, err := revokeAllUserSessionsAt(ctx, tx, userID.String(), at, model.SessionRevocationMFAReset)
	if err != nil {
		return nil, err
	}
	sessions := make([]*model.Session, 0, len(rows))
	for _, row := range rows {
		session, err := row.model()
		if err != nil {
			return nil, err
		}
		session.RevokedAt = model.OptionalTimeFrom(at)
		session.RevocationReason = model.SessionRevocationMFAReset
		if session.UpdatedAt.Before(at) {
			session.UpdatedAt = at
		}
		sessions = append(sessions, session)
	}
	if _, err := tx.Exec(ctx, `UPDATE personal_access_tokens SET updated_at=GREATEST(updated_at,?),revoked_at=? WHERE user_id=? AND revoked_at IS NULL`, at, at, userID.String()); err != nil {
		return nil, err
	}
	preparations := []personalAccessTokenPreparationRow{}
	if err := tx.Select(ctx, &preparations, `SELECT `+personalAccessTokenPreparationColumns+` FROM personal_access_token_mutation_preparations WHERE user_id=? ORDER BY id FOR UPDATE`, userID.String()); err != nil {
		return nil, err
	}
	for i := range preparations {
		if err := terminalizePersonalAccessTokenPreparation(ctx, tx, &preparations[i], model.AuditStatusFail, "authentication.mfa.reenrollment_required", nil); err != nil {
			return nil, err
		}
	}
	if _, err := tx.Exec(ctx, `UPDATE user_tokens SET updated_at=GREATEST(updated_at,?),archived_at=? WHERE user_id=? AND archived_at IS NULL AND consumed_at IS NULL`, at, at, userID.String()); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, `UPDATE audit_events a SET updated_at=GREATEST(a.updated_at,?),status='fail',error_code='authentication.mfa.reenrollment_required' FROM external_login_states s WHERE s.target_user_id=? AND s.audit_event_id=a.id AND a.status='attempt'`, at, userID.String()); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, `UPDATE external_login_states SET updated_at=GREATEST(updated_at,?),consumed_at=LEAST(GREATEST(created_at,?),expires_at-interval '1 microsecond') WHERE target_user_id=? AND consumed_at IS NULL`, at, at, userID.String()); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, `UPDATE browser_authentication_transactions SET state='cancelled',updated_at=?,cancelled_at=?,handle_hash=NULL,browser_proof_hash=NULL,state_hash=NULL,callback_url=NULL,code_challenge=NULL,proposed_public_jwk=NULL,proposed_key_thumbprint=NULL,desktop_release=NULL,desktop_build_id=NULL,desktop_platform=NULL,desktop_architecture=NULL,desktop_realtime_protocol=NULL,user_id=NULL,authentication_method=NULL,authentication_provider_id=NULL,external_identity_id=NULL,password_credential_id=NULL,password_credential_revision=NULL,authentication_strength=NULL,authenticated_at=NULL,mfa_completed_at=NULL,code_hash=NULL,code_expires_at=NULL WHERE user_id=? AND purpose='desktop_authorization' AND state IN ('authenticated','code_issued')`, at, at, userID.String()); err != nil {
		return nil, err
	}
	// Execution resources lose authority immediately; their existing durable
	// release reconciliation performs the physical host teardown afterwards.
	if _, err := tx.Exec(ctx, `UPDATE execution_grants g SET state='released',released_at=?,updated_at=GREATEST(g.updated_at,?),lifecycle_pending=false,pending_sitting_state=NULL,pending_sitting_revision=NULL,workspace_pending=false,pending_workspace_cursor=0,revision=g.revision+1 FROM exam_attempts a WHERE a.id=g.exam_attempt_id AND a.candidate_user_id=? AND g.state IN ('reserved','ready')`, at, at, userID.String()); err != nil {
		return nil, err
	}
	recovery, err := getMFARecoveryState(ctx, tx, userID)
	if err != nil {
		return nil, err
	}
	return &store.MFAResetResult{Recovery: recovery, Sessions: sessions, AccessTokenHashes: hashes}, nil
}
