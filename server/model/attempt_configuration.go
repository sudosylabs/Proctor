// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package model

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"slices"
)

const (
	AttemptConfigurationSchemaVersion = 1
	AttemptConfigurationMaxBytes      = 16 << 10
	AttemptConfigurationManifestLimit = 8
	AttemptCommandBindingLimit        = 32
)

// The compatibility manifest is server-owned immutable agreement data. The
// approved command catalog is intentionally empty until coordinated Desktop
// packaging introduces exact command/keybinding pairs.
const attemptConfigurationManifestJSON = `{"format_version":1,"attempt_configuration_schema_version":1,"preferences":{"theme_mode":["follow_system","light","dark"],"high_contrast_mode":["auto","on","off"],"ui_zoom_percent":[80,200],"editor_font_size_px":[12,32],"editor_line_height_percent":[120,200],"reduced_motion_mode":["auto","on","off"],"screen_reader_mode":["auto","on","off"],"announcement_detail":["standard","verbose"],"cursor_style":["line","block","underline"],"cursor_blinking":["blink","solid"],"candidate_command_bindings":{"maximum":32,"catalog":[]}}}`

type AttemptThemeMode string
type AttemptTriStateMode string
type AttemptAnnouncementDetail string
type AttemptCursorStyle string
type AttemptCursorBlinking string

const (
	AttemptThemeFollowSystem AttemptThemeMode = "follow_system"
	AttemptThemeLight        AttemptThemeMode = "light"
	AttemptThemeDark         AttemptThemeMode = "dark"

	AttemptModeAuto AttemptTriStateMode = "auto"
	AttemptModeOn   AttemptTriStateMode = "on"
	AttemptModeOff  AttemptTriStateMode = "off"

	AttemptAnnouncementStandard AttemptAnnouncementDetail = "standard"
	AttemptAnnouncementVerbose  AttemptAnnouncementDetail = "verbose"

	AttemptCursorLine      AttemptCursorStyle = "line"
	AttemptCursorBlock     AttemptCursorStyle = "block"
	AttemptCursorUnderline AttemptCursorStyle = "underline"

	AttemptCursorBlink AttemptCursorBlinking = "blink"
	AttemptCursorSolid AttemptCursorBlinking = "solid"
)

type AttemptCommandBinding struct {
	CommandID    string `json:"command_id"`
	KeybindingID string `json:"keybinding_id"`
}

type AttemptConfigurationPreferences struct {
	ThemeMode                AttemptThemeMode          `json:"theme_mode"`
	HighContrastMode         AttemptTriStateMode       `json:"high_contrast_mode"`
	UIZoomPercent            int                       `json:"ui_zoom_percent"`
	EditorFontSizePX         int                       `json:"editor_font_size_px"`
	EditorLineHeightPercent  int                       `json:"editor_line_height_percent"`
	ReducedMotionMode        AttemptTriStateMode       `json:"reduced_motion_mode"`
	ScreenReaderMode         AttemptTriStateMode       `json:"screen_reader_mode"`
	AnnouncementDetail       AttemptAnnouncementDetail `json:"announcement_detail"`
	CursorStyle              AttemptCursorStyle        `json:"cursor_style"`
	CursorBlinking           AttemptCursorBlinking     `json:"cursor_blinking"`
	CandidateCommandBindings []AttemptCommandBinding   `json:"candidate_command_bindings"`
}

// AttemptConfiguration is the immutable Attempt-owned value plus hidden
// first-admission provenance. Digest identifies only its runtime-visible
// semantics and therefore excludes both provenance revisions.
type AttemptConfiguration struct {
	SchemaVersion                    int                             `json:"schema_version"`
	ManifestFingerprint              string                          `json:"manifest_fingerprint"`
	SourceUserSettingsRevision       UserSettingsRevision            `json:"source_user_settings_revision"`
	SourceDesktopRegistryFingerprint string                          `json:"source_desktop_registry_fingerprint"`
	Preferences                      AttemptConfigurationPreferences `json:"preferences"`
	Digest                           string                          `json:"-"`
}

type attemptConfigurationSemantics struct {
	SchemaVersion       int                             `json:"schema_version"`
	ManifestFingerprint string                          `json:"manifest_fingerprint"`
	Preferences         AttemptConfigurationPreferences `json:"preferences"`
}

func (configuration *AttemptConfiguration) UnmarshalJSON(document []byte) error {
	if configuration == nil || len(document) == 0 || len(document) > AttemptConfigurationMaxBytes ||
		rejectDuplicateJSONFields(document) != nil {
		return errors.New("attempt configuration document is invalid")
	}
	type wire AttemptConfiguration
	var decoded wire
	decoder := json.NewDecoder(bytes.NewReader(document))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&decoded); err != nil {
		return errors.New("attempt configuration document is invalid")
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("attempt configuration document contains trailing data")
	}
	value, err := NewAttemptConfiguration(decoded.SchemaVersion, decoded.ManifestFingerprint,
		decoded.SourceUserSettingsRevision, decoded.SourceDesktopRegistryFingerprint, decoded.Preferences)
	if err != nil {
		return err
	}
	*configuration = value
	return nil
}

func CurrentAttemptConfigurationManifestFingerprint() string {
	return SHA256Fingerprint([]byte(attemptConfigurationManifestJSON))
}

func SHA256Fingerprint(data []byte) string {
	digest := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(digest[:])
}

func IsValidSHA256Fingerprint(value string) bool {
	return len(value) == len("sha256:")+sha256.Size*2 && value[:len("sha256:")] == "sha256:" &&
		validSHA256Fingerprint.MatchString(value[len("sha256:"):])
}

func (preferences AttemptConfigurationPreferences) Validate() error {
	if preferences.ThemeMode != AttemptThemeFollowSystem && preferences.ThemeMode != AttemptThemeLight && preferences.ThemeMode != AttemptThemeDark {
		return errors.New("attempt configuration theme mode is invalid")
	}
	validMode := func(value AttemptTriStateMode) bool {
		return value == AttemptModeAuto || value == AttemptModeOn || value == AttemptModeOff
	}
	if !validMode(preferences.HighContrastMode) || !validMode(preferences.ReducedMotionMode) || !validMode(preferences.ScreenReaderMode) ||
		preferences.UIZoomPercent < 80 || preferences.UIZoomPercent > 200 || preferences.EditorFontSizePX < 12 || preferences.EditorFontSizePX > 32 ||
		preferences.EditorLineHeightPercent < 120 || preferences.EditorLineHeightPercent > 200 ||
		(preferences.AnnouncementDetail != AttemptAnnouncementStandard && preferences.AnnouncementDetail != AttemptAnnouncementVerbose) ||
		(preferences.CursorStyle != AttemptCursorLine && preferences.CursorStyle != AttemptCursorBlock && preferences.CursorStyle != AttemptCursorUnderline) ||
		(preferences.CursorBlinking != AttemptCursorBlink && preferences.CursorBlinking != AttemptCursorSolid) ||
		preferences.CandidateCommandBindings == nil || len(preferences.CandidateCommandBindings) > AttemptCommandBindingLimit {
		return errors.New("attempt configuration preferences are invalid")
	}
	if !slices.IsSortedFunc(preferences.CandidateCommandBindings, compareAttemptCommandBinding) {
		return errors.New("attempt configuration command bindings are not canonical")
	}
	for index, binding := range preferences.CandidateCommandBindings {
		// No command/keybinding pairs are admitted by the current manifest.
		if binding.CommandID == "" || binding.KeybindingID == "" || index > 0 &&
			compareAttemptCommandBinding(preferences.CandidateCommandBindings[index-1], binding) == 0 {
			return errors.New("attempt configuration command bindings are invalid")
		}
		return errors.New("attempt configuration command binding is not approved by the current manifest")
	}
	return nil
}

func compareAttemptCommandBinding(left, right AttemptCommandBinding) int {
	if left.CommandID < right.CommandID {
		return -1
	}
	if left.CommandID > right.CommandID {
		return 1
	}
	if left.KeybindingID < right.KeybindingID {
		return -1
	}
	if left.KeybindingID > right.KeybindingID {
		return 1
	}
	return 0
}

func NewAttemptConfiguration(schemaVersion int, manifestFingerprint string, settingsRevision UserSettingsRevision,
	desktopRegistryFingerprint string, preferences AttemptConfigurationPreferences,
) (AttemptConfiguration, error) {
	value := AttemptConfiguration{SchemaVersion: schemaVersion, ManifestFingerprint: manifestFingerprint,
		SourceUserSettingsRevision: settingsRevision, SourceDesktopRegistryFingerprint: desktopRegistryFingerprint,
		Preferences: preferences}
	if err := value.prepareAndValidate(); err != nil {
		return AttemptConfiguration{}, err
	}
	return value, nil
}

func (configuration *AttemptConfiguration) prepareAndValidate() error {
	if configuration == nil || configuration.SchemaVersion != AttemptConfigurationSchemaVersion ||
		configuration.ManifestFingerprint != CurrentAttemptConfigurationManifestFingerprint() ||
		!configuration.SourceUserSettingsRevision.IsValid() || !IsValidSHA256Fingerprint(configuration.SourceDesktopRegistryFingerprint) ||
		configuration.Preferences.Validate() != nil {
		return errors.New("attempt configuration is invalid")
	}
	canonical, err := configuration.CanonicalSemantics()
	if err != nil {
		return err
	}
	configuration.Digest = SHA256Fingerprint(canonical)
	admission, err := configuration.CanonicalAdmission()
	if err != nil || len(admission) > AttemptConfigurationMaxBytes {
		return errors.New("attempt configuration exceeds its canonical bound")
	}
	return nil
}

func (configuration AttemptConfiguration) Validate() error {
	candidate := configuration
	if err := candidate.prepareAndValidate(); err != nil || candidate.Digest != configuration.Digest {
		return errors.New("attempt configuration is invalid")
	}
	return nil
}

func (configuration AttemptConfiguration) CanonicalSemantics() ([]byte, error) {
	if configuration.Preferences.Validate() != nil {
		return nil, errors.New("attempt configuration preferences are invalid")
	}
	return json.Marshal(attemptConfigurationSemantics{SchemaVersion: configuration.SchemaVersion,
		ManifestFingerprint: configuration.ManifestFingerprint, Preferences: configuration.Preferences})
}

func (configuration AttemptConfiguration) CanonicalAdmission() ([]byte, error) {
	type admission struct {
		SchemaVersion                    int                             `json:"schema_version"`
		ManifestFingerprint              string                          `json:"manifest_fingerprint"`
		SourceUserSettingsRevision       UserSettingsRevision            `json:"source_user_settings_revision"`
		SourceDesktopRegistryFingerprint string                          `json:"source_desktop_registry_fingerprint"`
		Preferences                      AttemptConfigurationPreferences `json:"preferences"`
	}
	return json.Marshal(admission{configuration.SchemaVersion, configuration.ManifestFingerprint,
		configuration.SourceUserSettingsRevision, configuration.SourceDesktopRegistryFingerprint, configuration.Preferences})
}

// DecodeAttemptConfiguration accepts only the canonical persisted admission
// document and verifies the separately persisted semantic digest. Keeping the
// digest outside the document prevents admission provenance from becoming
// part of the runtime configuration identity.
func DecodeAttemptConfiguration(document []byte, digest string) (AttemptConfiguration, error) {
	var value AttemptConfiguration
	if len(document) == 0 || len(document) > AttemptConfigurationMaxBytes || !IsValidSHA256Fingerprint(digest) ||
		rejectDuplicateJSONFields(document) != nil {
		return value, errors.New("attempt configuration document is invalid")
	}
	decoder := json.NewDecoder(bytes.NewReader(document))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&value); err != nil {
		return AttemptConfiguration{}, errors.New("attempt configuration document is invalid")
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return AttemptConfiguration{}, errors.New("attempt configuration document contains trailing data")
	}
	value.Digest = digest
	if err := value.Validate(); err != nil {
		return AttemptConfiguration{}, err
	}
	canonical, err := value.CanonicalAdmission()
	if err != nil || !bytes.Equal(document, canonical) {
		return AttemptConfiguration{}, errors.New("attempt configuration document is not canonical")
	}
	return value, nil
}

func (configuration AttemptConfiguration) Clone() AttemptConfiguration {
	configuration.Preferences.CandidateCommandBindings = slices.Clone(configuration.Preferences.CandidateCommandBindings)
	return configuration
}
