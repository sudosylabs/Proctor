// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package model

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
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

func (kind ExamRevisionPublicationKind) IsValid() bool {
	return kind == ExamRevisionPublicationStandard || kind == ExamRevisionPublicationLiveCorrection
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

func (policy ExamRevisionPolicy) Validate() error {
	canonical, err := CanonicalizeExamRevisionPolicy(policy.Bytes)
	if err != nil || policy.SchemaVersion != canonical.SchemaVersion || policy.SHA256 != canonical.SHA256 || !slices.Equal(policy.Bytes, canonical.Bytes) {
		return errors.New("model: invalid canonical Exam Revision policy")
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

func (resource ExamRevisionResource) Validate() error {
	if !resource.ResourceID.IsValid() || !resource.FileEntryID.IsValid() || !resource.FileRevisionID.IsValid() || !resource.RenditionID.IsValid() {
		return errors.New("model: invalid Exam Revision resource identity")
	}
	probe, err := NewExamResource(resource.ResourceID, NewExamID(), resource.FileEntryID, resource.FileRevisionID, resource.DisplayName, resource.DescriptionMarkdown, resource.Position, time.Unix(1, 0).UTC())
	if err != nil || probe.DisplayName != resource.DisplayName || !resource.MediaType.IsValid() || resource.SizeBytes < 0 || resource.SizeBytes > ExamResourceMaximumBytes || !validLowerSHA256(resource.SHA256) {
		return errors.New("model: invalid Exam Revision resource snapshot")
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

func (entry ExamRevisionStarterWorkspaceEntry) Validate() error {
	if !entry.EntryID.IsValid() {
		return errors.New("model: invalid Exam Revision Starter Workspace identity")
	}
	path, err := NormalizeStarterWorkspacePath(entry.Path)
	if err != nil || path != entry.Path {
		return errors.New("model: invalid Exam Revision Starter Workspace path")
	}
	switch entry.Kind {
	case StarterWorkspaceEntryDirectory:
		if !entry.ObjectID.IsZero() || !entry.ContentVersion.IsZero() || entry.MediaType != "" || entry.SizeBytes != 0 || entry.SHA256 != "" {
			return errors.New("model: Exam Revision directory cannot carry content")
		}
	case StarterWorkspaceEntryFile:
		if !entry.ObjectID.IsValid() || !entry.ContentVersion.IsValid() || entry.MediaType == "" || strings.TrimSpace(entry.MediaType) != entry.MediaType || len(entry.MediaType) > 255 || entry.SizeBytes < 0 || entry.SizeBytes > StarterWorkspaceMaximumFileBytes || !validLowerSHA256(entry.SHA256) {
			return errors.New("model: invalid Exam Revision Starter Workspace file")
		}
	default:
		return errors.New("model: invalid Exam Revision Starter Workspace kind")
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
	ExecutionProfile     ExecutionProfile
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
	ExecutionProfile       ExecutionProfile
	ExecutionProfileDigest string
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
	profile := spec.ExecutionProfile
	if !profile.Enabled && profile.Image == "" && profile.Network == "" {
		profile = DefaultExecutionProfile()
	}
	revision := &ExamRevision{
		ID: spec.ID, ExamID: spec.ExamID, Number: spec.Number, SourceDraftRevision: spec.SourceDraftRevision,
		Title: spec.Title, InstructionsMarkdown: spec.InstructionsMarkdown,
		Policy: cloneExamRevisionPolicy(spec.Policy), PolicyDigest: spec.Policy.SHA256,
		ExecutionProfile:  profile,
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
	revision.ExecutionProfileDigest, err = ExecutionProfileDigest(revision.ExecutionProfile)
	if err != nil {
		return nil, err
	}
	if revision.BrowserPolicy.SchemaVersion == 0 {
		revision.BrowserPolicy = DisabledBrowserPolicy()
	}
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
	ID                      ExamRevisionID
	Number                  int64
	InstructionsMarkdown    string
	Resources               []ExamRevisionResource
	BrowserPolicy           BrowserPolicy
	CandidateSummary        string
	AcknowledgementRequired bool
	PublishedByUserID       UserID
	PublishedAt             time.Time
}

func NewLiveCorrectionExamRevision(base *ExamRevision, spec LiveCorrectionExamRevisionSpecification) (*ExamRevision, error) {
	if base == nil {
		return nil, errors.New("model: live correction requires a base Exam Revision")
	}
	if err := base.Validate(); err != nil || spec.Number <= base.Number {
		return nil, errors.New("model: invalid live correction base or number")
	}
	browserPolicy := spec.BrowserPolicy.Clone()
	if browserPolicy.SchemaVersion == 0 {
		browserPolicy = base.BrowserPolicy.Clone()
	}
	changedAreas, err := CandidateCorrectionChangedAreas(base, spec.InstructionsMarkdown, spec.Resources, browserPolicy)
	if err != nil {
		return nil, err
	}
	notice, err := NewCandidateCorrectionNotice(spec.CandidateSummary, changedAreas, spec.AcknowledgementRequired)
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
		ExecutionProfile:     base.ExecutionProfile,
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
		return nil, errors.New("model: invalid live correction base")
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

func (revision *ExamRevision) Validate() error {
	if revision == nil || !revision.ID.IsValid() || !revision.ExamID.IsValid() || revision.Number < 1 || revision.SourceDraftRevision < 1 ||
		revision.Title == "" || strings.TrimSpace(revision.Title) != revision.Title || !revision.PublishedByUserID.IsValid() || revision.PublishedAt.IsZero() ||
		!revision.Kind.IsValid() || !revision.BaseRevisionID.IsZero() && (!revision.BaseRevisionID.IsValid() || revision.BaseRevisionID == revision.ID) {
		return errors.New("model: invalid Exam Revision metadata")
	}
	if revision.Kind == ExamRevisionPublicationLiveCorrection {
		if !revision.BaseRevisionID.IsValid() || revision.CandidateCorrection.Validate() != nil {
			return errors.New("model: live correction Revision requires Candidate Correction Notice")
		}
	} else if revision.CandidateCorrection != nil {
		return errors.New("model: standard Revision cannot contain Candidate Correction Notice")
	}
	draft := &ExamDraft{ExamID: revision.ExamID, Title: revision.Title, InstructionsMarkdown: revision.InstructionsMarkdown,
		Policy: DefaultExamPolicySet(), ExecutionProfile: revision.ExecutionProfile, BrowserPolicy: revision.BrowserPolicy.Clone(),
		UpdatedAt: revision.PublishedAt, Revision: revision.SourceDraftRevision}
	if err := draft.Validate(); err != nil {
		return fmt.Errorf("model: invalid Exam Revision authored text: %w", err)
	}
	if err := revision.Policy.Validate(); err != nil || revision.PolicyDigest != revision.Policy.SHA256 {
		return errors.New("model: invalid Exam Revision policy")
	}
	if err := revision.Capacity.Validate(); err != nil {
		return errors.New("model: invalid Exam Revision capacity policy")
	}
	profileDigest, err := ExecutionProfileDigest(revision.ExecutionProfile)
	if err != nil || revision.ExecutionProfileDigest != profileDigest {
		return errors.New("model: invalid Exam Revision Execution Profile")
	}
	browserDigest, err := BrowserPolicyDigest(revision.BrowserPolicy)
	if err != nil || revision.BrowserPolicyDigest != browserDigest {
		return errors.New("model: invalid Exam Revision Browser Policy")
	}
	if err := validateExamRevisionResources(revision.Resources, revision.Capacity); err != nil {
		return err
	}
	if err := validateExamRevisionStarterWorkspace(revision.StarterWorkspace, revision.Capacity); err != nil {
		return err
	}
	workspaceDigest, contentDigest, err := revision.computeDigests()
	if err != nil || revision.StarterWorkspaceDigest != workspaceDigest || revision.ContentDigest != contentDigest {
		return errors.New("model: invalid Exam Revision digest")
	}
	return nil
}

func (revision *ExamRevision) Clone() *ExamRevision {
	if revision == nil {
		return nil
	}
	clone := *revision
	clone.Policy = cloneExamRevisionPolicy(revision.Policy)
	clone.BrowserPolicy = revision.BrowserPolicy.Clone()
	clone.CandidateCorrection = revision.CandidateCorrection.Clone()
	clone.Resources = append([]ExamRevisionResource(nil), revision.Resources...)
	clone.StarterWorkspace = append([]ExamRevisionStarterWorkspaceEntry(nil), revision.StarterWorkspace...)
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
		return errors.New("model: too many Exam Revision resources")
	}
	resourceIDs, entryIDs := map[ExamResourceID]struct{}{}, map[FileEntryID]struct{}{}
	for position, resource := range resources {
		if err := resource.Validate(); err != nil || resource.Position != position || resource.SizeBytes > capacity.ResourceMaximumBytes {
			return errors.New("model: invalid ordered Exam Revision resources")
		}
		if _, exists := resourceIDs[resource.ResourceID]; exists {
			return errors.New("model: duplicate Exam Revision resource")
		}
		if _, exists := entryIDs[resource.FileEntryID]; exists {
			return errors.New("model: duplicate Exam Revision resource file")
		}
		resourceIDs[resource.ResourceID], entryIDs[resource.FileEntryID] = struct{}{}, struct{}{}
	}
	return nil
}

func validateExamRevisionStarterWorkspace(entries []ExamRevisionStarterWorkspaceEntry, capacity ExamCapacityPolicy) error {
	if len(entries) > capacity.WorkspaceMaximumEntries {
		return errors.New("model: too many Exam Revision Starter Workspace entries")
	}
	paths := make(map[string]StarterWorkspaceEntryKind, len(entries))
	ids, objects := map[StarterWorkspaceEntryID]struct{}{}, map[StarterWorkspaceObjectID]struct{}{}
	var total int64
	previous := ""
	for index, entry := range entries {
		if err := entry.Validate(); err != nil || entry.SizeBytes > capacity.WorkspaceMaximumFileBytes || index > 0 && strings.Compare(previous, entry.Path) >= 0 {
			return errors.New("model: invalid canonical Exam Revision Starter Workspace")
		}
		if _, exists := ids[entry.EntryID]; exists {
			return errors.New("model: duplicate Exam Revision Starter Workspace entry")
		}
		if _, exists := paths[entry.Path]; exists {
			return errors.New("model: duplicate Exam Revision Starter Workspace path")
		}
		if entry.Kind == StarterWorkspaceEntryFile {
			if _, exists := objects[entry.ObjectID]; exists {
				return errors.New("model: duplicate Exam Revision Starter Workspace object")
			}
			objects[entry.ObjectID] = struct{}{}
			total += entry.SizeBytes
			if total > capacity.WorkspaceMaximumTotalBytes {
				return errors.New("model: Exam Revision Starter Workspace is too large")
			}
		}
		ids[entry.EntryID] = struct{}{}
		paths[entry.Path] = entry.Kind
		previous = entry.Path
	}
	for path := range paths {
		for parent := parentWorkspacePath(path); parent != ""; parent = parentWorkspacePath(parent) {
			if paths[parent] != StarterWorkspaceEntryDirectory {
				return errors.New("model: Exam Revision Starter Workspace parent is missing")
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
	AcknowledgementRequired bool                        `json:"acknowledgement_required"`
}

func (revision *ExamRevision) computeDigests() (string, string, error) {
	profile, err := EncodeExecutionProfile(revision.ExecutionProfile)
	if err != nil {
		return "", "", err
	}
	browserPolicy, err := EncodeBrowserPolicy(revision.BrowserPolicy)
	if err != nil {
		return "", "", err
	}
	resources := make([]examRevisionResourceWire, len(revision.Resources))
	for index, resource := range revision.Resources {
		resources[index] = examRevisionResourceWire{resource.ResourceID.String(), resource.FileEntryID.String(), resource.FileRevisionID.String(), resource.RenditionID.String(), resource.DisplayName, resource.DescriptionMarkdown, resource.Position, resource.MediaType, resource.SizeBytes, resource.SHA256}
	}
	workspace := make([]examRevisionWorkspaceWire, len(revision.StarterWorkspace))
	for index, entry := range revision.StarterWorkspace {
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
		ExecutionProfile       json.RawMessage                `json:"execution_profile"`
		ExecutionProfileDigest string                         `json:"execution_profile_digest"`
		BrowserPolicy          json.RawMessage                `json:"browser_policy"`
		BrowserPolicyDigest    string                         `json:"browser_policy_digest"`
		Capacity               ExamCapacityPolicy             `json:"capacity"`
		Resources              []examRevisionResourceWire     `json:"resources"`
		StarterWorkspaceDigest string                         `json:"starter_workspace_digest"`
		StarterWorkspace       []examRevisionWorkspaceWire    `json:"starter_workspace"`
		CandidateCorrection    *candidateCorrectionNoticeWire `json:"candidate_correction"`
	}{ExamRevisionSnapshotSchemaVersion, revision.Title, revision.InstructionsMarkdown, revision.Policy.SchemaVersion,
		json.RawMessage(revision.Policy.Bytes), json.RawMessage(profile), revision.ExecutionProfileDigest,
		json.RawMessage(browserPolicy), revision.BrowserPolicyDigest, revision.Capacity,
		resources, workspaceDigest, workspace, candidateCorrectionNoticeWireFromModel(revision.CandidateCorrection)})
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
	return &candidateCorrectionNoticeWire{Summary: notice.Summary, ChangedAreas: slices.Clone(notice.ChangedAreas),
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
