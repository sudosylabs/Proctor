// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package filecontent

import (
	"bufio"
	"io"
)

// Match encoding/json's accepted nesting depth without retaining authored
// values. Decode constructs the document tree; Decoder.Token still allocates
// decoded strings and numbers. Neither meets this syntax-only pass's bound:
// memory must not grow with value length or the number of document members.
const maximumExamResourceJSONDepth = 10000

type examResourceJSONState uint8

const (
	jsonRootValue examResourceJSONState = iota
	jsonRootDone
	jsonArrayFirst
	jsonArrayValue
	jsonArrayAfter
	jsonObjectFirst
	jsonObjectKey
	jsonObjectColon
	jsonObjectValue
	jsonObjectAfter
)

func validateExamResourceJSON(file io.ReadSeeker) error {
	if err := validateExamResourceUTF8(file); err != nil {
		return err
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return err
	}
	return validateExamResourceJSONSyntax(file)
}

// The preceding UTF-8 pass rejects raw invalid encoding and NUL. This pass
// validates one JSON value without interpreting its contents: duplicate names,
// arbitrary numeric precision, and escaped unpaired surrogates remain valid.
// Parent states record what follows the current value, so nesting requires no
// recursion or per-container allocation. Reader errors stay content-safe; the
// owning resource pipeline preserves cancellation from its context.
func validateExamResourceJSONSyntax(source io.Reader) error {
	reader := bufio.NewReaderSize(source, examResourceCopyBuffer)
	var states [maximumExamResourceJSONDepth + 1]examResourceJSONState
	depth := 0
	for {
		value, err := readJSONNonSpace(reader)
		if err != nil {
			if err == io.EOF && depth == 0 && states[0] == jsonRootDone {
				return nil
			}
			return ErrInvalidExamResourceContent
		}
		state := states[depth]
		switch state {
		case jsonRootDone:
			return ErrInvalidExamResourceContent
		case jsonArrayAfter:
			if value == ']' {
				depth--
				continue
			}
			if value != ',' {
				return ErrInvalidExamResourceContent
			}
			states[depth] = jsonArrayValue
			continue
		case jsonObjectAfter:
			if value == '}' {
				depth--
				continue
			}
			if value != ',' {
				return ErrInvalidExamResourceContent
			}
			states[depth] = jsonObjectKey
			continue
		case jsonObjectFirst, jsonObjectKey:
			if value == '}' && state == jsonObjectFirst {
				depth--
				continue
			}
			if value != '"' || readJSONString(reader) != nil {
				return ErrInvalidExamResourceContent
			}
			states[depth] = jsonObjectColon
			continue
		case jsonObjectColon:
			if value != ':' {
				return ErrInvalidExamResourceContent
			}
			states[depth] = jsonObjectValue
			continue
		case jsonArrayFirst:
			if value == ']' {
				depth--
				continue
			}
			states[depth] = jsonArrayAfter
		case jsonArrayValue:
			states[depth] = jsonArrayAfter
		case jsonObjectValue:
			states[depth] = jsonObjectAfter
		case jsonRootValue:
			states[depth] = jsonRootDone
		}
		switch value {
		case '[', '{':
			if depth == maximumExamResourceJSONDepth {
				return ErrInvalidExamResourceContent
			}
			depth++
			states[depth] = jsonArrayFirst
			if value == '{' {
				states[depth] = jsonObjectFirst
			}
		case '"':
			err = readJSONString(reader)
		case 't':
			err = readJSONLiteral(reader, "rue")
		case 'f':
			err = readJSONLiteral(reader, "alse")
		case 'n':
			err = readJSONLiteral(reader, "ull")
		case '-', '0', '1', '2', '3', '4', '5', '6', '7', '8', '9':
			err = readJSONNumber(reader, value)
		default:
			err = ErrInvalidExamResourceContent
		}
		if err != nil {
			return err
		}
	}
}

func readJSONNonSpace(reader *bufio.Reader) (byte, error) {
	for {
		value, err := reader.ReadByte()
		if err != nil {
			return 0, err
		}
		if value != ' ' && value != '\n' && value != '\r' && value != '\t' {
			return value, nil
		}
	}
}

func readJSONLiteral(reader *bufio.Reader, suffix string) error {
	for i := range len(suffix) {
		value, err := reader.ReadByte()
		if err != nil || value != suffix[i] {
			return ErrInvalidExamResourceContent
		}
	}
	return nil
}

func readJSONString(reader *bufio.Reader) error {
	for {
		value, err := reader.ReadByte()
		if err != nil {
			return ErrInvalidExamResourceContent
		}
		switch {
		case value == '"':
			return nil
		case value < 0x20:
			return ErrInvalidExamResourceContent
		case value == '\\':
			value, err = reader.ReadByte()
			if err != nil {
				return ErrInvalidExamResourceContent
			}
			switch value {
			case '"', '\\', '/', 'b', 'f', 'n', 'r', 't':
			case 'u':
				for range 4 {
					value, err = reader.ReadByte()
					if err != nil || !((value >= '0' && value <= '9') || (value >= 'a' && value <= 'f') || (value >= 'A' && value <= 'F')) {
						return ErrInvalidExamResourceContent
					}
				}
			default:
				return ErrInvalidExamResourceContent
			}
		}
	}
}

func readJSONNumber(reader *bufio.Reader, first byte) error {
	// Only EOF can complete a number without a following delimiter. Preserve
	// every other lookahead error: bufio may consume a one-shot reader error,
	// so ignoring it could turn a failed read into an accepted root number.
	if first == '-' {
		var err error
		first, err = reader.ReadByte()
		if err != nil {
			return ErrInvalidExamResourceContent
		}
	}
	if first < '0' || first > '9' {
		return ErrInvalidExamResourceContent
	}
	if first != '0' {
		if _, err := readJSONDigits(reader); err != nil {
			if err == io.EOF {
				return nil
			}
			return ErrInvalidExamResourceContent
		}
	}
	next, err := reader.Peek(1)
	if err != nil {
		if err == io.EOF {
			return nil
		}
		return ErrInvalidExamResourceContent
	}
	if next[0] == '.' {
		_, _ = reader.ReadByte()
		found, err := readJSONDigits(reader)
		if !found || err != nil && err != io.EOF {
			return ErrInvalidExamResourceContent
		}
		if err == io.EOF {
			return nil
		}
		next, err = reader.Peek(1)
		if err != nil {
			if err == io.EOF {
				return nil
			}
			return ErrInvalidExamResourceContent
		}
	}
	if next[0] == 'e' || next[0] == 'E' {
		_, _ = reader.ReadByte()
		next, err = reader.Peek(1)
		if err != nil {
			return ErrInvalidExamResourceContent
		}
		if next[0] == '+' || next[0] == '-' {
			_, _ = reader.ReadByte()
		}
		found, err := readJSONDigits(reader)
		if !found || err != nil && err != io.EOF {
			return ErrInvalidExamResourceContent
		}
	}
	return nil
}

func readJSONDigits(reader *bufio.Reader) (bool, error) {
	found := false
	for {
		next, err := reader.Peek(1)
		if err != nil {
			return found, err
		}
		if next[0] < '0' || next[0] > '9' {
			return found, nil
		}
		_, _ = reader.ReadByte()
		found = true
	}
}
