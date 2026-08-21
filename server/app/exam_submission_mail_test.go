// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"context"
	"testing"
	"time"

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
	if err != nil {
		t.Fatal(err)
	}
	details := SubmissionReceiptMailDetails{ExamTitle: "Algorithms", SittingID: model.NewExamSittingID(),
		SubmissionID: model.NewSubmissionID(), SealedAt: at}
	prepared, err := preparer.PrepareSubmissionReceiptMail(SubmissionReceiptMailPreparation{Recipient: recipient,
		OccurrenceID: model.MailOccurrenceID(details.SubmissionID.String()), TemplateKey: model.MailTemplateExamSubmissionReceived,
		Details: details, ActionAt: at})
	if err != nil {
		t.Fatal(err)
	}
	if prepared.Occurrence.Kind != model.MailOccurrenceSubmissionReceipt || prepared.Occurrence.ActorUserID != recipient.ID ||
		prepared.Delivery.TargetUserID != recipient.ID || prepared.Delivery.TemplateKey != model.MailTemplateExamSubmissionReceived ||
		prepared.Delivery.Deadline.Sub(prepared.Delivery.CreatedAt) != 72*time.Hour || prepared.Job.Type != model.JobTypeMailDeliver ||
		renderer.details != details {
		t.Fatalf("prepared=%#v details=%#v", prepared, renderer.details)
	}
	if relevance, relevanceErr := evaluateMailDeliveryRelevance(context.Background(), prepared.Delivery); relevanceErr != nil || relevance != mailDeliveryRelevant {
		t.Fatalf("receipt relevance=%v, %v", relevance, relevanceErr)
	}
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
	if err != nil {
		t.Fatal(err)
	}
	submissionID := model.NewSubmissionID()
	prepared, err := preparer.PrepareSubmissionReceiptMail(SubmissionReceiptMailPreparation{Recipient: recipient,
		OccurrenceID: model.MailOccurrenceID(submissionID.String()), TemplateKey: model.MailTemplateExamSubmissionAutomaticallySealed,
		Details: SubmissionReceiptMailDetails{ExamTitle: "Algorithms", SittingID: model.NewExamSittingID(),
			SubmissionID: submissionID, SealedAt: at}, ActionAt: at})
	if err != nil {
		t.Fatal(err)
	}
	if prepared.Delivery.State != model.MailDeliverySuppressed ||
		prepared.Delivery.PublicFailureCode != model.MailDeliveryRecipientIneligibleCode || renderer.calls != 0 {
		t.Fatalf("suppressed=%#v renderer calls=%d", prepared.Delivery, renderer.calls)
	}
}

type submissionReceiptRendererFake struct {
	mailRendererFake
	details SubmissionReceiptMailDetails
	calls   int
}

func (renderer *submissionReceiptRendererFake) Render(request MailRenderRequest) (FrozenMailContent, error) {
	details, ok := request.Presentation.(SubmissionReceiptMailDetails)
	if !ok {
		return renderer.mailRendererFake.Render(request)
	}
	renderer.calls++
	renderer.details = details
	return FrozenMailContent{Subject: "Receipt", Text: details.SubmissionID.String(), HTML: "<p>Receipt</p>"}, nil
}
