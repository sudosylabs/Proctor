//go:build integration

// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package sqlstore

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/secretseal"
	"github.com/sudosylabs/proctor/server/store"
)

func TestClusteredMailRekeyRecoversCheckpointAndRotatesFanoutBundle(t *testing.T) {
	ctx := context.Background()
	nodeA := openTestStore(t)
	resetTestStore(t, nodeA)
	nodeB := openPeerTestStore(t)
	at := model.NowUTC().Add(-time.Second)
	institution, err := nodeA.Institution().Save(ctx, &model.Institution{Name: "mail-rekey", DisplayName: "Mail Rekey"})
	require.NoError(t, err)
	user := saveIntegrationUser(t, ctx, nodeA, &model.User{Username: "mail-rekey", Email: "mail-rekey@example.edu"})
	oldKey := base64.StdEncoding.EncodeToString([]byte("old-mail-rekey-key-material-0001"))
	newKey := base64.StdEncoding.EncodeToString([]byte("new-mail-rekey-key-material-0001"))
	oldSealer, err := secretseal.New(secretseal.Settings{EncryptionKey: oldKey, MaximumPlaintext: 1024})
	require.NoError(t, err)
	newSealer, err := secretseal.New(secretseal.Settings{EncryptionKey: newKey, DecryptionKeys: []string{oldKey}, MaximumPlaintext: 1024})
	require.NoError(t, err)
	bundleID := model.NewID()
	binding := secretseal.Binding{Purpose: "mail.fanout_bundle", Owner: bundleID}
	oldEnvelope, err := oldSealer.Seal(binding, []byte(`{"version":1,"template":"frozen"}`))
	require.NoError(t, err)
	oldDocument, _ := json.Marshal(oldEnvelope)
	tx, err := nodeA.GetMaster().DB().BeginTxx(ctx, nil)
	require.NoError(t, err)
	if _, err = tx.ExecContext(ctx, nodeA.GetMaster().DB().Rebind(`INSERT INTO mail_fanout_bundles(id,payload_key_id,encrypted_payload,created_at,revision) VALUES(?,?,?,?,1)`),
		bundleID, oldEnvelope.KeyID, oldDocument, at); err == nil {
		_, err = tx.ExecContext(ctx, nodeA.GetMaster().DB().Rebind(`INSERT INTO mail_payload_keys(key_id,active_references) VALUES(?,1)`), oldEnvelope.KeyID)
	}
	if err != nil {
		_ = tx.Rollback()
		require.NoError(t, err)
	}
	err = tx.Commit()
	require.NoError(t, err)

	start := clusteredMailRekeyStart(t, ctx, nodeA, user, institution, newSealer.PrimaryKeyID(), oldEnvelope.KeyID, at)
	_, err = nodeA.Mail().StartRekey(ctx, start)
	require.NoError(t, err)
	firstToken := clusteredMailRekeyClaimToken(t)
	first, err := nodeA.Job().ClaimNext(ctx, &store.JobClaimRequest{Types: []model.JobType{model.JobTypeMailRekey},
		NodeID: "mail-rekey-node-a", ClaimToken: firstToken, LeaseDuration: time.Minute})
	require.NoError(t, err)
	checkpoint := json.RawMessage(`{"after_kind":"delivery","after_id":"00000000000000000000000000","processed":1,"reencrypted":1}`)
	_, err = nodeA.Job().Checkpoint(ctx, &store.JobCheckpoint{AttemptID: first.Attempt.ID, ClaimToken: firstToken,
		CheckpointVersion: 1, Checkpoint: checkpoint})
	require.NoError(t, err)
	// Node A dies after its checkpoint. Expire its lease in PostgreSQL time
	// rather than racing a short wall-clock lease against statement latency.
	_, err = nodeA.GetMaster().Exec(ctx, `UPDATE job_attempts SET lease_expires_at=heartbeat_at+INTERVAL '1 microsecond' WHERE id=?`,
		first.Attempt.ID.String())
	require.NoError(t, err)
	secondToken := clusteredMailRekeyClaimToken(t)
	second, err := nodeB.Job().ClaimNext(ctx, &store.JobClaimRequest{Types: []model.JobType{model.JobTypeMailRekey},
		NodeID: "mail-rekey-node-b", ClaimToken: secondToken, LeaseDuration: time.Minute})
	require.NoError(t, err)
	var wantCheckpoint, gotCheckpoint map[string]any
	err = json.Unmarshal(checkpoint, &wantCheckpoint)
	require.NoError(t, err)
	err = json.Unmarshal(second.Job.Checkpoint, &gotCheckpoint)
	require.NoError(t, err)
	require.Equal(t, first.Job.ID, second.Job.ID)
	require.Equal(t, 1, second.Job.CheckpointVersion)
	require.Equal(t, wantCheckpoint, gotCheckpoint)
	_, err = nodeA.Job().Checkpoint(ctx, &store.JobCheckpoint{AttemptID: first.Attempt.ID, ClaimToken: firstToken,
		CheckpointVersion: 1, Checkpoint: checkpoint})
	require.True(t, store.IsConflict(err), "stale rekey checkpoint error")
	page, err := nodeB.Mail().ListRekeyTargets(ctx, &store.MailRekeyTargetPageRequest{JobID: second.Job.ID,
		PrimaryKeyID: newSealer.PrimaryKeyID(), Limit: 100})
	require.NoError(t, err, "fan-out rekey page = %#v", page)
	require.Len(t, page.Targets, 1, "fan-out rekey page = %#v", page)
	require.Equal(t, store.MailRekeyTargetFanoutBundle, page.Targets[0].Kind, "fan-out rekey page = %#v", page)
	var persisted secretseal.Envelope
	err = json.Unmarshal(page.Targets[0].EncryptedPayload, &persisted)
	require.NoError(t, err)
	plaintext, err := newSealer.Open(binding, persisted)
	require.NoError(t, err)
	rotated, err := newSealer.Seal(binding, plaintext)
	clear(plaintext)
	require.NoError(t, err, "primary promotion envelope = %#v", rotated)
	require.Equal(t, newSealer.PrimaryKeyID(), rotated.KeyID, "primary promotion envelope = %#v", rotated)
	rotatedDocument, _ := json.Marshal(rotated)
	applied, err := nodeB.Mail().ReplaceRekeyTarget(ctx, &store.MailRekeyReplacement{JobID: second.Job.ID,
		Kind: page.Targets[0].Kind, ID: page.Targets[0].ID, ExpectedKeyID: page.Targets[0].KeyID,
		PrimaryKeyID: newSealer.PrimaryKeyID(), EncryptedPayload: rotatedDocument})
	require.NoError(t, err, "ReplaceRekeyTarget()")
	require.True(t, applied, "ReplaceRekeyTarget()")
	proof, err := nodeA.Mail().ProveRekey(ctx, &store.MailRekeyProofRequest{JobID: second.Job.ID,
		PrimaryKeyID: newSealer.PrimaryKeyID(), RetiringKeyID: oldEnvelope.KeyID})
	require.NoError(t, err, "clustered retirement proof = %#v", proof)
	require.True(t, proof.RetirementSafe, "clustered retirement proof = %#v", proof)
	require.Equal(t, int64(0), proof.NonPrimaryReferences, "clustered retirement proof = %#v", proof)
	require.Equal(t, int64(0), proof.RetiringReferences, "clustered retirement proof = %#v", proof)
	result := json.RawMessage(`{"retirement_safe":true,"non_primary_references":0}`)
	completed, err := nodeB.Job().Complete(ctx, &store.JobCompletion{AttemptID: second.Attempt.ID, ClaimToken: secondToken,
		Kind: store.JobCompletionSucceeded, ResultVersion: 1, Result: result})
	require.NoError(t, err, "complete rekey Job = %#v", completed)
	require.Equal(t, model.JobStatusSucceeded, completed.Status, "complete rekey Job = %#v", completed)
	require.Equal(t, string(result), string(completed.Result), "complete rekey Job = %#v", completed)
}

func clusteredMailRekeyStart(t *testing.T, ctx context.Context, persistence store.Store, user *model.User, institution *model.Institution,
	primaryKeyID, retiringKeyID string, at time.Time,
) *store.MailRekeyStart {
	t.Helper()
	command, _ := json.Marshal(map[string]string{"primary_key_id": primaryKeyID, "retiring_key_id": retiringKeyID})
	job, err := model.NewJob(model.NewJobID(), model.JobTypeMailRekey, 1, command,
		"mail-rekey:"+primaryKeyID+":"+retiringKeyID, at, at, 10)
	require.NoError(t, err)
	attempt, err := persistence.Audit().Save(ctx, &model.AuditEvent{ActorID: user.ID, Action: string(model.ActionMailRekey),
		Resource:  model.Resource{Type: model.ResourceInstitution, ID: institution.ID.String()},
		ScopeType: model.RoleScopeInstitution, ScopeID: institution.ID.String(), Status: model.AuditStatusAttempt,
		NodeID: "mail-rekey-node-a", ClientType: string(model.SessionClientWeb), AuthMethod: "password"})
	require.NoError(t, err)
	return &store.MailRekeyStart{PrimaryKeyID: primaryKeyID, RetiringKeyID: retiringKeyID, Job: job,
		AuditEventID: attempt.ID, AuditAt: model.GetMillis()}
}

func clusteredMailRekeyClaimToken(t *testing.T) model.JobClaimToken {
	t.Helper()
	token, err := model.NewJobClaimToken()
	require.NoError(t, err)
	return token
}
