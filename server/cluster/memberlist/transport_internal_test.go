// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package memberlist

import (
	"context"
	"errors"
	"net"
	"strings"
	"testing"
)

func TestResolveAdvertiseHost(t *testing.T) {
	t.Parallel()

	transport := &Transport{
		lookupIP: func(_ context.Context, host string) ([]net.IPAddr, error) {
			if host != "node-a" {
				t.Fatalf("lookup host = %q", host)
			}
			return []net.IPAddr{
				{IP: net.ParseIP("2001:db8::1")},
				{IP: net.ParseIP("172.20.0.4")},
			}, nil
		},
	}

	resolved, err := transport.resolveAdvertiseHost(context.Background(), "node-a")
	if err != nil {
		t.Fatal(err)
	}
	if resolved != "172.20.0.4" {
		t.Fatalf("resolved host = %q", resolved)
	}
}

func TestResolveAdvertiseHostPreservesIPLiteral(t *testing.T) {
	t.Parallel()

	transport := &Transport{
		lookupIP: func(context.Context, string) ([]net.IPAddr, error) {
			t.Fatal("literal address triggered DNS lookup")
			return nil, nil
		},
	}

	resolved, err := transport.resolveAdvertiseHost(context.Background(), "127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	if resolved != "127.0.0.1" {
		t.Fatalf("resolved host = %q", resolved)
	}
}

func TestResolveAdvertiseHostFailsClosed(t *testing.T) {
	t.Parallel()

	transport := &Transport{
		lookupIP: func(context.Context, string) ([]net.IPAddr, error) {
			return nil, errors.New("DNS unavailable")
		},
	}

	_, err := transport.resolveAdvertiseHost(context.Background(), "node-a")
	if err == nil || !strings.Contains(err.Error(), "node-a") {
		t.Fatalf("resolve error = %v", err)
	}
}
