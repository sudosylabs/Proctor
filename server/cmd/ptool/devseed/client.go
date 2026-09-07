// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package devseed

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net"
	"net/http"
	"net/textproto"
	"net/url"
	"strconv"
	"strings"
	"time"
)

type object map[string]any

func decode(raw json.RawMessage) object {
	var value object
	_ = json.Unmarshal(raw, &value)
	return value
}

func str(value object, key string) string    { text, _ := value[key].(string); return text }
func number(value object, key string) int64  { n, _ := value[key].(float64); return int64(n) }
func nested(value object, key string) object { obj, _ := value[key].(map[string]any); return obj }

func loopbackOrigin(raw string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "http" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Opaque != "" || (u.Path != "" && u.Path != "/") || u.RawPath != "" {
		return "", errors.New("seed requires an HTTP loopback origin with an explicit port")
	}
	host := u.Hostname()
	if host != "localhost" {
		ip := net.ParseIP(host)
		if ip == nil || !ip.IsLoopback() {
			return "", errors.New("seed refuses non-loopback origins")
		}
	}
	port, err := strconv.Atoi(u.Port())
	if err != nil || port < 1 || port > 65535 {
		return "", errors.New("seed requires an explicit valid port")
	}
	return "http://" + net.JoinHostPort(host, strconv.Itoa(port)), nil
}

func localHTTPClient() *http.Client {
	dialer := &net.Dialer{Timeout: 5 * time.Second}
	return &http.Client{
		Timeout:       30 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		Transport: &http.Transport{Proxy: nil, DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			host, port, err := net.SplitHostPort(address)
			if err != nil {
				return nil, err
			}
			if host == "localhost" {
				host = "127.0.0.1"
			}
			ip := net.ParseIP(host)
			if ip == nil || !ip.IsLoopback() {
				return nil, errors.New("non-loopback dial refused")
			}
			return dialer.DialContext(ctx, network, net.JoinHostPort(host, port))
		}},
	}
}

type responseError struct{ Status int }

func (e *responseError) Error() string {
	if e.Status == http.StatusTooManyRequests {
		return "server returned HTTP 429; use the generated development rate-limit configuration or wait for the limit to expire, then rerun"
	}
	return fmt.Sprintf("server returned HTTP %d (response body withheld)", e.Status)
}

func (r *runner) request(origin, method, path, token, key string, body json.RawMessage, content []byte) (json.RawMessage, http.Header, error) {
	return r.requestWithMatch(origin, method, path, token, key, body, content, "")
}

func (r *runner) requestWithMatch(origin, method, path, token, key string, body json.RawMessage, content []byte, ifMatch string) (json.RawMessage, http.Header, error) {
	// Retain aliases within this run: collection readers may hold the previous
	// token while a long fixture crosses the ordinary access-token deadline.
	var owner *account
	if token != "" && origin == r.state.Server {
		owner = r.tokens[token]
		if owner == nil {
			for _, a := range r.state.Accounts {
				if a.Token == token {
					owner = a
					if r.tokens == nil {
						r.tokens = map[string]*account{}
					}
					r.tokens[token] = a
					break
				}
			}
		}
		if owner != nil {
			token = owner.Token
		}
	}
	raw, headers, err := r.send(origin, method, path, token, key, body, content, ifMatch)
	var failed *responseError
	if owner == nil || path == "/api/v1/users/me" || !errors.As(err, &failed) || failed.Status != http.StatusUnauthorized {
		return raw, headers, err
	}
	if err = r.login(owner); err != nil {
		return nil, nil, err
	}
	// Retry only once, with the exact body and idempotency key already journaled.
	return r.send(origin, method, path, owner.Token, key, body, content, ifMatch)
}

func (r *runner) send(origin, method, path, token, key string, body json.RawMessage, content []byte, ifMatch string) (json.RawMessage, http.Header, error) {
	if !strings.HasPrefix(path, "/") || strings.HasPrefix(path, "//") {
		return nil, nil, errors.New("invalid API path")
	}
	if body != nil {
		var compact bytes.Buffer
		if err := json.Compact(&compact, body); err != nil {
			return nil, nil, errors.New("invalid saved JSON request")
		}
		body = compact.Bytes()
	}
	var payload io.Reader
	contentType := "application/json"
	if content != nil && body == nil {
		// Profile pictures stream source bytes; Exam uploads include JSON metadata.
		payload, contentType = bytes.NewReader(content), "image/png"
	} else if content != nil {
		var buffer bytes.Buffer
		writer := multipart.NewWriter(&buffer)
		part, err := writer.CreatePart(textproto.MIMEHeader{"Content-Disposition": {`form-data; name="metadata"`}, "Content-Type": {"application/json"}})
		if err != nil {
			return nil, nil, err
		}
		if _, err = part.Write(body); err != nil {
			return nil, nil, err
		}
		part, err = writer.CreateFormFile("content", "fixture")
		if err != nil {
			return nil, nil, err
		}
		if _, err = part.Write(content); err != nil {
			return nil, nil, err
		}
		if err = writer.Close(); err != nil {
			return nil, nil, err
		}
		payload, contentType = &buffer, writer.FormDataContentType()
	} else if body != nil {
		payload = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(r.ctx, method, origin+path, payload)
	if err != nil {
		return nil, nil, errors.New("could not construct local request")
	}
	req.Header.Set("Accept", "application/json")
	if payload != nil {
		req.Header.Set("Content-Type", contentType)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if key != "" {
		req.Header.Set("Idempotency-Key", key)
	}
	if ifMatch != "" {
		req.Header.Set("If-Match", ifMatch)
	}
	resp, err := r.client.Do(req)
	if err != nil {
		if r.ctx.Err() != nil {
			return nil, nil, r.ctx.Err()
		}
		return nil, nil, errors.New("local request failed; rerun to reconcile the saved operation")
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20+1))
	if err != nil || len(raw) > 8<<20 {
		return nil, nil, errors.New("local response unreadable or larger than 8 MiB")
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, resp.Header, &responseError{Status: resp.StatusCode}
	}
	return raw, resp.Header, nil
}

func (r *runner) get(path, token string) (json.RawMessage, error) {
	raw, _, err := r.request(r.state.Server, http.MethodGet, path, token, "", nil, nil)
	return raw, err
}

func (r *runner) list(path, token string) ([]object, error) {
	var results []object
	for page := 0; page < 100; page++ {
		raw, headers, err := r.request(r.state.Server, http.MethodGet, path, token, "", nil, nil)
		if err != nil {
			return nil, err
		}
		var items []object
		if err := json.Unmarshal(raw, &items); err != nil {
			var wrapped struct {
				Items      []object `json:"items"`
				NextCursor string   `json:"next_cursor"`
			}
			if err = json.Unmarshal(raw, &wrapped); err != nil {
				return nil, errors.New("invalid collection response")
			}
			items = wrapped.Items
			if wrapped.NextCursor != "" {
				u, _ := url.Parse(path)
				query := u.Query()
				query.Set("cursor", wrapped.NextCursor)
				u.RawQuery = query.Encode()
				path = u.String()
				results = append(results, items...)
				continue
			}
		}
		results = append(results, items...)
		next := ""
		for _, link := range strings.Split(headers.Get("Link"), ",") {
			if !strings.Contains(link, `rel="next"`) {
				continue
			}
			start, end := strings.Index(link, "<"), strings.Index(link, ">")
			if start < 0 || end <= start {
				return nil, errors.New("invalid pagination link")
			}
			u, err := url.Parse(link[start+1 : end])
			if err != nil {
				return nil, errors.New("invalid pagination link")
			}
			if u.IsAbs() && u.Scheme+"://"+u.Host != r.state.Server {
				return nil, errors.New("foreign pagination link refused")
			}
			if u.User != nil || u.Fragment != "" {
				return nil, errors.New("invalid pagination link")
			}
			next = u.RequestURI()
		}
		if next == "" {
			return results, nil
		}
		path = next
	}
	return nil, errors.New("collection exceeded 100 pages")
}
