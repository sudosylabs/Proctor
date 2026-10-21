// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package server

import (
	"bufio"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"log"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/server/config"
)

func TestHTTPSRedirectUsesCanonicalPublicAuthority(t *testing.T) {
	t.Parallel()

	handler := newHTTPSRedirectHandler("proctor.example.edu")
	request := httptest.NewRequest(http.MethodPost, "http://attacker.example/exams/a%2Fb?attempt=1", nil)
	request.Host = "attacker.example"
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)
	require.Equal(t, http.StatusPermanentRedirect, response.Code, "status")
	require.Equal(t, "https://proctor.example.edu/exams/a%2Fb?attempt=1", response.Header().Get("Location"))
}

func TestHTTPRuntimeConfiguresStaticTLS(t *testing.T) {
	t.Parallel()

	settings := config.Default().Server
	settings.PublicURL = "https://proctor.example.edu"
	settings.TLS.Mode = "static"
	settings.TLS.CertificateFile = "certificate.pem"
	settings.TLS.PrivateKeyFile = "private-key.pem"
	runtime := newStandardHTTPRuntimeForTest(t, settings)

	require.NotNil(t, runtime.primary.TLSConfig)
	require.Equal(t, uint16(tls.VersionTLS12), runtime.primary.TLSConfig.MinVersion)
	require.Nil(t, runtime.newAutomaticCertificateManager, "static TLS unexpectedly configured ACME or HTTP forwarding")
	require.Nil(t, runtime.forwarding, "static TLS unexpectedly configured ACME or HTTP forwarding")
}

func TestHTTPRuntimeServesStaticTLSWithoutNetwork(t *testing.T) {
	t.Parallel()

	certificateFile, privateKeyFile := writeTestCertificate(t)
	settings := config.Default().Server
	settings.PublicURL = "https://proctor.example.edu"
	settings.TLS.Mode = "static"
	settings.TLS.CertificateFile = certificateFile
	settings.TLS.PrivateKeyFile = privateKeyFile
	runtimeSettings := runtimeSettingsFromConfig(settings)
	runtime := newHTTPServer(httpServerSettings{
		handler: http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
			_, _ = writer.Write([]byte("secure"))
		}),
		tls:               runtimeSettings.tls,
		readHeaderTimeout: time.Second,
		readTimeout:       time.Second,
		writeTimeout:      time.Second,
		idleTimeout:       time.Second,
		maxHeaderBytes:    1024,
	})

	listener := newPipeListener()
	accepted := make(chan struct{})
	serveResult := make(chan error, 1)
	go func() {
		serveResult <- runtime.Serve(listener, func() bool {
			close(accepted)
			return true
		})
	}()
	<-accepted

	serverConnection, clientConnection := net.Pipe()
	listener.connections <- serverConnection
	client := tls.Client(clientConnection, &tls.Config{
		MinVersion:         tls.VersionTLS12,
		ServerName:         "proctor.example.edu",
		InsecureSkipVerify: true, // The generated certificate is trusted only by this test.
	})
	require.NoError(t, client.HandshakeContext(t.Context()))
	request, err := http.NewRequest(http.MethodGet, "https://proctor.example.edu/health/ready", nil)
	require.NoError(t, err)
	require.NoError(t, request.Write(client))
	response, err := http.ReadResponse(bufio.NewReader(client), request)
	require.NoError(t, err)
	_ = response.Body.Close()
	_ = client.Close()

	shutdownContext, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	require.NoError(t, runtime.Shutdown(shutdownContext))
	require.ErrorIs(t, (<-serveResult), http.ErrServerClosed)
}

func TestHTTPRuntimeRestrictsACMEAndWrapsForwarding(t *testing.T) {
	t.Parallel()

	settings := config.Default().Server
	settings.PublicURL = "https://proctor.example.edu"
	settings.TLS.Mode = "lets_encrypt"
	settings.TLS.LetsEncrypt.Email = "operator@example.edu"
	settings.TLS.LetsEncrypt.CacheDirectory = filepath.Join(t.TempDir(), "acme")
	settings.TLS.ForwardHTTPToHTTPS = true
	settings.TLS.HTTPListenAddress = ":80"
	runtime := newStandardHTTPRuntimeForTest(t, settings)

	require.NotNil(t, runtime.newAutomaticCertificateManager, "Let's Encrypt runtime is missing ACME or forwarding server")
	require.NotNil(t, runtime.forwarding, "Let's Encrypt runtime is missing ACME or forwarding server")
	manager := runtime.newAutomaticCertificateManager()
	defer func() { _ = manager.Close() }()
	tlsConfig := manager.TLSConfig()
	require.Equal(t, uint16(tls.VersionTLS12), tlsConfig.MinVersion)
	require.Contains(t, tlsConfig.NextProtos, "h2")
	require.Contains(t, tlsConfig.NextProtos, "http/1.1")
	_, err := manager.TLSConfig().GetCertificate(&tls.ClientHelloInfo{ServerName: "other.example.edu"})
	require.Error(t, err, "unconfigured cached certificate hostname accepted")

	request := httptest.NewRequest(http.MethodGet, "http://untrusted.example/health/ready", nil)
	response := httptest.NewRecorder()
	manager.HTTPHandler(runtime.forwarding.Handler).ServeHTTP(response, request)
	require.Equal(t, http.StatusPermanentRedirect, response.Code, "forwarding response")
	require.Equal(t, "https://proctor.example.edu/health/ready", response.Header().Get("Location"), "forwarding response")

	challengeHandler := manager.HTTPHandler(runtime.forwarding.Handler)
	untrustedChallenge := httptest.NewRequest(http.MethodGet, "http://other.example.edu/.well-known/acme-challenge/token", nil)
	untrustedResponse := httptest.NewRecorder()
	challengeHandler.ServeHTTP(untrustedResponse, untrustedChallenge)
	require.Equal(t, http.StatusForbidden, untrustedResponse.Code, "untrusted challenge status")
	configuredChallenge := httptest.NewRequest(http.MethodGet, "http://proctor.example.edu:80/.well-known/acme-challenge/missing", nil)
	configuredResponse := httptest.NewRecorder()
	challengeHandler.ServeHTTP(configuredResponse, configuredChallenge)
	require.Equal(t, http.StatusNotFound, configuredResponse.Code, "configured challenge status")
}

func TestHTTPRuntimeUsesCanonicalACMEHostname(t *testing.T) {
	t.Parallel()

	settings := config.Default().Server
	settings.PublicURL = "https://PROCTOR.Example.EDU."
	settings.TLS.Mode = config.ServerTLSModeLetsEncrypt
	settings.TLS.LetsEncrypt.CacheDirectory = t.TempDir()
	settings.TLS.ForwardHTTPToHTTPS = true
	runtime := newStandardHTTPRuntimeForTest(t, settings)

	require.Equal(t, "proctor.example.edu", runtime.tls.letsEncryptHostname)
	require.Equal(t, "proctor.example.edu", runtime.tls.redirectAuthority)
	require.Equal(t, "proctor.example.edu", canonicalCertificateHostname("PROCTOR.Example.EDU.:443"))
}

func TestHTTPRuntimeStopsAutomaticCertificateManagerAfterStartupFailure(t *testing.T) {
	t.Parallel()

	bindErr := errors.New("address unavailable")
	cleanupErr := errors.New("certificate cleanup failed")
	settings := config.Default().Server
	settings.PublicURL = "https://proctor.example.edu"
	settings.TLS.Mode = config.ServerTLSModeLetsEncrypt
	settings.TLS.LetsEncrypt.CacheDirectory = filepath.Join(t.TempDir(), "acme")
	settings.TLS.ForwardHTTPToHTTPS = true
	runtimeSettings := runtimeSettingsFromConfig(settings)
	manager := newFakeAutomaticCertificateManager()
	manager.closeErr = cleanupErr
	runtime := newHTTPServer(httpServerSettings{
		handler: http.NotFoundHandler(),
		listen: func(string, string) (net.Listener, error) {
			return nil, bindErr
		},
		tls:               runtimeSettings.tls,
		readHeaderTimeout: time.Second,
		readTimeout:       time.Second,
		writeTimeout:      time.Second,
		idleTimeout:       time.Second,
		maxHeaderBytes:    1024,
	}).(*standardHTTPRuntime)
	runtime.newAutomaticCertificateManager = func() automaticCertificateManager { return manager }

	err := runtime.Serve(unusedListener{}, func() bool { return true })
	require.ErrorIs(t, err, bindErr)
	require.ErrorIs(t, err, cleanupErr)
	select {
	case <-manager.stopped:
	default:
		require.FailNow(t, "automatic certificate manager was not stopped")
	}
}

func TestHTTPRuntimeGracefulShutdownDrainsForwardingRequests(t *testing.T) {
	t.Parallel()

	primaryListener := newPipeListener()
	forwardingListener := newPipeListener()
	requestStarted := make(chan struct{})
	releaseRequest := make(chan struct{})
	runtime := &standardHTTPRuntime{
		primary: &http.Server{Handler: http.NotFoundHandler()},
		forwarding: &http.Server{Handler: http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
			close(requestStarted)
			<-releaseRequest
			writer.WriteHeader(http.StatusPermanentRedirect)
		})},
		listen: func(string, string) (net.Listener, error) {
			return forwardingListener, nil
		},
		tls: httpTLSSettings{httpListenAddress: ":80"},
	}
	runtime.serve = runtime.primary.Serve
	serveResult := make(chan error, 1)
	go func() {
		serveResult <- runtime.Serve(primaryListener, func() bool { return true })
	}()

	serverConnection, clientConnection := net.Pipe()
	go func() { forwardingListener.connections <- serverConnection }()
	go func() { _, _ = io.Copy(io.Discard, clientConnection) }()
	_, err := fmt.Fprint(clientConnection, "GET / HTTP/1.1\r\nHost: proctor.example.edu\r\nConnection: close\r\n\r\n")
	require.NoError(t, err)
	<-requestStarted

	shutdownResult := make(chan error, 1)
	shutdownContext, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	go func() { shutdownResult <- runtime.Shutdown(shutdownContext) }()
	select {
	case err := <-shutdownResult:
		require.FailNow(t, fmt.Sprintf("Shutdown returned before forwarding request drained: %v", err))
	case <-time.After(50 * time.Millisecond):
	}
	close(releaseRequest)
	require.NoError(t, (<-shutdownResult))
	require.NoError(t, (<-serveResult))
	_ = clientConnection.Close()
}

func TestPrepareACMECacheDirectoryCreatesPrivateEmptyDirectory(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "nested", "acme")
	require.NoError(t, prepareACMECacheDirectory(path))
	info, err := os.Stat(path)
	require.NoError(t, err)
	require.Equal(t, uint32(0o700), uint32(info.Mode().Perm()), "cache permissions")
	entries, err := os.ReadDir(path)
	require.NoError(t, err)
	require.Empty(t, entries)
}

func TestPrepareACMECacheDirectoryRejectsUnsafePaths(t *testing.T) {
	t.Parallel()

	t.Run("permissive directory", func(t *testing.T) {
		path := t.TempDir()
		require.NoError(t, os.Chmod(path, 0o750))
		t.Cleanup(func() { _ = os.Chmod(path, 0o700) })
		err := prepareACMECacheDirectory(path)
		require.Error(t, err)
		require.Contains(t, err.Error(), "require 0700")
	})

	t.Run("non-directory", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "acme-cache")
		require.NoError(t, os.WriteFile(path, []byte("not a directory"), 0o600))
		require.Error(t, prepareACMECacheDirectory(path), "prepareACMECacheDirectory() succeeded")
	})
}

func TestHTTPRuntimeReportsForwardingBindFailureBeforePrimaryHandoff(t *testing.T) {
	t.Parallel()

	bindErr := errors.New("address unavailable")
	settings := config.Default().Server
	settings.PublicURL = "https://proctor.example.edu"
	settings.TLS.Mode = "static"
	settings.TLS.CertificateFile = "certificate.pem"
	settings.TLS.PrivateKeyFile = "private-key.pem"
	settings.TLS.ForwardHTTPToHTTPS = true
	settings.TLS.HTTPListenAddress = ":80"
	runtimeSettings := runtimeSettingsFromConfig(settings)
	runtime := newHTTPServer(httpServerSettings{
		handler: http.NotFoundHandler(),
		listen: func(string, string) (net.Listener, error) {
			return nil, bindErr
		},
		tls:               runtimeSettings.tls,
		readHeaderTimeout: time.Second,
		readTimeout:       time.Second,
		writeTimeout:      time.Second,
		idleTimeout:       time.Second,
		maxHeaderBytes:    1024,
	})

	accepted := false
	err := runtime.Serve(unusedListener{}, func() bool {
		accepted = true
		return true
	})
	require.ErrorIs(t, err, bindErr)
	require.Contains(t, err.Error(), "HTTP-to-HTTPS forwarding")
	require.False(t, accepted, "primary listener ownership transferred after forwarding bind failure")
}

func TestHTTPRuntimeClosesForwardingListenerWhenTLSStartupFails(t *testing.T) {
	t.Parallel()

	forwardingListener := newPipeListener()
	closeErr := errors.New("forwarding listener close failed")
	forwardingListener.closeErr = closeErr
	settings := config.Default().Server
	settings.PublicURL = "https://proctor.example.edu"
	settings.TLS.Mode = "static"
	settings.TLS.CertificateFile = filepath.Join(t.TempDir(), "missing-certificate.pem")
	settings.TLS.PrivateKeyFile = filepath.Join(t.TempDir(), "missing-private-key.pem")
	settings.TLS.ForwardHTTPToHTTPS = true
	settings.TLS.HTTPListenAddress = ":80"
	runtimeSettings := runtimeSettingsFromConfig(settings)
	runtime := newHTTPServer(httpServerSettings{
		handler: http.NotFoundHandler(),
		listen: func(string, string) (net.Listener, error) {
			return forwardingListener, nil
		},
		tls:               runtimeSettings.tls,
		readHeaderTimeout: time.Second,
		readTimeout:       time.Second,
		writeTimeout:      time.Second,
		idleTimeout:       time.Second,
		maxHeaderBytes:    1024,
	})

	accepted := false
	err := runtime.Serve(unusedListener{}, func() bool {
		accepted = true
		return true
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "missing-certificate.pem")
	require.ErrorIs(t, err, closeErr)
	require.False(t, accepted, "primary listener ownership transferred after certificate load failure")
	select {
	case <-forwardingListener.closed:
	default:
		require.FailNow(t, "forwarding listener remained open after TLS startup failure")
	}
}

func newStandardHTTPRuntimeForTest(t *testing.T, settings config.Server) *standardHTTPRuntime {
	t.Helper()
	runtimeSettings := runtimeSettingsFromConfig(settings)
	runtime, ok := newHTTPServer(httpServerSettings{
		handler:           http.NotFoundHandler(),
		listen:            net.Listen,
		tls:               runtimeSettings.tls,
		readHeaderTimeout: time.Second,
		readTimeout:       time.Second,
		writeTimeout:      time.Second,
		idleTimeout:       time.Second,
		maxHeaderBytes:    1024,
	}).(*standardHTTPRuntime)
	require.True(t, ok, "newHTTPServer returned an unexpected implementation")
	return runtime
}

type unusedListener struct{}

func (unusedListener) Accept() (net.Conn, error) { panic("unused listener accepted") }
func (unusedListener) Close() error              { return nil }
func (unusedListener) Addr() net.Addr            { return unusedAddress("unused") }

type unusedAddress string

func (a unusedAddress) Network() string { return string(a) }
func (a unusedAddress) String() string  { return string(a) }

type pipeListener struct {
	connections chan net.Conn
	closed      chan struct{}
	closeOnce   sync.Once
	closeErr    error
}

func newPipeListener() *pipeListener {
	return &pipeListener{connections: make(chan net.Conn), closed: make(chan struct{})}
}

func (l *pipeListener) Accept() (net.Conn, error) {
	select {
	case connection := <-l.connections:
		return connection, nil
	case <-l.closed:
		return nil, net.ErrClosed
	}
}

func (l *pipeListener) Close() error {
	l.closeOnce.Do(func() { close(l.closed) })
	return l.closeErr
}

func (l *pipeListener) Addr() net.Addr { return unusedAddress("pipe") }

type fakeAutomaticCertificateManager struct {
	stopped  chan struct{}
	once     sync.Once
	closeErr error
}

func newFakeAutomaticCertificateManager() *fakeAutomaticCertificateManager {
	return &fakeAutomaticCertificateManager{stopped: make(chan struct{})}
}

func (*fakeAutomaticCertificateManager) TLSConfig() *tls.Config {
	return &tls.Config{
		MinVersion: tls.VersionTLS12,
		GetCertificate: func(*tls.ClientHelloInfo) (*tls.Certificate, error) {
			return nil, errors.New("certificate unavailable")
		},
	}
}

func (*fakeAutomaticCertificateManager) HTTPHandler(fallback http.Handler) http.Handler {
	return fallback
}

func (m *fakeAutomaticCertificateManager) Close() error {
	m.once.Do(func() { close(m.stopped) })
	return m.closeErr
}

func writeTestCertificate(t *testing.T) (string, string) {
	t.Helper()
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	now := time.Now()
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "proctor.example.edu"},
		DNSNames:     []string{"proctor.example.edu"},
		NotBefore:    now.Add(-time.Minute),
		NotAfter:     now.Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	certificateDER, err := x509.CreateCertificate(rand.Reader, template, template, publicKey, privateKey)
	require.NoError(t, err)
	privateKeyDER, err := x509.MarshalPKCS8PrivateKey(privateKey)
	require.NoError(t, err)
	directory := t.TempDir()
	certificateFile := filepath.Join(directory, "certificate.pem")
	privateKeyFile := filepath.Join(directory, "private-key.pem")
	require.NoError(t, os.WriteFile(certificateFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certificateDER}), 0o600))
	require.NoError(t, os.WriteFile(privateKeyFile, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: privateKeyDER}), 0o600))
	return certificateFile, privateKeyFile
}

func TestAutomaticCertificateRenewalFailureIsLogged(t *testing.T) {
	t.Parallel()

	var output strings.Builder
	manager := newAutomaticCertificateManager(httpTLSSettings{letsEncryptHostname: "proctor.example.edu"}, log.New(&output, "", 0))
	t.Cleanup(func() { require.NoError(t, manager.Close()) })
	manager.manager.RenewalFailed("proctor.example.edu", 45*time.Minute, errors.New("ACME order rejected"))
	require.Equal(t,
		"Let's Encrypt certificate renewal for proctor.example.edu failed; keeping the current certificate and retrying in 45m0s: ACME order rejected\n",
		output.String())
}
