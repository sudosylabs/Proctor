// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"context"
	"testing"
	"time"

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
	if err != nil {
		t.Fatal(err)
	}
	reviewID := model.NewSubmissionReviewID()
	details := ResultReleaseMailDetails{ExamTitle: "Algorithms", ReleasedAt: at}
	prepared, err := preparer.PrepareResultReleaseMail(ResultReleaseDirectMailPreparation{Recipient: recipient,
		OccurrenceID: model.MailOccurrenceID(reviewID.String()), Details: details, ReleasedAt: at})
	if err != nil {
		t.Fatal(err)
	}
	if prepared.Occurrence.Kind != model.MailOccurrenceResultRelease ||
		prepared.Occurrence.ID != model.MailOccurrenceID(reviewID.String()) ||
		prepared.Occurrence.ActorUserID != recipient.ID || prepared.Delivery.TargetUserID != recipient.ID ||
		prepared.Delivery.TemplateKey != model.MailTemplateExamResultReleased ||
		prepared.Delivery.Deadline.Sub(prepared.Delivery.CreatedAt) != 72*time.Hour || renderer.details != details {
		t.Fatalf("prepared=%#v details=%#v", prepared, renderer.details)
	}
	if relevance, relevanceErr := evaluateMailDeliveryRelevance(context.Background(), prepared.Delivery); relevanceErr != nil || relevance != mailDeliveryRelevant {
		t.Fatalf("release relevance=%v, %v", relevance, relevanceErr)
	}
}

func TestResultReleaseMailPreparerSuppressesInactiveCandidate(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 8, 21, 14, 0, 0, 0, time.UTC)
	recipient := &model.User{Username: "candidate", Email: "candidate@example.edu", DisplayName: "Candidate"}
	recipient.PrepareCreate(model.NewUserID(), at.Add(-time.Hour))
	recipient.DisabledAt = model.OptionalTimeFrom(at.Add(-time.Minute))
	preparer, err := newDirectMailPreparer(&resultReleaseRendererFake{}, &mailSenderFake{enabled: true,
		from: MailAddress{Name: "Proctor", Address: "no-reply@example.test"}}, mailTestSealer(t))
	if err != nil {
		t.Fatal(err)
	}
	prepared, err := preparer.PrepareResultReleaseMail(ResultReleaseDirectMailPreparation{Recipient: recipient,
		OccurrenceID: model.MailOccurrenceID(model.NewSubmissionReviewID().String()),
		Details:      ResultReleaseMailDetails{ExamTitle: "Algorithms", ReleasedAt: at}, ReleasedAt: at})
	if err != nil {
		t.Fatal(err)
	}
	if prepared.Delivery.State != model.MailDeliverySuppressed ||
		prepared.Delivery.PublicFailureCode != model.MailDeliveryRecipientIneligibleCode {
		t.Fatalf("inactive result mail = %#v", prepared.Delivery)
	}
}

type resultReleaseRendererFake struct {
	mailRendererFake
	details ResultReleaseMailDetails
}

func (renderer *resultReleaseRendererFake) RenderResultRelease(_ model.MailTemplateKey, _ string,
	details ResultReleaseMailDetails,
) (FrozenMailContent, error) {
	renderer.details = details
	return FrozenMailContent{Subject: "Result available", Text: details.ExamTitle, HTML: "<p>Result available</p>"}, nil
}
