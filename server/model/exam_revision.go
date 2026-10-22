// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package model

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"slices"
	"strings"
	"time"
)

const ExamRevisionSnapshotSchemaVersion = 1

type ExamRevisionPublicationKind string

const (
	ExamRevisionPublicationStandard       ExamRevisionPublicationKind = "standard"
	ExamRevisionPublicationLiveCorrection ExamRevisionPublicationKind = "live_correction"
)

func (k ExamRevisionPublicationKind) IsValid() bool {
	return k == ExamRevisionPublicationStandard || k == ExamRevisionPublicationLiveCorrection
}

// ExamRevisionPolicy is the exact canonical policy document frozen into one
// published Revision. Bytes are application canonical JSON, never PostgreSQL's
// JSONB textual representation; SHA256 is lowercase hexadecimal over Bytes.
type ExamRevisionPolicy struct {
	SchemaVersion int
	Bytes         []byte
	SHA256        string
}

func NewExamRevisionPolicy(policy ExamPolicySet) (ExamRevisionPolicy, error) {
	encoded, err := EncodeExamPolicySet(policy)
	if err != nil {
		return ExamRevisionPolicy{}, err
	}
	return newExamRevisionPolicy(policy.SchemaVersion, encoded), nil
}

// CanonicalizeExamRevisionPolicy strictly decodes an authored or persisted
// policy document, then re-encodes the typed value using the explicit schema
// codec. Input whitespace and member order therefore cannot affect its digest.
func CanonicalizeExamRevisionPolicy(data []byte) (ExamRevisionPolicy, error) {
	policy, err := DecodeExamPolicySet(data)
	if err != nil {
		return ExamRevisionPolicy{}, err
	}
	return NewExamRevisionPolicy(policy)
}

func newExamRevisionPolicy(schemaVersion int, encoded []byte) ExamRevisionPolicy {
	digest := sha256.Sum256(encoded)
	return ExamRevisionPolicy{SchemaVersion: schemaVersion, Bytes: bytesClone(encoded), SHA256: hex.EncodeToString(digest[:])}
}

func (p ExamRevisionPolicy) Validate() error {
	canonical, err := CanonicalizeExamRevisionPolicy(p.Bytes)
	if err != nil || p.SchemaVersion != canonical.SchemaVersion || p.SHA256 != canonical.SHA256 || !slices.Equal(p.Bytes, canonical.Bytes) {
		return invalidModelError("ExamRevisionPolicy.Validate", "exam_revision_policy", "", "invalid canonical Exam Revision policy", "")
	}
	return nil
}

type ExamRevisionResource struct {
	ResourceID          ExamResourceID
	FileEntryID         FileEntryID
	FileRevisionID      FileRevisionID
	RenditionID         FileRenditionID
	DisplayName         string
	DescriptionMarkdown string
	Position            int
	MediaType           ExamResourceMediaType
	SizeBytes           int64
	SHA256              string
}

func (r ExamRevisionResource) Validate() error {
	if !r.ResourceID.IsValid() || !r.FileEntryID.IsValid() || !r.FileRevisionID.IsValid() || !r.RenditionID.IsValid() {
		return invalidModelError("ExamRevisionResource.Validate", "exam_revision_resource", "", "invalid Exam Revision resource identity", "")
	}
	probe, err := NewExamResource(r.ResourceID, NewExamID(), r.FileEntryID, r.FileRevisionID, r.DisplayName, r.DescriptionMarkdown, r.Position, time.Unix(1, 0).UTC())
	if err != nil || probe.DisplayName != r.DisplayName || !r.MediaType.IsValid() || r.SizeBytes < 0 || r.SizeBytes > ExamResourceMaximumBytes || !validLowerSHA256(r.SHA256) {
		return invalidModelError("ExamRevisionResource.Validate", "exam_revision_resource", "", "invalid Exam Revision resource snapshot", "")
	}
	return nil
}

type ExamRevisionStarterWorkspaceEntry struct {
	EntryID        StarterWorkspaceEntryID
	Kind           StarterWorkspaceEntryKind
	Path           string
	ObjectID       StarterWorkspaceObjectID
	ContentVersion WorkspaceContentVersion
	MediaType      string
	SizeBytes      int64
	SHA256         string
}

func (e ExamRevisionStarterWorkspaceEntry) Validate() error {
	if !e.EntryID.IsValid() {
		return invalidModelError("ExamRevisionStarterWorkspaceEntry.Validate", "exam_revision_starter_workspace_entry", "", "invalid Exam Revision Starter Workspace identity", "")
	}
	path, err := NormalizeStarterWorkspacePath(e.Path)
	if err != nil || path != e.Path {
		return invalidModelError("ExamRevisionStarterWorkspaceEntry.Validate", "exam_revision_starter_workspace_entry", "", "invalid Exam Revision Starter Workspace path", "")
	}
	switch e.Kind {
	case StarterWorkspaceEntryDirectory:
		if !e.ObjectID.IsZero() || !e.ContentVersion.IsZero() || e.MediaType != "" || e.SizeBytes != 0 || e.SHA256 != "" {
			return invalidModelError("ExamRevisionStarterWorkspaceEntry.Validate", "exam_revision_starter_workspace_entry", "", "Exam Revision directory cannot carry content", "")
		}
	case StarterWorkspaceEntryFile:
		if !e.ObjectID.IsValid() || !e.ContentVersion.IsValid() || e.MediaType == "" || strings.TrimSpace(e.MediaType) != e.MediaType || len(e.MediaType) > 255 || e.SizeBytes < 0 || e.SizeBytes > StarterWorkspaceMaximumFileBytes || !validLowerSHA256(e.SHA256) {
			return invalidModelError("ExamRevisionStarterWorkspaceEntry.Validate", "exam_revision_starter_workspace_entry", "", "invalid Exam Revision Starter Workspace file", "")
		}
	default:
		return invalidModelError("ExamRevisionStarterWorkspaceEntry.Validate", "exam_revision_starter_workspace_entry", "", "invalid Exam Revision Starter Workspace kind", "")
	}
	return nil
}

type ExamRevisionSpecification struct {
	ID                   ExamRevisionID
	ExamID               ExamID
	Number               int64
	SourceDraftRevision  int64
	Title                string
	InstructionsMarkdown string
	Policy               ExamRevisionPolicy
	BrowserPolicy        BrowserPolicy
	Capacity             ExamCapacityPolicy
	Resources            []ExamRevisionResource
	StarterWorkspace     []ExamRevisionStarterWorkspaceEntry
	PublishedByUserID    UserID
	PublishedAt          time.Time
	BaseRevisionID       ExamRevisionID
	Kind                 ExamRevisionPublicationKind
	CandidateCorrection  *CandidateCorrectionNotice
}

// ExamRevision is one immutable published generation. Its aggregate digests
// make unchanged-Draft detection and later live-correction constraints
// independent of SQL JSON rendering and row order.
type ExamRevision struct {
	ID                     ExamRevisionID
	ExamID                 ExamID
	Number                 int64
	SourceDraftRevision    int64
	Title                  string
	InstructionsMarkdown   string
	Policy                 ExamRevisionPolicy
	PolicyDigest           string
	BrowserPolicy          BrowserPolicy
	BrowserPolicyDigest    string
	Capacity               ExamCapacityPolicy
	Resources              []ExamRevisionResource
	StarterWorkspace       []ExamRevisionStarterWorkspaceEntry
	StarterWorkspaceDigest string
	ContentDigest          string
	PublishedByUserID      UserID
	PublishedAt            time.Time
	BaseRevisionID         ExamRevisionID
	Kind                   ExamRevisionPublicationKind
	CandidateCorrection    *CandidateCorrectionNotice
}

func NewExamRevision(spec ExamRevisionSpecification) (*ExamRevision, error) {
	revision := &ExamRevision{
		ID: spec.ID, ExamID: spec.ExamID, Number: spec.Number, SourceDraftRevision: spec.SourceDraftRevision,
		Title: spec.Title, InstructionsMarkdown: spec.InstructionsMarkdown,
		Policy: cloneExamRevisionPolicy(spec.Policy), PolicyDigest: spec.Policy.SHA256,
		BrowserPolicy:     spec.BrowserPolicy.Clone(),
		Capacity:          spec.Capacity,
		Resources:         append([]ExamRevisionResource(nil), spec.Resources...),
		StarterWorkspace:  append([]ExamRevisionStarterWorkspaceEntry(nil), spec.StarterWorkspace...),
		PublishedByUserID: spec.PublishedByUserID, PublishedAt: TimeUTC(spec.PublishedAt),
		BaseRevisionID: spec.BaseRevisionID, Kind: spec.Kind,
		CandidateCorrection: spec.CandidateCorrection.Clone(),
	}
	slices.SortFunc(revision.StarterWorkspace, func(left, right ExamRevisionStarterWorkspaceEntry) int {
		if order := strings.Compare(left.Path, right.Path); order != 0 {
			return order
		}
		return strings.Compare(left.EntryID.String(), right.EntryID.String())
	})
	var err error
	revision.BrowserPolicyDigest, err = BrowserPolicyDigest(revision.BrowserPolicy)
	if err != nil {
		return nil, err
	}
	workspaceDigest, contentDigest, err := revision.computeDigests()
	if err != nil {
		return nil, err
	}
	revision.StarterWorkspaceDigest = workspaceDigest
	revision.ContentDigest = contentDigest
	if err = revision.Validate(); err != nil {
		return nil, err
	}
	return revision, nil
}

// NewLiveCorrectionExamRevision derives one immutable live correction from an
// already published Revision. Only candidate-visible instructions and the
// complete ordered resource manifest may change; policy and Starter Workspace
// remain pinned byte-for-byte to the base Revision.
type LiveCorrectionExamRevisionSpecification struct {
	ID                   ExamRevisionID
	Number               int64
	InstructionsMarkdown string
	Resources            []ExamRevisionResource
	// BrowserPolicy replaces the base Revision's policy; nil keeps it.
	BrowserPolicy           *BrowserPolicy
	CandidateSummary        string
	AffectedCapabilities    []CandidateCapability
	AcknowledgementRequired bool
	PublishedByUserID       UserID
	PublishedAt             time.Time
}

func NewLiveCorrectionExamRevision(base *ExamRevision, spec LiveCorrectionExamRevisionSpecification) (*ExamRevision, error) {
	if base == nil {
		return nil, invalidModelError("NewLiveCorrectionExamRevision", "exam_revision", "", "live correction requires a base Exam Revision", "")
	}
	if err := base.Validate(); err != nil || spec.Number <= base.Number {
		return nil, invalidModelError("NewLiveCorrectionExamRevision", "exam_revision", "", "invalid live correction base or number", "")
	}
	browserPolicy := base.BrowserPolicy.Clone()
	if spec.BrowserPolicy != nil {
		browserPolicy = spec.BrowserPolicy.Clone()
	}
	changedAreas, err := CandidateCorrectionChangedAreas(base, spec.InstructionsMarkdown, spec.Resources, browserPolicy)
	if err != nil {
		return nil, err
	}
	notice, err := NewCandidateCorrectionNotice(spec.CandidateSummary, changedAreas, spec.AffectedCapabilities, spec.AcknowledgementRequired)
	if err != nil {
		return nil, err
	}
	return NewExamRevision(ExamRevisionSpecification{
		ID:                   spec.ID,
		ExamID:               base.ExamID,
		Number:               spec.Number,
		SourceDraftRevision:  base.SourceDraftRevision,
		Title:                base.Title,
		InstructionsMarkdown: spec.InstructionsMarkdown,
		Policy:               cloneExamRevisionPolicy(base.Policy),
		BrowserPolicy:        browserPolicy,
		Capacity:             base.Capacity,
		Resources:            append([]ExamRevisionResource(nil), spec.Resources...),
		StarterWorkspace:     append([]ExamRevisionStarterWorkspaceEntry(nil), base.StarterWorkspace...),
		PublishedByUserID:    spec.PublishedByUserID,
		PublishedAt:          spec.PublishedAt,
		BaseRevisionID:       base.ID,
		Kind:                 ExamRevisionPublicationLiveCorrection,
		CandidateCorrection:  notice,
	})
}

func CandidateCorrectionChangedAreas(base *ExamRevision, instructionsMarkdown string, resources []ExamRevisionResource, browserPolicy BrowserPolicy) ([]ExamCorrectionChangedArea, error) {
	if base == nil || base.Validate() != nil {
		return nil, invalidModelError("CandidateCorrectionChangedAreas", "exam_revision", "", "invalid live correction base", "")
	}
	changedAreas := make([]ExamCorrectionChangedArea, 0, 3)
	if base.InstructionsMarkdown != instructionsMarkdown {
		changedAreas = append(changedAreas, ExamCorrectionChangedInstructions)
	}
	if !sameExamRevisionResources(base.Resources, resources) {
		changedAreas = append(changedAreas, ExamCorrectionChangedResources)
	}
	browserDigest, err := BrowserPolicyDigest(browserPolicy)
	if err != nil {
		return nil, err
	}
	if browserDigest != base.BrowserPolicyDigest {
		changedAreas = append(changedAreas, ExamCorrectionChangedBrowserPolicy)
	}
	slices.SortFunc(changedAreas, func(left, right ExamCorrectionChangedArea) int { return strings.Compare(string(left), string(right)) })
	return changedAreas, nil
}

func (r *ExamRevision) Validate() error {
	if r == nil || !r.ID.IsValid() || !r.ExamID.IsValid() || r.Number < 1 || r.SourceDraftRevision < 1 ||
		r.Title == "" || strings.TrimSpace(r.Title) != r.Title || !r.PublishedByUserID.IsValid() || r.PublishedAt.IsZero() ||
		!r.Kind.IsValid() || !r.BaseRevisionID.IsZero() && (!r.BaseRevisionID.IsValid() || r.BaseRevisionID == r.ID) {
		return invalidModelError("ExamRevision.Validate", "exam_revision", "", "invalid Exam Revision metadata", "")
	}
	if r.Kind == ExamRevisionPublicationLiveCorrection {
		if !r.BaseRevisionID.IsValid() || r.CandidateCorrection.Validate() != nil {
			return invalidModelError("ExamRevision.Validate", "exam_revision", "", "live correction Revision requires Candidate Correction Notice", "")
		}
	} else if r.CandidateCorrection != nil {
		return invalidModelError("ExamRevision.Validate", "exam_revision", "", "standard Revision cannot contain Candidate Correction Notice", "")
	}
	draft := &ExamDraft{ExamID: r.ExamID, Title: r.Title, InstructionsMarkdown: r.InstructionsMarkdown,
		Policy: DefaultExamPolicySet(), BrowserPolicy: r.BrowserPolicy.Clone(),
		UpdatedAt: r.PublishedAt, Revision: r.SourceDraftRevision}
	if err := draft.Validate(); err != nil {
		return invalidModelError("ExamRevision.Validate", "exam_revision", "", "invalid Exam Revision authored text", "").wrap(err)
	}
	if err := r.Policy.Validate(); err != nil || r.PolicyDigest != r.Policy.SHA256 {
		return invalidModelError("ExamRevision.Validate", "exam_revision", "", "invalid Exam Revision policy", "")
	}
	if err := r.Capacity.Validate(); err != nil {
		return invalidModelError("ExamRevision.Validate", "exam_revision", "", "invalid Exam Revision capacity policy", "")
	}
	browserDigest, err := BrowserPolicyDigest(r.BrowserPolicy)
	if err != nil || r.BrowserPolicyDigest != browserDigest {
		return invalidModelError("ExamRevision.Validate", "exam_revision", "", "invalid Exam Revision Browser Policy", "")
	}
	if err = validateExamRevisionResources(r.Resources, r.Capacity); err != nil {
		return err
	}
	if err = validateExamRevisionStarterWorkspace(r.StarterWorkspace, r.Capacity); err != nil {
		return err
	}
	workspaceDigest, contentDigest, err := r.computeDigests()
	if err != nil || r.StarterWorkspaceDigest != workspaceDigest || r.ContentDigest != contentDigest {
		return invalidModelError("ExamRevision.Validate", "exam_revision", "", "invalid Exam Revision digest", "")
	}
	return nil
}

func (r *ExamRevision) Clone() *ExamRevision {
	if r == nil {
		return nil
	}
	clone := *r
	clone.Policy = cloneExamRevisionPolicy(r.Policy)
	clone.BrowserPolicy = r.BrowserPolicy.Clone()
	clone.CandidateCorrection = r.CandidateCorrection.Clone()
	clone.Resources = append([]ExamRevisionResource(nil), r.Resources...)
	clone.StarterWorkspace = append([]ExamRevisionStarterWorkspaceEntry(nil), r.StarterWorkspace...)
	return &clone
}

// SameExamRevisionCandidatePresentation reports whether two immutable
// snapshots present exactly the same live-correctable material. Opaque storage
// generation identities are deliberately excluded: replacing bytes with the
// same verified media, size, and digest is not a candidate-visible change.
func SameExamRevisionCandidatePresentation(left, right *ExamRevision) bool {
	if left == nil || right == nil || left.InstructionsMarkdown != right.InstructionsMarkdown || left.Capacity != right.Capacity ||
		left.BrowserPolicyDigest != right.BrowserPolicyDigest || len(left.Resources) != len(right.Resources) {
		return false
	}
	for index := range left.Resources {
		l, r := left.Resources[index], right.Resources[index]
		if l.ResourceID != r.ResourceID || l.DisplayName != r.DisplayName || l.DescriptionMarkdown != r.DescriptionMarkdown ||
			l.Position != r.Position || l.MediaType != r.MediaType || l.SizeBytes != r.SizeBytes || l.SHA256 != r.SHA256 {
			return false
		}
	}
	return true
}

func sameExamRevisionResources(left, right []ExamRevisionResource) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		l, r := left[index], right[index]
		if l.ResourceID != r.ResourceID || l.DisplayName != r.DisplayName || l.DescriptionMarkdown != r.DescriptionMarkdown ||
			l.Position != r.Position || l.MediaType != r.MediaType || l.SizeBytes != r.SizeBytes || l.SHA256 != r.SHA256 {
			return false
		}
	}
	return true
}

func validateExamRevisionResources(resources []ExamRevisionResource, capacity ExamCapacityPolicy) error {
	if len(resources) > capacity.ResourceMaximumCount {
		return invalidModelError("validateExamRevisionResources", "exam_revision", "", "too many Exam Revision resources", "")
	}
	resourceIDs, entryIDs := map[ExamResourceID]struct{}{}, map[FileEntryID]struct{}{}
	for position, resource := range resources {
		if err := resource.Validate(); err != nil || resource.Position != position || resource.SizeBytes > capacity.ResourceMaximumBytes {
			return invalidModelError("validateExamRevisionResources", "exam_revision", "", "invalid ordered Exam Revision resources", "")
		}
		if _, exists := resourceIDs[resource.ResourceID]; exists {
			return invalidModelError("validateExamRevisionResources", "exam_revision", "", "duplicate Exam Revision resource", "")
		}
		if _, exists := entryIDs[resource.FileEntryID]; exists {
			return invalidModelError("validateExamRevisionResources", "exam_revision", "", "duplicate Exam Revision resource file", "")
		}
		resourceIDs[resource.ResourceID], entryIDs[resource.FileEntryID] = struct{}{}, struct{}{}
	}
	return nil
}

func validateExamRevisionStarterWorkspace(entries []ExamRevisionStarterWorkspaceEntry, capacity ExamCapacityPolicy) error {
	if len(entries) > capacity.WorkspaceMaximumEntries {
		return invalidModelError("validateExamRevisionStarterWorkspace", "exam_revision", "", "too many Exam Revision Starter Workspace entries", "")
	}
	paths := make(map[string]StarterWorkspaceEntryKind, len(entries))
	ids, objects := map[StarterWorkspaceEntryID]struct{}{}, map[StarterWorkspaceObjectID]struct{}{}
	var total int64
	previous := ""
	for index, entry := range entries {
		if err := entry.Validate(); err != nil || entry.SizeBytes > capacity.WorkspaceMaximumFileBytes || index > 0 && strings.Compare(previous, entry.Path) >= 0 {
			return invalidModelError("validateExamRevisionStarterWorkspace", "exam_revision", "", "invalid canonical Exam Revision Starter Workspace", "")
		}
		if _, exists := ids[entry.EntryID]; exists {
			return invalidModelError("validateExamRevisionStarterWorkspace", "exam_revision", "", "duplicate Exam Revision Starter Workspace entry", "")
		}
		if _, exists := paths[entry.Path]; exists {
			return invalidModelError("validateExamRevisionStarterWorkspace", "exam_revision", "", "duplicate Exam Revision Starter Workspace path", "")
		}
		if entry.Kind == StarterWorkspaceEntryFile {
			if _, exists := objects[entry.ObjectID]; exists {
				return invalidModelError("validateExamRevisionStarterWorkspace", "exam_revision", "", "duplicate Exam Revision Starter Workspace object", "")
			}
			objects[entry.ObjectID] = struct{}{}
			total += entry.SizeBytes
			if total > capacity.WorkspaceMaximumTotalBytes {
				return invalidModelError("validateExamRevisionStarterWorkspace", "exam_revision", "", "Exam Revision Starter Workspace is too large", "")
			}
		}
		ids[entry.EntryID] = struct{}{}
		paths[entry.Path] = entry.Kind
		previous = entry.Path
	}
	for path := range paths {
		for parent := parentWorkspacePath(path); parent != ""; parent = parentWorkspacePath(parent) {
			if paths[parent] != StarterWorkspaceEntryDirectory {
				return invalidModelError("validateExamRevisionStarterWorkspace", "exam_revision", "", "Exam Revision Starter Workspace parent is missing", "")
			}
		}
	}
	return nil
}

func parentWorkspacePath(path string) string {
	index := strings.LastIndexByte(path, '/')
	if index < 0 {
		return ""
	}
	return path[:index]
}

type examRevisionResourceWire struct {
	ResourceID          string                `json:"resource_id"`
	FileEntryID         string                `json:"file_entry_id"`
	FileRevisionID      string                `json:"file_revision_id"`
	RenditionID         string                `json:"rendition_id"`
	DisplayName         string                `json:"display_name"`
	DescriptionMarkdown string                `json:"description_markdown"`
	Position            int                   `json:"position"`
	MediaType           ExamResourceMediaType `json:"media_type"`
	SizeBytes           int64                 `json:"size_bytes"`
	SHA256              string                `json:"sha256"`
}

type examRevisionWorkspaceWire struct {
	EntryID        string                    `json:"entry_id"`
	Kind           StarterWorkspaceEntryKind `json:"kind"`
	Path           string                    `json:"path"`
	ObjectID       string                    `json:"object_id"`
	ContentVersion WorkspaceContentVersion   `json:"content_version"`
	MediaType      string                    `json:"media_type"`
	SizeBytes      int64                     `json:"size_bytes"`
	SHA256         string                    `json:"sha256"`
}

type candidateCorrectionNoticeWire struct {
	Summary                 string                      `json:"summary"`
	ChangedAreas            []ExamCorrectionChangedArea `json:"changed_areas"`
	AffectedCapabilities    []CandidateCapability       `json:"affected_capabilities"`
	AcknowledgementRequired bool                        `json:"acknowledgement_required"`
}

func (r *ExamRevision) computeDigests() (string, string, error) {
	browserPolicy, err := EncodeBrowserPolicy(r.BrowserPolicy)
	if err != nil {
		return "", "", err
	}
	resources := make([]examRevisionResourceWire, len(r.Resources))
	for index, resource := range r.Resources {
		resources[index] = examRevisionResourceWire{resource.ResourceID.String(), resource.FileEntryID.String(), resource.FileRevisionID.String(), resource.RenditionID.String(), resource.DisplayName, resource.DescriptionMarkdown, resource.Position, resource.MediaType, resource.SizeBytes, resource.SHA256}
	}
	workspace := make([]examRevisionWorkspaceWire, len(r.StarterWorkspace))
	for index, entry := range r.StarterWorkspace {
		workspace[index] = examRevisionWorkspaceWire{entry.EntryID.String(), entry.Kind, entry.Path, entry.ObjectID.String(), entry.ContentVersion, entry.MediaType, entry.SizeBytes, entry.SHA256}
	}
	workspaceBytes, err := json.Marshal(struct {
		SchemaVersion int                         `json:"schema_version"`
		Entries       []examRevisionWorkspaceWire `json:"entries"`
	}{ExamRevisionSnapshotSchemaVersion, workspace})
	if err != nil {
		return "", "", err
	}
	workspaceSum := sha256.Sum256(workspaceBytes)
	workspaceDigest := hex.EncodeToString(workspaceSum[:])
	contentBytes, err := json.Marshal(struct {
		SchemaVersion          int                            `json:"schema_version"`
		Title                  string                         `json:"title"`
		InstructionsMarkdown   string                         `json:"instructions_markdown"`
		PolicySchemaVersion    int                            `json:"policy_schema_version"`
		Policy                 json.RawMessage                `json:"policy"`
		BrowserPolicy          json.RawMessage                `json:"browser_policy"`
		BrowserPolicyDigest    string                         `json:"browser_policy_digest"`
		Capacity               ExamCapacityPolicy             `json:"capacity"`
		Resources              []examRevisionResourceWire     `json:"resources"`
		StarterWorkspaceDigest string                         `json:"starter_workspace_digest"`
		StarterWorkspace       []examRevisionWorkspaceWire    `json:"starter_workspace"`
		CandidateCorrection    *candidateCorrectionNoticeWire `json:"candidate_correction"`
	}{ExamRevisionSnapshotSchemaVersion, r.Title, r.InstructionsMarkdown, r.Policy.SchemaVersion,
		json.RawMessage(r.Policy.Bytes),
		json.RawMessage(browserPolicy), r.BrowserPolicyDigest, r.Capacity,
		resources, workspaceDigest, workspace, candidateCorrectionNoticeWireFromModel(r.CandidateCorrection)})
	if err != nil {
		return "", "", err
	}
	contentSum := sha256.Sum256(contentBytes)
	return workspaceDigest, hex.EncodeToString(contentSum[:]), nil
}

func candidateCorrectionNoticeWireFromModel(notice *CandidateCorrectionNotice) *candidateCorrectionNoticeWire {
	if notice == nil {
		return nil
	}
	return &candidateCorrectionNoticeWire{Summary: notice.Summary, ChangedAreas: slices.Clone(notice.ChangedAreas), AffectedCapabilities: slices.Clone(notice.AffectedCapabilities),
		AcknowledgementRequired: notice.AcknowledgementRequired}
}

func cloneExamRevisionPolicy(policy ExamRevisionPolicy) ExamRevisionPolicy {
	policy.Bytes = bytesClone(policy.Bytes)
	return policy
}

func bytesClone(value []byte) []byte { return append([]byte(nil), value...) }

func validLowerSHA256(value string) bool {
	if len(value) != sha256.Size*2 || strings.ToLower(value) != value {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}
