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
	"time"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

type browserSourceRefusalRecord struct {
	SourceSessionID model.BrowserSourceSessionID    `json:"source_session_id"`
	RequestDigest   string                          `json:"request_digest"`
	Code            string                          `json:"code"`
	Capacity        *model.DeliveryMetadataCapacity `json:"capacity"`
}

func (r browserSourceRefusalRecord) Validate() error {
	if !r.SourceSessionID.IsValid() || !model.IsValidSHA256Fingerprint(r.RequestDigest) {
		return model.ErrDeliveryInvalid
	}
	switch r.Code {
	case "exam.browser.source_budget_exhausted":
		if r.Capacity != nil {
			return model.ErrDeliveryInvalid
		}
	case "exam.delivery.metadata_capacity":
		if r.Capacity == nil || r.Capacity.Validate() != nil {
			return model.ErrDeliveryInvalid
		}
	default:
		return model.ErrDeliveryInvalid
	}
	return nil
}

func (s *sqlExamAttemptStore) StartBrowserActivity(ctx context.Context, input *store.BrowserActivitySourceStart) (*model.BrowserSourceStatus, error) {
	if input == nil || input.Declaration().Validate() != nil || !model.IsValidId(input.AuditEventID) || input.AuditAt <= 0 {
		return nil, store.NewErrInvalidInput("browser_activity", "source_start", nil)
	}
	canonical, _ := input.Declaration().Canonical()
	digest := model.SHA256Fingerprint(canonical)
	type outcome struct {
		Status  *model.BrowserSourceStatus
		Refusal *store.BrowserSourceRefusal
	}
	result, err := runSQLTransaction(ctx, s.GetMaster().Begin, "start Browser Activity source", func(ctx context.Context, tx *sqlxTxWrapper) (outcome, error) {
		// Serialize browser owner changes before acquiring the shared live guard.
		var locked string
		if err := tx.Get(ctx, &locked, `SELECT a.id FROM exam_attempts a JOIN exam_sittings sit ON sit.id=a.exam_sitting_id WHERE a.id=? AND a.candidate_user_id=? FOR SHARE OF sit`, input.Access.AttemptID.String(), input.Access.CandidateUserID.String()); err != nil {
			return outcome{}, translateError("browser_activity_source", string(input.SourceSessionID), err)
		}
		if err := tx.Get(ctx, &locked, `SELECT id FROM exam_attempts WHERE id=? FOR UPDATE`, input.Access.AttemptID.String()); err != nil {
			return outcome{}, err
		}
		guard, err := s.lockCandidateGuard(ctx, tx, input.Access)
		if err != nil {
			return outcome{}, err
		}
		var part struct {
			Generation  int64 `db:"generation"`
			Initial     bool  `db:"browser_initial_started"`
			Corrections int64 `db:"browser_correction_starts"`
			Resets      int64 `db:"browser_runtime_reset_starts"`
		}
		if err := tx.Get(ctx, &part, `SELECT p.generation,o.browser_initial_started,o.browser_correction_starts,o.browser_runtime_reset_starts FROM exam_attempt_participations p JOIN exam_attempt_security_owners o ON o.participation_id=p.id JOIN exam_attempt_connections c ON c.participation_id=p.id WHERE p.id=? AND p.exam_attempt_id=? AND p.state='active' AND c.id=? AND c.state='open' AND p.session_id=? AND c.session_id=? FOR UPDATE OF p,o`, input.ParticipationID.String(), guard.AttemptID, input.Access.ConnectionID.String(), input.Access.SessionID.String(), input.Access.SessionID.String()); err != nil {
			return outcome{}, translateError("browser_activity_source", string(input.SourceSessionID), err)
		}
		if part.Generation != input.Generation {
			return outcome{}, store.NewErrConflict("browser_activity", "attempt_participation_generation", nil)
		}
		// Retry identity is checked before mutable policy/gate state. It can return
		// the original now-closed source but never revive navigation authority.
		var existing struct {
			ID     string `db:"id"`
			Digest string `db:"start_digest"`
		}
		err = tx.Get(ctx, &existing, `SELECT id::text,start_digest FROM browser_activity_sources WHERE id=?::uuid AND participation_id=? AND candidate_user_id=? AND registration_id=? AND key_thumbprint=?`, string(input.SourceSessionID), input.ParticipationID.String(), input.Access.CandidateUserID.String(), input.Access.DesktopRegistrationID.String(), input.Access.DPoPKeyThumbprint)
		if err == nil {
			if existing.Digest != digest {
				return outcome{}, store.NewErrConflict("browser_activity", "browser_source_conflict", nil)
			}
			status, err := browserSourceStatus(ctx, tx, input.SourceSessionID)
			if err != nil {
				return outcome{}, err
			}
			if err := completeBrowserSourceAudit(ctx, tx, input, true, "started"); err != nil {
				return outcome{}, err
			}
			return outcome{Status: status}, nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return outcome{}, err
		}
		// A later policy correction can create a successor after a refusal.
		// Retry identity belongs to the retained predecessor slot, not whichever
		// source is newest today. Current owner/generation were reauthorized above.
		var refused []struct {
			ID   string `db:"id"`
			Body []byte `db:"refusal_canonical"`
		}
		if err := tx.Select(ctx, &refused, `SELECT id::text,refusal_canonical FROM browser_activity_sources WHERE participation_id=? AND candidate_user_id=? AND registration_id=? AND key_thumbprint=? AND refusal_canonical IS NOT NULL ORDER BY start_ordinal LIMIT ? FOR UPDATE`, input.ParticipationID.String(), input.Access.CandidateUserID.String(), input.Access.DesktopRegistrationID.String(), input.Access.DPoPKeyThumbprint, model.BrowserSourceMaximumPerParticipation); err != nil {
			return outcome{}, err
		}
		for _, saved := range refused {
			var refusal browserSourceRefusalRecord
			if json.Unmarshal(saved.Body, &refusal) != nil || refusal.Validate() != nil {
				return outcome{}, invalidPersistedState("browser_activity_source", "refusal", nil)
			}
			if refusal.SourceSessionID != input.SourceSessionID {
				continue
			}
			if refusal.RequestDigest != digest {
				return outcome{}, store.NewErrConflict("browser_activity", "browser_source_conflict", nil)
			}
			status, err := browserSourceStatus(ctx, tx, model.BrowserSourceSessionID(saved.ID))
			if err != nil {
				return outcome{}, err
			}
			if err := completeBrowserSourceAudit(ctx, tx, input, true, refusal.Code); err != nil {
				return outcome{}, err
			}
			return outcome{Refusal: &store.BrowserSourceRefusal{Code: refusal.Code, Status: *status, Capacity: refusal.Capacity}}, nil
		}
		var prior struct {
			ID       string `db:"id"`
			Revision string `db:"policy_revision_id"`
			Digest   string `db:"policy_digest"`
			State    string `db:"state"`
			Closure  []byte `db:"closure_canonical"`
		}
		err = tx.Get(ctx, &prior, `SELECT id::text,policy_revision_id,policy_digest,state,closure_canonical FROM browser_activity_sources WHERE participation_id=? ORDER BY start_ordinal DESC LIMIT 1 FOR UPDATE`, input.ParticipationID.String())
		hasPrior := err == nil
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return outcome{}, err
		}
		if guard.SittingState != string(model.ExamSittingOpen) {
			return outcome{}, store.NewErrConflict("exam_sitting", "exam_sitting_state", nil)
		}
		if err := lockSecurityInteraction(ctx, tx, input.Access.AttemptID); err != nil {
			return outcome{}, err
		}
		pending, err := hasPendingCandidateCorrectionCapability(ctx, tx, guard.AttemptID, guard.SittingID, guard.AdmissionRevisionID, guard.RevisionID, model.CandidateCapabilityBrowser)
		if err != nil {
			return outcome{}, err
		}
		if pending {
			return outcome{}, store.NewErrConflict("browser_activity", "exam_correction_acknowledgement_required", nil)
		}
		var policy struct {
			Raw    []byte `db:"browser_policy_canonical"`
			Digest string `db:"browser_policy_digest"`
		}
		if err := tx.Get(ctx, &policy, `SELECT browser_policy_canonical,browser_policy_digest FROM exam_revisions WHERE id=? AND sealed=true`, guard.RevisionID); err != nil {
			return outcome{}, err
		}
		parsed, err := model.DecodeBrowserPolicy(policy.Raw)
		if err != nil {
			return outcome{}, err
		}
		if !parsed.Enabled {
			return outcome{}, store.NewErrConflict("browser_activity", "browser_policy_disabled", nil)
		}
		if input.PolicyRevisionID.String() != guard.RevisionID || input.PolicyDigest != policy.Digest {
			return outcome{}, store.NewErrConflict("browser_activity", "browser_source_policy", nil)
		}
		switch input.Transition.Kind {
		case "initial":
			if hasPrior || part.Initial {
				return outcome{}, store.NewErrConflict("browser_activity", "browser_source_current", nil)
			}
		case "runtime_reset":
			if !hasPrior || prior.ID != string(input.Transition.PredecessorSourceSessionID) || prior.State != "current" || prior.Revision != input.PolicyRevisionID.String() || prior.Digest != input.PolicyDigest {
				return outcome{}, store.NewErrConflict("browser_activity", "browser_source_predecessor", nil)
			}
		case "policy_correction":
			var closure model.DeliveryClosure
			if hasPrior && (json.Unmarshal(prior.Closure, &closure) != nil || closure.Validate(false) != nil) {
				return outcome{}, invalidPersistedState("browser_source", "predecessor_closure", model.ErrDeliveryInvalid)
			}
			if !hasPrior || prior.ID != string(input.Transition.PredecessorSourceSessionID) || prior.Revision == input.PolicyRevisionID.String() || closure.CloseReason == nil || (*closure.CloseReason != model.DeliveryClosedPolicyCorrection && *closure.CloseReason != model.DeliveryClosedBrowserDisabled && *closure.CloseReason != model.DeliveryClosedRuntimeReset) {
				return outcome{}, store.NewErrConflict("browser_activity", "browser_source_predecessor", nil)
			}
		}
		var now time.Time
		if err := tx.Get(ctx, &now, `SELECT clock_timestamp()`); err != nil {
			return outcome{}, err
		}
		now = now.UTC().Truncate(time.Millisecond)
		var used int64
		if err := tx.Get(ctx, &used, `SELECT control_metadata_bytes FROM exam_attempt_delivery_budgets WHERE exam_attempt_id=? FOR UPDATE`, guard.AttemptID); err != nil {
			return outcome{}, err
		}
		reserved, capacityErr := model.ReserveDeliveryMetadata(used, model.BrowserOwnerReservationBytes)
		exhausted := input.Transition.Kind == "runtime_reset" && part.Resets >= model.BrowserRuntimeResetStartLimit || input.Transition.Kind == "policy_correction" && part.Corrections >= model.BrowserCorrectionStartLimit
		if exhausted || capacityErr != nil {
			code := "exam.browser.source_budget_exhausted"
			var capacity *model.DeliveryMetadataCapacity
			if capacityErr != nil {
				if !errors.As(capacityErr, &capacity) {
					return outcome{}, capacityErr
				}
				code = "exam.delivery.metadata_capacity"
			}
			if input.Transition.Kind != "runtime_reset" {
				if capacity != nil {
					return outcome{}, capacity
				}
				return outcome{}, store.NewErrConflict("browser_activity", "browser_source_limit", nil)
			}
			if err := closeBrowserSource(ctx, tx, model.BrowserSourceSessionID(prior.ID), model.DeliveryClosedRuntimeReset, now); err != nil {
				return outcome{}, err
			}
			refusal := browserSourceRefusalRecord{SourceSessionID: input.SourceSessionID, RequestDigest: digest, Code: code, Capacity: capacity}
			raw, err := canonicalPreflightValue(refusal)
			if err != nil || len(raw) > 2048 {
				return outcome{}, model.ErrDeliveryInvalid
			}
			if _, err := tx.Exec(ctx, `UPDATE browser_activity_sources SET refusal_canonical=? WHERE id=?::uuid`, raw, prior.ID); err != nil {
				return outcome{}, err
			}
			if _, err := tx.Exec(ctx, `UPDATE exam_attempt_security_owners SET browser_source_unavailable=true WHERE participation_id=?`, input.ParticipationID.String()); err != nil {
				return outcome{}, err
			}
			if capacity != nil {
				if err := latchDeliveryMetadata(ctx, tx, input.Access.AttemptID); err != nil {
					return outcome{}, err
				}
			}
			status, err := browserSourceStatus(ctx, tx, model.BrowserSourceSessionID(prior.ID))
			if err != nil {
				return outcome{}, err
			}
			if err := completeBrowserSourceAudit(ctx, tx, input, false, code); err != nil {
				return outcome{}, err
			}
			return outcome{Refusal: &store.BrowserSourceRefusal{Code: code, Status: *status, Capacity: capacity}}, nil
		}
		if input.Transition.Kind == "runtime_reset" {
			if err := closeBrowserSource(ctx, tx, model.BrowserSourceSessionID(prior.ID), model.DeliveryClosedRuntimeReset, now); err != nil {
				return outcome{}, err
			}
		}
		if _, err := tx.Exec(ctx, `INSERT INTO browser_activity_sources(id,exam_id,exam_sitting_id,exam_attempt_id,participation_id,generation,session_id,connection_id,candidate_user_id,registration_id,key_thumbprint,policy_revision_id,policy_digest,start_transition,start_digest,start_canonical,start_ordinal,predecessor_id,reset_reason,state,started_at,reserved_bytes) VALUES(?::uuid,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?::uuid,?,'current',?,?)`, string(input.SourceSessionID), guard.ExamID, guard.SittingID, guard.AttemptID, input.ParticipationID.String(), input.Generation, input.Access.SessionID.String(), input.Access.ConnectionID.String(), input.Access.CandidateUserID.String(), input.Access.DesktopRegistrationID.String(), input.Access.DPoPKeyThumbprint, input.PolicyRevisionID.String(), input.PolicyDigest, input.Transition.Kind, digest, canonical, 1+part.Corrections+part.Resets+browserInitialStartCount(part.Initial), nullableString(string(input.Transition.PredecessorSourceSessionID)), nullableString(string(input.Transition.Reason)), now, model.BrowserOwnerReservationBytes); err != nil {
			return outcome{}, translateError("browser_activity_source", string(input.SourceSessionID), err)
		}
		if _, err := tx.Exec(ctx, `UPDATE exam_attempt_delivery_budgets SET control_metadata_bytes=? WHERE exam_attempt_id=?`, reserved, guard.AttemptID); err != nil {
			return outcome{}, err
		}
		corrections, resets := part.Corrections, part.Resets
		if input.Transition.Kind == "policy_correction" {
			corrections++
		}
		if input.Transition.Kind == "runtime_reset" {
			resets++
		}
		if _, err := tx.Exec(ctx, `UPDATE exam_attempt_security_owners SET browser_initial_started=true,browser_correction_starts=?,browser_runtime_reset_starts=?,browser_source_unavailable=false WHERE participation_id=?`, corrections, resets, input.ParticipationID.String()); err != nil {
			return outcome{}, err
		}
		status, err := settleBrowserSource(ctx, tx, input.SourceSessionID)
		if err != nil {
			return outcome{}, err
		}
		if err := completeBrowserSourceAudit(ctx, tx, input, false, "started"); err != nil {
			return outcome{}, err
		}
		return outcome{Status: status}, nil
	})
	if err != nil {
		return nil, err
	}
	if result.Refusal != nil {
		return nil, result.Refusal
	}
	return result.Status, nil
}

func closeBrowserSource(ctx context.Context, tx *sqlxTxWrapper, id model.BrowserSourceSessionID, reason model.DeliveryCloseReason, at time.Time) error {
	var row struct {
		Raw       []byte    `db:"closure_canonical"`
		Allocated int64     `db:"allocated_through_sequence"`
		Seen      int64     `db:"highest_seen"`
		Lease     time.Time `db:"lease_expires_at"`
	}
	if err := tx.Get(ctx, &row, `SELECT s.closure_canonical,s.allocated_through_sequence,s.highest_seen,p.lease_expires_at FROM browser_activity_sources s JOIN exam_attempt_participations p ON p.id=s.participation_id WHERE s.id=?::uuid FOR UPDATE OF s`, string(id)); err != nil {
		return err
	}
	var closure model.DeliveryClosure
	if len(row.Raw) > 0 && (json.Unmarshal(row.Raw, &closure) != nil || closure.Validate(false) != nil) {
		return invalidPersistedState("browser_source", "closure", model.ErrDeliveryInvalid)
	}
	if row.Lease.Before(at) {
		at = row.Lease
		reason = model.DeliveryClosedParticipation
	}
	closure, err := closure.Close(false, reason, max(row.Allocated, row.Seen), at.UTC().Truncate(time.Millisecond), nil)
	if err != nil {
		return err
	}
	raw, err := canonicalPreflightValue(closure)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `UPDATE browser_activity_sources SET state='closed',ended_at=?,closure_canonical=?,upload_expires_at=?,allocated_through_sequence=GREATEST(allocated_through_sequence,highest_seen) WHERE id=?::uuid`, *closure.ClosedAt, raw, closure.UploadExpiresAt, string(id))
	if err != nil {
		return err
	}
	_, err = settleBrowserSource(ctx, tx, id)
	return err
}

func browserSourceStatus(ctx context.Context, tx *sqlxTxWrapper, id model.BrowserSourceSessionID) (*model.BrowserSourceStatus, error) {
	return browserSourceState(ctx, tx, id, false)
}

// settleBrowserSource belongs to an audited mutation, never a status read.
func settleBrowserSource(ctx context.Context, tx *sqlxTxWrapper, id model.BrowserSourceSessionID) (*model.BrowserSourceStatus, error) {
	return browserSourceState(ctx, tx, id, true)
}
func browserSourceState(ctx context.Context, tx *sqlxTxWrapper, id model.BrowserSourceSessionID, persist bool) (*model.BrowserSourceStatus, error) {
	var row struct {
		Attempt             string         `db:"exam_attempt_id"`
		Part                string         `db:"participation_id"`
		Generation          int64          `db:"generation"`
		Revision            string         `db:"policy_revision_id"`
		Digest              string         `db:"policy_digest"`
		Predecessor         sql.NullString `db:"predecessor_id"`
		Transition          string         `db:"start_transition"`
		Reason              sql.NullString `db:"reset_reason"`
		Started             time.Time      `db:"started_at"`
		Closure             []byte         `db:"closure_canonical"`
		Allocated           int64          `db:"allocated_through_sequence"`
		Seen                int64          `db:"highest_seen"`
		Contiguous          int64          `db:"highest_contiguous"`
		Terminal            int64          `db:"terminal_missing_through_sequence"`
		DeclarationRevision int64          `db:"declaration_revision"`
		Summary             []byte         `db:"summary_canonical"`
		PartSummary         bool           `db:"part_summary"`
		PartReason          sql.NullString `db:"part_reason"`
		AttemptSummary      bool           `db:"attempt_summary"`
		AttemptReason       sql.NullString `db:"attempt_reason"`
		Corrections         int64          `db:"browser_correction_starts"`
		Resets              int64          `db:"browser_runtime_reset_starts"`
		Now                 time.Time      `db:"server_time"`
	}
	if err := tx.Get(ctx, &row, `SELECT s.exam_attempt_id,s.participation_id,s.generation,s.policy_revision_id,s.policy_digest,s.predecessor_id::text,s.start_transition,s.reset_reason,s.started_at,s.closure_canonical,s.allocated_through_sequence,s.highest_seen,s.highest_contiguous,s.terminal_missing_through_sequence,s.declaration_revision,s.summary_canonical,o.browser_summary_only AS part_summary,o.browser_stop_reason AS part_reason,b.browser_summary_only AS attempt_summary,b.browser_stop_reason AS attempt_reason,o.browser_correction_starts,o.browser_runtime_reset_starts,clock_timestamp() AS server_time FROM browser_activity_sources s JOIN exam_attempt_security_owners o ON o.participation_id=s.participation_id JOIN exam_attempt_delivery_budgets b ON b.exam_attempt_id=s.exam_attempt_id WHERE s.id=?::uuid`, string(id)); err != nil {
		return nil, err
	}
	value := &model.BrowserSourceStatus{SourceSessionID: id, AttemptID: model.ExamAttemptID(row.Attempt), ParticipationID: model.AttemptParticipationID(row.Part), Generation: row.Generation, PolicyRevisionID: model.ExamRevisionID(row.Revision), PolicyDigest: row.Digest, StartTransition: row.Transition, StartedAt: row.Started.UTC().Truncate(time.Millisecond), DeclarationRevision: row.DeclarationRevision, DetailMode: "collecting", RemainingCorrectionStarts: model.BrowserCorrectionStartLimit - row.Corrections, RemainingRuntimeResetStarts: model.BrowserRuntimeResetStartLimit - row.Resets, ServerTime: row.Now.UTC().Truncate(time.Millisecond)}
	if row.Predecessor.Valid {
		id := model.BrowserSourceSessionID(row.Predecessor.String)
		value.PredecessorSourceSessionID = &id
	}
	if row.Reason.Valid {
		reason := model.BrowserSourceResetReason(row.Reason.String)
		value.RuntimeResetReason = &reason
	}
	if len(row.Closure) > 0 && (json.Unmarshal(row.Closure, &value.Closure) != nil || value.Closure.Validate(false) != nil) {
		return nil, invalidPersistedState("browser_source", "value", model.ErrDeliveryInvalid)
	}
	if len(row.Summary) > 0 {
		var summary model.UnretainedDeliverySummary
		if json.Unmarshal(row.Summary, &summary) != nil {
			return nil, invalidPersistedState("browser_source", "value", model.ErrDeliveryInvalid)
		}
		value.Summary = &summary
	}
	if row.PartSummary || row.AttemptSummary {
		scope := "participation"
		reason := model.DeliveryStopReason(row.PartReason.String)
		if row.AttemptSummary {
			scope = "attempt"
			reason = model.DeliveryStopReason(row.AttemptReason.String)
		}
		value.DetailMode = "summary_only"
		value.BudgetScope = &scope
		value.SummaryOnlyReason = &reason
	}
	allocated := max(row.Allocated, row.Seen)
	if value.Closure.ClosedAt != nil && !value.ServerTime.Before(*value.Closure.UploadExpiresAt) {
		var err error
		value.Closure, err = value.Closure.Expire(false, value.ServerTime)
		if err != nil {
			return nil, invalidPersistedState("browser_source", "closure", err)
		}
		row.Terminal = allocated
		raw, err := canonicalPreflightValue(value.Closure)
		if err != nil {
			return nil, err
		}
		if persist {
			if _, err := tx.Exec(ctx, `UPDATE browser_activity_sources SET closure_canonical=?,terminal_missing_through_sequence=?,allocated_through_sequence=? WHERE id=?::uuid`, raw, row.Terminal, allocated, string(id)); err != nil {
				return nil, err
			}
		}
	}
	received, gaps, err := browserDeliveryRanges(ctx, tx, id)
	if err != nil {
		return nil, err
	}
	progress, err := model.ResolveDeliveryProgress(allocated, received, gaps, row.Terminal)
	if err != nil {
		return nil, invalidPersistedState("delivery", "progress", err)
	}
	if persist {
		var policyRaw []byte
		if err := tx.Get(ctx, &policyRaw, `SELECT browser_policy_canonical FROM exam_revisions WHERE id=? AND browser_policy_digest=?`, row.Revision, row.Digest); err != nil {
			return nil, err
		}
		policy, err := model.DecodeBrowserPolicy(policyRaw)
		if err != nil {
			return nil, err
		}
		if err := advanceBrowserInterpretation(ctx, tx, id, value.AttemptID, policy, progress.SettledThrough); err != nil {
			return nil, err
		}
		if _, err := tx.Exec(ctx, `UPDATE browser_activity_sources SET highest_contiguous=?,settled_through_sequence=? WHERE id=?::uuid`, progress.HighestContiguous, progress.SettledThrough, string(id)); err != nil {
			return nil, err
		}
	}
	value.BrowserDeliveryProgress = model.BrowserDeliveryProgress{HighestContiguous: progress.HighestContiguous, SettledThrough: progress.SettledThrough, HighestSeen: progress.HighestSeen, AllocatedThrough: allocated, TerminalMissingThrough: row.Terminal, MissingRanges: progress.Missing, MissingRangesTruncated: progress.MissingTruncated}
	if value.Validate() != nil {
		return nil, invalidPersistedState("browser_source", "value", model.ErrDeliveryInvalid)
	}
	if persist {
		if err := updateBrowserSourceSettlement(ctx, tx, value); err != nil {
			return nil, err
		}
	}
	return value, nil
}

func browserInitialStartCount(value bool) int64 {
	if value {
		return 1
	}
	return 0
}

// The owning lifecycle transaction has already fenced the Participation. Every
// source has a reserved closure slot, including sources closed by old delivery
// finalization paths earlier in that same transaction.
func closeParticipationBrowserSources(ctx context.Context, tx *sqlxTxWrapper, part model.AttemptParticipationID, reason model.DeliveryCloseReason, at time.Time) error {
	var ids []string
	if err := tx.Select(ctx, &ids, `SELECT id::text FROM browser_activity_sources WHERE participation_id=? ORDER BY start_ordinal LIMIT 49 FOR UPDATE`, part.String()); err != nil {
		return err
	}
	for _, id := range ids {
		if err := closeBrowserSource(ctx, tx, model.BrowserSourceSessionID(id), reason, at); err != nil {
			return err
		}
	}
	return nil
}

func (s *sqlExamAttemptStore) BrowserSourceStatus(ctx context.Context, access store.BrowserDeliveryAccess) (*model.BrowserSourceStatus, error) {
	return runSQLTransaction(ctx, s.GetMaster().Begin, "get Browser source status", func(ctx context.Context, tx *sqlxTxWrapper) (*model.BrowserSourceStatus, error) {
		if err := s.lockBrowserDelivery(ctx, tx, access); err != nil {
			return nil, err
		}
		return browserSourceStatus(ctx, tx, access.SourceSessionID)
	})
}
func (s *sqlExamAttemptStore) BrowserSourceList(ctx context.Context, access store.BrowserDeliveryAccess) ([]model.BrowserSourceStatus, error) {
	if !access.ParticipationID.IsValid() {
		return nil, store.NewErrInvalidInput("browser_activity", "participation_id", nil)
	}
	return runSQLTransaction(ctx, s.GetMaster().Begin, "list Browser source status", func(ctx context.Context, tx *sqlxTxWrapper) ([]model.BrowserSourceStatus, error) {
		if err := lockBrowserParticipationOwner(ctx, tx, access); err != nil {
			return nil, err
		}
		var ids []string
		if err := tx.Select(ctx, &ids, `SELECT id::text FROM browser_activity_sources WHERE participation_id=? ORDER BY start_ordinal LIMIT 49`, access.ParticipationID.String()); err != nil {
			return nil, err
		}
		statuses := make([]model.BrowserSourceStatus, 0, len(ids))
		for _, id := range ids {
			query := access
			query.SourceSessionID = model.BrowserSourceSessionID(id)
			if err := s.lockBrowserDelivery(ctx, tx, query); err != nil {
				return nil, err
			}
			status, err := browserSourceStatus(ctx, tx, query.SourceSessionID)
			if err != nil {
				return nil, err
			}
			statuses = append(statuses, *status)
		}
		return statuses, nil
	})
}

func lockBrowserParticipationOwner(ctx context.Context, tx *sqlxTxWrapper, access store.BrowserDeliveryAccess) error {
	if err := lockDeliveryParticipationOwner(ctx, tx, store.DeliveryBudgetAccess{Access: access.Access, ParticipationID: access.ParticipationID}); err != nil {
		return err
	}
	a := access.Access
	var retired bool
	if err := tx.Get(ctx, &retired, `SELECT browser_retired_at IS NOT NULL FROM exam_attempt_delivery_budgets WHERE exam_attempt_id=?`, a.AttemptID.String()); err != nil {
		return err
	}
	if retired {
		return model.ErrDeliveryExpired
	}
	return nil
}
func (s *sqlExamAttemptStore) lockBrowserDelivery(ctx context.Context, tx *sqlxTxWrapper, access store.BrowserDeliveryAccess) error {
	if !access.SourceSessionID.IsValid() {
		return store.NewErrInvalidInput("browser_activity", "source", nil)
	}
	var row struct {
		Part      string         `db:"participation_id"`
		Closure   []byte         `db:"closure_canonical"`
		State     string         `db:"state"`
		Ended     sql.NullTime   `db:"ended_at"`
		Lease     time.Time      `db:"lease_expires_at"`
		PartEnded sql.NullTime   `db:"participation_ended_at"`
		Reason    sql.NullString `db:"end_reason"`
		Now       time.Time      `db:"server_time"`
	}
	if err := tx.Get(ctx, &row, `SELECT participation_id FROM (SELECT id,exam_attempt_id,participation_id,candidate_user_id,registration_id,key_thumbprint FROM browser_activity_sources UNION ALL SELECT source_session_id,exam_attempt_id,participation_id,candidate_user_id,registration_id,key_thumbprint FROM browser_delivery_retired_sources) s WHERE id=?::uuid AND exam_attempt_id=? AND candidate_user_id=? AND registration_id=? AND key_thumbprint=?`, string(access.SourceSessionID), access.Access.AttemptID.String(), access.Access.CandidateUserID.String(), access.Access.DesktopRegistrationID.String(), access.Access.DPoPKeyThumbprint); err != nil {
		return translateError("browser_activity_source", string(access.SourceSessionID), err)
	}
	if access.ParticipationID != "" && access.ParticipationID.String() != row.Part {
		return store.NewErrNotFound("browser_activity_source", string(access.SourceSessionID))
	}
	access.ParticipationID = model.AttemptParticipationID(row.Part)
	if err := lockBrowserParticipationOwner(ctx, tx, access); err != nil {
		return err
	}
	if err := tx.Get(ctx, &row, `SELECT s.participation_id,s.closure_canonical,s.state,s.ended_at,p.lease_expires_at,p.ended_at AS participation_ended_at,p.end_reason,clock_timestamp() AS server_time FROM browser_activity_sources s JOIN exam_attempt_participations p ON p.id=s.participation_id WHERE s.id=?::uuid FOR UPDATE OF s,p`, string(access.SourceSessionID)); err != nil {
		return err
	}
	var closure model.DeliveryClosure
	if len(row.Closure) > 0 && (json.Unmarshal(row.Closure, &closure) != nil || closure.Validate(false) != nil) {
		return invalidPersistedState("browser_source", "closure", model.ErrDeliveryInvalid)
	}
	if closure.ClosedAt == nil && (row.Ended.Valid || row.PartEnded.Valid || !row.Now.Before(row.Lease)) {
		at := row.Lease
		reason := model.DeliveryClosedParticipation
		if row.PartEnded.Valid && row.PartEnded.Time.Before(at) {
			at = row.PartEnded.Time
			reason = nativeParticipationCloseReason(model.AttemptParticipationEndReason(row.Reason.String))
		}
		if row.Ended.Valid && row.Ended.Time.Before(at) {
			at = row.Ended.Time
		}
		if err := closeBrowserSource(ctx, tx, access.SourceSessionID, reason, at); err != nil {
			return err
		}
	} else if closure.ClosedAt == nil {
		if _, err := s.lockCandidateGuard(ctx, tx, access.Access); err != nil {
			return err
		}
	}
	return nil
}

func closeSittingBrowserSources(ctx context.Context, tx *sqlxTxWrapper, sittingID model.ExamSittingID, enabled bool, at time.Time) error {
	reason := model.DeliveryClosedPolicyCorrection
	if !enabled {
		reason = model.DeliveryClosedBrowserDisabled
	}
	// The correction owns the Sitting write fence. Page the affected candidates
	// while retaining one transaction, so no source is left on a superseded policy.
	for {
		var ids []string
		if err := tx.Select(ctx, &ids, `SELECT id::text FROM browser_activity_sources WHERE exam_sitting_id=? AND state='current' ORDER BY id LIMIT 128 FOR UPDATE`, sittingID.String()); err != nil {
			return err
		}
		if len(ids) == 0 {
			return nil
		}
		for _, id := range ids {
			if err := closeBrowserSource(ctx, tx, model.BrowserSourceSessionID(id), reason, at); err != nil {
				return err
			}
		}
	}
}

func (s *sqlExamAttemptStore) ResolveLiveDeliveryTarget(ctx context.Context, access store.CandidateAttemptAccess) (*store.DeliveryOwnerTarget, error) {
	return runSQLTransaction(ctx, s.GetMaster().Begin, "resolve Browser source owner", func(ctx context.Context, tx *sqlxTxWrapper) (*store.DeliveryOwnerTarget, error) {
		guard, err := s.lockCandidateGuard(ctx, tx, access)
		if err != nil {
			return nil, err
		}
		return &store.DeliveryOwnerTarget{SittingID: model.ExamSittingID(guard.SittingID), ClassID: model.ClassID(guard.ClassID)}, nil
	})
}
func completeBrowserSourceAudit(ctx context.Context, tx *sqlxTxWrapper, input *store.BrowserActivitySourceStart, replayed bool, reason string) error {
	data, err := model.EncodeAuditData(map[string]any{"source_session_id": string(input.SourceSessionID), "start_transition": input.Transition.Kind, "replayed": replayed, "result": reason})
	if err != nil {
		return err
	}
	_, err = completeAuditEvent(ctx, tx, input.AuditEventID, model.AuditStatusSuccess, "", data, input.AuditAt)
	return err
}
