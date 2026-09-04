// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package memberlist

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	hashimemberlist "github.com/hashicorp/memberlist"
)

func TestNodeMetadataAdvertisesOnlyCompiledProtocolAndNeverTruncates(t *testing.T) {
	t.Parallel()

	transport := &Transport{cfg: Config{NodeID: "node-a", ServerVersion: "test"}}
	delegate := &delegate{transport: transport}
	payload := delegate.NodeMeta(hashimemberlist.MetaMaxSize)
	var meta nodeMeta
	if err := json.Unmarshal(payload, &meta); err != nil {
		t.Fatal(err)
	}
	if meta.ProtocolMin != supportedProtocolMin || meta.ProtocolMax != supportedProtocolMax {
		t.Fatalf("advertised protocol = %d..%d", meta.ProtocolMin, meta.ProtocolMax)
	}
	if got := delegate.NodeMeta(len(payload) - 1); got != nil {
		t.Fatalf("oversized metadata was truncated to %q", got)
	}
}

func TestTransportConfigurationCopiesAndValidatesKeyring(t *testing.T) {
	t.Parallel()

	primary := bytes.Repeat([]byte{1}, 32)
	fallback := bytes.Repeat([]byte{2}, 32)
	cfg := validInternalConfig(primary)
	cfg.DecryptionKeys = [][]byte{fallback}
	transport, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	primary[0] = 9
	fallback[0] = 9
	if transport.cfg.EncryptionKey[0] != 1 || transport.cfg.DecryptionKeys[0][0] != 2 {
		t.Fatal("transport configuration aliases caller key material")
	}

	duplicate := validInternalConfig(bytes.Repeat([]byte{3}, 32))
	duplicate.DecryptionKeys = [][]byte{bytes.Repeat([]byte{3}, 32)}
	if _, err := New(duplicate); err == nil || err.Error() != "cluster encryption keyring contains duplicate keys" {
		t.Fatalf("duplicate keyring error = %v", err)
	}
}

func TestTransportRejectsOversizedLocalMetadataBeforeStart(t *testing.T) {
	t.Parallel()

	cfg := validInternalConfig(bytes.Repeat([]byte{1}, 32))
	cfg.ServerVersion = strings.Repeat("v", 129)
	if _, err := New(cfg); err == nil || !strings.Contains(err.Error(), "server_version is invalid") {
		t.Fatalf("oversized metadata error = %v", err)
	}
}

func TestBroadcastMembersStopsOnCancellationAndPreservesSendErrors(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sendErr := errors.New("send failed")
	members := []*hashimemberlist.Node{
		{Name: "node-local"},
		{Name: "node-a"},
		{Name: "node-b"},
	}
	var sent []string
	err := broadcastMembers(
		ctx,
		"node-local",
		members,
		[]byte("payload"),
		func(member *hashimemberlist.Node, _ []byte) error {
			sent = append(sent, member.Name)
			cancel()
			return sendErr
		},
	)
	if !errors.Is(err, sendErr) || !errors.Is(err, context.Canceled) {
		t.Fatalf("broadcast error = %v, want send failure joined with cancellation", err)
	}
	if len(sent) != 1 || sent[0] != "node-a" {
		t.Fatalf("sent peers = %v, want only node-a", sent)
	}
}

func validInternalConfig(key []byte) Config {
	return Config{
		NodeID:             "node-a",
		BindAddress:        "127.0.0.1:7946",
		AdvertiseAddress:   "127.0.0.1:7946",
		EncryptionKey:      key,
		Discovery:          NewMemoryDiscovery(),
		DiscoveryTTL:       5 * time.Second,
		DiscoveryHeartbeat: time.Second,
		ServerVersion:      "test",
		Logger:             recordingAdmissionLogger{},
	}
}
