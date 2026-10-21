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

func TestSubmissionReceiptMailPreparerCreatesBoundedReceipt(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 8, 21, 9, 30, 0, 0, time.UTC)
	recipient := &model.User{Username: "candidate", Email: "candidate@example.edu", DisplayName: "Candidate"}
	recipient.PrepareCreate(model.NewUserID(), at.Add(-time.Hour))
	renderer := &submissionReceiptRendererFake{}
	preparer, err := newDirectMailPreparer(renderer, &mailSenderFake{enabled: true,
		from: MailAddress{Name: "Proctor", Address: "no-reply@example.test"}}, mailTestSealer(t))
	require.NoError(t, err)
	details := SubmissionReceiptMailDetails{ExamTitle: "Algorithms", SittingID: model.NewExamSittingID(),
		SubmissionID: model.NewSubmissionID(), SealedAt: at}
	prepared, err := preparer.PrepareSubmissionReceiptMail(SubmissionReceiptMailPreparation{Recipient: recipient,
		OccurrenceID: model.MailOccurrenceID(details.SubmissionID.String()), TemplateKey: model.MailTemplateExamSubmissionReceived,
		Details: details, ActionAt: at})
	require.NoError(t, err)
	require.Equal(t, model.MailOccurrenceSubmissionReceipt, prepared.Occurrence.Kind, "prepared=%#v details", prepared)
	require.Equal(t, recipient.ID, prepared.Occurrence.ActorUserID, "prepared=%#v details", prepared)
	require.Equal(t, recipient.ID, prepared.Delivery.TargetUserID, "prepared=%#v details", prepared)
	require.Equal(t, model.MailTemplateExamSubmissionReceived, prepared.Delivery.TemplateKey, "prepared=%#v details", prepared)
	require.Equal(t, 72*time.Hour, prepared.Delivery.Deadline.Sub(prepared.Delivery.CreatedAt), "prepared=%#v details", prepared)
	require.Equal(t, model.JobTypeMailDeliver, prepared.Job.Type, "prepared=%#v details", prepared)
	require.Equal(t, details, renderer.details, "prepared=%#v details", prepared)
	relevance, relevanceErr := evaluateMailDeliveryRelevance(context.Background(), prepared.Delivery)
	require.NoError(t, relevanceErr, "receipt relevance")
	require.Equal(t, mailDeliveryRelevant, relevance, "receipt relevance")
}

func TestSubmissionReceiptMailPreparerSuppressesInactiveRecipient(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 8, 21, 9, 30, 0, 0, time.UTC)
	recipient := &model.User{Username: "candidate", Email: "candidate@example.edu", DisplayName: "Candidate"}
	recipient.PrepareCreate(model.NewUserID(), at.Add(-time.Hour))
	recipient.DisabledAt = model.OptionalTimeFrom(at.Add(-time.Minute))
	renderer := &submissionReceiptRendererFake{}
	preparer, err := newDirectMailPreparer(renderer, &mailSenderFake{enabled: true,
		from: MailAddress{Name: "Proctor", Address: "no-reply@example.test"}}, mailTestSealer(t))
	require.NoError(t, err)
	submissionID := model.NewSubmissionID()
	prepared, err := preparer.PrepareSubmissionReceiptMail(SubmissionReceiptMailPreparation{Recipient: recipient,
		OccurrenceID: model.MailOccurrenceID(submissionID.String()), TemplateKey: model.MailTemplateExamSubmissionAutomaticallySealed,
		Details: SubmissionReceiptMailDetails{ExamTitle: "Algorithms", SittingID: model.NewExamSittingID(),
			SubmissionID: submissionID, SealedAt: at}, ActionAt: at})
	require.NoError(t, err)
	require.Equal(t, model.MailDeliverySuppressed, prepared.Delivery.State, "suppressed=%#v renderer calls", prepared.Delivery)
	require.Equal(t, model.MailDeliveryRecipientIneligibleCode, prepared.Delivery.PublicFailureCode, "suppressed=%#v renderer calls", prepared.Delivery)
	require.Equal(t, 0, renderer.calls, "suppressed=%#v renderer calls", prepared.Delivery)
}

type submissionReceiptRendererFake struct {
	mailRendererFake
	details SubmissionReceiptMailDetails
	calls   int
}

func (r *submissionReceiptRendererFake) Render(request MailRenderRequest) (FrozenMailContent, error) {
	details, ok := request.Presentation.(SubmissionReceiptMailDetails)
	if !ok {
		return r.mailRendererFake.Render(request)
	}
	r.calls++
	r.details = details
	return FrozenMailContent{Subject: "Receipt", Text: details.SubmissionID.String(), HTML: "<p>Receipt</p>"}, nil
}
