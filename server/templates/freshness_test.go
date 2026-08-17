// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package templates

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"path"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/sudosylabs/proctor/server/i18n"
)

//go:embed *.mjml *.html *.txt partials/*.mjml package.json package-lock.json
var authoredAndGeneratedFiles embed.FS

var generatedHeader = regexp.MustCompile(`\A<!-- Code generated from ([^ ]+) by mjml ([^;]+); DO NOT EDIT\. Source digest: sha256:([0-9a-f]{64})\. Output digest: sha256:([0-9a-f]{64})\. -->\n`)

func TestGeneratedTemplatesAreFreshAndSourcesDeclareExactProperties(t *testing.T) {
	t.Parallel()
	packageJSON, err := authoredAndGeneratedFiles.ReadFile("package.json")
	if err != nil {
		t.Fatal(err)
	}
	var toolchain struct {
		DevDependencies struct {
			MJML string `json:"mjml"`
		} `json:"devDependencies"`
	}
	if err := json.Unmarshal(packageJSON, &toolchain); err != nil || toolchain.DevDependencies.MJML == "" {
		t.Fatalf("decode pinned MJML version: %v", err)
	}

	partialEntries, err := authoredAndGeneratedFiles.ReadDir("partials")
	if err != nil {
		t.Fatalf("ReadDir(partials): %v", err)
	}
	var partialNames []string
	for _, entry := range partialEntries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".mjml") {
			partialNames = append(partialNames, path.Join("partials", entry.Name()))
		}
	}
	sort.Strings(partialNames)

	propertyNames := []string{
		".Copy.Subject", ".Copy.Preheader", ".Copy.Heading", ".Copy.Body",
		".Copy.ActionLabel", ".Copy.Footer", ".ActionURL",
	}
	for _, key := range i18n.AllKeys() {
		sourceName := string(key) + ".mjml"
		source, err := authoredAndGeneratedFiles.ReadFile(sourceName)
		if err != nil {
			t.Errorf("read %s: %v", sourceName, err)
			continue
		}
		textSource, err := authoredAndGeneratedFiles.ReadFile(string(key) + ".txt")
		if err != nil {
			t.Errorf("read %s.txt: %v", key, err)
			continue
		}
		for _, propertyName := range propertyNames {
			if !strings.Contains(string(source), propertyName) {
				t.Errorf("%s does not document property %s", sourceName, propertyName)
			}
			if !strings.Contains(string(textSource), propertyName) {
				t.Errorf("%s.txt does not document property %s", key, propertyName)
			}
		}

		generated, err := authoredAndGeneratedFiles.ReadFile(string(key) + ".html")
		if err != nil {
			t.Errorf("read %s.html: %v", key, err)
			continue
		}
		matches := generatedHeader.FindSubmatch(generated)
		if matches == nil || string(matches[1]) != sourceName {
			t.Errorf("%s.html does not have a valid generation header", key)
			continue
		}
		if got := string(matches[2]); got != toolchain.DevDependencies.MJML {
			t.Errorf("%s.html compiler version = %s, want %s; regenerate templates", key, got, toolchain.DevDependencies.MJML)
		}
		wantSourceDigest, err := digestSources(sourceName, partialNames)
		if err != nil {
			t.Errorf("digest %s sources: %v", key, err)
			continue
		}
		if got := string(matches[3]); got != wantSourceDigest {
			t.Errorf("%s.html source digest = %s, want %s; regenerate templates", key, got, wantSourceDigest)
		}
		body := generated[len(matches[0]):]
		bodyDigest := sha256.Sum256(body)
		if got, want := string(matches[4]), hex.EncodeToString(bodyDigest[:]); got != want {
			t.Errorf("%s.html output digest = %s, want %s; regenerate templates", key, got, want)
		}
	}
}

func digestSources(sourceName string, partialNames []string) (string, error) {
	hash := sha256.New()
	inputs := append([]string{sourceName}, partialNames...)
	inputs = append(inputs, "package.json", "package-lock.json")
	for _, relativeName := range inputs {
		content, err := authoredAndGeneratedFiles.ReadFile(relativeName)
		if err != nil {
			return "", fmt.Errorf("read %s: %w", relativeName, err)
		}
		hash.Write([]byte(relativeName))
		hash.Write([]byte{0})
		hash.Write(content)
		hash.Write([]byte{0})
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}
