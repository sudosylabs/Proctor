// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package memberlist

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	hashimemberlist "github.com/hashicorp/memberlist"

	"github.com/sudosylabs/proctor/server/cluster"
)

func encodedAdmissionMeta(t *testing.T, meta nodeMeta) []byte {
	t.Helper()
	payload, err := json.Marshal(meta)
	if err != nil {
		t.Fatal(err)
	}
	return payload
}

func admissionPeer(t *testing.T, name, version string, protocolMin, protocolMax int) *hashimemberlist.Node {
	t.Helper()
	return &hashimemberlist.Node{
		Name: name,
		Meta: encodedAdmissionMeta(t, nodeMeta{
			NodeID:        name,
			ServerVersion: version,
			ProtocolMin:   protocolMin,
			ProtocolMax:   protocolMax,
		}),
	}
}

func TestJoinedPeerAdmissionAcceptsExactLocalAndInclusiveProtocolEndpoints(t *testing.T) {
	t.Parallel()

	transport := &Transport{cfg: Config{NodeID: "node-local", ProtocolMin: 2, ProtocolMax: 4}}
	local := &hashimemberlist.Node{Name: "node-local", Meta: []byte("ignored local metadata")}
	members := []*hashimemberlist.Node{
		local,
		admissionPeer(t, "node-low", "v1", 1, 2),
		admissionPeer(t, "node-high", "v2", 4, 5),
		{
			Name: "node-future-fields",
			Meta: []byte(`{"node_id":"node-future-fields","server_version":"v3","protocol_min":2,"protocol_max":4,"future":true}`),
		},
	}

	if err := transport.admitJoinedPeers(local, members); err != nil {
		t.Fatal(err)
	}
}

func TestJoinedPeerAdmissionRejectsInvalidPeerSetByCategory(t *testing.T) {
	t.Parallel()

	malformedSecret := "malformed-secret-that-must-not-appear"
	tests := []struct {
		name       string
		members    func(*testing.T, *hashimemberlist.Node) []*hashimemberlist.Node
		want       error
		wantDetail string
	}{
		{
			name: "nil peer",
			members: func(_ *testing.T, local *hashimemberlist.Node) []*hashimemberlist.Node {
				return []*hashimemberlist.Node{local, nil}
			},
			want:       errAdmissionMetadataInvalid,
			wantDetail: "member is missing",
		},
		{
			name: "missing metadata",
			members: func(_ *testing.T, local *hashimemberlist.Node) []*hashimemberlist.Node {
				return []*hashimemberlist.Node{local, {Name: "node-a"}}
			},
			want:       errAdmissionMetadataInvalid,
			wantDetail: "metadata is missing",
		},
		{
			name: "malformed metadata",
			members: func(_ *testing.T, local *hashimemberlist.Node) []*hashimemberlist.Node {
				return []*hashimemberlist.Node{local, {Name: "node-a", Meta: []byte("{" + malformedSecret)}}
			},
			want:       errAdmissionMetadataInvalid,
			wantDetail: "malformed JSON",
		},
		{
			name: "missing metadata identity",
			members: func(t *testing.T, local *hashimemberlist.Node) []*hashimemberlist.Node {
				return []*hashimemberlist.Node{local, {
					Name: "node-a",
					Meta: encodedAdmissionMeta(t, nodeMeta{ServerVersion: "v1", ProtocolMin: 2, ProtocolMax: 4}),
				}}
			},
			want:       errAdmissionMetadataInvalid,
			wantDetail: "node identity is missing",
		},
		{
			name: "identity does not match member name",
			members: func(t *testing.T, local *hashimemberlist.Node) []*hashimemberlist.Node {
				peer := admissionPeer(t, "node-a", "v1", 2, 4)
				peer.Name = "node-b"
				return []*hashimemberlist.Node{local, peer}
			},
			want: errAdmissionIdentityMismatch,
		},
		{
			name: "distinct peer repeats local name",
			members: func(t *testing.T, local *hashimemberlist.Node) []*hashimemberlist.Node {
				return []*hashimemberlist.Node{local, admissionPeer(t, "node-local", "v1", 2, 4)}
			},
			want:       cluster.ErrNodeIDInUse,
			wantDetail: "peer duplicates local identity",
		},
		{
			name: "remote identity repeats",
			members: func(t *testing.T, local *hashimemberlist.Node) []*hashimemberlist.Node {
				return []*hashimemberlist.Node{
					local,
					admissionPeer(t, "node-a", "v1", 2, 4),
					admissionPeer(t, "node-a", "v2", 2, 4),
				}
			},
			want:       cluster.ErrNodeIDInUse,
			wantDetail: "peer identity is duplicated",
		},
		{
			name: "server version is blank",
			members: func(t *testing.T, local *hashimemberlist.Node) []*hashimemberlist.Node {
				return []*hashimemberlist.Node{local, admissionPeer(t, "node-a", " ", 2, 4)}
			},
			want: errAdmissionServerVersionMissing,
		},
		{
			name: "protocol minimum is invalid",
			members: func(t *testing.T, local *hashimemberlist.Node) []*hashimemberlist.Node {
				return []*hashimemberlist.Node{local, admissionPeer(t, "node-a", "v1", 0, 4)}
			},
			want: errAdmissionProtocolInvalid,
		},
		{
			name: "protocol maximum precedes minimum",
			members: func(t *testing.T, local *hashimemberlist.Node) []*hashimemberlist.Node {
				return []*hashimemberlist.Node{local, admissionPeer(t, "node-a", "v1", 4, 3)}
			},
			want: errAdmissionProtocolInvalid,
		},
		{
			name: "protocol range is below local",
			members: func(t *testing.T, local *hashimemberlist.Node) []*hashimemberlist.Node {
				return []*hashimemberlist.Node{local, admissionPeer(t, "node-a", "v1", 1, 1)}
			},
			want: errAdmissionProtocolIncompatible,
		},
		{
			name: "protocol range is above local",
			members: func(t *testing.T, local *hashimemberlist.Node) []*hashimemberlist.Node {
				return []*hashimemberlist.Node{local, admissionPeer(t, "node-a", "v1", 5, 6)}
			},
			want: errAdmissionProtocolIncompatible,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			transport := &Transport{cfg: Config{NodeID: "node-local", ProtocolMin: 2, ProtocolMax: 4}}
			local := &hashimemberlist.Node{Name: "node-local"}
			err := transport.admitJoinedPeers(local, test.members(t, local))
			if !errors.Is(err, test.want) {
				t.Fatalf("admission error = %v, want category %v", err, test.want)
			}
			if test.wantDetail != "" && !strings.Contains(err.Error(), test.wantDetail) {
				t.Fatalf("admission error = %q, want detail %q", err, test.wantDetail)
			}
			if strings.Contains(err.Error(), malformedSecret) {
				t.Fatalf("admission error exposed unbounded metadata: %q", err)
			}
		})
	}
}

func TestJoinedPeerAdmissionRequiresLocalMember(t *testing.T) {
	t.Parallel()

	transport := &Transport{cfg: Config{NodeID: "node-local", ProtocolMin: 2, ProtocolMax: 4}}
	err := transport.admitJoinedPeers(nil, nil)
	if !errors.Is(err, errAdmissionMetadataInvalid) || !strings.Contains(err.Error(), "local member is missing") {
		t.Fatalf("admission error = %v", err)
	}
}
