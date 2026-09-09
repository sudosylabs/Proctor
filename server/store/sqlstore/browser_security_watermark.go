// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package sqlstore

import (
	"context"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

type browserControlSource struct {
	ID           string `db:"id"`
	Slot         int64  `db:"start_ordinal"`
	Allocated    int64  `db:"allocated_through_sequence"`
	Acknowledged int64  `db:"highest_contiguous"`
	Closed       bool   `db:"closed"`
}

// Validate every selector before processing the control receipt. A foreign
// selector fails the entire request; a closed owned source is a local rejection.
func lockBrowserControlSources(ctx context.Context, tx *sqlxTxWrapper, input *store.ExamAttemptParticipationRenewal) (map[string]browserControlSource, error) {
	sources := make(map[string]browserControlSource)
	for _, mark := range input.SecurityCoverage.DeliveryWatermarks {
		if mark.Family != "browser" {
			continue
		}
		var row browserControlSource
		err := tx.Get(ctx, &row, `SELECT id::text,start_ordinal,allocated_through_sequence,highest_contiguous,state<>'current' AS closed FROM browser_activity_sources WHERE id=?::uuid AND exam_attempt_id=? AND participation_id=? AND generation=? AND candidate_user_id=? AND registration_id=? AND key_thumbprint=? FOR UPDATE`, mark.SourceID, input.AttemptID.String(), input.ParticipationID.String(), input.Generation, input.CandidateUserID.String(), input.DesktopRegistrationID.String(), input.DPoPKeyThumbprint)
		if err != nil {
			return nil, translateError("delivery_source", mark.SourceID, err)
		}
		if mark.AcknowledgedThroughSequence > row.Acknowledged {
			return nil, store.NewErrInvalidInput("security_control", "acknowledgement", nil)
		}
		sources[mark.SourceID] = row
	}
	return sources, nil
}

func applyBrowserControlWatermark(ctx context.Context, tx *sqlxTxWrapper, input *store.ExamAttemptParticipationRenewal, mark model.DeliveryWatermark, source browserControlSource) (*[2]int64, error) {
	if source.Closed {
		value := [2]int64{source.Slot, 0}
		return &value, nil
	}
	delta := mark.AllocatedThroughSequence - source.Allocated
	if delta <= 0 {
		return nil, nil
	}
	part, budget, _, err := browserQuotas(ctx, tx, input.ParticipationID, input.AttemptID)
	if err != nil {
		return nil, err
	}
	if part.SummaryOnly || budget.SummaryOnly || delta > model.BrowserParticipationPositionLimit-part.AllocatedPositions || delta > model.BrowserAttemptPositionLimit-budget.AllocatedPositions {
		if !part.SummaryOnly && !budget.SummaryOnly {
			if err := latchBrowserDelivery(ctx, tx, input.AttemptID, input.ParticipationID, model.DeliveryStopPositions, delta > model.BrowserAttemptPositionLimit-budget.AllocatedPositions); err != nil {
				return nil, err
			}
		}
		// Fixed source state and pending interpretation have reserved capacity.
		var ids []string
		if err := tx.Select(ctx, &ids, `SELECT id::text FROM browser_activity_sources WHERE exam_attempt_id=? AND terminal_missing_through_sequence>settled_through_sequence ORDER BY participation_id,start_ordinal LIMIT ? FOR UPDATE`, input.AttemptID.String(), maximumAttemptBrowserSources+1); err != nil {
			return nil, err
		}
		if int64(len(ids)) > maximumAttemptBrowserSources {
			return nil, invalidPersistedState("browser_watermark", "value", model.ErrDeliveryInvalid)
		}
		for _, id := range ids {
			if _, err := settleBrowserSource(ctx, tx, model.BrowserSourceSessionID(id)); err != nil {
				return nil, err
			}
		}
		value := [2]int64{source.Slot, 1}
		return &value, nil
	}
	if _, err := tx.Exec(ctx, `UPDATE browser_activity_sources SET allocated_through_sequence=? WHERE id=?::uuid`, mark.AllocatedThroughSequence, source.ID); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, `UPDATE exam_attempt_security_owners SET browser_allocated_positions=browser_allocated_positions+? WHERE participation_id=?`, delta, input.ParticipationID.String()); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, `UPDATE exam_attempt_delivery_budgets SET browser_allocated_positions=browser_allocated_positions+? WHERE exam_attempt_id=?`, delta, input.AttemptID.String()); err != nil {
		return nil, err
	}
	if err := markBrowserInventoryChanged(ctx, tx, input.AttemptID); err != nil {
		return nil, err
	}
	if _, err := settleBrowserSource(ctx, tx, model.BrowserSourceSessionID(source.ID)); err != nil {
		return nil, err
	}
	return nil, nil
}
