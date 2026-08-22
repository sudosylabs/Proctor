// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package autocert

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"errors"
	"net"
	"net/http"
	"os"
	"sync"
	"testing"
	"time"

	"golang.org/x/crypto/acme"
)

func TestManagerCloseCancelsLifecycleAndIsIdempotent(t *testing.T) {
	t.Parallel()

	manager := &Manager{}
	lifecycle, err := manager.lifecycleContext()
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-lifecycle.Done():
	default:
		t.Fatal("manager lifecycle remains active after Close")
	}
	if err := manager.Close(); err != nil {
		t.Fatalf("second Close() error = %v", err)
	}
	if _, err := manager.lifecycleContext(); err == nil {
		t.Fatal("closed manager returned an active lifecycle context")
	}
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
	if renewal == nil {
		t.Fatal("renewal was not scheduled")
	}
	if err := manager.Close(); err != nil {
		t.Fatal(err)
	}
	manager.renewalMu.Lock()
	remaining := len(manager.renewal)
	manager.renewalMu.Unlock()
	if remaining != 0 {
		t.Fatalf("renewal entries after Close = %d", remaining)
	}
	renewal.timerMu.Lock()
	timer := renewal.timer
	renewal.timerMu.Unlock()
	if timer != nil {
		t.Fatal("renewal timer remains active after Close")
	}
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
			t.Fatal("Close held renewalMu while waiting for a renewal timer")
		}
	}
	select {
	case err := <-closed:
		renewal.timerMu.Unlock()
		t.Fatalf("Close returned while renewal timer was locked: %v", err)
	default:
	}
	renewal.timerMu.Unlock()
	if err := <-closed; err != nil {
		t.Fatal(err)
	}
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
	if err := <-closed; err != nil {
		t.Fatal(err)
	}
	if err := <-serverHandshake; err == nil {
		t.Fatal("server TLS handshake succeeded after issuance cancellation")
	}
	if err := <-clientHandshake; err == nil {
		t.Fatal("client TLS handshake succeeded after issuance cancellation")
	}
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
	if err := <-closed; err != nil {
		t.Fatal(err)
	}
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
	if _, err := manager.createCert(context.Background(), certKey{domain: "proctor.example.edu"}); err == nil {
		t.Fatal("certificate creation unexpectedly succeeded")
	}
	<-backgroundStarted
	if err := manager.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-stateRemoved:
		t.Fatal("failed-issuance retry ran after manager shutdown")
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
	if err := <-closed; !errors.Is(err, context.Canceled) {
		t.Fatalf("first Close() error = %v", err)
	}
	if err := manager.Close(); !errors.Is(err, context.Canceled) {
		t.Fatalf("second Close() error = %v", err)
	}
}

func TestChallengeCleanupSurvivesIssuanceCancellation(t *testing.T) {
	t.Parallel()

	accountKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
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
			if err != nil {
				t.Fatal(err)
			}
			entries, err := os.ReadDir(cacheDirectory)
			if err != nil {
				t.Fatal(err)
			}
			if len(entries) != 1 {
				t.Fatalf("challenge cache entries before cleanup = %d, want 1", len(entries))
			}
			cancel()
			if err := cleanup(); err != nil {
				t.Fatal(err)
			}
			entries, err = os.ReadDir(cacheDirectory)
			if err != nil {
				t.Fatal(err)
			}
			if len(entries) != 0 {
				t.Fatalf("challenge cache entries after canceled cleanup = %d, want 0", len(entries))
			}
			manager.challengeMu.RLock()
			remainingCertificates := len(manager.certTokens)
			remainingHTTP := len(manager.httpTokens)
			manager.challengeMu.RUnlock()
			if remainingCertificates != 0 || remainingHTTP != 0 {
				t.Fatalf("in-memory challenge state remains: certs=%d HTTP=%d", remainingCertificates, remainingHTTP)
			}
		})
	}
}

func TestChallengeCleanupRetainsPersistentDeletionFailures(t *testing.T) {
	t.Parallel()

	accountKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
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
			if err != nil {
				t.Fatal(err)
			}
			if err := cleanup(); !errors.Is(err, deleteErr) {
				t.Fatalf("cleanup error = %v", err)
			}
			if err := manager.Close(); !errors.Is(err, deleteErr) {
				t.Fatalf("first Close() error = %v", err)
			}
			if err := manager.Close(); !errors.Is(err, deleteErr) {
				t.Fatalf("second Close() error = %v", err)
			}
		})
	}
}

func assertCloseWaits(t *testing.T, closed <-chan error) {
	t.Helper()
	select {
	case err := <-closed:
		t.Fatalf("Close returned before owned work stopped: %v", err)
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
