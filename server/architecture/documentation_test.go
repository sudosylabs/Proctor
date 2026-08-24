// Copyright (c) 2026 Sudo Systems Labs Ltd
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.

package architecture_test

import (
	"bufio"
	"bytes"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
	"unicode"
)

var (
	inlineMarkdownLink = regexp.MustCompile(`!?\[[^\]]*\]\(([^)\n]+)\)`)
	referenceLink      = regexp.MustCompile(`(?m)^\s*\[[^\]]+\]:\s*(\S+)`)
	machinePath        = regexp.MustCompile("(?m)(/Users/[^\\s`]+|/home/[^\\s`]+|[A-Za-z]:\\\\Users\\\\[^\\s`]+)")
)

func TestRepositoryDocumentationIsPortable(t *testing.T) {
	repositoryRoot, ok := documentationRepositoryRoot(t)
	if !ok {
		t.Skip("repository-level documentation is unavailable outside the monorepo checkout")
	}

	candidates := documentationCandidates(t, repositoryRoot)
	var markdownFiles []string
	for name := range candidates {
		extension := strings.ToLower(filepath.Ext(name))
		if extension == ".md" || extension == ".mdx" {
			markdownFiles = append(markdownFiles, name)
		}
	}
	sort.Strings(markdownFiles)

	for _, name := range markdownFiles {
		validateMarkdownFile(t, repositoryRoot, name, candidates)
	}
}

func documentationRepositoryRoot(t *testing.T) (string, bool) {
	t.Helper()

	command := exec.Command("git", "rev-parse", "--show-toplevel")
	output, err := command.Output()
	if err != nil {
		return "", false
	}

	root := strings.TrimSpace(string(output))
	if _, err := os.Stat(filepath.Join(root, "go.work")); err != nil {
		return "", false
	}
	return root, true
}

func documentationCandidates(t *testing.T, root string) map[string]struct{} {
	t.Helper()

	command := exec.Command(
		"git", "-C", root, "ls-files", "--cached", "--others",
		"--exclude-standard", "-z",
	)
	output, err := command.Output()
	if err != nil {
		t.Fatalf("list repository documentation candidates: %v", err)
	}

	candidates := make(map[string]struct{})
	for _, raw := range bytes.Split(output, []byte{0}) {
		if len(raw) == 0 {
			continue
		}
		name := filepath.ToSlash(string(raw))
		if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(name))); err == nil {
			candidates[name] = struct{}{}
		}
	}
	return candidates
}

func validateMarkdownFile(t *testing.T, root, name string, candidates map[string]struct{}) {
	t.Helper()

	contents, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(name)))
	if err != nil {
		t.Errorf("%s: read documentation: %v", name, err)
		return
	}

	for _, match := range machinePath.FindAllString(string(contents), -1) {
		t.Errorf("%s: machine-specific path %q is not portable", name, match)
	}

	var targets []string
	for _, match := range inlineMarkdownLink.FindAllSubmatch(contents, -1) {
		targets = append(targets, string(match[1]))
	}
	for _, match := range referenceLink.FindAllSubmatch(contents, -1) {
		targets = append(targets, string(match[1]))
	}

	for _, target := range targets {
		validateMarkdownTarget(t, root, name, target, candidates)
	}
}

func validateMarkdownTarget(t *testing.T, root, source, rawTarget string, candidates map[string]struct{}) {
	t.Helper()

	target := markdownDestination(rawTarget)
	if target == "" {
		return
	}

	parsed, err := url.Parse(target)
	if err != nil {
		t.Errorf("%s: invalid Markdown link %q: %v", source, target, err)
		return
	}

	switch strings.ToLower(parsed.Scheme) {
	case "https", "mailto":
		return
	case "http":
		t.Errorf("%s: external Markdown link %q must use HTTPS", source, target)
		return
	case "file":
		t.Errorf("%s: file URL %q is a machine-local dependency", source, target)
		return
	case "":
	default:
		t.Errorf("%s: unsupported Markdown link scheme in %q", source, target)
		return
	}

	if parsed.Path == "" {
		if parsed.Fragment != "" {
			validateHeadingFragment(t, root, source, source, parsed.Fragment)
		}
		return
	}
	if filepath.IsAbs(filepath.FromSlash(parsed.Path)) || filepath.VolumeName(parsed.Path) != "" {
		t.Errorf("%s: absolute Markdown link %q is not portable", source, target)
		return
	}

	decodedPath, err := url.PathUnescape(parsed.Path)
	if err != nil {
		t.Errorf("%s: invalid escaped Markdown path %q: %v", source, target, err)
		return
	}

	resolved := filepath.Clean(filepath.Join(filepath.Dir(source), filepath.FromSlash(decodedPath)))
	relative, err := filepath.Rel(root, filepath.Join(root, resolved))
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		t.Errorf("%s: Markdown link %q escapes the repository", source, target)
		return
	}
	resolved = filepath.ToSlash(relative)

	linkedFile := resolved
	if _, ok := candidates[resolved]; !ok {
		readme := strings.TrimSuffix(resolved, "/") + "/README.md"
		if _, ok := candidates[readme]; ok {
			linkedFile = readme
		} else if !candidateDirectory(resolved, candidates) {
			t.Errorf("%s: Markdown link %q targets missing, ignored, or untracked content", source, target)
			return
		}
	}

	linkedExtension := strings.ToLower(filepath.Ext(linkedFile))
	if parsed.Fragment != "" && (linkedExtension == ".md" || linkedExtension == ".mdx") {
		validateHeadingFragment(t, root, source, linkedFile, parsed.Fragment)
	}
}

func candidateDirectory(directory string, candidates map[string]struct{}) bool {
	prefix := strings.TrimSuffix(directory, "/") + "/"
	for candidate := range candidates {
		if strings.HasPrefix(candidate, prefix) {
			return true
		}
	}
	return false
}

func markdownDestination(raw string) string {
	target := strings.TrimSpace(raw)
	if strings.HasPrefix(target, "<") {
		if end := strings.Index(target, ">"); end >= 0 {
			return target[1:end]
		}
	}
	if fields := strings.Fields(target); len(fields) > 0 {
		return fields[0]
	}
	return ""
}

func validateHeadingFragment(t *testing.T, root, source, target, fragment string) {
	t.Helper()

	decoded, err := url.PathUnescape(fragment)
	if err != nil {
		t.Errorf("%s: invalid heading fragment %q: %v", source, fragment, err)
		return
	}
	wanted := strings.ToLower(decoded)

	file, err := os.Open(filepath.Join(root, filepath.FromSlash(target)))
	if err != nil {
		t.Errorf("%s: open heading target %s: %v", source, target, err)
		return
	}
	defer file.Close()

	anchors := make(map[string]int)
	inFence := false
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Text()
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~") {
			inFence = !inFence
			continue
		}
		if inFence || !strings.HasPrefix(trimmed, "#") {
			continue
		}
		heading := strings.TrimSpace(strings.TrimLeft(trimmed, "#"))
		if heading == "" {
			continue
		}
		base := githubHeadingSlug(heading)
		count := anchors[base]
		anchors[base] = count + 1
		anchor := base
		if count > 0 {
			anchor += "-" + strconv.Itoa(count)
		}
		if anchor == wanted {
			return
		}
	}
	if err := scanner.Err(); err != nil {
		t.Errorf("%s: scan heading target %s: %v", source, target, err)
		return
	}
	t.Errorf("%s: Markdown fragment #%s does not match a heading in %s", source, fragment, target)
}

func githubHeadingSlug(heading string) string {
	var slug strings.Builder
	lastHyphen := false
	for _, r := range strings.ToLower(heading) {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_':
			slug.WriteRune(r)
			lastHyphen = false
		case unicode.IsSpace(r) || r == '-':
			if slug.Len() > 0 && !lastHyphen {
				slug.WriteByte('-')
				lastHyphen = true
			}
		}
	}
	return strings.Trim(slug.String(), "-")
}
