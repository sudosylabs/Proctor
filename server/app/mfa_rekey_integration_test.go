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
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/server/app/job"
	"github.com/sudosylabs/proctor/server/app/jobhandler"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/secretseal"
	"github.com/sudosylabs/proctor/server/store"
	"github.com/sudosylabs/proctor/server/store/sqlstore"
)

func TestMFARekeyJobResealsEveryLiveSecretAndProvesRetirement(t *testing.T) {
	persistence := openClusteredJobIntegrationStore(t)
	ctx := context.Background()
	at := model.NowUTC().Add(-time.Second)
	institution, err := persistence.Institution().Save(ctx, &model.Institution{Name: "mfa-rekey", DisplayName: "MFA Rekey"})
	require.NoError(t, err)
	operator := createMFARekeyIntegrationUser(t, ctx, persistence, "mfa-rekey-operator", at)

	oldKey := base64.StdEncoding.EncodeToString([]byte("old-mfa-rekey-key-material-00001"))
	newKey := base64.StdEncoding.EncodeToString([]byte("new-mfa-rekey-key-material-00001"))
	oldSealer, err := secretseal.New(secretseal.Settings{EncryptionKey: oldKey, MaximumPlaintext: 128})
	require.NoError(t, err)
	newSealer, err := secretseal.New(secretseal.Settings{EncryptionKey: newKey, DecryptionKeys: []string{oldKey}, MaximumPlaintext: 128})
	require.NoError(t, err)
	secrets := map[model.UserID]string{}
	for index := range 3 {
		user := createMFARekeyIntegrationUser(t, ctx, persistence, fmt.Sprintf("mfa-rekey-user-%d", index), at)
		secret := fmt.Sprintf("JBSWY3DPEHPK3PX%d", index)
		envelope, sealErr := oldSealer.Seal(secretseal.Binding{Purpose: jobhandler.MFASealingPurpose, Owner: user.ID.String()}, []byte(secret))
		require.NoError(t, sealErr)
		encoded, encodeErr := json.Marshal(envelope)
		require.NoError(t, encodeErr)
		_, err = persistence.MFA().SeedPending(ctx, &model.MFACredential{UserID: user.ID, State: model.MFAStatePending,
			EncryptedSecret: string(encoded), EncryptionKeyID: envelope.KeyID, CreatedAt: model.NowUTC(),
			PendingExpiresAt: model.OptionalTimeFrom(model.NowUTC().Add(10 * time.Minute))})
		require.NoError(t, err)
		secrets[user.ID] = secret
	}

	command, err := json.Marshal(jobhandler.MFARekeyCommand{PrimaryKeyID: newSealer.PrimaryKeyID(), RetiringKeyID: oldSealer.PrimaryKeyID()})
	require.NoError(t, err)
	rekeyJob, err := model.NewJob(model.NewJobID(), model.JobTypeMFARekey, command,
		"mfa-rekey:"+newSealer.PrimaryKeyID()+":"+oldSealer.PrimaryKeyID(), at, at, 10)
	require.NoError(t, err)
	attempt, err := persistence.Audit().Save(ctx, &model.AuditEvent{ActorID: operator.ID, Action: string(model.ActionMFARekey),
		Resource:  model.Resource{Type: model.ResourceInstitution, ID: institution.ID.String()},
		ScopeType: model.RoleScopeInstitution, ScopeID: institution.ID.String(), Status: model.AuditStatusAttempt,
		NodeID: "mfa-rekey-node", ClientType: string(model.SessionClientWeb), AuthMethod: "password"})
	require.NoError(t, err)
	_, err = persistence.MFA().StartRekey(ctx, &store.MFARekeyStart{PrimaryKeyID: newSealer.PrimaryKeyID(),
		RetiringKeyID: oldSealer.PrimaryKeyID(), Job: rekeyJob, AuditEventID: attempt.ID, AuditAt: model.NowUTC()})
	require.NoError(t, err)

	descriptor := integrationJobDescriptor(t, jobhandler.NewCatalog(jobhandler.CatalogDependencies{
		JobStore: persistence.Job(), MFARekey: persistence.MFA(), MFASealer: newSealer,
	}), model.JobTypeMFARekey)
	node, err := job.New(job.Dependencies{Store: persistence.Job(), Descriptors: []job.Descriptor{descriptor},
		NodeID: "mfa-rekey-node", Diagnostics: &integrationJobDiagnostics{}, Policy: job.Policy{PollInterval: 5 * time.Millisecond}})
	require.NoError(t, err)
	require.NoError(t, node.Start(ctx))
	defer func() { _ = node.Close() }()
	waitForMailRekeyJob(t, persistence.Job(), rekeyJob.ID, func(job *model.Job, _ []model.JobAttempt) bool {
		return job.Status == model.JobStatusSucceeded
	})

	completed, err := persistence.Job().Get(ctx, rekeyJob.ID)
	require.NoError(t, err)
	result, err := jobhandler.DecodeMFARekeyResult(completed.Result)
	require.NoError(t, err)
	require.Equal(t, jobhandler.MFARekeyResult{PrimaryKeyID: newSealer.PrimaryKeyID(), RetiringKeyID: oldSealer.PrimaryKeyID(),
		Processed: 3, Resealed: 3, RetirementSafe: true}, result)
	for userID, secret := range secrets {
		credential, getErr := persistence.MFA().GetByUser(ctx, userID)
		require.NoError(t, getErr)
		require.Equal(t, newSealer.PrimaryKeyID(), credential.EncryptionKeyID)
		var envelope secretseal.Envelope
		require.NoError(t, json.Unmarshal([]byte(credential.EncryptedSecret), &envelope))
		retiredRing, ringErr := secretseal.New(secretseal.Settings{EncryptionKey: newKey, MaximumPlaintext: 128})
		require.NoError(t, ringErr)
		plaintext, openErr := retiredRing.Open(secretseal.Binding{Purpose: jobhandler.MFASealingPurpose, Owner: userID.String()}, envelope)
		require.NoError(t, openErr, "the secret opens without the retired key")
		require.Equal(t, secret, string(plaintext))
	}
	state, err := persistence.MFA().InspectKeyState(ctx)
	require.NoError(t, err)
	require.Equal(t, []store.MFASecretKeyUsage{{KeyID: newSealer.PrimaryKeyID(), Credentials: 3}}, state.Active)
	require.Equal(t, newSealer.PrimaryKeyID(), state.RequiredPrimaryKeyID)
}

func createMFARekeyIntegrationUser(t *testing.T, ctx context.Context, persistence *sqlstore.SQLStore, username string, at time.Time) *model.User {
	t.Helper()
	user, defaultJob, err := prepareUserDefaultProfilePictureJob(&model.User{Username: username, Email: username + "@example.test"}, at)
	require.NoError(t, err)
	settings, err := prepareInitialUserSettingsDocument(user)
	require.NoError(t, err)
	created, err := persistence.User().Create(ctx, &store.UserCreation{User: user, Settings: settings, DefaultProfilePictureJob: defaultJob})
	require.NoError(t, err)
	return created.User
}
