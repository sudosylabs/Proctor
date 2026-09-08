// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

// Package canonicaljson implements the bounded, integer-only JSON encoding used
// for Desktop agreement documents. It owns bytes, not document schemas, digest
// exclusions, authenticity, or authorization. It depends only on the standard
// library and reports errors without copying document content into diagnostics.
package canonicaljson

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"slices"
	"strconv"
	"unicode/utf8"
)

const (
	// MaxDepth bounds recursive containers independently of the caller's byte limit.
	MaxDepth = 64
	// MaxSafeInteger is the largest exactly representable JavaScript safe integer.
	MaxSafeInteger int64 = 1<<53 - 1
)

var (
	ErrInvalid = errors.New("canonical JSON document is invalid")
	ErrLimit   = errors.New("canonical JSON document exceeds its limit")
)

// Canonicalize accepts one JSON value within maxBytes and emits recursively
// sorted ASCII object keys, preserved array order, decimal safe integers, and
// JSON.stringify string escaping. Duplicate keys, non-scalar strings, invalid
// UTF-8, fractional/exponent numbers, and excessive nesting fail before decoding
// can erase their original representation. Document owners enforce closed fields,
// required values, and narrower bounds such as positive or nonnegative integers.
func Canonicalize(document []byte, maxBytes int) ([]byte, error) {
	if maxBytes <= 0 || len(document) > maxBytes {
		return nil, ErrLimit
	}
	if len(document) == 0 || !utf8.Valid(document) || !validStringEscapes(document) {
		return nil, ErrInvalid
	}
	decoder := json.NewDecoder(bytes.NewReader(document))
	decoder.UseNumber()
	encoded, err := readValue(decoder, 0)
	if err != nil {
		return nil, err
	}
	if _, err = decoder.Token(); !errors.Is(err, io.EOF) {
		return nil, ErrInvalid
	}
	if len(encoded) > maxBytes {
		return nil, ErrLimit
	}
	return encoded, nil
}

func readValue(decoder *json.Decoder, depth int) ([]byte, error) {
	token, err := decoder.Token()
	if err != nil {
		return nil, ErrInvalid
	}
	switch value := token.(type) {
	case nil:
		return []byte("null"), nil
	case bool:
		return strconv.AppendBool(nil, value), nil
	case string:
		return appendString(nil, value), nil
	case json.Number:
		integer, parseErr := strconv.ParseInt(string(value), 10, 64)
		if parseErr != nil || integer < -MaxSafeInteger || integer > MaxSafeInteger {
			return nil, ErrInvalid
		}
		return strconv.AppendInt(nil, integer, 10), nil
	case json.Delim:
		if depth >= MaxDepth {
			return nil, ErrLimit
		}
		switch value {
		case '{':
			return readObject(decoder, depth+1)
		case '[':
			return readArray(decoder, depth+1)
		}
	}
	return nil, ErrInvalid
}

func readObject(decoder *json.Decoder, depth int) ([]byte, error) {
	values := make(map[string][]byte)
	keys := []string{}
	for decoder.More() {
		token, err := decoder.Token()
		if err != nil {
			return nil, ErrInvalid
		}
		key, ok := token.(string)
		if !ok || !ascii(key) {
			return nil, ErrInvalid
		}
		if _, exists := values[key]; exists {
			return nil, ErrInvalid
		}
		value, err := readValue(decoder, depth)
		if err != nil {
			return nil, err
		}
		keys = append(keys, key)
		values[key] = value
	}
	if token, err := decoder.Token(); err != nil || token != json.Delim('}') {
		return nil, ErrInvalid
	}
	slices.Sort(keys)
	encoded := []byte{'{'}
	for index, key := range keys {
		if index > 0 {
			encoded = append(encoded, ',')
		}
		encoded = appendString(encoded, key)
		encoded = append(encoded, ':')
		encoded = append(encoded, values[key]...)
	}
	return append(encoded, '}'), nil
}

func readArray(decoder *json.Decoder, depth int) ([]byte, error) {
	encoded := []byte{'['}
	for decoder.More() {
		value, err := readValue(decoder, depth)
		if err != nil {
			return nil, err
		}
		if len(encoded) > 1 {
			encoded = append(encoded, ',')
		}
		encoded = append(encoded, value...)
	}
	if token, err := decoder.Token(); err != nil || token != json.Delim(']') {
		return nil, ErrInvalid
	}
	return append(encoded, ']'), nil
}

func ascii(value string) bool {
	for index := range len(value) {
		if value[index] >= utf8.RuneSelf {
			return false
		}
	}
	return true
}

// appendString preserves all already-validated scalar UTF-8, including U+2028
// and U+2029. strconv.AppendQuote and encoding/json's string encoder both escape
// scalars that JSON.stringify preserves, so neither supplies these wire bytes.
func appendString(encoded []byte, value string) []byte {
	const hex = "0123456789abcdef"
	encoded = append(encoded, '"')
	for index := range len(value) {
		character := value[index]
		switch character {
		case '"', '\\':
			encoded = append(encoded, '\\', character)
		case '\b':
			encoded = append(encoded, '\\', 'b')
		case '\f':
			encoded = append(encoded, '\\', 'f')
		case '\n':
			encoded = append(encoded, '\\', 'n')
		case '\r':
			encoded = append(encoded, '\\', 'r')
		case '\t':
			encoded = append(encoded, '\\', 't')
		default:
			if character < 0x20 {
				encoded = append(encoded, '\\', 'u', '0', '0', hex[character>>4], hex[character&15])
			} else {
				encoded = append(encoded, character)
			}
		}
	}
	return append(encoded, '"')
}

// encoding/json substitutes U+FFFD for unpaired surrogate escapes. Validate
// their original spelling before asking the standard decoder for string values.
// Grammar, quote termination, and all other escapes remain decoder-owned.
func validStringEscapes(document []byte) bool {
	inside := false
	for index := 0; index < len(document); index++ {
		switch document[index] {
		case '"':
			inside = !inside
		case '\\':
			if !inside {
				continue
			}
			index++
			if index >= len(document) {
				return false
			}
			if document[index] != 'u' {
				continue
			}
			unit, ok := unicodeEscape(document[index+1:])
			if !ok || unit >= 0xdc00 && unit <= 0xdfff {
				return false
			}
			index += 4
			if unit < 0xd800 || unit > 0xdbff {
				continue
			}
			if len(document)-index < 7 || document[index+1] != '\\' || document[index+2] != 'u' {
				return false
			}
			low, ok := unicodeEscape(document[index+3:])
			if !ok || low < 0xdc00 || low > 0xdfff {
				return false
			}
			index += 6
		}
	}
	return true
}

func unicodeEscape(data []byte) (uint16, bool) {
	if len(data) < 4 {
		return 0, false
	}
	var value uint16
	for _, character := range data[:4] {
		value <<= 4
		switch {
		case character >= '0' && character <= '9':
			value |= uint16(character - '0')
		case character >= 'a' && character <= 'f':
			value |= uint16(character-'a') + 10
		case character >= 'A' && character <= 'F':
			value |= uint16(character-'A') + 10
		default:
			return 0, false
		}
	}
	return value, true
}
