// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package jobhandler

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/server/app/job"
	"github.com/sudosylabs/proctor/server/app/mail"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/secretseal"
	"github.com/sudosylabs/proctor/server/store"
)

func TestSittingMailExpansionPagesLargeRosterCheckpointsAndIsolatesOneRecipientConflict(t *testing.T) {
	at := time.Date(2026, 8, 20, 9, 0, 0, 0, time.UTC)
	preparer, err := mail.NewSittingComposer(sittingMailRendererFake{}, &mailSenderFake{enabled: true,
		from: mail.Address{Name: "Proctor", Address: "no-reply@example.test"}}, mailTestSealer(t), func() time.Time { return at })
	require.NoError(t, err)
	sitting, err := model.NewExamSitting(model.NewExamSittingID(), model.NewExamID(), model.NewExamRevisionID(), model.NewClassID(),
		at.Add(24*time.Hour), at.Add(26*time.Hour), at)
	require.NoError(t, err)
	prepared, err := preparer.Prepare(model.NewUserID(), sitting, store.ExamSittingMailScheduled,
		mail.SittingScheduleDetails{ExamTitle: "Algorithms", ClassDisplayName: "Class A"})
	require.NoError(t, err)
	users := make([]*model.User, model.SittingMailExpansionPageSize+1)
	for index := range users {
		users[index] = mailTestUser(mailTestPrincipal(at), at)
	}
	storeFake := &sittingMailExpansionStoreFake{fanout: &store.ExamSittingMailFanoutSnapshot{Occurrence: prepared.Occurrence,
		Bundle: prepared.Bundle, SittingID: sitting.ID, SittingRevision: sitting.Revision, ChangeKind: store.ExamSittingMailScheduled,
		Deadline: at.Add(72 * time.Hour)}, users: users, conflictID: users[100].ID}
	var checkpoints []job.CheckpointValue
	execution := job.NewExecution(prepared.ExpansionJob, nil, func(_ context.Context, value job.CheckpointValue) error {
		checkpoints = append(checkpoints, value)
		return nil
	}, nil)
	outcome := (sittingMailExpansionHandler{sittings: storeFake, mail: preparer}).Run(context.Background(), execution)
	require.Equal(t, job.OutcomeSucceeded, outcome.Kind, "outcome=%#v completed=%s commits=%d checkpoints", outcome, storeFake.completed, len(storeFake.commits))
	require.Equal(t, prepared.Occurrence.ID, storeFake.completed, "outcome=%#v completed=%s commits=%d checkpoints", outcome, storeFake.completed, len(storeFake.commits))
	require.Len(t, storeFake.commits, len(users), "outcome=%#v completed=%s commits=%d checkpoints", outcome, storeFake.completed, len(storeFake.commits))
	require.Len(t, checkpoints, 2, "outcome=%#v completed=%s commits=%d checkpoints", outcome, storeFake.completed, len(storeFake.commits))
	result, err := model.DecodeSittingMailExpansionCheckpoint(checkpoints[len(checkpoints)-1].Document)
	require.NoError(t, err, "checkpoint=%#v err", result)
	require.Equal(t, int64(model.SittingMailExpansionPageSize), result.Expanded, "checkpoint=%#v err", result)
	require.Equal(t, int64(1), result.Suppressed, "checkpoint=%#v err", result)
	require.Equal(t, users[len(users)-1].ID, result.AfterUserID, "checkpoint=%#v err", result)
	descriptor := sittingMailExpansionDescriptor(sittingMailExpansionHandler{})
	require.Equal(t, model.JobTypeMailExpandSitting, descriptor.Type)
	require.Equal(t, 2, descriptor.Concurrency)
	require.True(t, descriptor.Checkpoints)
}

func TestSittingMailExpansionLeaseLossReplaysRecipientCommitsWithoutDuplicates(t *testing.T) {
	at := time.Date(2026, 8, 20, 9, 0, 0, 0, time.UTC)
	preparer, err := mail.NewSittingComposer(sittingMailRendererFake{}, &mailSenderFake{enabled: true,
		from: mail.Address{Name: "Proctor", Address: "no-reply@example.test"}}, mailTestSealer(t), func() time.Time { return at })
	require.NoError(t, err)
	sitting, err := model.NewExamSitting(model.NewExamSittingID(), model.NewExamID(), model.NewExamRevisionID(), model.NewClassID(),
		at.Add(24*time.Hour), at.Add(26*time.Hour), at)
	require.NoError(t, err)
	prepared, err := preparer.Prepare(model.NewUserID(), sitting, store.ExamSittingMailScheduled,
		mail.SittingScheduleDetails{ExamTitle: "Algorithms", ClassDisplayName: "Class A"})
	require.NoError(t, err)
	users := []*model.User{mailTestUser(mailTestPrincipal(at), at), mailTestUser(mailTestPrincipal(at), at)}
	persistence := &sittingMailExpansionStoreFake{fanout: &store.ExamSittingMailFanoutSnapshot{Occurrence: prepared.Occurrence,
		Bundle: prepared.Bundle, SittingID: sitting.ID, SittingRevision: sitting.Revision,
		ChangeKind: store.ExamSittingMailScheduled, Deadline: at.Add(72 * time.Hour)}, users: users}
	handler := sittingMailExpansionHandler{sittings: persistence, mail: preparer}
	leaseLost := errors.New("lease lost before checkpoint commit")
	first := handler.Run(context.Background(), job.NewExecution(prepared.ExpansionJob, nil,
		func(context.Context, job.CheckpointValue) error { return leaseLost }, nil))
	require.Equal(t, job.OutcomeRetryableFailure, first.Kind, "first outcome=%#v completed=%s", first, persistence.completed)
	require.ErrorIs(t, first.Err, leaseLost, "first outcome=%#v completed=%s", first, persistence.completed)
	require.False(t, persistence.completed.IsValid(), "first outcome=%#v completed=%s", first, persistence.completed)
	second := handler.Run(context.Background(), job.NewExecution(prepared.ExpansionJob, nil,
		func(context.Context, job.CheckpointValue) error { return nil }, nil))
	require.Equal(t, job.OutcomeSucceeded, second.Kind, "second outcome=%#v completed=%s inserted=%d commits", second, persistence.completed, len(persistence.inserted))
	require.Equal(t, prepared.Occurrence.ID, persistence.completed, "second outcome=%#v completed=%s inserted=%d commits", second, persistence.completed, len(persistence.inserted))
	require.Len(t, persistence.inserted, len(users), "second outcome=%#v completed=%s inserted=%d commits", second, persistence.completed, len(persistence.inserted))
	require.Len(t, persistence.commits, len(users)*2, "second outcome=%#v completed=%s inserted=%d commits", second, persistence.completed, len(persistence.inserted))
}

type sittingMailRendererFake struct{}

func (sittingMailRendererFake) SittingLocales() (string, []string) { return "en", []string{"en"} }

func (sittingMailRendererFake) Render(request mail.RenderRequest) (mail.FrozenContent, error) {
	details, ok := request.Presentation.(mail.SittingScheduleDetails)
	if !ok {
		return mail.FrozenContent{}, nil
	}
	key := request.Key
	return mail.FrozenContent{Subject: string(key), Text: details.ExamTitle + " " + details.ClassDisplayName,
		HTML: "<p>" + details.ExamTitle + " " + details.ClassDisplayName + "</p>"}, nil
}

type mailSenderFake struct {
	enabled bool
	from    mail.Address
}

func (s *mailSenderFake) Enabled() bool      { return s.enabled }
func (s *mailSenderFake) From() mail.Address { return s.from }
func (s *mailSenderFake) Send(context.Context, mail.Outbound) (mail.TransportOutcome, error) {
	return mail.TransportUnknown, nil
}

func mailTestSealer(t *testing.T) *secretseal.Sealer {
	t.Helper()
	key := base64.StdEncoding.EncodeToString(make([]byte, 32))
	value, err := secretseal.New(secretseal.Settings{EncryptionKey: key, MaximumPlaintext: secretseal.MaximumPlaintextBytes})
	require.NoError(t, err)
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

func (f *sittingMailExpansionStoreFake) GetMailFanout(context.Context, model.MailOccurrenceID) (*store.ExamSittingMailFanoutSnapshot, error) {
	return f.fanout, nil
}

func (f *sittingMailExpansionStoreFake) ListMailRecipients(_ context.Context,
	request store.ExamSittingMailRecipientPageRequest,
) (*store.ExamSittingMailRecipientPage, error) {
	start := 0
	if request.AfterUserID.IsValid() {
		for index := range f.users {
			if f.users[index].ID == request.AfterUserID {
				start = index + 1
				break
			}
		}
	}
	end := start + request.Limit
	if end > len(f.users) {
		end = len(f.users)
	}
	page := &store.ExamSittingMailRecipientPage{Fanout: f.fanout, More: end < len(f.users)}
	for _, user := range f.users[start:end] {
		page.Recipients = append(page.Recipients, store.ExamSittingMailRecipient{User: user, TemplateKey: model.MailTemplateExamSittingScheduled})
	}
	return page, nil
}

func (f *sittingMailExpansionStoreFake) CommitMailRecipient(_ context.Context,
	input *store.ExamSittingMailRecipientCommit,
) (*store.ExamSittingMailRecipientResult, error) {
	f.commits = append(f.commits, input)
	if input.Recipient.ID == f.conflictID {
		return nil, store.NewErrConflict("exam_sitting_mail_recipient", "changed", errors.New("changed"))
	}
	if f.inserted == nil {
		f.inserted = make(map[model.UserID]bool)
	}
	if f.inserted[input.Recipient.ID] {
		return &store.ExamSittingMailRecipientResult{Delivery: input.Delivery, Inserted: false}, nil
	}
	f.inserted[input.Recipient.ID] = true
	return &store.ExamSittingMailRecipientResult{Delivery: input.Delivery, Inserted: true}, nil
}

func (f *sittingMailExpansionStoreFake) CompleteMailExpansion(_ context.Context,
	input *store.ExamSittingMailExpansionCompletion,
) (*store.ExamSittingMailFanoutSnapshot, error) {
	f.completed = input.OccurrenceID
	return f.fanout, nil
}

func TestSittingMailExpansionSkipsAnUnaddressableRecipientAndReachesLaterOnes(t *testing.T) {
	at := time.Date(2026, 8, 20, 9, 0, 0, 0, time.UTC)
	preparer, err := mail.NewSittingComposer(sittingMailRendererFake{}, &mailSenderFake{enabled: true,
		from: mail.Address{Name: "Proctor", Address: "no-reply@example.test"}}, mailTestSealer(t), func() time.Time { return at })
	require.NoError(t, err)
	sitting, err := model.NewExamSitting(model.NewExamSittingID(), model.NewExamID(), model.NewExamRevisionID(), model.NewClassID(),
		at.Add(24*time.Hour), at.Add(26*time.Hour), at)
	require.NoError(t, err)
	prepared, err := preparer.Prepare(model.NewUserID(), sitting, store.ExamSittingMailScheduled,
		mail.SittingScheduleDetails{ExamTitle: "Algorithms", ClassDisplayName: "Class A"})
	require.NoError(t, err)
	users := []*model.User{mailTestUser(mailTestPrincipal(at), at), mailTestUser(mailTestPrincipal(at), at), mailTestUser(mailTestPrincipal(at), at)}
	users[0].DisplayName = "Operator\r\nBcc: other@example.test"
	persistence := &sittingMailExpansionStoreFake{fanout: &store.ExamSittingMailFanoutSnapshot{Occurrence: prepared.Occurrence,
		Bundle: prepared.Bundle, SittingID: sitting.ID, SittingRevision: sitting.Revision,
		ChangeKind: store.ExamSittingMailScheduled, Deadline: at.Add(72 * time.Hour)}, users: users}
	outcome := (sittingMailExpansionHandler{sittings: persistence, mail: preparer}).Run(context.Background(),
		job.NewExecution(prepared.ExpansionJob, nil, func(context.Context, job.CheckpointValue) error { return nil }, nil))
	require.Equal(t, job.OutcomeSucceeded, outcome.Kind, "outcome=%#v", outcome)
	require.Equal(t, prepared.Occurrence.ID, persistence.completed)
	require.False(t, persistence.inserted[users[0].ID])
	require.True(t, persistence.inserted[users[1].ID])
	require.True(t, persistence.inserted[users[2].ID])
	var result model.SittingMailExpansionResult
	require.NoError(t, json.Unmarshal(outcome.Result, &result))
	require.Equal(t, model.SittingMailExpansionResult{Expanded: 2, Undeliverable: 1}, result)
}
