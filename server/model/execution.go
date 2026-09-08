// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package model

import (
	"fmt"
	"strings"
	"time"
)

const (
	ExecutionHostIDMaximumBytes = 64
	ExecutionImageMaximumBytes  = 255
)

type ExecutionNetwork string

const (
	ExecutionNetworkNone      ExecutionNetwork = "none"
	ExecutionNetworkAllowlist ExecutionNetwork = "allowlist"
)

// ExecutionControlState is the last durably requested state of an exact host
// occupancy. Its acknowledgement is separate from the requested state.
type ExecutionControlState string

const (
	ExecutionControlRunning ExecutionControlState = "running"
	ExecutionControlFrozen  ExecutionControlState = "frozen"
	ExecutionControlRevoked ExecutionControlState = "revoked"
)

// ExecutionFence contains no Participation or other product authority identifiers.
// Revisions are monotonic within one grant and cannot be reset by reconnection.
type ExecutionFence struct {
	GrantID          ExecutionGrantID
	EnvironmentEpoch string
	ControlRevision  int64
}

func (f ExecutionFence) Validate() error {
	if !f.GrantID.IsValid() || !ValidExecutionEnvironmentEpoch(f.EnvironmentEpoch) || f.ControlRevision < 1 || f.ControlRevision > 1<<53-1 {
		return fmt.Errorf("model: invalid Execution fence")
	}
	return nil
}

func ValidExecutionEnvironmentEpoch(value string) bool {
	if len(value) == 0 || len(value) > 128 {
		return false
	}
	for _, c := range value {
		if (c < 'a' || c > 'z') && (c < 'A' || c > 'Z') && (c < '0' || c > '9') && c != '-' && c != '_' {
			return false
		}
	}
	return true
}

type ExecutionGrantState string

const (
	ExecutionGrantReserved ExecutionGrantState = "reserved"
	ExecutionGrantReady    ExecutionGrantState = "ready"
	ExecutionGrantReleased ExecutionGrantState = "released"
)

// ExecutionGrant is the durable placement record. Host readiness and capacity
// remain transient execenv observations; only the chosen placement and cleanup
// progress are authoritative application state.
type ExecutionGrant struct {
	ProcessedHostSequence       int64
	EnvironmentEpoch            string
	ControlRevision             int64
	ControlAcknowledgedRevision int64
	DesiredControlState         ExecutionControlState
	// ControlAuthorityDigest binds an effect to its original durable gates. It
	// prevents a healthy/fault/healthy transition from accepting an old recovery.
	ControlAuthorityDigest string
	ID                     ExecutionGrantID
	AttemptID              ExamAttemptID
	HostID                 string
	Image                  string
	Network                ExecutionNetwork
	State                  ExecutionGrantState
	// AppliedSittingState is the last open/paused state whose host effect was
	// completed for this exact grant. The pending marker and exact-grant lease
	// keep same-grant Freeze/Thaw effects ordered across application nodes.
	AppliedSittingState    ExamSittingState
	AppliedSittingRevision int64
	LifecyclePending       bool
	PendingSittingState    ExamSittingState
	PendingSittingRevision int64
	// Workspace effects use the same exact-grant lease as lifecycle effects.
	// An unfinished effect is retired after recovery rather than replayed over
	// a running guest whose observation stream cannot be reset atomically.
	AppliedWorkspaceCursor int64
	WorkspacePending       bool
	PendingWorkspaceCursor int64
	CreatedAt              time.Time
	UpdatedAt              time.Time
	ReleasedAt             OptionalTime
	RevokedAt              OptionalTime
	Revision               int64
}

func (grant *ExecutionGrant) Validate() error {
	if grant == nil || !grant.ID.IsValid() || !grant.AttemptID.IsValid() ||
		!ValidExecutionHostID(grant.HostID) || !validExecutionImage(grant.Image) ||
		(grant.Network != ExecutionNetworkNone && grant.Network != ExecutionNetworkAllowlist) ||
		(grant.AppliedSittingState != ExamSittingOpen && grant.AppliedSittingState != ExamSittingPaused) ||
		grant.AppliedSittingRevision < 1 ||
		grant.ProcessedHostSequence < 0 || grant.ProcessedHostSequence > 1<<53-1 || grant.AppliedWorkspaceCursor < 0 || grant.PendingWorkspaceCursor < 0 ||
		grant.CreatedAt.IsZero() || grant.UpdatedAt.IsZero() || grant.UpdatedAt.Before(grant.CreatedAt) ||
		grant.Revision < 1 {
		return fmt.Errorf("model: invalid Execution Grant")
	}
	if grant.EnvironmentEpoch == "" {
		if grant.ProcessedHostSequence != 0 || grant.ControlRevision != 0 || grant.ControlAcknowledgedRevision != 0 || grant.DesiredControlState != "" || grant.ControlAuthorityDigest != "" {
			return fmt.Errorf("model: unbound Execution control")
		}
	} else {
		if grant.Fence().Validate() != nil || grant.ControlAcknowledgedRevision < 0 || grant.ControlAcknowledgedRevision > grant.ControlRevision ||
			(grant.DesiredControlState != ExecutionControlRunning && grant.DesiredControlState != ExecutionControlFrozen && grant.DesiredControlState != ExecutionControlRevoked) ||
			len(grant.ControlAuthorityDigest) != 64 || strings.Trim(grant.ControlAuthorityDigest, "0123456789abcdef") != "" {
			return fmt.Errorf("model: invalid Execution control")
		}
	}
	if grant.LifecyclePending {
		if (grant.PendingSittingState != ExamSittingOpen && grant.PendingSittingState != ExamSittingPaused) || grant.PendingSittingRevision < 1 {
			return fmt.Errorf("model: invalid pending Execution Grant lifecycle")
		}
	} else if grant.PendingSittingState != "" || grant.PendingSittingRevision != 0 {
		return fmt.Errorf("model: unexpected pending Execution Grant lifecycle")
	}
	if !grant.WorkspacePending && grant.PendingWorkspaceCursor != 0 {
		return fmt.Errorf("model: unexpected pending Execution Grant Workspace cursor")
	}
	if grant.WorkspacePending && (grant.LifecyclePending || grant.PendingWorkspaceCursor < grant.AppliedWorkspaceCursor) {
		return fmt.Errorf("model: invalid pending Execution Grant Workspace effect")
	}
	switch grant.State {
	case ExecutionGrantReserved, ExecutionGrantReady:
		if grant.ReleasedAt.Valid || grant.RevokedAt.Valid {
			return fmt.Errorf("model: active Execution Grant has terminal timestamps")
		}
	case ExecutionGrantReleased:
		if !grant.ReleasedAt.Valid || grant.ReleasedAt.Time.Before(grant.CreatedAt) ||
			(grant.RevokedAt.Valid && grant.RevokedAt.Time.Before(grant.ReleasedAt.Time)) {
			return fmt.Errorf("model: invalid released Execution Grant")
		}
	default:
		return fmt.Errorf("model: invalid Execution Grant state")
	}
	return nil
}

func ValidExecutionHostID(value string) bool {
	if len(value) == 0 || len(value) > ExecutionHostIDMaximumBytes {
		return false
	}
	for _, character := range value {
		if (character < 'a' || character > 'z') && (character < 'A' || character > 'Z') &&
			(character < '0' || character > '9') && character != '.' && character != '_' && character != '-' {
			return false
		}
	}
	return true
}

func validExecutionImage(value string) bool {
	return value != "" && strings.TrimSpace(value) == value && len(value) <= ExecutionImageMaximumBytes &&
		!strings.ContainsAny(value, "\x00\r\n")
}

func (grant *ExecutionGrant) Fence() ExecutionFence {
	return ExecutionFence{GrantID: grant.ID, EnvironmentEpoch: grant.EnvironmentEpoch, ControlRevision: grant.ControlRevision}
}
