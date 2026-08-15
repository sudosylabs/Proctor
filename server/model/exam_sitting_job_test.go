// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package model

import (
	"encoding/json"
	"testing"
)

func TestExamSittingLifecycleCommandCodecIsStrict(t *testing.T) {
	t.Parallel()
	sittingID := NewExamSittingID()
	document, err := EncodeExamSittingLifecycleCommand(ExamSittingLifecycleCommandV1{ExamSittingID: sittingID})
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeExamSittingLifecycleCommand(1, document)
	if err != nil || decoded.ExamSittingID != sittingID {
		t.Fatalf("Decode() = %#v, %v", decoded, err)
	}
	for name, malformed := range map[string]json.RawMessage{
		"unknown":    json.RawMessage(`{"exam_sitting_id":"` + sittingID.String() + `","extra":true}`),
		"duplicate":  json.RawMessage(`{"exam_sitting_id":"` + sittingID.String() + `","exam_sitting_id":"` + sittingID.String() + `"}`),
		"trailing":   append(append(json.RawMessage(nil), document...), []byte(` {}`)...),
		"invalid id": json.RawMessage(`{"exam_sitting_id":"bad"}`),
	} {
		if _, err = DecodeExamSittingLifecycleCommand(1, malformed); err == nil {
			t.Fatalf("%s command was accepted", name)
		}
	}
	if _, err = DecodeExamSittingLifecycleCommand(2, document); err == nil {
		t.Fatal("future command version was accepted")
	}
}

func TestExamSittingLifecycleDedupeKeyIsRevisionFenced(t *testing.T) {
	t.Parallel()
	id := NewExamSittingID()
	key, err := ExamSittingLifecycleDedupeKey(id, ExamSittingLifecycleJobDeadline, 7)
	if err != nil {
		t.Fatal(err)
	}
	if want := "exam-sitting:" + id.String() + ":deadline:7"; key != want {
		t.Fatalf("key = %q, want %q", key, want)
	}
	if _, err = ExamSittingLifecycleDedupeKey(id, "unknown", 7); err == nil {
		t.Fatal("unknown lifecycle phase was accepted")
	}
}
