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
	"fmt"
	"io"
	"time"

	"github.com/sudosylabs/proctor/server/internal/canonicaljson"
)

const (
	ExamPolicySchemaVersion = 1
	ExamPolicySetMaxBytes   = 64 * 1024
)

type IntegrityThresholdOutcome string

const (
	IntegrityOutcomeFlag           IntegrityThresholdOutcome = "flag"
	IntegrityOutcomeFlagAndWarn    IntegrityThresholdOutcome = "flag_and_warn"
	IntegrityOutcomeFlagAndSuspend IntegrityThresholdOutcome = "flag_and_suspend"
)

type ConnectionLossPolicy struct {
	Outcome IntegrityThresholdOutcome
}

type FocusLossPolicy struct {
	Enabled         bool
	MinimumDuration time.Duration
	IncidentCount   int
	Window          time.Duration
	Outcome         IntegrityThresholdOutcome
}

// ExamPolicySet is the complete typed policy copied into each new Exam Draft.
// Its Go name is unversioned; SchemaVersion selects the explicit persisted codec.
type ExamPolicySet struct {
	SchemaVersion  int
	ConnectionLoss ConnectionLossPolicy
	FocusLoss      FocusLossPolicy
	Native         NativeSecurityPolicy
}

func DefaultExamPolicySet() ExamPolicySet {
	return ExamPolicySet{
		SchemaVersion:  ExamPolicySchemaVersion,
		Native:         DefaultNativeSecurityPolicy(),
		ConnectionLoss: ConnectionLossPolicy{Outcome: IntegrityOutcomeFlagAndSuspend},
		FocusLoss: FocusLossPolicy{
			Enabled: true, MinimumDuration: 2 * time.Second, IncidentCount: 3,
			Window: 5 * time.Minute, Outcome: IntegrityOutcomeFlagAndWarn,
		},
	}
}

func (p ExamPolicySet) Validate() error {
	if p.Native.Validate() != nil {
		return errNativePolicy
	}
	if p.FocusLoss.MinimumDuration%time.Millisecond != 0 || p.FocusLoss.Window%time.Millisecond != 0 {
		return errors.New("model: focus loss durations require whole milliseconds")
	}
	if p.SchemaVersion != ExamPolicySchemaVersion {
		return fmt.Errorf("model: unsupported exam policy schema version %d", p.SchemaVersion)
	}
	if p.ConnectionLoss.Outcome != IntegrityOutcomeFlagAndSuspend {
		return errors.New("model: connection loss must flag and suspend")
	}
	if p.FocusLoss.MinimumDuration < 500*time.Millisecond || p.FocusLoss.MinimumDuration > 5*time.Minute {
		return errors.New("model: focus loss minimum duration is out of bounds")
	}
	if p.FocusLoss.IncidentCount < 1 || p.FocusLoss.IncidentCount > 100 {
		return errors.New("model: focus loss incident count is out of bounds")
	}
	if p.FocusLoss.Window < 10*time.Second || p.FocusLoss.Window > 4*time.Hour || p.FocusLoss.Window < p.FocusLoss.MinimumDuration {
		return errors.New("model: focus loss window is out of bounds")
	}
	switch p.FocusLoss.Outcome {
	case IntegrityOutcomeFlag, IntegrityOutcomeFlagAndWarn, IntegrityOutcomeFlagAndSuspend:
		return nil
	default:
		return errors.New("model: focus loss outcome is invalid")
	}
}

type examPolicyWire struct {
	Native         NativeSecurityPolicy     `json:"native"`
	ConnectionLoss examConnectionPolicyWire `json:"connection_loss"`
	FocusLoss      examFocusPolicyWire      `json:"focus_loss"`
}

type examConnectionPolicyWire struct {
	Outcome IntegrityThresholdOutcome `json:"outcome"`
}

type examFocusPolicyWire struct {
	Enabled                     bool                      `json:"enabled"`
	MinimumDurationMilliseconds int64                     `json:"minimum_duration_milliseconds"`
	IncidentCount               int                       `json:"incident_count"`
	WindowMilliseconds          int64                     `json:"window_milliseconds"`
	Outcome                     IntegrityThresholdOutcome `json:"outcome"`
}

func EncodeExamPolicySet(policy ExamPolicySet) ([]byte, error) {
	if err := policy.Validate(); err != nil {
		return nil, err
	}
	data, err := encodeCanonicalExamDocument(examPolicyWire{
		Native:         policy.Native.Clone(),
		ConnectionLoss: examConnectionPolicyWire{Outcome: policy.ConnectionLoss.Outcome},
		FocusLoss: examFocusPolicyWire{
			Enabled:                     policy.FocusLoss.Enabled,
			MinimumDurationMilliseconds: policy.FocusLoss.MinimumDuration.Milliseconds(),
			IncidentCount:               policy.FocusLoss.IncidentCount,
			WindowMilliseconds:          policy.FocusLoss.Window.Milliseconds(),
			Outcome:                     policy.FocusLoss.Outcome,
		},
	})
	if err != nil {
		return nil, err
	}
	if len(data) > ExamPolicySetMaxBytes {
		return nil, errors.New("model: exam policy document is too large")
	}
	return data, nil
}

func DecodeExamPolicySet(data []byte) (ExamPolicySet, error) {
	if len(data) == 0 || len(data) > ExamPolicySetMaxBytes {
		return ExamPolicySet{}, errors.New("model: exam policy document size is invalid")
	}
	if err := validateExamDocumentJSON(data); err != nil {
		return ExamPolicySet{}, err
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var wire struct {
		Native         *NativeSecurityPolicy `json:"native"`
		ConnectionLoss *struct {
			Outcome *IntegrityThresholdOutcome `json:"outcome"`
		} `json:"connection_loss"`
		FocusLoss *struct {
			Enabled                     *bool                      `json:"enabled"`
			MinimumDurationMilliseconds *int64                     `json:"minimum_duration_milliseconds"`
			IncidentCount               *int                       `json:"incident_count"`
			WindowMilliseconds          *int64                     `json:"window_milliseconds"`
			Outcome                     *IntegrityThresholdOutcome `json:"outcome"`
		} `json:"focus_loss"`
	}
	if err := decoder.Decode(&wire); err != nil {
		return ExamPolicySet{}, fmt.Errorf("model: decode exam policy: %w", err)
	}
	if err := requireJSONEOF(decoder); err != nil {
		return ExamPolicySet{}, err
	}
	if wire.Native == nil || wire.ConnectionLoss == nil || wire.ConnectionLoss.Outcome == nil ||
		wire.FocusLoss == nil || wire.FocusLoss.Enabled == nil || wire.FocusLoss.MinimumDurationMilliseconds == nil ||
		wire.FocusLoss.IncidentCount == nil || wire.FocusLoss.WindowMilliseconds == nil || wire.FocusLoss.Outcome == nil {
		return ExamPolicySet{}, errors.New("model: exam policy document is incomplete")
	}
	if *wire.FocusLoss.MinimumDurationMilliseconds < 500 || *wire.FocusLoss.MinimumDurationMilliseconds > 300_000 ||
		*wire.FocusLoss.WindowMilliseconds < 10_000 || *wire.FocusLoss.WindowMilliseconds > 14_400_000 {
		return ExamPolicySet{}, errors.New("model: exam policy duration is out of bounds")
	}
	policy := ExamPolicySet{
		SchemaVersion:  ExamPolicySchemaVersion,
		Native:         wire.Native.Clone(),
		ConnectionLoss: ConnectionLossPolicy{Outcome: *wire.ConnectionLoss.Outcome},
		FocusLoss: FocusLossPolicy{
			Enabled:         *wire.FocusLoss.Enabled,
			MinimumDuration: time.Duration(*wire.FocusLoss.MinimumDurationMilliseconds) * time.Millisecond,
			IncidentCount:   *wire.FocusLoss.IncidentCount,
			Window:          time.Duration(*wire.FocusLoss.WindowMilliseconds) * time.Millisecond,
			Outcome:         *wire.FocusLoss.Outcome,
		},
	}
	if err := policy.Validate(); err != nil {
		return ExamPolicySet{}, err
	}
	return policy, nil
}

func requireJSONEOF(decoder *json.Decoder) error {
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("model: exam policy has trailing JSON")
		}
		return fmt.Errorf("model: decode exam policy trailing input: %w", err)
	}
	return nil
}

// validateExamDocumentJSON preserves strict wire validation before typed decoding.
func validateExamDocumentJSON(data []byte) error {
	_, err := canonicaljson.Canonicalize(data, len(data))
	return err
}

// encodeCanonicalExamDocument receives already validated, schema-owned values.
// Its callers select digest fields and enforce the canonical document size.
func encodeCanonicalExamDocument(value any) ([]byte, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	return canonicaljson.Canonicalize(encoded, len(encoded))
}

// Clone isolates the authored family collection; family values are immutable.
func (p ExamPolicySet) Clone() ExamPolicySet { p.Native = p.Native.Clone(); return p }

// Equal compares complete validated semantics, including native selections.
func (p ExamPolicySet) Equal(other ExamPolicySet) bool {
	left, err := EncodeExamPolicySet(p)
	if err != nil {
		return false
	}
	right, err := EncodeExamPolicySet(other)
	return err == nil && bytes.Equal(left, right)
}
