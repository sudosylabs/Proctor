// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package storetest

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

// changeEmailWithUndo applies an email change and returns the raw undo
// credential mailed to the previous address.
func changeEmailWithUndo(t *testing.T, ctx context.Context, ss store.Store, user *model.User, newEmail string) (*store.UserEmailChangeResult, string) {
	t.Helper()
	at := model.TimeUTC(time.Now().Add(time.Second))
	token := &model.UserToken{UserID: user.ID, Purpose: model.UserTokenEmailVerification,
		TokenHash: model.HashToken(model.NewCredentialToken()), Target: newEmail, ExpiresAt: at.Add(time.Hour)}
	token.PrepareCreate(model.NewUserTokenID(), at)
	rawUndo := model.NewCredentialToken()
	undo := &model.UserToken{UserID: user.ID, Purpose: model.UserTokenEmailChangeUndo,
		TokenHash: model.HashToken(rawUndo), Target: user.Email, ExpiresAt: at.Add(72 * time.Hour)}
	undo.PrepareCreate(model.NewUserTokenID(), at)
	warningOccurrence, warningDelivery, warningJob := userTokenMailFixture(t, user.ID, model.MailOccurrenceID(undo.ID.String()),
		model.MailOccurrenceAccountToken, model.MailTemplateIdentityEmailChangeWarningOld, model.JobTypeMailDeliverCredential, at, undo.ExpiresAt)
	verifyOccurrence, verifyDelivery, verifyJob := userTokenMailFixture(t, user.ID, model.MailOccurrenceID(token.ID.String()),
		model.MailOccurrenceAccountToken, model.MailTemplateIdentityEmailChangeVerifyNew, model.JobTypeMailDeliverCredential, at, token.ExpiresAt)
	audit := saveUserProfileAuditAttempt(t, ctx, ss, user.ID.String())
	result, err := ss.UserToken().ChangeEmail(ctx, &store.UserEmailChange{UserID: user.ID, ExpectedRevision: user.Revision, NewEmail: newEmail,
		Token: token, UndoToken: undo, TokenLifetime: time.Hour, WarningLifetime: 72 * time.Hour,
		WarningOccurrence: warningOccurrence, WarningDelivery: warningDelivery, WarningJob: warningJob,
		VerificationOccurrence: verifyOccurrence, VerificationDelivery: verifyDelivery, VerificationJob: verifyJob,
		AuditEventID: audit.ID, AuditAt: at})
	require.NoError(t, err)
	return result, rawUndo
}

func undoEmailChange(ctx context.Context, ss store.Store, rawUndo, institutionID string) (*store.EmailChangeUndoResult, error) {
	return ss.UserToken().ConsumeEmailChangeUndo(ctx, &store.EmailChangeUndo{TokenHash: model.HashToken(rawUndo),
		RevocationReason: model.SessionRevocationEmailChangeUndone,
		AuditEvent:       userTokenCompletionAudit(string(model.ActionAuthenticationEmailChangeUndo), institutionID)})
}

func testEmailChangeUndoRestoresPreviousAddress(t *testing.T, ss store.Store) {
	ctx := context.Background()
	institution := saveInstitution(t, ctx, ss)
	user, _ := saveLocalUser(t, ctx, ss)
	previous := user.Email
	session, credentials, _ := newSession(user.ID.String())
	session, _, err := SaveSession(ctx, ss, testSessionCreation(t, ctx, ss, session, credentials, 10))
	require.NoError(t, err)
	changed, rawUndo := changeEmailWithUndo(t, ctx, ss, user, model.NewID()+"@attacker.example.edu")
	require.Equal(t, previous, changed.UndoToken.Target)
	require.Equal(t, model.HashToken(rawUndo), changed.UndoToken.TokenHash)
	notice := findUserTokenDelivery(t, ctx, ss, changed.UndoToken.ID, model.MailTemplateIdentityEmailChangeWarningOld)
	require.Equal(t, model.MailDeliveryQueued, notice.State)

	result, err := undoEmailChange(ctx, ss, rawUndo, institution.ID.String())
	require.NoError(t, err)
	require.Equal(t, previous, result.User.Email)
	require.True(t, result.User.EmailVerified, "redeeming the link proves control of the previous address")
	require.Len(t, result.RevokedSessions, 1)
	require.Equal(t, session.ID, result.RevokedSessions[0].ID)
	revoked, err := ss.Session().Get(ctx, session.ID)
	require.NoError(t, err)
	require.Equal(t, model.SessionRevocationEmailChangeUndone, revoked.RevocationReason)
	verification, err := ss.UserToken().Get(ctx, changed.Token.ID)
	require.NoError(t, err)
	require.True(t, verification.ArchivedAt.Valid, "the replaced address can still verify")
	audits, err := ss.Audit().List(ctx, store.AuditListOptions{Action: string(model.ActionAuthenticationEmailChangeUndo), Limit: 50, Visibility: store.AuditVisibilityScope{InstitutionWide: true}})
	require.NoError(t, err)
	found := false
	for _, event := range audits {
		found = found || event.Resource.ID == user.ID.String()
	}
	require.True(t, found, "the undo was not audited")

	_, err = undoEmailChange(ctx, ss, rawUndo, institution.ID.String())
	require.True(t, store.IsNotFound(err), "a redeemed undo link worked twice: %v", err)
}

func testEmailChangeUndoRefusesAnAddressAnotherUserHolds(t *testing.T, ss store.Store) {
	ctx := context.Background()
	institution := saveInstitution(t, ctx, ss)
	user, _ := saveLocalUser(t, ctx, ss)
	previous := user.Email
	_, rawUndo := changeEmailWithUndo(t, ctx, ss, user, model.NewID()+"@moved.example.edu")
	squatter := newUser()
	squatter.Email = previous
	_, err := createUser(t, ctx, ss, squatter)
	require.NoError(t, err)

	_, err = undoEmailChange(ctx, ss, rawUndo, institution.ID.String())
	require.True(t, store.IsConflict(err), "undo onto a taken address = %v", err)
	unchanged, err := ss.User().Get(ctx, user.ID)
	require.NoError(t, err)
	require.NotEqual(t, previous, unchanged.Email)
}
