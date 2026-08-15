// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

// Package safemarkdown owns the shared candidate-facing Markdown sanitizer
// used by protected Exam presentation and released student remarks. It owns
// neither rendering nor product-specific presentation policy and depends only
// on the standard library; application children decide where sanitization is
// required and clients must still render the result in a sandboxed mode.
package safemarkdown

import (
	"fmt"
	"html"
	"net/url"
	"strings"
	"unicode"
)

// Sanitize preserves inert Markdown formatting while
// removing constructs that can execute active HTML, navigate through unsafe
// schemes, or automatically fetch remote content. Candidate clients must
// still render Markdown in a sandboxed, HTML-disabled mode.
func Sanitize(value string) string {
	value, code := protectMarkdownCode(value)
	value = removeActiveHTMLElements(value)
	value = stripRawHTML(value)
	value = stripReferenceDefinitions(value)
	return code.restore(sanitizeMarkdownDestinations(value))
}

type protectedMarkdownCode struct {
	prefix   string
	snippets []string
}

func protectMarkdownCode(value string) (string, protectedMarkdownCode) {
	prefix := "\x00proctor-markdown-code-"
	for strings.Contains(value, prefix) {
		prefix += "x"
	}
	protected := protectedMarkdownCode{prefix: prefix}
	var output strings.Builder
	output.Grow(len(value))
	for index := 0; index < len(value); {
		if value[index] != '`' {
			output.WriteByte(value[index])
			index++
			continue
		}
		run := repeatedByte(value[index:], '`')
		closingOffset := strings.Index(value[index+run:], strings.Repeat("`", run))
		if closingOffset < 0 {
			output.WriteString(value[index:])
			break
		}
		closing := index + run + closingOffset + run
		protected.snippets = append(protected.snippets, value[index:closing])
		output.WriteString(protected.placeholder(len(protected.snippets) - 1))
		index = closing
	}
	return output.String(), protected
}

func (protected protectedMarkdownCode) placeholder(index int) string {
	return fmt.Sprintf("%s%d\x00", protected.prefix, index)
}

func (protected protectedMarkdownCode) restore(value string) string {
	for index, snippet := range protected.snippets {
		value = strings.ReplaceAll(value, protected.placeholder(index), snippet)
	}
	return value
}

func removeActiveHTMLElements(value string) string {
	for _, tag := range []string{"script", "style", "iframe", "object", "embed", "svg", "math", "form", "video", "audio"} {
		for {
			lower := strings.ToLower(value)
			start := findHTMLTag(lower, tag, 0)
			if start < 0 {
				break
			}
			openEnd := strings.IndexByte(lower[start:], '>')
			if openEnd < 0 {
				value = value[:start]
				break
			}
			openEnd += start
			if openEnd > start && lower[openEnd-1] == '/' {
				value = value[:start] + value[openEnd+1:]
				continue
			}
			closeStart := strings.Index(lower[openEnd+1:], "</"+tag)
			if closeStart < 0 {
				value = value[:start]
				break
			}
			closeStart += openEnd + 1
			closeEnd := strings.IndexByte(lower[closeStart:], '>')
			if closeEnd < 0 {
				value = value[:start]
				break
			}
			closeEnd += closeStart
			value = value[:start] + value[closeEnd+1:]
		}
	}
	return value
}

func findHTMLTag(lower, tag string, offset int) int {
	needle := "<" + tag
	for offset < len(lower) {
		found := strings.Index(lower[offset:], needle)
		if found < 0 {
			return -1
		}
		found += offset
		after := found + len(needle)
		if after == len(lower) || lower[after] == '>' || lower[after] == '/' || unicode.IsSpace(rune(lower[after])) {
			return found
		}
		offset = after
	}
	return -1
}

func stripRawHTML(value string) string {
	var output strings.Builder
	output.Grow(len(value))
	for index := 0; index < len(value); {
		if strings.HasPrefix(value[index:], "<!--") {
			end := strings.Index(value[index+4:], "-->")
			if end < 0 {
				break
			}
			index += 4 + end + 3
			continue
		}
		if value[index] == '<' {
			end := strings.IndexByte(value[index+1:], '>')
			if end >= 0 {
				end += index + 1
				if looksLikeRawHTML(value[index+1 : end]) {
					index = end + 1
					continue
				}
			}
		}
		output.WriteByte(value[index])
		index++
	}
	return output.String()
}

func looksLikeRawHTML(inside string) bool {
	inside = strings.TrimSpace(inside)
	if inside == "" {
		return false
	}
	if inside[0] == '/' {
		inside = strings.TrimSpace(inside[1:])
	}
	if inside == "" {
		return false
	}
	if inside[0] == '!' || inside[0] == '?' {
		return true
	}
	first := inside[0]
	return first >= 'A' && first <= 'Z' || first >= 'a' && first <= 'z'
}

func stripReferenceDefinitions(value string) string {
	lines := strings.SplitAfter(value, "\n")
	var output strings.Builder
	output.Grow(len(value))
	for _, line := range lines {
		candidate := strings.TrimLeft(line, " \t")
		if len(line)-len(candidate) <= 3 && candidate != "" && candidate[0] == '[' {
			if closing := strings.Index(candidate, "]:"); closing > 1 {
				continue
			}
		}
		output.WriteString(line)
	}
	return output.String()
}

func sanitizeMarkdownDestinations(value string) string {
	var output strings.Builder
	output.Grow(len(value))
	for index := 0; index < len(value); {
		if value[index] == '`' {
			run := repeatedByte(value[index:], '`')
			closing := strings.Index(value[index+run:], strings.Repeat("`", run))
			if closing >= 0 {
				closing += index + run + run
				output.WriteString(value[index:closing])
				index = closing
				continue
			}
		}
		image := value[index] == '!' && index+1 < len(value) && value[index+1] == '['
		link := value[index] == '['
		if image || link {
			labelStart := index + 1
			if image {
				labelStart++
			}
			labelEnd := findUnescaped(value, ']', labelStart)
			if labelEnd >= 0 {
				label := value[labelStart:labelEnd]
				next := labelEnd + 1
				if next < len(value) && value[next] == '(' {
					end := findClosingParenthesis(value, next)
					if end >= 0 {
						if !image {
							if destination, ok := safeMarkdownDestination(value[next+1 : end]); ok {
								output.WriteByte('[')
								output.WriteString(label)
								output.WriteString("](")
								output.WriteString(destination)
								output.WriteByte(')')
								index = end + 1
								continue
							}
						}
						output.WriteString(label)
						index = end + 1
						continue
					}
				}
				if next < len(value) && value[next] == '[' {
					if referenceEnd := findUnescaped(value, ']', next+1); referenceEnd >= 0 {
						output.WriteString(label)
						index = referenceEnd + 1
						continue
					}
				}
				if image {
					output.WriteString(label)
					index = labelEnd + 1
					continue
				}
			}
		}
		output.WriteByte(value[index])
		index++
	}
	return output.String()
}

func repeatedByte(value string, target byte) int {
	count := 0
	for count < len(value) && value[count] == target {
		count++
	}
	return count
}

func findUnescaped(value string, target byte, start int) int {
	for index := start; index < len(value); index++ {
		if value[index] == '\\' {
			index++
			continue
		}
		if value[index] == target {
			return index
		}
	}
	return -1
}

func findClosingParenthesis(value string, opening int) int {
	depth := 1
	for index := opening + 1; index < len(value); index++ {
		if value[index] == '\\' {
			index++
			continue
		}
		switch value[index] {
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				return index
			}
		}
	}
	return -1
}

func safeMarkdownDestination(raw string) (string, bool) {
	destination := strings.TrimSpace(raw)
	if strings.HasPrefix(destination, "<") && strings.HasSuffix(destination, ">") {
		destination = strings.TrimSpace(destination[1 : len(destination)-1])
	} else if split := strings.IndexFunc(destination, unicode.IsSpace); split >= 0 {
		destination = destination[:split]
	}
	destination = html.UnescapeString(strings.ReplaceAll(destination, "\\", ""))
	if destination == "" || strings.IndexFunc(destination, func(character rune) bool {
		return unicode.IsControl(character) || unicode.IsSpace(character)
	}) >= 0 || strings.HasPrefix(destination, "//") {
		return "", false
	}
	parsed, err := url.Parse(destination)
	if err != nil {
		return "", false
	}
	switch strings.ToLower(parsed.Scheme) {
	case "":
		return destination, !strings.Contains(strings.Split(destination, "/")[0], ":")
	case "https", "mailto":
		return destination, true
	default:
		return "", false
	}
}
