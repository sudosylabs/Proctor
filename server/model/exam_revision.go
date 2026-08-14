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
	Resources            []ExamRevisionResource
	StarterWorkspace     []ExamRevisionStarterWorkspaceEntry
	PublishedByUserID    UserID
	PublishedAt          time.Time
	BaseRevisionID       ExamRevisionID
	Kind                 ExamRevisionPublicationKind
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
	Resources              []ExamRevisionResource
	StarterWorkspace       []ExamRevisionStarterWorkspaceEntry
	StarterWorkspaceDigest string
	ContentDigest          string
	PublishedByUserID      UserID
	PublishedAt            time.Time
	BaseRevisionID         ExamRevisionID
	Kind                   ExamRevisionPublicationKind
}

func NewExamRevision(spec ExamRevisionSpecification) (*ExamRevision, error) {
	revision := &ExamRevision{
		ID: spec.ID, ExamID: spec.ExamID, Number: spec.Number, SourceDraftRevision: spec.SourceDraftRevision,
		Title: spec.Title, InstructionsMarkdown: spec.InstructionsMarkdown,
		Policy: cloneExamRevisionPolicy(spec.Policy), PolicyDigest: spec.Policy.SHA256,
		Resources:         append([]ExamRevisionResource(nil), spec.Resources...),
		StarterWorkspace:  append([]ExamRevisionStarterWorkspaceEntry(nil), spec.StarterWorkspace...),
		PublishedByUserID: spec.PublishedByUserID, PublishedAt: TimeUTC(spec.PublishedAt),
		BaseRevisionID: spec.BaseRevisionID, Kind: spec.Kind,
	}
	slices.SortFunc(revision.StarterWorkspace, func(left, right ExamRevisionStarterWorkspaceEntry) int {
		if order := strings.Compare(left.Path, right.Path); order != 0 {
			return order
		}
		return strings.Compare(left.EntryID.String(), right.EntryID.String())
	})
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

func (revision *ExamRevision) Validate() error {
	if revision == nil || !revision.ID.IsValid() || !revision.ExamID.IsValid() || revision.Number < 1 || revision.SourceDraftRevision < 1 ||
		revision.Title == "" || strings.TrimSpace(revision.Title) != revision.Title || !revision.PublishedByUserID.IsValid() || revision.PublishedAt.IsZero() ||
		!revision.Kind.IsValid() || !revision.BaseRevisionID.IsZero() && (!revision.BaseRevisionID.IsValid() || revision.BaseRevisionID == revision.ID) {
		return errors.New("model: invalid Exam Revision metadata")
	}
	draft := &ExamDraft{ExamID: revision.ExamID, Title: revision.Title, InstructionsMarkdown: revision.InstructionsMarkdown,
		Policy: DefaultExamPolicySet(), UpdatedAt: revision.PublishedAt, Revision: revision.SourceDraftRevision}
	if err := draft.Validate(); err != nil {
		return fmt.Errorf("model: invalid Exam Revision authored text: %w", err)
	}
	if err := revision.Policy.Validate(); err != nil || revision.PolicyDigest != revision.Policy.SHA256 {
		return errors.New("model: invalid Exam Revision policy")
	}
	if err := validateExamRevisionResources(revision.Resources); err != nil {
		return err
	}
	if err := validateExamRevisionStarterWorkspace(revision.StarterWorkspace); err != nil {
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
	clone.Resources = append([]ExamRevisionResource(nil), revision.Resources...)
	clone.StarterWorkspace = append([]ExamRevisionStarterWorkspaceEntry(nil), revision.StarterWorkspace...)
	return &clone
}

func validateExamRevisionResources(resources []ExamRevisionResource) error {
	if len(resources) > ExamResourceMaximumCount {
		return errors.New("model: too many Exam Revision resources")
	}
	resourceIDs, entryIDs := map[ExamResourceID]struct{}{}, map[FileEntryID]struct{}{}
	for position, resource := range resources {
		if err := resource.Validate(); err != nil || resource.Position != position {
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

func validateExamRevisionStarterWorkspace(entries []ExamRevisionStarterWorkspaceEntry) error {
	if len(entries) > StarterWorkspaceMaximumEntries {
		return errors.New("model: too many Exam Revision Starter Workspace entries")
	}
	paths := make(map[string]StarterWorkspaceEntryKind, len(entries))
	ids, objects := map[StarterWorkspaceEntryID]struct{}{}, map[StarterWorkspaceObjectID]struct{}{}
	var total int64
	previous := ""
	for index, entry := range entries {
		if err := entry.Validate(); err != nil || index > 0 && strings.Compare(previous, entry.Path) >= 0 {
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
			if total > StarterWorkspaceMaximumTotalBytes {
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

func (revision *ExamRevision) computeDigests() (string, string, error) {
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
		SchemaVersion          int                         `json:"schema_version"`
		Title                  string                      `json:"title"`
		InstructionsMarkdown   string                      `json:"instructions_markdown"`
		PolicySchemaVersion    int                         `json:"policy_schema_version"`
		Policy                 json.RawMessage             `json:"policy"`
		Resources              []examRevisionResourceWire  `json:"resources"`
		StarterWorkspaceDigest string                      `json:"starter_workspace_digest"`
		StarterWorkspace       []examRevisionWorkspaceWire `json:"starter_workspace"`
	}{ExamRevisionSnapshotSchemaVersion, revision.Title, revision.InstructionsMarkdown, revision.Policy.SchemaVersion, json.RawMessage(revision.Policy.Bytes), resources, workspaceDigest, workspace})
	if err != nil {
		return "", "", err
	}
	contentSum := sha256.Sum256(contentBytes)
	return workspaceDigest, hex.EncodeToString(contentSum[:]), nil
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
