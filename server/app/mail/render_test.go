// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package mail

import (
	"os"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/server/localization"
	"github.com/sudosylabs/proctor/server/model"
)

func testRenderer() (*templateRenderer, error) {
	localizer, err := localization.New(os.DirFS("../../i18n"), localization.EnglishLocale)
	if err != nil {
		return nil, err
	}
	return newRenderer(os.DirFS("../../templates"), localizer, true)
}

func TestRendererEscapesBoundedPersonalAccessTokenDetailsWithoutScopes(t *testing.T) {
	t.Parallel()

	renderer, err := testRenderer()
	require.NoError(t, err)
	message, err := renderer.render(renderRequest{
		Key: model.MailTemplateIdentityPersonalAccessTokenCreated,
		PersonalAccessToken: &PersonalAccessTokenDetails{
			Description:        `<script>automation & reports</script>`,
			ExpiresAt:          time.Date(2026, 9, 20, 9, 30, 0, 0, time.UTC),
			ActionAt:           time.Date(2026, 8, 20, 8, 15, 0, 0, time.UTC),
			ActionCount:        2,
			AcademicUnitScoped: true,
		},
	})
	require.NoError(t, err)
	for _, want := range []string{
		"&lt;script&gt;automation &amp; reports&lt;/script&gt;",
		"2026-09-20T09:30:00Z",
		"2026-08-20T08:15:00Z",
		"Academic Unit constrained",
		">2<",
	} {
		assert.Contains(t, message.HTML, want)
	}
	require.Contains(t, message.Text, `<script>automation & reports</script>`, "text alternative changed the plain token description")
	for _, forbidden := range []string{"class.view", "role.manage", "raw-secret-value", "token_hash"} {
		require.NotContains(t, message.HTML, forbidden)
		require.NotContains(t, message.Text, forbidden)
	}
}

func TestRendererEscapesBoundedExamManagerDetails(t *testing.T) {
	t.Parallel()

	renderer, err := testRenderer()
	require.NoError(t, err)
	message, err := renderer.render(renderRequest{
		Key: model.MailTemplateExamOwnershipTransferredFromYou,
		ExamManager: &ExamManagerDetails{
			Title:        `<script>Algorithms & data</script>`,
			Relationship: "manager",
			ActionAt:     time.Date(2026, 8, 20, 8, 15, 0, 0, time.UTC),
		},
	})
	require.NoError(t, err)
	for _, want := range []string{
		"&lt;script&gt;Algorithms &amp; data&lt;/script&gt;",
		"Exam Manager",
		"2026-08-20T08:15:00Z",
	} {
		assert.Contains(t, message.HTML, want)
	}
	require.Contains(t, message.Text, `<script>Algorithms & data</script>`, "text alternative changed the plain Exam title")
	for _, forbidden := range []string{"actor-user", "private-reason", "role.manage"} {
		require.NotContains(t, message.HTML, forbidden)
		require.NotContains(t, message.Text, forbidden)
	}
}

func TestRendererIncludesTimezoneExplicitSafeSittingFacts(t *testing.T) {
	t.Parallel()
	renderer, err := testRenderer()
	require.NoError(t, err)
	message, err := renderer.render(renderRequest{Key: model.MailTemplateExamSittingRescheduled, SittingSchedule: &SittingScheduleDetails{
		ExamTitle: "Algorithms & structures", ClassDisplayName: "CS 2A",
		StartsAt: time.Date(2026, 9, 1, 8, 30, 0, 0, time.FixedZone("node", 7200)),
		EndsAt:   time.Date(2026, 9, 1, 10, 30, 0, 0, time.FixedZone("node", 7200)),
	}})
	require.NoError(t, err)
	for _, want := range []string{"Algorithms &amp; structures", "CS 2A", "2026-09-01T06:30:00Z", "2026-09-01T08:30:00Z", "UTC"} {
		assert.Contains(t, message.HTML, want)
	}
	for _, forbidden := range []string{"instructions", "resource", "answer", "candidate list"} {
		require.NotContains(t, strings.ToLower(message.HTML), forbidden)
		require.NotContains(t, strings.ToLower(message.Text), forbidden)
	}
}

func TestRendererEscapesCompleteClassTransitionFacts(t *testing.T) {
	t.Parallel()
	renderer, err := testRenderer()
	require.NoError(t, err)
	message, err := renderer.render(renderRequest{Key: model.MailTemplateAcademicClassTransferred, ClassTransition: &ClassTransitionDetails{
		PreviousClassDisplayName: `Old <Class> & A`, ClassDisplayName: `New <Class> & B`,
		StartsAt: time.Date(2026, 9, 1, 8, 30, 0, 0, time.FixedZone("node", 7200)),
		EndsAt:   time.Date(2027, 6, 30, 16, 0, 0, 0, time.UTC),
	}})
	require.NoError(t, err)
	for _, want := range []string{"Old &lt;Class&gt; &amp; A", "New &lt;Class&gt; &amp; B", "2026-09-01T06:30:00Z", "2027-06-30T16:00:00Z", "UTC"} {
		assert.Contains(t, message.HTML, want)
	}
	require.Contains(t, message.Text, `Old <Class> & A`, "text alternative changed Class display names")
	require.Contains(t, message.Text, `New <Class> & B`, "text alternative changed Class display names")
	for _, forbidden := range []string{"student list", "exam instructions", "actor-user", "private reason"} {
		require.NotContains(t, strings.ToLower(message.HTML), forbidden)
		require.NotContains(t, strings.ToLower(message.Text), forbidden)
	}
}

func TestRendererIncludesOnlySafeSubmissionReceiptFacts(t *testing.T) {
	t.Parallel()
	renderer, err := testRenderer()
	require.NoError(t, err)
	sittingID, submissionID := model.NewExamSittingID(), model.NewSubmissionID()
	for _, key := range []model.MailTemplateKey{model.MailTemplateExamSubmissionReceived,
		model.MailTemplateExamSubmissionManagerEnded, model.MailTemplateExamSubmissionAutomaticallySealed} {
		message, renderErr := renderer.render(renderRequest{Key: key, SubmissionReceipt: &SubmissionReceiptDetails{
			ExamTitle: "Algorithms <Final> & proofs", SittingID: sittingID,
			SubmissionID: submissionID, SealedAt: time.Date(2026, 8, 21, 9, 30, 0, 0, time.FixedZone("node", 7200)),
		}})
		require.NoError(t, renderErr, "Render(%q)", key)
		for _, want := range []string{"Algorithms &lt;Final&gt; &amp; proofs", sittingID.String(), submissionID.String(), "2026-08-21T07:30:00Z", "UTC"} {
			assert.Contains(t, message.HTML, want, "Render(%q) HTML does not contain", key)
		}
		for _, forbidden := range []string{"manifest", "workspace", "answer", "integrity", "path/inside"} {
			require.NotContains(t, strings.ToLower(message.HTML), forbidden, "Render(%q) leaked forbidden content", key)
			require.NotContains(t, strings.ToLower(message.Text), forbidden, "Render(%q) leaked forbidden content", key)
		}
	}
}

func TestRendererIncludesOnlySafeReleasedResultFacts(t *testing.T) {
	t.Parallel()
	renderer, err := testRenderer()
	require.NoError(t, err)
	message, err := renderer.render(renderRequest{Key: model.MailTemplateExamResultReleased, ResultRelease: &ResultReleaseDetails{
		ExamTitle:  "Algorithms <Final> & proofs",
		ReleasedAt: time.Date(2026, 8, 21, 9, 30, 0, 0, time.FixedZone("node", 7200)),
	}})
	require.NoError(t, err)
	for _, want := range []string{"Algorithms &lt;Final&gt; &amp; proofs", "2026-08-21T07:30:00Z", "UTC"} {
		assert.Contains(t, message.HTML, want)
	}
	for _, forbidden := range []string{"score", "outcome", "remark", "evidence", "submission", "workspace", "rationale"} {
		require.NotContains(t, strings.ToLower(message.HTML), forbidden)
		require.NotContains(t, strings.ToLower(message.Text), forbidden)
	}
}

func TestRendererContextuallyEscapesLocalizedCopyAndActionURL(t *testing.T) {
	t.Parallel()

	key := model.MailTemplateIdentityVerifyEmail
	localizer, err := localization.New(fstest.MapFS{"en.json": {Data: []byte(`[
  {"id":"mail.identity.verify_email.action_label","translation":"Verify >"},
  {"id":"mail.identity.verify_email.body","translation":"A & B"},
  {"id":"mail.identity.verify_email.footer","translation":"No reply <needed>"},
  {"id":"mail.identity.verify_email.heading","translation":"<script>alert(1)</script>"},
  {"id":"mail.identity.verify_email.preheader","translation":"Use this & only this link"},
  {"id":"mail.identity.verify_email.subject","translation":"Verify <account>"}
]`)}}, localization.EnglishLocale)
	require.NoError(t, err)
	renderer, err := newRenderer(os.DirFS("../../templates"), localizer, false)
	require.NoError(t, err)

	message, err := renderer.render(renderRequest{
		Key:       key,
		ActionURL: "https://proctor.example.test/account/verify-email?one=1&two=2",
	})
	require.NoError(t, err)
	require.NotContains(t, message.HTML, "<script>alert(1)</script>", "HTML contains unescaped localized markup")
	for _, want := range []string{"&lt;script&gt;alert(1)&lt;/script&gt;", "one=1&amp;two=2"} {
		assert.Contains(t, message.HTML, want)
	}
	require.Contains(t, message.Text, "<script>alert(1)</script>", "text alternative changed plain localized copy")
}

func TestRendererRejectsUnsafeActionURL(t *testing.T) {
	t.Parallel()

	renderer, err := testRenderer()
	require.NoError(t, err)
	_, err = renderer.render(renderRequest{
		Key:       model.MailTemplateIdentityVerifyEmail,
		ActionURL: "javascript:alert(1)",
	})
	require.Error(t, err, "Render accepted a non-HTTPS action URL")
}

func TestRendererLoopbackHTTPRequiresExplicitDevelopmentPolicy(t *testing.T) {
	t.Parallel()

	for _, raw := range []string{
		"http://localhost:8065/join#token=representative",
		"http://127.0.0.1:8065/join#token=representative",
		"http://[::1]:8065/join#token=representative",
	} {
		assert.Error(t, validateActionURL(raw, false), "validateActionURL(%q) accepted loopback HTTP without development policy", raw)
		assert.NoError(t, validateActionURL(raw, true), "validateActionURL(%q) rejected explicit loopback development URL", raw)
	}
	for _, raw := range []string{
		"http://192.0.2.1:8065/join#token=representative",
		"http://user@localhost:8065/join#token=representative",
	} {
		assert.Error(t, validateActionURL(raw, true), "validateActionURL(%q) accepted unsafe HTTP development URL", raw)
	}
}

func TestRendererParsesAndRendersEveryProductionTemplate(t *testing.T) {
	t.Parallel()

	renderer, err := testRenderer()
	require.NoError(t, err)
	for _, key := range model.AllMailTemplateKeys() {
		request := renderRequest{
			Key:             key,
			RecipientLocale: "zz-ZZ",
			ActionURL:       "https://proctor.example.test/action#token=representative",
		}
		if isPersonalAccessTokenTemplate(key) {
			request.PersonalAccessToken = &PersonalAccessTokenDetails{
				Description: "Representative automation", ExpiresAt: time.Date(2026, 9, 20, 9, 30, 0, 0, time.UTC),
				ActionAt: time.Date(2026, 8, 20, 8, 15, 0, 0, time.UTC), ActionCount: 2,
			}
		}
		if isExamManagerTemplate(key) {
			request.ExamManager = &ExamManagerDetails{
				Title: "Representative exam", Relationship: "manager",
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
			if key != model.MailTemplateAcademicClassTransferred {
				request.ClassTransition.PreviousClassDisplayName = ""
			}
		}
		if isSubmissionReceiptTemplate(key) {
			request.SubmissionReceipt = &SubmissionReceiptDetails{ExamTitle: "Representative exam",
				SittingID: model.NewExamSittingID(), SubmissionID: model.NewSubmissionID(),
				SealedAt: time.Date(2026, 8, 21, 9, 30, 0, 0, time.UTC)}
		}
		if key == model.MailTemplateExamResultReleased {
			request.ResultRelease = &ResultReleaseDetails{ExamTitle: "Representative exam",
				ReleasedAt: time.Date(2026, 8, 21, 10, 30, 0, 0, time.UTC)}
		}
		message, err := renderer.render(request)
		if !assert.NoError(t, err, "Render(%q)", key) {
			continue
		}
		assert.NotEmpty(t, message.Subject, "Render(%q) subject", key)
		assert.NotEmpty(t, message.Text, "Render(%q) text", key)
		assert.NotEmpty(t, message.HTML, "Render(%q) HTML", key)
		source, readErr := os.ReadFile("../../templates/" + string(key) + ".html")
		require.NoError(t, readErr)
		assert.Equal(t, strings.Count(string(source), "<!--[if"), strings.Count(message.HTML, "<!--[if"), "Render(%q) dropped Outlook conditional comments", key)
		assert.Equal(t, strings.Count(string(source), "<![endif]-->"), strings.Count(message.HTML, "<![endif]-->"), "Render(%q) dropped Outlook conditional comments", key)
		assert.Contains(t, message.HTML, "<o:OfficeDocumentSettings>", "Render(%q) dropped the Outlook settings block", key)
		for _, expected := range []string{`src="cid:` + testLockupCID + `"`, `alt="Proctor"`, `width="200" height="39"`} {
			assert.Contains(t, message.HTML, expected, "Render(%q) is missing its accessible inline logo", key)
		}
	}
}
