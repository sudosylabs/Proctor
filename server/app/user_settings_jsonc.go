// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package app

import (
	"encoding/json"
	"strings"
	"unicode/utf8"

	"github.com/sudosylabs/proctor/server/model"
)

const (
	userSettingsSourceMaxBytes       = model.UserSettingsSourceMaxBytes
	userSettingsNestingMaxDepth      = model.UserSettingsNestingMaxDepth
	userSettingsPathMaximumCount     = model.UserSettingsPathMaximumCount
	userSettingsKeyMaxBytes          = model.UserSettingsKeyMaxBytes
	userSettingsStringMaxBytes       = model.UserSettingsStringMaxBytes
	userSettingsCollectionMaxEntries = model.UserSettingsCollectionMaxElements
)

type userSettingsDiagnostic struct {
	Code   string
	Line   int
	Column int
}

func validateUserSettingsSource(source string) []userSettingsDiagnostic {
	if !utf8.ValidString(source) {
		return []userSettingsDiagnostic{{Code: "invalid_utf8", Line: 1, Column: 1}}
	}
	if len(source) > userSettingsSourceMaxBytes {
		return []userSettingsDiagnostic{{Code: "source_too_large", Line: 1, Column: 1}}
	}

	parser := userSettingsJSONCParser{source: source, line: 1, column: 1}
	parser.skipSpaceAndComments()
	if len(parser.diagnostics) == 0 && !parser.parseObject(1, userSettingsObjectRoot) {
		return parser.diagnostics
	}
	parser.skipSpaceAndComments()
	if len(parser.diagnostics) == 0 && parser.offset != len(source) {
		parser.fail("trailing_value", parser.line, parser.column)
	}
	return parser.diagnostics
}

type userSettingsObjectKind uint8

const (
	userSettingsObjectNested userSettingsObjectKind = iota
	userSettingsObjectRoot
	userSettingsObjectLanguage
)

type userSettingsJSONCParser struct {
	source      string
	offset      int
	line        int
	column      int
	pathCount   int
	diagnostics []userSettingsDiagnostic
}

func (p *userSettingsJSONCParser) parseValue(depth int, objectKind userSettingsObjectKind) bool {
	p.skipSpaceAndComments()
	if len(p.diagnostics) != 0 || p.offset >= len(p.source) {
		if len(p.diagnostics) == 0 {
			p.fail("value_required", p.line, p.column)
		}
		return false
	}

	switch p.source[p.offset] {
	case '{':
		return p.parseObject(depth, objectKind)
	case '[':
		return p.parseArray(depth)
	case '"':
		_, ok := p.parseString()
		return ok
	case 't':
		return p.parseLiteral("true")
	case 'f':
		return p.parseLiteral("false")
	case 'n':
		return p.parseLiteral("null")
	default:
		return p.parseNumber()
	}
}

func (p *userSettingsJSONCParser) parseObject(depth int, kind userSettingsObjectKind) bool {
	if depth > userSettingsNestingMaxDepth {
		p.fail("nesting_too_deep", p.line, p.column)
		return false
	}
	if !p.consume('{') {
		p.fail("top_level_object_required", p.line, p.column)
		return false
	}
	p.skipSpaceAndComments()
	if len(p.diagnostics) != 0 {
		return false
	}
	if p.consume('}') {
		return true
	}

	seen := make(map[string]struct{})
	count := 0
	for {
		keyLine, keyColumn := p.line, p.column
		key, ok := p.parseString()
		if !ok {
			if len(p.diagnostics) == 0 {
				p.fail("object_key_required", keyLine, keyColumn)
			}
			return false
		}
		count++
		p.pathCount++
		if count > userSettingsCollectionMaxEntries {
			p.fail("collection_too_large", keyLine, keyColumn)
			return false
		}
		if p.pathCount > userSettingsPathMaximumCount {
			p.fail("too_many_setting_paths", keyLine, keyColumn)
			return false
		}
		if len(key) > userSettingsKeyMaxBytes {
			p.fail("key_too_large", keyLine, keyColumn)
			return false
		}
		if _, duplicate := seen[key]; duplicate {
			p.fail("duplicate_key", keyLine, keyColumn)
			return false
		}
		seen[key] = struct{}{}

		valueKind := userSettingsObjectNested
		switch kind {
		case userSettingsObjectRoot:
			if isUserSettingsLanguageBlock(key) {
				valueKind = userSettingsObjectLanguage
			} else if !isUserSettingsIdentifier(key) {
				p.fail("invalid_setting_key", keyLine, keyColumn)
				return false
			}
		case userSettingsObjectLanguage:
			if !isUserSettingsIdentifier(key) {
				p.fail("invalid_setting_key", keyLine, keyColumn)
				return false
			}
		}

		p.skipSpaceAndComments()
		if !p.consume(':') {
			p.fail("colon_required", p.line, p.column)
			return false
		}
		p.skipSpaceAndComments()
		if valueKind == userSettingsObjectLanguage && (p.offset >= len(p.source) || p.source[p.offset] != '{') {
			p.fail("language_block_object_required", p.line, p.column)
			return false
		}
		if !p.parseValue(depth+1, valueKind) {
			return false
		}

		p.skipSpaceAndComments()
		if len(p.diagnostics) != 0 {
			return false
		}
		if p.consume('}') {
			return true
		}
		if !p.consume(',') {
			p.fail("comma_or_object_end_required", p.line, p.column)
			return false
		}
		p.skipSpaceAndComments()
		if len(p.diagnostics) != 0 {
			return false
		}
		if p.consume('}') {
			return true
		}
	}
}

func (p *userSettingsJSONCParser) parseArray(depth int) bool {
	if depth > userSettingsNestingMaxDepth {
		p.fail("nesting_too_deep", p.line, p.column)
		return false
	}
	if !p.consume('[') {
		return false
	}
	p.skipSpaceAndComments()
	if len(p.diagnostics) != 0 {
		return false
	}
	if p.consume(']') {
		return true
	}

	count := 0
	for {
		count++
		if count > userSettingsCollectionMaxEntries {
			p.fail("collection_too_large", p.line, p.column)
			return false
		}
		if !p.parseValue(depth+1, userSettingsObjectNested) {
			return false
		}
		p.skipSpaceAndComments()
		if len(p.diagnostics) != 0 {
			return false
		}
		if p.consume(']') {
			return true
		}
		if !p.consume(',') {
			p.fail("comma_or_array_end_required", p.line, p.column)
			return false
		}
		p.skipSpaceAndComments()
		if len(p.diagnostics) != 0 {
			return false
		}
		if p.consume(']') {
			return true
		}
	}
}

func (p *userSettingsJSONCParser) parseString() (string, bool) {
	if p.offset >= len(p.source) || p.source[p.offset] != '"' {
		return "", false
	}
	start, line, column := p.offset, p.line, p.column
	p.advance()
	for p.offset < len(p.source) {
		value := p.source[p.offset]
		if value == '"' {
			p.advance()
			var decoded string
			if err := json.Unmarshal([]byte(p.source[start:p.offset]), &decoded); err != nil {
				p.fail("invalid_string", line, column)
				return "", false
			}
			if len(decoded) > userSettingsStringMaxBytes {
				p.fail("string_too_large", line, column)
				return "", false
			}
			return decoded, true
		}
		if value < 0x20 {
			p.fail("invalid_string", p.line, p.column)
			return "", false
		}
		if value == '\\' {
			p.advance()
			if p.offset >= len(p.source) {
				break
			}
			p.advance()
			continue
		}
		p.advance()
	}
	p.fail("unterminated_string", line, column)
	return "", false
}

func (p *userSettingsJSONCParser) parseLiteral(literal string) bool {
	line, column := p.line, p.column
	if !strings.HasPrefix(p.source[p.offset:], literal) {
		p.fail("invalid_value", line, column)
		return false
	}
	for range literal {
		p.advance()
	}
	if p.offset < len(p.source) && !isUserSettingsValueDelimiter(p.source[p.offset]) {
		p.fail("invalid_value", line, column)
		return false
	}
	return true
}

func (p *userSettingsJSONCParser) parseNumber() bool {
	start, line, column := p.offset, p.line, p.column
	for p.offset < len(p.source) && !isUserSettingsValueDelimiter(p.source[p.offset]) {
		p.advance()
	}
	if start == p.offset || !json.Valid([]byte(p.source[start:p.offset])) {
		p.fail("invalid_value", line, column)
		return false
	}
	return true
}

func (p *userSettingsJSONCParser) skipSpaceAndComments() {
	for p.offset < len(p.source) && len(p.diagnostics) == 0 {
		switch p.source[p.offset] {
		case ' ', '\t', '\r', '\n':
			p.advance()
		case '/':
			line, column := p.line, p.column
			if p.offset+1 >= len(p.source) {
				return
			}
			switch p.source[p.offset+1] {
			case '/':
				p.advance()
				p.advance()
				for p.offset < len(p.source) && p.source[p.offset] != '\n' {
					p.advance()
				}
			case '*':
				p.advance()
				p.advance()
				closed := false
				for p.offset < len(p.source) {
					if p.source[p.offset] == '*' && p.offset+1 < len(p.source) && p.source[p.offset+1] == '/' {
						p.advance()
						p.advance()
						closed = true
						break
					}
					p.advance()
				}
				if !closed {
					p.fail("unterminated_comment", line, column)
				}
			default:
				return
			}
		default:
			return
		}
	}
}

func (p *userSettingsJSONCParser) consume(value byte) bool {
	if p.offset >= len(p.source) || p.source[p.offset] != value {
		return false
	}
	p.advance()
	return true
}

func (p *userSettingsJSONCParser) advance() {
	value := p.source[p.offset]
	p.offset++
	if value == '\n' {
		p.line++
		p.column = 1
		return
	}
	if value&0xc0 != 0x80 {
		p.column++
	}
}

func (p *userSettingsJSONCParser) fail(code string, line, column int) {
	if len(p.diagnostics) != 0 {
		return
	}
	p.diagnostics = append(p.diagnostics, userSettingsDiagnostic{Code: code, Line: line, Column: column})
}

func isUserSettingsValueDelimiter(value byte) bool {
	switch value {
	case ' ', '\t', '\r', '\n', ',', '}', ']', '/':
		return true
	default:
		return false
	}
}

func isUserSettingsIdentifier(value string) bool {
	if value == "" {
		return false
	}
	segmentStart := true
	for _, character := range value {
		if character == '.' {
			if segmentStart {
				return false
			}
			segmentStart = true
			continue
		}
		if !isUserSettingsIdentifierCharacter(character) {
			return false
		}
		segmentStart = false
	}
	return !segmentStart
}

func isUserSettingsIdentifierCharacter(value rune) bool {
	return value >= 'a' && value <= 'z' || value >= 'A' && value <= 'Z' ||
		value >= '0' && value <= '9' || value == '_' || value == '-'
}

func isUserSettingsLanguageBlock(value string) bool {
	if len(value) < 3 || value[0] != '[' || value[len(value)-1] != ']' {
		return false
	}
	for _, character := range value[1 : len(value)-1] {
		if !isUserSettingsIdentifierCharacter(character) && character != '+' && character != '#' {
			return false
		}
	}
	return true
}
