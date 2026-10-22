// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package model

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestCanonicalExamRevisionPolicyIgnoresAuthoredJSONFieldOrder(t *testing.T) {
	t.Parallel()
	ordered, err := os.ReadFile("testdata/default_exam_policy.json")
	require.NoError(t, err)
	var fields map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(ordered, &fields))
	reordered := []byte(fmt.Sprintf(` { "native": %s, "focus_loss": {"outcome":"flag_and_warn","window_milliseconds":300000,"incident_count":3,"minimum_duration_milliseconds":2000,"enabled":true}, "connection_loss":{"outcome":"flag_and_suspend"} } `, fields["native"]))

	first, err := CanonicalizeExamRevisionPolicy(ordered)
	require.NoError(t, err)
	second, err := CanonicalizeExamRevisionPolicy(reordered)
	require.NoError(t, err)
	require.True(t, bytes.Equal(first.Bytes, ordered), "first=%#v second=%#v", first, second)
	require.True(t, bytes.Equal(first.Bytes, second.Bytes), "first=%#v second=%#v", first, second)
	require.Equal(t, second.SHA256, first.SHA256, "first=%#v second=%#v", first, second)
	want := fmt.Sprintf("%x", sha256.Sum256(ordered))
	require.Equal(t, 1, first.SchemaVersion, "canonical policy=%#v want digest", first)
	require.Equal(t, want, first.SHA256, "canonical policy=%#v want digest", first)
}

func TestNewExamRevisionFreezesExactBoundedSnapshot(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 8, 15, 12, 0, 0, 0, time.UTC)
	examID, revisionID := NewExamID(), NewExamRevisionID()
	policy, err := NewExamRevisionPolicy(DefaultExamPolicySet())
	require.NoError(t, err)
	resource := ExamRevisionResource{
		ResourceID: NewExamResourceID(), FileEntryID: NewFileEntryID(), FileRevisionID: NewFileRevisionID(),
		RenditionID: NewFileRenditionID(), DisplayName: "Reference", DescriptionMarkdown: "Read **this**.",
		Position: 0, MediaType: ExamResourceMediaPDF, SizeBytes: 128, SHA256: fmt.Sprintf("%x", sha256.Sum256([]byte("resource"))),
	}
	directory := ExamRevisionStarterWorkspaceEntry{EntryID: NewStarterWorkspaceEntryID(), Kind: StarterWorkspaceEntryDirectory, Path: "cmd"}
	file := ExamRevisionStarterWorkspaceEntry{
		EntryID: NewStarterWorkspaceEntryID(), Kind: StarterWorkspaceEntryFile, Path: "cmd/main.go",
		ObjectID: NewStarterWorkspaceObjectID(), ContentVersion: NewWorkspaceContentVersion(), MediaType: "text/x-go",
		SizeBytes: 12, SHA256: fmt.Sprintf("%x", sha256.Sum256([]byte("package main"))),
	}
	revision, err := NewExamRevision(ExamRevisionSpecification{
		ID: revisionID, ExamID: examID, Number: 1, SourceDraftRevision: 7,
		Title: "Algorithms", InstructionsMarkdown: "# Solve", Policy: policy, Capacity: DefaultExamCapacityPolicy(),
		Resources: []ExamRevisionResource{resource}, StarterWorkspace: []ExamRevisionStarterWorkspaceEntry{file, directory},
		PublishedByUserID: NewUserID(), PublishedAt: at, Kind: ExamRevisionPublicationStandard,
	})
	require.NoError(t, err)
	require.Equal(t, "cmd", revision.StarterWorkspace[0].Path)
	require.Equal(t, "cmd/main.go", revision.StarterWorkspace[1].Path)
	require.NotEmpty(t, revision.Policy.Bytes)
	require.Len(t, revision.PolicyDigest, 64)
	require.Len(t, revision.StarterWorkspaceDigest, 64)
	require.Len(t, revision.ContentDigest, 64)

	// Construction clones caller-owned slices and policy bytes. A published
	// value cannot be changed by retaining mutable input storage.
	resource.DisplayName = "changed"
	file.Path = "changed.go"
	policy.Bytes[0] = '!'
	require.Equal(t, "Reference", revision.Resources[0].DisplayName)
	require.Equal(t, "cmd/main.go", revision.StarterWorkspace[1].Path)
	require.Equal(t, byte('{'), revision.Policy.Bytes[0])
	require.NoError(t, revision.Validate())
}

func TestExamRevisionRejectsIncompleteOrUnboundedSnapshots(t *testing.T) {
	t.Parallel()
	at := time.Now().UTC()
	policy, _ := NewExamRevisionPolicy(DefaultExamPolicySet())
	base := ExamRevisionSpecification{
		ID: NewExamRevisionID(), ExamID: NewExamID(), Number: 1, SourceDraftRevision: 1,
		Title: "Exam", Policy: policy, Capacity: DefaultExamCapacityPolicy(), PublishedByUserID: NewUserID(), PublishedAt: at,
		Kind: ExamRevisionPublicationStandard,
	}
	badResource := ExamRevisionResource{ResourceID: NewExamResourceID(), FileEntryID: NewFileEntryID(), FileRevisionID: NewFileRevisionID(), RenditionID: NewFileRenditionID(), DisplayName: "Bad", Position: 1, MediaType: ExamResourceMediaText, SizeBytes: 1, SHA256: fmt.Sprintf("%x", sha256.Sum256([]byte("x")))}
	badFile := ExamRevisionStarterWorkspaceEntry{EntryID: NewStarterWorkspaceEntryID(), Kind: StarterWorkspaceEntryFile, Path: "main.go"}
	for _, test := range []struct {
		name   string
		mutate func(*ExamRevisionSpecification)
	}{
		{name: "resource order gap", mutate: func(spec *ExamRevisionSpecification) { spec.Resources = []ExamRevisionResource{badResource} }},
		{name: "missing capacity policy", mutate: func(spec *ExamRevisionSpecification) { spec.Capacity = ExamCapacityPolicy{} }},
		{name: "file missing exact object", mutate: func(spec *ExamRevisionSpecification) {
			spec.StarterWorkspace = []ExamRevisionStarterWorkspaceEntry{badFile}
		}},
		{name: "unsupported publication kind", mutate: func(spec *ExamRevisionSpecification) { spec.Kind = ExamRevisionPublicationKind("future") }},
		{name: "invalid base revision", mutate: func(spec *ExamRevisionSpecification) { spec.BaseRevisionID = "invalid" }},
	} {
		t.Run(test.name, func(t *testing.T) {
			spec := base
			test.mutate(&spec)
			_, err := NewExamRevision(spec)
			require.Error(t, err, "invalid revision snapshot was accepted")
		})
	}
}

func TestNewLiveCorrectionExamRevisionChangesOnlyCorrectableMaterial(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 8, 15, 14, 0, 0, 0, time.UTC)
	policy, err := NewExamRevisionPolicy(DefaultExamPolicySet())
	require.NoError(t, err)
	resource := ExamRevisionResource{ResourceID: NewExamResourceID(), FileEntryID: NewFileEntryID(),
		FileRevisionID: NewFileRevisionID(), RenditionID: NewFileRenditionID(), DisplayName: "Reference", Position: 0,
		MediaType: ExamResourceMediaText, SizeBytes: 4, SHA256: fmt.Sprintf("%x", sha256.Sum256([]byte("base")))}
	workspace := ExamRevisionStarterWorkspaceEntry{EntryID: NewStarterWorkspaceEntryID(), Kind: StarterWorkspaceEntryFile,
		Path: "main.go", ObjectID: NewStarterWorkspaceObjectID(), ContentVersion: NewWorkspaceContentVersion(),
		MediaType: "text/x-go", SizeBytes: 12, SHA256: fmt.Sprintf("%x", sha256.Sum256([]byte("package main")))}
	capacity := DefaultExamCapacityPolicy()
	capacity.ResourceMaximumCount = 20
	base, err := NewExamRevision(ExamRevisionSpecification{ID: NewExamRevisionID(), ExamID: NewExamID(), Number: 3,
		SourceDraftRevision: 9, Title: "Algorithms", InstructionsMarkdown: "Old", Policy: policy, Capacity: capacity,
		Resources: []ExamRevisionResource{resource}, StarterWorkspace: []ExamRevisionStarterWorkspaceEntry{workspace},
		PublishedByUserID: NewUserID(), PublishedAt: at.Add(-time.Hour), Kind: ExamRevisionPublicationStandard})
	require.NoError(t, err)
	replacement := resource
	replacement.FileRevisionID, replacement.RenditionID = NewFileRevisionID(), NewFileRenditionID()
	replacement.SHA256 = fmt.Sprintf("%x", sha256.Sum256([]byte("fixed")))
	corrected, err := NewLiveCorrectionExamRevision(base, LiveCorrectionExamRevisionSpecification{ID: NewExamRevisionID(), Number: 4,
		InstructionsMarkdown: "Fixed **instructions**", Resources: []ExamRevisionResource{replacement},
		AffectedCapabilities: []CandidateCapability{CandidateCapabilityBrowser, CandidateCapabilitySubmission, CandidateCapabilityWorkspace}, CandidateSummary: "The instructions and reference were corrected.", PublishedByUserID: NewUserID(), PublishedAt: at})
	require.NoError(t, err)
	require.Equal(t, ExamRevisionPublicationLiveCorrection, corrected.Kind, "live correction=%#v base=%#v", corrected, base)
	require.Equal(t, base.ID, corrected.BaseRevisionID, "live correction=%#v base=%#v", corrected, base)
	require.Equal(t, base.ExamID, corrected.ExamID, "live correction=%#v base=%#v", corrected, base)
	require.Equal(t, base.Title, corrected.Title, "live correction=%#v base=%#v", corrected, base)
	require.Equal(t, base.SourceDraftRevision, corrected.SourceDraftRevision, "live correction=%#v base=%#v", corrected, base)
	require.Equal(t, capacity, corrected.Capacity, "live correction=%#v base=%#v", corrected, base)
	require.Equal(t, base.PolicyDigest, corrected.PolicyDigest, "live correction=%#v base=%#v", corrected, base)
	require.True(t, bytes.Equal(corrected.Policy.Bytes, base.Policy.Bytes), "live correction=%#v base=%#v", corrected, base)
	require.Equal(t, base.StarterWorkspaceDigest, corrected.StarterWorkspaceDigest, "live correction=%#v base=%#v", corrected, base)
	require.Equal(t, "Fixed **instructions**", corrected.InstructionsMarkdown, "live correction=%#v base=%#v", corrected, base)
	require.NotEqual(t, base.ContentDigest, corrected.ContentDigest, "live correction=%#v base=%#v", corrected, base)
	workspace.Path = "changed.go"
	policy.Bytes[0] = '!'
	require.Equal(t, "main.go", corrected.StarterWorkspace[0].Path)
	require.Equal(t, byte('{'), corrected.Policy.Bytes[0])
}

func TestNewLiveCorrectionExamRevisionRejectsInvalidBaseOrOrdering(t *testing.T) {
	t.Parallel()
	_, err := NewLiveCorrectionExamRevision(nil, LiveCorrectionExamRevisionSpecification{ID: NewExamRevisionID(), Number: 2})
	require.Error(t, err, "nil base was accepted")
	policy, _ := NewExamRevisionPolicy(DefaultExamPolicySet())
	base, err := NewExamRevision(ExamRevisionSpecification{ID: NewExamRevisionID(), ExamID: NewExamID(), Number: 2,
		SourceDraftRevision: 1, Title: "Exam", Policy: policy, Capacity: DefaultExamCapacityPolicy(), PublishedByUserID: NewUserID(), PublishedAt: time.Now().UTC(),
		Kind: ExamRevisionPublicationStandard})
	require.NoError(t, err)
	_, err = NewLiveCorrectionExamRevision(base, LiveCorrectionExamRevisionSpecification{ID: NewExamRevisionID(), Number: base.Number,
		AffectedCapabilities: []CandidateCapability{CandidateCapabilityBrowser, CandidateCapabilitySubmission, CandidateCapabilityWorkspace}, CandidateSummary: "Correction.", PublishedByUserID: NewUserID(), PublishedAt: time.Now().UTC()})
	require.Error(t, err, "non-increasing Revision number was accepted")
}
