// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package model

import (
	"errors"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

const (
	DesktopCompatibilityPolicyRevokedBuildMaxCount = 256
	DesktopCompatibilityPolicyMessageMaxRunes      = 500
	DesktopCompatibilityPolicyMessageMaxBytes      = 2 * 1024
	DesktopReleaseMaxBytes                         = 64
	DesktopBuildIDMaxBytes                         = 128
	DesktopCapabilityMatrixIdentityMaxBytes        = 128
)

var (
	ErrDesktopCompatibilityPolicyRevisionConflict = errors.New("desktop compatibility policy revision conflict")
	errDesktopCompatibilityPolicyInvalid          = errors.New("desktop compatibility policy is invalid")
	validDesktopBuildIdentity                     = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)
	validSHA256Fingerprint                        = regexp.MustCompile(`^[0-9a-f]{64}$`)
)

// DesktopPlatform is the closed platform vocabulary accepted from Proctor
// Desktop. It is compatibility metadata and never proof of the running binary.
type DesktopPlatform string

const (
	DesktopPlatformDarwin  DesktopPlatform = "darwin"
	DesktopPlatformWindows DesktopPlatform = "win32"
	DesktopPlatformLinux   DesktopPlatform = "linux"
)

// DesktopArchitecture is the closed executable-architecture vocabulary.
type DesktopArchitecture string

const (
	DesktopArchitectureARM64 DesktopArchitecture = "arm64"
	DesktopArchitectureX64   DesktopArchitecture = "x64"
)

// DesktopAvailability is the operator-controlled public availability state
// used by Desktop ping and authorization admission.
type DesktopAvailability string

const (
	DesktopAvailabilityReady       DesktopAvailability = "ready"
	DesktopAvailabilityMaintenance DesktopAvailability = "maintenance"
)

func (a DesktopAvailability) IsValid() bool {
	return a == DesktopAvailabilityReady || a == DesktopAvailabilityMaintenance
}

// DesktopCompatibilityPolicy is the Institution-owned revisioned policy that
// can narrow the build catalog embedded in this server release.
type DesktopCompatibilityPolicy struct {
	InstitutionID          InstitutionID
	Revision               int64
	MinimumDesktopRelease  string
	RevokedDesktopBuildIDs []string
	AdministratorMessage   string
	Availability           DesktopAvailability
	RetryAt                OptionalTime
	CreatedAt              time.Time
	UpdatedAt              time.Time
}

// DesktopCompatibilityPolicySettings is the complete replaceable policy.
type DesktopCompatibilityPolicySettings struct {
	MinimumDesktopRelease  string
	RevokedDesktopBuildIDs []string
	AdministratorMessage   string
	Availability           DesktopAvailability
	RetryAt                OptionalTime
}

// NewInitialDesktopCompatibilityPolicy returns the non-narrowing policy
// created atomically with the Institution during bootstrap.
func NewInitialDesktopCompatibilityPolicy(institutionID InstitutionID, at time.Time) *DesktopCompatibilityPolicy {
	at = TimeUTC(at)
	return &DesktopCompatibilityPolicy{
		InstitutionID:          institutionID,
		Revision:               1,
		RevokedDesktopBuildIDs: []string{},
		Availability:           DesktopAvailabilityReady,
		CreatedAt:              at,
		UpdatedAt:              at,
	}
}

// Validate checks persisted policy state without normalizing malformed data.
func (p *DesktopCompatibilityPolicy) Validate() error {
	if p == nil || !p.InstitutionID.IsValid() || p.Revision < 1 ||
		p.CreatedAt.IsZero() || p.UpdatedAt.IsZero() || p.UpdatedAt.Before(p.CreatedAt) ||
		(p.RetryAt.Valid && !p.RetryAt.Time.After(p.UpdatedAt)) {
		return errDesktopCompatibilityPolicyInvalid
	}
	return p.Settings().Validate()
}

// Settings returns a defensive copy of the complete replaceable policy.
func (p *DesktopCompatibilityPolicy) Settings() DesktopCompatibilityPolicySettings {
	if p == nil {
		return DesktopCompatibilityPolicySettings{
			RevokedDesktopBuildIDs: []string{},
			Availability:           DesktopAvailabilityReady,
		}
	}
	return DesktopCompatibilityPolicySettings{
		MinimumDesktopRelease:  p.MinimumDesktopRelease,
		RevokedDesktopBuildIDs: slices.Clone(p.RevokedDesktopBuildIDs),
		AdministratorMessage:   p.AdministratorMessage,
		Availability:           p.Availability,
		RetryAt:                p.RetryAt,
	}
}

// Validate checks policy input. Collections are canonical so fingerprints,
// idempotency documents, and persistence do not depend on caller ordering.
func (s DesktopCompatibilityPolicySettings) Validate() error {
	if !s.Availability.IsValid() || (s.Availability == DesktopAvailabilityReady && s.RetryAt.Valid) ||
		(s.RetryAt.Valid && s.RetryAt.Time.IsZero()) {
		return errDesktopCompatibilityPolicyInvalid
	}
	if s.MinimumDesktopRelease != "" && !IsValidDesktopRelease(s.MinimumDesktopRelease) {
		return errDesktopCompatibilityPolicyInvalid
	}
	if len(s.RevokedDesktopBuildIDs) > DesktopCompatibilityPolicyRevokedBuildMaxCount {
		return errDesktopCompatibilityPolicyInvalid
	}
	previous := ""
	for _, buildID := range s.RevokedDesktopBuildIDs {
		if !IsValidDesktopBuildID(buildID) || buildID <= previous {
			return errDesktopCompatibilityPolicyInvalid
		}
		previous = buildID
	}
	message := s.AdministratorMessage
	if message != strings.TrimSpace(message) || len(message) > DesktopCompatibilityPolicyMessageMaxBytes ||
		utf8.RuneCountInString(message) > DesktopCompatibilityPolicyMessageMaxRunes || !utf8.ValidString(message) {
		return errDesktopCompatibilityPolicyInvalid
	}
	for _, value := range message {
		if unicode.IsControl(value) {
			return errDesktopCompatibilityPolicyInvalid
		}
	}
	return nil
}

// Replace applies a complete optimistic replacement. An exact no-op preserves
// the current revision.
func (p *DesktopCompatibilityPolicy) Replace(
	expectedRevision int64,
	settings DesktopCompatibilityPolicySettings,
	at time.Time,
) error {
	if p == nil || p.Validate() != nil || settings.Validate() != nil || at.IsZero() {
		return errDesktopCompatibilityPolicyInvalid
	}
	if expectedRevision != p.Revision {
		return ErrDesktopCompatibilityPolicyRevisionConflict
	}
	at = TimeUTC(at)
	if at.Before(p.UpdatedAt) || settings.RetryAt.Valid && !settings.RetryAt.Time.After(at) {
		return errDesktopCompatibilityPolicyInvalid
	}
	if p.MinimumDesktopRelease == settings.MinimumDesktopRelease &&
		slices.Equal(p.RevokedDesktopBuildIDs, settings.RevokedDesktopBuildIDs) &&
		p.AdministratorMessage == settings.AdministratorMessage && p.Availability == settings.Availability &&
		p.RetryAt.Valid == settings.RetryAt.Valid && (!p.RetryAt.Valid || p.RetryAt.Time.Equal(settings.RetryAt.Time)) {
		return nil
	}
	p.MinimumDesktopRelease = settings.MinimumDesktopRelease
	p.RevokedDesktopBuildIDs = slices.Clone(settings.RevokedDesktopBuildIDs)
	p.AdministratorMessage = settings.AdministratorMessage
	p.Availability = settings.Availability
	p.RetryAt = settings.RetryAt.UTC()
	p.Revision++
	p.UpdatedAt = at
	return p.Validate()
}

// Clone returns a defensive policy copy.
func (p *DesktopCompatibilityPolicy) Clone() *DesktopCompatibilityPolicy {
	if p == nil {
		return nil
	}
	clone := *p
	clone.RevokedDesktopBuildIDs = slices.Clone(p.RevokedDesktopBuildIDs)
	return &clone
}

// Auditable returns bounded policy facts without the administrator message or
// revoked build identities.
func (p *DesktopCompatibilityPolicy) Auditable() map[string]any {
	if p == nil {
		return map[string]any{}
	}
	return map[string]any{
		"institution_id":            p.InstitutionID.String(),
		"revision":                  p.Revision,
		"minimum_desktop_release":   p.MinimumDesktopRelease,
		"revoked_build_count":       len(p.RevokedDesktopBuildIDs),
		"administrator_message_set": p.AdministratorMessage != "",
		"availability":              p.Availability,
		"retry_at_set":              p.RetryAt.Valid,
	}
}

// DesktopBuildTuple is one verified target and capability-matrix identity
// embedded into a server release. It is not supplied by Institution policy.
type DesktopBuildTuple struct {
	DesktopRelease                          string
	DesktopBuildID                          string
	Platform                                DesktopPlatform
	Architecture                            DesktopArchitecture
	RealtimeProtocol                        int
	AttemptConfigurationManifestFingerprint string
	DesktopSettingsRegistryFingerprint      string
	CapabilityMatrixIdentity                string
}

// Validate checks one immutable build-catalog entry.
func (t DesktopBuildTuple) Validate() error {
	if !IsValidDesktopRelease(t.DesktopRelease) || !IsValidDesktopBuildID(t.DesktopBuildID) ||
		!t.Platform.IsValid() || !t.Architecture.IsValid() || t.RealtimeProtocol < 1 ||
		!IsValidSHA256Fingerprint(t.AttemptConfigurationManifestFingerprint) ||
		!IsValidSHA256Fingerprint(t.DesktopSettingsRegistryFingerprint) ||
		!validBoundedDesktopIdentity(t.CapabilityMatrixIdentity, DesktopCapabilityMatrixIdentityMaxBytes) {
		return errors.New("desktop build tuple is invalid")
	}
	return nil
}

// IsValid reports whether the platform is recognized by this protocol.
func (p DesktopPlatform) IsValid() bool {
	return p == DesktopPlatformDarwin || p == DesktopPlatformWindows || p == DesktopPlatformLinux
}

// IsValid reports whether the architecture is recognized by this protocol.
func (a DesktopArchitecture) IsValid() bool {
	return a == DesktopArchitectureARM64 || a == DesktopArchitectureX64
}

// IsValidDesktopRelease reports whether value is canonical semantic-version
// text without the Go module convention's leading v.
func IsValidDesktopRelease(value string) bool {
	if value == "" || len(value) > DesktopReleaseMaxBytes || strings.HasPrefix(value, "v") {
		return false
	}
	_, valid := parseDesktopRelease(value)
	return valid
}

// CompareDesktopReleases compares two already-canonical Desktop releases.
func CompareDesktopReleases(left, right string) (int, error) {
	leftRelease, leftValid := parseDesktopRelease(left)
	rightRelease, rightValid := parseDesktopRelease(right)
	if !leftValid || !rightValid {
		return 0, errors.New("desktop release is invalid")
	}
	for index := range leftRelease.core {
		if comparison := compareSemanticNumericIdentifier(
			leftRelease.core[index],
			rightRelease.core[index],
		); comparison != 0 {
			return comparison, nil
		}
	}
	if len(leftRelease.prerelease) == 0 && len(rightRelease.prerelease) == 0 {
		return 0, nil
	}
	if len(leftRelease.prerelease) == 0 {
		return 1, nil
	}
	if len(rightRelease.prerelease) == 0 {
		return -1, nil
	}
	limit := min(len(leftRelease.prerelease), len(rightRelease.prerelease))
	for index := range limit {
		leftIdentifier := leftRelease.prerelease[index]
		rightIdentifier := rightRelease.prerelease[index]
		leftNumeric := isSemanticNumericIdentifier(leftIdentifier)
		rightNumeric := isSemanticNumericIdentifier(rightIdentifier)
		switch {
		case leftNumeric && rightNumeric:
			if comparison := compareSemanticNumericIdentifier(leftIdentifier, rightIdentifier); comparison != 0 {
				return comparison, nil
			}
		case leftNumeric:
			return -1, nil
		case rightNumeric:
			return 1, nil
		case leftIdentifier < rightIdentifier:
			return -1, nil
		case leftIdentifier > rightIdentifier:
			return 1, nil
		}
	}
	switch {
	case len(leftRelease.prerelease) < len(rightRelease.prerelease):
		return -1, nil
	case len(leftRelease.prerelease) > len(rightRelease.prerelease):
		return 1, nil
	default:
		return 0, nil
	}
}

// IsValidDesktopBuildID reports whether value is a bounded canonical build ID.
func IsValidDesktopBuildID(value string) bool {
	return validBoundedDesktopIdentity(value, DesktopBuildIDMaxBytes)
}

func validBoundedDesktopIdentity(value string, maximum int) bool {
	return value != "" && len(value) <= maximum && validDesktopBuildIdentity.MatchString(value)
}

type parsedDesktopRelease struct {
	core       [3]string
	prerelease []string
}

func parseDesktopRelease(value string) (parsedDesktopRelease, bool) {
	if value == "" || len(value) > DesktopReleaseMaxBytes || strings.HasPrefix(value, "v") {
		return parsedDesktopRelease{}, false
	}
	precedence, build, hasBuild := strings.Cut(value, "+")
	if hasBuild && (!validSemanticIdentifiers(build, true) || strings.Contains(build, "+")) {
		return parsedDesktopRelease{}, false
	}
	core, prerelease, hasPrerelease := strings.Cut(precedence, "-")
	if hasPrerelease && !validSemanticIdentifiers(prerelease, false) {
		return parsedDesktopRelease{}, false
	}
	coreIdentifiers := strings.Split(core, ".")
	if len(coreIdentifiers) != 3 {
		return parsedDesktopRelease{}, false
	}
	var parsed parsedDesktopRelease
	for index, identifier := range coreIdentifiers {
		if !isSemanticNumericIdentifier(identifier) || hasSemanticLeadingZero(identifier) {
			return parsedDesktopRelease{}, false
		}
		parsed.core[index] = identifier
	}
	if hasPrerelease {
		parsed.prerelease = strings.Split(prerelease, ".")
	}
	return parsed, true
}

func validSemanticIdentifiers(value string, build bool) bool {
	if value == "" {
		return false
	}
	for _, identifier := range strings.Split(value, ".") {
		if identifier == "" {
			return false
		}
		for index := range len(identifier) {
			character := identifier[index]
			if !((character >= '0' && character <= '9') ||
				(character >= 'A' && character <= 'Z') ||
				(character >= 'a' && character <= 'z') || character == '-') {
				return false
			}
		}
		if !build && isSemanticNumericIdentifier(identifier) && hasSemanticLeadingZero(identifier) {
			return false
		}
	}
	return true
}

func isSemanticNumericIdentifier(value string) bool {
	if value == "" {
		return false
	}
	for index := range len(value) {
		if value[index] < '0' || value[index] > '9' {
			return false
		}
	}
	return true
}

func hasSemanticLeadingZero(value string) bool {
	return len(value) > 1 && value[0] == '0'
}

func compareSemanticNumericIdentifier(left, right string) int {
	switch {
	case len(left) < len(right):
		return -1
	case len(left) > len(right):
		return 1
	case left < right:
		return -1
	case left > right:
		return 1
	default:
		return 0
	}
}

var _ Auditable = (*DesktopCompatibilityPolicy)(nil)
