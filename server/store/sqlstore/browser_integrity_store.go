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
	"github.com/sudosylabs/proctor/server/model"
	"time"
)

// Interpretation runs once per source/sequence under the Attempt fence. Its
// state transition, copy, budget and overflow count commit in one transaction.
func retainBrowserIntegrity(ctx context.Context, tx *sqlxTxWrapper, source model.BrowserSourceSessionID, attempt model.ExamAttemptID, event model.BrowserActivityEvent, policy model.BrowserPolicy, receivedAt time.Time) error {
	rule := model.BrowserIntegrityRule(event, policy)
	if rule == nil {
		return nil
	}
	var retired bool
	if err := tx.Get(ctx, &retired, `SELECT EXISTS(SELECT 1 FROM exam_submissions WHERE exam_attempt_id=? AND integrity_retired_at IS NOT NULL)`, attempt.String()); err != nil {
		return err
	}
	if retired {
		return nil
	}
	var owner struct {
		Part       string `db:"participation_id"`
		Generation int64  `db:"generation"`
	}
	if err := tx.Get(ctx, &owner, `SELECT participation_id,generation FROM browser_activity_sources WHERE id=?::uuid AND exam_attempt_id=?`, string(source), attempt.String()); err != nil {
		return err
	}
	var budget struct {
		Groups  int64     `db:"browser_flag_groups"`
		Records int64     `db:"browser_evidence_records"`
		Bytes   int64     `db:"browser_evidence_bytes"`
		Now     time.Time `db:"now"`
	}
	if err := tx.Get(ctx, &budget, `SELECT browser_flag_groups,browser_evidence_records,browser_evidence_bytes,clock_timestamp() AS now FROM exam_attempt_delivery_budgets WHERE exam_attempt_id=? FOR UPDATE`, attempt.String()); err != nil {
		return err
	}
	var group struct {
		Flag    string `db:"integrity_flag_id"`
		Details int64  `db:"retained_details"`
	}
	err := tx.Get(ctx, &group, `SELECT integrity_flag_id,retained_details FROM browser_integrity_groups WHERE exam_attempt_id=? AND participation_id=? AND policy_revision_id=? AND rule_id=?`, attempt.String(), owner.Part, event.PolicyRevisionID.String(), rule.RuleID)
	if errors.Is(err, sql.ErrNoRows) {
		if budget.Groups >= model.BrowserFlagGroupLimit {
			_, err = tx.Exec(ctx, `INSERT INTO browser_integrity_overflow(exam_attempt_id,validated_event_count,first_received_at,last_received_at,reason) VALUES(?,1,?,?,'group_capacity') ON CONFLICT(exam_attempt_id) DO UPDATE SET validated_event_count=browser_integrity_overflow.validated_event_count+1,first_received_at=LEAST(browser_integrity_overflow.first_received_at,EXCLUDED.first_received_at),last_received_at=GREATEST(browser_integrity_overflow.last_received_at,EXCLUDED.last_received_at)`, attempt.String(), receivedAt, receivedAt)
			if err != nil {
				return err
			}
			return invalidateDeliveryReviewInventory(ctx, tx, attempt)
		}
		group.Flag = model.NewIntegrityFlagID().String()
		if _, err = tx.Exec(ctx, `INSERT INTO integrity_flags(id,exam_attempt_id,generation,policy_kind,state,created_at) VALUES(?,?,?,'browser_navigation','open',?)`, group.Flag, attempt.String(), owner.Generation, budget.Now); err != nil {
			return err
		}
		if _, err = tx.Exec(ctx, `INSERT INTO browser_integrity_groups(integrity_flag_id,exam_attempt_id,participation_id,policy_revision_id,rule_id) VALUES(?,?,?,?,?)`, group.Flag, attempt.String(), owner.Part, event.PolicyRevisionID.String(), rule.RuleID); err != nil {
			return err
		}
		if _, err = tx.Exec(ctx, `UPDATE exam_attempt_delivery_budgets SET browser_flag_groups=browser_flag_groups+1 WHERE exam_attempt_id=?`, attempt.String()); err != nil {
			return err
		}
	} else if err != nil {
		return err
	}
	detail := model.BrowserIntegrityEvidence{SourceSessionID: source, PolicyRevisionID: event.PolicyRevisionID, RuleID: rule.RuleID, Event: event}
	if err := detail.Validate(); err != nil {
		return err
	}
	raw, err := canonicalPreflightValue(detail)
	if err != nil {
		return err
	}
	if len(raw) > 32768 {
		return model.ErrDeliveryInvalid
	}
	if group.Details < 100 && budget.Records < model.BrowserEvidenceRecordLimit && budget.Bytes+int64(len(raw)) <= model.BrowserEvidenceByteLimit {
		if _, err = tx.Exec(ctx, `INSERT INTO integrity_evidence(id,exam_attempt_id,participation_id,integrity_flag_id,generation,policy_kind,observed_at,recorded_at,browser_detail_canonical) VALUES(?,?,?,?,?,'browser_navigation',?,?,?)`, model.NewIntegrityEvidenceID().String(), attempt.String(), owner.Part, group.Flag, owner.Generation, receivedAt, budget.Now, raw); err != nil {
			return err
		}
		if _, err = tx.Exec(ctx, `UPDATE browser_integrity_groups SET retained_details=retained_details+1 WHERE integrity_flag_id=?`, group.Flag); err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `UPDATE exam_attempt_delivery_budgets SET browser_evidence_records=browser_evidence_records+1,browser_evidence_bytes=browser_evidence_bytes+? WHERE exam_attempt_id=?`, len(raw), attempt.String())
	} else {
		_, err = tx.Exec(ctx, `UPDATE browser_integrity_groups SET overflow_count=overflow_count+1,overflow_first_received_at=LEAST(overflow_first_received_at,?),overflow_last_received_at=GREATEST(overflow_last_received_at,?) WHERE integrity_flag_id=?`, receivedAt, receivedAt, group.Flag)
	}
	if err != nil {
		return err
	}
	return staleBrowserGroupDecision(ctx, tx, attempt, group.Flag)
}
