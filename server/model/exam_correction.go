// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package model

import (
	"errors"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

const ExamSittingMaximumLiveCorrections = 32

// CandidateCapability identifies an independently gated candidate operation family.
type CandidateCapability string

const (
	CandidateCapabilityBrowser    CandidateCapability = "browser"
	CandidateCapabilitySubmission CandidateCapability = "submission"
	CandidateCapabilityWorkspace  CandidateCapability = "workspace"
)

func (capability CandidateCapability) IsValid() bool {
	switch capability {
	case CandidateCapabilityBrowser, CandidateCapabilitySubmission, CandidateCapabilityWorkspace:
		return true
	default:
		return false
	}
}

// ValidateCandidateCapabilities checks the bounded, sorted, unique projection.
// An empty set is valid for a pending union; authored selections must be nonempty.
func ValidateCandidateCapabilities(capabilities []CandidateCapability) error {
	if len(capabilities) > 3 {
		return errors.New("model: invalid Candidate capabilities")
	}
	for index, capability := range capabilities {
		if !capability.IsValid() || index > 0 && capabilities[index-1] >= capability {
			return errors.New("model: invalid Candidate capabilities")
		}
	}
	return nil
}

// ValidateCandidateCorrectionSelection checks authoring metadata before actual
// changed areas are known. Revision creation also enforces their semantic minimum.
func ValidateCandidateCorrectionSelection(summary string, capabilities []CandidateCapability) error {
	if !validCandidateCorrectionSummary(summary) || len(capabilities) == 0 {
		return errors.New("model: invalid Candidate Correction selection")
	}
	return ValidateCandidateCapabilities(capabilities)
}

// MinimumCorrectionCapabilities returns the sorted union required by actual
// semantic changes. A browser-only correction has no workspace or submission gate.
func MinimumCorrectionCapabilities(areas []ExamCorrectionChangedArea) []CandidateCapability {
	result := make([]CandidateCapability, 0, 3)
	if slices.Contains(areas, ExamCorrectionChangedBrowserPolicy) {
		result = append(result, CandidateCapabilityBrowser)
	}
	if slices.Contains(areas, ExamCorrectionChangedInstructions) || slices.Contains(areas, ExamCorrectionChangedResources) {
		result = append(result, CandidateCapabilitySubmission, CandidateCapabilityWorkspace)
	}
	return result
}

// PendingCorrectionCapabilities derives a projection from validated immutable
// notices. Acknowledging one notice cannot remove another notice's gate.
func PendingCorrectionCapabilities(corrections []CandidateLiveCorrection) []CandidateCapability {
	pending := make([]CandidateCapability, 0, 3)
	for _, capability := range []CandidateCapability{CandidateCapabilityBrowser, CandidateCapabilitySubmission, CandidateCapabilityWorkspace} {
		for _, correction := range corrections {
			if correction.AcknowledgementState == CorrectionAcknowledgementPending && slices.Contains(correction.AffectedCapabilities, capability) {
				pending = append(pending, capability)
				break
			}
		}
	}
	return pending
}

type ExamCorrectionChangedArea string

const (
	ExamCorrectionChangedInstructions  ExamCorrectionChangedArea = "instructions"
	ExamCorrectionChangedResources     ExamCorrectionChangedArea = "resources"
	ExamCorrectionChangedBrowserPolicy ExamCorrectionChangedArea = "browser_policy"
)

func (area ExamCorrectionChangedArea) IsValid() bool {
	return area == ExamCorrectionChangedInstructions || area == ExamCorrectionChangedResources || area == ExamCorrectionChangedBrowserPolicy
}

// CandidateCorrectionNotice is immutable candidate-visible metadata owned by
// one live-correction Exam Revision. The manager's private reason is retained
// separately and must never be projected through this value.
type CandidateCorrectionNotice struct {
	Summary                 string
	ChangedAreas            []ExamCorrectionChangedArea
	AffectedCapabilities    []CandidateCapability
	AcknowledgementRequired bool
}

func NewCandidateCorrectionNotice(summary string, changedAreas []ExamCorrectionChangedArea, affectedCapabilities []CandidateCapability, acknowledgementRequired bool) (*CandidateCorrectionNotice, error) {
	notice := &CandidateCorrectionNotice{Summary: summary, ChangedAreas: slices.Clone(changedAreas), AffectedCapabilities: slices.Clone(affectedCapabilities), AcknowledgementRequired: acknowledgementRequired}
	if err := notice.Validate(); err != nil {
		return nil, err
	}
	return notice, nil
}

func (notice *CandidateCorrectionNotice) Validate() error {
	if notice == nil || ValidateCandidateCorrectionSelection(notice.Summary, notice.AffectedCapabilities) != nil || len(notice.ChangedAreas) < 1 || len(notice.ChangedAreas) > 3 {
		return errors.New("model: invalid Candidate Correction Notice")
	}
	for index, area := range notice.ChangedAreas {
		if !area.IsValid() || index > 0 && strings.Compare(string(notice.ChangedAreas[index-1]), string(area)) >= 0 {
			return errors.New("model: invalid Candidate Correction Notice changed areas")
		}
	}
	for _, required := range MinimumCorrectionCapabilities(notice.ChangedAreas) {
		if !slices.Contains(notice.AffectedCapabilities, required) {
			return errors.New("model: Candidate Correction selection omits an affected capability")
		}
	}
	return nil
}

func (notice *CandidateCorrectionNotice) Clone() *CandidateCorrectionNotice {
	if notice == nil {
		return nil
	}
	clone := *notice
	clone.ChangedAreas = slices.Clone(notice.ChangedAreas)
	clone.AffectedCapabilities = slices.Clone(notice.AffectedCapabilities)
	return &clone
}

func validCandidateCorrectionSummary(summary string) bool {
	if !utf8.ValidString(summary) || strings.TrimSpace(summary) != summary || len(summary) < 1 || len(summary) > 2000 || utf8.RuneCountInString(summary) > 500 {
		return false
	}
	for _, value := range summary {
		if unicode.IsControl(value) {
			return false
		}
	}
	return true
}

type CorrectionAcknowledgementState string

const (
	CorrectionAcknowledgementNotRequired  CorrectionAcknowledgementState = "not_required"
	CorrectionAcknowledgementPending      CorrectionAcknowledgementState = "pending"
	CorrectionAcknowledgementAcknowledged CorrectionAcknowledgementState = "acknowledged"
)

type CandidateLiveCorrection struct {
	RevisionID              ExamRevisionID
	RevisionNumber          int64
	EffectiveAt             time.Time
	Summary                 string
	ChangedAreas            []ExamCorrectionChangedArea
	AffectedCapabilities    []CandidateCapability
	AcknowledgementRequired bool
	AcknowledgementState    CorrectionAcknowledgementState
	AcknowledgedAt          OptionalTime
}

func (correction CandidateLiveCorrection) Validate() error {
	notice, err := NewCandidateCorrectionNotice(correction.Summary, correction.ChangedAreas, correction.AffectedCapabilities, correction.AcknowledgementRequired)
	if err != nil || !correction.RevisionID.IsValid() || correction.RevisionNumber < 1 || correction.EffectiveAt.IsZero() {
		return errors.New("model: invalid Candidate Live Correction")
	}
	_ = notice
	switch correction.AcknowledgementState {
	case CorrectionAcknowledgementNotRequired:
		if correction.AcknowledgementRequired || correction.AcknowledgedAt.Valid {
			return errors.New("model: inconsistent notice-only Candidate Live Correction")
		}
	case CorrectionAcknowledgementPending:
		if !correction.AcknowledgementRequired || correction.AcknowledgedAt.Valid {
			return errors.New("model: inconsistent pending Candidate Live Correction")
		}
	case CorrectionAcknowledgementAcknowledged:
		if !correction.AcknowledgementRequired || !correction.AcknowledgedAt.Valid || correction.AcknowledgedAt.Time.Before(correction.EffectiveAt) {
			return errors.New("model: inconsistent acknowledged Candidate Live Correction")
		}
	default:
		return errors.New("model: invalid Candidate Live Correction acknowledgement state")
	}
	return nil
}

func CloneCandidateLiveCorrections(values []CandidateLiveCorrection) []CandidateLiveCorrection {
	cloned := make([]CandidateLiveCorrection, len(values))
	copy(cloned, values)
	for index := range cloned {
		cloned[index].ChangedAreas = slices.Clone(values[index].ChangedAreas)
		cloned[index].AffectedCapabilities = slices.Clone(values[index].AffectedCapabilities)
	}
	return cloned
}
