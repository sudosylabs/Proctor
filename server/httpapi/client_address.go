// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package httpapi

import (
	"net"
	"net/http"
	"net/netip"
	"strings"
)

// maximumForwardedHops bounds the X-Forwarded-For entries examined per
// request so a client cannot make address resolution unbounded.
const maximumForwardedHops = 32

// trustedProxies decides which connection peers may describe the original
// client. The zero value trusts no peer.
type trustedProxies []netip.Prefix

func (proxies trustedProxies) contains(address netip.Addr) bool {
	if !address.IsValid() {
		return false
	}
	address = address.Unmap()
	for _, prefix := range proxies {
		if prefix.Contains(address) {
			return true
		}
	}
	return false
}

// requestPeer is the connection peer and whether it is a trusted proxy.
type requestPeer struct {
	address netip.Addr
	raw     string
	trusted bool
}

func (proxies trustedProxies) peer(request *http.Request) requestPeer {
	raw := request.RemoteAddr
	if host, _, err := net.SplitHostPort(request.RemoteAddr); err == nil {
		raw = host
	}
	address, err := netip.ParseAddr(raw)
	if err != nil || address.Zone() != "" {
		return requestPeer{raw: raw}
	}
	address = address.Unmap()
	return requestPeer{address: address, raw: address.String(), trusted: proxies.contains(address)}
}

// clientAddress returns the address recorded as the request's client. A
// request from an untrusted peer is attributed to that peer and its
// forwarding headers are ignored. A request from a trusted proxy is
// attributed to the rightmost X-Forwarded-For hop that is not itself a
// trusted proxy; every hop to its right was appended by a trusted proxy, and
// hops to its left are client-controlled. When every hop is trusted the
// leftmost one is the client. A malformed hop ends the walk at the last
// trusted address, so a client cannot inject an arbitrary identity.
//
// The standardized Forwarded header is deliberately not consulted: a proxy
// that maintains only X-Forwarded-For would pass a client-forged Forwarded
// header through unchanged.
func (proxies trustedProxies) clientAddress(request *http.Request, peer requestPeer) string {
	if !peer.trusted {
		return peer.raw
	}
	hops := forwardedForHops(request.Header.Values("X-Forwarded-For"))
	client := peer.address
	for index := len(hops) - 1; index >= 0; index-- {
		hop, ok := parseForwardedHop(hops[index])
		if !ok {
			break
		}
		client = hop
		if !proxies.contains(hop) {
			break
		}
	}
	return client.String()
}

func forwardedForHops(values []string) []string {
	var hops []string
	for _, value := range values {
		for hop := range strings.SplitSeq(value, ",") {
			hops = append(hops, strings.TrimSpace(hop))
		}
	}
	if len(hops) > maximumForwardedHops {
		hops = hops[len(hops)-maximumForwardedHops:]
	}
	return hops
}

func parseForwardedHop(value string) (netip.Addr, bool) {
	if address, err := netip.ParseAddr(value); err == nil && address.Zone() == "" {
		return address.Unmap(), true
	}
	if addressPort, err := netip.ParseAddrPort(value); err == nil && addressPort.Addr().Zone() == "" {
		return addressPort.Addr().Unmap(), true
	}
	return netip.Addr{}, false
}
