// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package memberlist

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	hashimemberlist "github.com/hashicorp/memberlist"

	"github.com/sudosylabs/proctor/server/cluster"
)

var (
	errAdmissionMetadataInvalid      = errors.New("cluster peer metadata is invalid")
	errAdmissionIdentityMismatch     = errors.New("cluster peer identity does not match member name")
	errAdmissionServerVersionMissing = errors.New("cluster peer server version is missing")
	errAdmissionProtocolInvalid      = errors.New("cluster peer protocol range is invalid")
	errAdmissionProtocolIncompatible = errors.New("cluster peer protocol range is incompatible")
)

func (t *Transport) admitJoinedPeers(
	local *hashimemberlist.Node,
	members []*hashimemberlist.Node,
) error {
	if local == nil {
		return fmt.Errorf("%w: local member is missing", errAdmissionMetadataInvalid)
	}
	seen := make(map[string]struct{}, len(members))
	for _, member := range members {
		// Pointer identity is deliberate: a distinct peer that repeats the
		// local Memberlist name must still pass admission and be rejected as a
		// duplicate identity rather than evading validation.
		if member == local {
			continue
		}
		if member == nil {
			return fmt.Errorf("%w: member is missing", errAdmissionMetadataInvalid)
		}
		meta, err := decodeAdmissionNodeMeta(member.Meta)
		if err != nil {
			return err
		}
		if meta.NodeID != member.Name {
			return errAdmissionIdentityMismatch
		}
		if meta.NodeID == t.cfg.NodeID {
			return fmt.Errorf("%w: peer duplicates local identity", cluster.ErrNodeIDInUse)
		}
		if _, exists := seen[meta.NodeID]; exists {
			return fmt.Errorf("%w: peer identity is duplicated", cluster.ErrNodeIDInUse)
		}
		seen[meta.NodeID] = struct{}{}
		if strings.TrimSpace(meta.ServerVersion) == "" {
			return errAdmissionServerVersionMissing
		}
		if meta.ProtocolMin <= 0 || meta.ProtocolMax < meta.ProtocolMin {
			return errAdmissionProtocolInvalid
		}
		if !protocolsCompatible(t.cfg.ProtocolMin, t.cfg.ProtocolMax, meta.ProtocolMin, meta.ProtocolMax) {
			return errAdmissionProtocolIncompatible
		}
	}
	return nil
}

func decodeAdmissionNodeMeta(raw []byte) (nodeMeta, error) {
	var meta nodeMeta
	if len(raw) == 0 {
		return meta, fmt.Errorf("%w: metadata is missing", errAdmissionMetadataInvalid)
	}
	if err := json.Unmarshal(raw, &meta); err != nil {
		return meta, fmt.Errorf("%w: malformed JSON", errAdmissionMetadataInvalid)
	}
	if strings.TrimSpace(meta.NodeID) == "" {
		return meta, fmt.Errorf("%w: node identity is missing", errAdmissionMetadataInvalid)
	}
	return meta, nil
}
