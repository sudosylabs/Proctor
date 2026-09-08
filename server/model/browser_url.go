// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package model

import (
	"errors"
	"net"
	"net/url"
	"strconv"
	"strings"
	"unicode/utf8"
)

const (
	BrowserPolicyOriginMaximumBytes = 2048
	BrowserPolicyPathMaximumBytes   = 2048
	// Navigation input uses JavaScript string length (UTF-16 code units).
	BrowserNavigationMaximumCharacters = 8192
	// A UTF-16 code unit contributes at most three UTF-8 bytes, each of which
	// may require a three-byte percent triplet in the retained pathname.
	browserLocationMaximumPathBytes = 9 * BrowserNavigationMaximumCharacters
)

func CanonicalizeBrowserPolicyOrigin(value string) (string, error) {
	return canonicalizeBrowserPolicyRuleOrigin(value, false)
}

func canonicalizeBrowserPolicyRuleOrigin(value string, httpException bool) (string, error) {
	if len(value) > BrowserPolicyOriginMaximumBytes || !browserASCII(value) || invalidBrowserURLText(value) {
		return "", errors.New("origin contains forbidden characters or exceeds its limit")
	}
	parsed, err := url.Parse(value)
	scheme, port := "https", "443"
	if httpException {
		scheme, port = "http", "80"
	}
	if err != nil || parsed.Scheme != scheme || parsed.Host == "" || parsed.User != nil || strings.ContainsAny(value, "?#") || (parsed.Path != "" && parsed.Path != "/") || parsed.RawPath != "" {
		return "", errors.New("origin does not match its explicit network scheme")
	}
	host, port, err := canonicalBrowserHostPortWithDefault(parsed, port)
	if err != nil {
		return "", err
	}
	return scheme + "://" + browserHostPort(host, port), nil
}

// CanonicalizeBrowserPolicyPath accepts a prefix the browser can preserve
// exactly. Authoring only removes one non-root trailing slash; it never rewrites
// dot segments, repeated internal slashes or percent-encoded unreserved bytes.
func CanonicalizeBrowserPolicyPath(value string) (string, error) {
	if len(value) > BrowserPolicyPathMaximumBytes || !browserASCII(value) || value == "" ||
		value[0] != '/' || strings.HasPrefix(value, "//") || invalidBrowserURLText(value) || strings.ContainsAny(value, "?#") {
		return "", errors.New("path prefix must be a bounded ASCII absolute path without query or fragment")
	}
	canonical, err := canonicalBrowserPath(value)
	if err != nil || canonical != value {
		return "", errors.New("path prefix requires forbidden normalization")
	}
	for _, segment := range strings.Split(value, "/") {
		if browserDotSegment(segment) != 0 {
			return "", errors.New("path prefix contains a dot segment")
		}
	}
	if value != "/" {
		value = strings.TrimSuffix(value, "/")
	}
	// Removing another slash would change a significant empty path segment.
	if value != "/" && strings.HasSuffix(value, "/") {
		return "", errors.New("path prefix has multiple trailing slashes")
	}
	return value, nil
}

func CanonicalizeBrowserLocation(value string) (BrowserLocation, error) {
	if invalidBrowserURLText(value) || browserStringLength(value) > BrowserNavigationMaximumCharacters {
		return BrowserLocation{}, errors.New("URL contains forbidden characters or exceeds its limit")
	}
	parsed, err := url.Parse(value)
	if err != nil || (parsed.Scheme != "https" && parsed.Scheme != "http") || parsed.Host == "" || parsed.User != nil {
		return BrowserLocation{}, errors.New("URL must be absolute HTTP or HTTPS")
	}
	defaultPort := "443"
	if parsed.Scheme == "http" {
		defaultPort = "80"
	}
	host, port, err := canonicalBrowserHostPortWithDefault(parsed, defaultPort)
	if err != nil {
		return BrowserLocation{}, err
	}
	// Read the original pathname: net/url's escaping and path.Clean have
	// different semantics from the browser's serialized pathname.
	rest := value[strings.Index(value, "://")+3:]
	rawPath := "/"
	if index := strings.IndexAny(rest, "/?#"); index >= 0 && rest[index] == '/' {
		rawPath = rest[index:]
		if end := strings.IndexAny(rawPath, "?#"); end >= 0 {
			rawPath = rawPath[:end]
		}
	}
	urlPath, err := canonicalBrowserPath(rawPath)
	if err != nil {
		return BrowserLocation{}, err
	}
	return BrowserLocation{Scheme: parsed.Scheme, Host: host, Port: port, Path: urlPath}, nil
}

func canonicalBrowserHostPort(parsed *url.URL) (string, string, error) {
	return canonicalBrowserHostPortWithDefault(parsed, "443")
}

func canonicalBrowserHostPortWithDefault(parsed *url.URL, defaultPort string) (string, string, error) {
	host := parsed.Hostname()
	if host == "" || len(host) > 253 || !browserASCII(host) || strings.HasSuffix(host, ".") ||
		strings.ContainsAny(parsed.Host, "[]") || strings.HasSuffix(parsed.Host, ":") {
		return "", "", errors.New("host is unsupported or invalid")
	}
	host = strings.ToLower(host)
	for _, label := range strings.Split(host, ".") {
		if len(label) < 1 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' || strings.HasPrefix(label, "xn--") {
			return "", "", errors.New("host label is unsupported or invalid")
		}
		for _, character := range []byte(label) {
			if !(character >= 'a' && character <= 'z' || character >= '0' && character <= '9' || character == '-') {
				return "", "", errors.New("host label is invalid")
			}
		}
	}
	if browserNumericHost(host) {
		var err error
		host, err = canonicalBrowserIPv4(host)
		if err != nil {
			return "", "", err
		}
	}
	port := parsed.Port()
	if port != "" {
		number, err := strconv.Atoi(port)
		if err != nil || number < 1 || number > 65535 || strconv.Itoa(number) != port {
			return "", "", errors.New("port is invalid")
		}
		if port == defaultPort {
			port = ""
		}
	}
	return host, port, nil
}

func browserHostPort(host, port string) string {
	if port != "" {
		return host + ":" + port
	}
	return host
}

func invalidBrowserURLText(value string) bool {
	if value == "" || !utf8.ValidString(value) || strings.ContainsRune(value, '\\') {
		return true
	}
	for _, character := range value {
		if character < 0x20 || character == 0x7f {
			return true
		}
	}
	return false
}

func browserASCII(value string) bool {
	for index := range len(value) {
		if value[index] >= utf8.RuneSelf {
			return false
		}
	}
	return true
}

func browserStringLength(value string) int {
	length := 0
	for _, character := range value {
		length++
		if character > 0xffff {
			length++
		}
	}
	return length
}

// canonicalBrowserPath preserves empty segments and percent-triplet case.
// Only browser-recognized dot segments and browser path escaping can change
// navigation bytes; encoded separators and malformed escapes always fail.
func canonicalBrowserPath(value string) (string, error) {
	if value == "" || value[0] != '/' || invalidBrowserURLText(value) || strings.ContainsAny(value, "?#") {
		return "", errors.New("invalid browser pathname")
	}
	for index := 0; index < len(value); index++ {
		if value[index] != '%' {
			continue
		}
		if index+2 >= len(value) {
			return "", errors.New("malformed percent encoding")
		}
		high, low := fromHex(value[index+1]), fromHex(value[index+2])
		if high < 0 || low < 0 {
			return "", errors.New("malformed percent encoding")
		}
		decoded := byte(high<<4 | low)
		if decoded == '/' || decoded == '\\' {
			return "", errors.New("encoded path separator is forbidden")
		}
		index += 2
	}
	segments := strings.Split(value[1:], "/")
	selected := make([]string, 0, len(segments))
	for index, segment := range segments {
		dot := browserDotSegment(segment)
		if dot == 2 && len(selected) > 0 {
			selected = selected[:len(selected)-1]
		}
		if dot == 0 {
			selected = append(selected, segment)
		} else if index == len(segments)-1 {
			selected = append(selected, "")
		}
	}
	raw := "/" + strings.Join(selected, "/")
	var encoded strings.Builder
	for index := range len(raw) {
		character := raw[index]
		if character <= 0x20 || character > 0x7e || strings.ContainsRune("\"<>`{}", rune(character)) {
			const hex = "0123456789ABCDEF"
			encoded.WriteByte('%')
			encoded.WriteByte(hex[character>>4])
			encoded.WriteByte(hex[character&15])
		} else {
			encoded.WriteByte(character)
		}
	}
	return encoded.String(), nil
}

func browserDotSegment(value string) int {
	switch strings.ToLower(value) {
	case ".", "%2e":
		return 1
	case "..", ".%2e", "%2e.", "%2e%2e":
		return 2
	default:
		return 0
	}
}

// canonicalBrowserIPv4 implements the IPv4-number grammar used by WHATWG URL
// hosts so the server and the embedded browser agree on non-dotted-decimal
// spellings before policy matching.
func canonicalBrowserIPv4(host string) (string, error) {
	parts := strings.Split(host, ".")
	if len(parts) > 1 && parts[len(parts)-1] == "" {
		parts = parts[:len(parts)-1]
	}
	if len(parts) < 1 || len(parts) > 4 {
		return "", errors.New("numeric host is invalid")
	}
	numbers := make([]uint64, len(parts))
	for index, part := range parts {
		if part == "" {
			return "", errors.New("numeric host is invalid")
		}
		radix, digits := 10, part
		if len(digits) >= 2 && (strings.HasPrefix(digits, "0x") || strings.HasPrefix(digits, "0X")) {
			radix, digits = 16, digits[2:]
		} else if len(digits) >= 2 && digits[0] == '0' {
			radix, digits = 8, digits[1:]
		}
		if digits == "" {
			digits = "0"
		}
		number, err := strconv.ParseUint(digits, radix, 32)
		if err != nil {
			return "", errors.New("numeric host is invalid")
		}
		numbers[index] = number
	}
	for _, number := range numbers[:len(numbers)-1] {
		if number > 255 {
			return "", errors.New("numeric host is invalid")
		}
	}
	lastLimit := uint64(1) << (8 * (5 - len(numbers)))
	if numbers[len(numbers)-1] >= lastLimit {
		return "", errors.New("numeric host is invalid")
	}
	address := numbers[len(numbers)-1]
	for index, number := range numbers[:len(numbers)-1] {
		address += number << (8 * (3 - index))
	}
	return net.IPv4(byte(address>>24), byte(address>>16), byte(address>>8), byte(address)).String(), nil
}

func browserNumericHost(host string) bool {
	host = strings.TrimSuffix(host, ".")
	last := host
	if index := strings.LastIndexByte(host, '.'); index >= 0 {
		last = host[index+1:]
	}
	if last == "" {
		return false
	}
	if strings.HasPrefix(last, "0x") || strings.HasPrefix(last, "0X") {
		for _, character := range last[2:] {
			if !strings.ContainsRune("0123456789abcdefABCDEF", character) {
				return false
			}
		}
		return true
	}
	for _, character := range last {
		if character < '0' || character > '9' {
			return false
		}
	}
	return true
}

func fromHex(value byte) int {
	switch {
	case value >= '0' && value <= '9':
		return int(value - '0')
	case value >= 'a' && value <= 'f':
		return int(value-'a') + 10
	case value >= 'A' && value <= 'F':
		return int(value-'A') + 10
	default:
		return -1
	}
}
