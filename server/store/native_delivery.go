// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package store

import "github.com/sudosylabs/proctor/server/model"

// NativeDeliveryAccess supplies current authenticated User/key ownership.
// Connection/continuity and generation are required while the stream is live;
// historical delivery uses the closed owner and a current Session of its key.
// No client chooses whether a stream is historical.
type NativeDeliveryAccess struct {
	Access          CandidateAttemptAccess
	StreamID        string
	ParticipationID model.AttemptParticipationID
	Generation      int64
}

type NativeDeliveryGapDeclaration struct {
	Access       NativeDeliveryAccess
	Declaration  model.DeclareDeliveryGaps
	AuditEventID string
	AuditAt      int64
}
type NativeDeliveryFinalDeclaration struct {
	Access       NativeDeliveryAccess
	Declaration  model.FinalDeliveryDeclaration
	AuditEventID string
	AuditAt      int64
}
type NativeDeliverySummaryUpdate struct {
	Access       NativeDeliveryAccess
	Summary      model.UnretainedDeliverySummary
	AuditEventID string
	AuditAt      int64
}

const (
	NativeDeliveryGapsOperation    = "exam.native_delivery.gaps.v1"
	NativeDeliverySummaryOperation = "exam.native_delivery.summary.v1"
	NativeDeliveryFinalOperation   = "exam.native_delivery.final.v1"
)

type NativeDeliveryTarget struct {
	Security     model.AdmittedSecurity
	MatrixDigest string
	SittingID    model.ExamSittingID
	ClassID      model.ClassID
}

type NativeDeliveryAppend struct {
	Access       NativeDeliveryAccess
	Batch        model.NativeSecurityBatch
	DesktopBuild model.DesktopBuildTuple
	AuditEventID string
	AuditAt      int64
}

const NativeDeliveryAppendOperation = "exam.native_delivery.append.v1"

// NativeDeliveryRefusal is returned after a quota latch and its audit commit.
// Callers must not attempt to fail that already completed audit.
type NativeDeliveryRefusal struct{ Reason string }

func (e *NativeDeliveryRefusal) Error() string { return "native delivery detail refused" }
