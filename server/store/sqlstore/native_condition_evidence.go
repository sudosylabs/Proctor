// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------
package sqlstore

import (
	"context"
	"encoding/json"
	"time"

	"github.com/sudosylabs/proctor/server/internal/canonicaljson"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

// Called only for newly interpreted, catalog-validated occurrence records under
// the Attempt fence. Existing receipt and retained-record quotas bound copies.
func retainNativeCondition(ctx context.Context, tx *sqlxTxWrapper, owner nativeDeliveryOwner, row nativeStoredRecord, occurrence model.NativeOccurrence, unresolved bool) error {
	var binding admittedSecurityBinding
	if json.Unmarshal(owner.Binding, &binding) != nil || binding.Security.Validate() != nil {
		return invalidPersistedState("native_condition", "value", model.ErrNativeDeliveryInvalid)
	}
	security := binding.Security
	attempt := security.Policy.Scope.AttemptID
	var retired bool
	if err := tx.Get(ctx, &retired, `SELECT EXISTS(SELECT 1 FROM exam_submissions WHERE exam_attempt_id=? AND integrity_retired_at IS NOT NULL)`, attempt.String()); err != nil {
		return err
	}
	if retired {
		return nil
	}
	var received time.Time
	if err := tx.Get(ctx, &received, `SELECT received_at FROM exam_native_delivery_batches WHERE participation_id=? AND batch_sequence=?`, owner.ParticipationID, row.Sequence); err != nil {
		return err
	}
	value := model.NativeConditionEvidence{ID: model.NewIntegrityEvidenceID(), AttemptID: attempt, ParticipationID: security.ParticipationID, Generation: security.Generation, StreamID: security.DeliveryStreamID, SecuritySessionID: security.SecuritySessionID, PolicyRevisionID: security.Policy.ExamRevisionID, PolicyDigest: security.Policy.Digest, ApplicationReleaseID: security.Policy.ApplicationReleaseID, MatrixID: security.Policy.MatrixID, BatchSequence: row.Sequence, RecordIndex: row.Index, Occurrence: occurrence, UnresolvedOpener: unresolved, ReceivedAt: received.UTC().Truncate(time.Millisecond), InterpretedAt: owner.Now.UTC().Truncate(time.Millisecond)}
	if err := validatePersistedModel("native_condition", value); err != nil {
		return err
	}
	raw, err := json.Marshal(value)
	if err == nil {
		raw, err = canonicaljson.Canonicalize(raw, model.NativeConditionEvidenceMaxBytes)
	}
	if err != nil {
		return err
	}
	result, err := tx.Exec(ctx, `INSERT INTO native_condition_evidence(id,exam_attempt_id,participation_id,batch_sequence,record_index,canonical) VALUES(?,?,?,?,?,?) ON CONFLICT(participation_id,batch_sequence,record_index) DO NOTHING`, value.ID.String(), attempt.String(), owner.ParticipationID, row.Sequence, row.Index, raw)
	if err != nil {
		return err
	}
	inserted, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if inserted == 0 {
		return nil
	}
	inventory, err := nativeConditionInventory(ctx, tx, attempt)
	if err != nil {
		return err
	}
	inventory, err = inventory.Append(raw)
	if err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE exam_attempt_delivery_budgets SET native_condition_records=?,native_condition_digest=? WHERE exam_attempt_id=?`, inventory.Records, inventory.Digest, attempt.String()); err != nil {
		return err
	}
	return invalidateDeliveryReviewInventory(ctx, tx, attempt)
}

func (s *SQLExamIntegrityReviewStore) ListNativeConditions(ctx context.Context, options store.NativeConditionListOptions) (*store.NativeConditionPage, error) {
	if !options.SubmissionID.IsValid() || options.Limit < 1 || options.Limit > 100 || options.AfterID != "" && !options.AfterID.IsValid() {
		return nil, store.NewErrInvalidInput("native_condition", "list", nil)
	}
	var rows [][]byte
	if err := s.GetMaster().Select(ctx, &rows, `SELECT e.canonical FROM native_condition_evidence e JOIN exam_submissions sub ON sub.exam_attempt_id=e.exam_attempt_id WHERE sub.id=? AND sub.sealed=true AND sub.integrity_retired_at IS NULL AND e.id>? ORDER BY e.id LIMIT ?`, options.SubmissionID.String(), options.AfterID.String(), options.Limit+1); err != nil {
		return nil, err
	}
	page := &store.NativeConditionPage{Items: []model.NativeConditionEvidence{}, HasMore: len(rows) > options.Limit}
	if page.HasMore {
		rows = rows[:options.Limit]
	}
	for _, raw := range rows {
		var value model.NativeConditionEvidence
		if json.Unmarshal(raw, &value) != nil || value.Validate() != nil {
			return nil, invalidPersistedState("native_condition", "value", model.ErrNativeDeliveryInvalid)
		}
		page.Items = append(page.Items, value)
	}
	return page, nil
}

func nativeConditionInventory(ctx context.Context, tx *sqlxTxWrapper, attempt model.ExamAttemptID) (model.NativeConditionInventory, error) {
	var row struct {
		Records int64  `db:"native_condition_records"`
		Digest  string `db:"native_condition_digest"`
	}
	if err := tx.Get(ctx, &row, `SELECT native_condition_records,native_condition_digest FROM exam_attempt_delivery_budgets WHERE exam_attempt_id=?`, attempt.String()); err != nil {
		return model.NativeConditionInventory{}, err
	}
	value := model.NativeConditionInventory{Records: row.Records, Digest: row.Digest}
	return value, validatePersistedModel("native_condition", value)
}
