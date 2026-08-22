// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package sqlstore

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/lib/pq"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

type SQLAccessPolicyStore struct{ *SQLStore }

func newSQLAccessPolicyStore(sqlStore *SQLStore) store.AccessPolicyStore {
	return &SQLAccessPolicyStore{SQLStore: sqlStore}
}

type accessPolicyRow struct {
	ID                               string    `db:"id"`
	Revision                         int64     `db:"revision"`
	CreatedAt                        time.Time `db:"created_at"`
	UpdatedAt                        time.Time `db:"updated_at"`
	LocalLoginEnabled                bool      `db:"local_login_enabled"`
	PublicRegistrationEnabled        bool      `db:"public_registration_enabled"`
	InvitationAdmissionEnabled       bool      `db:"invitation_admission_enabled"`
	InvitationLocalCredentialEnabled bool      `db:"invitation_local_credential_enabled"`
	DesktopAuthorizationEnabled      bool      `db:"desktop_authorization_enabled"`
	ProviderAdmissions               jsonValue `db:"provider_admissions"`
}

type accessPolicyTransitionRow struct {
	PolicyID      string         `db:"access_policy_id"`
	FromRevision  int64          `db:"from_revision"`
	ToRevision    int64          `db:"to_revision"`
	ActorID       string         `db:"actor_user_id"`
	ChangedFields pq.StringArray `db:"changed_fields"`
	ChangedAt     time.Time      `db:"changed_at"`
	Outcome       string         `db:"outcome"`
}

func (s SQLAccessPolicyStore) Get(ctx context.Context, historyLimit int) (*store.AccessPolicySnapshot, error) {
	if historyLimit < 0 || historyLimit > model.AccessPolicyTransitionHistoryLimit {
		return nil, store.NewErrInvalidInput("access_policy", "history_limit", historyLimit)
	}
	policy, err := getAccessPolicy(ctx, s.GetMaster(), "")
	if err != nil {
		return nil, err
	}
	history, err := listAccessPolicyHistory(ctx, s.GetMaster(), policy.ID, historyLimit)
	if err != nil {
		return nil, err
	}
	return &store.AccessPolicySnapshot{Policy: policy, History: history}, nil
}

func (s SQLAccessPolicyStore) Preflight(ctx context.Context, input *store.AccessPolicyPreflight) ([]store.AccessPolicyBlocker, error) {
	if err := validateAccessPolicyPreflight(input); err != nil {
		return nil, err
	}
	return runSQLTransaction(ctx, s.GetMaster().Begin, "access policy preflight", func(ctx context.Context, tx *sqlxTxWrapper) ([]store.AccessPolicyBlocker, error) {
		current, err := getAccessPolicy(ctx, tx, "FOR SHARE")
		if err != nil {
			return nil, err
		}
		if current.Revision != input.ExpectedRevision {
			return nil, &store.ErrAccessPolicyRevisionConflict{CurrentRevision: current.Revision}
		}
		var databaseNow time.Time
		if err := tx.Get(ctx, &databaseNow, `SELECT CURRENT_TIMESTAMP`); err != nil {
			return nil, fmt.Errorf("read access policy preflight time: %w", err)
		}
		return accessPolicyBlockers(ctx, tx, current, input.Settings, input.Capabilities, databaseNow)
	})
}

type accessPolicyReplacementOutcome struct {
	Result              *store.AccessPolicyReplacementResult `json:"result"`
	ChangedFields       []string                             `json:"changed_fields"`
	RevokedSessionCount int                                  `json:"revoked_session_count"`
}

func (s SQLAccessPolicyStore) Replace(ctx context.Context, input *store.AccessPolicyReplacement, command *store.CommandIdempotency) (*store.AccessPolicyReplacementResult, error) {
	if err := validateAccessPolicyReplacement(input, command); err != nil {
		return nil, err
	}
	mutation, err := runIdempotentMutation(ctx, s.SQLStore, "access policy replacement", idempotentMutation[accessPolicyReplacementOutcome]{
		command: command, auditEventID: input.AuditEventID,
		execute: func(ctx context.Context, tx *sqlxTxWrapper) (accessPolicyReplacementOutcome, error) {
			if err := lockSystemAdministratorAuthenticationPaths(ctx, tx); err != nil {
				return accessPolicyReplacementOutcome{}, err
			}
			current, err := getAccessPolicy(ctx, tx, "FOR UPDATE")
			if err != nil {
				return accessPolicyReplacementOutcome{}, err
			}
			if current.Revision != input.Preflight.ExpectedRevision {
				return accessPolicyReplacementOutcome{}, &store.ErrAccessPolicyRevisionConflict{CurrentRevision: current.Revision}
			}
			var databaseNow time.Time
			if err := tx.Get(ctx, &databaseNow, `SELECT CURRENT_TIMESTAMP`); err != nil {
				return accessPolicyReplacementOutcome{}, fmt.Errorf("read access policy mutation time: %w", err)
			}
			blockers, err := accessPolicyBlockers(ctx, tx, current, input.Preflight.Settings, input.Preflight.Capabilities, databaseNow)
			if err != nil {
				return accessPolicyReplacementOutcome{}, err
			}
			if len(blockers) != 0 {
				return accessPolicyReplacementOutcome{}, &store.ErrAccessPolicyBlocked{Blockers: blockers}
			}
			candidate := current.Clone()
			transition, err := candidate.Replace(current.Revision, input.Preflight.Settings, input.ActorID, databaseNow)
			if err != nil {
				return accessPolicyReplacementOutcome{}, store.NewErrInvalidInput("access_policy", "replacement", nil).Wrap(err)
			}
			changed := transition != nil
			revocations := []store.AccessPolicySessionRevocation{}
			if changed && input.Preflight.RevokeExistingSessions {
				transition.ChangedFields = append(transition.ChangedFields, "revoke_existing_sessions")
				sort.Strings(transition.ChangedFields)
				if err := transition.Validate(); err != nil {
					return accessPolicyReplacementOutcome{}, store.NewErrInvalidInput("access_policy", "transition", nil).Wrap(err)
				}
				revocations, err = revokeSessionsForDisabledAccessMethods(ctx, tx, current, candidate, databaseNow)
				if err != nil {
					return accessPolicyReplacementOutcome{}, err
				}
			}
			if changed {
				providers, encodeErr := json.Marshal(candidate.ProviderAdmissions)
				if encodeErr != nil {
					return accessPolicyReplacementOutcome{}, store.NewErrInvalidInput("access_policy", "provider_admissions", nil).Wrap(encodeErr)
				}
				result, updateErr := tx.Exec(ctx, `UPDATE access_policies SET revision=?, updated_at=?, local_login_enabled=?,
					public_registration_enabled=?, invitation_admission_enabled=?, invitation_local_credential_enabled=?,
					desktop_authorization_enabled=?, provider_admissions=?::jsonb WHERE singleton=1 AND revision=?`,
					candidate.Revision, candidate.UpdatedAt, candidate.LocalLoginEnabled, candidate.PublicRegistrationEnabled,
					candidate.InvitationAdmissionEnabled, candidate.InvitationLocalCredentialEnabled,
					candidate.DesktopAuthorizationEnabled, providers, current.Revision)
				if updateErr != nil {
					return accessPolicyReplacementOutcome{}, fmt.Errorf("replace access policy: %w", translateError("access_policy", candidate.ID.String(), updateErr))
				}
				if err := requireRevisionAffected(ctx, tx, result, "access_policy", "access_policies", candidate.ID.String()); err != nil {
					return accessPolicyReplacementOutcome{}, err
				}
				if err := insertAccessPolicyTransition(ctx, tx, transition); err != nil {
					return accessPolicyReplacementOutcome{}, err
				}
				if _, err := tx.Exec(ctx, `DELETE FROM access_policy_transitions WHERE access_policy_id=? AND to_revision NOT IN
					(SELECT to_revision FROM access_policy_transitions WHERE access_policy_id=? ORDER BY to_revision DESC LIMIT ?)`,
					candidate.ID.String(), candidate.ID.String(), model.AccessPolicyTransitionHistoryLimit); err != nil {
					return accessPolicyReplacementOutcome{}, fmt.Errorf("bound access policy transition history: %w", err)
				}
			}
			changedFields := []string{}
			if transition != nil {
				changedFields = append(changedFields, transition.ChangedFields...)
			}
			history, err := listAccessPolicyHistory(ctx, tx, candidate.ID, model.AccessPolicyTransitionHistoryLimit)
			if err != nil {
				return accessPolicyReplacementOutcome{}, err
			}
			outcome := accessPolicyReplacementOutcome{Result: &store.AccessPolicyReplacementResult{
				Snapshot: &store.AccessPolicySnapshot{Policy: candidate, History: history}, Changed: changed,
				SessionRevocations: revocations,
			}, ChangedFields: changedFields, RevokedSessionCount: accessPolicyRevokedSessionCount(revocations)}
			if err := completeAccessPolicyReplacementAudit(ctx, tx, outcome, input, false, ""); err != nil {
				return accessPolicyReplacementOutcome{}, err
			}
			return outcome, nil
		},
		encode: func(outcome accessPolicyReplacementOutcome) ([]byte, error) {
			retained := outcome
			result := *outcome.Result
			// Revocation identifiers and token hashes are post-commit effects, not
			// part of the public retained response. Replays must never emit them.
			result.SessionRevocations = []store.AccessPolicySessionRevocation{}
			retained.Result = &result
			return json.Marshal(&retained)
		},
		decode: decodeAccessPolicyReplacementOutcome,
		completeReplay: func(ctx context.Context, tx *sqlxTxWrapper, outcome accessPolicyReplacementOutcome, originalAuditID string) error {
			return completeAccessPolicyReplacementAudit(ctx, tx, outcome, input, true, originalAuditID)
		},
	})
	if err != nil {
		return nil, err
	}
	result := mutation.Value.Result
	result.Replayed = mutation.Replayed
	return result, nil
}

func decodeAccessPolicyReplacementOutcome(version int, encoded []byte) (accessPolicyReplacementOutcome, error) {
	if version != 1 {
		return accessPolicyReplacementOutcome{}, invalidPersistedState("command_outcome", "outcome_version", errors.New("unsupported access policy outcome version"))
	}
	var outcome accessPolicyReplacementOutcome
	if err := json.Unmarshal(encoded, &outcome); err != nil || outcome.Result == nil || outcome.Result.Snapshot == nil || outcome.Result.Snapshot.Policy == nil || outcome.Result.Snapshot.Policy.Validate() != nil ||
		outcome.RevokedSessionCount < 0 || len(outcome.ChangedFields) > 7 {
		return accessPolicyReplacementOutcome{}, invalidPersistedState("command_outcome", "outcome", errors.New("invalid access policy outcome"))
	}
	for _, transition := range outcome.Result.Snapshot.History {
		if transition == nil || transition.Validate() != nil {
			return accessPolicyReplacementOutcome{}, invalidPersistedState("command_outcome", "outcome", errors.New("invalid access policy history outcome"))
		}
	}
	for _, revocation := range outcome.Result.SessionRevocations {
		if !revocation.UserID.IsValid() || len(revocation.SessionIDs) == 0 {
			return accessPolicyReplacementOutcome{}, invalidPersistedState("command_outcome", "outcome", errors.New("invalid access policy session revocation outcome"))
		}
		for _, id := range revocation.SessionIDs {
			if !id.IsValid() {
				return accessPolicyReplacementOutcome{}, invalidPersistedState("command_outcome", "outcome", errors.New("invalid access policy revoked session"))
			}
		}
	}
	return outcome, nil
}

func completeAccessPolicyReplacementAudit(ctx context.Context, tx *sqlxTxWrapper, outcome accessPolicyReplacementOutcome,
	input *store.AccessPolicyReplacement, replayed bool, originalAuditID string,
) error {
	data := map[string]any{
		"operation": "replace", "expected_revision": input.Preflight.ExpectedRevision,
		"resulting_revision":       outcome.Result.Snapshot.Policy.Revision,
		"changed_fields":           append([]string(nil), outcome.ChangedFields...),
		"revoke_existing_sessions": input.Preflight.RevokeExistingSessions,
		"revoked_session_count":    outcome.RevokedSessionCount,
	}
	if replayed {
		data["idempotency_replayed"] = true
		data["original_audit_event_id"] = originalAuditID
	}
	encoded, err := model.EncodeAuditData(data)
	if err != nil {
		return store.NewErrInvalidInput("access_policy", "audit", nil).Wrap(err)
	}
	if _, err = completeAuditEvent(ctx, tx, input.AuditEventID, model.AuditStatusSuccess, "", encoded, input.AuditAt); err != nil {
		return fmt.Errorf("complete access policy replacement audit: %w", err)
	}
	return nil
}

func disabledAccessPolicyAuthenticationMethods(current, candidate *model.AccessPolicy) (bool, []string) {
	providers := make([]string, 0, len(current.ProviderAdmissions))
	for providerID := range current.ProviderAdmissions {
		if _, enabled := candidate.ProviderAdmissions[providerID]; !enabled {
			providers = append(providers, providerID)
		}
	}
	sort.Strings(providers)
	return current.LocalLoginEnabled && !candidate.LocalLoginEnabled, providers
}

type accessPolicySessionCredentialRow struct {
	UserID    string `db:"user_id"`
	SessionID string `db:"session_id"`
	TokenHash string `db:"token_hash"`
}

func revokeSessionsForDisabledAccessMethods(ctx context.Context, tx *sqlxTxWrapper, current, candidate *model.AccessPolicy, at time.Time) ([]store.AccessPolicySessionRevocation, error) {
	localDisabled, providerIDs := disabledAccessPolicyAuthenticationMethods(current, candidate)
	if !localDisabled && len(providerIDs) == 0 {
		return []store.AccessPolicySessionRevocation{}, nil
	}
	// AuthenticationMethod identifies the protocol (password, oidc, cas), while
	// AuthenticationProviderID identifies the immutable configured provider.
	// Never infer provider identity from the protocol: provider IDs may collide
	// with protocol or local-method names.
	providerArray := pq.Array(providerIDs)
	var sessionRows []struct {
		UserID    string `db:"user_id"`
		SessionID string `db:"session_id"`
	}
	if err := tx.Select(ctx, &sessionRows, `SELECT user_id, id AS session_id FROM sessions
		WHERE archived_at IS NULL AND revoked_at IS NULL AND expires_at>? AND idle_expires_at>?
		AND ((? AND authentication_method='password' AND authentication_provider_id='')
			OR authentication_provider_id=ANY(?))
		ORDER BY user_id, id FOR UPDATE`, at, at, localDisabled, providerArray); err != nil {
		return nil, fmt.Errorf("select access-policy sessions for revocation: %w", err)
	}
	if len(sessionRows) == 0 {
		return []store.AccessPolicySessionRevocation{}, nil
	}
	credentialRows := []accessPolicySessionCredentialRow{}
	if err := tx.Select(ctx, &credentialRows, `SELECT s.user_id, s.id AS session_id, c.token_hash
		FROM sessions s JOIN session_credentials c ON c.session_id=s.id
		WHERE s.archived_at IS NULL AND s.revoked_at IS NULL AND s.expires_at>? AND s.idle_expires_at>?
		AND ((? AND s.authentication_method='password' AND s.authentication_provider_id='')
			OR s.authentication_provider_id=ANY(?))
		AND c.kind='access' AND c.archived_at IS NULL AND c.revoked_at IS NULL
		ORDER BY s.user_id, s.id`, at, at, localDisabled, providerArray); err != nil {
		return nil, fmt.Errorf("select access-policy session credentials for revocation: %w", err)
	}
	if _, err := tx.Exec(ctx, `UPDATE session_credentials c SET updated_at=GREATEST(c.updated_at, ?), revoked_at=?
		FROM sessions s WHERE s.id=c.session_id AND s.archived_at IS NULL AND s.revoked_at IS NULL
		AND s.expires_at>? AND s.idle_expires_at>?
		AND ((? AND s.authentication_method='password' AND s.authentication_provider_id='')
			OR s.authentication_provider_id=ANY(?))
		AND c.archived_at IS NULL AND c.revoked_at IS NULL`, at, at, at, at, localDisabled, providerArray); err != nil {
		return nil, fmt.Errorf("revoke access-policy session credentials: %w", err)
	}
	if _, err := tx.Exec(ctx, `UPDATE sessions SET updated_at=GREATEST(updated_at, ?), revoked_at=?, revocation_reason=?
		WHERE archived_at IS NULL AND revoked_at IS NULL AND expires_at>? AND idle_expires_at>?
		AND ((? AND authentication_method='password' AND authentication_provider_id='')
			OR authentication_provider_id=ANY(?))`,
		at, at, string(model.SessionRevocationAccessPolicyChanged), at, at, localDisabled, providerArray); err != nil {
		return nil, fmt.Errorf("revoke access-policy sessions: %w", err)
	}
	byUser := make(map[string]*store.AccessPolicySessionRevocation)
	userOrder := make([]string, 0)
	for _, row := range sessionRows {
		entry := byUser[row.UserID]
		if entry == nil {
			userID, err := model.ParseUserID(row.UserID)
			if err != nil {
				return nil, invalidPersistedState("session", "user_id", err)
			}
			entry = &store.AccessPolicySessionRevocation{UserID: userID, SessionIDs: []model.SessionID{}, AccessTokenHashes: []string{}}
			byUser[row.UserID], userOrder = entry, append(userOrder, row.UserID)
		}
		sessionID, err := model.ParseSessionID(row.SessionID)
		if err != nil {
			return nil, invalidPersistedState("session", "id", err)
		}
		entry.SessionIDs = append(entry.SessionIDs, sessionID)
	}
	for _, row := range credentialRows {
		if entry := byUser[row.UserID]; entry != nil {
			entry.AccessTokenHashes = append(entry.AccessTokenHashes, row.TokenHash)
		}
	}
	result := make([]store.AccessPolicySessionRevocation, 0, len(userOrder))
	for _, userID := range userOrder {
		result = append(result, *byUser[userID])
	}
	return result, nil
}

func accessPolicyRevokedSessionCount(revocations []store.AccessPolicySessionRevocation) int {
	total := 0
	for _, revocation := range revocations {
		total += len(revocation.SessionIDs)
	}
	return total
}

func getAccessPolicy(ctx context.Context, executor sqlxExecutor, lock string) (*model.AccessPolicy, error) {
	var row accessPolicyRow
	query := `SELECT id, revision, created_at, updated_at, local_login_enabled, public_registration_enabled,
		invitation_admission_enabled, invitation_local_credential_enabled, desktop_authorization_enabled, provider_admissions
		FROM access_policies WHERE singleton=1`
	if lock != "" {
		query += " " + lock
	}
	if err := executor.Get(ctx, &row, query); err != nil {
		return nil, translateError("access_policy", "singleton", err)
	}
	return row.model()
}

func (row accessPolicyRow) model() (*model.AccessPolicy, error) {
	id, err := model.ParseAccessPolicyID(row.ID)
	if err != nil {
		return nil, invalidPersistedState("access_policy", "id", err)
	}
	providers := map[string]model.ProviderAdmissionMode{}
	if err := json.Unmarshal(row.ProviderAdmissions, &providers); err != nil {
		return nil, invalidPersistedState("access_policy", "provider_admissions", err)
	}
	policy := &model.AccessPolicy{ID: id, Revision: row.Revision, CreatedAt: model.TimeUTC(row.CreatedAt), UpdatedAt: model.TimeUTC(row.UpdatedAt),
		LocalLoginEnabled: row.LocalLoginEnabled, PublicRegistrationEnabled: row.PublicRegistrationEnabled,
		InvitationAdmissionEnabled: row.InvitationAdmissionEnabled, InvitationLocalCredentialEnabled: row.InvitationLocalCredentialEnabled,
		DesktopAuthorizationEnabled: row.DesktopAuthorizationEnabled, ProviderAdmissions: providers}
	if err := validatePersistedModel("access_policy", policy); err != nil {
		return nil, err
	}
	return policy, nil
}

func listAccessPolicyHistory(ctx context.Context, executor sqlxExecutor, policyID model.AccessPolicyID, limit int) ([]*model.AccessPolicyTransition, error) {
	if limit == 0 {
		return []*model.AccessPolicyTransition{}, nil
	}
	rows := []accessPolicyTransitionRow{}
	if err := executor.Select(ctx, &rows, `SELECT access_policy_id, from_revision, to_revision, actor_user_id, changed_fields, changed_at, outcome
		FROM access_policy_transitions WHERE access_policy_id=? ORDER BY to_revision DESC LIMIT ?`, policyID.String(), limit); err != nil {
		return nil, fmt.Errorf("list access policy transitions: %w", err)
	}
	result := make([]*model.AccessPolicyTransition, 0, len(rows))
	for _, row := range rows {
		transition, err := row.model()
		if err != nil {
			return nil, err
		}
		result = append(result, transition)
	}
	return result, nil
}

func (row accessPolicyTransitionRow) model() (*model.AccessPolicyTransition, error) {
	policyID, err := model.ParseAccessPolicyID(row.PolicyID)
	if err != nil {
		return nil, invalidPersistedState("access_policy_transition", "access_policy_id", err)
	}
	actorID, err := model.ParseUserID(row.ActorID)
	if err != nil {
		return nil, invalidPersistedState("access_policy_transition", "actor_user_id", err)
	}
	transition := &model.AccessPolicyTransition{PolicyID: policyID, FromRevision: row.FromRevision, ToRevision: row.ToRevision,
		ActorID: actorID, ChangedFields: append([]string(nil), row.ChangedFields...), ChangedAt: model.TimeUTC(row.ChangedAt),
		Outcome: model.AccessPolicyTransitionOutcome(row.Outcome)}
	if err := transition.Validate(); err != nil {
		return nil, invalidPersistedState("access_policy_transition", "value", err)
	}
	return transition, nil
}

func insertAccessPolicyTransition(ctx context.Context, executor sqlxExecutor, transition *model.AccessPolicyTransition) error {
	if transition == nil || transition.Validate() != nil {
		return store.NewErrInvalidInput("access_policy_transition", "value", nil)
	}
	_, err := executor.Exec(ctx, `INSERT INTO access_policy_transitions (access_policy_id, from_revision, to_revision, actor_user_id, changed_fields, changed_at, outcome)
		VALUES (?, ?, ?, ?, ?, ?, ?)`, transition.PolicyID.String(), transition.FromRevision, transition.ToRevision,
		transition.ActorID.String(), pq.Array(transition.ChangedFields), transition.ChangedAt, string(transition.Outcome))
	if err != nil {
		return fmt.Errorf("save access policy transition: %w", translateError("access_policy_transition", transition.PolicyID.String(), err))
	}
	return nil
}

func validateAccessPolicyPreflight(input *store.AccessPolicyPreflight) error {
	if input == nil || input.ExpectedRevision < 1 || input.CheckedAt.IsZero() || input.Settings.Validate() != nil ||
		!validAccessDeploymentCapabilities(input.Capabilities) {
		return store.NewErrInvalidInput("access_policy", "preflight", nil)
	}
	return nil
}

func validateAccessPolicyReplacement(input *store.AccessPolicyReplacement, command *store.CommandIdempotency) error {
	if input == nil || !input.ActorID.IsValid() || !model.IsValidId(input.AuditEventID) || input.AuditAt <= 0 ||
		command == nil || command.UserID != input.ActorID || command.Operation != "access_policy.replace.v1" ||
		command.FingerprintVersion != 1 || command.OutcomeVersion != 1 || command.Retention <= 0 || command.Wait <= 0 {
		return store.NewErrInvalidInput("access_policy", "replacement", nil)
	}
	return validateAccessPolicyPreflight(&input.Preflight)
}

func accessPolicyBlockers(ctx context.Context, executor sqlxExecutor, current *model.AccessPolicy, settings model.AccessPolicySettings,
	capabilities store.AccessDeploymentCapabilities, at time.Time,
) ([]store.AccessPolicyBlocker, error) {
	blockers := make([]store.AccessPolicyBlocker, 0)
	for id, mode := range settings.ProviderAdmissions {
		capability, available := capabilities.Providers[id]
		if !available {
			blockers = append(blockers, store.AccessPolicyBlocker{Code: store.AccessPolicyBlockerProviderUnavailable, ProviderID: id})
			continue
		}
		if mode == model.ProviderAdmissionAutoProvision && !capability.AutoProvision {
			blockers = append(blockers, store.AccessPolicyBlocker{Code: store.AccessPolicyBlockerProviderAdmissionUnsupported, ProviderID: id})
		}
		if mode == model.ProviderAdmissionInvitationRequired && current.ProviderAdmissions[id] != model.ProviderAdmissionInvitationRequired && !capabilities.DurableMail {
			blockers = append(blockers, store.AccessPolicyBlocker{Code: store.AccessPolicyBlockerInvitationMailUnavailable, ProviderID: id})
		}
	}
	if settings.InvitationAdmissionEnabled && !current.InvitationAdmissionEnabled && !capabilities.DurableMail {
		blockers = append(blockers, store.AccessPolicyBlocker{Code: store.AccessPolicyBlockerInvitationMailUnavailable})
	}
	administratorHasPath, err := hasUsableSystemAdministratorAuthenticationPath(
		ctx, executor, settings, capabilities, at, systemAdministratorAuthenticationPathScope{},
	)
	if err != nil {
		return nil, err
	}
	if !administratorHasPath {
		blockers = append(blockers, store.AccessPolicyBlocker{Code: store.AccessPolicyBlockerLastAdministratorPath})
	}
	sort.Slice(blockers, func(i, j int) bool {
		if blockers[i].Code == blockers[j].Code {
			return blockers[i].ProviderID < blockers[j].ProviderID
		}
		return blockers[i].Code < blockers[j].Code
	})
	return blockers, nil
}

var _ store.AccessPolicyStore = (*SQLAccessPolicyStore)(nil)
