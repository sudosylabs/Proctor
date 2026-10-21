// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package autocert

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/acme"
)

func TestManagerCloseCancelsLifecycleAndIsIdempotent(t *testing.T) {
	t.Parallel()

	manager := &Manager{}
	lifecycle, err := manager.lifecycleContext()
	require.NoError(t, err)
	require.NoError(t, manager.Close())
	select {
	case <-lifecycle.Done():
	default:
		require.FailNow(t, "manager lifecycle remains active after Close")
	}
	require.NoError(t, manager.Close())
	_, err = manager.lifecycleContext()
	require.Error(t, err, "closed manager returned an active lifecycle context")
}

func TestManagerCloseStopsScheduledRenewal(t *testing.T) {
	t.Parallel()

	manager := &Manager{}
	now := time.Now()
	key := certKey{domain: "proctor.example.edu"}
	manager.startRenew(key, nil, now, now.Add(90*24*time.Hour))

	manager.renewalMu.Lock()
	renewal := manager.renewal[key]
	manager.renewalMu.Unlock()
	require.NotNil(t, renewal, "renewal was not scheduled")
	require.NoError(t, manager.Close())
	manager.renewalMu.Lock()
	remaining := len(manager.renewal)
	manager.renewalMu.Unlock()
	require.Equal(t, 0, remaining, "renewal entries after Close")
	renewal.timerMu.Lock()
	timer := renewal.timer
	renewal.timerMu.Unlock()
	require.Nil(t, timer, "renewal timer remains active after Close")
}

func TestManagerCloseDoesNotHoldRenewalMapWhileWaitingForTimer(t *testing.T) {
	t.Parallel()

	manager := &Manager{}
	now := time.Now()
	key := certKey{domain: "proctor.example.edu"}
	manager.startRenew(key, nil, now, now.Add(90*24*time.Hour))
	manager.renewalMu.Lock()
	renewal := manager.renewal[key]
	manager.renewalMu.Unlock()
	renewal.timerMu.Lock()

	closed := make(chan error, 1)
	go func() { closed <- manager.Close() }()
	deadline := time.Now().Add(time.Second)
	for {
		if manager.renewalMu.TryLock() {
			detached := manager.renewal == nil
			manager.renewalMu.Unlock()
			if detached {
				break
			}
		}
		if time.Now().After(deadline) {
			renewal.timerMu.Unlock()
			require.FailNow(t, "Close held renewalMu while waiting for a renewal timer")
		}
	}
	select {
	case err := <-closed:
		renewal.timerMu.Unlock()
		require.FailNow(t, fmt.Sprintf("Close returned while renewal timer was locked: %v", err))
	default:
	}
	renewal.timerMu.Unlock()
	require.NoError(t, (<-closed))
}

func TestManagerCloseCancelsAndWaitsForInFlightCertificateIssuance(t *testing.T) {
	t.Parallel()

	cache := newCancellationBlockingCache()
	manager := &Manager{
		Cache:      cache,
		Prompt:     AcceptTOS,
		HostPolicy: HostWhitelist("proctor.example.edu"),
	}
	serverConnection, clientConnection := net.Pipe()
	t.Cleanup(func() {
		_ = serverConnection.Close()
		_ = clientConnection.Close()
	})
	serverTLS := tls.Server(serverConnection, manager.TLSConfig())
	clientTLS := tls.Client(clientConnection, &tls.Config{
		MinVersion:         tls.VersionTLS12,
		ServerName:         "proctor.example.edu",
		InsecureSkipVerify: true, // Issuance is deliberately canceled before a certificate exists.
	})
	serverHandshake := make(chan error, 1)
	clientHandshake := make(chan error, 1)
	go func() { serverHandshake <- serverTLS.Handshake() }()
	go func() { clientHandshake <- clientTLS.Handshake() }()
	<-cache.started

	closed := make(chan error, 1)
	go func() { closed <- manager.Close() }()
	<-cache.canceled
	assertCloseWaits(t, closed)
	close(cache.release)
	require.NoError(t, (<-closed))
	require.Error(t, (<-serverHandshake), "server TLS handshake succeeded after issuance cancellation")
	require.Error(t, (<-clientHandshake), "client TLS handshake succeeded after issuance cancellation")
}

func TestManagerCloseCancelsAndWaitsForInFlightRenewal(t *testing.T) {
	t.Parallel()

	cache := newCancellationBlockingCache()
	manager := &Manager{Cache: cache}
	now := time.Now()
	manager.startRenew(certKey{domain: "proctor.example.edu"}, nil, now.Add(-90*24*time.Hour), now)
	<-cache.started

	closed := make(chan error, 1)
	go func() { closed <- manager.Close() }()
	<-cache.canceled
	assertCloseWaits(t, closed)
	close(cache.release)
	require.NoError(t, (<-closed))
}

func TestManagerCloseCancelsFailedIssuanceRetry(t *testing.T) {
	t.Parallel()

	backgroundStarted := make(chan struct{})
	stateRemoved := make(chan struct{}, 1)
	manager := &Manager{
		Client:             &acme.Client{DirectoryURL: "://invalid"},
		retryAfter:         time.Hour,
		didStartBackground: func() { close(backgroundStarted) },
		didRemoveState:     func(certKey) { stateRemoved <- struct{}{} },
	}
	_, err := manager.createCert(context.Background(), certKey{domain: "proctor.example.edu"})
	require.Error(t, err, "certificate creation unexpectedly succeeded")
	<-backgroundStarted
	require.NoError(t, manager.Close())
	select {
	case <-stateRemoved:
		require.FailNow(t, "failed-issuance retry ran after manager shutdown")
	default:
	}
}

func TestManagerCloseCancelsAndWaitsForAuthorizationCleanup(t *testing.T) {
	t.Parallel()

	roundTripper := newCancellationBlockingRoundTripper()
	manager := &Manager{}
	manager.client = &acme.Client{
		DirectoryURL: "https://ca.invalid/directory",
		HTTPClient:   &http.Client{Transport: roundTripper},
	}
	manager.runBackground(func(ctx context.Context) {
		manager.deactivatePendingAuthz(ctx, []string{"https://ca.invalid/authorization/1"})
	})
	<-roundTripper.started
	closed := make(chan error, 1)
	go func() { closed <- manager.Close() }()
	<-roundTripper.canceled
	assertCloseWaits(t, closed)
	close(roundTripper.release)
	require.ErrorIs(t, (<-closed), context.Canceled)
	require.ErrorIs(t, manager.Close(), context.Canceled)
}

func TestChallengeCleanupSurvivesIssuanceCancellation(t *testing.T) {
	t.Parallel()

	accountKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	client := &acme.Client{Key: accountKey}
	for _, challengeType := range []string{"http-01", "tls-alpn-01"} {
		t.Run(challengeType, func(t *testing.T) {
			t.Parallel()

			cacheDirectory := t.TempDir()
			manager := &Manager{Cache: DirCache(cacheDirectory)}
			ctx, cancel := context.WithCancel(context.Background())
			cleanup, err := manager.fulfill(ctx, client, &acme.Challenge{
				Type:  challengeType,
				Token: "test-token",
			}, "proctor.example.edu")
			require.NoError(t, err)
			entries, err := os.ReadDir(cacheDirectory)
			require.NoError(t, err)
			require.Len(t, entries, 1, "challenge cache entries before cleanup")
			cancel()
			require.NoError(t, cleanup())
			entries, err = os.ReadDir(cacheDirectory)
			require.NoError(t, err)
			require.Empty(t, entries, "challenge cache entries after canceled cleanup")
			manager.challengeMu.RLock()
			remainingCertificates := len(manager.certTokens)
			remainingHTTP := len(manager.httpTokens)
			manager.challengeMu.RUnlock()
			require.Equal(t, 0, remainingCertificates, "in-memory challenge state remains: certs")
			require.Equal(t, 0, remainingHTTP, "in-memory challenge state remains: certs")
		})
	}
}

func TestChallengeCleanupRetainsPersistentDeletionFailures(t *testing.T) {
	t.Parallel()

	accountKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	client := &acme.Client{Key: accountKey}
	deleteErr := errors.New("cache deletion failed")
	for _, challengeType := range []string{"http-01", "tls-alpn-01"} {
		t.Run(challengeType, func(t *testing.T) {
			t.Parallel()

			manager := &Manager{Cache: failingDeleteCache{err: deleteErr}}
			cleanup, err := manager.fulfill(context.Background(), client, &acme.Challenge{
				Type:  challengeType,
				Token: "test-token",
			}, "proctor.example.edu")
			require.NoError(t, err)
			require.ErrorIs(t, cleanup(), deleteErr)
			require.ErrorIs(t, manager.Close(), deleteErr)
			require.ErrorIs(t, manager.Close(), deleteErr)
		})
	}
}

func assertCloseWaits(t *testing.T, closed <-chan error) {
	t.Helper()
	select {
	case err := <-closed:
		require.FailNow(t, fmt.Sprintf("Close returned before owned work stopped: %v", err))
	case <-time.After(50 * time.Millisecond):
	}
}

type cancellationBlockingCache struct {
	started      chan struct{}
	canceled     chan struct{}
	release      chan struct{}
	startOnce    sync.Once
	canceledOnce sync.Once
}

func newCancellationBlockingCache() *cancellationBlockingCache {
	return &cancellationBlockingCache{
		started:  make(chan struct{}),
		canceled: make(chan struct{}),
		release:  make(chan struct{}),
	}
}

func (c *cancellationBlockingCache) Get(ctx context.Context, _ string) ([]byte, error) {
	c.startOnce.Do(func() { close(c.started) })
	<-ctx.Done()
	c.canceledOnce.Do(func() { close(c.canceled) })
	<-c.release
	return nil, ctx.Err()
}

func (*cancellationBlockingCache) Put(context.Context, string, []byte) error { return nil }
func (*cancellationBlockingCache) Delete(context.Context, string) error      { return nil }

type cancellationBlockingRoundTripper struct {
	started      chan struct{}
	canceled     chan struct{}
	release      chan struct{}
	startOnce    sync.Once
	canceledOnce sync.Once
}

type failingDeleteCache struct {
	err error
}

func (failingDeleteCache) Get(context.Context, string) ([]byte, error) { return nil, ErrCacheMiss }
func (failingDeleteCache) Put(context.Context, string, []byte) error   { return nil }
func (c failingDeleteCache) Delete(context.Context, string) error      { return c.err }

func newCancellationBlockingRoundTripper() *cancellationBlockingRoundTripper {
	return &cancellationBlockingRoundTripper{
		started:  make(chan struct{}),
		canceled: make(chan struct{}),
		release:  make(chan struct{}),
	}
}

func (r *cancellationBlockingRoundTripper) RoundTrip(request *http.Request) (*http.Response, error) {
	r.startOnce.Do(func() { close(r.started) })
	<-request.Context().Done()
	r.canceledOnce.Do(func() { close(r.canceled) })
	<-r.release
	return nil, request.Context().Err()
}

var _ Cache = (*cancellationBlockingCache)(nil)
var _ Cache = failingDeleteCache{}
var _ http.RoundTripper = (*cancellationBlockingRoundTripper)(nil)
