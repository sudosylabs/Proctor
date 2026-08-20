// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package templates

import (
	"strings"
	"testing"
	"time"

	"github.com/sudosylabs/proctor/server/i18n"
)

func TestRendererEscapesBoundedPersonalAccessTokenDetailsWithoutScopes(t *testing.T) {
	t.Parallel()

	renderer, err := DefaultRenderer()
	if err != nil {
		t.Fatalf("DefaultRenderer: %v", err)
	}
	message, err := renderer.Render(Request{
		Key: i18n.IdentityPersonalAccessTokenCreated,
		PersonalAccessToken: &PersonalAccessTokenDetails{
			Description:        `<script>automation & reports</script>`,
			ExpiresAt:          time.Date(2026, 9, 20, 9, 30, 0, 0, time.UTC),
			ActionAt:           time.Date(2026, 8, 20, 8, 15, 0, 0, time.UTC),
			ActionCount:        2,
			AcademicUnitScoped: true,
		},
	})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	for _, want := range []string{
		"&lt;script&gt;automation &amp; reports&lt;/script&gt;",
		"2026-09-20T09:30:00Z",
		"2026-08-20T08:15:00Z",
		"Academic Unit constrained",
		">2<",
	} {
		if !strings.Contains(message.HTML, want) {
			t.Errorf("HTML does not contain %q", want)
		}
	}
	if !strings.Contains(message.Text, `<script>automation & reports</script>`) {
		t.Fatal("text alternative changed the plain token description")
	}
	for _, forbidden := range []string{"class.view", "role.manage", "raw-secret-value", "token_hash"} {
		if strings.Contains(message.HTML, forbidden) || strings.Contains(message.Text, forbidden) {
			t.Fatalf("rendered PAT notice exposes forbidden value %q", forbidden)
		}
	}
}

func TestRendererContextuallyEscapesLocalizedCopyAndActionURL(t *testing.T) {
	t.Parallel()

	key := i18n.IdentityVerifyEmail
	catalog, err := i18n.NewCatalog(map[string]map[i18n.Key]i18n.Copy{
		i18n.EnglishLocale: {
			key: {
				Subject:     "Verify <account>",
				Preheader:   "Use this & only this link",
				Heading:     "<script>alert(1)</script>",
				Body:        "A & B",
				ActionLabel: "Verify >",
				Footer:      "No reply <needed>",
			},
		},
	})
	if err != nil {
		t.Fatalf("NewCatalog: %v", err)
	}
	renderer, err := NewRenderer(catalog)
	if err != nil {
		t.Fatalf("NewRenderer: %v", err)
	}

	message, err := renderer.Render(Request{
		Key:       key,
		ActionURL: "https://proctor.example.test/account/verify-email?one=1&two=2",
	})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if strings.Contains(message.HTML, "<script>alert(1)</script>") {
		t.Fatal("HTML contains unescaped localized markup")
	}
	for _, want := range []string{"&lt;script&gt;alert(1)&lt;/script&gt;", "one=1&amp;two=2"} {
		if !strings.Contains(message.HTML, want) {
			t.Errorf("HTML does not contain %q", want)
		}
	}
	if !strings.Contains(message.Text, "<script>alert(1)</script>") {
		t.Fatal("text alternative changed plain localized copy")
	}
}

func TestRendererRejectsUnsafeActionURL(t *testing.T) {
	t.Parallel()

	renderer, err := DefaultRenderer()
	if err != nil {
		t.Fatalf("DefaultRenderer: %v", err)
	}
	_, err = renderer.Render(Request{
		Key:       i18n.IdentityVerifyEmail,
		ActionURL: "javascript:alert(1)",
	})
	if err == nil {
		t.Fatal("Render accepted a non-HTTPS action URL")
	}
}

func TestRendererParsesAndRendersEveryProductionTemplate(t *testing.T) {
	t.Parallel()

	renderer, err := DefaultRenderer()
	if err != nil {
		t.Fatalf("DefaultRenderer: %v", err)
	}
	for _, key := range i18n.AllKeys() {
		request := Request{
			Key:                key,
			RecipientLocale:    "zz-ZZ",
			InstallationLocale: "en",
			ActionURL:          "https://proctor.example.test/action#token=representative",
		}
		if isPersonalAccessTokenTemplate(key) {
			request.PersonalAccessToken = &PersonalAccessTokenDetails{
				Description: "Representative automation", ExpiresAt: time.Date(2026, 9, 20, 9, 30, 0, 0, time.UTC),
				ActionAt: time.Date(2026, 8, 20, 8, 15, 0, 0, time.UTC), ActionCount: 2,
			}
		}
		message, err := renderer.Render(request)
		if err != nil {
			t.Errorf("Render(%q): %v", key, err)
			continue
		}
		if message.Subject == "" || message.Text == "" || message.HTML == "" {
			t.Errorf("Render(%q) returned an empty message part", key)
		}
	}
}
