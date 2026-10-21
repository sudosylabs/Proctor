// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package app

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/server/model"
)

func TestResultReleaseMailPreparerContainsOnlyAvailabilityFacts(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 8, 21, 14, 0, 0, 0, time.UTC)
	recipient := &model.User{Username: "candidate", Email: "candidate@example.edu", DisplayName: "Candidate"}
	recipient.PrepareCreate(model.NewUserID(), at.Add(-time.Hour))
	renderer := &resultReleaseRendererFake{}
	preparer, err := newDirectMailPreparer(renderer, &mailSenderFake{enabled: true,
		from: MailAddress{Name: "Proctor", Address: "no-reply@example.test"}}, mailTestSealer(t))
	require.NoError(t, err)
	reviewID := model.NewSubmissionReviewID()
	details := ResultReleaseMailDetails{ExamTitle: "Algorithms", ReleasedAt: at}
	prepared, err := preparer.PrepareResultReleaseMail(ResultReleaseDirectMailPreparation{Recipient: recipient,
		OccurrenceID: model.MailOccurrenceID(reviewID.String()), Details: details, ReleasedAt: at})
	require.NoError(t, err)
	require.Equal(t, model.MailOccurrenceResultRelease, prepared.Occurrence.Kind, "prepared=%#v details", prepared)
	require.Equal(t, model.MailOccurrenceID(reviewID.String()), prepared.Occurrence.ID, "prepared=%#v details", prepared)
	require.Equal(t, recipient.ID, prepared.Occurrence.ActorUserID, "prepared=%#v details", prepared)
	require.Equal(t, recipient.ID, prepared.Delivery.TargetUserID, "prepared=%#v details", prepared)
	require.Equal(t, model.MailTemplateExamResultReleased, prepared.Delivery.TemplateKey, "prepared=%#v details", prepared)
	require.Equal(t, 72*time.Hour, prepared.Delivery.Deadline.Sub(prepared.Delivery.CreatedAt), "prepared=%#v details", prepared)
	require.Equal(t, details, renderer.details, "prepared=%#v details", prepared)
	relevance, relevanceErr := evaluateMailDeliveryRelevance(context.Background(), prepared.Delivery)
	require.NoError(t, relevanceErr, "release relevance")
	require.Equal(t, mailDeliveryRelevant, relevance, "release relevance")
}

func TestResultReleaseMailPreparerSuppressesInactiveCandidate(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 8, 21, 14, 0, 0, 0, time.UTC)
	recipient := &model.User{Username: "candidate", Email: "candidate@example.edu", DisplayName: "Candidate"}
	recipient.PrepareCreate(model.NewUserID(), at.Add(-time.Hour))
	recipient.DisabledAt = model.OptionalTimeFrom(at.Add(-time.Minute))
	preparer, err := newDirectMailPreparer(&resultReleaseRendererFake{}, &mailSenderFake{enabled: true,
		from: MailAddress{Name: "Proctor", Address: "no-reply@example.test"}}, mailTestSealer(t))
	require.NoError(t, err)
	prepared, err := preparer.PrepareResultReleaseMail(ResultReleaseDirectMailPreparation{Recipient: recipient,
		OccurrenceID: model.MailOccurrenceID(model.NewSubmissionReviewID().String()),
		Details:      ResultReleaseMailDetails{ExamTitle: "Algorithms", ReleasedAt: at}, ReleasedAt: at})
	require.NoError(t, err)
	require.Equal(t, model.MailDeliverySuppressed, prepared.Delivery.State)
	require.Equal(t, model.MailDeliveryRecipientIneligibleCode, prepared.Delivery.PublicFailureCode)
}

type resultReleaseRendererFake struct {
	mailRendererFake
	details ResultReleaseMailDetails
}

func (r *resultReleaseRendererFake) Render(request MailRenderRequest) (FrozenMailContent, error) {
	details, ok := request.Presentation.(ResultReleaseMailDetails)
	if !ok {
		return r.mailRendererFake.Render(request)
	}
	r.details = details
	return FrozenMailContent{Subject: "Result available", Text: details.ExamTitle, HTML: "<p>Result available</p>"}, nil
}
