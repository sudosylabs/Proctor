// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package i18n

import "testing"

func TestCatalogResolvesRecipientInstallationAndEnglishFallback(t *testing.T) {
	t.Parallel()

	key := Key("example.message")
	catalog, err := NewCatalog(map[string]map[Key]Copy{
		"en": {key: testCopy("English")},
		"fr": {key: testCopy("Français")},
		"de": {key: testCopy("Deutsch")},
	})
	if err != nil {
		t.Fatalf("NewCatalog: %v", err)
	}

	tests := []struct {
		name         string
		recipient    string
		installation string
		wantLocale   string
		wantSubject  string
	}{
		{name: "recipient exact", recipient: "fr", installation: "de", wantLocale: "fr", wantSubject: "Français"},
		{name: "recipient language", recipient: "fr-CA", installation: "de", wantLocale: "fr", wantSubject: "Français"},
		{name: "installation", recipient: "es-MX", installation: "de-DE", wantLocale: "de", wantSubject: "Deutsch"},
		{name: "english", recipient: "es", installation: "it", wantLocale: EnglishLocale, wantSubject: "English"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			resolved, err := catalog.Resolve(key, test.recipient, test.installation)
			if err != nil {
				t.Fatalf("Resolve: %v", err)
			}
			if resolved.Locale != test.wantLocale || resolved.Copy.Subject != test.wantSubject {
				t.Fatalf("Resolve() = locale %q subject %q, want %q %q", resolved.Locale, resolved.Copy.Subject, test.wantLocale, test.wantSubject)
			}
		})
	}
}

func TestCatalogRejectsIncompleteCopy(t *testing.T) {
	t.Parallel()

	_, err := NewCatalog(map[string]map[Key]Copy{
		EnglishLocale: {Key("example.message"): {Subject: "Subject"}},
	})
	if err == nil {
		t.Fatal("NewCatalog accepted incomplete copy")
	}
}

func TestDefaultCatalogCoversClosedTemplateCatalog(t *testing.T) {
	t.Parallel()

	catalog, err := DefaultCatalog()
	if err != nil {
		t.Fatalf("DefaultCatalog: %v", err)
	}
	if len(AllKeys()) != 43 {
		t.Fatalf("AllKeys length = %d, want 43", len(AllKeys()))
	}
	for _, key := range AllKeys() {
		if _, err := catalog.Resolve(key, "", ""); err != nil {
			t.Errorf("Resolve(%q): %v", key, err)
		}
	}
}

func testCopy(subject string) Copy {
	return Copy{
		Subject:     subject,
		Preheader:   "Preheader",
		Heading:     "Heading",
		Body:        "Body",
		ActionLabel: "Open Proctor",
		Footer:      "Footer",
	}
}
