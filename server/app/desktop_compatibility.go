// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package app

import (
	"context"
	"errors"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"time"

	examattempt "github.com/sudosylabs/proctor/server/app/exam/attempt"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

// DesktopCompatibilityState is the closed public result of checking one
// declared Desktop build against this server and Institution policy.
type DesktopCompatibilityState string

const (
	DesktopCompatibilityCompatible         DesktopCompatibilityState = "compatible"
	DesktopCompatibilityUpdateRequired     DesktopCompatibilityState = "update_required"
	DesktopCompatibilityServerIncompatible DesktopCompatibilityState = "server_incompatible"
	DesktopCompatibilityUnsupportedTarget  DesktopCompatibilityState = "unsupported_target"
)

// DesktopCompatibilityQuery is untrusted release metadata supplied to ping,
// Desktop Session creation, and Attempt entry or re-entry.
type DesktopCompatibilityQuery struct {
	DesktopRelease   string
	DesktopBuildID   string
	Platform         string
	Architecture     string
	RealtimeProtocol int
}

// DesktopCompatibilityResult is safe public compatibility metadata. Catalog
// fingerprints and capability-matrix identities remain server-internal.
type DesktopCompatibilityResult struct {
	Availability            model.DesktopAvailability
	RetryAt                 model.OptionalTime
	Compatibility           DesktopCompatibilityState
	Reason                  string
	MinimumDesktopRelease   string
	MaximumDesktopRelease   string
	MinimumRealtimeProtocol int
	MaximumRealtimeProtocol int
	AdministratorMessage    string
	// PolicyRevision is an internal admission fence. Public transports must not
	// expose it; mutation boundaries carry it back to authoritative storage.
	PolicyRevision int64
}

type desktopBuildCatalog struct {
	entries []model.DesktopBuildTuple
}

type desktopCompatibilityInstitutionStore interface {
	GetSingleton(context.Context) (*model.Institution, error)
}

type desktopCompatibilityAuthorizer interface {
	Authorize(context.Context, Invocation, model.Action, model.Resource) error
}

type desktopCompatibilityService struct {
	policies                store.DesktopCompatibilityPolicyStore
	institutions            desktopCompatibilityInstitutionStore
	authorization           desktopCompatibilityAuthorizer
	audit                   mutationAuditor
	catalog                 *desktopBuildCatalog
	recentAuthenticationTTL time.Duration
	now                     func() time.Time
}

// ReplaceDesktopCompatibilityPolicyCommand completely replaces the
// Institution policy behind optimistic revision and idempotency fences.
type ReplaceDesktopCompatibilityPolicyCommand struct {
	ExpectedRevision int64
	Settings         model.DesktopCompatibilityPolicySettings
	IdempotencyKey   string
}

var validDesktopTargetSelector = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,31}$`)

func newDesktopBuildCatalog(entries []model.DesktopBuildTuple) (*desktopBuildCatalog, error) {
	cloned := slices.Clone(entries)
	seenBuildIDs := make(map[string]struct{}, len(cloned))
	for _, entry := range cloned {
		if err := entry.Validate(); err != nil {
			return nil, err
		}
		if _, exists := seenBuildIDs[entry.DesktopBuildID]; exists {
			return nil, errors.New("desktop build catalog contains a duplicate build ID")
		}
		seenBuildIDs[entry.DesktopBuildID] = struct{}{}
	}
	sort.Slice(cloned, func(left, right int) bool {
		first, second := cloned[left], cloned[right]
		if first.Platform != second.Platform {
			return first.Platform < second.Platform
		}
		if first.Architecture != second.Architecture {
			return first.Architecture < second.Architecture
		}
		comparison, _ := model.CompareDesktopReleases(first.DesktopRelease, second.DesktopRelease)
		if comparison != 0 {
			return comparison < 0
		}
		return first.DesktopBuildID < second.DesktopBuildID
	})
	return &desktopBuildCatalog{entries: cloned}, nil
}

func newDesktopCompatibilityService(
	policies store.DesktopCompatibilityPolicyStore,
	institutions desktopCompatibilityInstitutionStore,
	authorization desktopCompatibilityAuthorizer,
	audit mutationAuditor,
	builds []model.DesktopBuildTuple,
	recentAuthenticationTTL time.Duration,
	now func() time.Time,
) (*desktopCompatibilityService, error) {
	if policies == nil || institutions == nil || authorization == nil || audit == nil ||
		recentAuthenticationTTL <= 0 || now == nil {
		return nil, errors.New("desktop compatibility service dependencies are invalid")
	}
	catalog, err := newDesktopBuildCatalog(builds)
	if err != nil {
		return nil, err
	}
	return &desktopCompatibilityService{
		policies:                policies,
		institutions:            institutions,
		authorization:           authorization,
		audit:                   audit,
		catalog:                 catalog,
		recentAuthenticationTTL: recentAuthenticationTTL,
		now:                     now,
	}, nil
}

// EvaluateDesktopCompatibility checks untrusted build declarations against
// the current authoritative Institution policy and immutable server catalog.
func (a *App) EvaluateDesktopCompatibility(
	ctx context.Context,
	query DesktopCompatibilityQuery,
) (DesktopCompatibilityResult, error) {
	if a == nil || a.desktopCompatibility == nil {
		return DesktopCompatibilityResult{}, NewError("desktop_compatibility_policy.unavailable")
	}
	return a.desktopCompatibility.Evaluate(ctx, query)
}

// GetDesktopCompatibilityPolicy returns the current administrative policy.
func (a *App) GetDesktopCompatibilityPolicy(
	ctx context.Context,
	invocation Invocation,
) (*model.DesktopCompatibilityPolicy, error) {
	if a == nil || a.desktopCompatibility == nil {
		return nil, NewError("desktop_compatibility_policy.unavailable")
	}
	return a.desktopCompatibility.Read(ctx, invocation)
}

// ReplaceDesktopCompatibilityPolicy replaces the current policy.
func (a *App) ReplaceDesktopCompatibilityPolicy(
	ctx context.Context,
	invocation Invocation,
	command ReplaceDesktopCompatibilityPolicyCommand,
) (*model.DesktopCompatibilityPolicy, error) {
	if a == nil || a.desktopCompatibility == nil {
		return nil, NewError("desktop_compatibility_policy.unavailable")
	}
	return a.desktopCompatibility.Replace(ctx, invocation, command)
}

func (s *desktopCompatibilityService) Evaluate(
	ctx context.Context,
	query DesktopCompatibilityQuery,
) (DesktopCompatibilityResult, error) {
	if err := validateDesktopCompatibilityQuery(query); err != nil {
		return DesktopCompatibilityResult{}, err
	}
	policy, err := s.policies.Get(ctx)
	if err != nil || policy == nil {
		return DesktopCompatibilityResult{}, desktopCompatibilityPolicyError(err)
	}
	result, err := s.catalog.evaluate(policy, query)
	if err != nil {
		return DesktopCompatibilityResult{}, err
	}
	if result.RetryAt.Valid && !result.RetryAt.Time.After(model.TimeUTC(s.now())) {
		result.RetryAt = model.OptionalTime{}
	}
	result.PolicyRevision = policy.Revision
	return result, nil
}

// ResolveAttemptDesktopBuild returns the one catalog tuple whose immutable
// selectors established the authenticated Desktop Session. Institution policy
// may narrow the embedded catalog, so Attempt entry rechecks it at the time of
// admission without trusting request-body build metadata.
func (s *desktopCompatibilityService) ResolveAttemptDesktopBuild(
	ctx context.Context,
	principal model.Principal,
) (examattempt.DesktopBuildResolution, error) {
	if principal.Validate() != nil || !principal.HasRegisteredDesktopKey() {
		return examattempt.DesktopBuildResolution{}, errors.New("Desktop principal is invalid")
	}
	query := DesktopCompatibilityQuery{
		DesktopRelease: principal.DesktopRelease, DesktopBuildID: principal.DesktopBuildID,
		Platform: string(principal.DesktopPlatform), Architecture: string(principal.DesktopArchitecture),
		RealtimeProtocol: principal.DesktopRealtimeProtocol,
	}
	result, err := s.Evaluate(ctx, query)
	if err != nil || result.Availability != model.DesktopAvailabilityReady ||
		result.Compatibility != DesktopCompatibilityCompatible {
		return examattempt.DesktopBuildResolution{}, errors.New("Desktop build is not compatible")
	}
	for _, entry := range s.catalog.entries {
		if entry.DesktopRelease == query.DesktopRelease && entry.DesktopBuildID == query.DesktopBuildID &&
			entry.Platform == principal.DesktopPlatform && entry.Architecture == principal.DesktopArchitecture &&
			entry.RealtimeProtocol == query.RealtimeProtocol {
			return examattempt.DesktopBuildResolution{Build: entry, CompatibilityPolicyRevision: result.PolicyRevision}, nil
		}
	}
	return examattempt.DesktopBuildResolution{}, errors.New("Desktop build is missing from the verified catalog")
}

func (s *desktopCompatibilityService) Read(
	ctx context.Context,
	invocation Invocation,
) (*model.DesktopCompatibilityPolicy, error) {
	if _, _, err := s.authorize(ctx, invocation, model.ActionAccessPolicyView); err != nil {
		return nil, err
	}
	policy, err := s.policies.Get(ctx)
	if err != nil || policy == nil {
		return nil, desktopCompatibilityPolicyError(err)
	}
	return policy.Clone(), nil
}

func (s *desktopCompatibilityService) Replace(
	ctx context.Context,
	invocation Invocation,
	command ReplaceDesktopCompatibilityPolicyCommand,
) (*model.DesktopCompatibilityPolicy, error) {
	if err := requireStrongRecentSession(
		invocation.Principal(),
		s.now(),
		s.recentAuthenticationTTL,
	); err != nil {
		return nil, err
	}
	institution, resource, err := s.authorize(
		ctx,
		invocation,
		model.ActionDesktopCompatibilityPolicyManage,
	)
	if err != nil {
		return nil, err
	}
	if command.ExpectedRevision < 1 || command.Settings.Validate() != nil {
		return nil, NewError("desktop_compatibility_policy.invalid")
	}
	if command.IdempotencyKey == "" {
		return nil, NewError("idempotency.key_required")
	}
	idempotency, err := newCommandIdempotency(
		invocation,
		"desktop_compatibility_policy.replace.v1",
		command.IdempotencyKey,
		struct {
			ExpectedRevision int64                                    `json:"expected_revision"`
			Settings         model.DesktopCompatibilityPolicySettings `json:"settings"`
		}{
			ExpectedRevision: command.ExpectedRevision,
			Settings:         command.Settings,
		},
	)
	if err != nil {
		return nil, err
	}
	stored, err := runAuditedMutation(
		ctx,
		s.audit,
		mutationAttempt{
			Invocation: invocation,
			Action:     model.ActionDesktopCompatibilityPolicyManage,
			Resource:   resource,
			ScopeType:  model.RoleScopeInstitution,
			ScopeID:    institution.ID.String(),
			Operation:  "replace",
			Value: map[string]any{
				"expected_revision":         command.ExpectedRevision,
				"minimum_desktop_release":   command.Settings.MinimumDesktopRelease,
				"revoked_build_count":       len(command.Settings.RevokedDesktopBuildIDs),
				"administrator_message_set": command.Settings.AdministratorMessage != "",
				"availability":              command.Settings.Availability,
				"retry_at_set":              command.Settings.RetryAt.Valid,
			},
		},
		s.now,
		func(
			ctx context.Context,
			reference mutationAttemptReference,
		) (*store.DesktopCompatibilityPolicyReplacementResult, error) {
			return s.policies.Replace(
				ctx,
				&store.DesktopCompatibilityPolicyReplacement{
					ActorID:          invocation.Principal().UserID,
					ExpectedRevision: command.ExpectedRevision,
					Settings:         command.Settings,
					AuditEventID:     reference.ID,
					AuditAt:          reference.MutationAtMillis,
				},
				idempotency,
			)
		},
		desktopCompatibilityPolicyError,
	)
	if err != nil {
		return nil, err
	}
	if stored == nil || stored.Policy == nil || stored.Policy.Validate() != nil {
		return nil, NewError("desktop_compatibility_policy.unavailable")
	}
	return stored.Policy.Clone(), nil
}

func (s *desktopCompatibilityService) authorize(
	ctx context.Context,
	invocation Invocation,
	action model.Action,
) (*model.Institution, model.Resource, error) {
	institution, err := s.institutions.GetSingleton(ctx)
	if err != nil || institution == nil || !institution.ID.IsValid() {
		return nil, model.Resource{}, desktopCompatibilityPolicyError(err)
	}
	resource := model.Resource{Type: model.ResourceInstitution, ID: institution.ID.String()}
	if err := s.authorization.Authorize(ctx, invocation, action, resource); err != nil {
		return nil, model.Resource{}, err
	}
	return institution, resource, nil
}

func desktopCompatibilityPolicyError(err error) error {
	var revision *store.ErrDesktopCompatibilityPolicyRevisionConflict
	var conflict *store.ErrConflict
	switch {
	case errors.As(err, &revision):
		return NewError("desktop_compatibility_policy.revision_conflict").WithField(
			"current_revision",
			strconv.FormatInt(revision.CurrentRevision, 10),
		).Wrap(err)
	case errors.As(err, &conflict) && conflict.Constraint == "actor_not_system_administrator":
		return NewError("authorization.denied").Wrap(err)
	case idempotencyError(err) != nil:
		return idempotencyError(err)
	default:
		return NewError("desktop_compatibility_policy.unavailable").Wrap(err)
	}
}

func (catalog *desktopBuildCatalog) evaluate(
	policy *model.DesktopCompatibilityPolicy,
	query DesktopCompatibilityQuery,
) (DesktopCompatibilityResult, error) {
	if err := validateDesktopCompatibilityQuery(query); err != nil {
		return DesktopCompatibilityResult{}, err
	}
	if catalog == nil || policy == nil || policy.Validate() != nil {
		return DesktopCompatibilityResult{}, NewError("desktop_compatibility_policy.unavailable")
	}

	matchingTarget := make([]model.DesktopBuildTuple, 0, len(catalog.entries))
	for _, entry := range catalog.entries {
		if string(entry.Platform) == query.Platform && string(entry.Architecture) == query.Architecture {
			matchingTarget = append(matchingTarget, entry)
		}
	}
	if len(matchingTarget) == 0 {
		return DesktopCompatibilityResult{
			Availability:         policy.Availability,
			RetryAt:              policy.RetryAt,
			Compatibility:        DesktopCompatibilityUnsupportedTarget,
			Reason:               "unsupported_target",
			AdministratorMessage: policy.AdministratorMessage,
		}, nil
	}

	result := desktopCompatibilityBounds(policy, matchingTarget)
	result.AdministratorMessage = policy.AdministratorMessage
	minimumComparison, _ := model.CompareDesktopReleases(query.DesktopRelease, result.MinimumDesktopRelease)
	if minimumComparison < 0 {
		result.Compatibility = DesktopCompatibilityUpdateRequired
		result.Reason = "release_too_old"
		return result, nil
	}
	maximumComparison, _ := model.CompareDesktopReleases(query.DesktopRelease, result.MaximumDesktopRelease)
	if maximumComparison > 0 {
		result.Compatibility = DesktopCompatibilityServerIncompatible
		result.Reason = "release_too_new"
		return result, nil
	}
	if query.RealtimeProtocol < result.MinimumRealtimeProtocol {
		result.Compatibility = DesktopCompatibilityUpdateRequired
		result.Reason = "realtime_protocol_too_old"
		return result, nil
	}
	if query.RealtimeProtocol > result.MaximumRealtimeProtocol {
		result.Compatibility = DesktopCompatibilityServerIncompatible
		result.Reason = "realtime_protocol_too_new"
		return result, nil
	}
	if _, revoked := slices.BinarySearch(policy.RevokedDesktopBuildIDs, query.DesktopBuildID); revoked {
		result.Compatibility = DesktopCompatibilityUpdateRequired
		result.Reason = "build_revoked"
		return result, nil
	}
	for _, entry := range matchingTarget {
		isExactBuild := entry.DesktopBuildID == query.DesktopBuildID &&
			entry.DesktopRelease == query.DesktopRelease && entry.RealtimeProtocol == query.RealtimeProtocol
		if isExactBuild {
			result.Compatibility = DesktopCompatibilityCompatible
			result.Reason = "compatible"
			return result, nil
		}
	}
	result.Compatibility = DesktopCompatibilityServerIncompatible
	result.Reason = "build_unrecognized"
	return result, nil
}

func validateDesktopCompatibilityQuery(query DesktopCompatibilityQuery) error {
	selectorsValid := model.IsValidDesktopRelease(query.DesktopRelease) &&
		model.IsValidDesktopBuildID(query.DesktopBuildID) &&
		validDesktopTargetSelector.MatchString(query.Platform) &&
		validDesktopTargetSelector.MatchString(query.Architecture) &&
		query.RealtimeProtocol > 0
	if !selectorsValid {
		return NewError("request.invalid")
	}
	return nil
}

func desktopCompatibilityBounds(
	policy *model.DesktopCompatibilityPolicy,
	entries []model.DesktopBuildTuple,
) DesktopCompatibilityResult {
	minimumRelease := entries[0].DesktopRelease
	maximumRelease := entries[0].DesktopRelease
	minimumRealtime := entries[0].RealtimeProtocol
	maximumRealtime := entries[0].RealtimeProtocol
	for _, entry := range entries[1:] {
		if comparison, _ := model.CompareDesktopReleases(entry.DesktopRelease, minimumRelease); comparison < 0 {
			minimumRelease = entry.DesktopRelease
		}
		if comparison, _ := model.CompareDesktopReleases(entry.DesktopRelease, maximumRelease); comparison > 0 {
			maximumRelease = entry.DesktopRelease
		}
		minimumRealtime = min(minimumRealtime, entry.RealtimeProtocol)
		maximumRealtime = max(maximumRealtime, entry.RealtimeProtocol)
	}
	if policy.MinimumDesktopRelease != "" {
		if comparison, _ := model.CompareDesktopReleases(policy.MinimumDesktopRelease, minimumRelease); comparison > 0 {
			minimumRelease = policy.MinimumDesktopRelease
		}
	}
	return DesktopCompatibilityResult{
		Availability:            policy.Availability,
		RetryAt:                 policy.RetryAt,
		MinimumDesktopRelease:   minimumRelease,
		MaximumDesktopRelease:   maximumRelease,
		MinimumRealtimeProtocol: minimumRealtime,
		MaximumRealtimeProtocol: maximumRealtime,
	}
}
