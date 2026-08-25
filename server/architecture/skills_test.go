// Copyright (c) 2026 Sudo Systems Labs Ltd
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.

package architecture_test

import (
	"bytes"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

const maximumSkillLines = 500

var (
	skillNamePattern           = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)
	retiredSkillAuthorityPaths = retiredRepositoryAuthorityPaths()
)

type skillFrontmatter struct {
	Name        string `yaml:"name"`
	Description string `yaml:"description"`
}

func TestRepositorySkillsAreValid(t *testing.T) {
	repositoryRoot, ok := documentationRepositoryRoot(t)
	if !ok {
		t.Skip("repository skills are unavailable outside the monorepo checkout")
	}

	issues := validateSkillTree(
		repositoryRoot,
		documentationCandidates(t, repositoryRoot),
		retiredSkillAuthorityPaths,
	)
	for _, issue := range issues {
		t.Error(issue)
	}
}

func TestRetiredDocumentationAuthoritiesAreAbsent(t *testing.T) {
	repositoryRoot, ok := documentationRepositoryRoot(t)
	if !ok {
		t.Skip("repository documentation is unavailable outside the monorepo checkout")
	}

	retired := retiredRepositoryAuthorityPaths()
	for _, name := range retired {
		path := filepath.Join(repositoryRoot, filepath.FromSlash(strings.TrimSuffix(name, "/")))
		if _, err := os.Lstat(path); err == nil {
			t.Errorf("retired documentation authority still exists: %s", name)
		} else if !os.IsNotExist(err) {
			t.Errorf("inspect retired documentation authority %s: %v", name, err)
		}
	}
	if _, err := os.Lstat(filepath.Join(repositoryRoot, ".github", "skills")); err == nil {
		t.Error("repository skills must not be mirrored under .github/skills")
	} else if !os.IsNotExist(err) {
		t.Errorf("inspect .github/skills: %v", err)
	}

	candidates := documentationCandidates(t, repositoryRoot)
	for name := range candidates {
		if !repositoryTextCandidate(name) {
			continue
		}
		contents, err := os.ReadFile(filepath.Join(repositoryRoot, filepath.FromSlash(name)))
		if err != nil {
			t.Errorf("%s: read for retired-authority references: %v", name, err)
			continue
		}
		for _, retiredName := range retired {
			if bytes.Contains(contents, []byte(retiredName)) {
				t.Errorf("%s: references retired documentation authority %s", name, retiredName)
			}
		}
	}
}

func TestSkillValidationRejectsMalformedFixtures(t *testing.T) {
	legacyContext := retiredRepositoryAuthorityPaths()[0]
	tests := []struct {
		name      string
		files     map[string]string
		retired   []string
		wantIssue string
	}{
		{
			name:      "missing entrypoint",
			files:     map[string]string{".agents/skills/example/references/rules.md": "# Rules\n"},
			wantIssue: "requires exactly one top-level SKILL.md",
		},
		{
			name:      "nested entrypoint",
			files:     mergeSkillFixture("example", "Example work.", map[string]string{".agents/skills/example/references/SKILL.md": validSkill("example", "Nested.")}),
			wantIssue: "contains nested SKILL.md",
		},
		{
			name:      "malformed frontmatter",
			files:     map[string]string{".agents/skills/example/SKILL.md": "# Example\n"},
			wantIssue: "must begin with YAML frontmatter",
		},
		{
			name:      "invalid yaml",
			files:     map[string]string{".agents/skills/example/SKILL.md": "---\nname: [\ndescription: Example work.\n---\n"},
			wantIssue: "parse YAML frontmatter",
		},
		{
			name:      "missing name",
			files:     map[string]string{".agents/skills/example/SKILL.md": "---\ndescription: Example work.\n---\n"},
			wantIssue: "non-empty name",
		},
		{
			name:      "missing description",
			files:     map[string]string{".agents/skills/example/SKILL.md": "---\nname: example\n---\n"},
			wantIssue: "non-empty description",
		},
		{
			name:      "invalid name",
			files:     map[string]string{".agents/skills/example/SKILL.md": validSkill("Example Skill", "Example work.")},
			wantIssue: "lowercase hyphenated name",
		},
		{
			name:      "folder mismatch",
			files:     map[string]string{".agents/skills/example/SKILL.md": validSkill("different", "Example work.")},
			wantIssue: "does not match folder",
		},
		{
			name: "duplicate name",
			files: map[string]string{
				".agents/skills/first/SKILL.md":  validSkill("first", "First work."),
				".agents/skills/second/SKILL.md": validSkill("first", "Second work."),
			},
			wantIssue: "duplicate skill name",
		},
		{
			name:      "missing resource",
			files:     mergeSkillFixture("example", "Example work.", map[string]string{".agents/skills/example/SKILL.md": validSkill("example", "Example work.") + "\nRead [rules](references/rules.md).\n"}),
			wantIssue: "targets missing, ignored, or untracked content",
		},
		{
			name:      "absolute resource",
			files:     map[string]string{".agents/skills/example/SKILL.md": validSkill("example", "Example work.") + "\nRead [rules](/tmp/rules.md).\n"},
			wantIssue: "absolute Markdown link",
		},
		{
			name:      "machine path",
			files:     map[string]string{".agents/skills/example/SKILL.md": validSkill("example", "Example work.") + "\nUse /Users/example/rules.md.\n"},
			wantIssue: "machine-specific path",
		},
		{
			name:      "scratch dependency",
			files:     map[string]string{".agents/skills/example/SKILL.md": validSkill("example", "Example work.") + "\nRead `.scratch/notes.md`.\n"},
			wantIssue: "depends on ignored .scratch content",
		},
		{
			name:      "retired authority",
			files:     map[string]string{".agents/skills/example/SKILL.md": validSkill("example", "Example work.") + "\nRead `" + legacyContext + "`.\n"},
			retired:   []string{legacyContext},
			wantIssue: "references retired authority " + legacyContext,
		},
		{
			name:      "oversized entrypoint",
			files:     map[string]string{".agents/skills/example/SKILL.md": validSkill("example", "Example work.") + strings.Repeat("rule\n", maximumSkillLines)},
			wantIssue: "exceeds 500 lines",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			writeSkillFixture(t, root, test.files)
			issues := validateSkillTree(root, fixtureCandidates(test.files), test.retired)
			if !containsIssue(issues, test.wantIssue) {
				t.Fatalf("issues %q do not contain %q", issues, test.wantIssue)
			}
		})
	}
}

func TestSkillValidationAcceptsPortableSkill(t *testing.T) {
	files := mergeSkillFixture("example", "Example work.", map[string]string{
		".agents/skills/example/SKILL.md":            validSkill("example", "Example work.") + "\nRead [rules](references/rules.md).\n",
		".agents/skills/example/references/rules.md": "# Rules\n",
	})
	root := t.TempDir()
	writeSkillFixture(t, root, files)
	if issues := validateSkillTree(root, fixtureCandidates(files), nil); len(issues) != 0 {
		t.Fatalf("valid skill produced issues: %q", issues)
	}
}

func TestClaudeSkillAdaptersAreSymlinksToCanonicalSkills(t *testing.T) {
	t.Run("valid adapter", func(t *testing.T) {
		root := t.TempDir()
		files := map[string]string{
			".agents/skills/example/SKILL.md": validSkill("example", "Example work."),
		}
		writeSkillFixture(t, root, files)
		writeClaudeAdapter(t, root, "example", "../../.agents/skills/example")
		if issues := validateSkillTree(root, fixtureCandidates(files), nil); len(issues) != 0 {
			t.Fatalf("valid adapter produced issues: %q", issues)
		}
	})

	tests := []struct {
		name      string
		configure func(t *testing.T, root string)
		wantIssue string
	}{
		{
			name: "copied adapter",
			configure: func(t *testing.T, root string) {
				t.Helper()
				path := filepath.Join(root, ".claude", "skills", "example", "SKILL.md")
				if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte(validSkill("example", "Copied.")), 0o644); err != nil {
					t.Fatal(err)
				}
			},
			wantIssue: "must be a symlink",
		},
		{
			name:      "missing adapter",
			configure: func(t *testing.T, root string) { t.Helper() },
			wantIssue: "missing Claude adapter",
		},
		{
			name: "wrong target",
			configure: func(t *testing.T, root string) {
				t.Helper()
				writeClaudeAdapter(t, root, "example", "../../.agents/skills/other")
			},
			wantIssue: "must target ../../.agents/skills/example",
		},
		{
			name: "duplicate target",
			configure: func(t *testing.T, root string) {
				t.Helper()
				writeSkillFixture(t, root, map[string]string{
					".agents/skills/other/SKILL.md": validSkill("other", "Other work."),
				})
				writeClaudeAdapter(t, root, "example", "../../.agents/skills/example")
				writeClaudeAdapter(t, root, "other", "../../.agents/skills/example")
			},
			wantIssue: "duplicate Claude adapter target",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			files := map[string]string{
				".agents/skills/example/SKILL.md": validSkill("example", "Example work."),
			}
			writeSkillFixture(t, root, files)
			if err := os.MkdirAll(filepath.Join(root, ".claude", "skills"), 0o755); err != nil {
				t.Fatal(err)
			}
			test.configure(t, root)
			issues := validateSkillTree(root, fixtureCandidates(files), nil)
			if !containsIssue(issues, test.wantIssue) {
				t.Fatalf("issues %q do not contain %q", issues, test.wantIssue)
			}
		})
	}
}

func validateSkillTree(root string, candidates map[string]struct{}, retiredPaths []string) []string {
	skillsRoot := filepath.Join(root, ".agents", "skills")
	entries, err := os.ReadDir(skillsRoot)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return []string{fmt.Sprintf("read repository skills: %v", err)}
	}

	var issues []string
	seenNames := make(map[string]string)
	for _, entry := range entries {
		if !entry.IsDir() {
			issues = append(issues, fmt.Sprintf(".agents/skills/%s: skill root contains a non-directory entry", entry.Name()))
			continue
		}

		directory := filepath.Join(skillsRoot, entry.Name())
		entrypoint := filepath.Join(directory, "SKILL.md")
		if _, err := os.Stat(entrypoint); err != nil {
			issues = append(issues, fmt.Sprintf(".agents/skills/%s: requires exactly one top-level SKILL.md", entry.Name()))
		}

		walkErr := filepath.WalkDir(directory, func(path string, item os.DirEntry, walkErr error) error {
			if walkErr != nil {
				issues = append(issues, fmt.Sprintf("%s: inspect skill resource: %v", repositoryPath(root, path), walkErr))
				return nil
			}
			if item.IsDir() {
				return nil
			}
			if item.Name() == "SKILL.md" && path != entrypoint {
				issues = append(issues, fmt.Sprintf("%s: skill contains nested SKILL.md", repositoryPath(root, path)))
			}
			if strings.EqualFold(filepath.Ext(path), ".md") {
				issues = append(issues, validateSkillMarkdown(root, path, candidates, retiredPaths)...)
			}
			return nil
		})
		if walkErr != nil {
			issues = append(issues, fmt.Sprintf(".agents/skills/%s: walk skill: %v", entry.Name(), walkErr))
		}

		contents, err := os.ReadFile(entrypoint)
		if err != nil {
			continue
		}
		if lines := bytes.Count(contents, []byte{'\n'}) + 1; lines > maximumSkillLines {
			issues = append(issues, fmt.Sprintf("%s: exceeds %d lines (%d)", repositoryPath(root, entrypoint), maximumSkillLines, lines))
		}

		frontmatter, parseIssues := parseSkillFrontmatter(repositoryPath(root, entrypoint), contents)
		issues = append(issues, parseIssues...)
		if frontmatter == nil {
			continue
		}
		if frontmatter.Name == "" {
			issues = append(issues, fmt.Sprintf("%s: frontmatter requires a non-empty name", repositoryPath(root, entrypoint)))
		} else {
			if !skillNamePattern.MatchString(frontmatter.Name) {
				issues = append(issues, fmt.Sprintf("%s: frontmatter name %q must be a lowercase hyphenated name", repositoryPath(root, entrypoint), frontmatter.Name))
			}
			if frontmatter.Name != entry.Name() {
				issues = append(issues, fmt.Sprintf("%s: frontmatter name %q does not match folder %q", repositoryPath(root, entrypoint), frontmatter.Name, entry.Name()))
			}
			if previous, exists := seenNames[frontmatter.Name]; exists {
				issues = append(issues, fmt.Sprintf("%s: duplicate skill name %q also declared by %s", repositoryPath(root, entrypoint), frontmatter.Name, previous))
			} else {
				seenNames[frontmatter.Name] = repositoryPath(root, entrypoint)
			}
		}
		if frontmatter.Description == "" {
			issues = append(issues, fmt.Sprintf("%s: frontmatter requires a non-empty description", repositoryPath(root, entrypoint)))
		}
	}

	issues = append(issues, validateClaudeSkillAdapters(root)...)
	sort.Strings(issues)
	return issues
}

func validateClaudeSkillAdapters(root string) []string {
	adaptersRoot := filepath.Join(root, ".claude", "skills")
	adapters, err := os.ReadDir(adaptersRoot)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return []string{fmt.Sprintf("read Claude skill adapters: %v", err)}
	}

	canonicalRoot := filepath.Join(root, ".agents", "skills")
	canonicalEntries, err := os.ReadDir(canonicalRoot)
	if err != nil {
		return []string{fmt.Sprintf("read canonical skills for Claude adapters: %v", err)}
	}
	canonical := make(map[string]struct{})
	for _, entry := range canonicalEntries {
		if entry.IsDir() {
			canonical[entry.Name()] = struct{}{}
		}
	}

	var issues []string
	seenTargets := make(map[string]string)
	seenAdapters := make(map[string]struct{})
	for _, adapter := range adapters {
		name := adapter.Name()
		path := filepath.Join(adaptersRoot, name)
		seenAdapters[name] = struct{}{}
		if adapter.Type()&os.ModeSymlink == 0 {
			issues = append(issues, fmt.Sprintf(".claude/skills/%s: Claude skill adapter must be a symlink", name))
			continue
		}
		target, err := os.Readlink(path)
		if err != nil {
			issues = append(issues, fmt.Sprintf(".claude/skills/%s: read Claude skill adapter: %v", name, err))
			continue
		}
		target = filepath.ToSlash(target)
		expected := "../../.agents/skills/" + name
		if target != expected {
			issues = append(issues, fmt.Sprintf(".claude/skills/%s: Claude skill adapter must target %s, got %s", name, expected, target))
		}
		resolved := filepath.Clean(filepath.Join(adaptersRoot, filepath.FromSlash(target)))
		if previous, exists := seenTargets[resolved]; exists {
			issues = append(issues, fmt.Sprintf(".claude/skills/%s: duplicate Claude adapter target also used by %s", name, previous))
		} else {
			seenTargets[resolved] = name
		}
		if info, err := os.Stat(resolved); err != nil || !info.IsDir() {
			issues = append(issues, fmt.Sprintf(".claude/skills/%s: Claude skill adapter target is missing or not a directory", name))
		}
	}
	for name := range canonical {
		if _, exists := seenAdapters[name]; !exists {
			issues = append(issues, fmt.Sprintf(".agents/skills/%s: missing Claude adapter", name))
		}
	}
	return issues
}

func parseSkillFrontmatter(name string, contents []byte) (*skillFrontmatter, []string) {
	normalized := strings.ReplaceAll(string(contents), "\r\n", "\n")
	if !strings.HasPrefix(normalized, "---\n") {
		return nil, []string{fmt.Sprintf("%s: must begin with YAML frontmatter", name)}
	}
	end := strings.Index(normalized[4:], "\n---\n")
	if end < 0 {
		return nil, []string{fmt.Sprintf("%s: YAML frontmatter has no closing delimiter", name)}
	}

	var frontmatter skillFrontmatter
	if err := yaml.Unmarshal([]byte(normalized[4:4+end]), &frontmatter); err != nil {
		return nil, []string{fmt.Sprintf("%s: parse YAML frontmatter: %v", name, err)}
	}
	frontmatter.Name = strings.TrimSpace(frontmatter.Name)
	frontmatter.Description = strings.TrimSpace(frontmatter.Description)
	return &frontmatter, nil
}

func validateSkillMarkdown(root, path string, candidates map[string]struct{}, retiredPaths []string) []string {
	contents, err := os.ReadFile(path)
	if err != nil {
		return []string{fmt.Sprintf("%s: read skill Markdown: %v", repositoryPath(root, path), err)}
	}
	name := repositoryPath(root, path)
	text := string(contents)
	var issues []string
	for _, match := range machinePath.FindAllString(text, -1) {
		issues = append(issues, fmt.Sprintf("%s: machine-specific path %q is not portable", name, match))
	}
	if strings.Contains(text, ".scratch/") {
		issues = append(issues, fmt.Sprintf("%s: depends on ignored .scratch content", name))
	}
	for _, retired := range retiredPaths {
		if strings.Contains(text, retired) {
			issues = append(issues, fmt.Sprintf("%s: references retired authority %s", name, retired))
		}
	}

	var targets []string
	for _, match := range inlineMarkdownLink.FindAllSubmatch(contents, -1) {
		targets = append(targets, string(match[1]))
	}
	for _, match := range referenceLink.FindAllSubmatch(contents, -1) {
		targets = append(targets, string(match[1]))
	}
	for _, rawTarget := range targets {
		target := markdownDestination(rawTarget)
		if target == "" {
			continue
		}
		parsed, err := url.Parse(target)
		if err != nil {
			issues = append(issues, fmt.Sprintf("%s: invalid Markdown link %q: %v", name, target, err))
			continue
		}
		if parsed.Scheme == "https" || parsed.Scheme == "mailto" || (parsed.Scheme == "" && parsed.Path == "") {
			continue
		}
		if parsed.Scheme != "" {
			issues = append(issues, fmt.Sprintf("%s: unsupported Markdown link scheme in %q", name, target))
			continue
		}
		if filepath.IsAbs(filepath.FromSlash(parsed.Path)) || filepath.VolumeName(parsed.Path) != "" {
			issues = append(issues, fmt.Sprintf("%s: absolute Markdown link %q is not portable", name, target))
			continue
		}
		decoded, err := url.PathUnescape(parsed.Path)
		if err != nil {
			issues = append(issues, fmt.Sprintf("%s: invalid escaped Markdown path %q: %v", name, target, err))
			continue
		}
		resolved := filepath.ToSlash(filepath.Clean(filepath.Join(filepath.Dir(name), filepath.FromSlash(decoded))))
		if resolved == ".." || strings.HasPrefix(resolved, "../") {
			issues = append(issues, fmt.Sprintf("%s: Markdown link %q escapes the repository", name, target))
			continue
		}
		if _, exists := candidates[resolved]; !exists && !candidateDirectory(resolved, candidates) {
			issues = append(issues, fmt.Sprintf("%s: Markdown link %q targets missing, ignored, or untracked content", name, target))
		}
	}
	return issues
}

func validSkill(name, description string) string {
	return fmt.Sprintf("---\nname: %s\ndescription: %s\n---\n\n# Skill\n", name, description)
}

func mergeSkillFixture(name, description string, extra map[string]string) map[string]string {
	files := map[string]string{
		filepath.ToSlash(filepath.Join(".agents", "skills", name, "SKILL.md")): validSkill(name, description),
	}
	for path, contents := range extra {
		files[path] = contents
	}
	return files
}

func writeSkillFixture(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for name, contents := range files {
		path := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("create fixture directory: %v", err)
		}
		if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
			t.Fatalf("write fixture: %v", err)
		}
	}
}

func fixtureCandidates(files map[string]string) map[string]struct{} {
	candidates := make(map[string]struct{}, len(files))
	for name := range files {
		candidates[filepath.ToSlash(name)] = struct{}{}
	}
	return candidates
}

func writeClaudeAdapter(t *testing.T, root, name, target string) {
	t.Helper()
	path := filepath.Join(root, ".claude", "skills", name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("create Claude adapter directory: %v", err)
	}
	if err := os.Symlink(target, path); err != nil {
		t.Fatalf("create Claude adapter: %v", err)
	}
}

func containsIssue(issues []string, wanted string) bool {
	for _, issue := range issues {
		if strings.Contains(issue, wanted) {
			return true
		}
	}
	return false
}

func repositoryPath(root, path string) string {
	relative, err := filepath.Rel(root, path)
	if err != nil {
		return filepath.ToSlash(path)
	}
	return filepath.ToSlash(relative)
}

func retiredRepositoryAuthorityPaths() []string {
	return []string{
		"CON" + "TEXT.md",
		filepath.ToSlash(filepath.Join("docs", "architecture")) + "/",
		filepath.ToSlash(filepath.Join("docs", "contributing")) + "/",
		filepath.ToSlash(filepath.Join("docs", "project")) + "/",
	}
}

func repositoryTextCandidate(name string) bool {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".go", ".js", ".json", ".md", ".mdx", ".mjs", ".ts", ".tsx", ".txt", ".yaml", ".yml":
		return true
	default:
		return name == "AGENTS.md" || name == "CLAUDE.md"
	}
}
