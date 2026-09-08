// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package model

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"slices"
)

const (
	AttemptConfigurationMaxBytes = 16 << 10
	AttemptApprovedIDLimit       = 128
)

var errAttemptConfiguration = errors.New("attempt configuration is invalid")

// AttemptConfigurationPresentation is the closed, reproducible presentation
// subset. Pixel line height is independent of font size. Stronger accessibility
// constraints affect a runtime projection, never this frozen source.
type AttemptConfigurationPresentation struct {
	ColorTheme         string `json:"color_theme"`
	PreferHighContrast bool   `json:"prefer_high_contrast"`
	ZoomPercent        int    `json:"zoom_percent"`
	EditorFontSizePX   int    `json:"editor_font_size_px"`
	EditorLineHeightPX int    `json:"editor_line_height_px"`
	ReducedMotion      bool   `json:"reduced_motion"`
	ScreenReaderMode   string `json:"screen_reader_mode"`
	AnnouncementMode   string `json:"announcement_mode"`
	CursorStyle        string `json:"cursor_style"`
	CursorBlinking     string `json:"cursor_blinking"`
}

func (p AttemptConfigurationPresentation) Validate() error {
	if !slices.Contains([]string{"light", "dark", "hcLight", "hcDark"}, p.ColorTheme) ||
		p.ZoomPercent < 80 || p.ZoomPercent > 200 || p.EditorFontSizePX < 12 || p.EditorFontSizePX > 24 ||
		p.EditorLineHeightPX < 16 || p.EditorLineHeightPX > 40 ||
		!slices.Contains([]string{"auto", "on", "off"}, p.ScreenReaderMode) ||
		!slices.Contains([]string{"auto", "verbose", "minimal"}, p.AnnouncementMode) ||
		!slices.Contains([]string{"line", "block", "underline"}, p.CursorStyle) ||
		!slices.Contains([]string{"blink", "smooth", "phase", "expand", "solid"}, p.CursorBlinking) {
		return errAttemptConfiguration
	}
	return nil
}

// AttemptConfigurationCandidate includes the exact first-admission provenance.
// Neither catalog identifiers nor a fingerprint supplied by a client confer trust.
type AttemptConfigurationCandidate struct {
	ManifestFingerprint  string                           `json:"manifest_fingerprint"`
	RegistryFingerprint  string                           `json:"registry_fingerprint"`
	DesktopBuild         string                           `json:"desktop_build"`
	DesktopTarget        string                           `json:"desktop_target"`
	UserSettingsRevision UserSettingsRevision             `json:"user_settings_revision"`
	Presentation         AttemptConfigurationPresentation `json:"presentation"`
	ApprovedCommands     []string                         `json:"approved_commands"`
	ApprovedKeybindings  []string                         `json:"approved_keybindings"`
}

// AttemptConfiguration is frozen once by the owning admission transaction.
// Its digest covers the complete candidate, excluding only the server revision
// and digest. Re-entry preserves this original provenance.
type AttemptConfiguration struct {
	AttemptConfigurationCandidate
	Revision string `json:"attempt_configuration_revision"`
	Digest   string `json:"digest"`
}

// IsValidAgreementID recognizes bounded ASCII catalog and agreement identifiers.
func IsValidAgreementID(value string) bool {
	if len(value) == 0 || len(value) > 128 {
		return false
	}
	for i := range len(value) {
		c := value[i]
		if c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' {
			continue
		}
		if i == 0 || c != '.' && c != '_' && c != ':' && c != '-' {
			return false
		}
	}
	return true
}

func ValidateAttemptApprovedIDs(ids []string) error {
	if ids == nil || len(ids) > AttemptApprovedIDLimit {
		return errAttemptConfiguration
	}
	previous := ""
	for _, id := range ids {
		if !IsValidAgreementID(id) || id <= previous {
			return errAttemptConfiguration
		}
		previous = id
	}
	return nil
}

func (c AttemptConfigurationCandidate) Validate() error {
	if !IsValidSHA256Fingerprint(c.ManifestFingerprint) || !IsValidRegistryFingerprint(c.RegistryFingerprint) ||
		!IsValidAgreementID(c.DesktopBuild) || !IsValidAgreementID(c.DesktopTarget) || !c.UserSettingsRevision.IsValid() ||
		c.Presentation.Validate() != nil || ValidateAttemptApprovedIDs(c.ApprovedCommands) != nil ||
		ValidateAttemptApprovedIDs(c.ApprovedKeybindings) != nil {
		return errAttemptConfiguration
	}
	// The complete frozen object must fit too; Freeze enforces that extra bound.
	document, err := encodeCanonicalExamDocument(c)
	if err != nil || len(document) > AttemptConfigurationMaxBytes {
		return errAttemptConfiguration
	}
	return nil
}

// ValidateForBuild is for a new freeze only. Existing configurations instead
// compare reproducibility with the current manifest, retaining original build metadata.
func (c AttemptConfigurationCandidate) ValidateForBuild(build DesktopBuildTuple) error {
	if c.Validate() != nil || build.Validate() != nil || c.DesktopBuild != build.DesktopBuildID ||
		c.DesktopTarget != build.TargetTuple() || c.RegistryFingerprint != build.DesktopSettingsRegistryFingerprint ||
		c.ManifestFingerprint != build.AttemptConfigurationManifestFingerprint ||
		build.ConfigurationManifest.ValidateSelection(c.ApprovedCommands, c.ApprovedKeybindings) != nil {
		return errAttemptConfiguration
	}
	return nil
}

func (c AttemptConfigurationCandidate) CanonicalAdmission() ([]byte, error) {
	if c.Validate() != nil {
		return nil, errAttemptConfiguration
	}
	return encodeCanonicalExamDocument(c)
}

func (c AttemptConfigurationCandidate) Clone() AttemptConfigurationCandidate {
	c.ApprovedCommands = slices.Clone(c.ApprovedCommands)
	c.ApprovedKeybindings = slices.Clone(c.ApprovedKeybindings)
	return c
}

func (c AttemptConfigurationCandidate) Freeze(revision string) (AttemptConfiguration, error) {
	if !IsValidAgreementID(revision) {
		return AttemptConfiguration{}, errAttemptConfiguration
	}
	canonical, err := c.CanonicalAdmission()
	if err != nil {
		return AttemptConfiguration{}, err
	}
	frozen := AttemptConfiguration{AttemptConfigurationCandidate: c.Clone(), Revision: revision, Digest: SHA256Fingerprint(canonical)}
	if err := frozen.Validate(); err != nil {
		return AttemptConfiguration{}, err
	}
	return frozen, nil
}

func (c AttemptConfiguration) Validate() error {
	canonical, err := c.AttemptConfigurationCandidate.CanonicalAdmission()
	if err != nil || !IsValidAgreementID(c.Revision) || c.Digest != SHA256Fingerprint(canonical) {
		return errAttemptConfiguration
	}
	document, err := encodeCanonicalExamDocument(c)
	if err != nil || len(document) > AttemptConfigurationMaxBytes {
		return errAttemptConfiguration
	}
	return nil
}

// CanonicalAdmission returns the one persisted frozen representation.
func (c AttemptConfiguration) CanonicalAdmission() ([]byte, error) {
	if c.Validate() != nil {
		return nil, errAttemptConfiguration
	}
	return encodeCanonicalExamDocument(c)
}

func (c AttemptConfiguration) Clone() AttemptConfiguration {
	c.AttemptConfigurationCandidate = c.AttemptConfigurationCandidate.Clone()
	return c
}

// Explicit marshaling avoids promoting the candidate's decoding methods onto
// the frozen envelope and losing its server-owned fields.
func (c AttemptConfiguration) MarshalJSON() ([]byte, error) {
	type candidate AttemptConfigurationCandidate
	return json.Marshal(struct {
		candidate
		Revision string `json:"attempt_configuration_revision"`
		Digest   string `json:"digest"`
	}{candidate(c.AttemptConfigurationCandidate), c.Revision, c.Digest})
}

func (c *AttemptConfigurationCandidate) UnmarshalJSON(document []byte) error {
	if c == nil || validateConfigurationShape(document, 8) != nil {
		return errAttemptConfiguration
	}
	type wire AttemptConfigurationCandidate
	var decoded wire
	decoder := json.NewDecoder(bytes.NewReader(document))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&decoded) != nil {
		return errAttemptConfiguration
	}
	value := AttemptConfigurationCandidate(decoded)
	if value.Validate() != nil {
		return errAttemptConfiguration
	}
	*c = value
	return nil
}

func (c *AttemptConfiguration) UnmarshalJSON(document []byte) error {
	if c == nil || validateConfigurationShape(document, 10) != nil {
		return errAttemptConfiguration
	}
	type candidate AttemptConfigurationCandidate
	var decoded struct {
		candidate
		Revision string `json:"attempt_configuration_revision"`
		Digest   string `json:"digest"`
	}
	decoder := json.NewDecoder(bytes.NewReader(document))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&decoded) != nil {
		return errAttemptConfiguration
	}
	value := AttemptConfiguration{AttemptConfigurationCandidate: AttemptConfigurationCandidate(decoded.candidate), Revision: decoded.Revision, Digest: decoded.Digest}
	if value.Validate() != nil {
		return errAttemptConfiguration
	}
	*c = value
	return nil
}

func validateConfigurationShape(document []byte, fields int) error {
	if len(document) == 0 || len(document) > AttemptConfigurationMaxBytes || validateExamDocumentJSON(document) != nil {
		return errAttemptConfiguration
	}
	var root map[string]json.RawMessage
	if json.Unmarshal(document, &root) != nil || len(root) != fields {
		return errAttemptConfiguration
	}
	keys := []string{"manifest_fingerprint", "registry_fingerprint", "desktop_build", "desktop_target", "user_settings_revision", "presentation", "approved_commands", "approved_keybindings"}
	if fields == 10 {
		keys = append(keys, "attempt_configuration_revision", "digest")
	}
	for _, key := range keys {
		if _, ok := root[key]; !ok {
			return errAttemptConfiguration
		}
	}
	for _, value := range root {
		if bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return errAttemptConfiguration
		}
	}
	var presentation map[string]json.RawMessage
	if json.Unmarshal(root["presentation"], &presentation) != nil || len(presentation) != 10 {
		return errAttemptConfiguration
	}
	for _, key := range []string{"color_theme", "prefer_high_contrast", "zoom_percent", "editor_font_size_px", "editor_line_height_px", "reduced_motion", "screen_reader_mode", "announcement_mode", "cursor_style", "cursor_blinking"} {
		if _, ok := presentation[key]; !ok {
			return errAttemptConfiguration
		}
	}
	for _, value := range presentation {
		if bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return errAttemptConfiguration
		}
	}
	return nil
}

func DecodeAttemptConfiguration(document []byte, digest string) (AttemptConfiguration, error) {
	var c AttemptConfiguration
	if json.Unmarshal(document, &c) != nil || c.Digest != digest {
		return AttemptConfiguration{}, errAttemptConfiguration
	}
	canonical, err := c.CanonicalAdmission()
	if err != nil || !bytes.Equal(canonical, document) {
		return AttemptConfiguration{}, errAttemptConfiguration
	}
	return c, nil
}

func SHA256Fingerprint(data []byte) string {
	digest := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(digest[:])
}

func IsValidSHA256Fingerprint(value string) bool {
	return len(value) == len("sha256:")+sha256.Size*2 && value[:len("sha256:")] == "sha256:" &&
		validSHA256Fingerprint.MatchString(value[len("sha256:"):])
}
