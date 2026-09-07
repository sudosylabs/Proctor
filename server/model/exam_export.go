// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package model

import (
	"encoding/hex"
	"errors"
	"time"
)

const (
	ExamExportMaximumSubmissions           = 200
	ExamExportMaximumEntries               = 50_000
	ExamExportMaximumRecords               = 50_000
	ExamExportMaximumSourceBytes   int64   = 8 << 30
	ExamExportMaximumSnapshotBytes         = 64 << 20
	ExamExportMaximumArchiveBytes  int64   = ExamExportMaximumSourceBytes + 2*ExamExportMaximumSnapshotBytes
	ExamExportSourceLifetime               = 24 * time.Hour
	ExamExportMaximumAttempts              = 3
	JobTypeExamExportBuild         JobType = "exam_export.build"
	JobTypeExamExportCleanup       JobType = "exam_export.cleanup"
)

type ExamExportState string

const (
	ExamExportQueued  ExamExportState = "queued"
	ExamExportReady   ExamExportState = "ready"
	ExamExportFailed  ExamExportState = "failed"
	ExamExportExpired ExamExportState = "expired"
)

// ExamExport is an independent temporary archive of an exact category selection.
// Its advertised expiry starts at request creation and is never extended by
// construction, access, retries, preservation holds, or later policy changes.
type ExamExport struct {
	ID               ExamExportID
	Scope            RetentionHoldScope
	RequesterUserID  UserID
	Categories       []RetentionCategory
	State            ExamExportState
	PolicyRevision   int64
	CreatedAt        time.Time
	ExpiresAt        time.Time
	SourceExpiresAt  time.Time
	SubmissionCount  int
	FileCount        int
	SourceBytes      int64
	ArchiveSizeBytes int64
	ArchiveSHA256    string
	ReadyAt          OptionalTime
}

func ValidExamExportCategories(categories []RetentionCategory) bool {
	return len(categories) == 1 && (categories[0] == RetentionCategoryWork || categories[0] == RetentionCategoryIntegrity) ||
		len(categories) == 2 && categories[0] == RetentionCategoryWork && categories[1] == RetentionCategoryIntegrity
}

func (e *ExamExport) Validate() error {
	if e == nil || !e.ID.IsValid() || e.Scope.Validate() != nil || !e.Scope.SittingID.IsValid() || !e.RequesterUserID.IsValid() ||
		!ValidExamExportCategories(e.Categories) || e.PolicyRevision < 1 || e.CreatedAt.IsZero() ||
		!e.ExpiresAt.After(e.CreatedAt) || e.ExpiresAt.After(e.CreatedAt.Add(ExamExportMaximumDays*24*time.Hour)) ||
		!e.SourceExpiresAt.After(e.CreatedAt) || e.SourceExpiresAt.After(e.CreatedAt.Add(ExamExportSourceLifetime)) || e.SourceExpiresAt.After(e.ExpiresAt) ||
		e.SubmissionCount < 1 || e.SubmissionCount > ExamExportMaximumSubmissions || e.FileCount < 0 || e.FileCount > ExamExportMaximumEntries ||
		e.SourceBytes < 0 || e.SourceBytes > ExamExportMaximumSourceBytes {
		return errors.New("model: invalid Exam export")
	}
	switch e.State {
	case ExamExportQueued, ExamExportFailed, ExamExportExpired:
		if e.ArchiveSizeBytes != 0 || e.ArchiveSHA256 != "" || e.ReadyAt.Valid {
			return errors.New("model: unavailable Exam export contains archive metadata")
		}
	case ExamExportReady:
		if !e.ReadyAt.Valid || e.ReadyAt.Time.Before(e.CreatedAt) || !e.ReadyAt.Time.Before(e.ExpiresAt) ||
			!ValidExamExportContent(e.ArchiveSizeBytes, e.ArchiveSHA256) {
			return errors.New("model: invalid ready Exam export")
		}
	default:
		return errors.New("model: invalid Exam export state")
	}
	return nil
}

func ValidExamExportContent(size int64, digest string) bool {
	if size <= 0 || size > ExamExportMaximumArchiveBytes || len(digest) != 64 {
		return false
	}
	decoded, err := hex.DecodeString(digest)
	return err == nil && hex.EncodeToString(decoded) == digest
}
