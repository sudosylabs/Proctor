// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package app

import (
	"testing"
	"time"

	"github.com/sudosylabs/proctor/server/model"
)

func TestPersonalAccessTokenSecurityNoticeIsTerminalWhenMailIsDisabled(t *testing.T) {
	t.Parallel()

	at := time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC)
	principal := mailTestPrincipal(at)
	preparer, err := newDirectMailPreparer(mailRendererFake{}, &mailSenderFake{enabled: false}, nil)
	if err != nil {
		t.Fatal(err)
	}
	prepared, err := preparer.PreparePersonalAccessTokenSecurityNotice(personalAccessTokenSecurityNoticePreparation{
		Recipient:   mailTestUser(principal, at),
		TemplateKey: model.MailTemplateIdentityPersonalAccessTokenCreated,
		Description: "automation", ExpiresAt: at.Add(time.Hour), ActionAt: at,
		ActionCount: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if prepared.Occurrence.Kind != model.MailOccurrenceSecurityNotice ||
		prepared.Delivery.State != model.MailDeliverySuppressed ||
		prepared.Delivery.PublicFailureCode != model.MailDeliveryDisabledCode ||
		len(prepared.Delivery.EncryptedPayload) != 0 ||
		prepared.Job.Status != model.JobStatusCanceled ||
		prepared.Job.Type != model.JobTypeMailDeliver {
		t.Fatalf("disabled-mail preparation = %#v %#v %#v", prepared.Occurrence, prepared.Delivery, prepared.Job)
	}
}
