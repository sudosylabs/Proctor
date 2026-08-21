// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package jobs

import (
	"context"
	"encoding/base64"
	"errors"
	"testing"
	"time"

	jobengine "github.com/sudosylabs/proctor/server/app/job"
	appmail "github.com/sudosylabs/proctor/server/app/mail"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/secretseal"
	"github.com/sudosylabs/proctor/server/store"
)

func TestSittingMailExpansionPagesLargeRosterCheckpointsAndIsolatesOneRecipientConflict(t *testing.T) {
	at := time.Date(2026, 8, 20, 9, 0, 0, 0, time.UTC)
	preparer, err := appmail.NewSittingComposer(sittingMailRendererFake{}, &mailSenderFake{enabled: true,
		from: appmail.Address{Name: "Proctor", Address: "no-reply@example.test"}}, mailTestSealer(t), func() time.Time { return at })
	if err != nil {
		t.Fatal(err)
	}
	sitting, err := model.NewExamSitting(model.NewExamSittingID(), model.NewExamID(), model.NewExamRevisionID(), model.NewClassID(),
		at.Add(24*time.Hour), at.Add(26*time.Hour), at)
	if err != nil {
		t.Fatal(err)
	}
	prepared, err := preparer.Prepare(model.NewUserID(), sitting, store.ExamSittingMailScheduled,
		appmail.SittingScheduleDetails{ExamTitle: "Algorithms", ClassDisplayName: "Class A"})
	if err != nil {
		t.Fatal(err)
	}
	users := make([]*model.User, model.SittingMailExpansionPageSize+1)
	for index := range users {
		users[index] = mailTestUser(mailTestPrincipal(at), at)
	}
	storeFake := &sittingMailExpansionStoreFake{fanout: &store.ExamSittingMailFanoutSnapshot{Occurrence: prepared.Occurrence,
		Bundle: prepared.Bundle, SittingID: sitting.ID, SittingRevision: sitting.Revision, ChangeKind: store.ExamSittingMailScheduled,
		Deadline: at.Add(72 * time.Hour)}, users: users, conflictID: users[100].ID}
	var checkpoints []jobengine.CheckpointValue
	execution := jobengine.NewExecution(prepared.ExpansionJob, nil, func(_ context.Context, value jobengine.CheckpointValue) error {
		checkpoints = append(checkpoints, value)
		return nil
	}, nil)
	outcome := (sittingMailExpansionHandler{sittings: storeFake, mail: preparer}).Run(context.Background(), execution)
	if outcome.Kind != jobengine.OutcomeSucceeded || storeFake.completed != prepared.Occurrence.ID || len(storeFake.commits) != len(users) || len(checkpoints) != 2 {
		t.Fatalf("outcome=%#v completed=%s commits=%d checkpoints=%d", outcome, storeFake.completed, len(storeFake.commits), len(checkpoints))
	}
	result, err := model.DecodeSittingMailExpansionCheckpoint(1, checkpoints[len(checkpoints)-1].Document)
	if err != nil || result.Expanded != model.SittingMailExpansionPageSize || result.Suppressed != 1 || result.AfterUserID != users[len(users)-1].ID {
		t.Fatalf("checkpoint=%#v err=%v", result, err)
	}
	descriptor := sittingMailExpansionDescriptor(sittingMailExpansionHandler{})
	if descriptor.Type != model.JobTypeMailExpandSitting || descriptor.Concurrency != 2 || descriptor.CheckpointVersions[0] != 1 {
		t.Fatalf("descriptor=%#v", descriptor)
	}
}

func TestSittingMailExpansionLeaseLossReplaysRecipientCommitsWithoutDuplicates(t *testing.T) {
	at := time.Date(2026, 8, 20, 9, 0, 0, 0, time.UTC)
	preparer, err := appmail.NewSittingComposer(sittingMailRendererFake{}, &mailSenderFake{enabled: true,
		from: appmail.Address{Name: "Proctor", Address: "no-reply@example.test"}}, mailTestSealer(t), func() time.Time { return at })
	if err != nil {
		t.Fatal(err)
	}
	sitting, err := model.NewExamSitting(model.NewExamSittingID(), model.NewExamID(), model.NewExamRevisionID(), model.NewClassID(),
		at.Add(24*time.Hour), at.Add(26*time.Hour), at)
	if err != nil {
		t.Fatal(err)
	}
	prepared, err := preparer.Prepare(model.NewUserID(), sitting, store.ExamSittingMailScheduled,
		appmail.SittingScheduleDetails{ExamTitle: "Algorithms", ClassDisplayName: "Class A"})
	if err != nil {
		t.Fatal(err)
	}
	users := []*model.User{mailTestUser(mailTestPrincipal(at), at), mailTestUser(mailTestPrincipal(at), at)}
	persistence := &sittingMailExpansionStoreFake{fanout: &store.ExamSittingMailFanoutSnapshot{Occurrence: prepared.Occurrence,
		Bundle: prepared.Bundle, SittingID: sitting.ID, SittingRevision: sitting.Revision,
		ChangeKind: store.ExamSittingMailScheduled, Deadline: at.Add(72 * time.Hour)}, users: users}
	handler := sittingMailExpansionHandler{sittings: persistence, mail: preparer}
	leaseLost := errors.New("lease lost before checkpoint commit")
	first := handler.Run(context.Background(), jobengine.NewExecution(prepared.ExpansionJob, nil,
		func(context.Context, jobengine.CheckpointValue) error { return leaseLost }, nil))
	if first.Kind != jobengine.OutcomeRetryableFailure || !errors.Is(first.Err, leaseLost) || persistence.completed.IsValid() {
		t.Fatalf("first outcome=%#v completed=%s", first, persistence.completed)
	}
	second := handler.Run(context.Background(), jobengine.NewExecution(prepared.ExpansionJob, nil,
		func(context.Context, jobengine.CheckpointValue) error { return nil }, nil))
	if second.Kind != jobengine.OutcomeSucceeded || persistence.completed != prepared.Occurrence.ID ||
		len(persistence.inserted) != len(users) || len(persistence.commits) != len(users)*2 {
		t.Fatalf("second outcome=%#v completed=%s inserted=%d commits=%d", second, persistence.completed,
			len(persistence.inserted), len(persistence.commits))
	}
}

type sittingMailRendererFake struct{}

func (sittingMailRendererFake) RenderSittingScheduleNotice(key model.MailTemplateKey, _ string,
	details appmail.SittingScheduleDetails,
) (appmail.FrozenContent, error) {
	return appmail.FrozenContent{Subject: string(key), Text: details.ExamTitle + " " + details.ClassDisplayName,
		HTML: "<p>" + details.ExamTitle + " " + details.ClassDisplayName + "</p>"}, nil
}

type mailSenderFake struct {
	enabled bool
	from    appmail.Address
}

func (s *mailSenderFake) Enabled() bool         { return s.enabled }
func (s *mailSenderFake) From() appmail.Address { return s.from }
func (s *mailSenderFake) Send(context.Context, appmail.Outbound) (appmail.TransportOutcome, error) {
	return appmail.TransportUnknown, nil
}

func mailTestSealer(t *testing.T) *secretseal.Sealer {
	t.Helper()
	key := base64.StdEncoding.EncodeToString(make([]byte, 32))
	value, err := secretseal.New(secretseal.Settings{EncryptionKey: key, MaximumPlaintext: secretseal.MaximumPlaintextBytes})
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func mailTestPrincipal(at time.Time) model.Principal {
	return model.Principal{UserID: model.NewUserID(), SessionID: model.NewSessionID(), CredentialID: model.PrincipalCredentialID(model.NewSessionCredentialID()), CredentialType: model.CredentialSessionAccess, AuthenticationMethod: "password", AuthenticationStrength: model.AuthenticationSingleFactor, ClientType: model.SessionClientWeb, AuthenticatedAt: at}
}

func mailTestUser(principal model.Principal, at time.Time) *model.User {
	user := &model.User{Email: "operator@example.test", DisplayName: "Operator", EmailVerified: true, Locale: "en", Timezone: "UTC"}
	user.PrepareCreate(principal.UserID, at.Add(-time.Hour))
	return user
}

type sittingMailExpansionStoreFake struct {
	fanout     *store.ExamSittingMailFanoutSnapshot
	users      []*model.User
	conflictID model.UserID
	commits    []*store.ExamSittingMailRecipientCommit
	inserted   map[model.UserID]bool
	completed  model.MailOccurrenceID
}

func (fake *sittingMailExpansionStoreFake) GetMailFanout(context.Context, model.MailOccurrenceID) (*store.ExamSittingMailFanoutSnapshot, error) {
	return fake.fanout, nil
}

func (fake *sittingMailExpansionStoreFake) ListMailRecipients(_ context.Context,
	request store.ExamSittingMailRecipientPageRequest,
) (*store.ExamSittingMailRecipientPage, error) {
	start := 0
	if request.AfterUserID.IsValid() {
		for index := range fake.users {
			if fake.users[index].ID == request.AfterUserID {
				start = index + 1
				break
			}
		}
	}
	end := start + request.Limit
	if end > len(fake.users) {
		end = len(fake.users)
	}
	page := &store.ExamSittingMailRecipientPage{Fanout: fake.fanout, More: end < len(fake.users)}
	for _, user := range fake.users[start:end] {
		page.Recipients = append(page.Recipients, store.ExamSittingMailRecipient{User: user, TemplateKey: model.MailTemplateExamSittingScheduled})
	}
	return page, nil
}

func (fake *sittingMailExpansionStoreFake) CommitMailRecipient(_ context.Context,
	input *store.ExamSittingMailRecipientCommit,
) (*store.ExamSittingMailRecipientResult, error) {
	fake.commits = append(fake.commits, input)
	if input.Recipient.ID == fake.conflictID {
		return nil, store.NewErrConflict("exam_sitting_mail_recipient", "changed", errors.New("changed"))
	}
	if fake.inserted == nil {
		fake.inserted = make(map[model.UserID]bool)
	}
	if fake.inserted[input.Recipient.ID] {
		return &store.ExamSittingMailRecipientResult{Delivery: input.Delivery, Inserted: false}, nil
	}
	fake.inserted[input.Recipient.ID] = true
	return &store.ExamSittingMailRecipientResult{Delivery: input.Delivery, Inserted: true}, nil
}

func (fake *sittingMailExpansionStoreFake) CompleteMailExpansion(_ context.Context,
	input *store.ExamSittingMailExpansionCompletion,
) (*store.ExamSittingMailFanoutSnapshot, error) {
	fake.completed = input.OccurrenceID
	return fake.fanout, nil
}
