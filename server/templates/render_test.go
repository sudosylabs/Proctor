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

func TestRendererEscapesBoundedExamManagerDetails(t *testing.T) {
	t.Parallel()

	renderer, err := DefaultRenderer()
	if err != nil {
		t.Fatalf("DefaultRenderer: %v", err)
	}
	message, err := renderer.Render(Request{
		Key: i18n.ExamOwnershipTransferredFromYou,
		ExamManager: &ExamManagerDetails{
			Title:        `<script>Algorithms & data</script>`,
			Relationship: ExamManagerRelationshipManager,
			ActionAt:     time.Date(2026, 8, 20, 8, 15, 0, 0, time.UTC),
		},
	})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	for _, want := range []string{
		"&lt;script&gt;Algorithms &amp; data&lt;/script&gt;",
		"Exam Manager",
		"2026-08-20T08:15:00Z",
	} {
		if !strings.Contains(message.HTML, want) {
			t.Errorf("HTML does not contain %q", want)
		}
	}
	if !strings.Contains(message.Text, `<script>Algorithms & data</script>`) {
		t.Fatal("text alternative changed the plain Exam title")
	}
	for _, forbidden := range []string{"actor-user", "private-reason", "role.manage"} {
		if strings.Contains(message.HTML, forbidden) || strings.Contains(message.Text, forbidden) {
			t.Fatalf("rendered Exam Manager notice exposes forbidden value %q", forbidden)
		}
	}
}

func TestRendererIncludesTimezoneExplicitSafeSittingFacts(t *testing.T) {
	t.Parallel()
	renderer, err := DefaultRenderer()
	if err != nil {
		t.Fatal(err)
	}
	message, err := renderer.Render(Request{Key: i18n.ExamSittingRescheduled, SittingSchedule: &SittingScheduleDetails{
		ExamTitle: "Algorithms & structures", ClassDisplayName: "CS 2A",
		StartsAt: time.Date(2026, 9, 1, 8, 30, 0, 0, time.FixedZone("node", 7200)),
		EndsAt:   time.Date(2026, 9, 1, 10, 30, 0, 0, time.FixedZone("node", 7200)),
	}})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Algorithms &amp; structures", "CS 2A", "2026-09-01T06:30:00Z", "2026-09-01T08:30:00Z", "UTC"} {
		if !strings.Contains(message.HTML, want) {
			t.Errorf("HTML does not contain %q", want)
		}
	}
	for _, forbidden := range []string{"instructions", "resource", "answer", "candidate list"} {
		if strings.Contains(strings.ToLower(message.HTML), forbidden) || strings.Contains(strings.ToLower(message.Text), forbidden) {
			t.Fatalf("Sitting notice leaked forbidden content %q", forbidden)
		}
	}
}

func TestRendererEscapesCompleteClassTransitionFacts(t *testing.T) {
	t.Parallel()
	renderer, err := DefaultRenderer()
	if err != nil {
		t.Fatal(err)
	}
	message, err := renderer.Render(Request{Key: i18n.AcademicClassTransferred, ClassTransition: &ClassTransitionDetails{
		PreviousClassDisplayName: `Old <Class> & A`, ClassDisplayName: `New <Class> & B`,
		StartsAt: time.Date(2026, 9, 1, 8, 30, 0, 0, time.FixedZone("node", 7200)),
		EndsAt:   time.Date(2027, 6, 30, 16, 0, 0, 0, time.UTC),
	}})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Old &lt;Class&gt; &amp; A", "New &lt;Class&gt; &amp; B", "2026-09-01T06:30:00Z", "2027-06-30T16:00:00Z", "UTC"} {
		if !strings.Contains(message.HTML, want) {
			t.Errorf("HTML does not contain %q", want)
		}
	}
	if !strings.Contains(message.Text, `Old <Class> & A`) || !strings.Contains(message.Text, `New <Class> & B`) {
		t.Fatal("text alternative changed Class display names")
	}
	for _, forbidden := range []string{"student list", "exam instructions", "actor-user", "private reason"} {
		if strings.Contains(strings.ToLower(message.HTML), forbidden) || strings.Contains(strings.ToLower(message.Text), forbidden) {
			t.Fatalf("Class transition notice leaked forbidden content %q", forbidden)
		}
	}
}

func TestRendererIncludesOnlySafeSubmissionReceiptFacts(t *testing.T) {
	t.Parallel()
	renderer, err := DefaultRenderer()
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []i18n.Key{i18n.ExamSubmissionReceived, i18n.ExamSubmissionAutomaticallySealed} {
		message, renderErr := renderer.Render(Request{Key: key, SubmissionReceipt: &SubmissionReceiptDetails{
			ExamTitle: "Algorithms <Final> & proofs", SittingID: "sitting-safe-id",
			SubmissionID: "submission-safe-id", SealedAt: time.Date(2026, 8, 21, 9, 30, 0, 0, time.FixedZone("node", 7200)),
		}})
		if renderErr != nil {
			t.Fatalf("Render(%q): %v", key, renderErr)
		}
		for _, want := range []string{"Algorithms &lt;Final&gt; &amp; proofs", "sitting-safe-id", "submission-safe-id", "2026-08-21T07:30:00Z", "UTC"} {
			if !strings.Contains(message.HTML, want) {
				t.Errorf("Render(%q) HTML does not contain %q", key, want)
			}
		}
		for _, forbidden := range []string{"manifest", "workspace", "answer", "integrity", "path/inside"} {
			if strings.Contains(strings.ToLower(message.HTML), forbidden) || strings.Contains(strings.ToLower(message.Text), forbidden) {
				t.Fatalf("Render(%q) leaked forbidden content %q", key, forbidden)
			}
		}
	}
}

func TestRendererIncludesOnlySafeReleasedResultFacts(t *testing.T) {
	t.Parallel()
	renderer, err := DefaultRenderer()
	if err != nil {
		t.Fatal(err)
	}
	message, err := renderer.Render(Request{Key: i18n.ExamResultReleased, ResultRelease: &ResultReleaseDetails{
		ExamTitle:  "Algorithms <Final> & proofs",
		ReleasedAt: time.Date(2026, 8, 21, 9, 30, 0, 0, time.FixedZone("node", 7200)),
	}})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Algorithms &lt;Final&gt; &amp; proofs", "2026-08-21T07:30:00Z", "UTC"} {
		if !strings.Contains(message.HTML, want) {
			t.Errorf("HTML does not contain %q", want)
		}
	}
	for _, forbidden := range []string{"score", "outcome", "remark", "evidence", "submission", "workspace", "rationale"} {
		if strings.Contains(strings.ToLower(message.HTML), forbidden) || strings.Contains(strings.ToLower(message.Text), forbidden) {
			t.Fatalf("released-result mail leaked forbidden content %q", forbidden)
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
		if isExamManagerTemplate(key) {
			request.ExamManager = &ExamManagerDetails{
				Title: "Representative exam", Relationship: ExamManagerRelationshipManager,
				ActionAt: time.Date(2026, 8, 20, 8, 15, 0, 0, time.UTC),
			}
		}
		if isSittingScheduleTemplate(key) {
			request.SittingSchedule = &SittingScheduleDetails{ExamTitle: "Representative exam", ClassDisplayName: "Class A",
				StartsAt: time.Date(2026, 9, 1, 8, 30, 0, 0, time.UTC), EndsAt: time.Date(2026, 9, 1, 10, 30, 0, 0, time.UTC)}
		}
		if isClassTransitionTemplate(key) {
			request.ClassTransition = &ClassTransitionDetails{PreviousClassDisplayName: "Class A", ClassDisplayName: "Class B",
				StartsAt: time.Date(2026, 9, 1, 8, 30, 0, 0, time.UTC), EndsAt: time.Date(2027, 6, 30, 16, 0, 0, 0, time.UTC)}
			if key != i18n.AcademicClassTransferred {
				request.ClassTransition.PreviousClassDisplayName = ""
			}
		}
		if isSubmissionReceiptTemplate(key) {
			request.SubmissionReceipt = &SubmissionReceiptDetails{ExamTitle: "Representative exam",
				SittingID: "sitting-safe-id", SubmissionID: "submission-safe-id",
				SealedAt: time.Date(2026, 8, 21, 9, 30, 0, 0, time.UTC)}
		}
		if key == i18n.ExamResultReleased {
			request.ResultRelease = &ResultReleaseDetails{ExamTitle: "Representative exam",
				ReleasedAt: time.Date(2026, 8, 21, 10, 30, 0, 0, time.UTC)}
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
