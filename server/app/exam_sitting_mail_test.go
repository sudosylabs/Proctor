// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"strings"
	"testing"
	"time"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

func TestSittingMailPreparerFreezesAllFourSafeRenderVariants(t *testing.T) {
	at := time.Date(2026, 8, 20, 9, 0, 0, 0, time.UTC)
	sitting, err := model.NewExamSitting(model.NewExamSittingID(), model.NewExamID(), model.NewExamRevisionID(), model.NewClassID(),
		at.Add(24*time.Hour), at.Add(26*time.Hour), at)
	if err != nil {
		t.Fatal(err)
	}
	renderer := sittingMailRendererFake{}
	preparer, err := newSittingMailPreparer(renderer, &mailSenderFake{enabled: true,
		from: MailAddress{Name: "Proctor", Address: "no-reply@example.test"}}, mailTestSealer(t), func() time.Time { return at })
	if err != nil {
		t.Fatal(err)
	}
	prepared, err := preparer.Prepare(model.NewUserID(), sitting, store.ExamSittingMailScheduled,
		SittingScheduleMailDetails{ExamTitle: "Algorithms", ClassDisplayName: "Class A"})
	if err != nil {
		t.Fatal(err)
	}
	if prepared.Occurrence.Kind != model.MailOccurrenceSittingSchedule || prepared.Bundle == nil ||
		prepared.ExpansionJob.Type != model.JobTypeMailExpandSitting || prepared.ExpansionJob.DedupePolicy != model.JobDedupePermanent {
		t.Fatalf("Prepare()=%#v", prepared)
	}
	for _, forbidden := range []string{"Algorithms", "Class A", "no-reply@example.test"} {
		if strings.Contains(string(prepared.Bundle.EncryptedPayload), forbidden) {
			t.Fatalf("encrypted bundle exposed %q", forbidden)
		}
	}
	opened, err := preparer.OpenBundle(prepared.Bundle)
	if err != nil || len(opened.Messages) != 4 || opened.Messages[model.MailTemplateExamSittingCancelled].Subject == "" {
		t.Fatalf("opened bundle=(%#v,%v)", opened, err)
	}
}

type sittingMailRendererFake struct{}

func (sittingMailRendererFake) RenderSittingScheduleNotice(key model.MailTemplateKey, _, _ string,
	details SittingScheduleMailDetails,
) (FrozenMailContent, error) {
	return FrozenMailContent{Subject: string(key), Text: details.ExamTitle + " " + details.ClassDisplayName,
		HTML: "<p>" + details.ExamTitle + " " + details.ClassDisplayName + "</p>"}, nil
}
