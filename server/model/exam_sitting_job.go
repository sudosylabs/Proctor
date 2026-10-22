// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package model

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"strconv"
)

type ExamSittingLifecycleJobPhase string

const (
	ExamSittingLifecycleJobOpen     ExamSittingLifecycleJobPhase = "open"
	ExamSittingLifecycleJobDeadline ExamSittingLifecycleJobPhase = "deadline"
	ExamSittingLifecycleJobFinalize ExamSittingLifecycleJobPhase = "finalize"
)

func (p ExamSittingLifecycleJobPhase) IsValid() bool {
	return p == ExamSittingLifecycleJobOpen || p == ExamSittingLifecycleJobDeadline || p == ExamSittingLifecycleJobFinalize
}

// ExamSittingLifecycleDedupeKey identifies one revision-fenced lifecycle
// occurrence. Rescheduling leaves old active work harmlessly stale while a new
// resulting Sitting revision receives distinct authoritative work.
func ExamSittingLifecycleDedupeKey(id ExamSittingID, phase ExamSittingLifecycleJobPhase, revision int64) (string, error) {
	if !id.IsValid() || !phase.IsValid() || revision < 1 {
		return "", invalidModelError("ExamSittingLifecycleDedupeKey", "exam_sitting_job", "", "invalid Exam Sitting lifecycle dedupe identity", "")
	}
	return "exam-sitting:" + id.String() + ":" + string(phase) + ":" + strconv.FormatInt(revision, 10), nil
}

// ExamSittingLifecycleCommand identifies the authoritative Sitting that a
// delayed lifecycle Job must reread. Mutable state, revision, and schedule are
// deliberately absent so stale work converges through PostgreSQL state.
type ExamSittingLifecycleCommand struct {
	ExamSittingID ExamSittingID `json:"exam_sitting_id"`
}

func EncodeExamSittingLifecycleCommand(command ExamSittingLifecycleCommand) (json.RawMessage, error) {
	if !command.ExamSittingID.IsValid() {
		return nil, invalidModelError("EncodeExamSittingLifecycleCommand", "exam_sitting_job", "", "Exam Sitting lifecycle command has invalid Sitting ID", "")
	}
	return json.Marshal(command)
}

func DecodeExamSittingLifecycleCommand(document json.RawMessage) (ExamSittingLifecycleCommand, error) {
	var command ExamSittingLifecycleCommand
	if err := rejectDuplicateExamSittingLifecycleMembers(document); err != nil {
		return command, err
	}
	decoder := json.NewDecoder(bytes.NewReader(document))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&command); err != nil {
		return command, invalidModelError("DecodeExamSittingLifecycleCommand", "exam_sitting_job", "", "decode Exam Sitting lifecycle command", "").wrap(err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return command, invalidModelError("DecodeExamSittingLifecycleCommand", "exam_sitting_job", "", "Exam Sitting lifecycle command contains trailing JSON", "")
		}
		return command, invalidModelError("DecodeExamSittingLifecycleCommand", "exam_sitting_job", "", "decode trailing Exam Sitting lifecycle command", "").wrap(err)
	}
	if !command.ExamSittingID.IsValid() {
		return command, invalidModelError("DecodeExamSittingLifecycleCommand", "exam_sitting_job", "", "Exam Sitting lifecycle command has invalid Sitting ID", "")
	}
	return command, nil
}

func rejectDuplicateExamSittingLifecycleMembers(document json.RawMessage) error {
	decoder := json.NewDecoder(bytes.NewReader(document))
	token, err := decoder.Token()
	if err != nil || token != json.Delim('{') {
		return invalidModelError("rejectDuplicateExamSittingLifecycleMembers", "exam_sitting_job", "", "Exam Sitting lifecycle command must be an object", "")
	}
	seen := make(map[string]struct{})
	for decoder.More() {
		member, tokenErr := decoder.Token()
		if tokenErr != nil {
			return tokenErr
		}
		name, ok := member.(string)
		if !ok {
			return invalidModelError("rejectDuplicateExamSittingLifecycleMembers", "exam_sitting_job", "", "Exam Sitting lifecycle command member is invalid", "")
		}
		if _, duplicate := seen[name]; duplicate {
			return invalidModelError("rejectDuplicateExamSittingLifecycleMembers", "exam_sitting_job", "", "Exam Sitting lifecycle command contains a duplicate member", "")
		}
		seen[name] = struct{}{}
		var value json.RawMessage
		if err = decoder.Decode(&value); err != nil {
			return err
		}
	}
	_, err = decoder.Token()
	return err
}
