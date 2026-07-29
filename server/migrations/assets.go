// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: Apache-2.0
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
