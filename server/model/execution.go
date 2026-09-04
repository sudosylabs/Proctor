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
	ID        ExecutionGrantID
	AttemptID ExamAttemptID
	HostID    string
	Image     string
	Network   ExecutionNetwork
	State     ExecutionGrantState
	// AppliedSittingState is the last open/paused state whose host effect was
	// completed for this exact grant. The pending marker and exact-grant lease
	// keep same-grant Freeze/Thaw effects ordered across application nodes.
	AppliedSittingState    ExamSittingState
	AppliedSittingRevision int64
	LifecyclePending       bool
	PendingSittingState    ExamSittingState
	PendingSittingRevision int64
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
		grant.CreatedAt.IsZero() || grant.UpdatedAt.IsZero() || grant.UpdatedAt.Before(grant.CreatedAt) ||
		grant.Revision < 1 {
		return fmt.Errorf("model: invalid Execution Grant")
	}
	if grant.LifecyclePending {
		if (grant.PendingSittingState != ExamSittingOpen && grant.PendingSittingState != ExamSittingPaused) || grant.PendingSittingRevision < 1 {
			return fmt.Errorf("model: invalid pending Execution Grant lifecycle")
		}
	} else if grant.PendingSittingState != "" || grant.PendingSittingRevision != 0 {
		return fmt.Errorf("model: unexpected pending Execution Grant lifecycle")
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
