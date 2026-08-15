// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package attempt

import (
	"strings"
	"testing"
)

func TestCandidateMarkdownSanitizerSecurityTable(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		input     string
		contains  []string
		forbidden []string
	}{
		{name: "entity and escaped unsafe schemes", input: `[entity](javascript&#58;alert(1)) [escaped](java\script:alert(1))`,
			contains: []string{"entity", "escaped"}, forbidden: []string{"javascript", `java\script`, "alert(1)"}},
		{name: "reference shortcut and inline images", input: "![inline](https://evil.test/i) ![reference][remote] ![shortcut]\n[remote]: https://evil.test/r",
			contains: []string{"inline", "reference", "shortcut"}, forbidden: []string{"![", "evil.test", "[remote]:"}},
		{name: "mixed case raw active html", input: `<ScRiPt src=https://evil.test/x>bad()</sCrIpT><IfRaMe src=https://evil.test/y>frame</iFrAmE><b>safe text</b>`,
			contains: []string{"safe text"}, forbidden: []string{"script", "iframe", "evil.test", "bad()", "frame", "<b>"}},
		{name: "code spans and fences are inert", input: "`<script>x</script> ![remote](https://evil.test/i)`\n```html\n<img src=https://evil.test/p>\n```",
			contains: []string{"`<script>x</script> ![remote](https://evil.test/i)`", "```html\n<img src=https://evil.test/p>\n```"}},
		{name: "safe links", input: `[relative](guide/start) [https](https://example.test/help) [mail](mailto:help@example.test)`,
			contains: []string{"[relative](guide/start)", "[https](https://example.test/help)", "[mail](mailto:help@example.test)"}},
		{name: "protocol relative links and remote images", input: `[protocol](//evil.test/path) ![remote](//evil.test/image)`,
			contains: []string{"protocol", "remote"}, forbidden: []string{"//evil.test", "!["}},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			got := sanitizeCandidateMarkdown(test.input)
			for _, expected := range test.contains {
				if !strings.Contains(got, expected) {
					t.Fatalf("safe Markdown %q was removed: %q", expected, got)
				}
			}
			for _, forbidden := range test.forbidden {
				if strings.Contains(strings.ToLower(got), strings.ToLower(forbidden)) {
					t.Fatalf("unsafe construct %q survived: %q", forbidden, got)
				}
			}
		})
	}
}

func TestCandidateMarkdownSanitizerRetainsInertFormattingAndCode(t *testing.T) {
	t.Parallel()
	input := "# Heading\n\nUse **bold**, _emphasis_, and `code <script>x</script> ![sample](https://example.test/x)`.\n\n" +
		"```html\n<img src=https://example.test/pixel>\n```\n\n[Safe](https://example.test/guide)"
	got := sanitizeCandidateMarkdown(input)
	for _, safe := range []string{"# Heading", "**bold**", "_emphasis_", "`code <script>x</script> ![sample](https://example.test/x)`",
		"```html\n<img src=https://example.test/pixel>\n```", "[Safe](https://example.test/guide)"} {
		if !strings.Contains(got, safe) {
			t.Fatalf("safe Markdown %q was removed: %q", safe, got)
		}
	}
}

func TestCandidateMarkdownSanitizerRemovesActiveAndRemoteLoadingConstructs(t *testing.T) {
	t.Parallel()
	input := `<ScRiPt src="https://evil.test/a.js">alert(1)</ScRiPt>
<iframe src="https://evil.test/frame"></iframe>
<img src="https://evil.test/pixel">
[script](JaVaScRiPt:alert(1)) [data](data:text/html,bad) [file](file:///secret)
![inline](https://evil.test/image) ![reference][remote]
[remote]: https://evil.test/reference
[relative](guide/start) [mail](mailto:help@example.test)`
	got := sanitizeCandidateMarkdown(input)
	for _, forbidden := range []string{"<script", "a.js", "alert(1)", "<iframe", "frame", "<img", "pixel", "javascript:",
		"data:text", "file:", "![", "evil.test", "[remote]:"} {
		if strings.Contains(strings.ToLower(got), forbidden) {
			t.Fatalf("unsafe construct %q survived: %q", forbidden, got)
		}
	}
	for _, safe := range []string{"script", "data", "file", "inline", "reference", "[relative](guide/start)", "[mail](mailto:help@example.test)"} {
		if !strings.Contains(got, safe) {
			t.Fatalf("safe text/Markdown %q was removed: %q", safe, got)
		}
	}
}
