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
	"strings"
	"testing"
	"time"

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
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if got := classifyMailTransportError(fmt.Errorf("wrapped: %w", test.err)); got != test.want {
				t.Fatalf("classifyMailTransportError() = %q, want %q", got, test.want)
			}
		})
	}
}

func TestProductionMailTelemetryRetainsBoundedSafeMetrics(t *testing.T) {
	t.Parallel()
	recorder, reader := newMailTelemetry(nil, nil)
	operational, ok := reader.(*operationalMailTelemetry)
	if !ok {
		t.Fatalf("production metrics reader type = %T", reader)
	}
	if recorder != operational {
		t.Fatalf("production recorder type = %T, want shared operational telemetry", recorder)
	}
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
	if aggregate.count != 2 || aggregate.attempts != 0 || aggregate.processingLatency != 2*time.Second || aggregate.maximumLatency != time.Second ||
		attemptAggregate.count != 0 || attemptAggregate.attempts != 2 ||
		operational.queues[key].Count != 7 ||
		operational.queueBuckets[key] != "lt_15m0s" || operational.health != app.MailHealthQueueDelayed ||
		len(snapshot.Deliveries) != 2 || len(snapshot.Queues) != 1 {
		t.Fatalf("operational telemetry = deliveries %#v queues %#v buckets %#v health %q",
			operational.deliveries, operational.queues, operational.queueBuckets, operational.health)
	}
	operational.mu.Unlock()
	recorder.RecordMailQueueSnapshot(ctx, nil)
	if len(reader.Snapshot().Queues) != 0 {
		t.Fatalf("drained queue remained in snapshot: %#v", reader.Snapshot().Queues)
	}
}

func TestAccountMailerAdapterEmbedsFrozenLogoWithoutRewritingHTML(t *testing.T) {
	t.Parallel()
	files, err := runtimeAssetDirectory("templates")
	if err != nil {
		t.Fatal(err)
	}
	assets, err := appmail.NewInlineAssets(files)
	if err != nil {
		t.Fatal(err)
	}
	localizer, err := NewEmbeddedLocalizer()
	if err != nil {
		t.Fatal(err)
	}
	renderer, err := appmail.NewRenderer(files, localizer)
	if err != nil {
		t.Fatal(err)
	}
	content, err := renderer.Render(appmail.RenderRequest{Key: model.MailTemplateSystemTest, Locale: "en"})
	if err != nil {
		t.Fatal(err)
	}
	transport := &inlineRecordingMailer{}
	adapter := accountMailerAdapter{mailer: transport, assets: assets}
	outbound := appmail.Outbound{From: appmail.Address{Address: "from@example.test"}, To: appmail.Address{Address: "to@example.test"},
		EnvelopeFrom: "from@example.test", Subject: content.Subject, Text: content.Text, HTML: content.HTML,
		MessageID: "<fixed@example.test>", Date: time.Date(2026, 9, 6, 12, 0, 0, 0, time.UTC)}
	for range 2 {
		if _, err := adapter.Send(context.Background(), outbound); err != nil {
			t.Fatal(err)
		}
	}
	if len(transport.messages) != 2 {
		t.Fatal("transport did not receive both attempts")
	}
	want, err := files.Open("proctor-lockup-25d-v1.png")
	if err != nil {
		t.Fatal(err)
	}
	defer want.Close()
	wantBytes, err := io.ReadAll(want)
	if err != nil {
		t.Fatal(err)
	}
	for _, message := range transport.messages {
		if message.HTML != outbound.HTML || message.MessageID != outbound.MessageID || len(message.Attachments) != 1 {
			t.Fatal("adapter rewrote frozen content or omitted its inline image")
		}
		image := message.Attachments[0]
		if !image.Inline || image.ContentType != "image/png" || !bytes.Equal(image.Data, wantBytes) ||
			!strings.Contains(message.HTML, `src="cid:`+image.ContentID+`"`) {
			t.Fatal("inline image does not match the frozen HTML")
		}
		composer, err := mailpkg.NewComposer(mailpkg.ComposerConfig{MessageIDDomain: "example.test"})
		if err != nil {
			t.Fatal(err)
		}
		delivery, err := composer.Compose(message)
		if err != nil || !bytes.Contains(delivery.Data, []byte("multipart/related")) ||
			!bytes.Contains(delivery.Data, []byte("<"+image.ContentID+">")) {
			t.Fatalf("composed message omitted related image: %v", err)
		}
	}
}

func TestAccountMailerAdapterRejectsUnknownImageBeforeSending(t *testing.T) {
	t.Parallel()
	files, err := runtimeAssetDirectory("templates")
	if err != nil {
		t.Fatal(err)
	}
	assets, err := appmail.NewInlineAssets(files)
	if err != nil {
		t.Fatal(err)
	}
	transport := &inlineRecordingMailer{}
	adapter := accountMailerAdapter{mailer: transport, assets: assets}
	outcome, err := adapter.Send(context.Background(), appmail.Outbound{HTML: `<img src="cid:missing">`})
	if err == nil || outcome != appmail.TransportPermanent || len(transport.messages) != 0 {
		t.Fatalf("unknown image reached transport: %s, %v", outcome, err)
	}
	if _, err := adapter.Send(context.Background(), appmail.Outbound{HTML: `<img src="proctor-lockup.png">`}); err != nil ||
		len(transport.messages) != 1 || len(transport.messages[0].Attachments) != 0 {
		t.Fatalf("legacy frozen message was rejected or received different artwork: %v", err)
	}
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
