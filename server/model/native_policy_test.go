// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package model

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDefaultNativeSecurityPolicy(t *testing.T) {
	t.Parallel()
	policy := DefaultNativeSecurityPolicy()
	require.NoError(t, policy.Validate(), "invalid default")
	document, err := encodeCanonicalExamDocument(policy)
	require.NoError(t, err)
	var decoded NativeSecurityPolicy
	require.NoError(t, json.Unmarshal(document, &decoded), "default policy failed strict round trip")
	for i, id := range NativeFamilies() {
		require.Equal(t, id, decoded.Families[i].ID(), "optional family enabled by default")
		require.Equal(t, NativeModeDisabled, decoded.Families[i].Mode(), "optional family enabled by default")
	}
	cloned := policy.Clone()
	cloned.Families[0] = NativeFamilyPolicy{}
	require.NoError(t, policy.Validate(), "clone aliases family collection")
	for name, raw := range map[string]string{
		"wrong baseline": strings.Replace(string(document), `"desktop_candidate"`, `"optional"`, 1),
		"wrong registry": strings.Replace(string(document), NativeRegistryDigest, SHA256Fingerprint([]byte("unknown")), 1),
		"unknown root":   strings.Replace(string(document), `"baseline_id":`, `"unknown":true,"baseline_id":`, 1),
		"case alias":     strings.Replace(string(document), `"baseline_id"`, `"BASELINE_ID"`, 1),
	} {
		t.Run(name, func(t *testing.T) {
			var value NativeSecurityPolicy
			require.Error(t, json.Unmarshal([]byte(raw), &value), "accepted invalid native policy")
		})
	}
}

func TestNativeFamilyPolicyExactUnions(t *testing.T) {
	t.Parallel()
	valid := []string{
		`{"id":"process_application","mode":"observe","allowed_application_ids":["approved.application"]}`,
		`{"id":"interactive_session","mode":"enforce"}`,
		`{"id":"clipboard","mode":"observe","strategy":"disabled","observe_os_clipboard_changes":true}`,
		`{"id":"clipboard","mode":"enforce","strategy":"private_attempt","observe_os_clipboard_changes":false}`,
		`{"id":"printing","mode":"observe","candidate_print_commands":"not_applied","monitored_spooler":"required"}`,
		`{"id":"printing","mode":"enforce","candidate_print_commands":"deny","monitored_spooler":"not_required"}`,
		`{"id":"removable_storage","mode":"enforce","enabled_sub_capabilities":["portable_mtp"],"allowed_storage_function_class_ids":[]}`,
		`{"id":"virtualization","mode":"observe","rule":"block_recognized_guest"}`,
		`{"id":"network_configuration","mode":"enforce","enabled_sub_capabilities":["route_change"],"allowed_managed_profile_ids":[],"allowed_adapter_class_ids":[],"allowed_tunnel_class_ids":[]}`,
		`{"id":"camera","mode":"observe","require_ready":true,"concurrent_use_check":"not_required"}`,
		`{"id":"microphone","mode":"enforce","require_ready":false,"concurrent_use_check":"supported_required"}`,
		`{"id":"external_capture","mode":"disabled"}`,
	}
	for _, document := range valid {
		t.Run(document, func(t *testing.T) {
			var value NativeFamilyPolicy
			require.NoError(t, json.Unmarshal([]byte(document), &value))
			encoded, err := json.Marshal(value)
			require.NoError(t, err)
			var decoded NativeFamilyPolicy
			require.NoError(t, json.Unmarshal(encoded, &decoded), "family round trip failed")
		})
	}
}

func TestNativeFamilyPolicyRejectsUnsafeOrMeaninglessSelections(t *testing.T) {
	t.Parallel()
	tests := map[string]string{
		"disabled allowlist":         `{"id":"process_application","mode":"disabled","allowed_application_ids":["application"]}`,
		"duplicate allowlist":        `{"id":"process_application","mode":"observe","allowed_application_ids":["a","a"]}`,
		"unsorted allowlist":         `{"id":"process_application","mode":"observe","allowed_application_ids":["z","a"]}`,
		"executable path":            `{"id":"process_application","mode":"enforce","allowed_application_ids":["/usr/bin/tool"]}`,
		"missing boolean":            `{"id":"clipboard","mode":"enforce","strategy":"private_attempt"}`,
		"null boolean":               `{"id":"clipboard","mode":"enforce","strategy":"private_attempt","observe_os_clipboard_changes":null}`,
		"observe clipboard no check": `{"id":"clipboard","mode":"observe","strategy":"disabled","observe_os_clipboard_changes":false}`,
		"clipboard strategy":         `{"id":"clipboard","mode":"enforce","strategy":"os_clipboard","observe_os_clipboard_changes":true}`,
		"printing observe effect":    `{"id":"printing","mode":"observe","candidate_print_commands":"deny","monitored_spooler":"required"}`,
		"no printing checks":         `{"id":"printing","mode":"enforce","candidate_print_commands":"not_applied","monitored_spooler":"not_required"}`,
		"empty storage":              `{"id":"removable_storage","mode":"observe","enabled_sub_capabilities":[],"allowed_storage_function_class_ids":[]}`,
		"storage cross family":       `{"id":"removable_storage","mode":"observe","enabled_sub_capabilities":["route_change"],"allowed_storage_function_class_ids":[]}`,
		"duplicate category":         `{"id":"removable_storage","mode":"observe","enabled_sub_capabilities":["portable_mtp","portable_mtp"],"allowed_storage_function_class_ids":[]}`,
		"unknown physical":           `{"id":"virtualization","mode":"enforce","rule":"assume_physical"}`,
		"empty network":              `{"id":"network_configuration","mode":"observe","enabled_sub_capabilities":[],"allowed_managed_profile_ids":[],"allowed_adapter_class_ids":[],"allowed_tunnel_class_ids":[]}`,
		"disabled network check":     `{"id":"network_configuration","mode":"disabled","enabled_sub_capabilities":["route_change"],"allowed_managed_profile_ids":[],"allowed_adapter_class_ids":[],"allowed_tunnel_class_ids":[]}`,
		"empty media":                `{"id":"camera","mode":"observe","require_ready":false,"concurrent_use_check":"not_required"}`,
		"disabled media check":       `{"id":"microphone","mode":"disabled","require_ready":true,"concurrent_use_check":"not_required"}`,
		"external capture detection": `{"id":"external_capture","mode":"observe"}`,
		"cross family member":        `{"id":"interactive_session","mode":"observe","require_ready":false}`,
		"case alias":                 `{"ID":"interactive_session","mode":"observe"}`,
		"duplicate member":           `{"id":"interactive_session","mode":"observe","mode":"enforce"}`,
		"null list":                  `{"id":"process_application","mode":"disabled","allowed_application_ids":null}`,
	}
	for name, document := range tests {
		t.Run(name, func(t *testing.T) {
			var value NativeFamilyPolicy
			require.Error(t, json.Unmarshal([]byte(document), &value), "accepted invalid family")
		})
	}
}
