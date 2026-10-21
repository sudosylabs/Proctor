// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package model

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestExamSittingLifecycleCommandCodecIsStrict(t *testing.T) {
	t.Parallel()
	sittingID := NewExamSittingID()
	document, err := EncodeExamSittingLifecycleCommand(ExamSittingLifecycleCommandV1{ExamSittingID: sittingID})
	require.NoError(t, err)
	decoded, err := DecodeExamSittingLifecycleCommand(1, document)
	require.NoError(t, err, "Decode() = %#v", decoded)
	require.Equal(t, sittingID, decoded.ExamSittingID, "Decode() = %#v", decoded)
	for name, malformed := range map[string]json.RawMessage{
		"unknown":    json.RawMessage(`{"exam_sitting_id":"` + sittingID.String() + `","extra":true}`),
		"duplicate":  json.RawMessage(`{"exam_sitting_id":"` + sittingID.String() + `","exam_sitting_id":"` + sittingID.String() + `"}`),
		"trailing":   append(append(json.RawMessage(nil), document...), []byte(` {}`)...),
		"invalid id": json.RawMessage(`{"exam_sitting_id":"bad"}`),
	} {
		_, err = DecodeExamSittingLifecycleCommand(1, malformed)
		require.Error(t, err, "%s command was accepted", name)
	}
	_, err = DecodeExamSittingLifecycleCommand(2, document)
	require.Error(t, err, "future command version was accepted")
}

func TestExamSittingLifecycleDedupeKeyIsRevisionFenced(t *testing.T) {
	t.Parallel()
	id := NewExamSittingID()
	key, err := ExamSittingLifecycleDedupeKey(id, ExamSittingLifecycleJobDeadline, 7)
	require.NoError(t, err)
	require.Equal(t, ("exam-sitting:" + id.String() + ":deadline:7"), key, "key")
	_, err = ExamSittingLifecycleDedupeKey(id, "unknown", 7)
	require.Error(t, err, "unknown lifecycle phase was accepted")
}
