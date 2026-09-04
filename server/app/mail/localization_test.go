// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package mail

import (
	"os"
	"testing"

	"github.com/sudosylabs/proctor/server/localization"
)

func TestLocalizationDefinitionsMatchCatalog(t *testing.T) {
	t.Parallel()
	localizer, err := localization.New(os.DirFS("../../i18n"), localization.EnglishLocale)
	if err != nil {
		t.Fatal(err)
	}
	// Restrict this assertion to mail-owned definitions. The full cross-consumer
	// exact-set check lives in ptool.
	definitions := LocalizationDefinitions()
	if len(definitions) != 349 {
		t.Fatalf("mail localization definitions = %d, want 349", len(definitions))
	}
	for _, definition := range definitions {
		if _, err := localizer.Resolve(localization.EnglishLocale, definition.ID, nil); err != nil {
			t.Fatalf("resolve %q: %v", definition.ID, err)
		}
	}
}
