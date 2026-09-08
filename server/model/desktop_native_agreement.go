// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package model

import (
	"errors"
	"slices"
)

const NativeCoverageClaimLimit = 64

type NativeSourceID string

const (
	NativeSourceDisplay        NativeSourceID = "display-topology"
	NativeSourceWindow         NativeSourceID = "candidate-window"
	NativeSourceCapture        NativeSourceID = "capture-session"
	NativeSourceProcess        NativeSourceID = "process-application"
	NativeSourceSession        NativeSourceID = "interactive-session"
	NativeSourceClipboard      NativeSourceID = "clipboard"
	NativeSourcePrint          NativeSourceID = "print-spooler"
	NativeSourceStorage        NativeSourceID = "removable-storage"
	NativeSourceVirtualization NativeSourceID = "virtualization"
	NativeSourceNetwork        NativeSourceID = "network-configuration"
	NativeSourceMedia          NativeSourceID = "media-device"
)

func NativeSources() []NativeSourceID {
	return []NativeSourceID{NativeSourceDisplay, NativeSourceWindow, NativeSourceCapture, NativeSourceProcess,
		NativeSourceSession, NativeSourceClipboard, NativeSourcePrint, NativeSourceStorage, NativeSourceVirtualization, NativeSourceNetwork, NativeSourceMedia}
}

func (s NativeSourceID) IsValid() bool { return slices.Contains(NativeSources(), s) }

type NativeCapabilityClaim string

const (
	NativeClaimObserve     NativeCapabilityClaim = "observe"
	NativeClaimEnforce     NativeCapabilityClaim = "enforce"
	NativeClaimUnavailable NativeCapabilityClaim = "unavailable"
)

// NativeCoverageDefinition is release-admitted registry meaning, not a client
// claim. Effect-only requirements have no source but still carry their exact
// schema identity and must be certified by the capability matrix.
type NativeCoverageDefinition struct {
	CoverageKey         string
	CapabilityID        string
	SourceID            NativeSourceID
	SourceSchemaDigest  string
	PermittedClaims     []NativeCapabilityClaim
	RequiredPermissions []string
	Baseline            bool
}

// NativeCapabilityMatrixEntry retains the exact signed release claim.
type NativeCapabilityMatrixEntry struct {
	CoverageKey         string                `json:"coverage_key"`
	SourceSchemaDigest  string                `json:"source_schema_digest"`
	Claim               NativeCapabilityClaim `json:"claim"`
	ComponentID         string                `json:"component_id"`
	AdapterVersion      string                `json:"adapter_version"`
	HelperBuildID       string                `json:"helper_build_id,omitempty"`
	RequiredPermissions []string              `json:"required_permissions"`
	Limitations         []string              `json:"limitations"`
	Verification        string                `json:"verification"`
}

type NativeCapabilityMatrix struct {
	RegistryDigest string                        `json:"registry_digest"`
	MatrixID       string                        `json:"matrix_id"`
	ReleaseID      string                        `json:"release_id"`
	TargetTuple    string                        `json:"target_tuple"`
	Entries        []NativeCapabilityMatrixEntry `json:"entries"`
}

// NativeDetectorDefinition is a finite release-owned interpretation catalog.
// Exact condition IDs permit deterministic conditions without accepting prose,
// executable regular expressions, raw inventories or an arbitrary detail bag.
type NativeDetectorDefinition struct {
	DetectorID   string                  `json:"detector_id"`
	Version      int64                   `json:"version"`
	CapabilityID string                  `json:"capability_id"`
	ConditionIDs []string                `json:"condition_ids"`
	SourceIDs    []NativeSourceID        `json:"source_ids"`
	AllowedModes []NativeCapabilityClaim `json:"allowed_modes"`
}

// DesktopNativeAgreement is immutable verified-release meaning. Signature and
// exact artifact-byte verification belong to the packaging adapter. Construction
// validates consistency; it never asserts OS attestation or a running source.
type DesktopNativeAgreement struct {
	registryDigest        string
	sourceManifestDigest  string
	matrixDigest          string
	detectorCatalogDigest string
	releaseID             string
	targetTuple           string
	definitions           []NativeCoverageDefinition
	matrix                NativeCapabilityMatrix
	detectors             []NativeDetectorDefinition
}

var errNativeAgreement = errors.New("desktop native agreement is invalid")

func NewDesktopNativeAgreement(registryDigest, sourceManifestDigest, matrixDigest, detectorCatalogDigest string,
	definitions []NativeCoverageDefinition, matrix NativeCapabilityMatrix, detectors []NativeDetectorDefinition,
) (*DesktopNativeAgreement, error) {
	if !IsValidSHA256Fingerprint(registryDigest) || !IsValidSHA256Fingerprint(sourceManifestDigest) ||
		!IsValidSHA256Fingerprint(matrixDigest) || !IsValidSHA256Fingerprint(detectorCatalogDigest) || matrix.RegistryDigest != registryDigest ||
		!IsValidAgreementID(matrix.MatrixID) || !IsValidAgreementID(matrix.ReleaseID) || !IsValidAgreementID(matrix.TargetTuple) ||
		len(definitions) == 0 || len(definitions) > NativeCoverageClaimLimit || len(matrix.Entries) != len(definitions) ||
		detectors == nil || len(detectors) > 256 {
		return nil, errNativeAgreement
	}
	previous := ""
	baselineSources := map[NativeSourceID]bool{}
	for index, definition := range definitions {
		entry := matrix.Entries[index]
		if !IsValidAgreementID(definition.CoverageKey) || definition.CoverageKey <= previous || !IsValidAgreementID(definition.CapabilityID) ||
			!IsValidSHA256Fingerprint(definition.SourceSchemaDigest) || definition.SourceID != "" && !definition.SourceID.IsValid() ||
			definition.PermittedClaims == nil || len(definition.PermittedClaims) > 2 || entry.CoverageKey != definition.CoverageKey ||
			entry.SourceSchemaDigest != definition.SourceSchemaDigest || !IsValidAgreementID(entry.ComponentID) || !IsValidAgreementID(entry.AdapterVersion) ||
			entry.HelperBuildID != "" && !IsValidAgreementID(entry.HelperBuildID) || validateNativePermissions(entry.RequiredPermissions) != nil ||
			validateNativePermissions(definition.RequiredPermissions) != nil || !slices.Equal(entry.RequiredPermissions, definition.RequiredPermissions) ||
			validateNativeLimitations(entry.Limitations) != nil {
			return nil, errNativeAgreement
		}
		previous = definition.CoverageKey
		for i, claim := range definition.PermittedClaims {
			if claim != NativeClaimObserve && claim != NativeClaimEnforce || slices.Contains(definition.PermittedClaims[:i], claim) {
				return nil, errNativeAgreement
			}
		}
		if entry.Claim == NativeClaimUnavailable {
			if entry.Verification != "unavailable" {
				return nil, errNativeAgreement
			}
		} else if (entry.Claim != NativeClaimObserve && entry.Claim != NativeClaimEnforce) || entry.Verification != "passed" ||
			!slices.Contains(definition.PermittedClaims, entry.Claim) {
			return nil, errNativeAgreement
		}
		if definition.Baseline {
			if entry.Claim != NativeClaimEnforce || entry.Verification != "passed" {
				return nil, errNativeAgreement
			}
			if definition.SourceID != "" {
				baselineSources[definition.SourceID] = true
			}
		}
	}
	// Mandatory baseline source certification is independent of optional families.
	for _, source := range []NativeSourceID{NativeSourceDisplay, NativeSourceWindow, NativeSourceCapture} {
		if !baselineSources[source] {
			return nil, errNativeAgreement
		}
	}
	previous = ""
	var version int64
	for _, detector := range detectors {
		if !IsValidAgreementID(detector.DetectorID) || detector.Version < 1 || detector.Version > 9007199254740991 ||
			detector.DetectorID < previous || detector.DetectorID == previous && detector.Version <= version ||
			!IsValidAgreementID(detector.CapabilityID) || validateNativeConditionIDs(detector.ConditionIDs) != nil ||
			len(detector.SourceIDs) == 0 || len(detector.SourceIDs) > len(NativeSources()) || len(detector.AllowedModes) == 0 || len(detector.AllowedModes) > 2 {
			return nil, errNativeAgreement
		}
		previous, version = detector.DetectorID, detector.Version
		lastSource := -1
		for _, source := range detector.SourceIDs {
			order := slices.Index(NativeSources(), source)
			if order < 0 || order <= lastSource {
				return nil, errNativeAgreement
			}
			lastSource = order
			found := false
			for _, definition := range definitions {
				if definition.CapabilityID == detector.CapabilityID && definition.SourceID == source {
					found = true
				}
			}
			if !found {
				return nil, errNativeAgreement
			}
		}
		for i, mode := range detector.AllowedModes {
			if mode != NativeClaimObserve && mode != NativeClaimEnforce || slices.Contains(detector.AllowedModes[:i], mode) {
				return nil, errNativeAgreement
			}
			supported := false
			for _, definition := range definitions {
				if definition.CapabilityID == detector.CapabilityID && slices.Contains(definition.PermittedClaims, mode) {
					supported = true
				}
			}
			if !supported {
				return nil, errNativeAgreement
			}
		}
	}
	result := &DesktopNativeAgreement{registryDigest: registryDigest, sourceManifestDigest: sourceManifestDigest, matrixDigest: matrixDigest,
		detectorCatalogDigest: detectorCatalogDigest, releaseID: matrix.ReleaseID, targetTuple: matrix.TargetTuple}
	result.definitions = cloneNativeDefinitions(definitions)
	result.matrix = cloneNativeMatrix(matrix)
	result.detectors = cloneNativeDetectors(detectors)
	return result, nil
}

func validateNativePermissions(values []string) error {
	if values == nil || len(values) > 5 {
		return errNativeAgreement
	}
	for i, value := range values {
		if !slices.Contains([]string{"accessibility", "full_disk_access", "endpoint_security", "camera", "microphone"}, value) || slices.Contains(values[:i], value) {
			return errNativeAgreement
		}
	}
	return nil
}

func validateNativeLimitations(values []string) error {
	if values == nil || len(values) > 32 {
		return errNativeAgreement
	}
	for i, value := range values {
		if !IsValidAgreementID(value) || slices.Contains(values[:i], value) {
			return errNativeAgreement
		}
	}
	return nil
}
func validateNativeConditionIDs(values []string) error {
	if len(values) == 0 || len(values) > 256 {
		return errNativeAgreement
	}
	previous := ""
	for _, value := range values {
		if !IsValidAgreementID(value) || value <= previous {
			return errNativeAgreement
		}
		previous = value
	}
	return nil
}

func cloneNativeDefinitions(values []NativeCoverageDefinition) []NativeCoverageDefinition {
	result := slices.Clone(values)
	for i := range result {
		result[i].PermittedClaims = slices.Clone(result[i].PermittedClaims)
		result[i].RequiredPermissions = slices.Clone(result[i].RequiredPermissions)
	}
	return result
}
func cloneNativeMatrix(matrix NativeCapabilityMatrix) NativeCapabilityMatrix {
	matrix.Entries = slices.Clone(matrix.Entries)
	for i := range matrix.Entries {
		matrix.Entries[i].RequiredPermissions = slices.Clone(matrix.Entries[i].RequiredPermissions)
		matrix.Entries[i].Limitations = slices.Clone(matrix.Entries[i].Limitations)
	}
	return matrix
}
func cloneNativeDetectors(values []NativeDetectorDefinition) []NativeDetectorDefinition {
	result := slices.Clone(values)
	for i := range result {
		result[i].ConditionIDs = slices.Clone(result[i].ConditionIDs)
		result[i].SourceIDs = slices.Clone(result[i].SourceIDs)
		result[i].AllowedModes = slices.Clone(result[i].AllowedModes)
	}
	return result
}

func (a *DesktopNativeAgreement) RegistryDigest() string {
	if a == nil {
		return ""
	}
	return a.registryDigest
}
func (a *DesktopNativeAgreement) SourceManifestDigest() string {
	if a == nil {
		return ""
	}
	return a.sourceManifestDigest
}
func (a *DesktopNativeAgreement) MatrixDigest() string {
	if a == nil {
		return ""
	}
	return a.matrixDigest
}
func (a *DesktopNativeAgreement) DetectorCatalogDigest() string {
	if a == nil {
		return ""
	}
	return a.detectorCatalogDigest
}
func (a *DesktopNativeAgreement) Matrix() NativeCapabilityMatrix {
	if a == nil {
		return NativeCapabilityMatrix{}
	}
	return cloneNativeMatrix(a.matrix)
}
func (a *DesktopNativeAgreement) Definitions() []NativeCoverageDefinition {
	if a == nil {
		return nil
	}
	return cloneNativeDefinitions(a.definitions)
}

// Coverage returns truthful certified coverage for a required claim. An observe
// requirement may be unavailable. Enforce and baseline requirements fail closed.
func (a *DesktopNativeAgreement) Coverage(key string, required NativeCapabilityClaim) (NativeCapabilityMatrixEntry, error) {
	if a == nil || required != NativeClaimObserve && required != NativeClaimEnforce {
		return NativeCapabilityMatrixEntry{}, errNativeAgreement
	}
	index, found := slices.BinarySearchFunc(a.definitions, key, func(d NativeCoverageDefinition, k string) int {
		if d.CoverageKey < k {
			return -1
		}
		if d.CoverageKey > k {
			return 1
		}
		return 0
	})
	if !found {
		return NativeCapabilityMatrixEntry{}, errNativeAgreement
	}
	entry := a.matrix.Entries[index]
	if required == NativeClaimEnforce && entry.Claim != NativeClaimEnforce {
		return NativeCapabilityMatrixEntry{}, errNativeAgreement
	}
	entry.RequiredPermissions = slices.Clone(entry.RequiredPermissions)
	entry.Limitations = slices.Clone(entry.Limitations)
	return entry, nil
}

func (a *DesktopNativeAgreement) ValidateDetector(id string, version int64, capability, condition string, mode NativeCapabilityClaim, sources []NativeSourceID) error {
	if a == nil {
		return errNativeAgreement
	}
	for _, detector := range a.detectors {
		if detector.DetectorID == id && detector.Version == version && detector.CapabilityID == capability &&
			slices.Contains(detector.ConditionIDs, condition) && slices.Contains(detector.AllowedModes, mode) && slices.Equal(detector.SourceIDs, sources) {
			return nil
		}
	}
	return errNativeAgreement
}
