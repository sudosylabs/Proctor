// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package app

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/server/app/apperror"
	"github.com/sudosylabs/proctor/server/app/invocation"
	"github.com/sudosylabs/proctor/server/app/jobhandler"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/secretseal"
	"github.com/sudosylabs/proctor/server/store"
)

type mailRekeyStarterFake struct {
	input  *store.MailRekeyStart
	state  *store.MailKeyState
	job    *model.Job
	err    error
	events *[]string
}

func (f *mailRekeyStarterFake) InspectKeyState(context.Context) (*store.MailKeyState, error) {
	return f.state, nil
}

func (f *mailRekeyStarterFake) StartRekey(_ context.Context, input *store.MailRekeyStart) (*store.MailRekeyOperation, error) {
	if f.events != nil {
		*f.events = append(*f.events, "store")
	}
	f.input = input
	if f.err != nil {
		return nil, f.err
	}
	return &store.MailRekeyOperation{JobID: input.Job.ID, PrimaryKeyID: input.PrimaryKeyID,
		RetiringKeyID: input.RetiringKeyID, CreatedAt: input.Job.CreatedAt}, nil
}

func (f *mailRekeyStarterFake) Get(_ context.Context, id model.JobID) (*model.Job, error) {
	if f.job == nil || f.job.ID != id {
		return nil, store.NewErrNotFound("job", id.String())
	}
	copy := *f.job
	return &copy, nil
}

type mailRekeyAuditFake struct {
	beginID  string
	attempt  mutationAttempt
	failID   string
	failCode string
	failErr  error
	events   *[]string
}

func (f *mailRekeyAuditFake) Begin(context.Context, invocation.Invocation, model.Action, model.Resource, string, map[string]any, map[string]any) (model.AuditEventID, error) {
	return "", errors.New("unscoped mail rekey audit is forbidden")
}

func (f *mailRekeyAuditFake) BeginAtScope(_ context.Context, invocation invocation.Invocation, action model.Action, resource model.Resource, scopeType model.RoleScopeType, scopeID, operation string, value, prior map[string]any) (model.AuditEventID, error) {
	if f.events != nil {
		*f.events = append(*f.events, "audit")
	}
	f.attempt = mutationAttempt{Invocation: invocation, Action: action, Resource: resource, ScopeType: scopeType,
		ScopeID: scopeID, Operation: operation, Value: value, Prior: prior}
	return model.AuditEventID(f.beginID), nil
}

func (f *mailRekeyAuditFake) Fail(_ context.Context, id model.AuditEventID, code string) error {
	f.failID, f.failCode = id.String(), code
	return f.failErr
}

func TestStartMailRekeyRequiresStrongRecentSessionAndEnqueuesAuditedJob(t *testing.T) {
	now := time.Date(2026, 9, 4, 12, 0, 0, 123456789, time.UTC)
	wantCreatedAt := time.Date(2026, 9, 4, 12, 0, 0, 123456000, time.UTC)
	oldKey := base64.StdEncoding.EncodeToString([]byte("old-mail-rekey-key-material-0001"))
	newKey := base64.StdEncoding.EncodeToString([]byte("new-mail-rekey-key-material-0001"))
	sealer, err := secretseal.New(secretseal.Settings{EncryptionKey: newKey, DecryptionKeys: []string{oldKey}, MaximumPlaintext: 1024})
	require.NoError(t, err)
	events := []string{}
	persistence := &mailRekeyStarterFake{events: &events, state: &store.MailKeyState{Active: []store.MailPayloadKeyUsage{{
		KeyID: oldSealerKeyID(t, oldKey), ActiveReferences: 4,
	}}}}
	authorizer := &mailAuthorizerFake{}
	auditor := &mailRekeyAuditFake{beginID: model.NewID(), events: &events}
	service := &mailService{deps: mailServiceDependencies{Authorization: authorizer, Sealer: sealer, RecentAuthenticationTTL: 15 * time.Minute, Now: func() time.Time { return now }}, rekey: persistence, keyState: persistence, rekeyAudit: auditor}
	woke := 0
	service.wake = func() { woke++ }
	// Authentication restores durable Session timestamps; the service clock
	// can still carry finer precision before creating the Job.
	principal := mailTestPrincipal(model.TimeUTC(now))
	principal.AuthenticationStrength = model.AuthenticationMultiFactor
	principal.MFACompletedAt = model.OptionalTimeFrom(now.Add(-time.Minute))

	view, appErr := service.StartMailRekey(context.Background(), invocation.New(principal, model.RequestMetadata{}), oldSealerKeyID(t, oldKey))
	require.NoError(t, appErr)
	require.NotNil(t, persistence.input, "view=%#v input=%#v audit=%#v woke", view, persistence.input, auditor.attempt)
	require.Equal(t, model.JobTypeMailRekey, persistence.input.Job.Type, "view=%#v input=%#v audit=%#v woke", view, persistence.input, auditor.attempt)
	require.Equal(t, sealer.PrimaryKeyID(), persistence.input.PrimaryKeyID, "view=%#v input=%#v audit=%#v woke", view, persistence.input, auditor.attempt)
	require.Equal(t, oldSealerKeyID(t, oldKey), persistence.input.RetiringKeyID, "view=%#v input=%#v audit=%#v woke", view, persistence.input, auditor.attempt)
	require.Equal(t, model.AuditEventID(auditor.beginID), persistence.input.AuditEventID, "view=%#v input=%#v audit=%#v woke", view, persistence.input, auditor.attempt)
	require.True(t, model.TimeUTC(now).Equal(persistence.input.AuditAt), "view=%#v input=%#v audit=%#v woke", view, persistence.input, auditor.attempt)
	require.Equal(t, persistence.input.Job.ID, view.JobID, "view=%#v input=%#v audit=%#v woke", view, persistence.input, auditor.attempt)
	require.Equal(t, sealer.PrimaryKeyID(), view.PrimaryKeyID, "view=%#v input=%#v audit=%#v woke", view, persistence.input, auditor.attempt)
	require.Equal(t, oldSealerKeyID(t, oldKey), view.RetiringKeyID, "view=%#v input=%#v audit=%#v woke", view, persistence.input, auditor.attempt)
	require.Equal(t, "start_rekey", auditor.attempt.Operation, "view=%#v input=%#v audit=%#v woke", view, persistence.input, auditor.attempt)
	require.Equal(t, model.RoleScopeInstitution, auditor.attempt.ScopeType, "view=%#v input=%#v audit=%#v woke", view, persistence.input, auditor.attempt)
	require.True(t, view.CreatedAt.Equal(wantCreatedAt), "view=%#v input=%#v audit=%#v woke", view, persistence.input, auditor.attempt)
	require.Equal(t, 1, woke, "view=%#v input=%#v audit=%#v woke", view, persistence.input, auditor.attempt)
	require.Len(t, authorizer.actions, 1)
	require.Equal(t, model.ActionMailRekey, authorizer.actions[0])
	require.Len(t, events, 2)
	require.Equal(t, "audit", events[0])
	require.Equal(t, "store", events[1])
	state, appErr := service.GetMailKeyState(context.Background(), invocation.New(principal, model.RequestMetadata{}))
	require.NoError(t, appErr, "KeyState() = %#v", state)
	require.Equal(t, sealer.PrimaryKeyID(), state.PrimaryKeyID, "KeyState() = %#v", state)
	require.Len(t, state.Active, 1, "KeyState() = %#v", state)
	require.Equal(t, oldSealerKeyID(t, oldKey), state.Active[0].KeyID, "KeyState() = %#v", state)
	require.Equal(t, int64(4), state.Active[0].ActiveReferences, "KeyState() = %#v", state)

	pat := principal
	pat.SessionID = ""
	pat.CredentialID = model.PrincipalCredentialID(model.NewID())
	pat.CredentialType = model.CredentialPersonalAccessToken
	pat.AuthenticationStrength = ""
	pat.AuthenticatedAt = time.Time{}
	pat.MFACompletedAt = model.OptionalTime{}
	pat.ClientType = model.SessionClientCLI
	pat.CredentialScopes = []string{string(model.ActionMailManage)}
	_, appErr = service.StartMailRekey(context.Background(), invocation.New(pat, model.RequestMetadata{}), oldSealerKeyID(t, oldKey))
	require.True(t, apperror.Is(appErr, "authentication.token_invalid"), "PAT error = %v", appErr)
	_, appErr = service.GetMailKeyState(context.Background(), invocation.New(pat, model.RequestMetadata{}))
	require.True(t, apperror.Is(appErr, "authentication.token_invalid"), "PAT key state error = %v", appErr)
	weak := mailTestPrincipal(model.TimeUTC(now))
	_, appErr = service.StartMailRekey(context.Background(), invocation.New(weak, model.RequestMetadata{}), oldSealerKeyID(t, oldKey))
	require.True(t, apperror.Is(appErr, "authentication.strong_required"), "weak Session error = %v", appErr)
	_, appErr = service.GetMailKeyState(context.Background(), invocation.New(weak, model.RequestMetadata{}))
	require.True(t, apperror.Is(appErr, "authentication.strong_required"), "weak Session key state error = %v", appErr)
	stale := principal
	stale.AuthenticatedAt = model.TimeUTC(now.Add(-time.Hour))
	stale.MFACompletedAt = model.OptionalTimeFrom(now.Add(-time.Hour))
	_, appErr = service.StartMailRekey(context.Background(), invocation.New(stale, model.RequestMetadata{}), oldSealerKeyID(t, oldKey))
	require.True(t, apperror.Is(appErr, "authentication.reauthentication_required"), "stale Session error = %v", appErr)
	_, appErr = service.GetMailKeyState(context.Background(), invocation.New(stale, model.RequestMetadata{}))
	require.True(t, apperror.Is(appErr, "authentication.reauthentication_required"), "stale Session key state error = %v", appErr)
}

func TestStartMailRekeyCompletesConflictAndPersistenceFailureAudits(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 9, 4, 12, 0, 0, 123456789, time.UTC)
	oldKey := base64.StdEncoding.EncodeToString([]byte("old-mail-rekey-key-material-0001"))
	newKey := base64.StdEncoding.EncodeToString([]byte("new-mail-rekey-key-material-0001"))
	sealer, err := secretseal.New(secretseal.Settings{EncryptionKey: newKey, DecryptionKeys: []string{oldKey}, MaximumPlaintext: 1024})
	require.NoError(t, err)
	for _, test := range []struct {
		name     string
		storeErr error
		failErr  error
		wantCode string
		failCode string
	}{
		{name: "active or promotion conflict", storeErr: store.NewErrConflict("mail_rekey", "active_operation", nil),
			wantCode: "mail.rekey.conflict", failCode: "mail.rekey.conflict"},
		{name: "persistence unavailable", storeErr: errors.New("database unavailable"),
			wantCode: "mail.unavailable", failCode: "mail.unavailable"},
		{name: "audit completion unavailable", storeErr: errors.New("database unavailable"),
			failErr: apperror.New("audit.unavailable"), wantCode: "mail.unavailable", failCode: "mail.unavailable"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			persistence := &mailRekeyStarterFake{err: test.storeErr}
			auditor := &mailRekeyAuditFake{beginID: model.NewID(), failErr: test.failErr}
			service := &mailService{deps: mailServiceDependencies{Authorization: &mailAuthorizerFake{}, Sealer: sealer, RecentAuthenticationTTL: 15 * time.Minute, Now: func() time.Time { return now }}, rekey: persistence, rekeyAudit: auditor, wake: func() { require.FailNow(t, "failed rekey woke workers") }}
			principal := mailTestPrincipal(model.TimeUTC(now))
			principal.AuthenticationStrength = model.AuthenticationMultiFactor
			principal.MFACompletedAt = model.OptionalTimeFrom(now)

			_, appErr := service.StartMailRekey(context.Background(), invocation.New(principal, model.RequestMetadata{}), oldSealerKeyID(t, oldKey))
			require.True(t, apperror.Is(appErr, test.wantCode), "StartRekey() error=%v input=%#v audit=%#v", appErr, persistence.input, auditor)
			require.NotNil(t, persistence.input, "StartRekey() error=%v input=%#v audit=%#v", appErr, persistence.input, auditor)
			require.Equal(t, model.AuditEventID(auditor.beginID), persistence.input.AuditEventID, "StartRekey() error=%v input=%#v audit=%#v", appErr, persistence.input, auditor)
			require.Equal(t, auditor.beginID, auditor.failID, "StartRekey() error=%v input=%#v audit=%#v", appErr, persistence.input, auditor)
			require.Equal(t, test.failCode, auditor.failCode, "StartRekey() error=%v input=%#v audit=%#v", appErr, persistence.input, auditor)
		})
	}
}

func TestGetMailRekeyStatusProjectsOnlyTypedProgressAndRetirementProof(t *testing.T) {
	now := time.Date(2026, 9, 4, 12, 0, 0, 123456789, time.UTC)
	primaryKeyID := "22222222222222222222222222222222"
	retiringKeyID := "11111111111111111111111111111111"
	command, _ := json.Marshal(jobhandler.MailRekeyCommand{PrimaryKeyID: primaryKeyID, RetiringKeyID: retiringKeyID})
	job, err := model.NewJob(model.NewJobID(), model.JobTypeMailRekey, command, "mail-rekey:test", now, now, 10)
	require.NoError(t, err)
	running, err := job.Start(now.Add(time.Second))
	require.NoError(t, err)
	checkpoint, _ := json.Marshal(jobhandler.MailRekeyCheckpoint{AfterKind: store.MailRekeyTargetDelivery,
		AfterID: model.NewMailDeliveryID().String(), Processed: 4, Reencrypted: 3})
	running, err = running.UpdateProgress(&model.JobProgress{Current: 4, Total: 9, Stage: "reencrypting"}, checkpoint, now.Add(2*time.Second))
	require.NoError(t, err)
	result, _ := json.Marshal(jobhandler.MailRekeyResult{PrimaryKeyID: primaryKeyID, RetiringKeyID: retiringKeyID,
		Processed: 9, Reencrypted: 8, RetirementSafe: true})
	succeeded, err := running.Succeed(result, now.Add(3*time.Second))
	require.NoError(t, err)
	persistence := &mailRekeyStarterFake{job: succeeded}
	authorizer := &mailAuthorizerFake{}
	service := &mailService{deps: mailServiceDependencies{Authorization: authorizer, RecentAuthenticationTTL: 15 * time.Minute, Now: func() time.Time { return now.Add(4 * time.Second) }}, rekeyJobs: persistence}
	principal := mailTestPrincipal(model.TimeUTC(now))
	principal.AuthenticationStrength = model.AuthenticationMultiFactor
	principal.MFACompletedAt = model.OptionalTimeFrom(now)

	view, appErr := service.GetMailRekeyStatus(context.Background(), invocation.New(principal, model.RequestMetadata{}), succeeded.ID)

	require.NoError(t, appErr)
	require.Equal(t, succeeded.ID, view.JobID)
	require.Equal(t, model.JobStatusSucceeded, view.Status)
	require.Equal(t, primaryKeyID, view.PrimaryKeyID)
	require.Equal(t, retiringKeyID, view.RetiringKeyID)
	require.Equal(t, int64(9), view.Processed)
	require.Equal(t, int64(8), view.Reencrypted)
	require.NotNil(t, view.Progress)
	require.Equal(t, int64(4), view.Progress.Current)
	require.Equal(t, int64(9), view.Progress.Total)
	require.NotNil(t, view.Proof)
	require.True(t, view.Proof.RetirementSafe)
	require.Equal(t, int64(0), view.Proof.NonPrimaryReferences)
	require.Equal(t, int64(0), view.Proof.RetiringReferences)
	require.Len(t, authorizer.actions, 1)
	require.Equal(t, model.ActionMailRekey, authorizer.actions[0])
}

func TestGetMailRekeyStatusConcealsJobsOwnedByOtherDomains(t *testing.T) {
	now := time.Date(2026, 9, 4, 12, 0, 0, 123456789, time.UTC)
	other, err := model.NewJob(model.NewJobID(), model.JobTypeProfilePictureGenerateDefault,
		json.RawMessage(`{"user_id":"`+model.NewUserID().String()+`"}`), model.NewUserID().String(), now, now, 8)
	require.NoError(t, err)
	persistence := &mailRekeyStarterFake{job: other}
	service := &mailService{deps: mailServiceDependencies{Authorization: &mailAuthorizerFake{}, RecentAuthenticationTTL: 15 * time.Minute, Now: func() time.Time { return now }}, rekeyJobs: persistence}
	principal := mailTestPrincipal(model.TimeUTC(now))
	principal.AuthenticationStrength = model.AuthenticationMultiFactor
	principal.MFACompletedAt = model.OptionalTimeFrom(now)

	_, appErr := service.GetMailRekeyStatus(context.Background(), invocation.New(principal, model.RequestMetadata{}), other.ID)
	require.True(t, apperror.Is(appErr, "resource.not_found"), "RekeyStatus(other Job) error = %v", appErr)

	corruptCommand, _ := json.Marshal(jobhandler.MailRekeyCommand{PrimaryKeyID: "not-a-key", RetiringKeyID: "also-not-a-key"})
	corrupt, err := model.NewJob(model.NewJobID(), model.JobTypeMailRekey, corruptCommand,
		"mail-rekey:corrupt", now, now, 10)
	require.NoError(t, err)
	persistence.job = corrupt
	_, appErr = service.GetMailRekeyStatus(context.Background(), invocation.New(principal, model.RequestMetadata{}), corrupt.ID)
	require.True(t, apperror.Is(appErr, "mail.unavailable"), "RekeyStatus(corrupt rekey Job) error = %v", appErr)
}

func oldSealerKeyID(t *testing.T, key string) string {
	t.Helper()
	sealer, err := secretseal.New(secretseal.Settings{EncryptionKey: key, MaximumPlaintext: 1})
	require.NoError(t, err)
	return sealer.PrimaryKeyID()
}
