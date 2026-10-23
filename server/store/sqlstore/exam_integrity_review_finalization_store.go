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

// reviewFinalizationInventory is the locked integrity inventory a Submission
// Review finalizes over.
type reviewFinalizationInventory struct {
	flags         []model.IntegrityReviewInventoryFlag
	evidence      []model.IntegrityReviewInventoryEvidence
	discrepancies []model.IntegrityReviewInventoryDiscrepancy
}

// reviewFinalizationDelivery is the delivery and native-condition state that
// a finalization digest and snapshot bind.
type reviewFinalizationDelivery struct {
	settlement       model.BrowserSubmissionSettlement
	deliveryRevision int64
	nativeInventory  model.NativeConditionInventory
}

func finalizeReview(ctx context.Context, tx *sqlxTxWrapper, input *store.ExamIntegrityReviewFinalize) (examIntegrityReviewOutcome, error) {
	auth, err := lockReviewScope(ctx, tx, input.SubmissionID, input.ActorUserID, input.ManagerOverride)
	if err != nil {
		return examIntegrityReviewOutcome{}, err
	}
	review, err := loadReviewForMutation(ctx, tx, input.SubmissionID)
	if err != nil {
		return examIntegrityReviewOutcome{}, err
	}
	if review.ID != input.ReviewID || review.Revision != input.ExpectedRevision {
		return examIntegrityReviewOutcome{}, store.NewErrConflict("submission_review", "integrity_review_revision", nil)
	}
	var submissionState string
	if err = tx.Get(ctx, &submissionState, `SELECT integrity_state FROM exam_submissions WHERE id=? AND sealed=true`, input.SubmissionID.String()); err != nil {
		return examIntegrityReviewOutcome{}, err
	}
	if submissionState != "settled" && submissionState != "gapped" {
		return examIntegrityReviewOutcome{}, store.NewErrConflict("submission_review", "integrity_review_incomplete", nil)
	}
	var inventory reviewFinalizationInventory
	if inventory.flags, err = lockReviewInventoryFlags(ctx, tx, review.ID, auth.AttemptID); err != nil {
		return examIntegrityReviewOutcome{}, err
	}
	if inventory.evidence, err = lockReviewInventoryEvidence(ctx, tx, auth.AttemptID); err != nil {
		return examIntegrityReviewOutcome{}, err
	}
	if inventory.discrepancies, err = lockReviewInventoryDiscrepancies(ctx, tx, input.SubmissionID); err != nil {
		return examIntegrityReviewOutcome{}, err
	}
	digest, delivery, err := reviewFinalizationDigest(ctx, tx, auth.AttemptID, inventory)
	if err != nil {
		return examIntegrityReviewOutcome{}, err
	}
	if err = insertReviewInventory(ctx, tx, review, auth.AttemptID, inventory); err != nil {
		return examIntegrityReviewOutcome{}, err
	}
	if err = review.Finalize(input.ExpectedRevision, input.ActorUserID, len(inventory.flags), len(inventory.evidence), len(inventory.discrepancies), digest, input.ChangedAt); err != nil {
		return examIntegrityReviewOutcome{}, err
	}
	if err = persistReview(ctx, tx, review, input.ExpectedRevision); err != nil {
		return examIntegrityReviewOutcome{}, err
	}
	if err = insertReviewFinalizationSnapshot(ctx, tx, review, auth.AttemptID, delivery); err != nil {
		return examIntegrityReviewOutcome{}, err
	}
	out := examIntegrityReviewOutcome{Authorization: *auth, Review: *review}
	if err = completeIntegrityReviewAudit(ctx, tx, out, input.AuditEventID, input.AuditAt, false, ""); err != nil {
		return examIntegrityReviewOutcome{}, err
	}
	return out, nil
}

// lockReviewInventoryFlags share-locks the Attempt's integrity flags and
// requires a current decision in the Review for each.
func lockReviewInventoryFlags(ctx context.Context, tx *sqlxTxWrapper, reviewID model.SubmissionReviewID, attemptID model.ExamAttemptID) ([]model.IntegrityReviewInventoryFlag, error) {
	type flagInventoryRow struct {
		FlagID           string         `db:"flag_id"`
		DecisionID       sql.NullString `db:"decision_id"`
		DecisionRevision sql.NullInt64  `db:"decision_revision"`
	}
	var flags []flagInventoryRow
	if err := tx.Select(ctx, &flags, `SELECT f.id AS flag_id,d.id AS decision_id,d.revision AS decision_revision FROM integrity_flags f LEFT JOIN integrity_review_decisions d ON d.integrity_flag_id=f.id AND d.submission_review_id=? AND NOT d.inventory_stale WHERE f.exam_attempt_id=? ORDER BY f.id LIMIT 457 FOR SHARE OF f`, reviewID.String(), attemptID.String()); err != nil {
		return nil, err
	}
	if len(flags) > model.SubmissionReviewMaximumFlags {
		return nil, store.NewErrConflict("submission_review", "integrity_review_too_large", nil)
	}
	flagItems := make([]model.IntegrityReviewInventoryFlag, 0, len(flags))
	for _, row := range flags {
		if !row.DecisionID.Valid {
			return nil, store.NewErrConflict("submission_review", "integrity_review_incomplete", nil)
		}
		flagID, e := model.ParseIntegrityFlagID(row.FlagID)
		if e != nil {
			return nil, e
		}
		decisionID, e := model.ParseIntegrityReviewDecisionID(row.DecisionID.String)
		if e != nil {
			return nil, e
		}
		flagItems = append(flagItems, model.IntegrityReviewInventoryFlag{FlagID: flagID, DecisionID: decisionID, DecisionRevision: row.DecisionRevision.Int64})
	}
	return flagItems, nil
}

// lockReviewInventoryEvidence share-locks the Attempt's integrity evidence.
func lockReviewInventoryEvidence(ctx context.Context, tx *sqlxTxWrapper, attemptID model.ExamAttemptID) ([]model.IntegrityReviewInventoryEvidence, error) {
	type evidenceInventoryRow struct {
		EvidenceID string `db:"evidence_id"`
		FlagID     string `db:"flag_id"`
	}
	var evidenceRows []evidenceInventoryRow
	if err := tx.Select(ctx, &evidenceRows, `SELECT e.id AS evidence_id,e.integrity_flag_id AS flag_id FROM integrity_evidence e WHERE e.exam_attempt_id=? ORDER BY e.id LIMIT 30001 FOR SHARE`, attemptID.String()); err != nil {
		return nil, err
	}
	if len(evidenceRows) > model.SubmissionReviewMaximumEvidence {
		return nil, store.NewErrConflict("submission_review", "integrity_review_too_large", nil)
	}
	evidenceItems := make([]model.IntegrityReviewInventoryEvidence, 0, len(evidenceRows))
	for _, row := range evidenceRows {
		evidenceID, e := model.ParseIntegrityEvidenceID(row.EvidenceID)
		if e != nil {
			return nil, e
		}
		flagID, e := model.ParseIntegrityFlagID(row.FlagID)
		if e != nil {
			return nil, e
		}
		evidenceItems = append(evidenceItems, model.IntegrityReviewInventoryEvidence{EvidenceID: evidenceID, FlagID: flagID})
	}
	return evidenceItems, nil
}

// lockReviewInventoryDiscrepancies share-locks the Submission's integrity
// discrepancies.
func lockReviewInventoryDiscrepancies(ctx context.Context, tx *sqlxTxWrapper, submissionID model.SubmissionID) ([]model.IntegrityReviewInventoryDiscrepancy, error) {
	var discrepancyRows []struct {
		DiscrepancyID string `db:"discrepancy_id"`
	}
	if err := tx.Select(ctx, &discrepancyRows, `SELECT id AS discrepancy_id FROM integrity_discrepancies
		WHERE submission_id=? ORDER BY id LIMIT 201 FOR SHARE`, submissionID.String()); err != nil {
		return nil, err
	}
	if len(discrepancyRows) > model.SubmissionReviewMaximumDiscrepancies {
		return nil, store.NewErrConflict("submission_review", "integrity_review_too_large", nil)
	}
	discrepancyItems := make([]model.IntegrityReviewInventoryDiscrepancy, 0, len(discrepancyRows))
	for _, row := range discrepancyRows {
		id, e := model.ParseIntegrityDiscrepancyID(row.DiscrepancyID)
		if e != nil {
			return nil, e
		}
		discrepancyItems = append(discrepancyItems, model.IntegrityReviewInventoryDiscrepancy{DiscrepancyID: id})
	}
	return discrepancyItems, nil
}

// reviewFinalizationDigest digests the inventory together with the browser
// settlement, delivery inventory revision and native-condition inventory.
func reviewFinalizationDigest(ctx context.Context, tx *sqlxTxWrapper, attemptID model.ExamAttemptID, inventory reviewFinalizationInventory) (string, reviewFinalizationDelivery, error) {
	var delivery reviewFinalizationDelivery
	digest, err := model.IntegrityReviewInventoryDigest(inventory.flags, inventory.evidence, inventory.discrepancies)
	if err != nil {
		return "", delivery, err
	}
	delivery.settlement, err = browserSubmissionSettlement(ctx, tx, attemptID)
	if err != nil {
		return "", delivery, err
	}
	if err = tx.Get(ctx, &delivery.deliveryRevision, `SELECT review_inventory_revision FROM exam_attempt_delivery_budgets WHERE exam_attempt_id=?`, attemptID.String()); err != nil {
		return "", delivery, err
	}
	digest, err = model.IntegrityReviewDeliveryDigest(digest, delivery.settlement, delivery.deliveryRevision)
	if err != nil {
		return "", delivery, err
	}
	delivery.nativeInventory, err = nativeConditionInventory(ctx, tx, attemptID)
	if err != nil {
		return "", delivery, err
	}
	digest, err = model.IntegrityReviewNativeDigest(digest, delivery.nativeInventory)
	if err != nil {
		return "", delivery, err
	}
	return digest, delivery, nil
}

// insertReviewInventory records the finalized flag, evidence and discrepancy
// inventory under the next Review revision.
func insertReviewInventory(ctx context.Context, tx *sqlxTxWrapper, review *model.SubmissionReview, attemptID model.ExamAttemptID, inventory reviewFinalizationInventory) error {
	for _, item := range inventory.flags {
		if _, err := tx.Exec(ctx, `INSERT INTO submission_review_inventory_flags(submission_review_id,finalization_revision,exam_attempt_id,integrity_flag_id,decision_id,decision_revision) VALUES(?,?,?,?,?,?)`, review.ID.String(), review.Revision+1, attemptID.String(), item.FlagID.String(), item.DecisionID.String(), item.DecisionRevision); err != nil {
			return err
		}
	}
	for _, item := range inventory.evidence {
		if _, err := tx.Exec(ctx, `INSERT INTO submission_review_inventory_evidence(submission_review_id,finalization_revision,exam_attempt_id,integrity_flag_id,integrity_evidence_id) VALUES(?,?,?,?,?)`, review.ID.String(), review.Revision+1, attemptID.String(), item.FlagID.String(), item.EvidenceID.String()); err != nil {
			return err
		}
	}
	for _, item := range inventory.discrepancies {
		if _, err := tx.Exec(ctx, `INSERT INTO submission_review_inventory_discrepancies
			(submission_review_id,finalization_revision,submission_id,exam_attempt_id,integrity_discrepancy_id) VALUES(?,?,?,?,?)`,
			review.ID.String(), review.Revision+1, review.SubmissionID.String(), attemptID.String(), item.DiscrepancyID.String()); err != nil {
			return err
		}
	}
	return nil
}

// insertReviewFinalizationSnapshot records the canonical snapshot of the
// finalized Review, its decisions, and the browser and native evidence state.
func insertReviewFinalizationSnapshot(ctx context.Context, tx *sqlxTxWrapper, review *model.SubmissionReview, attemptID model.ExamAttemptID, delivery reviewFinalizationDelivery) error {
	var decisionRows []examIntegrityReviewDecisionRow
	if err := tx.Select(ctx, &decisionRows, examIntegrityReviewDecisionSelect+` WHERE submission_review_id=? ORDER BY integrity_flag_id LIMIT 457`, review.ID.String()); err != nil {
		return err
	}
	var browserGroups []struct {
		Flag     string       `db:"integrity_flag_id"`
		Details  int64        `db:"retained_details"`
		Overflow int64        `db:"overflow_count"`
		First    sql.NullTime `db:"overflow_first_received_at"`
		Last     sql.NullTime `db:"overflow_last_received_at"`
	}
	if err := tx.Select(ctx, &browserGroups, `SELECT integrity_flag_id,retained_details,overflow_count,overflow_first_received_at,overflow_last_received_at FROM browser_integrity_groups WHERE exam_attempt_id=? ORDER BY integrity_flag_id LIMIT 257`, attemptID.String()); err != nil {
		return err
	}
	if len(browserGroups) > 256 {
		return model.ErrDeliveryInvalid
	}
	var browserOverflow []struct {
		Count int64     `db:"validated_event_count"`
		First time.Time `db:"first_received_at"`
		Last  time.Time `db:"last_received_at"`
	}
	if err := tx.Select(ctx, &browserOverflow, `SELECT validated_event_count,first_received_at,last_received_at FROM browser_integrity_overflow WHERE exam_attempt_id=?`, attemptID.String()); err != nil {
		return err
	}
	snapshot, err := canonicalPreflightValue(map[string]any{"native_conditions": delivery.nativeInventory, "review": review, "decisions": decisionRows, "browser_activity": delivery.settlement, "delivery_inventory_revision": delivery.deliveryRevision, "browser_groups": browserGroups, "browser_evidence_overflow": browserOverflow})
	if err != nil {
		return err
	}
	if len(snapshot) > 2*1024*1024 {
		return model.ErrDeliveryInvalid
	}
	if _, err := tx.Exec(ctx, `INSERT INTO submission_review_finalizations(submission_review_id,finalization_revision,exam_attempt_id,snapshot_canonical) VALUES(?,?,?,?)`, review.ID.String(), review.Revision, attemptID.String(), snapshot); err != nil {
		return err
	}
	return nil
}
