// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package sqlstore

import (
	"bytes"
	"context"
	"encoding/json"
	"time"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

// The binding and latest report occupy closed, separately reserved slots. New
// reset edges and detail receipts must obtain their own capacity before writing.
type admittedSecurityBinding struct {
	Security               model.AdmittedSecurity         `json:"security"`
	CatalogBindings        []model.SecurityCatalogBinding `json:"catalog_bindings"`
	CapabilityMatrixDigest string                         `json:"capability_matrix_digest"`
}
type connectSecurityAllocation struct {
	binding admittedSecurityBinding
	report  []byte
	now     time.Time
}

func (s *sqlExamAttemptStore) prepareConnectSecurity(ctx context.Context, tx *sqlxTxWrapper, input *store.ExamAttemptConnect, guard examAttemptAdmissionGuard, attemptID model.ExamAttemptID, generation int64, first bool) (connectSecurityAllocation, error) {
	var zero connectSecurityAllocation
	if input.Security.Kind != "preflight" {
		return zero, preflightConflict("preflight_required")
	}
	access := store.SecurityPreflightAccess{SittingID: input.SittingID, CandidateUserID: input.CandidateUserID, SessionID: input.SessionID, DesktopRegistrationID: input.DesktopRegistrationID, DPoPKeyThumbprint: input.DPoPKeyThumbprint, DesktopBuild: input.DesktopBuild, DesktopCompatibilityPolicyRevision: input.DesktopCompatibilityPolicyRevision}
	row, err := lockCurrentSecurityPreflight(ctx, tx, access, input.Security.PreflightID)
	if err != nil {
		return zero, err
	}
	if row.RevisionID != guard.RevisionID || first && row.AttemptID.Valid || !first && (!row.AttemptID.Valid || row.AttemptID.String != attemptID.String()) {
		return zero, preflightConflict("policy_changed")
	}
	if !row.ReportedAt.Valid || row.DatabaseNow.Before(row.ReportedAt.Time) || row.DatabaseNow.Sub(row.ReportedAt.Time) > model.SecurityPreflightAdmissionFreshness {
		return zero, preflightConflict("preflight_expired")
	}
	var prepared store.SecurityPreflightPrepared
	var report model.SecurityPreflightReport
	if err := json.Unmarshal(row.Prepared, &prepared); err != nil {
		return zero, invalidPersistedState("security_preflight", "prepared", err)
	}
	if err := json.Unmarshal(row.Report, &report); err != nil {
		return zero, preflightConflict("preflight_required")
	}
	if model.SHA256Fingerprint(row.Report) != input.Security.ReportDigest {
		return zero, preflightConflict("preflight_report_changed")
	}
	policy := prepared.Resolved.Policy
	examID, err := model.ParseExamID(guard.ExamID)
	if err != nil {
		return zero, err
	}
	revisionID, err := model.ParseExamRevisionID(guard.RevisionID)
	if err != nil {
		return zero, err
	}
	revision, err := getExamRevisionSnapshot(ctx, tx, examID, revisionID)
	if err != nil {
		return zero, err
	}
	selections, err := model.DecodeExamPolicySet(revision.Policy.Bytes)
	if err != nil {
		return zero, err
	}
	// Re-resolve the exact policy identity against current immutable catalogs and
	// the locked current Revision. A prior accepted report cannot authorize drift.
	current, err := model.ResolveNativePolicy(model.NativePolicyResolution{PolicyID: policy.PolicyID, Revision: policy.Revision, Ordinal: generation, InstitutionID: policy.InstitutionID, ExamRevisionID: revisionID, SittingID: input.SittingID, Scope: policy.Scope, IssuedAt: policy.IssuedAt, ActiveFrom: policy.ActiveFrom, ActivationTime: row.DatabaseNow, Selections: selections, Build: input.DesktopBuild})
	if err != nil {
		return zero, err
	}
	before, err := canonicalPreflightValue(prepared.Resolved)
	if err != nil {
		return zero, err
	}
	after, err := canonicalPreflightValue(current)
	if err != nil {
		return zero, err
	}
	if !bytes.Equal(before, after) {
		return zero, preflightConflict("policy_changed")
	}
	evaluated, err := model.EvaluateSecurityPreflight(prepared.Challenge, current, input.DesktopBuild.NativeAgreement, report, row.DatabaseNow)
	if err != nil {
		return zero, err
	}
	if evaluated.Admission != "eligible" {
		return zero, preflightConflict("posture_blocked")
	}
	if first {
		policy, err = policy.RebindAttempt(attemptID, row.DatabaseNow)
		if err != nil {
			return zero, err
		}
	} else if policy.Scope.Kind != "attempt" || policy.Scope.AttemptID != attemptID {
		return zero, preflightConflict("policy_changed")
	}
	content, err := policy.ContentDigest(current.CatalogBindings, current.CapabilityMatrixDigest)
	if err != nil || content != current.PolicyContentDigest {
		return zero, preflightConflict("policy_changed")
	}
	var used int64
	if !first {
		if err := tx.Get(ctx, &used, `SELECT control_metadata_bytes FROM exam_attempt_delivery_budgets WHERE exam_attempt_id=? FOR UPDATE`, attemptID.String()); err != nil {
			return zero, err
		}
	}
	if _, err := model.ReserveDeliveryMetadata(used, model.NativeOwnerReservationBytes); err != nil {
		return zero, err
	}
	binding := admittedSecurityBinding{Security: model.AdmittedSecurity{Policy: policy, PolicyContentDigest: content, PreflightID: row.PreflightID, PreflightReportDigest: evaluated.ReportDigest, PreflightPolicyDigest: current.Policy.Digest, ParticipationID: input.ParticipationID, Generation: generation, SecuritySessionID: report.SecuritySessionID, DeliveryStreamID: model.NewId(), RenewalIntervalSeconds: int64(model.AttemptParticipationRenewalInterval / time.Second)}, CatalogBindings: current.CatalogBindings, CapabilityMatrixDigest: current.CapabilityMatrixDigest}
	encoded, err := canonicalPreflightValue(binding)
	if err != nil || len(encoded) > model.SecurityPolicyResponseMaxBytes {
		return zero, store.NewErrInvalidInput("security", "binding_size", nil)
	}
	return connectSecurityAllocation{binding: binding, report: row.Report, now: row.DatabaseNow}, nil
}

func persistConnectSecurity(ctx context.Context, tx *sqlxTxWrapper, input *store.ExamAttemptConnect, allocation connectSecurityAllocation) error {
	security := allocation.binding.Security
	attemptID := security.Policy.Scope.AttemptID.String()
	// Recheck clock fences after all admission locks and Workspace creation.
	// A slow transaction cannot spend a report or issue a lease after its deadline.
	var timing struct {
		Now         time.Time `db:"now"`
		Expires     time.Time `db:"expires_at"`
		Reported    time.Time `db:"reported_at"`
		Lease       time.Time `db:"lease_expires_at"`
		End         time.Time `db:"scheduled_end_at"`
		SessionIdle time.Time `db:"idle_expires_at"`
		SessionEnd  time.Time `db:"session_expires_at"`
	}
	if err := tx.Get(ctx, &timing, `SELECT statement_timestamp() AS now,f.expires_at,f.reported_at,p.lease_expires_at,s.scheduled_end_at,se.idle_expires_at,se.expires_at AS session_expires_at FROM exam_security_preflights f JOIN exam_sittings s ON s.id=f.sitting_id JOIN sessions se ON se.id=f.session_id JOIN exam_attempt_participations p ON p.id=? WHERE f.preflight_id=? AND f.session_id=?`, security.ParticipationID.String(), input.Security.PreflightID, input.SessionID.String()); err != nil {
		return err
	}
	if !timing.Now.Before(timing.Expires) || timing.Now.Sub(timing.Reported) > model.SecurityPreflightAdmissionFreshness || !timing.Now.Before(timing.Lease) || !timing.Now.Before(timing.End) || !timing.Now.Before(timing.SessionIdle) || !timing.Now.Before(timing.SessionEnd) {
		return preflightConflict("preflight_expired")
	}
	allocation.now = timing.Now.UTC().Truncate(time.Millisecond)

	// The Attempt row is locked by Connect. Other delivery owners must acquire
	// the same budget row before reserving; no reservation is ever refunded.
	if _, err := tx.Exec(ctx, `INSERT INTO exam_attempt_delivery_budgets(exam_attempt_id,control_metadata_bytes) VALUES(?,0) ON CONFLICT DO NOTHING`, attemptID); err != nil {
		return err
	}
	var used int64
	if err := tx.Get(ctx, &used, `SELECT control_metadata_bytes FROM exam_attempt_delivery_budgets WHERE exam_attempt_id=? FOR UPDATE`, attemptID); err != nil {
		return err
	}
	next, err := model.ReserveDeliveryMetadata(used, model.NativeOwnerReservationBytes)
	if err != nil {
		return err
	}
	ledger, err := canonicalPreflightValue(model.NewNativeControlLedger())
	if err != nil {
		return err
	}
	binding, err := canonicalPreflightValue(allocation.binding)
	if err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO exam_attempt_security_owners(participation_id,exam_attempt_id,delivery_stream_id,security_session_id,session_id,registration_id,key_thumbprint,binding_canonical,latest_report_canonical,control_ledger_canonical,reserved_bytes,created_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`, security.ParticipationID.String(), attemptID, security.DeliveryStreamID, security.SecuritySessionID, input.SessionID.String(), input.DesktopRegistrationID.String(), input.DPoPKeyThumbprint, binding, allocation.report, ledger, model.NativeOwnerReservationBytes, allocation.now); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE exam_attempt_delivery_budgets SET control_metadata_bytes=? WHERE exam_attempt_id=?`, next, attemptID); err != nil {
		return err
	}
	result, err := tx.Exec(ctx, `UPDATE exam_security_preflights SET consumed_at=? WHERE session_id=? AND sitting_id=? AND preflight_id=? AND consumed_at IS NULL`, allocation.now, input.SessionID.String(), input.SittingID.String(), input.Security.PreflightID)
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count != 1 {
		return preflightConflict("preflight_consumed")
	}
	return nil
}

func loadAdmittedSecurity(ctx context.Context, executor sqlxExecutor, participationID string) (model.AdmittedSecurity, error) {
	var raw []byte
	if err := executor.Get(ctx, &raw, `SELECT binding_canonical FROM exam_attempt_security_owners WHERE participation_id=?`, participationID); err != nil {
		return model.AdmittedSecurity{}, translateError("security_owner", participationID, err)
	}
	var binding admittedSecurityBinding
	if err := json.Unmarshal(raw, &binding); err != nil {
		return model.AdmittedSecurity{}, invalidPersistedState("security_owner", "binding", err)
	}
	security := binding.Security
	content, err := security.Policy.ContentDigest(binding.CatalogBindings, binding.CapabilityMatrixDigest)
	if err != nil || security.Validate() != nil || content != security.PolicyContentDigest || security.ParticipationID.String() != participationID {
		return model.AdmittedSecurity{}, invalidPersistedState("security_owner", "binding", model.ErrSecurityPolicyInvalid)
	}
	return security, nil
}

func validateSecurityResume(ctx context.Context, tx *sqlxTxWrapper, input *store.ExamAttemptConnect, participationID string, generation int64) error {
	if input.Security.Kind != "resume" || input.Security.ParticipationID.String() != participationID || input.Security.Generation != generation {
		return preflightConflict("resume_required")
	}
	var owner struct {
		SessionID      string `db:"session_id"`
		RegistrationID string `db:"registration_id"`
		Key            string `db:"key_thumbprint"`
	}
	if err := tx.Get(ctx, &owner, `SELECT session_id,registration_id,key_thumbprint FROM exam_attempt_security_owners WHERE participation_id=? FOR SHARE`, participationID); err != nil {
		return err
	}
	if owner.SessionID != input.SessionID.String() || owner.RegistrationID != input.DesktopRegistrationID.String() || owner.Key != input.DPoPKeyThumbprint {
		return preflightConflict("session_changed")
	}
	security, err := loadAdmittedSecurity(ctx, tx, participationID)
	if err != nil {
		return err
	}
	if security.Policy.Digest != input.Security.PolicyDigest {
		return preflightConflict("policy_changed")
	}
	return nil
}

func (s *sqlExamAttemptStore) RecoverSecurityPolicy(ctx context.Context, access store.SecurityPreflightAccess, attemptID model.ExamAttemptID) (*store.SecurityPolicyRecovery, error) {
	if !attemptID.IsValid() || !access.CandidateUserID.IsValid() || !access.SessionID.IsValid() || !access.DesktopRegistrationID.IsValid() || !model.IsValidDPoPKeyThumbprint(access.DPoPKeyThumbprint) || access.DesktopBuild.Validate() != nil || access.DesktopBuild.NativeAgreement == nil || access.DesktopCompatibilityPolicyRevision < 1 {
		return nil, store.NewErrInvalidInput("security_owner", "recovery", nil)
	}
	return runSQLTransaction(ctx, s.GetMaster().Begin, "recover security policy", func(ctx context.Context, tx *sqlxTxWrapper) (*store.SecurityPolicyRecovery, error) {
		if err := requireDesktopCompatibilityPolicyRevision(ctx, tx, access.DesktopCompatibilityPolicyRevision); err != nil {
			return nil, err
		}
		var row struct {
			SittingID           string    `db:"exam_sitting_id"`
			ParticipationID     string    `db:"participation_id"`
			Configuration       []byte    `db:"attempt_configuration_canonical"`
			ConfigurationDigest string    `db:"attempt_configuration_digest"`
			LeaseExpiresAt      time.Time `db:"lease_expires_at"`
		}
		// Recovery works after a lost Connect response even if the transport has
		// closed. It does not require an open Connection or expose ended generations.
		readOwner := func() error {
			return tx.Get(ctx, &row, `SELECT a.exam_sitting_id,p.id AS participation_id,a.attempt_configuration_canonical,a.attempt_configuration_digest,p.lease_expires_at FROM exam_attempts a JOIN exam_attempt_participations p ON p.exam_attempt_id=a.id AND p.state='active' JOIN exam_attempt_security_owners o ON o.participation_id=p.id WHERE a.id=? AND a.candidate_user_id=? AND a.state='active' AND p.session_id=? AND o.session_id=p.session_id AND o.registration_id=? AND o.key_thumbprint=? FOR SHARE OF a,p,o`, attemptID.String(), access.CandidateUserID.String(), access.SessionID.String(), access.DesktopRegistrationID.String(), access.DPoPKeyThumbprint)
		}
		var sittingSelector string
		if err := tx.Get(ctx, &sittingSelector, `SELECT exam_sitting_id FROM exam_attempts WHERE id=? AND candidate_user_id=?`, attemptID.String(), access.CandidateUserID.String()); err != nil {
			return nil, translateError("security_owner", attemptID.String(), err)
		}
		sittingID, err := model.ParseExamSittingID(sittingSelector)
		if err != nil {
			return nil, err
		}
		access.SittingID = sittingID
		guard, err := s.lockExamAttemptEligibility(ctx, tx, preflightConnectSelector(access), true)
		if err != nil {
			return nil, err
		}
		if err := readOwner(); err != nil {
			return nil, translateError("security_owner", attemptID.String(), err)
		}
		if !guard.DatabaseNow.Before(row.LeaseExpiresAt) {
			return nil, store.NewErrConflict("attempt_participation", "attempt_participation_expired", nil)
		}
		configuration, err := validateExistingAttemptConfiguration(row.Configuration, row.ConfigurationDigest, preflightConnectSelector(access))
		if err != nil {
			return nil, err
		}
		security, err := loadAdmittedSecurity(ctx, tx, row.ParticipationID)
		if err != nil {
			return nil, err
		}
		participationID, err := model.ParseAttemptParticipationID(row.ParticipationID)
		if err != nil {
			return nil, err
		}
		recovery, err := recoverNativeControl(ctx, tx, attemptID, participationID, security.DeliveryStreamID, guard.State == string(model.ExamSittingOpen))
		if err != nil {
			return nil, err
		}
		recovery.ServerTime = guard.DatabaseNow.UTC().Truncate(time.Millisecond)
		recovery.Security = security
		recovery.FrozenAttemptConfiguration = configuration
		return recovery, nil
	})
}
