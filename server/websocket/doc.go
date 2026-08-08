// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only
//
// Package websocket is the sibling transport boundary for Proctor's
// authenticated WebSocket protocol. It owns the hub, connections, wire DTOs,
// sequencing, bounded replay, origin policy, and upgrade acceptance.
//
// It deliberately does not own HTTP route registration, application
// authorization policy, or durable domain models. Composition mounts the hub
// on the HTTP API and attaches it as the application realtime sink
// (ADR-0025).
package websocket
