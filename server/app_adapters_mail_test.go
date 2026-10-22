// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package server

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	mailpkg "github.com/sudosylabs/proctor/packages/mail"
	"github.com/sudosylabs/proctor/server/app"
	appmail "github.com/sudosylabs/proctor/server/app/mail"
	"github.com/sudosylabs/proctor/server/model"
)

type portableMailOutcomeError struct{ outcome string }

func (e portableMailOutcomeError) Error() string       { return "transport failed" }
func (e portableMailOutcomeError) MailOutcome() string { return e.outcome }

func TestAccountMailerAdapterClassifiesPortableAndLegacyFailures(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		err  error
		want appmail.TransportOutcome
	}{
		{name: "portable temporary", err: portableMailOutcomeError{outcome: "temporary"}, want: appmail.TransportTemporary},
		{name: "portable permanent", err: portableMailOutcomeError{outcome: "permanent"}, want: appmail.TransportPermanent},
		{name: "portable uncertain", err: portableMailOutcomeError{outcome: "acceptance_uncertain"}, want: appmail.TransportAcceptanceUncertain},
		{name: "legacy temporary", err: mailpkg.ErrConnection, want: appmail.TransportTemporary},
		{name: "legacy permanent", err: mailpkg.ErrRejected, want: appmail.TransportPermanent},
		{name: "unknown", err: errors.New("unknown"), want: appmail.TransportUnknown},
		{name: "relay TLS", err: mailpkg.ErrTLS, want: appmail.TransportTemporary},
		{name: "relay authentication", err: mailpkg.ErrAuthentication, want: appmail.TransportTemporary},
		{name: "relay TLS annotated permanent", err: mailpkg.WithOutcome(mailpkg.OutcomePermanent, mailpkg.ErrTLS), want: appmail.TransportTemporary},
		{name: "relay authentication annotated permanent", err: mailpkg.WithOutcome(mailpkg.OutcomePermanent, mailpkg.ErrAuthentication), want: appmail.TransportTemporary},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, test.want, classifyMailTransportError(fmt.Errorf("wrapped: %w", test.err)), "classifyMailTransportError()")
		})
	}
}

func TestProductionMailTelemetryRetainsBoundedSafeMetrics(t *testing.T) {
	t.Parallel()
	recorder, reader := newMailTelemetry(nil, nil)
	operational, ok := reader.(*operationalMailTelemetry)
	require.True(t, ok, "production metrics reader type = %T", reader)
	require.Equal(t, recorder, operational, "production recorder type = %T, want shared operational telemetry", recorder)
	ctx := context.Background()
	key := mailDeliveryMetricKey{template: model.MailTemplateSystemTest, state: model.MailDeliveryQueued, code: "mail.transport.temporary"}
	attemptKey := mailDeliveryMetricKey{template: model.MailTemplateSystemTest, state: model.MailDeliverySending, code: "mail.attempt_started"}
	for range 2 {
		recorder.RecordMailDelivery(ctx, app.MailDeliveryMetric{
			TemplateKey: key.template, State: key.state, OutcomeCode: key.code,
			ProcessingLatency: time.Second,
		})
		recorder.RecordMailAttempt(ctx, app.MailAttemptMetric{TemplateKey: attemptKey.template, State: attemptKey.state})
	}
	recorder.RecordMailQueueSnapshot(ctx, []app.MailQueueMetric{{
		TemplateKey: key.template, State: key.state, OutcomeCode: key.code,
		Count: 7, OldestAge: 6 * time.Minute, HealthCode: app.MailHealthQueueDelayed,
	}})
	recorder.RecordMailHealth(ctx, app.MailHealthMetric{Code: app.MailHealthQueueDelayed})
	snapshot := reader.Snapshot()

	operational.mu.Lock()
	aggregate := operational.deliveries[key]
	attemptAggregate := operational.deliveries[attemptKey]
	require.Equal(t, uint64(2), aggregate.count, "operational telemetry = deliveries %#v queues %#v buckets %#v health", operational.deliveries, operational.queues, operational.queueBuckets)
	require.Equal(t, uint64(0), aggregate.attempts, "operational telemetry = deliveries %#v queues %#v buckets %#v health", operational.deliveries, operational.queues, operational.queueBuckets)
	require.Equal(t, 2*time.Second, aggregate.processingLatency, "operational telemetry = deliveries %#v queues %#v buckets %#v health", operational.deliveries, operational.queues, operational.queueBuckets)
	require.Equal(t, time.Second, aggregate.maximumLatency, "operational telemetry = deliveries %#v queues %#v buckets %#v health", operational.deliveries, operational.queues, operational.queueBuckets)
	require.Equal(t, uint64(0), attemptAggregate.count, "operational telemetry = deliveries %#v queues %#v buckets %#v health", operational.deliveries, operational.queues, operational.queueBuckets)
	require.Equal(t, uint64(2), attemptAggregate.attempts, "operational telemetry = deliveries %#v queues %#v buckets %#v health", operational.deliveries, operational.queues, operational.queueBuckets)
	require.Equal(t, int64(7), operational.queues[key].Count, "operational telemetry = deliveries %#v queues %#v buckets %#v health", operational.deliveries, operational.queues, operational.queueBuckets)
	require.Equal(t, "lt_15m0s", operational.queueBuckets[key], "operational telemetry = deliveries %#v queues %#v buckets %#v health", operational.deliveries, operational.queues, operational.queueBuckets)
	require.Equal(t, app.MailHealthQueueDelayed, operational.health, "operational telemetry = deliveries %#v queues %#v buckets %#v health", operational.deliveries, operational.queues, operational.queueBuckets)
	require.Len(t, snapshot.Deliveries, 2, "operational telemetry = deliveries %#v queues %#v buckets %#v health", operational.deliveries, operational.queues, operational.queueBuckets)
	require.Len(t, snapshot.Queues, 1, "operational telemetry = deliveries %#v queues %#v buckets %#v health", operational.deliveries, operational.queues, operational.queueBuckets)
	operational.mu.Unlock()
	recorder.RecordMailQueueSnapshot(ctx, nil)
	require.Empty(t, reader.Snapshot().Queues)
}

func TestAccountMailerAdapterEmbedsFrozenLogoWithoutRewritingHTML(t *testing.T) {
	t.Parallel()
	files, err := runtimeAssetDirectory("templates")
	require.NoError(t, err)
	assets, err := appmail.NewInlineAssets(files)
	require.NoError(t, err)
	localizer, err := NewEmbeddedLocalizer()
	require.NoError(t, err)
	renderer, err := appmail.NewRenderer(files, localizer)
	require.NoError(t, err)
	content, err := renderer.Render(appmail.RenderRequest{Key: model.MailTemplateSystemTest, Locale: "en"})
	require.NoError(t, err)
	transport := &inlineRecordingMailer{}
	adapter := accountMailerAdapter{mailer: transport, assets: assets}
	outbound := appmail.Outbound{From: appmail.Address{Address: "from@example.test"}, To: appmail.Address{Address: "to@example.test"},
		EnvelopeFrom: "from@example.test", Subject: content.Subject, Text: content.Text, HTML: content.HTML,
		MessageID: "<fixed@example.test>", Date: time.Date(2026, 9, 6, 12, 0, 0, 0, time.UTC)}
	for range 2 {
		_, sendErr := adapter.Send(t.Context(), outbound)
		require.NoError(t, sendErr)
	}
	require.Len(t, transport.messages, 2, "transport did not receive both attempts")
	want, err := files.Open("proctor-lockup-25d-v1.png")
	require.NoError(t, err)
	defer want.Close()
	wantBytes, err := io.ReadAll(want)
	require.NoError(t, err)
	for _, message := range transport.messages {
		require.Equal(t, outbound.HTML, message.HTML, "adapter rewrote frozen content or omitted its inline image")
		require.Equal(t, outbound.MessageID, message.MessageID, "adapter rewrote frozen content or omitted its inline image")
		require.Len(t, message.Attachments, 1, "adapter rewrote frozen content or omitted its inline image")
		image := message.Attachments[0]
		require.True(t, image.Inline, "inline image does not match the frozen HTML")
		require.Equal(t, "image/png", image.ContentType, "inline image does not match the frozen HTML")
		require.True(t, bytes.Equal(image.Data, wantBytes), "inline image does not match the frozen HTML")
		require.Contains(t, message.HTML, `src="cid:`+image.ContentID+`"`, "inline image does not match the frozen HTML")
		composer, err := mailpkg.NewComposer(mailpkg.ComposerConfig{MessageIDDomain: "example.test"})
		require.NoError(t, err)
		delivery, err := composer.Compose(message)
		require.NoError(t, err)
		require.True(t, bytes.Contains(delivery.Data, []byte("multipart/related")))
		require.True(t, bytes.Contains(delivery.Data, []byte("<"+image.ContentID+">")))
	}
}

func TestAccountMailerAdapterRejectsUnknownImageBeforeSending(t *testing.T) {
	t.Parallel()
	files, err := runtimeAssetDirectory("templates")
	require.NoError(t, err)
	assets, err := appmail.NewInlineAssets(files)
	require.NoError(t, err)
	transport := &inlineRecordingMailer{}
	adapter := accountMailerAdapter{mailer: transport, assets: assets}
	outcome, err := adapter.Send(context.Background(), appmail.Outbound{HTML: `<img src="cid:missing">`})
	require.Error(t, err, "unknown image reached transport")
	require.Equal(t, appmail.TransportPermanent, outcome, "unknown image reached transport")
	require.Empty(t, transport.messages, "unknown image reached transport")
	_, err = adapter.Send(context.Background(), appmail.Outbound{HTML: `<img src="proctor-lockup.png">`})
	require.Error(t, err, "relative image reached transport")
	require.Empty(t, transport.messages, "relative image reached transport")
}

type inlineRecordingMailer struct{ messages []mailpkg.Message }

func (*inlineRecordingMailer) Enabled() bool { return true }
func (*inlineRecordingMailer) From() mailpkg.Address {
	return mailpkg.Address{Address: "from@example.test"}
}
func (m *inlineRecordingMailer) Send(_ context.Context, message mailpkg.Message) (mailpkg.Receipt, error) {
	m.messages = append(m.messages, message)
	return mailpkg.Receipt{}, nil
}
func (*inlineRecordingMailer) Test(context.Context) error { return nil }
