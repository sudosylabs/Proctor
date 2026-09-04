// ---------------------------------------------------------------------------------------------
// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// Modifications Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------
//
// Adapted from Mattermost server/channels/db/assets.go for Proctor's embedded
// PostgreSQL-only migrations.

// Package migrations embeds Proctor's versioned PostgreSQL schema migrations.
package migrations

import "embed"

// Assets contains both up and down migrations consumed by sqlstore.Migrator.
//
//go:embed postgres/*.sql
var Assets embed.FS
