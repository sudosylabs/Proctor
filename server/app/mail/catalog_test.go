// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package mail

import (
	"testing"

	"github.com/sudosylabs/proctor/server/model"
)

func TestMailCatalogCoversTheDurableTemplateCatalog(t *testing.T) {
	t.Parallel()
	keys := model.AllMailTemplateKeys()
	if len(mailCatalog) != len(keys) {
		t.Fatalf("mail catalog size = %d, want %d", len(mailCatalog), len(keys))
	}
	for _, key := range keys {
		definition, ok := definitionFor(key)
		kind, kindOK := key.OccurrenceKind()
		if !ok || !kindOK || definition.key != key || definition.kind != kind || definition.jobType == "" {
			t.Errorf("mail definition %q = %#v, present=%v kind=%q/%v", key, definition, ok, kind, kindOK)
		}
	}
}

func TestMailCatalogRestrictsCredentialAndActionDelivery(t *testing.T) {
	t.Parallel()
	for _, key := range model.AllMailTemplateKeys() {
		definition, _ := definitionFor(key)
		wantCredential := key == model.MailTemplateIdentityVerifyEmail || key == model.MailTemplateIdentityPasswordReset ||
			key == model.MailTemplateIdentityEmailChangeVerifyNew || key == model.MailTemplateAccessStudentClassInvitation ||
			key == model.MailTemplateAccessTeacherAcademicUnitInvitation || key == model.MailTemplateAccessAcademicUnitRoleInvitation ||
			key == model.MailTemplateAccessInstitutionRoleInvitation
		if (definition.jobType == model.JobTypeMailDeliverCredential) != wantCredential || definition.actionRequired != wantCredential ||
			(definition.defaultLifetime == 0) != wantCredential {
			t.Errorf("mail definition %q credential/action policy = %#v", key, definition)
		}
	}
}
