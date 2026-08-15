// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only
//
// Realtime publication is substantially adapted from Mattermost revision
// 10b780cb097b2ec94ab0f9df7ebcbd5b7850f13f, principally
// server/channels/app/platform/cluster_handlers.go and
// server/channels/app/platform/web_hub.go, governed by Mattermost's GNU Affero
// General Public License v3. Proctor preserves local-first, peer-local-only
// delivery while using transport-neutral event intents and composition-owned
// wire adapters. The server NOTICE records the complete provenance.

// Package realtime owns transport-neutral realtime events and connection-close
// reasons, local-first delivery, the stable peer-propagation names and codecs,
// loop prevention, session revocation and authentication or authorization
// invalidation propagation, bounded manager and candidate examination facts,
// exact Attempt-Connection unbinding, required security-collaborator
// construction, and thread-safe, attach-once sink and peer-fanout wiring.
//
// Construction is inert. The package starts no goroutines, owns no queue or
// retry loop, and has no infrastructure lifecycle. Callers remain responsible
// for publishing only after durable commit and for translating typed failures
// into public application errors. WebSocket authorization, Principal
// validation, cluster envelopes, and transport lifecycle remain outside this
// package.
//
// The package depends inward only on model and consumer-owned ports. It never
// imports its parent app package or concrete WebSocket and cluster adapters.
package realtime
