// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package memberlist

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"

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
	return t.validateAdmissionPeerSet(members, local)
}

func (t *Transport) validateAdmissionPeerSet(
	peers []*hashimemberlist.Node,
	excluded *hashimemberlist.Node,
) error {
	seen := make(map[string]struct{}, len(peers))
	for _, peer := range peers {
		// Pointer identity excludes only the current local node. A distinct
		// same-name incarnation still has to carry valid metadata before
		// Memberlist decides whether its address is stale or conflicting.
		if peer == excluded {
			continue
		}
		meta, err := t.validateAdmissionPeer(peer)
		if err != nil {
			return err
		}
		if meta.NodeID == t.cfg.NodeID {
			// A peer may briefly gossip this node's previous address during an
			// immediate restart. Memberlist's incarnation and name-conflict logic
			// decides whether that address is stale or live.
			continue
		}
		if _, exists := seen[meta.NodeID]; exists {
			return fmt.Errorf("%w: peer identity is duplicated", cluster.ErrNodeIDInUse)
		}
		seen[meta.NodeID] = struct{}{}
	}
	return nil
}

func (t *Transport) validateAdmissionPeer(member *hashimemberlist.Node) (nodeMeta, error) {
	if member == nil {
		return nodeMeta{}, fmt.Errorf("%w: member is missing", errAdmissionMetadataInvalid)
	}
	meta, err := decodeAdmissionNodeMeta(member.Meta)
	if err != nil {
		return nodeMeta{}, err
	}
	if meta.NodeID != member.Name {
		return nodeMeta{}, errAdmissionIdentityMismatch
	}
	if strings.TrimSpace(meta.ServerVersion) == "" {
		return nodeMeta{}, errAdmissionServerVersionMissing
	}
	if meta.ProtocolMin <= 0 || meta.ProtocolMax < meta.ProtocolMin {
		return nodeMeta{}, errAdmissionProtocolInvalid
	}
	if !protocolsCompatible(
		supportedProtocolMin,
		supportedProtocolMax,
		meta.ProtocolMin,
		meta.ProtocolMax,
	) {
		return nodeMeta{}, errAdmissionProtocolIncompatible
	}
	return meta, nil
}

// admissionDelegate applies the same peer contract to initial joins, later
// gossip, and cluster merges. Memberlist itself owns duplicate-name conflict
// resolution; the conflict callback adds bounded diagnostics.
type admissionDelegate struct {
	transport *Transport

	mu         sync.Mutex
	starting   bool
	startupErr error
}

func newAdmissionDelegate(transport *Transport) *admissionDelegate {
	return &admissionDelegate{transport: transport, starting: true}
}

func (d *admissionDelegate) NotifyAlive(peer *hashimemberlist.Node) error {
	_, err := d.transport.validateAdmissionPeer(peer)
	if err != nil {
		d.reject(err)
	}
	return err
}

func (d *admissionDelegate) NotifyMerge(peers []*hashimemberlist.Node) error {
	err := d.transport.validateAdmissionPeerSet(peers, nil)
	if err != nil {
		d.reject(err)
	}
	return err
}

func (d *admissionDelegate) NotifyConflict(_, _ *hashimemberlist.Node) {
	d.transport.cfg.Logger.ErrorContext(
		context.Background(),
		"duplicate cluster node identity rejected",
		cluster.ErrNodeIDInUse,
	)
}

func (d *admissionDelegate) reject(err error) {
	d.mu.Lock()
	if d.starting && d.startupErr == nil {
		d.startupErr = err
	}
	d.mu.Unlock()
	d.transport.cfg.Logger.ErrorContext(
		context.Background(),
		"cluster peer admission rejected",
		err,
	)
}

func (d *admissionDelegate) finishStartup() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.starting = false
	return d.startupErr
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
