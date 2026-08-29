// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package sqlstore

import (
	"context"
	"errors"
	"fmt"
	"math"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

type integrityDiscrepancyTarget struct {
	AttemptID       model.ExamAttemptID
	ParticipationID model.AttemptParticipationID
	Generation      int64
	ConnectionID    model.AttemptConnectionID
}

func automaticIntegrityDiscrepancyTarget(target store.ExamSubmissionAutomaticSealTarget) integrityDiscrepancyTarget {
	return integrityDiscrepancyTarget{AttemptID: target.AttemptID, ParticipationID: target.ParticipationID,
		Generation: target.Generation, ConnectionID: target.ConnectionID}
}

type terminalIntegrityDiscrepancies struct {
	FocusUnresolved    int64
	FocusReason        model.IntegrityDiscrepancyFocusLossGapReason
	BrowserUnresolved  int64
	BrowserActivity    model.BrowserActivitySubmission
	MissingCorrections []model.ExamRevisionID
}

func (value terminalIntegrityDiscrepancies) totalUnresolved() (int64, error) {
	total := value.FocusUnresolved
	if total < 0 || value.BrowserUnresolved < 0 || value.BrowserUnresolved > math.MaxInt64-total {
		return 0, errors.New("terminal integrity discrepancy count overflows")
	}
	total += value.BrowserUnresolved
	if int64(len(value.MissingCorrections)) > math.MaxInt64-total {
		return 0, errors.New("terminal integrity discrepancy count overflows")
	}
	return total + int64(len(value.MissingCorrections)), nil
}

func insertTerminalIntegrityDiscrepancies(ctx context.Context, tx *sqlxTxWrapper, submission *model.ExamSubmission,
	target integrityDiscrepancyTarget, value terminalIntegrityDiscrepancies,
) error {
	if submission == nil || !submission.ID.IsValid() || !target.AttemptID.IsValid() ||
		!target.ParticipationID.IsValid() || target.Generation < 1 || !target.ConnectionID.IsValid() {
		return store.NewErrInvalidInput("integrity_discrepancy", "terminal", nil)
	}
	total, err := value.totalUnresolved()
	if err != nil || total != submission.UnresolvedIntegrityCount {
		return invalidPersistedState("integrity_discrepancy", "terminal_count", errors.New("Submission gap accounting mismatch"))
	}
	recordCount := len(value.MissingCorrections)
	if value.FocusUnresolved > 0 {
		recordCount++
	}
	if value.BrowserUnresolved > 0 {
		recordCount++
	}
	if recordCount > model.SubmissionReviewMaximumDiscrepancies {
		return store.NewErrConflict("integrity_discrepancy", "integrity_discrepancy_limit", nil)
	}
	if value.FocusUnresolved > 0 {
		discrepancy, createErr := model.NewIntegrityDiscrepancy(model.IntegrityDiscrepancySpecification{
			ID: model.NewIntegrityDiscrepancyID(), SubmissionID: submission.ID, AttemptID: target.AttemptID,
			ParticipationID: target.ParticipationID, Generation: target.Generation,
			Kind: model.IntegrityDiscrepancyFocusLossGap, SchemaVersion: 1,
			GapReason: string(value.FocusReason), UnresolvedCount: value.FocusUnresolved, ReceivedAt: submission.SubmittedAt,
		})
		if createErr != nil {
			return invalidPersistedState("integrity_discrepancy", "focus_loss_gap", createErr)
		}
		if err = insertTerminalIntegrityDiscrepancy(ctx, tx, discrepancy, target); err != nil {
			return err
		}
	}
	if value.BrowserUnresolved > 0 {
		reason := string(value.BrowserActivity.GapReason)
		if reason == "" {
			reason = string(model.IntegrityDiscrepancyBrowserActivityPriorSourceGap)
		}
		discrepancy, createErr := model.NewIntegrityDiscrepancy(model.IntegrityDiscrepancySpecification{
			ID: model.NewIntegrityDiscrepancyID(), SubmissionID: submission.ID, AttemptID: target.AttemptID,
			ParticipationID: target.ParticipationID, Generation: target.Generation,
			Kind: model.IntegrityDiscrepancyBrowserActivityGap, SchemaVersion: 1,
			BrowserSourceSessionID: value.BrowserActivity.SourceSessionID,
			FinalSequence:          value.BrowserActivity.FinalSequence, GapReason: reason,
			UnresolvedCount: value.BrowserUnresolved, ReceivedAt: submission.SubmittedAt,
		})
		if createErr != nil {
			return invalidPersistedState("integrity_discrepancy", "browser_activity_gap", createErr)
		}
		if err = insertTerminalIntegrityDiscrepancy(ctx, tx, discrepancy, target); err != nil {
			return err
		}
	}
	for _, revisionID := range value.MissingCorrections {
		discrepancy, createErr := model.NewIntegrityDiscrepancy(model.IntegrityDiscrepancySpecification{
			ID: model.NewIntegrityDiscrepancyID(), SubmissionID: submission.ID, AttemptID: target.AttemptID,
			ParticipationID: target.ParticipationID, Generation: target.Generation,
			Kind: model.IntegrityDiscrepancyCorrectionAcknowledgementMissing, SchemaVersion: 1,
			CorrectionRevisionID: revisionID, UnresolvedCount: 1, ReceivedAt: submission.SubmittedAt,
		})
		if createErr != nil {
			return invalidPersistedState("integrity_discrepancy", "correction_acknowledgement", createErr)
		}
		if err = insertTerminalIntegrityDiscrepancy(ctx, tx, discrepancy, target); err != nil {
			return err
		}
	}
	return nil
}

func insertTerminalIntegrityDiscrepancy(ctx context.Context, tx *sqlxTxWrapper, value *model.IntegrityDiscrepancy,
	target integrityDiscrepancyTarget,
) error {
	_, err := tx.Exec(ctx, `INSERT INTO integrity_discrepancies
		(id,submission_id,exam_attempt_id,participation_id,connection_id,generation,kind,schema_version,
		 correction_revision_id,browser_activity_source_session_id,final_sequence,gap_reason,unresolved_count,received_at)
		VALUES(?,?,?,?,?,?,?,?,?,?::uuid,?,?,?,?)`, value.ID.String(), value.SubmissionID.String(), value.AttemptID.String(),
		value.ParticipationID.String(), target.ConnectionID.String(), value.Generation, string(value.Kind), value.SchemaVersion,
		nullableString(value.CorrectionRevisionID.String()), nullableString(string(value.BrowserSourceSessionID)),
		nullableInt64Pointer(value.FinalSequence), nullableString(value.GapReason), nullableInt64(value.UnresolvedCount > 0, value.UnresolvedCount),
		value.ReceivedAt)
	if err != nil {
		return fmt.Errorf("insert terminal Integrity Discrepancy: %w", translateError("integrity_discrepancy", value.ID.String(), err))
	}
	return nil
}
