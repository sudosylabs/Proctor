// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

// Package resource owns the bounded mechanics of authored Exam Resources.
// It coordinates protected access, immutable content staging, Draft selection,
// ordering, audit, idempotency and post-commit effects. It depends only on
// domain models and consumer-owned persistence/content/security ports; it does
// not import its parent application package, HTTP, SQL, VFS or platform code.
// Published revision snapshots and candidate Sitting projection remain owned
// by the parent examination application.
package resource
