// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package store

import (
	"context"
	"encoding/json"

	"github.com/sudosylabs/proctor/server/model"
)

const ExamExportCreateOperation = "exam.export.create.v1"

type ExamExportAccess struct {
	ExamRecordsMutation
	ExportID   model.ExamExportID
	Categories []model.RetentionCategory
}

type ExamExportCreation struct {
	ExamExportAccess
	JobID         model.JobID
	SubmissionIDs []model.SubmissionID
}

// ExamExportFile is private archive-construction input. Storage origins and
// object identities never enter the portable manifest or public HTTP response.
type ExamExportFile struct {
	SubmissionID model.SubmissionID
	Entry        model.ExamSubmissionManifestEntry
}

type ExamExportSnapshot struct {
	Export  *model.ExamExport
	Records json.RawMessage
	Files   []ExamExportFile
}

type ExamExportArtifact struct {
	ExportID  model.ExamExportID
	AttemptID model.JobAttemptID
}

type ExamExportBuild struct {
	ExamExportArtifact
	JobID      model.JobID
	ClaimToken model.JobClaimToken
}

type ExamExportPublication struct {
	ExamExportBuild
	SizeBytes int64
	SHA256    string
}

type ExamExportDownload struct {
	Export      *model.ExamExport
	Artifact    ExamExportArtifact
	Submissions []ExamSubmissionAuthorization
}

// ExamExportStore freezes an authorized exact scope/category snapshot and
// enqueues its finite Job with durable source protections in one transaction.
// It locks Policy before Exam, Sitting and ordered Submissions, matching the
// retirement fence; reads and replays recheck current credentials, manager
// relationship and both export and ordinary read authority. Integrity excludes
// the requester as candidate and also requires the dedicated Browser Activity
// permission and exact current Exam Manager membership, including
// when the general export action uses an override. Retained categories, never
// caller-provided read categories, determine that history check.
//
// A unique artifact is registered before each attempt writes. Publication is
// fenced by the live Job claim, source deadline and immutable request expiry.
// Only verified publication releases successful construction's source references.
// Failed/expired requests erase frozen payloads and release expired protections.
// Cleanup retains uncertain writer identities for repeated exact-key removal;
// an absence observation is not proof that a late remote write cannot complete.
type ExamExportStore interface {
	ListScope(context.Context, model.RetentionHoldScope) ([]ExamSubmissionAuthorization, error)
	// ListCreationScope resolves original captured ownership on exact retained
	// replay, or the bounded current scope for a new command. It never renews
	// the outcome or archive lifetime. Create repeats authority and exact-set
	// checks in its own atomic mutation; this projection grants no authority.
	ListCreationScope(context.Context, model.RetentionHoldScope, *CommandIdempotency) ([]ExamSubmissionAuthorization, error)
	Create(context.Context, *ExamExportCreation, *CommandIdempotency) (*model.ExamExport, error)
	Get(context.Context, *ExamExportAccess) (*ExamExportDownload, error)
	BeginBuild(context.Context, *ExamExportBuild) (*ExamExportSnapshot, error)
	Publish(context.Context, *ExamExportPublication) error
	FinishWriter(context.Context, ExamExportArtifact) error
	Reconcile(context.Context, int) (int, error)
	// BeginPurgeBatch atomically selects the oldest due eligible artifacts and
	// defers each selected key for one hour before I/O. Concurrent callers skip
	// the selected batch; failed or uncertain attempts preserve exact-key writer
	// references and do not record absence. The caller reserves its finite work
	// budget first, including when the result of this mutation is uncertain.
	BeginPurgeBatch(context.Context, int) ([]ExamExportArtifact, error)
	CompletePurge(context.Context, ExamExportArtifact) error
}
