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
	"testing"
	"time"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

func TestPersonalAccessTokenPreparationMaintenanceIsBoundedAndAppendOnly(t *testing.T) {
	persistence := openTestStore(t)
	resetTestStore(t, persistence)
	ctx := context.Background()
	institution, err := persistence.Institution().Save(ctx, &model.Institution{Name: "pat-preparations", DisplayName: "PAT Preparations"})
	if err != nil {
		t.Fatal(err)
	}
	user := saveIntegrationUser(t, ctx, persistence, &model.User{Username: "pat-preparations", Email: "pat-preparations@example.edu", DisplayName: "PAT Preparations"})
	session := savePersonalAccessTokenMutationSession(t, ctx, persistence, user.ID)

	for range 2 {
		prepared, prepareErr := persistence.PersonalAccessToken().PrepareMutation(ctx, &store.PersonalAccessTokenMutationPreparation{
			UserID: user.ID.String(), Kind: store.PersonalAccessTokenMutationCreate, Lifetime: 30 * time.Second,
			Audit: personalAccessTokenPreparationAudit(user.ID, session.ID, institution.ID),
		})
		if prepareErr != nil {
			t.Fatal(prepareErr)
		}
		if _, updateErr := persistence.GetMaster().Exec(ctx, `UPDATE personal_access_token_mutation_preparations SET created_at=clock_timestamp()-interval '31 seconds', expires_at=clock_timestamp()-interval '1 second' WHERE id=?`, prepared.ID); updateErr != nil {
			t.Fatal(updateErr)
		}
	}

	first, err := persistence.PersonalAccessToken().MaintainMutationPreparations(ctx, 1)
	if err != nil || first.Failed != 1 || !first.More {
		t.Fatalf("first maintenance=%#v err=%v", first, err)
	}
	second, err := persistence.PersonalAccessToken().MaintainMutationPreparations(ctx, 1)
	if err != nil || second.Failed != 1 || second.More {
		t.Fatalf("second maintenance=%#v err=%v", second, err)
	}
	var remaining int
	if err := persistence.GetMaster().Get(ctx, &remaining, `SELECT COUNT(*) FROM personal_access_token_mutation_preparations`); err != nil || remaining != 0 {
		t.Fatalf("remaining preparations=%d err=%v", remaining, err)
	}
	events, err := persistence.Audit().List(ctx, store.AuditListOptions{ActorId: user.ID.String(), Action: "personal_access_token.create", Limit: 10, Visibility: store.AuditVisibilityScope{InstitutionWide: true}})
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 {
		t.Fatalf("terminal failure audits=%#v, want 2", events)
	}
	for _, event := range events {
		if event.Status != model.AuditStatusFail || event.ErrorCode != personalAccessTokenPreparationFailureCode {
			t.Fatalf("maintenance audit=%#v", event)
		}
	}
}

// TestPersonalAccessTokenPreparationExpiryIsCheckedAfterUserFence proves that
// a terminal mutation cannot validate a preparation against time sampled
// before waiting behind the canonical per-user PAT fence.
func TestPersonalAccessTokenPreparationExpiryIsCheckedAfterUserFence(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	persistence := openTestStore(t)
	resetTestStore(t, persistence)
	institution, err := persistence.Institution().Save(ctx, &model.Institution{Name: "pat-preparation-fence", DisplayName: "PAT Preparation Fence"})
	if err != nil {
		t.Fatal(err)
	}
	user := saveIntegrationUser(t, ctx, persistence, &model.User{Username: "pat-preparation-fence", Email: "pat-preparation-fence@example.edu"})
	session := savePersonalAccessTokenMutationSession(t, ctx, persistence, user.ID)
	token := &model.PersonalAccessToken{
		UserID: user.ID, Description: "fenced expiry", TokenHash: model.HashToken(model.NewCredentialToken()),
		Scopes: []string{string(model.ActionClassView)}, ExpiresAt: model.NowUTC().Add(time.Hour),
	}
	token.PrepareCreate(model.NewPersonalAccessTokenID(), model.NowUTC())
	if err := insertPersonalAccessToken(ctx, persistence.GetMaster(), token); err != nil {
		t.Fatal(err)
	}
	preparation, err := persistence.PersonalAccessToken().PrepareMutation(ctx, &store.PersonalAccessTokenMutationPreparation{
		UserID: user.ID.String(), TokenID: token.ID.String(), Kind: store.PersonalAccessTokenMutationDisable, Lifetime: 30 * time.Second,
		Audit: &model.AuditEvent{ActorID: user.ID, SessionID: session.ID, Action: "personal_access_token.disable",
			Resource: model.Resource{Type: model.ResourceInstitution, ID: institution.ID.String()}, ScopeType: model.RoleScopeInstitution,
			ScopeID: institution.ID.String(), Status: model.AuditStatusAttempt, NodeID: "pat-preparation-fence"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = persistence.GetMaster().Exec(ctx, `UPDATE personal_access_token_mutation_preparations SET expires_at=clock_timestamp()+interval '150 milliseconds' WHERE id=?`, preparation.ID); err != nil {
		t.Fatal(err)
	}

	controller, err := persistence.GetMaster().DB().Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer controller.Close()
	const fencePrefix = "personal_access_tokens:user:"
	if _, err = controller.ExecContext(ctx, `SELECT pg_advisory_lock(hashtextextended($1, 0))`, fencePrefix+user.ID.String()); err != nil {
		t.Fatal(err)
	}
	locked := true
	defer func() {
		if locked {
			_, _ = controller.ExecContext(context.Background(), `SELECT pg_advisory_unlock(hashtextextended($1, 0))`, fencePrefix+user.ID.String())
		}
	}()
	var controllerPID int
	if err = controller.QueryRowContext(ctx, `SELECT pg_backend_pid()`).Scan(&controllerPID); err != nil {
		t.Fatal(err)
	}
	result := make(chan error, 1)
	go func() {
		_, changeErr := persistence.PersonalAccessToken().ChangeState(ctx, &store.PersonalAccessTokenStateMutation{
			ID: token.ID.String(), UserID: user.ID.String(), Disabled: true, MaximumActive: 10,
			PreparationID: preparation.ID,
		})
		result <- changeErr
	}()
	_ = waitForBlockedMailQuery(t, ctx, persistence, controllerPID, "pg_advisory_xact_lock")
	time.Sleep(200 * time.Millisecond)
	if _, err = controller.ExecContext(ctx, `SELECT pg_advisory_unlock(hashtextextended($1, 0))`, fencePrefix+user.ID.String()); err != nil {
		t.Fatal(err)
	}
	locked = false
	if err := <-result; !store.IsConflict(err) {
		t.Fatalf("ChangeState() after blocked expiry error = %v, want conflict", err)
	}
}

func personalAccessTokenPreparationAudit(userID model.UserID, sessionID model.SessionID, institutionID model.InstitutionID) *model.AuditEvent {
	return &model.AuditEvent{
		ActorID: userID, SessionID: sessionID, Action: "personal_access_token.create",
		Resource:  model.Resource{Type: model.ResourceInstitution, ID: institutionID.String()},
		ScopeType: model.RoleScopeInstitution, ScopeID: institutionID.String(),
		Status: model.AuditStatusAttempt, NodeID: "pat-preparation-test",
		ClientType: string(model.SessionClientWeb), AuthMethod: "password",
	}
}

func savePersonalAccessTokenMutationSession(t *testing.T, ctx context.Context, persistence *SQLStore, userID model.UserID) *model.Session {
	t.Helper()
	now := model.NowUTC()
	session, _, err := persistence.Session().Save(ctx, &model.Session{
		UserID: userID, ClientType: model.SessionClientWeb,
		AuthenticationMethod: "password", AuthenticationStrength: model.AuthenticationSingleFactor,
		IdleExpiresAt: now.Add(time.Hour), ExpiresAt: now.Add(2 * time.Hour),
	}, []*model.SessionCredential{
		{Kind: model.SessionCredentialAccess, TokenHash: model.HashToken(model.NewCredentialToken()), ExpiresAt: now.Add(30 * time.Minute)},
		{Kind: model.SessionCredentialRefresh, TokenHash: model.HashToken(model.NewCredentialToken()), ExpiresAt: now.Add(2 * time.Hour)},
	}, 100)
	if err != nil {
		t.Fatal(err)
	}
	return session
}
