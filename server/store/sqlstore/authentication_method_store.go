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
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/lib/pq"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

func (s SQLExternalIdentityStore) LinkWithAudit(ctx context.Context, input *store.ExternalIdentityLink) (*store.AuthenticationMethodMutationResult, error) {
	if input == nil || input.Identity == nil || !input.Identity.ID.IsZero() ||
		!input.Identity.UserID.IsValid() || !model.IsValidId(input.AuditEventID) || input.AuditAt <= 0 ||
		!validAccessDeploymentCapabilities(input.Capabilities) {
		return nil, store.NewErrInvalidInput("external_identity", "link", nil)
	}
	identity := *input.Identity
	identity.Provider = normalizeProviderID(identity.Provider)
	if _, ok := input.Capabilities.Providers[identity.Provider]; !ok {
		return nil, store.ErrAuthenticationMethodDisabled
	}
	return runSQLTransaction(ctx, s.GetMaster().Begin, "link external identity", func(ctx context.Context, tx *sqlxTxWrapper) (*store.AuthenticationMethodMutationResult, error) {
		if err := lockSystemAdministratorAuthenticationPaths(ctx, tx); err != nil {
			return nil, err
		}
		if err := requireActiveUser(ctx, tx, identity.UserID.String(), false); err != nil {
			return nil, err
		}
		if err := requireCurrentExternalProvider(ctx, tx, identity.Provider); err != nil {
			return nil, err
		}
		identity.PrepareCreate(model.NewExternalIdentityID(), model.TimeFromMillis(input.AuditAt))
		if err := identity.Validate(); err != nil {
			return nil, err
		}
		if err := insertExternalIdentity(ctx, tx, &identity); err != nil {
			return nil, err
		}
		if err := completeAuthenticationMethodAudit(ctx, tx, input.AuditEventID, input.AuditAt, identity.Auditable()); err != nil {
			return nil, err
		}
		return &store.AuthenticationMethodMutationResult{Identity: &identity, RevokedSessions: []*model.Session{}, RevokedTokenHashes: []string{}}, nil
	})
}

func (s SQLExternalIdentityStore) UnlinkWithAudit(ctx context.Context, input *store.ExternalIdentityUnlink) (*store.AuthenticationMethodMutationResult, error) {
	if input == nil || !input.ID.IsValid() || !input.UserID.IsValid() || input.ChangedAt <= 0 ||
		!input.RevocationReason.IsValid() || !model.IsValidId(input.AuditEventID) || input.AuditAt <= 0 ||
		!validAccessDeploymentCapabilities(input.Capabilities) {
		return nil, store.NewErrInvalidInput("external_identity", "unlink", nil)
	}
	return runSQLTransaction(ctx, s.GetMaster().Begin, "unlink external identity", func(ctx context.Context, tx *sqlxTxWrapper) (*store.AuthenticationMethodMutationResult, error) {
		if err := lockSystemAdministratorAuthenticationPaths(ctx, tx); err != nil {
			return nil, err
		}
		if err := lockUserSessions(ctx, tx, input.UserID.String()); err != nil {
			return nil, err
		}
		if err := requireActiveUser(ctx, tx, input.UserID.String(), false); err != nil {
			return nil, err
		}
		policy, err := getAccessPolicy(ctx, tx, "FOR SHARE")
		if err != nil {
			return nil, err
		}
		identity, err := getExternalIdentityForUpdate(ctx, tx, input.ID.String(), input.UserID.String())
		if err != nil {
			return nil, err
		}
		usable, err := hasUsableUserAuthenticationPath(ctx, tx, input.UserID.String(), policy.Settings(), input.Capabilities, false, identity.ID.String())
		if err != nil {
			return nil, err
		}
		if !usable {
			return nil, store.ErrLastUsableAuthenticationMethod
		}
		var at time.Time
		if err := tx.Get(ctx, &at, `SELECT clock_timestamp()`); err != nil {
			return nil, fmt.Errorf("read external identity unlink time: %w", err)
		}
		at = model.TimeUTC(at)
		if _, err := tx.Exec(ctx, `UPDATE external_identities SET updated_at=GREATEST(updated_at, ?), archived_at=? WHERE id=? AND user_id=? AND archived_at IS NULL`, at, at, identity.ID.String(), input.UserID.String()); err != nil {
			return nil, fmt.Errorf("archive external identity: %w", err)
		}
		identity.UpdatedAt, identity.ArchivedAt = at, model.OptionalTimeFrom(at)
		rows, hashes, err := revokeUserSessionsForAuthenticationMethod(ctx, tx, input.UserID.String(), "", identity.ID, at, input.RevocationReason)
		if err != nil {
			return nil, err
		}
		sessions, err := revokedSessionModelsAt(rows, at, input.RevocationReason)
		if err != nil {
			return nil, err
		}
		if err := completeAuthenticationMethodAudit(ctx, tx, input.AuditEventID, input.AuditAt, identity.Auditable()); err != nil {
			return nil, err
		}
		return &store.AuthenticationMethodMutationResult{Identity: identity, RevokedSessions: sessions, RevokedTokenHashes: hashes}, nil
	})
}

func (s SQLPasswordCredentialStore) EnrollWithAudit(ctx context.Context, input *store.PasswordCredentialEnrollment) (*store.AuthenticationMethodMutationResult, error) {
	if input == nil || input.Credential == nil || !input.Credential.ID.IsZero() || !input.Credential.UserID.IsValid() ||
		!model.IsValidId(input.AuditEventID) || input.AuditAt <= 0 || !validAccessDeploymentCapabilities(input.Capabilities) {
		return nil, store.NewErrInvalidInput("password_credential", "enroll", nil)
	}
	credential := *input.Credential
	return runSQLTransaction(ctx, s.GetMaster().Begin, "enroll password credential", func(ctx context.Context, tx *sqlxTxWrapper) (*store.AuthenticationMethodMutationResult, error) {
		if err := lockSystemAdministratorAuthenticationPaths(ctx, tx); err != nil {
			return nil, err
		}
		if err := requireActiveUser(ctx, tx, credential.UserID.String(), true); err != nil {
			return nil, err
		}
		if err := requireCurrentLocalLogin(ctx, tx); err != nil {
			return nil, err
		}
		credential.PrepareCreate(model.NewPasswordCredentialID(), model.TimeFromMillis(input.AuditAt))
		if err := credential.Validate(); err != nil {
			return nil, err
		}
		if err := insertPasswordCredential(ctx, tx, &credential); err != nil {
			return nil, err
		}
		if err := completeAuthenticationMethodAudit(ctx, tx, input.AuditEventID, input.AuditAt, credential.Auditable()); err != nil {
			return nil, err
		}
		return &store.AuthenticationMethodMutationResult{PasswordCredential: &credential, RevokedSessions: []*model.Session{}, RevokedTokenHashes: []string{}}, nil
	})
}

func (s SQLPasswordCredentialStore) RemoveWithAudit(ctx context.Context, input *store.PasswordCredentialRemoval) (*store.AuthenticationMethodMutationResult, error) {
	if input == nil || !input.UserID.IsValid() || input.ChangedAt <= 0 || !input.RevocationReason.IsValid() ||
		!model.IsValidId(input.AuditEventID) || input.AuditAt <= 0 || !validAccessDeploymentCapabilities(input.Capabilities) {
		return nil, store.NewErrInvalidInput("password_credential", "remove", nil)
	}
	return runSQLTransaction(ctx, s.GetMaster().Begin, "remove password credential", func(ctx context.Context, tx *sqlxTxWrapper) (*store.AuthenticationMethodMutationResult, error) {
		if err := lockSystemAdministratorAuthenticationPaths(ctx, tx); err != nil {
			return nil, err
		}
		if err := lockUserSessions(ctx, tx, input.UserID.String()); err != nil {
			return nil, err
		}
		if err := requireActiveUser(ctx, tx, input.UserID.String(), false); err != nil {
			return nil, err
		}
		policy, err := getAccessPolicy(ctx, tx, "FOR SHARE")
		if err != nil {
			return nil, err
		}
		credential, err := getPasswordCredentialForUpdate(ctx, tx, input.UserID.String())
		if err != nil {
			return nil, err
		}
		usable, err := hasUsableUserAuthenticationPath(ctx, tx, input.UserID.String(), policy.Settings(), input.Capabilities, true, "")
		if err != nil {
			return nil, err
		}
		if !usable {
			return nil, store.ErrLastUsableAuthenticationMethod
		}
		at := model.TimeFromMillis(input.ChangedAt)
		if _, err := tx.Exec(ctx, `UPDATE password_credentials SET updated_at=GREATEST(updated_at, ?), archived_at=? WHERE id=? AND user_id=? AND archived_at IS NULL`, at, at, credential.ID.String(), input.UserID.String()); err != nil {
			return nil, fmt.Errorf("archive password credential: %w", err)
		}
		credential.UpdatedAt, credential.ArchivedAt = at, model.OptionalTimeFrom(at)
		if _, err := tx.Exec(ctx, `UPDATE user_tokens SET updated_at=GREATEST(updated_at, ?), archived_at=? WHERE user_id=? AND purpose='password_reset' AND archived_at IS NULL AND consumed_at IS NULL`, at, at, input.UserID.String()); err != nil {
			return nil, fmt.Errorf("invalidate password recovery state: %w", err)
		}
		rows, hashes, err := revokeUserSessionsForAuthenticationMethod(ctx, tx, input.UserID.String(), "password", "", at, input.RevocationReason)
		if err != nil {
			return nil, err
		}
		sessions, err := revokedSessionModelsAt(rows, at, input.RevocationReason)
		if err != nil {
			return nil, err
		}
		if err := completeAuthenticationMethodAudit(ctx, tx, input.AuditEventID, input.AuditAt, credential.Auditable()); err != nil {
			return nil, err
		}
		return &store.AuthenticationMethodMutationResult{PasswordCredential: credential, RevokedSessions: sessions, RevokedTokenHashes: hashes}, nil
	})
}

func requireActiveUser(ctx context.Context, tx *sqlxTxWrapper, userID string, requireVerifiedEmail bool) error {
	var active bool
	err := tx.Get(ctx, &active, `SELECT archived_at IS NULL AND disabled_at IS NULL AND (NOT ? OR email_verified) FROM users WHERE id=? FOR UPDATE`, requireVerifiedEmail, userID)
	if errors.Is(err, sql.ErrNoRows) {
		return store.NewErrNotFound("user", userID).Wrap(err)
	}
	if err != nil {
		return fmt.Errorf("lock authentication method user: %w", err)
	}
	if !active {
		return store.NewErrConflict("user", "authentication_method_user_ineligible", nil)
	}
	return nil
}

func hasUsableUserAuthenticationPath(ctx context.Context, executor sqlxExecutor, userID string, settings model.AccessPolicySettings, capabilities store.AccessDeploymentCapabilities, excludePassword bool, excludedIdentityID string) (bool, error) {
	providers := make([]string, 0, len(settings.ProviderAdmissions))
	for provider := range settings.ProviderAdmissions {
		if _, ok := capabilities.Providers[provider]; ok {
			providers = append(providers, provider)
		}
	}
	sort.Strings(providers)
	var found bool
	err := executor.Get(ctx, &found, `SELECT ((? AND NOT ? AND EXISTS (SELECT 1 FROM password_credentials WHERE user_id=? AND archived_at IS NULL)) OR EXISTS (SELECT 1 FROM external_identities WHERE user_id=? AND archived_at IS NULL AND provider=ANY(?) AND (?='' OR id<>?)))`, settings.LocalLoginEnabled, excludePassword, userID, userID, pq.Array(providers), excludedIdentityID, excludedIdentityID)
	return found, err
}

func getExternalIdentityForUpdate(ctx context.Context, tx *sqlxTxWrapper, id, userID string) (*model.ExternalIdentity, error) {
	var row externalIdentityRow
	if err := tx.Get(ctx, &row, `SELECT id, created_at, updated_at, archived_at, user_id, provider, subject, last_seen_at FROM external_identities WHERE id=? AND user_id=? AND archived_at IS NULL FOR UPDATE`, id, userID); err != nil {
		return nil, translateError("external_identity", id, err)
	}
	return row.model()
}

func getPasswordCredentialForUpdate(ctx context.Context, tx *sqlxTxWrapper, userID string) (*model.PasswordCredential, error) {
	var row passwordCredentialRow
	if err := tx.Get(ctx, &row, `SELECT id, created_at, updated_at, archived_at, user_id, password_hash, password_changed_at FROM password_credentials WHERE user_id=? AND archived_at IS NULL FOR UPDATE`, userID); err != nil {
		return nil, translateError("password_credential", userID, err)
	}
	return row.model()
}

func revokeUserSessionsForAuthenticationMethod(ctx context.Context, executor sqlxExecutor, userID, method string, identityID model.ExternalIdentityID, at time.Time, reason model.SessionRevocationReason) ([]sessionRow, []string, error) {
	rows := []sessionRow{}
	if err := executor.Select(ctx, &rows, `SELECT id, created_at, updated_at, archived_at, user_id, client_type, desktop_registration_id, dpop_key_thumbprint, desktop_release, desktop_build_id, desktop_platform, desktop_architecture, desktop_realtime_protocol, device_id, device_name, authentication_method, authentication_provider_id, external_identity_id, authentication_strength, authenticated_at, mfa_completed_at, last_activity_at, idle_expires_at, expires_at, revoked_at, revocation_reason FROM sessions WHERE user_id=? AND archived_at IS NULL AND revoked_at IS NULL AND ((?<>'' AND external_identity_id=?) OR (?='' AND authentication_method=? AND authentication_provider_id='')) FOR UPDATE`, userID, identityID.String(), identityID.String(), identityID.String(), method); err != nil {
		return nil, nil, err
	}
	if len(rows) == 0 {
		return rows, []string{}, nil
	}
	ids := make([]string, 0, len(rows))
	for _, row := range rows {
		ids = append(ids, row.ID)
	}
	hashes := []string{}
	if err := executor.Select(ctx, &hashes, `SELECT token_hash FROM session_credentials WHERE session_id=ANY(?) AND archived_at IS NULL AND revoked_at IS NULL FOR UPDATE`, pq.Array(ids)); err != nil {
		return nil, nil, err
	}
	if _, err := executor.Exec(ctx, `UPDATE session_credentials SET updated_at=GREATEST(updated_at, ?), revoked_at=? WHERE session_id=ANY(?) AND archived_at IS NULL AND revoked_at IS NULL`, at, at, pq.Array(ids)); err != nil {
		return nil, nil, err
	}
	if _, err := executor.Exec(ctx, `UPDATE sessions SET updated_at=GREATEST(updated_at, ?), revoked_at=?, revocation_reason=? WHERE id=ANY(?) AND archived_at IS NULL AND revoked_at IS NULL`, at, at, string(reason), pq.Array(ids)); err != nil {
		return nil, nil, err
	}
	return rows, hashes, nil
}

func completeAuthenticationMethodAudit(ctx context.Context, tx *sqlxTxWrapper, auditID string, auditAt int64, result any) error {
	encoded, err := model.EncodeAuditData(result)
	if err != nil {
		return err
	}
	_, err = completeAuditEvent(ctx, tx, auditID, model.AuditStatusSuccess, "", encoded, auditAt)
	return err
}

func normalizeProviderID(value string) string { return strings.ToLower(strings.TrimSpace(value)) }
