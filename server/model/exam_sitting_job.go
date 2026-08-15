// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package model

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
)

type ExamSittingLifecycleJobPhase string

const (
	ExamSittingLifecycleJobOpen     ExamSittingLifecycleJobPhase = "open"
	ExamSittingLifecycleJobDeadline ExamSittingLifecycleJobPhase = "deadline"
	ExamSittingLifecycleJobFinalize ExamSittingLifecycleJobPhase = "finalize"
)

func (phase ExamSittingLifecycleJobPhase) IsValid() bool {
	return phase == ExamSittingLifecycleJobOpen || phase == ExamSittingLifecycleJobDeadline || phase == ExamSittingLifecycleJobFinalize
}

// ExamSittingLifecycleDedupeKey identifies one revision-fenced lifecycle
// occurrence. Rescheduling leaves old active work harmlessly stale while a new
// resulting Sitting revision receives distinct authoritative work.
func ExamSittingLifecycleDedupeKey(id ExamSittingID, phase ExamSittingLifecycleJobPhase, revision int64) (string, error) {
	if !id.IsValid() || !phase.IsValid() || revision < 1 {
		return "", errors.New("invalid Exam Sitting lifecycle dedupe identity")
	}
	return "exam-sitting:" + id.String() + ":" + string(phase) + ":" + strconv.FormatInt(revision, 10), nil
}

// ExamSittingLifecycleCommandV1 identifies the authoritative Sitting that a
// delayed lifecycle Job must reread. Mutable state, revision, and schedule are
// deliberately absent so stale work converges through PostgreSQL state.
type ExamSittingLifecycleCommandV1 struct {
	ExamSittingID ExamSittingID `json:"exam_sitting_id"`
}

func EncodeExamSittingLifecycleCommand(command ExamSittingLifecycleCommandV1) (json.RawMessage, error) {
	if !command.ExamSittingID.IsValid() {
		return nil, errors.New("Exam Sitting lifecycle command has invalid Sitting ID")
	}
	return json.Marshal(command)
}

func DecodeExamSittingLifecycleCommand(version int, document json.RawMessage) (ExamSittingLifecycleCommandV1, error) {
	var command ExamSittingLifecycleCommandV1
	if version != 1 {
		return command, fmt.Errorf("unsupported Exam Sitting lifecycle command version %d", version)
	}
	if err := rejectDuplicateExamSittingLifecycleMembers(document); err != nil {
		return command, err
	}
	decoder := json.NewDecoder(bytes.NewReader(document))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&command); err != nil {
		return command, fmt.Errorf("decode Exam Sitting lifecycle command: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return command, errors.New("Exam Sitting lifecycle command contains trailing JSON")
		}
		return command, fmt.Errorf("decode trailing Exam Sitting lifecycle command: %w", err)
	}
	if !command.ExamSittingID.IsValid() {
		return command, errors.New("Exam Sitting lifecycle command has invalid Sitting ID")
	}
	return command, nil
}

func rejectDuplicateExamSittingLifecycleMembers(document json.RawMessage) error {
	decoder := json.NewDecoder(bytes.NewReader(document))
	token, err := decoder.Token()
	if err != nil || token != json.Delim('{') {
		return errors.New("Exam Sitting lifecycle command must be an object")
	}
	seen := make(map[string]struct{})
	for decoder.More() {
		member, tokenErr := decoder.Token()
		if tokenErr != nil {
			return tokenErr
		}
		name, ok := member.(string)
		if !ok {
			return errors.New("Exam Sitting lifecycle command member is invalid")
		}
		if _, duplicate := seen[name]; duplicate {
			return errors.New("Exam Sitting lifecycle command contains a duplicate member")
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
