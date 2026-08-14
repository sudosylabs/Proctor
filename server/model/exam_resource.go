// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package model

import (
	"strings"
	"time"
	"unicode/utf8"
)

const (
	ExamResourceDisplayNameMaxRunes       = 255
	ExamResourceDescriptionMaxBytes       = 16 * 1024
	ExamResourceMaximumCount              = 10
	ExamResourceMaximumBytes        int64 = 10 << 20
)

type ExamResourceMediaType string

const (
	ExamResourceMediaPDF      ExamResourceMediaType = "application/pdf"
	ExamResourceMediaPNG      ExamResourceMediaType = "image/png"
	ExamResourceMediaJPEG     ExamResourceMediaType = "image/jpeg"
	ExamResourceMediaWebP     ExamResourceMediaType = "image/webp"
	ExamResourceMediaText     ExamResourceMediaType = "text/plain"
	ExamResourceMediaMarkdown ExamResourceMediaType = "text/markdown"
	ExamResourceMediaCSV      ExamResourceMediaType = "text/csv"
	ExamResourceMediaJSON     ExamResourceMediaType = "application/json"
)

func (m ExamResourceMediaType) IsValid() bool {
	switch m {
	case ExamResourceMediaPDF, ExamResourceMediaPNG, ExamResourceMediaJPEG, ExamResourceMediaWebP,
		ExamResourceMediaText, ExamResourceMediaMarkdown, ExamResourceMediaCSV, ExamResourceMediaJSON:
		return true
	default:
		return false
	}
}

// ExamResource is one ordered, protected supporting file selected by the
// mutable Exam Draft. FileEntryID is stable across content replacement while
// SelectedFileRevisionID pins the exact immutable bytes currently authored.
type ExamResource struct {
	ID                     ExamResourceID
	ExamID                 ExamID
	FileEntryID            FileEntryID
	SelectedFileRevisionID FileRevisionID
	DisplayName            string
	DescriptionMarkdown    string
	Position               int
	CreatedAt              time.Time
	UpdatedAt              time.Time
	ArchivedAt             OptionalTime
}

func NewExamResource(id ExamResourceID, examID ExamID, entryID FileEntryID, revisionID FileRevisionID, displayName, descriptionMarkdown string, position int, at time.Time) (*ExamResource, error) {
	resource := &ExamResource{ID: id, ExamID: examID, FileEntryID: entryID, SelectedFileRevisionID: revisionID,
		DisplayName: strings.TrimSpace(displayName), DescriptionMarkdown: descriptionMarkdown,
		Position: position, CreatedAt: TimeUTC(at), UpdatedAt: TimeUTC(at)}
	if err := resource.Validate(); err != nil {
		return nil, err
	}
	return resource, nil
}

func (r *ExamResource) Validate() error {
	const where = "ExamResource.Validate"
	if r == nil {
		return invalidModelError(where, "exam_resource", "value", "is required", "")
	}
	if !r.ID.IsValid() || !r.ExamID.IsValid() || !r.FileEntryID.IsValid() || !r.SelectedFileRevisionID.IsValid() {
		return invalidModelError(where, "exam_resource", "identity", "must contain valid identifiers", "")
	}
	details := "id=" + r.ID.String()
	if r.DisplayName == "" || !utf8.ValidString(r.DisplayName) || strings.TrimSpace(r.DisplayName) != r.DisplayName || utf8.RuneCountInString(r.DisplayName) > ExamResourceDisplayNameMaxRunes {
		return invalidModelError(where, "exam_resource", "display_name", "must contain 1 to 255 trimmed Unicode characters", details)
	}
	if !utf8.ValidString(r.DescriptionMarkdown) || len(r.DescriptionMarkdown) > ExamResourceDescriptionMaxBytes {
		return invalidModelError(where, "exam_resource", "description_markdown", "must be valid bounded UTF-8", details)
	}
	if r.Position < 0 || r.Position >= ExamResourceMaximumCount {
		return invalidModelError(where, "exam_resource", "position", "must be between 0 and 9", details)
	}
	if r.CreatedAt.IsZero() || r.UpdatedAt.IsZero() || r.UpdatedAt.Before(r.CreatedAt) || r.ArchivedAt.Valid && r.ArchivedAt.Time.Before(r.CreatedAt) {
		return invalidModelError(where, "exam_resource", "timestamps", "must be ordered and nonzero", details)
	}
	return nil
}

func (r *ExamResource) IsArchived() bool { return r != nil && r.ArchivedAt.Valid }

func (r *ExamResource) ApplyMetadata(displayName, descriptionMarkdown string, at time.Time) (bool, error) {
	if r == nil {
		return false, invalidModelError("ExamResource.ApplyMetadata", "exam_resource", "value", "is required", "")
	}
	candidate := *r
	candidate.DisplayName = strings.TrimSpace(displayName)
	candidate.DescriptionMarkdown = descriptionMarkdown
	if candidate.DisplayName == r.DisplayName && candidate.DescriptionMarkdown == r.DescriptionMarkdown {
		return false, nil
	}
	candidate.UpdatedAt = TimeUTC(at)
	if candidate.UpdatedAt.Before(r.UpdatedAt) {
		candidate.UpdatedAt = r.UpdatedAt
	}
	if err := candidate.Validate(); err != nil {
		return false, err
	}
	*r = candidate
	return true, nil
}

func (r *ExamResource) ReplaceContent(revisionID FileRevisionID, at time.Time) (bool, error) {
	if r == nil || !revisionID.IsValid() {
		return false, invalidModelError("ExamResource.ReplaceContent", "exam_resource", "selected_file_revision_id", "must be a valid identifier", "")
	}
	if r.SelectedFileRevisionID == revisionID {
		return false, nil
	}
	candidate := *r
	candidate.SelectedFileRevisionID = revisionID
	candidate.UpdatedAt = TimeUTC(at)
	if candidate.UpdatedAt.Before(r.UpdatedAt) {
		candidate.UpdatedAt = r.UpdatedAt
	}
	if err := candidate.Validate(); err != nil {
		return false, err
	}
	*r = candidate
	return true, nil
}

func (r *ExamResource) Archive(at time.Time) error {
	if r == nil || r.IsArchived() {
		return invalidModelError("ExamResource.Archive", "exam_resource", "archived_at", "cannot archive", "")
	}
	candidate := *r
	candidate.ArchivedAt = OptionalTimeFrom(at)
	candidate.UpdatedAt = TimeUTC(at)
	if candidate.UpdatedAt.Before(r.UpdatedAt) {
		candidate.UpdatedAt = r.UpdatedAt
		candidate.ArchivedAt = OptionalTimeFrom(r.UpdatedAt)
	}
	if err := candidate.Validate(); err != nil {
		return err
	}
	*r = candidate
	return nil
}
