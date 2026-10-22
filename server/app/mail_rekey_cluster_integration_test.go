//go:build integration

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
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	jobengine "github.com/sudosylabs/proctor/server/app/job"
	"github.com/sudosylabs/proctor/server/app/jobhandler"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/secretseal"
	"github.com/sudosylabs/proctor/server/store"
)

func TestMailRekeyOldPrimaryRelinquishesOnceAndNewPrimaryCompletes(t *testing.T) {
	persistence := openClusteredJobIntegrationStore(t)
	ctx := context.Background()
	at := model.NowUTC().Add(-time.Second)
	institution, err := persistence.Institution().Save(ctx, &model.Institution{Name: "mail-rekey-cluster", DisplayName: "Mail Rekey Cluster"})
	require.NoError(t, err)
	user, defaultJob, err := prepareUserDefaultProfilePictureJob(&model.User{Username: "mail-rekey-operator", Email: "mail-rekey-operator@example.test"}, at)
	require.NoError(t, err)
	settings, err := prepareInitialUserSettingsDocument(user)
	require.NoError(t, err)
	_, err = persistence.User().Create(ctx, &store.UserCreation{User: user, Settings: settings, DefaultProfilePictureJob: defaultJob})
	require.NoError(t, err)

	oldKey := base64.StdEncoding.EncodeToString([]byte("old-mail-rekey-key-material-0001"))
	newKey := base64.StdEncoding.EncodeToString([]byte("new-mail-rekey-key-material-0001"))
	oldSealer, err := secretseal.New(secretseal.Settings{EncryptionKey: oldKey, DecryptionKeys: []string{newKey}, MaximumPlaintext: 1024})
	require.NoError(t, err)
	newSealer, err := secretseal.New(secretseal.Settings{EncryptionKey: newKey, DecryptionKeys: []string{oldKey}, MaximumPlaintext: 1024})
	require.NoError(t, err)
	command, _ := json.Marshal(jobhandler.MailRekeyCommand{PrimaryKeyID: newSealer.PrimaryKeyID(), RetiringKeyID: oldSealer.PrimaryKeyID()})
	rekeyJob, err := model.NewJob(model.NewJobID(), model.JobTypeMailRekey, command,
		"mail-rekey:"+newSealer.PrimaryKeyID()+":"+oldSealer.PrimaryKeyID(), at, at, 10)
	require.NoError(t, err)
	audit, err := persistence.Audit().Save(ctx, &model.AuditEvent{ActorID: user.ID, Action: string(model.ActionMailRekey),
		Resource:  model.Resource{Type: model.ResourceInstitution, ID: institution.ID.String()},
		ScopeType: model.RoleScopeInstitution, ScopeID: institution.ID.String(), Status: model.AuditStatusAttempt,
		NodeID: "new-primary-node", ClientType: string(model.SessionClientWeb), AuthMethod: "password"})
	require.NoError(t, err)
	_, err = persistence.Mail().StartRekey(ctx, &store.MailRekeyStart{PrimaryKeyID: newSealer.PrimaryKeyID(),
		RetiringKeyID: oldSealer.PrimaryKeyID(), Job: rekeyJob, AuditEventID: audit.ID, AuditAt: model.GetMillis()})
	require.NoError(t, err)

	oldDescriptor := integrationJobDescriptor(t, jobhandler.NewCatalog(jobhandler.CatalogDependencies{
		JobStore: persistence.Job(), MailRekey: persistence.Mail(), MailSealer: oldSealer,
	}), model.JobTypeMailRekey)
	oldDescriptor.BaseRetryDelay, oldDescriptor.MaximumRetryDelay = 10*time.Millisecond, 10*time.Millisecond
	oldDescriptor.LeaseDuration, oldDescriptor.HeartbeatInterval = mailRekeyClusterLease, mailRekeyClusterHeartbeat
	oldNode, err := jobengine.New(jobengine.Config{Store: persistence.Job(), Descriptors: []jobengine.Descriptor{oldDescriptor},
		NodeID: "old-primary-node", Diagnostics: &integrationJobDiagnostics{}, Policy: jobengine.Policy{PollInterval: 5 * time.Millisecond}})
	require.NoError(t, err)
	err = oldNode.Start(ctx)
	require.NoError(t, err)
	defer func() { _ = oldNode.Close() }()
	waitForMailRekeyJob(t, persistence.Job(), rekeyJob.ID, func(job *model.Job, attempts []model.JobAttempt) bool {
		return job.Status == model.JobStatusQueued && job.AttemptCount == 1 && job.MaximumAttempts == 11 &&
			len(attempts) == 1 && attempts[0].Status == model.JobAttemptStatusIncompatible
	})

	// Wait beyond the persisted retry delay: the same stable incompatible node
	// must not reclaim and hot-loop when no capable peer is present.
	time.Sleep(40 * time.Millisecond)
	attempts, err := persistence.Job().ListAttempts(ctx, rekeyJob.ID)
	require.NoError(t, err, "no-compatible-node attempts")
	require.Len(t, attempts, 1, "no-compatible-node attempts")
	require.Equal(t, "old-primary-node", attempts[0].NodeID, "no-compatible-node attempts")

	newDescriptor := integrationJobDescriptor(t, jobhandler.NewCatalog(jobhandler.CatalogDependencies{
		JobStore: persistence.Job(), MailRekey: persistence.Mail(), MailSealer: newSealer,
	}), model.JobTypeMailRekey)
	newDescriptor.BaseRetryDelay, newDescriptor.MaximumRetryDelay = 10*time.Millisecond, 10*time.Millisecond
	newDescriptor.LeaseDuration, newDescriptor.HeartbeatInterval = mailRekeyClusterLease, mailRekeyClusterHeartbeat
	newNode, err := jobengine.New(jobengine.Config{Store: persistence.Job(), Descriptors: []jobengine.Descriptor{newDescriptor},
		NodeID: "new-primary-node", Diagnostics: &integrationJobDiagnostics{}, Policy: jobengine.Policy{PollInterval: 5 * time.Millisecond}})
	require.NoError(t, err)
	err = newNode.Start(ctx)
	require.NoError(t, err)
	defer func() { _ = newNode.Close() }()
	waitForMailRekeyJob(t, persistence.Job(), rekeyJob.ID, func(job *model.Job, attempts []model.JobAttempt) bool {
		return job.Status == model.JobStatusSucceeded && len(attempts) == 2 &&
			attempts[0].Status == model.JobAttemptStatusIncompatible && attempts[1].Status == model.JobAttemptStatusSucceeded
	})

	completed, err := persistence.Job().Get(ctx, rekeyJob.ID)
	require.NoError(t, err)
	var proof jobhandler.MailRekeyResult
	err = json.Unmarshal(completed.Result, &proof)
	require.NoError(t, err, "completed retirement proof = %#v", proof)
	require.True(t, proof.RetirementSafe, "completed retirement proof = %#v", proof)
	require.Equal(t, int64(0), proof.NonPrimaryReferences, "completed retirement proof = %#v", proof)
	require.Equal(t, int64(0), proof.RetiringReferences, "completed retirement proof = %#v", proof)
}

// Neither node dies here, so leases only have to survive scheduling delay on a
// loaded host: a missed heartbeat must not expire a live claim and add an
// attempt. Convergence is polled against a generous bound for the same reason.
const (
	mailRekeyClusterLease       = 10 * time.Second
	mailRekeyClusterHeartbeat   = time.Second
	mailRekeyClusterConvergence = 30 * time.Second
)

func waitForMailRekeyJob(t *testing.T, jobs store.JobStore, jobID model.JobID,
	ready func(*model.Job, []model.JobAttempt) bool,
) {
	t.Helper()
	deadline := time.Now().Add(mailRekeyClusterConvergence)
	for {
		job, err := jobs.Get(context.Background(), jobID)
		require.NoError(t, err)
		attempts, err := jobs.ListAttempts(context.Background(), jobID)
		require.NoError(t, err)
		if ready(job, attempts) {
			return
		}
		require.False(t, time.Now().After(deadline), "mail rekey Job did not converge: job=%#v attempts=%#v", job, attempts)
		time.Sleep(5 * time.Millisecond)
	}
}
