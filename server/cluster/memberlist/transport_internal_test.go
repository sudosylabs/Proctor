// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package memberlist

import (
	"context"
	"errors"
	"net"
	"sync/atomic"
	"testing"
	"time"

	hashimemberlist "github.com/hashicorp/memberlist"
	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/server/cluster"
)

type routingSender struct{ bestEffort, reliable atomic.Int32 }

func (s *routingSender) SendBestEffort(*hashimemberlist.Node, []byte) error {
	s.bestEffort.Add(1)
	return nil
}

func (s *routingSender) SendReliable(*hashimemberlist.Node, []byte) error {
	s.reliable.Add(1)
	return nil
}

func TestSendToMemberRoutesOnlyPacketSafeEnvelopesOverUDP(t *testing.T) {
	t.Parallel()

	const limit = 1272
	for _, test := range []struct {
		name         string
		payloadBytes int
		wantReliable bool
	}{
		{"small", 64, false},
		{"exact packet limit", limit, false},
		{"one byte over packet limit", limit + 1, true},
		{"realtime event maximum", 256 << 10, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			sender := &routingSender{}
			transport := &Transport{handlers: map[cluster.Event]cluster.Handler{}, reliable: newReliableDelivery(sender.SendReliable)}
			payload := make([]byte, test.payloadBytes)
			require.NoError(t, transport.sendToMember(sender, &hashimemberlist.Node{Name: "node-b"}, payload, limit, "realtime.event"))
			want := [2]int32{1, 0}
			if test.wantReliable {
				want = [2]int32{0, 1}
			}
			require.Eventually(t, func() bool {
				return [2]int32{sender.bestEffort.Load(), sender.reliable.Load()} == want
			}, 5*time.Second, time.Millisecond, "routing for %d bytes", test.payloadBytes)
			transport.reliable.close()
			require.NoError(t, transport.reliable.wait(context.Background()))
		})
	}
}

func TestResolveAdvertiseHost(t *testing.T) {
	t.Parallel()

	transport := &Transport{
		lookupIP: func(_ context.Context, host string) ([]net.IPAddr, error) {
			require.Equal(t, "node-a", host)
			return []net.IPAddr{
				{IP: net.ParseIP("2001:db8::1")},
				{IP: net.ParseIP("172.20.0.4")},
			}, nil
		},
	}

	resolved, err := transport.resolveAdvertiseHost(context.Background(), "node-a")
	require.NoError(t, err)
	require.Equal(t, "172.20.0.4", resolved)
}

func TestResolveAdvertiseHostPreservesIPLiteral(t *testing.T) {
	t.Parallel()

	transport := &Transport{
		lookupIP: func(context.Context, string) ([]net.IPAddr, error) {
			require.FailNow(t, "literal address triggered DNS lookup")
			return nil, nil
		},
	}

	resolved, err := transport.resolveAdvertiseHost(context.Background(), "127.0.0.1")
	require.NoError(t, err)
	require.Equal(t, "127.0.0.1", resolved)
}

func TestResolveAdvertiseHostFailsClosed(t *testing.T) {
	t.Parallel()

	transport := &Transport{
		lookupIP: func(context.Context, string) ([]net.IPAddr, error) {
			return nil, errors.New("DNS unavailable")
		},
	}

	_, err := transport.resolveAdvertiseHost(context.Background(), "node-a")
	require.Error(t, err)
	require.Contains(t, err.Error(), "node-a")
}
