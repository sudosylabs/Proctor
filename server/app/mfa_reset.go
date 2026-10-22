// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package app

import (
	"context"
	"errors"
	"strings"
	"unicode/utf8"

	"github.com/sudosylabs/proctor/server/app/apperror"
	"github.com/sudosylabs/proctor/server/app/invocation"
	appmail "github.com/sudosylabs/proctor/server/app/mail"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

// ResetUserMFACommand records an institution-assisted verification. The
// reference identifies the institution's verification record; it must not
// contain documents, passwords, factor secrets or recovery codes.
type ResetUserMFACommand struct {
	UserID                model.UserID
	IdentityVerified      bool
	Reason                string
	VerificationReference string
}

func (s *mfaApplicationService) ResetUserMFA(ctx context.Context, invocation invocation.Invocation, command ResetUserMFACommand) error {
	principal := invocation.Principal()
	if err := s.requireStrongRecentSession(principal); err != nil {
		return err
	}
	if err := s.requireEnabled(); err != nil {
		return err
	}
	userID, err := model.ParseUserID(command.UserID.String())
	reason, reference := strings.TrimSpace(command.Reason), strings.TrimSpace(command.VerificationReference)
	if err != nil || userID == principal.UserID || !command.IdentityVerified || reason == "" || utf8.RuneCountInString(reason) > 512 || reference == "" || utf8.RuneCountInString(reference) > 128 {
		return apperror.New("authentication.mfa.reset_invalid")
	}
	resource := model.Resource{Type: model.ResourceUser, ID: userID.String()}
	if err = s.security.authorization.Authorize(ctx, invocation, model.ActionUserMFAReset, resource); err != nil {
		return err
	}
	at := model.TimeFromMillis(s.now().UnixMilli())
	prepared, err := s.prepareSecurityNotice(ctx, userID, appmail.MFANoticeReset, at)
	if err != nil {
		return err
	}
	auditID, err := s.audit.Begin(ctx, invocation, model.ActionUserMFAReset, resource, map[string]any{"identity_verified": true, "reason": reason, "verification_reference": reference})
	if err != nil {
		return err
	}
	result, err := s.credentials.Reset(ctx, &store.MFAAssistedReset{Principal: principal, UserID: userID, IdentityVerified: true, Reason: reason, VerificationReference: reference, RecentAuthenticationTTL: s.recentAuthenticationTTL, AuditEventID: auditID, Capabilities: accessDeploymentCapabilities(s.security.capabilities.Snapshot()), Notice: mfaSecurityNotice(prepared), NoticeAt: at})
	if err != nil {
		return s.failMutation(ctx, auditID, err)
	}
	if result == nil || result.Recovery == nil {
		return authenticationUnavailable(errors.New("MFA reset returned no recovery state"))
	}
	s.effects.SessionsRevoked(ctx, userID.String(), mfaResetSessionIDs(result.Sessions), result.AccessTokenHashes)
	return nil
}

func mfaResetSessionIDs(sessions []*model.Session) []string {
	ids := make([]string, 0, len(sessions))
	for _, session := range sessions {
		ids = append(ids, session.ID.String())
	}
	return ids
}
