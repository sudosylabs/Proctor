// ---------------------------------------------------------------------------------------------
// Copyright 2016 The Go Authors. All rights reserved.
// Modifications Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.
// SPDX-License-Identifier: BSD-3-Clause
// ---------------------------------------------------------------------------------------------

package autocert

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/acme"
)

// TestGetCertificate_concurrent guards against a data race on certState
// fields between the goroutine performing the ACME work for a domain and
// the other goroutines waiting on the result. See golang/go#80119. Run it
// with -race; before the fix two callers could both believe they owned the
// state and the second Unlock was a fatal, unrecoverable runtime error.
func TestGetCertificate_concurrent(t *testing.T) {
	t.Parallel()

	// A failing transport makes the owner goroutine fail quickly inside
	// createCert, so its writes overlap with the non-owner goroutines
	// reaching the same code path, without any network access.
	manager := &Manager{
		Prompt: AcceptTOS,
		Client: &acme.Client{
			DirectoryURL: "https://acme.invalid/directory",
			HTTPClient:   &http.Client{Transport: failingRoundTripper{}},
		},
	}
	t.Cleanup(func() { require.NoError(t, manager.Close()) })

	key := certKey{domain: "proctor.example.edu"}
	start := make(chan struct{})
	var wg sync.WaitGroup
	for range 64 {
		wg.Go(func() {
			<-start
			_, err := manager.createCert(context.Background(), key)
			assert.Error(t, err)
		})
	}
	close(start)
	wg.Wait()
}

type failingRoundTripper struct{}

func (failingRoundTripper) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, errors.New("ACME directory unavailable")
}

var _ http.RoundTripper = failingRoundTripper{}
