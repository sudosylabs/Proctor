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
	"testing"

	hashimemberlist "github.com/hashicorp/memberlist"
	"github.com/stretchr/testify/require"
)

type routingSender struct{ bestEffort, reliable int }

func (s *routingSender) SendBestEffort(*hashimemberlist.Node, []byte) error {
	s.bestEffort++
	return nil
}

func (s *routingSender) SendReliable(*hashimemberlist.Node, []byte) error {
	s.reliable++
	return nil
}

func TestMemberSendFuncRoutesOnlyPacketSafeEnvelopesOverUDP(t *testing.T) {
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
			require.NoError(t, memberSendFunc(sender, test.payloadBytes, limit)(&hashimemberlist.Node{}, nil))
			require.Equal(t, test.wantReliable, (sender.reliable == 1 && sender.bestEffort == 0), "routing for %d bytes = best-effort:%d reliable:%d", test.payloadBytes, sender.bestEffort, sender.reliable)
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
