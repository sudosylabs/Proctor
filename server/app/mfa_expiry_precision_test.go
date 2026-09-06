// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package app

import (
	"context"
	"testing"
	"time"

	"github.com/sudosylabs/proctor/server/model"
)

func TestMFAActivationUsesNativeExclusiveDeadline(t *testing.T) {
	t.Parallel()

	deadline := time.Date(2026, 9, 5, 12, 0, 0, 123700000, time.UTC)
	for _, test := range []struct {
		name   string
		offset time.Duration
		valid  bool
	}{
		{name: "before", offset: -time.Microsecond, valid: true},
		{name: "nanosecond normalization", offset: -time.Nanosecond, valid: true},
		{name: "at deadline"},
		{name: "after", offset: time.Microsecond},
	} {
		t.Run(test.name, func(t *testing.T) {
			now := deadline.Add(test.offset).In(time.FixedZone("test", 2*60*60))
			principal := mfaTestPrincipal(now, model.AuthenticationMultiFactor)
			persistence := &mfaApplicationStoreFake{
				session: &model.Session{ID: principal.SessionID, UserID: principal.UserID},
			}
			service := newTestMFAApplicationService(t, persistence, &mfaApplicationAuditFake{}, &mfaApplicationEffectsFake{}, now)
			mailer := &mfaSecurityNoticeMailPreparerFake{}
			service.mail = mailer
			const secret = "JBSWY3DPEHPK3PXP" // #nosec G101 -- Public synthetic TOTP seed for expiry-boundary tests.
			sealed, err := service.mechanics.sealTOTPSecret(principal.UserID.String(), secret)
			if err != nil {
				t.Fatal(err)
			}
			persistence.credential = &model.MFACredential{
				ID: model.NewMFACredentialID(), UserID: principal.UserID,
				State: model.MFAStatePending, EncryptedSecret: sealed.encoded,
				EncryptionKeyID: sealed.keyID, CreatedAt: deadline.Add(-time.Minute),
				UpdatedAt: deadline.Add(-time.Minute), PendingExpiresAt: model.OptionalTimeFrom(deadline),
			}
			code, err := computeTOTP(secret, now.Unix()/30)
			if err != nil {
				t.Fatal(err)
			}
			_, err = service.Activate(context.Background(), NewInvocation(principal, model.RequestMetadata{}), ActivateMFACommand{Code: code})
			if !test.valid {
				if !Is(err, "authentication.mfa.invalid_code") || persistence.activation != nil || len(mailer.requests) != 0 {
					t.Fatalf("expired activation: error=%v mutation=%v notices=%d", err, persistence.activation != nil, len(mailer.requests))
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if persistence.activation == nil || persistence.activation.At != model.TimeUTC(now) {
				t.Fatalf("activation did not preserve native UTC decision instant")
			}
			if len(mailer.requests) != 1 || !mailer.requests[0].At.Equal(model.TimeFromMillis(now.UnixMilli())) {
				t.Fatal("activation changed the frozen notice timestamp contract")
			}
		})
	}
}
