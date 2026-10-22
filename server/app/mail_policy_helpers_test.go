// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package app

import "context"

// NopMailDeliveryRecorder records nothing, for tests that need no mail
// metrics.
type NopMailDeliveryRecorder struct{}

func (NopMailDeliveryRecorder) RecordMailDelivery(context.Context, MailDeliveryMetric) {}

func (NopMailDeliveryRecorder) RecordMailAttempt(context.Context, MailAttemptMetric) {}

func (NopMailDeliveryRecorder) RecordMailQueueSnapshot(context.Context, []MailQueueMetric) {
}

func (NopMailDeliveryRecorder) RecordMailHealth(context.Context, MailHealthMetric) {}

func (NopMailDeliveryRecorder) Snapshot() MailMetricsSnapshot { return MailMetricsSnapshot{} }
