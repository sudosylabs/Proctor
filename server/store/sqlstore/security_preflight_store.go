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
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/sudosylabs/proctor/server/internal/canonicaljson"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

func validSecurityPreflightAccess(access store.SecurityPreflightAccess) bool {
	return access.SittingID.IsValid() && access.CandidateUserID.IsValid() && access.SessionID.IsValid() && access.DesktopRegistrationID.IsValid() && model.IsValidDPoPKeyThumbprint(access.DPoPKeyThumbprint) && access.DesktopBuild.Validate() == nil && access.DesktopBuild.NativeAgreement != nil && access.DesktopCompatibilityPolicyRevision > 0
}
func preflightConnectSelector(access store.SecurityPreflightAccess) *store.ExamAttemptConnect {
	return &store.ExamAttemptConnect{SittingID: access.SittingID, CandidateUserID: access.CandidateUserID, SessionID: access.SessionID, DesktopRegistrationID: access.DesktopRegistrationID, DPoPKeyThumbprint: access.DPoPKeyThumbprint, DesktopBuild: access.DesktopBuild, DesktopCompatibilityPolicyRevision: access.DesktopCompatibilityPolicyRevision, ConfigurationManifestFingerprint: access.DesktopBuild.AttemptConfigurationManifestFingerprint}
}
func preflightConflict(reason string) error {
	return store.NewErrConflict("security_preflight", reason, nil)
}
func (s *sqlExamAttemptStore) lockPreflightEligibility(ctx context.Context, tx *sqlxTxWrapper, access store.SecurityPreflightAccess) (examAttemptAdmissionGuard, error) {
	if err := requireDesktopCompatibilityPolicyRevision(ctx, tx, access.DesktopCompatibilityPolicyRevision); err != nil {
		return examAttemptAdmissionGuard{}, err
	}
	return s.lockExamAttemptEligibility(ctx, tx, preflightConnectSelector(access), false)
}

func (s *sqlExamAttemptStore) PrepareSecurityPreflight(ctx context.Context, input *store.SecurityPreflightPrepare, command *store.CommandIdempotency) (*store.SecurityPreflightPrepared, error) {
	if input == nil || command == nil || !validSecurityPreflightAccess(input.Access) || !model.IsValidAgreementID(input.PreflightID) || !model.IsValidId(input.AuditEventID) || input.AuditAt <= 0 || input.AttemptID != "" && !input.AttemptID.IsValid() {
		return nil, store.NewErrInvalidInput("security_preflight", "prepare", nil)
	}
	agreement := input.Access.DesktopBuild.NativeAgreement
	if input.NativeRegistryDigest != agreement.RegistryDigest() || input.SourceManifestDigest != agreement.SourceManifestDigest() || input.ConfigurationManifestFingerprint != input.Access.DesktopBuild.ConfigurationManifest.Fingerprint() {
		return nil, preflightConflict("configuration_unsupported")
	}
	result, err := runIdempotentMutation(ctx, s.SQLStore, "prepare security preflight", idempotentMutation[*store.SecurityPreflightPrepared]{command: command, auditEventID: input.AuditEventID,
		execute: func(ctx context.Context, tx *sqlxTxWrapper) (*store.SecurityPreflightPrepared, error) {
			return s.prepareSecurityPreflight(ctx, tx, input)
		},
		encode: func(value *store.SecurityPreflightPrepared) ([]byte, error) { return canonicalPreflightValue(value) },
		decode: func(version int, raw []byte) (*store.SecurityPreflightPrepared, error) {
			var value store.SecurityPreflightPrepared
			if version != 1 {
				return nil, preflightConflict("preflight_superseded")
			}
			if err := decodeCommandOutcome(raw, &value); err != nil {
				return nil, err
			}
			return &value, nil
		},
		completeReplay: func(ctx context.Context, tx *sqlxTxWrapper, value *store.SecurityPreflightPrepared, original string) error {
			guard, err := s.lockPreflightEligibility(ctx, tx, input.Access)
			if err != nil {
				return err
			}
			row, err := lockCurrentSecurityPreflight(ctx, tx, input.Access, value.Challenge.PreflightID)
			if err != nil {
				return err
			}
			if row.RevisionID != guard.RevisionID {
				return preflightConflict("policy_changed")
			}
			retention, err := getRetentionPolicy(ctx, tx, "FOR SHARE OF p")
			if err != nil {
				return err
			}
			// Refresh the separately revisioned notice without changing challenge or policy identity.
			value.BrowserActivityDisclosure, err = model.NewBrowserActivityDisclosure(retention, value.BrowserActivityDisclosure.MayCreateIntegrityEvidence)
			if err != nil {
				return err
			}
			value.ServerTime = row.DatabaseNow
			return completeSecurityPreflightAudit(ctx, tx, input.AuditEventID, input.AuditAt, value.Challenge.PreflightID, true)
		},
	})
	if err != nil {
		return nil, err
	}
	result.Value.Replayed = result.Replayed
	return result.Value, nil
}

func (s *sqlExamAttemptStore) prepareSecurityPreflight(ctx context.Context, tx *sqlxTxWrapper, input *store.SecurityPreflightPrepare) (*store.SecurityPreflightPrepared, error) {
	access := input.Access
	guard, err := s.lockPreflightEligibility(ctx, tx, access)
	if err != nil {
		return nil, err
	}
	now := guard.DatabaseNow.UTC().Truncate(time.Millisecond)
	// Reclaim a bounded page of expired pending slots. Skip rows in concurrent
	// admission/report transactions; immutable Connect provenance lives in its owner.
	if _, err := tx.Exec(ctx, `DELETE FROM exam_security_preflights WHERE (session_id,sitting_id) IN (SELECT session_id,sitting_id FROM exam_security_preflights WHERE expires_at<=? ORDER BY expires_at LIMIT 128 FOR UPDATE SKIP LOCKED)`, now); err != nil {
		return nil, err
	}
	var previous time.Time
	err = tx.Get(ctx, &previous, `SELECT issued_at FROM exam_security_preflights WHERE session_id=? AND sitting_id=? FOR UPDATE`, access.SessionID.String(), access.SittingID.String())
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("lock pending preflight: %w", err)
	}
	if err == nil && now.Sub(previous) < time.Second {
		return nil, preflightConflict("preflight_rate_limited")
	}
	var existing struct {
		ID            string `db:"id"`
		State         string `db:"state"`
		Configuration []byte `db:"attempt_configuration_canonical"`
		Digest        string `db:"attempt_configuration_digest"`
	}
	err = tx.Get(ctx, &existing, `SELECT id,state,attempt_configuration_canonical,attempt_configuration_digest FROM exam_attempts WHERE exam_sitting_id=? AND candidate_user_id=? FOR SHARE`, access.SittingID.String(), access.CandidateUserID.String())
	var frozen *model.AttemptConfiguration
	switch {
	case err == nil:
		if input.AttemptID.String() != existing.ID || existing.State != string(model.ExamAttemptReady) {
			return nil, preflightConflict("attempt_not_ready")
		}
		configuration, decodeErr := validateExistingAttemptConfiguration(existing.Configuration, existing.Digest, preflightConnectSelector(access))
		if decodeErr != nil {
			return nil, decodeErr
		}
		frozen = &configuration
	case errors.Is(err, sql.ErrNoRows):
		if input.AttemptID != "" {
			return nil, store.NewErrNotFound("exam_attempt", input.AttemptID.String())
		}
	default:
		return nil, fmt.Errorf("inspect preflight Attempt: %w", err)
	}
	examID, err := model.ParseExamID(guard.ExamID)
	if err != nil {
		return nil, err
	}
	revisionID, err := model.ParseExamRevisionID(guard.RevisionID)
	if err != nil {
		return nil, err
	}
	revision, err := getExamRevisionSnapshot(ctx, tx, examID, revisionID)
	if err != nil {
		return nil, err
	}
	selections, err := model.DecodeExamPolicySet(revision.Policy.Bytes)
	if err != nil {
		return nil, err
	}
	var institution string
	if err := tx.Get(ctx, &institution, `SELECT institution_id FROM academic_units WHERE id=?`, guard.AcademicUnitID); err != nil {
		return nil, err
	}
	institutionID, err := model.ParseInstitutionID(institution)
	if err != nil {
		return nil, err
	}
	scope := model.SecurityPolicyScope{Kind: "admission", AdmissionScopeID: input.PreflightID}
	if input.AttemptID.IsValid() {
		scope = model.SecurityPolicyScope{Kind: "attempt", AttemptID: input.AttemptID}
	}
	ordinal := int64(1)
	if input.AttemptID.IsValid() {
		if err := tx.Get(ctx, &ordinal, `SELECT COALESCE(MAX(generation),0)+1 FROM exam_attempt_participations WHERE exam_attempt_id=?`, input.AttemptID.String()); err != nil {
			return nil, err
		}
	}
	resolved, err := model.ResolveNativePolicy(model.NativePolicyResolution{PolicyID: model.NewId(), Revision: guard.RevisionID, Ordinal: ordinal, InstitutionID: institutionID, ExamRevisionID: revisionID, SittingID: access.SittingID, Scope: scope, IssuedAt: now, ActiveFrom: now, ActivationTime: now, Selections: selections, Build: access.DesktopBuild})
	if err != nil {
		return nil, err
	}
	retention, err := getRetentionPolicy(ctx, tx, "FOR SHARE OF p")
	if err != nil {
		return nil, err
	}
	disclosure, err := model.NewBrowserActivityDisclosure(retention, revision.BrowserPolicy.MayCreateIntegrityEvidence())
	if err != nil {
		return nil, err
	}
	prepared := &store.SecurityPreflightPrepared{ServerTime: now, BrowserActivityDisclosure: disclosure, Challenge: model.SecurityPreflightChallenge{PreflightID: input.PreflightID, Challenge: input.Challenge, IssuedAt: now, ExpiresAt: now.Add(model.SecurityPreflightLifetime)}, Resolved: resolved, FrozenAttemptConfiguration: frozen}
	if prepared.Challenge.Validate() != nil {
		return nil, store.NewErrInvalidInput("security_preflight", "challenge", nil)
	}
	document, err := canonicalPreflightValue(prepared)
	if err != nil {
		return nil, err
	}
	var attemptID any
	if input.AttemptID.IsValid() {
		attemptID = input.AttemptID.String()
	}
	_, err = tx.Exec(ctx, `INSERT INTO exam_security_preflights(session_id,sitting_id,candidate_user_id,preflight_id,registration_id,key_thumbprint,build_id,exam_revision_id,attempt_id,prepared_canonical,issued_at,expires_at)
 VALUES(?,?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(session_id,sitting_id) DO UPDATE SET
 candidate_user_id=EXCLUDED.candidate_user_id,preflight_id=EXCLUDED.preflight_id,registration_id=EXCLUDED.registration_id,key_thumbprint=EXCLUDED.key_thumbprint,build_id=EXCLUDED.build_id,exam_revision_id=EXCLUDED.exam_revision_id,attempt_id=EXCLUDED.attempt_id,prepared_canonical=EXCLUDED.prepared_canonical,issued_at=EXCLUDED.issued_at,expires_at=EXCLUDED.expires_at,report_canonical=NULL,result_canonical=NULL,reported_at=NULL,consumed_at=NULL`, access.SessionID.String(), access.SittingID.String(), access.CandidateUserID.String(), input.PreflightID, access.DesktopRegistrationID.String(), access.DPoPKeyThumbprint, access.DesktopBuild.DesktopBuildID, guard.RevisionID, attemptID, document, now, prepared.Challenge.ExpiresAt)
	if err != nil {
		return nil, fmt.Errorf("persist security preflight: %w", err)
	}
	if err := completeSecurityPreflightAudit(ctx, tx, input.AuditEventID, input.AuditAt, input.PreflightID, false); err != nil {
		return nil, err
	}
	return prepared, nil
}

type securityPreflightRow struct {
	PreflightID    string         `db:"preflight_id"`
	CandidateID    string         `db:"candidate_user_id"`
	RegistrationID string         `db:"registration_id"`
	KeyThumbprint  string         `db:"key_thumbprint"`
	BuildID        string         `db:"build_id"`
	RevisionID     string         `db:"exam_revision_id"`
	AttemptID      sql.NullString `db:"attempt_id"`
	Prepared       []byte         `db:"prepared_canonical"`
	Report         []byte         `db:"report_canonical"`
	Result         []byte         `db:"result_canonical"`
	ExpiresAt      time.Time      `db:"expires_at"`
	ReportedAt     sql.NullTime   `db:"reported_at"`
	ConsumedAt     sql.NullTime   `db:"consumed_at"`
	DatabaseNow    time.Time      `db:"database_now"`
}

func lockCurrentSecurityPreflight(ctx context.Context, tx *sqlxTxWrapper, access store.SecurityPreflightAccess, id string) (securityPreflightRow, error) {
	var row securityPreflightRow
	if err := tx.Get(ctx, &row, `SELECT preflight_id,candidate_user_id,registration_id,key_thumbprint,build_id,exam_revision_id,attempt_id,prepared_canonical,report_canonical,result_canonical,expires_at,reported_at,consumed_at FROM exam_security_preflights WHERE session_id=? AND sitting_id=? FOR UPDATE`, access.SessionID.String(), access.SittingID.String()); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return row, preflightConflict("preflight_superseded")
		}
		return row, err
	}
	if row.PreflightID != id {
		return row, preflightConflict("preflight_superseded")
	}
	if row.CandidateID != access.CandidateUserID.String() || row.RegistrationID != access.DesktopRegistrationID.String() || row.KeyThumbprint != access.DPoPKeyThumbprint || row.BuildID != access.DesktopBuild.DesktopBuildID {
		return row, preflightConflict("session_changed")
	}
	if err := tx.Get(ctx, &row.DatabaseNow, `SELECT statement_timestamp()`); err != nil {
		return row, err
	}
	row.DatabaseNow = row.DatabaseNow.UTC().Truncate(time.Millisecond)
	if !row.DatabaseNow.Before(row.ExpiresAt) {
		return row, preflightConflict("preflight_expired")
	}
	if row.ConsumedAt.Valid {
		return row, preflightConflict("preflight_consumed")
	}
	return row, nil
}

func (s *sqlExamAttemptStore) ReportSecurityPreflight(ctx context.Context, input *store.SecurityPreflightReport, command *store.CommandIdempotency) (*model.SecurityPreflightResult, error) {
	if input == nil || command == nil || !validSecurityPreflightAccess(input.Access) || !model.IsValidAgreementID(input.PreflightID) || !model.IsValidId(input.AuditEventID) || input.AuditAt <= 0 || input.Report.Validate() != nil {
		return nil, store.NewErrInvalidInput("security_preflight", "report", nil)
	}
	result, err := runIdempotentMutation(ctx, s.SQLStore, "report security preflight", idempotentMutation[*model.SecurityPreflightResult]{command: command, auditEventID: input.AuditEventID,
		execute: func(ctx context.Context, tx *sqlxTxWrapper) (*model.SecurityPreflightResult, error) {
			guard, err := s.lockPreflightEligibility(ctx, tx, input.Access)
			if err != nil {
				return nil, err
			}
			row, err := lockCurrentSecurityPreflight(ctx, tx, input.Access, input.PreflightID)
			if err != nil {
				return nil, err
			}
			if row.RevisionID != guard.RevisionID {
				return nil, preflightConflict("policy_changed")
			}
			var prepared store.SecurityPreflightPrepared
			if err := json.Unmarshal(row.Prepared, &prepared); err != nil {
				return nil, invalidPersistedState("security_preflight", "prepared", err)
			}
			result, err := model.EvaluateSecurityPreflight(prepared.Challenge, prepared.Resolved, input.Access.DesktopBuild.NativeAgreement, input.Report, row.DatabaseNow)
			if err != nil {
				return nil, store.NewErrInvalidInput("security_preflight", "report", nil).Wrap(err)
			}
			document, err := input.Report.Canonical()
			if err != nil {
				return nil, err
			}
			outcome, err := canonicalPreflightValue(result)
			if err != nil {
				return nil, err
			}
			if _, err := tx.Exec(ctx, `UPDATE exam_security_preflights SET report_canonical=?,result_canonical=?,reported_at=? WHERE session_id=? AND sitting_id=? AND preflight_id=?`, document, outcome, row.DatabaseNow, input.Access.SessionID.String(), input.Access.SittingID.String(), input.PreflightID); err != nil {
				return nil, fmt.Errorf("persist security preflight report: %w", err)
			}
			if err := completeSecurityPreflightAudit(ctx, tx, input.AuditEventID, input.AuditAt, input.PreflightID, false); err != nil {
				return nil, err
			}
			return &result, nil
		}, encode: func(value *model.SecurityPreflightResult) ([]byte, error) { return canonicalPreflightValue(value) },
		decode: func(version int, raw []byte) (*model.SecurityPreflightResult, error) {
			var value model.SecurityPreflightResult
			if version != 1 {
				return nil, preflightConflict("preflight_superseded")
			}
			if err := decodeCommandOutcome(raw, &value); err != nil {
				return nil, err
			}
			return &value, nil
		},
		completeReplay: func(ctx context.Context, tx *sqlxTxWrapper, value *model.SecurityPreflightResult, original string) error {
			guard, err := s.lockPreflightEligibility(ctx, tx, input.Access)
			if err != nil {
				return err
			}
			row, err := lockCurrentSecurityPreflight(ctx, tx, input.Access, value.PreflightID)
			if err != nil {
				return err
			}
			if row.RevisionID != guard.RevisionID {
				return preflightConflict("policy_changed")
			}
			return completeSecurityPreflightAudit(ctx, tx, input.AuditEventID, input.AuditAt, input.PreflightID, true)
		},
	})
	if err != nil {
		return nil, err
	}
	return result.Value, nil
}
func canonicalPreflightValue(value any) ([]byte, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	return canonicaljson.Canonicalize(raw, 256*1024)
}
func completeSecurityPreflightAudit(ctx context.Context, tx *sqlxTxWrapper, id string, at int64, preflight string, replayed bool) error {
	data, err := model.EncodeAuditData(map[string]any{"preflight_id": preflight, "idempotency_replayed": replayed})
	if err != nil {
		return err
	}
	_, err = completeAuditEvent(ctx, tx, id, model.AuditStatusSuccess, "", data, at)
	return err
}

// ResolveSecurityPreflightSitting reveals only a stable resource selector owned
// by this User and Session. Reporting rechecks current eligibility and fences.
func (s *sqlExamAttemptStore) ResolveSecurityPreflightSitting(ctx context.Context, id string, userID model.UserID, sessionID model.SessionID) (model.ExamSittingID, error) {
	if !model.IsValidAgreementID(id) || !userID.IsValid() || !sessionID.IsValid() {
		return "", store.NewErrInvalidInput("security_preflight", "selector", nil)
	}
	var sitting string
	if err := s.GetMaster().Get(ctx, &sitting, `SELECT sitting_id FROM exam_security_preflights WHERE preflight_id=? AND candidate_user_id=? AND session_id=?`, id, userID.String(), sessionID.String()); err != nil {
		return "", translateError("security_preflight", id, err)
	}
	return model.ParseExamSittingID(sitting)
}
