// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package model

import (
	"bytes"
	"encoding/json"
	"errors"
	"slices"
)

// NativeRegistryDigest identifies the reviewed registry artifact. It does not
// certify any Desktop release or native implementation. New registry meaning
// requires coordinated server/client artifact admission. The identity is for
// proctor-desktop native/schemas/native-capability-registry.json at revision
// 271f63b293ea42c3ee1dfc9e30b96835a6def32e; no artifact source is embedded here.
const NativeRegistryDigest = "sha256:79faa1141236b073dff2ff802ce67abce3082417a7a4fa2954615e4226acd1e0"

type NativeMode string

const (
	NativeModeDisabled NativeMode = "disabled"
	NativeModeObserve  NativeMode = "observe"
	NativeModeEnforce  NativeMode = "enforce"
)

func (m NativeMode) IsValid() bool {
	return m == NativeModeDisabled || m == NativeModeObserve || m == NativeModeEnforce
}

func NativeFamilies() []string {
	return []string{"process_application", "interactive_session", "clipboard", "printing", "removable_storage", "virtualization", "network_configuration", "camera", "microphone", "external_capture"}
}

// NativeFamilyPolicy is a closed immutable union. Typed fields are private so
// callers cannot retain aliases or attach fields owned by another family.
// Decode the complete authored value; changes create a new value.
type NativeFamilyPolicy struct{ fields nativeFamilyFields }

type nativeFamilyFields struct {
	ID                             string     `json:"id"`
	Mode                           NativeMode `json:"mode"`
	AllowedApplicationIDs          []string   `json:"allowed_application_ids"`
	Strategy                       string     `json:"strategy"`
	ObserveOSClipboardChanges      bool       `json:"observe_os_clipboard_changes"`
	CandidatePrintCommands         string     `json:"candidate_print_commands"`
	MonitoredSpooler               string     `json:"monitored_spooler"`
	EnabledSubCapabilities         []string   `json:"enabled_sub_capabilities"`
	AllowedStorageFunctionClassIDs []string   `json:"allowed_storage_function_class_ids"`
	Rule                           string     `json:"rule"`
	AllowedManagedProfileIDs       []string   `json:"allowed_managed_profile_ids"`
	AllowedAdapterClassIDs         []string   `json:"allowed_adapter_class_ids"`
	AllowedTunnelClassIDs          []string   `json:"allowed_tunnel_class_ids"`
	RequireReady                   bool       `json:"require_ready"`
	ConcurrentUseCheck             string     `json:"concurrent_use_check"`
}

var errNativePolicy = errors.New("native policy is invalid")

func (p NativeFamilyPolicy) ID() string       { return p.fields.ID }
func (p NativeFamilyPolicy) Mode() NativeMode { return p.fields.Mode }

func (p NativeFamilyPolicy) Validate() error {
	f := p.fields
	if !f.Mode.IsValid() {
		return errNativePolicy
	}
	disabled := f.Mode == NativeModeDisabled
	switch f.ID {
	case "process_application":
		if validateNativePolicyIDs(f.AllowedApplicationIDs) != nil || disabled && len(f.AllowedApplicationIDs) != 0 {
			return errNativePolicy
		}
	case "interactive_session":
	case "clipboard":
		switch f.Mode {
		case NativeModeDisabled:
			if f.Strategy != "disabled" || f.ObserveOSClipboardChanges {
				return errNativePolicy
			}
		case NativeModeObserve:
			if f.Strategy != "disabled" || !f.ObserveOSClipboardChanges {
				return errNativePolicy
			}
		case NativeModeEnforce:
			if f.Strategy != "private_attempt" {
				return errNativePolicy
			}
		}
	case "printing":
		if f.MonitoredSpooler != "required" && f.MonitoredSpooler != "not_required" {
			return errNativePolicy
		}
		switch f.Mode {
		case NativeModeDisabled:
			if f.CandidatePrintCommands != "not_applied" || f.MonitoredSpooler != "not_required" {
				return errNativePolicy
			}
		case NativeModeObserve:
			if f.CandidatePrintCommands != "not_applied" || f.MonitoredSpooler != "required" {
				return errNativePolicy
			}
		case NativeModeEnforce:
			if f.CandidatePrintCommands != "deny" {
				return errNativePolicy
			}
		}
	case "removable_storage":
		if validateNativePolicySelection(f.EnabledSubCapabilities, []string{"removable_block_storage", "portable_mtp"}, disabled) != nil ||
			validateNativePolicyIDs(f.AllowedStorageFunctionClassIDs) != nil || disabled && len(f.AllowedStorageFunctionClassIDs) != 0 {
			return errNativePolicy
		}
	case "virtualization":
		if disabled && f.Rule != "not_applied" || !disabled && f.Rule != "block_recognized_guest" && f.Rule != "require_certified_physical_environment" {
			return errNativePolicy
		}
	case "network_configuration":
		if validateNativePolicySelection(f.EnabledSubCapabilities, []string{"system_proxy_state", "os_managed_vpn_state", "tunnel_interface_state", "route_change", "network_source_health"}, disabled) != nil ||
			validateNativePolicyIDs(f.AllowedManagedProfileIDs) != nil || validateNativePolicyIDs(f.AllowedAdapterClassIDs) != nil || validateNativePolicyIDs(f.AllowedTunnelClassIDs) != nil ||
			disabled && (len(f.AllowedManagedProfileIDs) != 0 || len(f.AllowedAdapterClassIDs) != 0 || len(f.AllowedTunnelClassIDs) != 0) {
			return errNativePolicy
		}
	case "camera", "microphone":
		if f.ConcurrentUseCheck != "not_required" && f.ConcurrentUseCheck != "supported_required" ||
			disabled && (f.RequireReady || f.ConcurrentUseCheck != "not_required") || !disabled && !f.RequireReady && f.ConcurrentUseCheck == "not_required" {
			return errNativePolicy
		}
	case "external_capture":
		if !disabled {
			return errNativePolicy
		}
	default:
		return errNativePolicy
	}
	return nil
}

func validateNativePolicyIDs(ids []string) error {
	if ids == nil || len(ids) > 256 {
		return errNativePolicy
	}
	previous := ""
	for _, id := range ids {
		if !IsValidAgreementID(id) || id <= previous {
			return errNativePolicy
		}
		previous = id
	}
	return nil
}
func validateNativePolicySelection(ids, allowed []string, disabled bool) error {
	if ids == nil || len(ids) > len(allowed) || disabled && len(ids) != 0 || !disabled && len(ids) == 0 {
		return errNativePolicy
	}
	for i, id := range ids {
		if !slices.Contains(allowed, id) || slices.Contains(ids[:i], id) {
			return errNativePolicy
		}
	}
	return nil
}

func (p NativeFamilyPolicy) MarshalJSON() ([]byte, error) {
	if p.Validate() != nil {
		return nil, errNativePolicy
	}
	f := p.fields
	// This closed encoding map is constructed only from the typed family value;
	// it is never populated with arbitrary client members.
	result := map[string]any{"id": f.ID, "mode": f.Mode}
	switch f.ID {
	case "process_application":
		result["allowed_application_ids"] = f.AllowedApplicationIDs
	case "clipboard":
		result["strategy"] = f.Strategy
		result["observe_os_clipboard_changes"] = f.ObserveOSClipboardChanges
	case "printing":
		result["candidate_print_commands"] = f.CandidatePrintCommands
		result["monitored_spooler"] = f.MonitoredSpooler
	case "removable_storage":
		result["enabled_sub_capabilities"] = f.EnabledSubCapabilities
		result["allowed_storage_function_class_ids"] = f.AllowedStorageFunctionClassIDs
	case "virtualization":
		result["rule"] = f.Rule
	case "network_configuration":
		result["enabled_sub_capabilities"] = f.EnabledSubCapabilities
		result["allowed_managed_profile_ids"] = f.AllowedManagedProfileIDs
		result["allowed_adapter_class_ids"] = f.AllowedAdapterClassIDs
		result["allowed_tunnel_class_ids"] = f.AllowedTunnelClassIDs
	case "camera", "microphone":
		result["require_ready"] = f.RequireReady
		result["concurrent_use_check"] = f.ConcurrentUseCheck
	}
	return json.Marshal(result)
}

func (p *NativeFamilyPolicy) UnmarshalJSON(document []byte) error {
	if p == nil || len(document) > ExamPolicySetMaxBytes || validateExamDocumentJSON(document) != nil {
		return errNativePolicy
	}
	var fields nativeFamilyFields
	decoder := json.NewDecoder(bytes.NewReader(document))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&fields) != nil {
		return errNativePolicy
	}
	candidate := NativeFamilyPolicy{fields: fields}
	if candidate.Validate() != nil {
		return errNativePolicy
	}
	// A complete family has exactly its own members. Re-encoding rejects null,
	// omitted boolean/default fields, foreign-family members and case aliases.
	var supplied any
	if json.Unmarshal(document, &supplied) != nil {
		return errNativePolicy
	}
	raw, err := encodeCanonicalExamDocument(supplied)
	if err != nil {
		return errNativePolicy
	}
	encoded, err := encodeCanonicalExamDocument(candidate)
	if err != nil || !bytes.Equal(raw, encoded) {
		return errNativePolicy
	}
	*p = candidate
	return nil
}

// NativeSecurityPolicy is the exact authored optional-family selection plus
// an immutable mandatory baseline. Catalog resolution is a separate admission
// operation; structurally valid IDs are not thereby registered or authorized.
type NativeSecurityPolicy struct {
	RegistryDigest string               `json:"registry_digest"`
	BaselineID     string               `json:"baseline_id"`
	Families       []NativeFamilyPolicy `json:"families"`
}

func DefaultNativeSecurityPolicy() NativeSecurityPolicy {
	result := NativeSecurityPolicy{RegistryDigest: NativeRegistryDigest, BaselineID: "desktop_candidate"}
	for _, id := range NativeFamilies() {
		f := nativeFamilyFields{ID: id, Mode: NativeModeDisabled}
		switch id {
		case "process_application":
			f.AllowedApplicationIDs = []string{}
		case "clipboard":
			f.Strategy = "disabled"
		case "printing":
			f.CandidatePrintCommands = "not_applied"
			f.MonitoredSpooler = "not_required"
		case "removable_storage":
			f.EnabledSubCapabilities = []string{}
			f.AllowedStorageFunctionClassIDs = []string{}
		case "virtualization":
			f.Rule = "not_applied"
		case "network_configuration":
			f.EnabledSubCapabilities = []string{}
			f.AllowedManagedProfileIDs = []string{}
			f.AllowedAdapterClassIDs = []string{}
			f.AllowedTunnelClassIDs = []string{}
		case "camera", "microphone":
			f.ConcurrentUseCheck = "not_required"
		}
		result.Families = append(result.Families, NativeFamilyPolicy{fields: f})
	}
	return result
}

func (p NativeSecurityPolicy) Validate() error {
	if p.RegistryDigest != NativeRegistryDigest || p.BaselineID != "desktop_candidate" || len(p.Families) != len(NativeFamilies()) {
		return errNativePolicy
	}
	for i, id := range NativeFamilies() {
		if p.Families[i].ID() != id || p.Families[i].Validate() != nil {
			return errNativePolicy
		}
	}
	return nil
}

func (p NativeSecurityPolicy) Clone() NativeSecurityPolicy {
	p.Families = slices.Clone(p.Families)
	// Family fields are private immutable values. Only decoders construct lists;
	// no accessor exposes their backing arrays, so the values can be shared.
	return p
}

func (p *NativeSecurityPolicy) UnmarshalJSON(document []byte) error {
	if p == nil || len(document) > ExamPolicySetMaxBytes || validateExamDocumentJSON(document) != nil {
		return errNativePolicy
	}
	type wire NativeSecurityPolicy
	var decoded wire
	decoder := json.NewDecoder(bytes.NewReader(document))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&decoded) != nil {
		return errNativePolicy
	}
	candidate := NativeSecurityPolicy(decoded)
	if candidate.Validate() != nil {
		return errNativePolicy
	}
	var members map[string]json.RawMessage
	if json.Unmarshal(document, &members) != nil || len(members) != 3 || members["registry_digest"] == nil || members["baseline_id"] == nil || members["families"] == nil {
		return errNativePolicy
	}
	*p = candidate
	return nil
}
