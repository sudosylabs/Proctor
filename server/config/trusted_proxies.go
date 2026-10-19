// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package config

import (
	"fmt"
	"net/netip"
	"strings"
)

// maximumTrustedProxies bounds the per-request proxy membership scan.
const maximumTrustedProxies = 256

// ParseTrustedProxies parses Server.TrustedProxies. Each entry is an IP
// address or a CIDR prefix; an address is trusted as a single-host prefix.
// IPv4-mapped IPv6 forms are normalized to IPv4.
func ParseTrustedProxies(values []string) ([]netip.Prefix, error) {
	if len(values) > maximumTrustedProxies {
		return nil, fmt.Errorf("must contain at most %d entries", maximumTrustedProxies)
	}
	prefixes := make([]netip.Prefix, 0, len(values))
	for _, value := range values {
		if value == "" || strings.TrimSpace(value) != value {
			return nil, fmt.Errorf("entry %q must be an IP address or CIDR prefix", value)
		}
		if strings.Contains(value, "/") {
			prefix, err := netip.ParsePrefix(value)
			if err != nil || prefix.Addr().Zone() != "" {
				return nil, fmt.Errorf("entry %q must be an IP address or CIDR prefix", value)
			}
			if prefix.Addr().Is4In6() && prefix.Bits() >= 96 {
				prefix = netip.PrefixFrom(prefix.Addr().Unmap(), prefix.Bits()-96)
			}
			prefixes = append(prefixes, prefix.Masked())
			continue
		}
		address, err := netip.ParseAddr(value)
		if err != nil || address.Zone() != "" {
			return nil, fmt.Errorf("entry %q must be an IP address or CIDR prefix", value)
		}
		address = address.Unmap()
		prefixes = append(prefixes, netip.PrefixFrom(address, address.BitLen()))
	}
	return prefixes, nil
}
