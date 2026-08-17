//go:build integration

// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"testing"
	"time"

	jobengine "github.com/sudosylabs/proctor/server/app/job"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/secretseal"
	"github.com/sudosylabs/proctor/server/store"
)

func TestMailRekeyOldPrimaryRelinquishesOnceAndNewPrimaryCompletes(t *testing.T) {
	persistence := openClusteredJobIntegrationStore(t)
	ctx := context.Background()
	at := model.NowUTC().Add(-time.Second)
	institution, err := persistence.Institution().Save(ctx, &model.Institution{Name: "mail-rekey-cluster", DisplayName: "Mail Rekey Cluster"})
	if err != nil {
		t.Fatal(err)
	}
	user, defaultJob, err := prepareUserDefaultProfilePictureJob(&model.User{Username: "mail-rekey-operator", Email: "mail-rekey-operator@example.test"}, at)
	if err != nil {
		t.Fatal(err)
	}
	settings, err := prepareInitialUserSettingsDocument(user)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = persistence.User().Create(ctx, &store.UserCreation{User: user, Settings: settings, DefaultProfilePictureJob: defaultJob}); err != nil {
		t.Fatal(err)
	}

	oldKey := base64.StdEncoding.EncodeToString([]byte("old-mail-rekey-key-material-0001"))
	newKey := base64.StdEncoding.EncodeToString([]byte("new-mail-rekey-key-material-0001"))
	oldSealer, err := secretseal.New(secretseal.Settings{EncryptionKey: oldKey, DecryptionKeys: []string{newKey}, MaximumPlaintext: 1024})
	if err != nil {
		t.Fatal(err)
	}
	newSealer, err := secretseal.New(secretseal.Settings{EncryptionKey: newKey, DecryptionKeys: []string{oldKey}, MaximumPlaintext: 1024})
	if err != nil {
		t.Fatal(err)
	}
	command, _ := json.Marshal(MailRekeyCommandV1{PrimaryKeyID: newSealer.PrimaryKeyID(), RetiringKeyID: oldSealer.PrimaryKeyID()})
	rekeyJob, err := model.NewJob(model.NewJobID(), model.JobTypeMailRekey, 1, command,
		"mail-rekey:"+newSealer.PrimaryKeyID()+":"+oldSealer.PrimaryKeyID(), at, at, 10)
	if err != nil {
		t.Fatal(err)
	}
	audit, err := persistence.Audit().Save(ctx, &model.AuditEvent{ActorID: user.ID, Action: string(model.ActionMailRekey),
		Resource:  model.Resource{Type: model.ResourceInstitution, ID: institution.ID.String()},
		ScopeType: model.RoleScopeInstitution, ScopeID: institution.ID.String(), Status: model.AuditStatusAttempt,
		NodeID: "new-primary-node", ClientType: string(model.SessionClientWeb), AuthMethod: "password"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = persistence.Mail().StartRekey(ctx, &store.MailRekeyStart{PrimaryKeyID: newSealer.PrimaryKeyID(),
		RetiringKeyID: oldSealer.PrimaryKeyID(), Job: rekeyJob, AuditEventID: audit.ID.String(), AuditAt: model.GetMillis()}); err != nil {
		t.Fatal(err)
	}

	oldDescriptor := mailRekeyDescriptor(mailRekeyHandler{mail: persistence.Mail(), sealer: oldSealer})
	oldDescriptor.BaseRetryDelay, oldDescriptor.MaximumRetryDelay = 10*time.Millisecond, 10*time.Millisecond
	oldDescriptor.LeaseDuration, oldDescriptor.HeartbeatInterval = 100*time.Millisecond, 20*time.Millisecond
	oldNode, err := jobengine.New(jobengine.Config{Store: persistence.Job(), Descriptors: []jobengine.Descriptor{oldDescriptor},
		NodeID: "old-primary-node", Diagnostics: &integrationJobDiagnostics{}, Policy: jobengine.Policy{PollInterval: 5 * time.Millisecond}})
	if err != nil {
		t.Fatal(err)
	}
	if err = oldNode.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = oldNode.Close() }()
	waitForMailRekeyJob(t, persistence.Job(), rekeyJob.ID, func(job *model.Job, attempts []model.JobAttempt) bool {
		return job.Status == model.JobStatusQueued && job.AttemptCount == 1 && job.MaximumAttempts == 11 &&
			len(attempts) == 1 && attempts[0].Status == model.JobAttemptStatusIncompatible
	})

	// Wait beyond the persisted retry delay: the same stable incompatible node
	// must not reclaim and hot-loop when no capable peer is present.
	time.Sleep(40 * time.Millisecond)
	attempts, err := persistence.Job().ListAttempts(ctx, rekeyJob.ID)
	if err != nil || len(attempts) != 1 || attempts[0].NodeID != "old-primary-node" {
		t.Fatalf("no-compatible-node attempts = %#v, %v", attempts, err)
	}

	newDescriptor := mailRekeyDescriptor(mailRekeyHandler{mail: persistence.Mail(), sealer: newSealer})
	newDescriptor.BaseRetryDelay, newDescriptor.MaximumRetryDelay = 10*time.Millisecond, 10*time.Millisecond
	newDescriptor.LeaseDuration, newDescriptor.HeartbeatInterval = 100*time.Millisecond, 20*time.Millisecond
	newNode, err := jobengine.New(jobengine.Config{Store: persistence.Job(), Descriptors: []jobengine.Descriptor{newDescriptor},
		NodeID: "new-primary-node", Diagnostics: &integrationJobDiagnostics{}, Policy: jobengine.Policy{PollInterval: 5 * time.Millisecond}})
	if err != nil {
		t.Fatal(err)
	}
	if err = newNode.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = newNode.Close() }()
	waitForMailRekeyJob(t, persistence.Job(), rekeyJob.ID, func(job *model.Job, attempts []model.JobAttempt) bool {
		return job.Status == model.JobStatusSucceeded && len(attempts) == 2 &&
			attempts[0].Status == model.JobAttemptStatusIncompatible && attempts[1].Status == model.JobAttemptStatusSucceeded
	})

	completed, err := persistence.Job().Get(ctx, rekeyJob.ID)
	if err != nil {
		t.Fatal(err)
	}
	var proof MailRekeyResultV1
	if err = json.Unmarshal(completed.Result, &proof); err != nil || !proof.RetirementSafe ||
		proof.NonPrimaryReferences != 0 || proof.RetiringReferences != 0 {
		t.Fatalf("completed retirement proof = %#v, %v", proof, err)
	}
}

func waitForMailRekeyJob(t *testing.T, jobs store.JobStore, jobID model.JobID,
	ready func(*model.Job, []model.JobAttempt) bool,
) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		job, err := jobs.Get(context.Background(), jobID)
		if err != nil {
			t.Fatal(err)
		}
		attempts, err := jobs.ListAttempts(context.Background(), jobID)
		if err != nil {
			t.Fatal(err)
		}
		if ready(job, attempts) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("mail rekey Job did not converge: job=%#v attempts=%#v", job, attempts)
		}
		time.Sleep(5 * time.Millisecond)
	}
}
