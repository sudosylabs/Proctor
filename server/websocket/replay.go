// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only
//
// This file contains a substantially modified adaptation of Mattermost's
// public WebSocket replay-queue flow. See server/NOTICE for exact provenance.

package websocket

import (
	"time"

	"github.com/sudosylabs/proctor/server/model"
)

type replayState struct {
	userID        string
	sessionID     string
	nextSequence  int64
	history       []*Event
	subscriptions map[string]Subscription
	expiresAt     time.Time
}

// connectionSnapshot is the immutable handoff from a stopped connection
// runtime to the Hub's replay catalog. The Hub decides whether to retain it.
type connectionSnapshot struct {
	id            string
	principal     model.Principal
	nextSequence  int64
	history       []*Event
	subscriptions map[string]Subscription
	replayable    bool
}

func (c *connection) finalSnapshot() connectionSnapshot {
	c.mu.Lock()
	defer c.mu.Unlock()
	return connectionSnapshot{
		id:            c.id,
		principal:     clonePrincipal(c.principal),
		nextSequence:  c.nextSequence,
		history:       cloneEvents(c.history),
		subscriptions: cloneSubscriptions(c.subscriptions),
		replayable:    c.replayable,
	}
}

func clonePrincipal(principal model.Principal) model.Principal {
	cloned := principal
	cloned.CredentialScopes = append([]string(nil), principal.CredentialScopes...)
	return cloned
}

func cloneEvents(events []*Event) []*Event {
	cloned := make([]*Event, 0, len(events))
	for _, event := range events {
		cloned = append(cloned, event.Clone())
	}
	return cloned
}

func cloneSubscriptions(
	subscriptions map[string]Subscription,
) map[string]Subscription {
	cloned := make(map[string]Subscription, len(subscriptions))
	for key, subscription := range subscriptions {
		cloned[key] = subscription
	}
	return cloned
}
