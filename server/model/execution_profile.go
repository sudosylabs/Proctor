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
	"io"
)

const ExecutionProfileSchemaVersion = 1

// ExecutionProfile is the authored, revision-frozen terminal choice. Resource
// limits and allowlist contents remain installation policy and are never
// teacher-controlled.
type ExecutionProfile struct {
	Enabled bool
	Image   string
	Network ExecutionNetwork
}

func DecodeExecutionProfile(document []byte) (ExecutionProfile, error) {
	type wire struct {
		SchemaVersion int              `json:"schema_version"`
		Enabled       bool             `json:"enabled"`
		Image         string           `json:"image"`
		Network       ExecutionNetwork `json:"network"`
	}
	decoder := json.NewDecoder(bytes.NewReader(document))
	decoder.DisallowUnknownFields()
	var decoded wire
	if err := decoder.Decode(&decoded); err != nil {
		return ExecutionProfile{}, err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return ExecutionProfile{}, errors.New("model: Execution Profile contains trailing JSON")
		}
		return ExecutionProfile{}, err
	}
	profile := ExecutionProfile{Enabled: decoded.Enabled, Image: decoded.Image, Network: decoded.Network}
	if decoded.SchemaVersion != ExecutionProfileSchemaVersion {
		return ExecutionProfile{}, errors.New("model: unsupported Execution Profile schema")
	}
	if err := profile.Validate(); err != nil {
		return ExecutionProfile{}, err
	}
	return profile, nil
}

func DefaultExecutionProfile() ExecutionProfile {
	return ExecutionProfile{Network: ExecutionNetworkNone}
}

func (profile ExecutionProfile) Validate() error {
	if profile.Network != ExecutionNetworkNone && profile.Network != ExecutionNetworkAllowlist {
		return errors.New("model: invalid Execution Profile network")
	}
	if profile.Enabled {
		if !validExecutionImage(profile.Image) {
			return errors.New("model: invalid Execution Profile image")
		}
		return nil
	}
	if profile.Image != "" || profile.Network != ExecutionNetworkNone {
		return errors.New("model: disabled Execution Profile carries capability")
	}
	return nil
}

func EncodeExecutionProfile(profile ExecutionProfile) ([]byte, error) {
	if err := profile.Validate(); err != nil {
		return nil, err
	}
	return json.Marshal(struct {
		SchemaVersion int              `json:"schema_version"`
		Enabled       bool             `json:"enabled"`
		Image         string           `json:"image"`
		Network       ExecutionNetwork `json:"network"`
	}{ExecutionProfileSchemaVersion, profile.Enabled, profile.Image, profile.Network})
}

func ExecutionProfileDigest(profile ExecutionProfile) (string, error) {
	encoded, err := EncodeExecutionProfile(profile)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:]), nil
}
