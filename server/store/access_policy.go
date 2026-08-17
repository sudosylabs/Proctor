// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package store

import (
	"context"
	"time"

	"github.com/sudosylabs/proctor/server/model"
)

type AccessProviderCapability struct {
	AutoProvision bool
}

// AccessDeploymentCapabilities is an ephemeral validated deployment snapshot.
// It deliberately contains no protocol, claim, SMTP, or credential material.
type AccessDeploymentCapabilities struct {
	Providers   map[string]AccessProviderCapability
	DurableMail bool
}

type AccessPolicyBlockerCode string

const (
	AccessPolicyBlockerProviderUnavailable          AccessPolicyBlockerCode = "provider_unavailable"
	AccessPolicyBlockerProviderAdmissionUnsupported AccessPolicyBlockerCode = "provider_admission_unsupported"
	AccessPolicyBlockerInvitationMailUnavailable    AccessPolicyBlockerCode = "invitation_delivery_unavailable"
	AccessPolicyBlockerLastAdministratorPath        AccessPolicyBlockerCode = "last_system_administrator_path"
)

// AccessPolicyBlocker contains only safe operator-facing facts. ProviderID is
// an already-public configured identifier; user identities are never exposed.
type AccessPolicyBlocker struct {
	Code       AccessPolicyBlockerCode
	ProviderID string
}

type AccessPolicySnapshot struct {
	Policy  *model.AccessPolicy
	History []*model.AccessPolicyTransition
}

type AccessPolicyPreflight struct {
	ExpectedRevision       int64
	Settings               model.AccessPolicySettings
	RevokeExistingSessions bool
	Capabilities           AccessDeploymentCapabilities
	CheckedAt              time.Time
}

type AccessPolicyReplacement struct {
	Preflight    AccessPolicyPreflight
	ActorID      model.UserID
	AuditEventID string
	AuditAt      int64
}

type AccessPolicyReplacementResult struct {
	Snapshot           *AccessPolicySnapshot
	Changed            bool
	Replayed           bool
	SessionRevocations []AccessPolicySessionRevocation
}

// AccessPolicySessionRevocation contains post-commit invalidation facts for
// one affected User. Raw credentials never leave persistence.
type AccessPolicySessionRevocation struct {
	UserID            model.UserID
	SessionIDs        []model.SessionID
	AccessTokenHashes []string
}

type AccessPolicyStore interface {
	Get(context.Context, int) (*AccessPolicySnapshot, error)
	Preflight(context.Context, *AccessPolicyPreflight) ([]AccessPolicyBlocker, error)
	Replace(context.Context, *AccessPolicyReplacement, *CommandIdempotency) (*AccessPolicyReplacementResult, error)
}

type ErrAccessPolicyRevisionConflict struct{ CurrentRevision int64 }

func (e *ErrAccessPolicyRevisionConflict) Error() string { return "access policy revision conflict" }

type ErrAccessPolicyBlocked struct{ Blockers []AccessPolicyBlocker }

func (e *ErrAccessPolicyBlocked) Error() string { return "access policy transition is blocked" }
