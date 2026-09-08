// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package model

import (
	"encoding/json"
	"slices"
)

// This immutable manifest covers the current candidate shape and its meanings.
// It is not a client-selected format or a complete editor settings registry.
const attemptConfigurationSchemaJSON = `{"candidate_fields":["manifest_fingerprint","registry_fingerprint","desktop_build","desktop_target","user_settings_revision","presentation","approved_commands","approved_keybindings"],"digest":"sha256-of-complete-canonical-candidate","maximum_canonical_bytes":16384,"maximum_approved_commands":128,"maximum_approved_keybindings":128,"presentation":{"color_theme":["light","dark","hcLight","hcDark"],"prefer_high_contrast":"boolean","zoom_percent":{"minimum":80,"maximum":200,"default":100,"unit":"percent"},"editor_font_size_px":{"minimum":12,"maximum":24,"default":14,"unit":"pixels"},"editor_line_height_px":{"minimum":16,"maximum":40,"default":22,"unit":"independent-pixels"},"reduced_motion":"boolean","screen_reader_mode":["auto","on","off"],"announcement_mode":["auto","verbose","minimal"],"cursor_style":["line","block","underline"],"cursor_blinking":["blink","smooth","phase","expand","solid"]},"keybinding_selection":"catalog-ids-associated-with-selected-candidate-safe-commands"}`

// AttemptCommandBinding associates a packaged keybinding identifier with its
// Candidate-safe command. It never contains a key chord or executable command.
type AttemptCommandBinding struct {
	CommandID    string `json:"command_id"`
	KeybindingID string `json:"keybinding_id"`
}

// AttemptConfigurationManifest owns an immutable admitted command catalog.
// Construct it only from trusted packaging data; user input cannot register IDs.
// Private fields and defensive accessors prevent post-verification mutation.
type AttemptConfigurationManifest struct {
	commands    []string
	keybindings []AttemptCommandBinding
	canonical   []byte
	fingerprint string
}

func NewAttemptConfigurationManifest(commands []string, keybindings []AttemptCommandBinding) (*AttemptConfigurationManifest, error) {
	if ValidateAttemptApprovedIDs(commands) != nil || keybindings == nil || len(keybindings) > AttemptApprovedIDLimit {
		return nil, errAttemptConfiguration
	}
	previous := ""
	for _, binding := range keybindings {
		if !IsValidAgreementID(binding.KeybindingID) || binding.KeybindingID <= previous || !slices.Contains(commands, binding.CommandID) {
			return nil, errAttemptConfiguration
		}
		previous = binding.KeybindingID
	}
	var schema any
	// This literal is owned and tested here; it contains only safe integer values.
	if err := json.Unmarshal([]byte(attemptConfigurationSchemaJSON), &schema); err != nil {
		return nil, err
	}
	canonical, err := encodeCanonicalExamDocument(struct {
		Schema      any                     `json:"schema"`
		Commands    []string                `json:"approved_commands"`
		Keybindings []AttemptCommandBinding `json:"approved_keybindings"`
	}{schema, commands, keybindings})
	if err != nil {
		return nil, err
	}
	return &AttemptConfigurationManifest{commands: slices.Clone(commands), keybindings: slices.Clone(keybindings), canonical: canonical, fingerprint: SHA256Fingerprint(canonical)}, nil
}

func (m *AttemptConfigurationManifest) Fingerprint() string {
	if m == nil {
		return ""
	}
	return m.fingerprint
}

func (m *AttemptConfigurationManifest) Canonical() []byte {
	if m == nil {
		return nil
	}
	return slices.Clone(m.canonical)
}

func (m *AttemptConfigurationManifest) ValidateSelection(commands, keybindings []string) error {
	if m == nil || m.fingerprint == "" || ValidateAttemptApprovedIDs(commands) != nil || ValidateAttemptApprovedIDs(keybindings) != nil {
		return errAttemptConfiguration
	}
	for _, id := range commands {
		if !slices.Contains(m.commands, id) {
			return errAttemptConfiguration
		}
	}
	for _, id := range keybindings {
		index, found := slices.BinarySearchFunc(m.keybindings, id, func(binding AttemptCommandBinding, target string) int {
			if binding.KeybindingID < target {
				return -1
			}
			if binding.KeybindingID > target {
				return 1
			}
			return 0
		})
		if !found || !slices.Contains(commands, m.keybindings[index].CommandID) {
			return errAttemptConfiguration
		}
	}
	return nil
}

// EmptyAttemptConfigurationManifest is valid when a verified release approves
// no command contributions. An empty release catalog still admits no builds.
func EmptyAttemptConfigurationManifest() *AttemptConfigurationManifest {
	manifest, err := NewAttemptConfigurationManifest([]string{}, []AttemptCommandBinding{})
	if err != nil {
		panic("invalid built-in attempt configuration manifest")
	}
	return manifest
}

func CurrentAttemptConfigurationManifestFingerprint() string {
	return EmptyAttemptConfigurationManifest().Fingerprint()
}
