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

func lockDeliveryParticipationOwner(ctx context.Context, tx *sqlxTxWrapper, access store.DeliveryBudgetAccess) error {
	a := access.Access
	if !a.AttemptID.IsValid() || !a.CandidateUserID.IsValid() || !a.SessionID.IsValid() || !a.DesktopRegistrationID.IsValid() || !model.IsValidDPoPKeyThumbprint(a.DPoPKeyThumbprint) || !access.ParticipationID.IsValid() {
		return store.NewErrInvalidInput("browser_activity", "owner", nil)
	}
	var id string
	if err := tx.Get(ctx, &id, `SELECT sit.id FROM exam_sittings sit JOIN exam_attempts a ON a.exam_sitting_id=sit.id WHERE a.id=? AND a.candidate_user_id=? FOR UPDATE OF sit`, a.AttemptID.String(), a.CandidateUserID.String()); err != nil {
		return translateError("browser_activity_source", access.ParticipationID.String(), err)
	}
	if err := tx.Get(ctx, &id, `SELECT id FROM exam_attempts WHERE id=? FOR UPDATE`, a.AttemptID.String()); err != nil {
		return err
	}
	if err := tx.Get(ctx, &id, `SELECT se.id FROM sessions se JOIN users u ON u.id=se.user_id JOIN desktop_registrations dr ON dr.id=se.desktop_registration_id AND dr.user_id=u.id WHERE se.id=? AND se.user_id=? AND se.desktop_registration_id=? AND se.dpop_key_thumbprint=? AND dr.key_thumbprint=? AND dr.revoked_at IS NULL AND se.archived_at IS NULL AND se.revoked_at IS NULL AND se.expires_at>clock_timestamp() AND se.idle_expires_at>clock_timestamp() AND u.archived_at IS NULL AND u.disabled_at IS NULL FOR SHARE OF se,u,dr`, a.SessionID.String(), a.CandidateUserID.String(), a.DesktopRegistrationID.String(), a.DPoPKeyThumbprint, a.DPoPKeyThumbprint); err != nil {
		return translateError("browser_activity_source", access.ParticipationID.String(), err)
	}
	if err := tx.Get(ctx, &id, `SELECT participation_id FROM exam_attempt_security_owners WHERE participation_id=? AND exam_attempt_id=? AND registration_id=? AND key_thumbprint=? FOR UPDATE`, access.ParticipationID.String(), a.AttemptID.String(), a.DesktopRegistrationID.String(), a.DPoPKeyThumbprint); err != nil {
		return translateError("browser_activity_source", access.ParticipationID.String(), err)
	}
	return nil
}

func deliveryBudgetSnapshot(ctx context.Context, tx *sqlxTxWrapper, access store.DeliveryBudgetAccess) (*model.DeliveryBudgetSnapshot, error) {
	value := &model.DeliveryBudgetSnapshot{ParticipationID: access.ParticipationID, Native: model.DeliveryFamilyBudget{Participation: model.NewDeliveryQuotaUsage(true, false), Attempt: model.NewDeliveryQuotaUsage(true, true), PendingByteLimit: model.DeliveryPendingByteLimit}, Browser: model.DeliveryFamilyBudget{Participation: model.NewDeliveryQuotaUsage(false, false), Attempt: model.NewDeliveryQuotaUsage(false, true), PendingByteLimit: model.DeliveryPendingByteLimit}}
	var counts struct {
		Generation     int64     `db:"generation"`
		NativePending  int64     `db:"native_pending_bytes"`
		BrowserPending int64     `db:"browser_pending_bytes"`
		Groups         int64     `db:"browser_flag_groups"`
		Records        int64     `db:"browser_evidence_records"`
		Bytes          int64     `db:"browser_evidence_bytes"`
		Intervals      int64     `db:"explicit_missing_intervals"`
		Metadata       int64     `db:"control_metadata_bytes"`
		Now            time.Time `db:"server_time"`
	}
	if err := tx.Get(ctx, &counts, `SELECT p.generation,b.native_pending_bytes,b.browser_pending_bytes,b.browser_flag_groups,b.browser_evidence_records,b.browser_evidence_bytes,b.explicit_missing_intervals,b.control_metadata_bytes,clock_timestamp() AS server_time FROM exam_attempt_participations p JOIN exam_attempt_delivery_budgets b ON b.exam_attempt_id=p.exam_attempt_id WHERE p.id=? AND p.exam_attempt_id=?`, access.ParticipationID.String(), access.Access.AttemptID.String()); err != nil {
		return nil, err
	}
	value.Generation = counts.Generation
	value.ServerTime = counts.Now.UTC().Truncate(time.Millisecond)
	value.Native.PendingBytes = counts.NativePending
	value.Browser.PendingBytes = counts.BrowserPending
	value.BrowserFlagGroups = counts.Groups
	value.BrowserEvidenceRecords = counts.Records
	value.BrowserEvidenceBytes = counts.Bytes
	value.ExplicitMissingIntervals = counts.Intervals
	value.ControlMetadataBytes = counts.Metadata
	for _, item := range []struct {
		q        string
		selector string
		usage    *model.DeliveryQuotaUsage
	}{
		{`SELECT retained_records,retained_bytes,allocated_through_sequence AS positions,summary_only,stop_reason FROM exam_attempt_security_owners WHERE participation_id=?`, access.ParticipationID.String(), &value.Native.Participation},
		{`SELECT native_retained_records AS retained_records,native_retained_bytes AS retained_bytes,native_allocated_positions AS positions,native_summary_only AS summary_only,native_stop_reason AS stop_reason FROM exam_attempt_delivery_budgets WHERE exam_attempt_id=?`, access.Access.AttemptID.String(), &value.Native.Attempt},
		{`SELECT browser_retained_records AS retained_records,browser_retained_bytes AS retained_bytes,browser_allocated_positions AS positions,browser_summary_only AS summary_only,browser_stop_reason AS stop_reason FROM exam_attempt_security_owners WHERE participation_id=?`, access.ParticipationID.String(), &value.Browser.Participation},
		{`SELECT browser_retained_records AS retained_records,browser_retained_bytes AS retained_bytes,browser_allocated_positions AS positions,browser_summary_only AS summary_only,browser_stop_reason AS stop_reason FROM exam_attempt_delivery_budgets WHERE exam_attempt_id=?`, access.Access.AttemptID.String(), &value.Browser.Attempt},
	} {
		var row struct {
			Records   int64          `db:"retained_records"`
			Bytes     int64          `db:"retained_bytes"`
			Positions int64          `db:"positions"`
			Summary   bool           `db:"summary_only"`
			Reason    sql.NullString `db:"stop_reason"`
		}
		if err := tx.Get(ctx, &row, item.q, item.selector); err != nil {
			return nil, err
		}
		item.usage.RetainedRecords = row.Records
		item.usage.RetainedBytes = row.Bytes
		item.usage.AllocatedPositions = row.Positions
		item.usage.SummaryOnly = row.Summary
		if row.Reason.Valid {
			reason := model.DeliveryStopReason(row.Reason.String)
			item.usage.StopReason = &reason
		}
	}
	if err := validatePersistedModel("delivery_budget", value); err != nil {
		return nil, err
	}
	raw, err := canonicalPreflightValue(value)
	if err != nil {
		return nil, err
	}
	if len(raw) > 8192 {
		return nil, invalidPersistedState("delivery_budget", "value", model.ErrDeliveryInvalid)
	}
	return value, nil
}
func (s *sqlExamAttemptStore) DeliveryBudget(ctx context.Context, access store.DeliveryBudgetAccess) (*model.DeliveryBudgetSnapshot, error) {
	return runSQLTransaction(ctx, s.GetMaster().Begin, "delivery budget", func(ctx context.Context, tx *sqlxTxWrapper) (*model.DeliveryBudgetSnapshot, error) {
		if err := lockDeliveryParticipationOwner(ctx, tx, access); err != nil {
			return nil, err
		}
		return deliveryBudgetSnapshot(ctx, tx, access)
	})
}
func (s *sqlExamAttemptStore) lockDeliveryStop(ctx context.Context, tx *sqlxTxWrapper, access store.CandidateAttemptAccess) (store.DeliveryBudgetAccess, error) {
	var part string
	if !access.ConnectionID.IsValid() {
		return store.DeliveryBudgetAccess{}, store.NewErrInvalidInput("delivery_budget", "connection", nil)
	}
	if err := tx.Get(ctx, &part, `SELECT participation_id FROM exam_attempt_connections WHERE id=? AND exam_attempt_id=? AND session_id=?`, access.ConnectionID.String(), access.AttemptID.String(), access.SessionID.String()); err != nil {
		return store.DeliveryBudgetAccess{}, translateError("delivery_budget", access.AttemptID.String(), err)
	}
	value := store.DeliveryBudgetAccess{Access: access, ParticipationID: model.AttemptParticipationID(part)}
	if err := lockDeliveryParticipationOwner(ctx, tx, value); err != nil {
		return value, err
	}
	if _, err := s.lockCandidateGuard(ctx, tx, access); err != nil {
		return value, err
	}
	return value, nil
}
func (s *sqlExamAttemptStore) StopDeliveryDetails(ctx context.Context, input *store.DeliveryDetailsStop, command *store.CommandIdempotency) (*model.StopDeliveryDetailsResult, error) {
	if input == nil || input.Request.Validate() != nil || !model.IsValidId(input.AuditEventID) || input.AuditAt <= 0 || command == nil || command.Operation != store.DeliveryStopDetailsOperation || command.UserID != input.Access.CandidateUserID {
		return nil, store.NewErrInvalidInput("delivery_budget", "stop", nil)
	}
	project := func(ctx context.Context, tx *sqlxTxWrapper, prior *model.StopDeliveryDetailsResult) (*model.StopDeliveryDetailsResult, error) {
		access, err := s.lockDeliveryStop(ctx, tx, input.Access)
		if err != nil {
			return nil, err
		}
		if prior != nil && prior.Budget.ParticipationID != access.ParticipationID {
			return nil, store.NewErrNotFound("delivery_budget", access.ParticipationID.String())
		}
		value := &model.StopDeliveryDetailsResult{Browser: []model.BrowserSourceStatus{}}
		if input.Request.Family == "native" {
			var stream string
			if err := tx.Get(ctx, &stream, `SELECT delivery_stream_id FROM exam_attempt_security_owners WHERE participation_id=?`, access.ParticipationID.String()); err != nil {
				return nil, err
			}
			owner, err := s.lockNativeDelivery(ctx, tx, store.NativeDeliveryAccess{Access: input.Access, StreamID: stream, ParticipationID: access.ParticipationID})
			if err != nil {
				return nil, err
			}
			value.Native, err = nativeDeliveryStatus(ctx, tx, owner, stream)
			if err != nil {
				return nil, err
			}
		} else {
			var ids []string
			if err := tx.Select(ctx, &ids, `SELECT id::text FROM browser_activity_sources WHERE participation_id=? ORDER BY start_ordinal LIMIT 50`, access.ParticipationID.String()); err != nil {
				return nil, err
			}
			if len(ids) > 49 {
				return nil, invalidPersistedState("delivery_budget", "value", model.ErrDeliveryInvalid)
			}
			for _, id := range ids {
				project := browserSourceStatus
				if prior == nil {
					project = settleBrowserSource
				}
				v, err := project(ctx, tx, model.BrowserSourceSessionID(id))
				if err != nil {
					return nil, err
				}
				value.Browser = append(value.Browser, *v)
			}
		}
		budget, err := deliveryBudgetSnapshot(ctx, tx, access)
		if err != nil {
			return nil, err
		}
		value.Budget = *budget
		raw, err := canonicalPreflightValue(value)
		if err != nil {
			return nil, err
		}
		if len(raw) > 1<<20 {
			return nil, invalidPersistedState("delivery_budget", "value", model.ErrDeliveryInvalid)
		}
		return value, nil
	}
	audit := func(ctx context.Context, tx *sqlxTxWrapper, replay bool) error {
		raw, err := model.EncodeAuditData(map[string]any{"exam_attempt_id": input.Access.AttemptID.String(), "family": input.Request.Family, "reason": string(input.Request.Reason), "idempotency_replayed": replay})
		if err != nil {
			return err
		}
		_, err = completeAuditEvent(ctx, tx, input.AuditEventID, model.AuditStatusSuccess, "", raw, input.AuditAt)
		return err
	}
	result, err := runIdempotentMutation(ctx, s.SQLStore, "stop delivery details", idempotentMutation[*model.StopDeliveryDetailsResult]{command: command, auditEventID: input.AuditEventID,
		execute: func(ctx context.Context, tx *sqlxTxWrapper) (*model.StopDeliveryDetailsResult, error) {
			access, err := s.lockDeliveryStop(ctx, tx, input.Access)
			if err != nil {
				return nil, err
			}
			if input.Request.Family == "native" {
				err = latchNativeDelivery(ctx, tx, input.Access.AttemptID, access.ParticipationID.String(), input.Request.Reason, false)
			} else {
				err = latchBrowserDelivery(ctx, tx, input.Access.AttemptID, access.ParticipationID, input.Request.Reason, false)
			}
			if err != nil {
				return nil, err
			}
			value, err := project(ctx, tx, nil)
			if err != nil {
				return nil, err
			}
			if err := audit(ctx, tx, false); err != nil {
				return nil, err
			}
			return value, nil
		}, encode: func(v *model.StopDeliveryDetailsResult) ([]byte, error) {
			if err := v.Validate(input.Request.Family); err != nil {
				return nil, err
			}
			return canonicalPreflightValue(v)
		}, decode: func(version int, raw []byte) (*model.StopDeliveryDetailsResult, error) {
			var v model.StopDeliveryDetailsResult
			if version != 1 {
				return nil, invalidPersistedState("delivery_budget", "value", model.ErrDeliveryInvalid)
			}
			if err := decodeCommandOutcome(raw, &v); err != nil {
				return nil, err
			}
			return &v, nil
		}, hydrateReplay: project, completeReplay: func(ctx context.Context, tx *sqlxTxWrapper, _ *model.StopDeliveryDetailsResult, _ string) error {
			return audit(ctx, tx, true)
		}})
	if err != nil {
		return nil, err
	}
	return result.Value, nil
}
