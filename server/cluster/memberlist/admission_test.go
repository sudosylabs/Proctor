// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package memberlist

import (
	"context"
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

func TestJoinedPeerAdmissionAcceptsCompiledProtocolAndUnknownMetadataFields(t *testing.T) {
	t.Parallel()

	transport := &Transport{cfg: Config{NodeID: "node-local"}}
	local := &hashimemberlist.Node{Name: "node-local", Meta: []byte("ignored local metadata")}
	members := []*hashimemberlist.Node{
		local,
		admissionPeer(t, "node-peer", "v1", 1, 1),
		{
			Name: "node-future-fields",
			Meta: []byte(`{"node_id":"node-future-fields","server_version":"v3","protocol_min":1,"protocol_max":1,"future":true}`),
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
					Meta: encodedAdmissionMeta(t, nodeMeta{ServerVersion: "v1", ProtocolMin: 1, ProtocolMax: 1}),
				}}
			},
			want:       errAdmissionMetadataInvalid,
			wantDetail: "node identity is missing",
		},
		{
			name: "identity does not match member name",
			members: func(t *testing.T, local *hashimemberlist.Node) []*hashimemberlist.Node {
				peer := admissionPeer(t, "node-a", "v1", 1, 1)
				peer.Name = "node-b"
				return []*hashimemberlist.Node{local, peer}
			},
			want: errAdmissionIdentityMismatch,
		},
		{
			name: "remote identity repeats",
			members: func(t *testing.T, local *hashimemberlist.Node) []*hashimemberlist.Node {
				return []*hashimemberlist.Node{
					local,
					admissionPeer(t, "node-a", "v1", 1, 1),
					admissionPeer(t, "node-a", "v2", 1, 1),
				}
			},
			want:       cluster.ErrNodeIDInUse,
			wantDetail: "peer identity is duplicated",
		},
		{
			name: "server version is blank",
			members: func(t *testing.T, local *hashimemberlist.Node) []*hashimemberlist.Node {
				return []*hashimemberlist.Node{local, admissionPeer(t, "node-a", " ", 1, 1)}
			},
			want: errAdmissionServerVersionMissing,
		},
		{
			name: "protocol minimum is invalid",
			members: func(t *testing.T, local *hashimemberlist.Node) []*hashimemberlist.Node {
				return []*hashimemberlist.Node{local, admissionPeer(t, "node-a", "v1", 0, 1)}
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
			name: "protocol range does not include compiled version",
			members: func(t *testing.T, local *hashimemberlist.Node) []*hashimemberlist.Node {
				return []*hashimemberlist.Node{local, admissionPeer(t, "node-a", "v1", 2, 3)}
			},
			want: errAdmissionProtocolIncompatible,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			transport := &Transport{cfg: Config{NodeID: "node-local"}}
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

	transport := &Transport{cfg: Config{NodeID: "node-local"}}
	err := transport.admitJoinedPeers(nil, nil)
	if !errors.Is(err, errAdmissionMetadataInvalid) || !strings.Contains(err.Error(), "local member is missing") {
		t.Fatalf("admission error = %v", err)
	}
}

func TestContinuousAdmissionRejectsLateAndMergedIncompatiblePeers(t *testing.T) {
	t.Parallel()

	transport := &Transport{cfg: Config{NodeID: "node-local", Logger: recordingAdmissionLogger{}}}
	admission := newAdmissionDelegate(transport)
	if err := admission.NotifyAlive(admissionPeer(t, "node-ok", "v1", 1, 1)); err != nil {
		t.Fatalf("compatible late peer: %v", err)
	}
	if err := admission.NotifyAlive(admissionPeer(t, "node-new", "v2", 2, 2)); !errors.Is(err, errAdmissionProtocolIncompatible) {
		t.Fatalf("late peer error = %v", err)
	}
	if err := admission.NotifyMerge([]*hashimemberlist.Node{
		admissionPeer(t, "node-a", "v1", 1, 1),
		admissionPeer(t, "node-a", "v1", 1, 1),
	}); !errors.Is(err, cluster.ErrNodeIDInUse) {
		t.Fatalf("merge error = %v", err)
	}
	if err := admission.finishStartup(); !errors.Is(err, errAdmissionProtocolIncompatible) {
		t.Fatalf("startup admission error = %v", err)
	}
}

func TestContinuousAdmissionValidatesSameNameAliveMetadata(t *testing.T) {
	t.Parallel()

	transport := &Transport{cfg: Config{NodeID: "node-local", Logger: recordingAdmissionLogger{}}}
	admission := newAdmissionDelegate(transport)
	err := admission.NotifyAlive(&hashimemberlist.Node{
		Name: "node-local",
		Meta: []byte("{malformed"),
	})
	if !errors.Is(err, errAdmissionMetadataInvalid) {
		t.Fatalf("same-name Alive error = %v, want invalid metadata", err)
	}
}

type recordingAdmissionLogger struct{}

func (recordingAdmissionLogger) ErrorContext(context.Context, string, error) {}
