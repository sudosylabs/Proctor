// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

// Package desktoprelease verifies exact packaged Desktop agreement artifacts.
// The composition root supplies admitted release keys and immutable expectations;
// no request, Institution preference or client upload can extend that trust set.
// It owns signature/byte verification, not eligibility, leases or native effects.
package desktoprelease

import (
	"bytes"
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"unicode/utf8"

	"github.com/sudosylabs/proctor/server/internal/canonicaljson"
	"github.com/sudosylabs/proctor/server/model"
)

const (
	MatrixMaxBytes   = 64 << 10
	ArtifactMaxBytes = 1 << 20
)

var ErrInvalidArtifact = errors.New("desktop release artifact is invalid")

// SignedMatrix contains the exact canonical UTF-8 bytes signed using an admitted
// Ed25519 release key. Signature is the raw 64-byte signature, not a DPoP proof.
type SignedMatrix struct {
	Payload   []byte
	Signature []byte
	KeyID     string
}

// Expectation is compiled release admission data, never a received DTO. The
// registry definitions are the server's reviewed interpretation of the pinned
// registry; schema bytes and all packaged catalogs are pinned independently.
type Expectation struct {
	Build                 model.DesktopBuildTuple
	ApplicationReleaseID  string
	RegistryDigest        string
	SourceManifestDigest  string
	MatrixDigest          string
	DetectorCatalogDigest string
	Coverage              []model.NativeCoverageDefinition
	SourceSchemaDigests   map[model.NativeSourceID]string
}

type Artifacts struct {
	Registry              []byte
	SourceManifest        []byte
	Schemas               map[string][]byte
	DetectorCatalog       []byte
	ConfigurationManifest []byte
	Matrix                SignedMatrix
}

// Verify admits only an exact signed matrix and its pinned registry, source
// schemas, detector catalog and configuration manifest. Returned model catalogs
// own defensive copies and cannot be altered through caller-owned buffers.
func Verify(expected Expectation, artifacts Artifacts, admittedKeys map[string]ed25519.PublicKey) (model.DesktopBuildTuple, error) {
	if expected.Build.Validate() != nil || !model.IsValidAgreementID(expected.ApplicationReleaseID) ||
		!model.IsValidAgreementID(artifacts.Matrix.KeyID) || len(artifacts.Matrix.Payload) == 0 || len(artifacts.Matrix.Payload) > MatrixMaxBytes {
		return model.DesktopBuildTuple{}, ErrInvalidArtifact
	}
	key := admittedKeys[artifacts.Matrix.KeyID]
	if len(key) != ed25519.PublicKeySize || len(artifacts.Matrix.Signature) != ed25519.SignatureSize ||
		!ed25519.Verify(key, artifacts.Matrix.Payload, artifacts.Matrix.Signature) {
		return model.DesktopBuildTuple{}, ErrInvalidArtifact
	}
	if !exactArtifact(artifacts.Registry, expected.RegistryDigest) || !exactArtifact(artifacts.SourceManifest, expected.SourceManifestDigest) ||
		!exactArtifact(artifacts.Matrix.Payload, expected.MatrixDigest) || !exactArtifact(artifacts.DetectorCatalog, expected.DetectorCatalogDigest) ||
		!exactArtifact(artifacts.ConfigurationManifest, expected.Build.AttemptConfigurationManifestFingerprint) ||
		!bytes.Equal(artifacts.ConfigurationManifest, expected.Build.ConfigurationManifest.Canonical()) {
		return model.DesktopBuildTuple{}, ErrInvalidArtifact
	}
	// Every source's exact schema is admitted even when the optional family is
	// disabled. Effect-only coverage additionally binds its own packaged schema.
	if len(expected.SourceSchemaDigests) != len(model.NativeSources()) || len(artifacts.Schemas) > len(model.NativeSources())+model.NativeCoverageClaimLimit {
		return model.DesktopBuildTuple{}, ErrInvalidArtifact
	}
	used := make(map[string]bool, len(artifacts.Schemas))
	for _, source := range model.NativeSources() {
		digest := expected.SourceSchemaDigests[source]
		if !exactArtifact(artifacts.Schemas[digest], digest) {
			return model.DesktopBuildTuple{}, ErrInvalidArtifact
		}
		used[digest] = true
	}
	for _, definition := range expected.Coverage {
		if !exactArtifact(artifacts.Schemas[definition.SourceSchemaDigest], definition.SourceSchemaDigest) ||
			definition.SourceID != "" && expected.SourceSchemaDigests[definition.SourceID] != definition.SourceSchemaDigest {
			return model.DesktopBuildTuple{}, ErrInvalidArtifact
		}
		used[definition.SourceSchemaDigest] = true
	}
	if len(used) != len(artifacts.Schemas) {
		return model.DesktopBuildTuple{}, ErrInvalidArtifact
	}
	var matrix model.NativeCapabilityMatrix
	if decodeCanonicalClosed(artifacts.Matrix.Payload, &matrix, MatrixMaxBytes) != nil ||
		matrix.ReleaseID != expected.ApplicationReleaseID || matrix.MatrixID != expected.Build.CapabilityMatrixIdentity ||
		matrix.TargetTuple != expected.Build.TargetTuple() || matrix.RegistryDigest != expected.RegistryDigest {
		return model.DesktopBuildTuple{}, ErrInvalidArtifact
	}
	var detectors []model.NativeDetectorDefinition
	if decodeCanonicalClosed(artifacts.DetectorCatalog, &detectors, ArtifactMaxBytes) != nil {
		return model.DesktopBuildTuple{}, ErrInvalidArtifact
	}
	agreement, err := model.NewDesktopNativeAgreement(expected.RegistryDigest, expected.SourceManifestDigest, expected.MatrixDigest,
		expected.DetectorCatalogDigest, expected.Coverage, matrix, detectors)
	if err != nil {
		return model.DesktopBuildTuple{}, ErrInvalidArtifact
	}
	build := expected.Build
	build.NativeAgreement = agreement
	return build, nil
}

func exactArtifact(document []byte, digest string) bool {
	return len(document) > 0 && len(document) <= ArtifactMaxBytes && model.IsValidSHA256Fingerprint(digest) &&
		utf8.Valid(document) && json.Valid(document) && model.SHA256Fingerprint(document) == digest
}

// Canonical re-encoding also rejects case aliases and omitted required fields,
// which encoding/json's unknown-field check alone does not detect.
func decodeCanonicalClosed(document []byte, value any, limit int) error {
	canonical, err := canonicaljson.Canonicalize(document, limit)
	if err != nil || !bytes.Equal(canonical, document) {
		return ErrInvalidArtifact
	}
	decoder := json.NewDecoder(bytes.NewReader(document))
	decoder.DisallowUnknownFields()
	if decoder.Decode(value) != nil {
		return ErrInvalidArtifact
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return ErrInvalidArtifact
	}
	encoded, err = canonicaljson.Canonicalize(encoded, limit)
	if err != nil || !bytes.Equal(encoded, document) {
		return ErrInvalidArtifact
	}
	return nil
}
