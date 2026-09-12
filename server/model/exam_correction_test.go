// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package model

import (
	"slices"
	"testing"
	"time"
)

func TestCorrectionSelectionsEnforceActualChangedAreaMinimums(t *testing.T) {
	all := []CandidateCapability{CandidateCapabilityBrowser, CandidateCapabilitySubmission, CandidateCapabilityWorkspace}
	work := []CandidateCapability{CandidateCapabilitySubmission, CandidateCapabilityWorkspace}
	for _, test := range []struct {
		name         string
		areas        []ExamCorrectionChangedArea
		capabilities []CandidateCapability
		valid        bool
	}{
		{"browser minimum", []ExamCorrectionChangedArea{ExamCorrectionChangedBrowserPolicy}, []CandidateCapability{CandidateCapabilityBrowser}, true},
		{"instructions minimum", []ExamCorrectionChangedArea{ExamCorrectionChangedInstructions}, work, true},
		{"resources minimum", []ExamCorrectionChangedArea{ExamCorrectionChangedResources}, work, true},
		{"combined union", []ExamCorrectionChangedArea{ExamCorrectionChangedBrowserPolicy, ExamCorrectionChangedInstructions}, all, true},
		{"deliberate superset", []ExamCorrectionChangedArea{ExamCorrectionChangedBrowserPolicy}, all, true},
		{"missing selection", []ExamCorrectionChangedArea{ExamCorrectionChangedBrowserPolicy}, nil, false},
		{"empty selection", []ExamCorrectionChangedArea{ExamCorrectionChangedBrowserPolicy}, []CandidateCapability{}, false},
		{"no content change", nil, all, false},
		{"browser omitted", []ExamCorrectionChangedArea{ExamCorrectionChangedBrowserPolicy}, work, false},
		{"workspace omitted", []ExamCorrectionChangedArea{ExamCorrectionChangedInstructions}, []CandidateCapability{CandidateCapabilitySubmission}, false},
		{"submission omitted", []ExamCorrectionChangedArea{ExamCorrectionChangedResources}, []CandidateCapability{CandidateCapabilityWorkspace}, false},
		{"unknown", []ExamCorrectionChangedArea{ExamCorrectionChangedBrowserPolicy}, []CandidateCapability{"arbitrary", CandidateCapabilityBrowser}, false},
		{"duplicate", []ExamCorrectionChangedArea{ExamCorrectionChangedBrowserPolicy}, []CandidateCapability{CandidateCapabilityBrowser, CandidateCapabilityBrowser}, false},
		{"unsorted", []ExamCorrectionChangedArea{ExamCorrectionChangedBrowserPolicy}, []CandidateCapability{CandidateCapabilityWorkspace, CandidateCapabilityBrowser}, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			for _, required := range []bool{false, true} {
				notice, err := NewCandidateCorrectionNotice("Updated reference.", test.areas, test.capabilities, required)
				if (err == nil) != test.valid {
					t.Fatalf("required=%v, notice=%#v, err=%v", required, notice, err)
				}
				if !test.valid {
					continue
				}
				clone := notice.Clone()
				clone.AffectedCapabilities[0] = "invalid"
				if notice.Validate() != nil || clone.Validate() == nil {
					t.Fatal("clone aliases or loses its selection")
				}
			}
		})
	}
}

func TestPendingCorrectionUnionPreservesOverlapsAndIgnoresNoticeOnly(t *testing.T) {
	at := NowUTC()
	notices := []CandidateLiveCorrection{
		{RevisionID: NewExamRevisionID(), RevisionNumber: 2, EffectiveAt: at, Summary: "Browser changed.", ChangedAreas: []ExamCorrectionChangedArea{ExamCorrectionChangedBrowserPolicy}, AffectedCapabilities: []CandidateCapability{CandidateCapabilityBrowser}, AcknowledgementRequired: true, AcknowledgementState: CorrectionAcknowledgementPending},
		{RevisionID: NewExamRevisionID(), RevisionNumber: 3, EffectiveAt: at, Summary: "Browser changed again.", ChangedAreas: []ExamCorrectionChangedArea{ExamCorrectionChangedBrowserPolicy}, AffectedCapabilities: []CandidateCapability{CandidateCapabilityBrowser}, AcknowledgementRequired: true, AcknowledgementState: CorrectionAcknowledgementPending},
		{RevisionID: NewExamRevisionID(), RevisionNumber: 4, EffectiveAt: at, Summary: "Instructions changed.", ChangedAreas: []ExamCorrectionChangedArea{ExamCorrectionChangedInstructions}, AffectedCapabilities: []CandidateCapability{CandidateCapabilitySubmission, CandidateCapabilityWorkspace}, AcknowledgementState: CorrectionAcknowledgementNotRequired},
	}
	for _, notice := range notices {
		if err := notice.Validate(); err != nil {
			t.Fatal(err)
		}
	}
	if got := PendingCorrectionCapabilities(notices); !slices.Equal(got, []CandidateCapability{CandidateCapabilityBrowser}) {
		t.Fatal(got)
	}
	notices[0].AcknowledgementState = CorrectionAcknowledgementAcknowledged
	notices[0].AcknowledgedAt = OptionalTimeFrom(at.Add(time.Second))
	if got := PendingCorrectionCapabilities(notices); !slices.Equal(got, []CandidateCapability{CandidateCapabilityBrowser}) {
		t.Fatal("one acknowledgement removed another gate", got)
	}
	notices[1].AcknowledgementState = CorrectionAcknowledgementAcknowledged
	notices[1].AcknowledgedAt = OptionalTimeFrom(at.Add(time.Second))
	if got := PendingCorrectionCapabilities(notices); len(got) != 0 || got == nil {
		t.Fatal("empty union is not an array", got)
	}
	clone := CloneCandidateLiveCorrections(notices)
	clone[0].AffectedCapabilities[0] = CandidateCapabilityWorkspace
	if notices[0].AffectedCapabilities[0] != CandidateCapabilityBrowser {
		t.Fatal("notice projection aliases its selection")
	}
}
