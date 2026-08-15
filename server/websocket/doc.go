// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only
//
// Package websocket is the sibling transport module for Proctor's
// authenticated WebSocket protocol.
//
// Hub owns lifecycle, upgrade admission, connection limits, sharding,
// publication selection, and the bounded replay catalog. Each accepted socket
// is handed to a private connection runtime that owns its pumps, liveness,
// outbound queue, sequencing, subscriptions, backpressure, and close-once
// behavior. The runtime also owns strict authenticated Exam Attempt connect,
// renewal, and Focus Loss claim actions; a policy suspension removes the
// durable Attempt binding before another protected action can run. A committed
// Submission can likewise remove one exact durable Attempt binding and its
// candidate subscription while leaving the generic socket open. After every
// pump has stopped, the runtime returns one immutable snapshot; Hub alone
// decides whether that snapshot is eligible for local replay. Wire DTOs and
// validation remain separate protocol contracts.
//
// The package deliberately does not own HTTP route registration, application
// authorization policy, durable state, cross-node replay, or infrastructure
// selection. Composition mounts Hub on the HTTP transport and attaches it as
// the application Realtime sink.
package websocket
