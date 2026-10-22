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
	"time"

	"github.com/sudosylabs/proctor/server/app/invocation"
	"github.com/sudosylabs/proctor/server/app/mail"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

type accountTokenPasswordHasher interface {
	Hash(context.Context, string) (string, error)
}

type accountTokenEffects interface {
	SessionsRevoked(context.Context, string, []string, []string)
}

type accountTokenMailPreparer interface {
	Enabled() bool
	PrepareEmailVerification(mail.AccountTokenPreparation) (*store.PreparedMail, error)
	PreparePasswordReset(mail.AccountTokenPreparation) (*store.PreparedMail, error)
	PreparePasswordChanged(mail.NoticePreparation) (*store.PreparedMail, error)
	PrepareEmailChangeWarning(mail.NoticePreparation) (*store.PreparedMail, error)
	PrepareEmailChangeVerification(mail.AccountTokenPreparation) (*store.PreparedMail, error)
	PrepareEmailVerifiedByAdministrator(mail.NoticePreparation) (*store.PreparedMail, error)
}

// accountTokenServiceDependencies are the collaborators of accountTokenService;
// newAccountTokenService validates them once and accountTokenService keeps them in deps.
type accountTokenServiceDependencies struct {
	Users        store.UserStore
	Passwords    store.PasswordCredentialStore
	Tokens       store.UserTokenStore
	AccessPolicy authenticationAccessPolicy
	Institutions store.InstitutionStore
	Mail         accountTokenMailPreparer
	Attempts     *authenticationAttemptAccounting
	Hasher       accountTokenPasswordHasher
	Audit        accountTokenAuditRecorder
	Effects      accountTokenEffects
	Diagnostics  recoveryDiagnostics
	Policy       AccountRecoveryPolicy
	PublicURL    string
	NewToken     func() string
	Now          func() time.Time
}

// accountTokenService owns purpose-specific verification and recovery tokens.
// Terminal transitions and their success audits remain named atomic store
// operations; only post-commit session effects happen outside persistence.
type accountTokenService struct {
	deps accountTokenServiceDependencies

	administration userEmailAdministration
}

func newAccountTokenService(deps accountTokenServiceDependencies) (*accountTokenService, error) {
	required := []struct {
		missing bool
		name    string
	}{
		{deps.Users == nil, "user store"},
		{deps.Passwords == nil, "password credential store"},
		{deps.Tokens == nil, "user token store"},
		{deps.AccessPolicy == nil, "authentication access policy"},
		{deps.Institutions == nil, "institution store"},
		{deps.Mail == nil, "account mail preparer"},
		{deps.Attempts == nil, "authentication attempt accounting"},
		{deps.Hasher == nil, "password hasher"},
		{deps.Audit.nodeID == "", "account recovery audit"},
		{deps.Effects == nil, "account recovery effects"},
		{deps.Diagnostics == nil, "account recovery diagnostics"},
		{deps.NewToken == nil, "account token generator"},
		{deps.Now == nil, "account token clock"},
	}
	for _, dependency := range required {
		if dependency.missing {
			return nil, errors.New(dependency.name + " is required")
		}
	}
	return &accountTokenService{deps: deps}, nil
}

type accountTokenAuditRecorder struct{ nodeID string }

func (r accountTokenAuditRecorder) Success(
	action model.Action,
	resource model.Resource,
	institutionID string,
	invocation invocation.Invocation,
	principal *model.Principal,
	authenticationMethod string,
) *model.AuditEvent {
	return recoveryAuditEvent(
		action, resource, institutionID, invocation.RequestMetadata(), r.nodeID,
		principal, authenticationMethod,
	)
}

var (
	_ accountTokenPasswordHasher = (*passwordHasher)(nil)
	_ accountTokenEffects        = (*realtimeService)(nil)
)
