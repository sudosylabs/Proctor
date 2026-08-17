// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"testing"
	"time"

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

func (f *mailRekeyAuditFake) Begin(context.Context, Invocation, model.Action, model.Resource, string, map[string]any, map[string]any) (string, error) {
	return "", errors.New("unscoped mail rekey audit is forbidden")
}

func (f *mailRekeyAuditFake) BeginAtScope(_ context.Context, invocation Invocation, action model.Action,
	resource model.Resource, scopeType model.RoleScopeType, scopeID, operation string, value, prior map[string]any,
) (string, error) {
	if f.events != nil {
		*f.events = append(*f.events, "audit")
	}
	f.attempt = mutationAttempt{Invocation: invocation, Action: action, Resource: resource, ScopeType: scopeType,
		ScopeID: scopeID, Operation: operation, Value: value, Prior: prior}
	return f.beginID, nil
}

func (f *mailRekeyAuditFake) Fail(_ context.Context, id, code string) error {
	f.failID, f.failCode = id, code
	return f.failErr
}

func TestStartMailRekeyRequiresStrongRecentSessionAndEnqueuesAuditedJob(t *testing.T) {
	now := time.Now().UTC()
	oldKey := base64.StdEncoding.EncodeToString([]byte("old-mail-rekey-key-material-0001"))
	newKey := base64.StdEncoding.EncodeToString([]byte("new-mail-rekey-key-material-0001"))
	sealer, err := secretseal.New(secretseal.Settings{EncryptionKey: newKey, DecryptionKeys: []string{oldKey}, MaximumPlaintext: 1024})
	if err != nil {
		t.Fatal(err)
	}
	events := []string{}
	persistence := &mailRekeyStarterFake{events: &events, state: &store.MailKeyState{Active: []store.MailPayloadKeyUsage{{
		KeyID: oldSealerKeyID(t, oldKey), ActiveReferences: 4,
	}}}}
	authorizer := &mailAuthorizerFake{}
	auditor := &mailRekeyAuditFake{beginID: model.NewId(), events: &events}
	service := &mailService{rekey: persistence, keyState: persistence, rekeyAudit: auditor, authorization: authorizer, sealer: sealer,
		recentAuthenticationTTL: 15 * time.Minute, now: func() time.Time { return now }}
	woke := 0
	service.wake = func() { woke++ }
	principal := mailTestPrincipal(now)
	principal.AuthenticationStrength = model.AuthenticationMultiFactor
	principal.MFACompletedAt = model.OptionalTimeFrom(now.Add(-time.Minute))

	view, appErr := service.StartRekey(context.Background(), NewInvocation(principal, model.RequestMetadata{}), oldSealerKeyID(t, oldKey))
	if appErr != nil {
		t.Fatal(appErr)
	}
	if persistence.input == nil || persistence.input.Job.Type != model.JobTypeMailRekey ||
		persistence.input.PrimaryKeyID != sealer.PrimaryKeyID() || persistence.input.RetiringKeyID != oldSealerKeyID(t, oldKey) ||
		persistence.input.AuditEventID != auditor.beginID || persistence.input.AuditAt != now.UnixMilli() ||
		view.JobID != persistence.input.Job.ID || view.PrimaryKeyID != sealer.PrimaryKeyID() ||
		view.RetiringKeyID != oldSealerKeyID(t, oldKey) || auditor.attempt.Operation != "start_rekey" ||
		auditor.attempt.ScopeType != model.RoleScopeInstitution || woke != 1 {
		t.Fatalf("view=%#v input=%#v audit=%#v woke=%d", view, persistence.input, auditor.attempt, woke)
	}
	if len(authorizer.actions) != 1 || authorizer.actions[0] != model.ActionMailRekey {
		t.Fatalf("authorization actions = %#v", authorizer.actions)
	}
	if len(events) != 2 || events[0] != "audit" || events[1] != "store" {
		t.Fatalf("mail rekey durable order = %#v", events)
	}
	state, appErr := service.KeyState(context.Background(), NewInvocation(principal, model.RequestMetadata{}))
	if appErr != nil || state.PrimaryKeyID != sealer.PrimaryKeyID() || len(state.Active) != 1 ||
		state.Active[0].KeyID != oldSealerKeyID(t, oldKey) || state.Active[0].ActiveReferences != 4 {
		t.Fatalf("KeyState() = %#v, %v", state, appErr)
	}

	pat := principal
	pat.SessionID = ""
	pat.CredentialID = model.PrincipalCredentialID(model.NewId())
	pat.CredentialType = model.CredentialPersonalAccessToken
	pat.AuthenticationStrength = ""
	pat.AuthenticatedAt = time.Time{}
	pat.MFACompletedAt = model.OptionalTime{}
	pat.ClientType = model.SessionClientCLI
	pat.CredentialScopes = []string{string(model.ActionMailManage)}
	if _, appErr = service.StartRekey(context.Background(), NewInvocation(pat, model.RequestMetadata{}), oldSealerKeyID(t, oldKey)); !Is(appErr, "authentication.invalid_token") {
		t.Fatalf("PAT error = %v", appErr)
	}
	if _, appErr = service.KeyState(context.Background(), NewInvocation(pat, model.RequestMetadata{})); !Is(appErr, "authentication.invalid_token") {
		t.Fatalf("PAT key state error = %v", appErr)
	}
	weak := mailTestPrincipal(now)
	if _, appErr = service.StartRekey(context.Background(), NewInvocation(weak, model.RequestMetadata{}), oldSealerKeyID(t, oldKey)); !Is(appErr, "authentication.strong_required") {
		t.Fatalf("weak Session error = %v", appErr)
	}
	if _, appErr = service.KeyState(context.Background(), NewInvocation(weak, model.RequestMetadata{})); !Is(appErr, "authentication.strong_required") {
		t.Fatalf("weak Session key state error = %v", appErr)
	}
	stale := principal
	stale.AuthenticatedAt = now.Add(-time.Hour)
	stale.MFACompletedAt = model.OptionalTimeFrom(now.Add(-time.Hour))
	if _, appErr = service.StartRekey(context.Background(), NewInvocation(stale, model.RequestMetadata{}), oldSealerKeyID(t, oldKey)); !Is(appErr, "authentication.reauthentication_required") {
		t.Fatalf("stale Session error = %v", appErr)
	}
	if _, appErr = service.KeyState(context.Background(), NewInvocation(stale, model.RequestMetadata{})); !Is(appErr, "authentication.reauthentication_required") {
		t.Fatalf("stale Session key state error = %v", appErr)
	}
}

func TestStartMailRekeyCompletesConflictAndPersistenceFailureAudits(t *testing.T) {
	t.Parallel()
	now := time.Now().UTC()
	oldKey := base64.StdEncoding.EncodeToString([]byte("old-mail-rekey-key-material-0001"))
	newKey := base64.StdEncoding.EncodeToString([]byte("new-mail-rekey-key-material-0001"))
	sealer, err := secretseal.New(secretseal.Settings{EncryptionKey: newKey, DecryptionKeys: []string{oldKey}, MaximumPlaintext: 1024})
	if err != nil {
		t.Fatal(err)
	}
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
			failErr: NewError("audit.unavailable"), wantCode: "audit.unavailable", failCode: "mail.unavailable"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			persistence := &mailRekeyStarterFake{err: test.storeErr}
			auditor := &mailRekeyAuditFake{beginID: model.NewId(), failErr: test.failErr}
			service := &mailService{rekey: persistence, rekeyAudit: auditor, authorization: &mailAuthorizerFake{}, sealer: sealer,
				recentAuthenticationTTL: 15 * time.Minute, now: func() time.Time { return now }, wake: func() { t.Fatal("failed rekey woke workers") }}
			principal := mailTestPrincipal(now)
			principal.AuthenticationStrength = model.AuthenticationMultiFactor
			principal.MFACompletedAt = model.OptionalTimeFrom(now)

			_, appErr := service.StartRekey(context.Background(), NewInvocation(principal, model.RequestMetadata{}), oldSealerKeyID(t, oldKey))
			if !Is(appErr, test.wantCode) || persistence.input == nil || persistence.input.AuditEventID != auditor.beginID ||
				auditor.failID != auditor.beginID || auditor.failCode != test.failCode {
				t.Fatalf("StartRekey() error=%v input=%#v audit=%#v", appErr, persistence.input, auditor)
			}
		})
	}
}

func TestGetMailRekeyStatusProjectsOnlyTypedProgressAndRetirementProof(t *testing.T) {
	now := time.Now().UTC()
	primaryKeyID := "22222222222222222222222222222222"
	retiringKeyID := "11111111111111111111111111111111"
	command, _ := json.Marshal(MailRekeyCommandV1{PrimaryKeyID: primaryKeyID, RetiringKeyID: retiringKeyID})
	job, err := model.NewJob(model.NewJobID(), model.JobTypeMailRekey, 1, command, "mail-rekey:test", now, now, 10)
	if err != nil {
		t.Fatal(err)
	}
	running, err := job.Start(now.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	checkpoint, _ := json.Marshal(MailRekeyCheckpointV1{AfterKind: store.MailRekeyTargetDelivery,
		AfterID: model.NewMailDeliveryID().String(), Processed: 4, Reencrypted: 3})
	running, err = running.UpdateProgress(&model.JobProgress{Current: 4, Total: 9, Stage: "reencrypting"}, 1, checkpoint, now.Add(2*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	result, _ := json.Marshal(MailRekeyResultV1{PrimaryKeyID: primaryKeyID, RetiringKeyID: retiringKeyID,
		Processed: 9, Reencrypted: 8, RetirementSafe: true})
	succeeded, err := running.Succeed(1, result, now.Add(3*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	persistence := &mailRekeyStarterFake{job: succeeded}
	authorizer := &mailAuthorizerFake{}
	service := &mailService{rekeyJobs: persistence, authorization: authorizer,
		recentAuthenticationTTL: 15 * time.Minute, now: func() time.Time { return now.Add(4 * time.Second) }}
	principal := mailTestPrincipal(now)
	principal.AuthenticationStrength = model.AuthenticationMultiFactor
	principal.MFACompletedAt = model.OptionalTimeFrom(now)

	view, appErr := service.RekeyStatus(context.Background(), NewInvocation(principal, model.RequestMetadata{}), succeeded.ID)

	if appErr != nil {
		t.Fatal(appErr)
	}
	if view.JobID != succeeded.ID || view.Status != model.JobStatusSucceeded || view.PrimaryKeyID != primaryKeyID ||
		view.RetiringKeyID != retiringKeyID || view.Processed != 9 || view.Reencrypted != 8 || view.Progress == nil ||
		view.Progress.Current != 4 || view.Progress.Total != 9 || view.Proof == nil || !view.Proof.RetirementSafe ||
		view.Proof.NonPrimaryReferences != 0 || view.Proof.RetiringReferences != 0 {
		t.Fatalf("RekeyStatus() = %#v", view)
	}
	if len(authorizer.actions) != 1 || authorizer.actions[0] != model.ActionMailRekey {
		t.Fatalf("authorization actions = %#v", authorizer.actions)
	}
}

func TestGetMailRekeyStatusConcealsJobsOwnedByOtherDomains(t *testing.T) {
	now := time.Now().UTC()
	other, err := model.NewJob(model.NewJobID(), model.JobTypeProfilePictureGenerateDefault, 1,
		json.RawMessage(`{"user_id":"`+model.NewUserID().String()+`"}`), model.NewUserID().String(), now, now, 8)
	if err != nil {
		t.Fatal(err)
	}
	persistence := &mailRekeyStarterFake{job: other}
	service := &mailService{rekeyJobs: persistence, authorization: &mailAuthorizerFake{},
		recentAuthenticationTTL: 15 * time.Minute, now: func() time.Time { return now }}
	principal := mailTestPrincipal(now)
	principal.AuthenticationStrength = model.AuthenticationMultiFactor
	principal.MFACompletedAt = model.OptionalTimeFrom(now)

	_, appErr := service.RekeyStatus(context.Background(), NewInvocation(principal, model.RequestMetadata{}), other.ID)
	if !Is(appErr, "resource.not_found") {
		t.Fatalf("RekeyStatus(other Job) error = %v", appErr)
	}

	corruptCommand, _ := json.Marshal(MailRekeyCommandV1{PrimaryKeyID: "not-a-key", RetiringKeyID: "also-not-a-key"})
	corrupt, err := model.NewJob(model.NewJobID(), model.JobTypeMailRekey, 1, corruptCommand,
		"mail-rekey:corrupt", now, now, 10)
	if err != nil {
		t.Fatal(err)
	}
	persistence.job = corrupt
	_, appErr = service.RekeyStatus(context.Background(), NewInvocation(principal, model.RequestMetadata{}), corrupt.ID)
	if !Is(appErr, "mail.unavailable") {
		t.Fatalf("RekeyStatus(corrupt rekey Job) error = %v", appErr)
	}
}

func oldSealerKeyID(t *testing.T, key string) string {
	t.Helper()
	sealer, err := secretseal.New(secretseal.Settings{EncryptionKey: key, MaximumPlaintext: 1})
	if err != nil {
		t.Fatal(err)
	}
	return sealer.PrimaryKeyID()
}
