// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"testing"
	"time"

	examengine "github.com/sudosylabs/proctor/server/app/exam"
	"github.com/sudosylabs/proctor/server/model"
)

func TestExamManagerMailPreparerCreatesOneOrdinaryBoundedNotice(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC)
	user := &model.User{Username: "manager", Email: "manager@example.edu", DisplayName: "Exam Manager"}
	user.PrepareCreate(model.NewUserID(), at.Add(-time.Hour))
	renderer := &examManagerRendererFake{}
	preparer, err := newDirectMailPreparer(renderer, &mailSenderFake{enabled: true, from: MailAddress{Name: "Proctor", Address: "no-reply@example.test"}}, mailTestSealer(t))
	if err != nil {
		t.Fatal(err)
	}
	prepared, err := preparer.PrepareManagerMail(examengine.ManagerMailPreparation{
		Recipient: user, OccurrenceID: model.NewMailOccurrenceID(), TemplateKey: model.MailTemplateExamManagerAdded,
		ExamTitle: `Algorithms & <Systems>`, Relationship: examengine.ManagerMailRelationshipManager, ActionAt: at,
	})
	if err != nil {
		t.Fatal(err)
	}
	if prepared.Occurrence.Kind != model.MailOccurrenceExamManagement || prepared.Occurrence.ActorUserID != user.ID ||
		prepared.Delivery.TargetUserID != user.ID || prepared.Delivery.TemplateKey != model.MailTemplateExamManagerAdded ||
		prepared.Delivery.Deadline.Sub(prepared.Delivery.CreatedAt) != 72*time.Hour || prepared.Job.Type != model.JobTypeMailDeliver {
		t.Fatalf("prepared Exam Manager mail = %#v", prepared)
	}
	if renderer.details.Title != `Algorithms & <Systems>` || renderer.details.Relationship != string(examengine.ManagerMailRelationshipManager) || !renderer.details.ActionAt.Equal(at) {
		t.Fatalf("render details = %#v", renderer.details)
	}
}

type examManagerRendererFake struct{ details ExamManagerMailDetails }

func (r *examManagerRendererFake) Render(model.MailTemplateKey, string, string, string) (FrozenMailContent, error) {
	return FrozenMailContent{}, nil
}

func (r *examManagerRendererFake) RenderPersonalAccessTokenSecurityNotice(model.MailTemplateKey, string, string, PersonalAccessTokenMailDetails) (FrozenMailContent, error) {
	return FrozenMailContent{}, nil
}

func (r *examManagerRendererFake) RenderExamManagerNotice(_ model.MailTemplateKey, _, _ string, details ExamManagerMailDetails) (FrozenMailContent, error) {
	r.details = details
	return FrozenMailContent{Subject: "Exam changed", Text: details.Title, HTML: "<p>Exam changed</p>"}, nil
}

func (r *examManagerRendererFake) RenderClassTransitionNotice(model.MailTemplateKey, string, string, ClassTransitionMailDetails) (FrozenMailContent, error) {
	return FrozenMailContent{}, nil
}
