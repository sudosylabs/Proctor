// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

// Package devseed creates bounded synthetic development fixtures through the
// public HTTP API. It owns scenario data, a private recovery journal, and a
// loopback-only client; it never constructs a server or accesses its stores.
package devseed
