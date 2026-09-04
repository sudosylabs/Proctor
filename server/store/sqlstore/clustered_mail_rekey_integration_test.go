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
	"reflect"
	"testing"
	"time"

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
	if err != nil {
		t.Fatal(err)
	}
	user := saveIntegrationUser(t, ctx, nodeA, &model.User{Username: "mail-rekey", Email: "mail-rekey@example.edu"})
	oldKey := base64.StdEncoding.EncodeToString([]byte("old-mail-rekey-key-material-0001"))
	newKey := base64.StdEncoding.EncodeToString([]byte("new-mail-rekey-key-material-0001"))
	oldSealer, err := secretseal.New(secretseal.Settings{EncryptionKey: oldKey, MaximumPlaintext: 1024})
	if err != nil {
		t.Fatal(err)
	}
	newSealer, err := secretseal.New(secretseal.Settings{EncryptionKey: newKey, DecryptionKeys: []string{oldKey}, MaximumPlaintext: 1024})
	if err != nil {
		t.Fatal(err)
	}
	bundleID := model.NewId()
	binding := secretseal.Binding{Purpose: "mail.fanout_bundle", Owner: bundleID}
	oldEnvelope, err := oldSealer.Seal(binding, []byte(`{"version":1,"template":"frozen"}`))
	if err != nil {
		t.Fatal(err)
	}
	oldDocument, _ := json.Marshal(oldEnvelope)
	tx, err := nodeA.GetMaster().DB().BeginTxx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = tx.ExecContext(ctx, nodeA.GetMaster().DB().Rebind(`INSERT INTO mail_fanout_bundles(id,payload_key_id,encrypted_payload,created_at,revision) VALUES(?,?,?,?,1)`),
		bundleID, oldEnvelope.KeyID, oldDocument, at); err == nil {
		_, err = tx.ExecContext(ctx, nodeA.GetMaster().DB().Rebind(`INSERT INTO mail_payload_keys(key_id,active_references) VALUES(?,1)`), oldEnvelope.KeyID)
	}
	if err != nil {
		_ = tx.Rollback()
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}

	start := clusteredMailRekeyStart(t, ctx, nodeA, user, institution, newSealer.PrimaryKeyID(), oldEnvelope.KeyID, at)
	if _, err = nodeA.Mail().StartRekey(ctx, start); err != nil {
		t.Fatal(err)
	}
	firstToken := clusteredMailRekeyClaimToken(t)
	first, err := nodeA.Job().ClaimNext(ctx, &store.JobClaimRequest{Types: []model.JobType{model.JobTypeMailRekey},
		NodeID: "mail-rekey-node-a", ClaimToken: firstToken, LeaseDuration: 20 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	checkpoint := json.RawMessage(`{"after_kind":"delivery","after_id":"00000000000000000000000000","processed":1,"reencrypted":1}`)
	if _, err = nodeA.Job().Checkpoint(ctx, &store.JobCheckpoint{AttemptID: first.Attempt.ID, ClaimToken: firstToken,
		CheckpointVersion: 1, Checkpoint: checkpoint}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(30 * time.Millisecond)
	secondToken := clusteredMailRekeyClaimToken(t)
	second, err := nodeB.Job().ClaimNext(ctx, &store.JobClaimRequest{Types: []model.JobType{model.JobTypeMailRekey},
		NodeID: "mail-rekey-node-b", ClaimToken: secondToken, LeaseDuration: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	var wantCheckpoint, gotCheckpoint map[string]any
	if err = json.Unmarshal(checkpoint, &wantCheckpoint); err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(second.Job.Checkpoint, &gotCheckpoint); err != nil {
		t.Fatal(err)
	}
	if second.Job.ID != first.Job.ID || second.Job.CheckpointVersion != 1 || !reflect.DeepEqual(gotCheckpoint, wantCheckpoint) {
		t.Fatalf("recovered rekey Job = %#v", second.Job)
	}
	if _, err = nodeA.Job().Checkpoint(ctx, &store.JobCheckpoint{AttemptID: first.Attempt.ID, ClaimToken: firstToken,
		CheckpointVersion: 1, Checkpoint: checkpoint}); !store.IsConflict(err) {
		t.Fatalf("stale rekey checkpoint error = %v", err)
	}
	page, err := nodeB.Mail().ListRekeyTargets(ctx, &store.MailRekeyTargetPageRequest{JobID: second.Job.ID,
		PrimaryKeyID: newSealer.PrimaryKeyID(), Limit: 100})
	if err != nil || len(page.Targets) != 1 || page.Targets[0].Kind != store.MailRekeyTargetFanoutBundle {
		t.Fatalf("fan-out rekey page = %#v, %v", page, err)
	}
	var persisted secretseal.Envelope
	if err = json.Unmarshal(page.Targets[0].EncryptedPayload, &persisted); err != nil {
		t.Fatal(err)
	}
	plaintext, err := newSealer.Open(binding, persisted)
	if err != nil {
		t.Fatalf("fallback read failed: %v", err)
	}
	rotated, err := newSealer.Seal(binding, plaintext)
	clear(plaintext)
	if err != nil || rotated.KeyID != newSealer.PrimaryKeyID() {
		t.Fatalf("primary promotion envelope = %#v, %v", rotated, err)
	}
	rotatedDocument, _ := json.Marshal(rotated)
	applied, err := nodeB.Mail().ReplaceRekeyTarget(ctx, &store.MailRekeyReplacement{JobID: second.Job.ID,
		Kind: page.Targets[0].Kind, ID: page.Targets[0].ID, ExpectedKeyID: page.Targets[0].KeyID,
		PrimaryKeyID: newSealer.PrimaryKeyID(), EncryptedPayload: rotatedDocument})
	if err != nil || !applied {
		t.Fatalf("ReplaceRekeyTarget() = %t, %v", applied, err)
	}
	proof, err := nodeA.Mail().ProveRekey(ctx, &store.MailRekeyProofRequest{JobID: second.Job.ID,
		PrimaryKeyID: newSealer.PrimaryKeyID(), RetiringKeyID: oldEnvelope.KeyID})
	if err != nil || !proof.RetirementSafe || proof.NonPrimaryReferences != 0 || proof.RetiringReferences != 0 {
		t.Fatalf("clustered retirement proof = %#v, %v", proof, err)
	}
	result := json.RawMessage(`{"retirement_safe":true,"non_primary_references":0}`)
	completed, err := nodeB.Job().Complete(ctx, &store.JobCompletion{AttemptID: second.Attempt.ID, ClaimToken: secondToken,
		Kind: store.JobCompletionSucceeded, ResultVersion: 1, Result: result})
	if err != nil || completed.Status != model.JobStatusSucceeded || string(completed.Result) != string(result) {
		t.Fatalf("complete rekey Job = %#v, %v", completed, err)
	}
}

func clusteredMailRekeyStart(t *testing.T, ctx context.Context, persistence store.Store, user *model.User, institution *model.Institution,
	primaryKeyID, retiringKeyID string, at time.Time,
) *store.MailRekeyStart {
	t.Helper()
	command, _ := json.Marshal(map[string]string{"primary_key_id": primaryKeyID, "retiring_key_id": retiringKeyID})
	job, err := model.NewJob(model.NewJobID(), model.JobTypeMailRekey, 1, command,
		"mail-rekey:"+primaryKeyID+":"+retiringKeyID, at, at, 10)
	if err != nil {
		t.Fatal(err)
	}
	attempt, err := persistence.Audit().Save(ctx, &model.AuditEvent{ActorID: user.ID, Action: string(model.ActionMailRekey),
		Resource:  model.Resource{Type: model.ResourceInstitution, ID: institution.ID.String()},
		ScopeType: model.RoleScopeInstitution, ScopeID: institution.ID.String(), Status: model.AuditStatusAttempt,
		NodeID: "mail-rekey-node-a", ClientType: string(model.SessionClientWeb), AuthMethod: "password"})
	if err != nil {
		t.Fatal(err)
	}
	return &store.MailRekeyStart{PrimaryKeyID: primaryKeyID, RetiringKeyID: retiringKeyID, Job: job,
		AuditEventID: attempt.ID.String(), AuditAt: model.GetMillis()}
}

func clusteredMailRekeyClaimToken(t *testing.T) model.JobClaimToken {
	t.Helper()
	token, err := model.NewJobClaimToken()
	if err != nil {
		t.Fatal(err)
	}
	return token
}
