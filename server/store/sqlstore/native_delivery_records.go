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
	"slices"

	"github.com/sudosylabs/proctor/server/model"
)

// Row metadata is canonical counted application data, including the stable
// occurrence index and source-watermark projection. SQL/index overhead is not
// substituted for it. Projection changes use already charged bounded slots.
type nativeRecordMetadata struct {
	BatchSequence    int64                     `json:"batch_sequence"`
	RecordIndex      int                       `json:"record_index"`
	OccurrenceID     *string                   `json:"occurrence_id"`
	SourceRanges     []model.NativeSourceRange `json:"source_ranges"`
	UnresolvedOpener bool                      `json:"unresolved_opener"`
}
type nativeStoredRecord struct {
	Sequence int64  `db:"batch_sequence"`
	Index    int    `db:"record_index"`
	Kind     string `db:"kind"`
	Raw      []byte `db:"record_canonical"`
	Metadata []byte `db:"metadata_canonical"`
}

func encodeNativeRecord(record model.NativeRecord, sequence int64, index int) (nativeStoredRecord, error) {
	row := nativeStoredRecord{Sequence: sequence, Index: index}
	meta := nativeRecordMetadata{BatchSequence: sequence, RecordIndex: index, SourceRanges: []model.NativeSourceRange{}}
	switch {
	case record.Occurrence != nil:
		row.Kind = "occurrence"
		meta.OccurrenceID = &record.Occurrence.OccurrenceID
		meta.SourceRanges = record.Occurrence.SourceRanges
	case record.Coverage != nil:
		row.Kind = "coverage_transition"
		s := record.Coverage.Source
		meta.SourceRanges = []model.NativeSourceRange{{SourceID: s.SourceID, SourceInstanceID: s.SourceInstanceID, FirstSequence: s.Sequence, LastSequence: s.Sequence}}
	case record.Reset != nil:
		row.Kind = "source_reset"
	case record.Gap != nil:
		row.Kind = "source_gap"
		g := record.Gap
		meta.SourceRanges = []model.NativeSourceRange{{SourceID: g.SourceID, SourceInstanceID: g.SourceInstanceID, FirstSequence: g.FirstMissingSequence, LastSequence: g.LastMissingSequence}}
	default:
		return row, model.ErrNativeDeliveryInvalid
	}
	var err error
	row.Raw, err = canonicalPreflightValue(record)
	if err != nil {
		return row, err
	}
	row.Metadata, err = canonicalPreflightValue(meta)
	if err != nil {
		return row, err
	}
	// Reserve the larger spelling of the boolean within counted bytes so an
	// unresolved projection never grows the stored representation.
	if len(row.Raw) > 8192 || len(row.Metadata) > 2048 {
		return row, model.ErrNativeDeliveryInvalid
	}
	return row, nil
}

func validateNativeBatchSources(ctx context.Context, tx *sqlxTxWrapper, owner nativeDeliveryOwner, resolved model.ResolvedNativePolicy, agreement *model.DesktopNativeAgreement, records []model.NativeRecord) error {
	var raw []byte
	if err := tx.Get(ctx, &raw, `SELECT latest_report_canonical FROM exam_attempt_security_owners WHERE participation_id=?`, owner.ParticipationID); err != nil {
		return err
	}
	var snapshot struct {
		Sources []model.NativeSourceCoverage `json:"sources"`
	}
	if json.Unmarshal(raw, &snapshot) != nil {
		return model.ErrNativeDeliveryInvalid
	}
	var historyRaw [][]byte
	if err := tx.Select(ctx, &historyRaw, `SELECT reset_canonical FROM exam_native_source_resets WHERE participation_id=?`, owner.ParticipationID); err != nil {
		return err
	}
	history := make([]model.NativeSourceReset, 0, len(historyRaw))
	pendingResets := []nativeResetRecord{}
	lifetimes := []model.NativeSourceLifetime{}
	for _, head := range snapshot.Sources {
		lifetimes = append(lifetimes, model.NativeSourceLifetime{SourceID: head.SourceID, InstanceID: head.SourceInstanceID})
	}
	// First collect all instances, then retire predecessors. SQL ordering is not
	// an event ordering and must not resurrect an intermediate lifetime.
	for _, raw := range historyRaw {
		var record nativeResetRecord
		if json.Unmarshal(raw, &record) != nil || record.Reset.Validate() != nil || record.ParticipationID.String() != owner.ParticipationID {
			return model.ErrNativeDeliveryInvalid
		}
		canonical, err := record.Reset.Canonical()
		if err != nil || model.SHA256Fingerprint(canonical) != record.Digest {
			return model.ErrNativeDeliveryInvalid
		}
		history = append(history, record.Reset)
		for _, instance := range []string{record.Reset.PreviousSourceInstanceID, record.Reset.NewSourceInstanceID} {
			if !slices.ContainsFunc(lifetimes, func(l model.NativeSourceLifetime) bool {
				return l.SourceID == record.Reset.SourceID && l.InstanceID == instance
			}) {
				lifetimes = append(lifetimes, model.NativeSourceLifetime{SourceID: record.Reset.SourceID, InstanceID: instance})
			}
		}
	}
	for _, edge := range history {
		for i := range lifetimes {
			if lifetimes[i].SourceID == edge.SourceID && lifetimes[i].InstanceID == edge.PreviousSourceInstanceID {
				final := edge.PreviousFinalSequence
				lifetimes[i].FinalSequence = &final
			}
		}
	}
	for _, record := range records {
		if err := model.ValidateNativeRecordBinding(record, resolved, agreement, lifetimes); err != nil {
			return err
		}
		if record.Reset == nil {
			continue
		}
		edge := *record.Reset
		canonical, err := edge.Canonical()
		if err != nil {
			return err
		}
		digest := model.SHA256Fingerprint(canonical)
		known := false
		for _, prior := range history {
			if prior.ResetID == edge.ResetID {
				raw, err := prior.Canonical()
				if err != nil {
					return err
				}
				if model.SHA256Fingerprint(raw) != digest {
					return nativeAppendConflict("batch_conflict")
				}
				known = true
				break
			}
		}
		if known {
			continue
		}
		// Live edges must have been accepted by the control owner. Historical edges
		// establish evidence continuity only; they never update live heads or gates.
		if owner.Closure.ClosedAt == nil {
			return nativeAppendConflict("batch_conflict")
		}
		previous := -1
		restarts := 0
		for i, l := range lifetimes {
			if l.SourceID == edge.SourceID && l.InstanceID == edge.NewSourceInstanceID {
				return nativeAppendConflict("batch_conflict")
			}
			if l.SourceID == edge.SourceID && l.InstanceID == edge.PreviousSourceInstanceID && l.FinalSequence == nil {
				previous = i
			}
		}
		for _, prior := range history {
			if prior.SourceID == edge.SourceID && prior.Reason == "restart" {
				restarts++
			}
		}
		if previous < 0 || edge.Reason == "restart" && restarts >= 1 {
			return nativeAppendConflict("batch_conflict")
		}
		watermark := int64(0)
		for _, head := range snapshot.Sources {
			if head.SourceID == edge.SourceID && head.SourceInstanceID == edge.PreviousSourceInstanceID {
				watermark = head.Sequence
			}
		}
		var retained int64
		if err := tx.Get(ctx, &retained, `SELECT COALESCE(MAX((r->>'last_sequence')::bigint),0) FROM exam_native_delivery_records d CROSS JOIN LATERAL jsonb_array_elements(convert_from(d.metadata_canonical,'UTF8')::jsonb->'source_ranges') r WHERE d.participation_id=? AND r->>'source_id'=? AND r->>'source_instance_id'=?`, owner.ParticipationID, string(edge.SourceID), edge.PreviousSourceInstanceID); err != nil {
			return err
		}
		if edge.PreviousFinalSequence < max(watermark, retained) {
			return nativeAppendConflict("batch_conflict")
		}
		for _, other := range records {
			if other.Reset == nil {
				row, err := encodeNativeRecord(other, 1, 0)
				if err != nil {
					return err
				}
				var m nativeRecordMetadata
				if json.Unmarshal(row.Metadata, &m) != nil {
					return model.ErrNativeDeliveryInvalid
				}
				for _, r := range m.SourceRanges {
					if r.SourceID == edge.SourceID && r.SourceInstanceID == edge.PreviousSourceInstanceID && r.LastSequence > edge.PreviousFinalSequence {
						return nativeAppendConflict("batch_conflict")
					}
				}
			}
		}
		pendingResets = append(pendingResets, nativeResetRecord{ParticipationID: model.AttemptParticipationID(owner.ParticipationID), Reset: edge, Digest: digest})
		final := edge.PreviousFinalSequence
		lifetimes[previous].FinalSequence = &final
		lifetimes = append(lifetimes, model.NativeSourceLifetime{SourceID: edge.SourceID, InstanceID: edge.NewSourceInstanceID})
		history = append(history, edge)
	}
	if len(pendingResets) > 0 {
		var used int64
		if err := tx.Get(ctx, &used, `SELECT b.control_metadata_bytes FROM exam_attempt_delivery_budgets b JOIN exam_attempt_security_owners o ON o.exam_attempt_id=b.exam_attempt_id WHERE o.participation_id=?`, owner.ParticipationID); err != nil {
			return err
		}
		encoded := make([][]byte, len(pendingResets))
		required := int64(0)
		for i, record := range pendingResets {
			raw, err := canonicalPreflightValue(record)
			if err != nil {
				return err
			}
			encoded[i] = raw
			required += int64(len(raw))
		}
		next, err := model.ReserveDeliveryMetadata(used, required)
		if err != nil {
			return err
		}
		for i, raw := range encoded {
			if _, err := tx.Exec(ctx, `INSERT INTO exam_native_source_resets(participation_id,reset_id,reset_canonical) VALUES(?,?,?)`, owner.ParticipationID, pendingResets[i].Reset.ResetID, raw); err != nil {
				return err
			}
		}
		if _, err := tx.Exec(ctx, `UPDATE exam_attempt_delivery_budgets SET control_metadata_bytes=? WHERE exam_attempt_id=(SELECT exam_attempt_id FROM exam_attempt_security_owners WHERE participation_id=?)`, next, owner.ParticipationID); err != nil {
			return err
		}
	}

	return nil
}

// Check all stored neighbors of each affected occurrence, including pending
// records. A missing earlier position can explain a missing opener, but cannot
// explain contradictory counts, an identity change, or recovery after recovery.
func validateNativeOccurrenceNeighbors(ctx context.Context, tx *sqlxTxWrapper, owner nativeDeliveryOwner, added []nativeStoredRecord) error {

	received, _, err := nativeDeliveryRanges(ctx, tx, owner)
	if err != nil {
		return err
	}
	contiguous := int64(0)
	for _, interval := range received {
		if interval.First > contiguous+1 {
			break
		}
		contiguous = interval.Last
	}
	for _, row := range added {
		if row.Kind != "occurrence" {
			continue
		}
		var record model.NativeRecord
		if json.Unmarshal(row.Raw, &record) != nil || record.Occurrence == nil {
			return model.ErrNativeDeliveryInvalid
		}
		id := record.Occurrence.OccurrenceID
		var previous, next []byte
		err := tx.Get(ctx, &previous, `SELECT record_canonical FROM exam_native_delivery_records WHERE participation_id=? AND kind='occurrence' AND convert_from(metadata_canonical,'UTF8')::jsonb->>'occurrence_id'=? AND (batch_sequence,record_index)<(?,?) ORDER BY batch_sequence DESC,record_index DESC LIMIT 1`, owner.ParticipationID, id, row.Sequence, row.Index)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		var prior *model.NativeOccurrenceProgress
		if len(previous) > 0 {
			var r model.NativeRecord
			if json.Unmarshal(previous, &r) != nil || r.Occurrence == nil {
				return model.ErrNativeDeliveryInvalid
			}
			prior = &model.NativeOccurrenceProgress{Latest: *r.Occurrence}
		}
		current, err := model.AdvanceNativeOccurrence(prior, *record.Occurrence, contiguous < row.Sequence-1)
		if err != nil {
			return err
		}
		err = tx.Get(ctx, &next, `SELECT record_canonical FROM exam_native_delivery_records WHERE participation_id=? AND kind='occurrence' AND convert_from(metadata_canonical,'UTF8')::jsonb->>'occurrence_id'=? AND (batch_sequence,record_index)>(?,?) ORDER BY batch_sequence,record_index LIMIT 1`, owner.ParticipationID, id, row.Sequence, row.Index)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if len(next) > 0 {
			var r model.NativeRecord
			if json.Unmarshal(next, &r) != nil || r.Occurrence == nil {
				return model.ErrNativeDeliveryInvalid
			}
			if _, err := model.AdvanceNativeOccurrence(&current, *r.Occurrence, false); err != nil {
				return err
			}
		}
	}

	return nil
}

// Interpret only records in the settled prefix. Terminal holes preserve an
// unresolved opener and never turn an operational record into a condition Flag.
func advanceNativeInterpretation(ctx context.Context, tx *sqlxTxWrapper, owner nativeDeliveryOwner) error {
	received, gaps, err := nativeDeliveryRanges(ctx, tx, owner)
	if err != nil {
		return err
	}
	progress, err := model.ResolveDeliveryProgress(owner.Allocated, received, gaps, owner.TerminalThrough)
	if err != nil {
		return err
	}
	var rows []nativeStoredRecord
	if err := tx.Select(ctx, &rows, `SELECT r.batch_sequence,r.record_index,r.kind,r.record_canonical,r.metadata_canonical FROM exam_native_delivery_records r JOIN exam_native_delivery_batches b USING(participation_id,batch_sequence) WHERE r.participation_id=? AND NOT b.processed AND r.batch_sequence<=? ORDER BY r.batch_sequence,r.record_index`, owner.ParticipationID, progress.SettledThrough); err != nil {
		return err
	}
	for _, row := range rows {
		if row.Kind != "occurrence" {
			continue
		}
		var record model.NativeRecord
		if json.Unmarshal(row.Raw, &record) != nil || record.Occurrence == nil {
			return model.ErrNativeDeliveryInvalid
		}
		var previous struct {
			Raw        []byte `db:"record_canonical"`
			Unresolved bool   `db:"unresolved_opener"`
		}
		var prior *model.NativeOccurrenceProgress
		err := tx.Get(ctx, &previous, `SELECT r.record_canonical,o.unresolved_opener FROM exam_native_occurrences o JOIN exam_native_delivery_records r USING(participation_id,batch_sequence,record_index) WHERE o.participation_id=? AND o.occurrence_id=?`, owner.ParticipationID, record.Occurrence.OccurrenceID)
		if err == nil {
			var r model.NativeRecord
			if json.Unmarshal(previous.Raw, &r) != nil || r.Occurrence == nil {
				return model.ErrNativeDeliveryInvalid
			}
			prior = &model.NativeOccurrenceProgress{Latest: *r.Occurrence, UnresolvedOpener: previous.Unresolved}
		} else if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		missingBefore := owner.TerminalThrough > 0 && row.Sequence > 1 || slices.ContainsFunc(gaps, func(r model.SequenceRange) bool { return r.First < row.Sequence })
		value, err := model.AdvanceNativeOccurrence(prior, *record.Occurrence, missingBefore)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO exam_native_occurrences(participation_id,occurrence_id,batch_sequence,record_index,unresolved_opener) VALUES(?,?,?,?,?) ON CONFLICT(participation_id,occurrence_id) DO UPDATE SET batch_sequence=EXCLUDED.batch_sequence,record_index=EXCLUDED.record_index,unresolved_opener=EXCLUDED.unresolved_opener`, owner.ParticipationID, record.Occurrence.OccurrenceID, row.Sequence, row.Index, value.UnresolvedOpener); err != nil {
			return err
		}
		if err := retainNativeCondition(ctx, tx, owner, row, *record.Occurrence, value.UnresolvedOpener); err != nil {
			return err
		}
		if value.UnresolvedOpener {
			var meta nativeRecordMetadata
			if json.Unmarshal(row.Metadata, &meta) != nil {
				return model.ErrNativeDeliveryInvalid
			}
			meta.UnresolvedOpener = true
			raw, err := canonicalPreflightValue(meta)
			if err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `UPDATE exam_native_delivery_records SET metadata_canonical=? WHERE participation_id=? AND batch_sequence=? AND record_index=?`, raw, owner.ParticipationID, row.Sequence, row.Index); err != nil {
				return err
			}
		}
	}
	var released int64
	if err := tx.Get(ctx, &released, `SELECT COALESCE(SUM(counted_bytes),0) FROM exam_native_delivery_batches WHERE participation_id=? AND NOT processed AND batch_sequence<=?`, owner.ParticipationID, progress.SettledThrough); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE exam_native_delivery_batches SET processed=true WHERE participation_id=? AND batch_sequence<=? AND NOT processed`, owner.ParticipationID, progress.SettledThrough); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE exam_attempt_delivery_budgets SET native_pending_bytes=native_pending_bytes-? WHERE exam_attempt_id=(SELECT exam_attempt_id FROM exam_attempt_security_owners WHERE participation_id=?)`, released, owner.ParticipationID); err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `UPDATE exam_attempt_security_owners SET acknowledged_through_sequence=?,interpreted_through_sequence=? WHERE participation_id=?`, progress.HighestContiguous, progress.SettledThrough, owner.ParticipationID)
	return err
}
