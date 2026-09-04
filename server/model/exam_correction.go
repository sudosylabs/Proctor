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
	AcknowledgementRequired bool
}

func NewCandidateCorrectionNotice(summary string, changedAreas []ExamCorrectionChangedArea, acknowledgementRequired bool) (*CandidateCorrectionNotice, error) {
	notice := &CandidateCorrectionNotice{Summary: summary, ChangedAreas: slices.Clone(changedAreas), AcknowledgementRequired: acknowledgementRequired}
	if err := notice.Validate(); err != nil {
		return nil, err
	}
	return notice, nil
}

func (notice *CandidateCorrectionNotice) Validate() error {
	if notice == nil || !validCandidateCorrectionSummary(notice.Summary) || len(notice.ChangedAreas) < 1 || len(notice.ChangedAreas) > 3 {
		return errors.New("model: invalid Candidate Correction Notice")
	}
	for index, area := range notice.ChangedAreas {
		if !area.IsValid() || index > 0 && strings.Compare(string(notice.ChangedAreas[index-1]), string(area)) >= 0 {
			return errors.New("model: invalid Candidate Correction Notice changed areas")
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
	AcknowledgementRequired bool
	AcknowledgementState    CorrectionAcknowledgementState
	AcknowledgedAt          OptionalTime
}

func (correction CandidateLiveCorrection) Validate() error {
	notice, err := NewCandidateCorrectionNotice(correction.Summary, correction.ChangedAreas, correction.AcknowledgementRequired)
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
	}
	return cloned
}
