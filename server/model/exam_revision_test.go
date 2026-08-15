// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package model

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestCanonicalExamRevisionPolicyIgnoresAuthoredJSONFieldOrder(t *testing.T) {
	t.Parallel()
	ordered := []byte(`{"schema_version":1,"connection_loss":{"outcome":"flag_and_suspend"},"focus_loss":{"enabled":true,"minimum_duration_milliseconds":2000,"incident_count":3,"window_milliseconds":300000,"outcome":"flag_and_warn"}}`)
	reordered := []byte(` { "focus_loss": {"outcome":"flag_and_warn","window_milliseconds":300000,"incident_count":3,"minimum_duration_milliseconds":2000,"enabled":true}, "connection_loss":{"outcome":"flag_and_suspend"}, "schema_version":1 } `)

	first, err := CanonicalizeExamRevisionPolicy(ordered)
	if err != nil {
		t.Fatal(err)
	}
	second, err := CanonicalizeExamRevisionPolicy(reordered)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first.Bytes, ordered) || !bytes.Equal(first.Bytes, second.Bytes) || first.SHA256 != second.SHA256 {
		t.Fatalf("first=%#v second=%#v", first, second)
	}
	want := fmt.Sprintf("%x", sha256.Sum256(ordered))
	if first.SchemaVersion != 1 || first.SHA256 != want {
		t.Fatalf("canonical policy=%#v want digest=%s", first, want)
	}
}

func TestNewExamRevisionFreezesExactBoundedSnapshot(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 8, 15, 12, 0, 0, 0, time.UTC)
	examID, revisionID := NewExamID(), NewExamRevisionID()
	policy, err := NewExamRevisionPolicy(DefaultExamPolicySet())
	if err != nil {
		t.Fatal(err)
	}
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
		Title: "Algorithms", InstructionsMarkdown: "# Solve", Policy: policy,
		Resources: []ExamRevisionResource{resource}, StarterWorkspace: []ExamRevisionStarterWorkspaceEntry{file, directory},
		PublishedByUserID: NewUserID(), PublishedAt: at, Kind: ExamRevisionPublicationStandard,
	})
	if err != nil {
		t.Fatal(err)
	}
	if revision.StarterWorkspace[0].Path != "cmd" || revision.StarterWorkspace[1].Path != "cmd/main.go" {
		t.Fatalf("workspace was not canonicalized: %#v", revision.StarterWorkspace)
	}
	if len(revision.Policy.Bytes) == 0 || len(revision.PolicyDigest) != 64 || len(revision.StarterWorkspaceDigest) != 64 || len(revision.ContentDigest) != 64 {
		t.Fatalf("revision digests=%#v", revision)
	}

	// Construction clones caller-owned slices and policy bytes. A published
	// value cannot be changed by retaining mutable input storage.
	resource.DisplayName = "changed"
	file.Path = "changed.go"
	policy.Bytes[0] = '!'
	if revision.Resources[0].DisplayName != "Reference" || revision.StarterWorkspace[1].Path != "cmd/main.go" || revision.Policy.Bytes[0] != '{' {
		t.Fatalf("revision retained mutable input: %#v", revision)
	}
	if err := revision.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestExamRevisionRejectsIncompleteOrUnboundedSnapshots(t *testing.T) {
	t.Parallel()
	at := time.Now().UTC()
	policy, _ := NewExamRevisionPolicy(DefaultExamPolicySet())
	base := ExamRevisionSpecification{
		ID: NewExamRevisionID(), ExamID: NewExamID(), Number: 1, SourceDraftRevision: 1,
		Title: "Exam", Policy: policy, PublishedByUserID: NewUserID(), PublishedAt: at,
		Kind: ExamRevisionPublicationStandard,
	}
	badResource := ExamRevisionResource{ResourceID: NewExamResourceID(), FileEntryID: NewFileEntryID(), FileRevisionID: NewFileRevisionID(), RenditionID: NewFileRenditionID(), DisplayName: "Bad", Position: 1, MediaType: ExamResourceMediaText, SizeBytes: 1, SHA256: fmt.Sprintf("%x", sha256.Sum256([]byte("x")))}
	badFile := ExamRevisionStarterWorkspaceEntry{EntryID: NewStarterWorkspaceEntryID(), Kind: StarterWorkspaceEntryFile, Path: "main.go"}
	for _, test := range []struct {
		name   string
		mutate func(*ExamRevisionSpecification)
	}{
		{name: "resource order gap", mutate: func(spec *ExamRevisionSpecification) { spec.Resources = []ExamRevisionResource{badResource} }},
		{name: "file missing exact object", mutate: func(spec *ExamRevisionSpecification) {
			spec.StarterWorkspace = []ExamRevisionStarterWorkspaceEntry{badFile}
		}},
		{name: "unsupported publication kind", mutate: func(spec *ExamRevisionSpecification) { spec.Kind = ExamRevisionPublicationKind("future") }},
		{name: "invalid base revision", mutate: func(spec *ExamRevisionSpecification) { spec.BaseRevisionID = "invalid" }},
	} {
		t.Run(test.name, func(t *testing.T) {
			spec := base
			test.mutate(&spec)
			if _, err := NewExamRevision(spec); err == nil {
				t.Fatal("invalid revision snapshot was accepted")
			}
		})
	}
}

func TestNewLiveCorrectionExamRevisionChangesOnlyCorrectableMaterial(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 8, 15, 14, 0, 0, 0, time.UTC)
	policy, err := NewExamRevisionPolicy(DefaultExamPolicySet())
	if err != nil {
		t.Fatal(err)
	}
	resource := ExamRevisionResource{ResourceID: NewExamResourceID(), FileEntryID: NewFileEntryID(),
		FileRevisionID: NewFileRevisionID(), RenditionID: NewFileRenditionID(), DisplayName: "Reference", Position: 0,
		MediaType: ExamResourceMediaText, SizeBytes: 4, SHA256: fmt.Sprintf("%x", sha256.Sum256([]byte("base")))}
	workspace := ExamRevisionStarterWorkspaceEntry{EntryID: NewStarterWorkspaceEntryID(), Kind: StarterWorkspaceEntryFile,
		Path: "main.go", ObjectID: NewStarterWorkspaceObjectID(), ContentVersion: NewWorkspaceContentVersion(),
		MediaType: "text/x-go", SizeBytes: 12, SHA256: fmt.Sprintf("%x", sha256.Sum256([]byte("package main")))}
	base, err := NewExamRevision(ExamRevisionSpecification{ID: NewExamRevisionID(), ExamID: NewExamID(), Number: 3,
		SourceDraftRevision: 9, Title: "Algorithms", InstructionsMarkdown: "Old", Policy: policy,
		Resources: []ExamRevisionResource{resource}, StarterWorkspace: []ExamRevisionStarterWorkspaceEntry{workspace},
		PublishedByUserID: NewUserID(), PublishedAt: at.Add(-time.Hour), Kind: ExamRevisionPublicationStandard})
	if err != nil {
		t.Fatal(err)
	}
	replacement := resource
	replacement.FileRevisionID, replacement.RenditionID = NewFileRevisionID(), NewFileRenditionID()
	replacement.SHA256 = fmt.Sprintf("%x", sha256.Sum256([]byte("fixed")))
	corrected, err := NewLiveCorrectionExamRevision(base, NewExamRevisionID(), 4, "Fixed **instructions**",
		[]ExamRevisionResource{replacement}, NewUserID(), at)
	if err != nil {
		t.Fatal(err)
	}
	if corrected.Kind != ExamRevisionPublicationLiveCorrection || corrected.BaseRevisionID != base.ID ||
		corrected.ExamID != base.ExamID || corrected.Title != base.Title || corrected.SourceDraftRevision != base.SourceDraftRevision ||
		corrected.PolicyDigest != base.PolicyDigest || !bytes.Equal(corrected.Policy.Bytes, base.Policy.Bytes) ||
		corrected.StarterWorkspaceDigest != base.StarterWorkspaceDigest || corrected.InstructionsMarkdown != "Fixed **instructions**" ||
		corrected.ContentDigest == base.ContentDigest {
		t.Fatalf("live correction=%#v base=%#v", corrected, base)
	}
	workspace.Path = "changed.go"
	policy.Bytes[0] = '!'
	if corrected.StarterWorkspace[0].Path != "main.go" || corrected.Policy.Bytes[0] != '{' {
		t.Fatalf("live correction retained mutable base input: %#v", corrected)
	}
}

func TestNewLiveCorrectionExamRevisionRejectsInvalidBaseOrOrdering(t *testing.T) {
	t.Parallel()
	if _, err := NewLiveCorrectionExamRevision(nil, NewExamRevisionID(), 2, "", nil, NewUserID(), time.Now().UTC()); err == nil {
		t.Fatal("nil base was accepted")
	}
	policy, _ := NewExamRevisionPolicy(DefaultExamPolicySet())
	base, err := NewExamRevision(ExamRevisionSpecification{ID: NewExamRevisionID(), ExamID: NewExamID(), Number: 2,
		SourceDraftRevision: 1, Title: "Exam", Policy: policy, PublishedByUserID: NewUserID(), PublishedAt: time.Now().UTC(),
		Kind: ExamRevisionPublicationStandard})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = NewLiveCorrectionExamRevision(base, NewExamRevisionID(), base.Number, "", nil, NewUserID(), time.Now().UTC()); err == nil {
		t.Fatal("non-increasing Revision number was accepted")
	}
}

func TestSameExamRevisionCandidatePresentationIgnoresStorageGenerations(t *testing.T) {
	t.Parallel()

	policy, err := NewExamRevisionPolicy(DefaultExamPolicySet())
	if err != nil {
		t.Fatal(err)
	}
	resource := ExamRevisionResource{ResourceID: NewExamResourceID(), FileEntryID: NewFileEntryID(), FileRevisionID: NewFileRevisionID(),
		RenditionID: NewFileRenditionID(), DisplayName: "Reference", Position: 0, MediaType: ExamResourceMediaText,
		SizeBytes: 4, SHA256: fmt.Sprintf("%x", sha256.Sum256([]byte("base")))}
	base, err := NewExamRevision(ExamRevisionSpecification{ID: NewExamRevisionID(), ExamID: NewExamID(), Number: 1,
		SourceDraftRevision: 1, Title: "Exam", Policy: policy, Resources: []ExamRevisionResource{resource},
		PublishedByUserID: NewUserID(), PublishedAt: time.Now().UTC(), Kind: ExamRevisionPublicationStandard})
	if err != nil {
		t.Fatal(err)
	}
	candidate := base.Clone()
	candidate.Resources[0].FileRevisionID = NewFileRevisionID()
	candidate.Resources[0].RenditionID = NewFileRenditionID()
	if !SameExamRevisionCandidatePresentation(base, candidate) {
		t.Fatal("same verified candidate presentation was treated as changed")
	}
	candidate.Resources[0].SHA256 = strings.Repeat("b", 64)
	if SameExamRevisionCandidatePresentation(base, candidate) {
		t.Fatal("changed verified content was treated as the same presentation")
	}
}
