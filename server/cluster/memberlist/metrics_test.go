// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package memberlist

import (
	"context"
	"testing"

	"github.com/sudosylabs/proctor/server/cluster"
)

func TestMetricEventLabelsOnlyRegisteredHandlers(t *testing.T) {
	t.Parallel()

	transport := &Transport{handlers: map[cluster.Event]cluster.Handler{
		"registered.event": func(context.Context, *cluster.Message) error { return nil },
	}}
	if got := transport.metricEvent("registered.event"); got != "registered.event" {
		t.Fatalf("registered event metric label = %q", got)
	}
	if got := transport.metricEvent("peer.supplied"); got != "unregistered" {
		t.Fatalf("unregistered event metric label = %q", got)
	}
}
