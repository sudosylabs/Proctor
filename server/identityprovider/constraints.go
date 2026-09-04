// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

// Package identityprovider owns deployment-independent constraints shared by
// external-provider configuration and durable identity policy.
package identityprovider

// MaximumCount bounds provider definitions, policy admissions, and safe
// provider projections for one installation.
const MaximumCount = 64
