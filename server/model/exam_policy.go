// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package model

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"
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
}

func DefaultExamPolicySet() ExamPolicySet {
	return ExamPolicySet{
		SchemaVersion:  ExamPolicySchemaVersion,
		ConnectionLoss: ConnectionLossPolicy{Outcome: IntegrityOutcomeFlagAndSuspend},
		FocusLoss: FocusLossPolicy{
			Enabled: true, MinimumDuration: 2 * time.Second, IncidentCount: 3,
			Window: 5 * time.Minute, Outcome: IntegrityOutcomeFlagAndWarn,
		},
	}
}

func (p ExamPolicySet) Validate() error {
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
	SchemaVersion  int                      `json:"schema_version"`
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
	data, err := json.Marshal(examPolicyWire{
		SchemaVersion:  policy.SchemaVersion,
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
	if err := rejectDuplicateJSONFields(data); err != nil {
		return ExamPolicySet{}, err
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var wire struct {
		SchemaVersion  *int `json:"schema_version"`
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
	if wire.SchemaVersion == nil || wire.ConnectionLoss == nil || wire.ConnectionLoss.Outcome == nil ||
		wire.FocusLoss == nil || wire.FocusLoss.Enabled == nil || wire.FocusLoss.MinimumDurationMilliseconds == nil ||
		wire.FocusLoss.IncidentCount == nil || wire.FocusLoss.WindowMilliseconds == nil || wire.FocusLoss.Outcome == nil {
		return ExamPolicySet{}, errors.New("model: exam policy document is incomplete")
	}
	if *wire.FocusLoss.MinimumDurationMilliseconds < 500 || *wire.FocusLoss.MinimumDurationMilliseconds > 300_000 ||
		*wire.FocusLoss.WindowMilliseconds < 10_000 || *wire.FocusLoss.WindowMilliseconds > 14_400_000 {
		return ExamPolicySet{}, errors.New("model: exam policy duration is out of bounds")
	}
	policy := ExamPolicySet{
		SchemaVersion:  *wire.SchemaVersion,
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

func rejectDuplicateJSONFields(data []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	var walk func() error
	walk = func() error {
		token, err := decoder.Token()
		if err != nil {
			return err
		}
		delimiter, ok := token.(json.Delim)
		if !ok {
			return nil
		}
		switch delimiter {
		case '{':
			seen := map[string]struct{}{}
			for decoder.More() {
				keyToken, err := decoder.Token()
				if err != nil {
					return err
				}
				key, ok := keyToken.(string)
				if !ok {
					return errors.New("model: invalid exam policy object key")
				}
				if _, exists := seen[key]; exists {
					return fmt.Errorf("model: duplicate exam policy field %q", key)
				}
				seen[key] = struct{}{}
				if err := walk(); err != nil {
					return err
				}
			}
			_, err = decoder.Token()
			return err
		case '[':
			for decoder.More() {
				if err := walk(); err != nil {
					return err
				}
			}
			_, err = decoder.Token()
			return err
		default:
			return errors.New("model: invalid exam policy JSON")
		}
	}
	return walk()
}
