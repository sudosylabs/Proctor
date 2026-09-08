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
)

func TestDefaultNativeSecurityPolicy(t *testing.T) {
	t.Parallel()
	policy := DefaultNativeSecurityPolicy()
	if policy.Validate() != nil {
		t.Fatal("invalid default")
	}
	document, err := encodeCanonicalExamDocument(policy)
	if err != nil {
		t.Fatal(err)
	}
	var decoded NativeSecurityPolicy
	if json.Unmarshal(document, &decoded) != nil {
		t.Fatal("default policy failed strict round trip")
	}
	for i, id := range NativeFamilies() {
		if decoded.Families[i].ID() != id || decoded.Families[i].Mode() != NativeModeDisabled {
			t.Fatal("optional family enabled by default")
		}
	}
	cloned := policy.Clone()
	cloned.Families[0] = NativeFamilyPolicy{}
	if policy.Validate() != nil {
		t.Fatal("clone aliases family collection")
	}
	for name, raw := range map[string]string{
		"wrong baseline": strings.Replace(string(document), `"desktop_candidate"`, `"optional"`, 1),
		"wrong registry": strings.Replace(string(document), NativeRegistryDigest, SHA256Fingerprint([]byte("unknown")), 1),
		"unknown root":   strings.Replace(string(document), `"baseline_id":`, `"unknown":true,"baseline_id":`, 1),
		"case alias":     strings.Replace(string(document), `"baseline_id"`, `"BASELINE_ID"`, 1),
	} {
		t.Run(name, func(t *testing.T) {
			var value NativeSecurityPolicy
			if json.Unmarshal([]byte(raw), &value) == nil {
				t.Fatal("accepted invalid native policy")
			}
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
			if err := json.Unmarshal([]byte(document), &value); err != nil {
				t.Fatal(err)
			}
			encoded, err := json.Marshal(value)
			if err != nil {
				t.Fatal(err)
			}
			var decoded NativeFamilyPolicy
			if json.Unmarshal(encoded, &decoded) != nil {
				t.Fatal("family round trip failed")
			}
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
			if json.Unmarshal([]byte(document), &value) == nil {
				t.Fatal("accepted invalid family")
			}
		})
	}
}
