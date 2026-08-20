// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"testing"
	"time"

	"github.com/sudosylabs/proctor/server/model"
)

func TestClassTransitionMailPreparerCreatesOneBoundedOrdinaryNotice(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC)
	recipient := &model.User{Username: "student", Email: "student@example.edu", DisplayName: "Student", EmailVerified: true}
	recipient.PrepareCreate(model.NewUserID(), at.Add(-time.Hour))
	renderer := &classTransitionRendererFake{}
	preparer, err := newDirectMailPreparer(renderer, &mailSenderFake{enabled: true,
		from: MailAddress{Name: "Proctor", Address: "no-reply@example.test"}}, mailTestSealer(t))
	if err != nil {
		t.Fatal(err)
	}
	details := ClassTransitionMailDetails{PreviousClassDisplayName: `Old <Class>`, ClassDisplayName: `New & Class`,
		StartsAt: at.Add(24 * time.Hour), EndsAt: at.Add(180 * 24 * time.Hour)}
	prepared, err := preparer.PrepareClassTransition(ClassTransitionMailPreparation{Recipient: recipient,
		OccurrenceID: model.NewMailOccurrenceID(), TemplateKey: model.MailTemplateAcademicClassTransferred,
		Details: details, ActionAt: at})
	if err != nil {
		t.Fatal(err)
	}
	if prepared.Occurrence.Kind != model.MailOccurrenceAcademicAdministration || prepared.Occurrence.ActorUserID != recipient.ID ||
		prepared.Delivery.TargetUserID != recipient.ID || prepared.Delivery.TemplateKey != model.MailTemplateAcademicClassTransferred ||
		prepared.Delivery.Deadline.Sub(prepared.Delivery.CreatedAt) != 72*time.Hour || prepared.Job.Type != model.JobTypeMailDeliver {
		t.Fatalf("prepared Class transition mail = %#v", prepared)
	}
	if renderer.details != details {
		t.Fatalf("render details = %#v, want %#v", renderer.details, details)
	}
}

func TestClassTransitionMailPreparerRecordsDisabledSuppression(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC)
	recipient := &model.User{Username: "student", Email: "student@example.edu", DisplayName: "Student"}
	recipient.PrepareCreate(model.NewUserID(), at.Add(-time.Hour))
	recipient.DisabledAt = model.OptionalTimeFrom(at.Add(-time.Minute))
	renderer := &classTransitionRendererFake{}
	preparer, err := newDirectMailPreparer(renderer, &mailSenderFake{enabled: true,
		from: MailAddress{Name: "Proctor", Address: "no-reply@example.test"}}, mailTestSealer(t))
	if err != nil {
		t.Fatal(err)
	}
	prepared, err := preparer.PrepareClassTransition(ClassTransitionMailPreparation{
		Recipient: recipient, OccurrenceID: model.NewMailOccurrenceID(),
		TemplateKey: model.MailTemplateAcademicClassEnrolled,
		Details:     ClassTransitionMailDetails{ClassDisplayName: "Class A", StartsAt: at.Add(24 * time.Hour)},
		ActionAt:    at,
	})
	if err != nil {
		t.Fatal(err)
	}
	if prepared.Delivery.State != model.MailDeliverySuppressed ||
		prepared.Delivery.PublicFailureCode != model.MailDeliveryRecipientIneligibleCode ||
		len(prepared.Delivery.EncryptedPayload) != 0 || prepared.Job.Status != model.JobStatusCanceled {
		t.Fatalf("disabled Class transition mail = %#v / %#v", prepared.Delivery, prepared.Job)
	}
	if renderer.calls != 0 {
		t.Fatalf("renderer calls=%d, want 0", renderer.calls)
	}
}

type classTransitionRendererFake struct {
	details ClassTransitionMailDetails
	calls   int
}

func (r *classTransitionRendererFake) Render(model.MailTemplateKey, string, string, string) (FrozenMailContent, error) {
	return FrozenMailContent{}, nil
}
func (r *classTransitionRendererFake) RenderPersonalAccessTokenSecurityNotice(model.MailTemplateKey, string, string, PersonalAccessTokenMailDetails) (FrozenMailContent, error) {
	return FrozenMailContent{}, nil
}
func (r *classTransitionRendererFake) RenderExamManagerNotice(model.MailTemplateKey, string, string, ExamManagerMailDetails) (FrozenMailContent, error) {
	return FrozenMailContent{}, nil
}
func (r *classTransitionRendererFake) RenderClassTransitionNotice(_ model.MailTemplateKey, _, _ string, details ClassTransitionMailDetails) (FrozenMailContent, error) {
	r.calls++
	r.details = details
	return FrozenMailContent{Subject: "Class changed", Text: details.ClassDisplayName, HTML: "<p>Class changed</p>"}, nil
}
