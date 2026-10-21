// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package mail

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/server/model"
)

func TestMailCatalogCoversTheDurableTemplateCatalog(t *testing.T) {
	t.Parallel()
	keys := model.AllMailTemplateKeys()
	require.Len(t, mailCatalog, len(keys), "mail catalog size")
	for _, key := range keys {
		definition, ok := definitionFor(key)
		kind, kindOK := key.OccurrenceKind()
		if !assert.True(t, ok, "mail definition %q is missing", key) {
			continue
		}
		assert.True(t, kindOK, "mail definition %q has no occurrence kind", key)
		assert.Equal(t, key, definition.key, "mail definition %q", key)
		assert.Equal(t, kind, definition.kind, "mail definition %q", key)
		assert.NotEmpty(t, definition.jobType, "mail definition %q", key)
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
		assert.Equal(t, wantCredential, definition.jobType == model.JobTypeMailDeliverCredential, "mail definition %q credential/action policy = %#v", key, definition)
		assert.Equal(t, wantCredential, definition.actionRequired, "mail definition %q credential/action policy = %#v", key, definition)
		assert.Equal(t, wantCredential, definition.defaultLifetime == 0, "mail definition %q credential/action policy = %#v", key, definition)
	}
}
