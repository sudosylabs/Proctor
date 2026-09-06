// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

// Package manageraccess owns the current Exam Manager and exact Academic Unit
// membership rule for selecting ordinary or explicit override actions. An Exam
// Manager relationship alone grants no permission, and override never creates
// that relationship.
//
// Callers validate their inputs and access projections, choose the relevant
// Actions and Resource, and perform authoritative authorization. They retain
// error presentation, audit, transaction intent, and post-commit effects. This
// package performs no mutation or authorization and caches no eligibility. It
// depends only on model, the bounded Store access projection, a narrow current
// membership port, and the standard library.
package manageraccess
