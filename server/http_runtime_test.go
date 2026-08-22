// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

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
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sudosylabs/proctor/server/config"
)

func TestHTTPSRedirectUsesCanonicalPublicAuthority(t *testing.T) {
	t.Parallel()

	handler := newHTTPSRedirectHandler("proctor.example.edu")
	request := httptest.NewRequest(http.MethodPost, "http://attacker.example/exams/a%2Fb?attempt=1", nil)
	request.Host = "attacker.example"
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)
	if response.Code != http.StatusPermanentRedirect {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusPermanentRedirect)
	}
	if location := response.Header().Get("Location"); location != "https://proctor.example.edu/exams/a%2Fb?attempt=1" {
		t.Fatalf("Location = %q", location)
	}
}

func TestHTTPRuntimeConfiguresStaticTLS(t *testing.T) {
	t.Parallel()

	settings := config.Default().Server
	settings.PublicURL = "https://proctor.example.edu"
	settings.TLS.Mode = "static"
	settings.TLS.CertificateFile = "certificate.pem"
	settings.TLS.PrivateKeyFile = "private-key.pem"
	runtime := newStandardHTTPRuntimeForTest(t, settings)

	if runtime.primary.TLSConfig == nil || runtime.primary.TLSConfig.MinVersion != tls.VersionTLS12 {
		t.Fatalf("TLS config = %#v", runtime.primary.TLSConfig)
	}
	if runtime.newAutomaticCertificateManager != nil || runtime.forwarding != nil {
		t.Fatal("static TLS unexpectedly configured ACME or HTTP forwarding")
	}
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
	if err := client.Handshake(); err != nil {
		t.Fatal(err)
	}
	request, err := http.NewRequest(http.MethodGet, "https://proctor.example.edu/health/ready", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := request.Write(client); err != nil {
		t.Fatal(err)
	}
	response, err := http.ReadResponse(bufio.NewReader(client), request)
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	_ = client.Close()

	shutdownContext, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := runtime.Shutdown(shutdownContext); err != nil {
		t.Fatal(err)
	}
	if err := <-serveResult; !errors.Is(err, http.ErrServerClosed) {
		t.Fatalf("Serve() error = %v", err)
	}
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

	if runtime.newAutomaticCertificateManager == nil || runtime.forwarding == nil {
		t.Fatal("Let's Encrypt runtime is missing ACME or forwarding server")
	}
	manager := runtime.newAutomaticCertificateManager()
	defer func() { _ = manager.Close() }()
	if tlsConfig := manager.TLSConfig(); tlsConfig.MinVersion != tls.VersionTLS12 ||
		!slices.Contains(tlsConfig.NextProtos, "h2") ||
		!slices.Contains(tlsConfig.NextProtos, "http/1.1") {
		t.Fatalf("TLS config = %#v", tlsConfig)
	}
	if _, err := manager.TLSConfig().GetCertificate(&tls.ClientHelloInfo{ServerName: "other.example.edu"}); err == nil {
		t.Fatal("unconfigured cached certificate hostname accepted")
	}

	request := httptest.NewRequest(http.MethodGet, "http://untrusted.example/health/ready", nil)
	response := httptest.NewRecorder()
	manager.HTTPHandler(runtime.forwarding.Handler).ServeHTTP(response, request)
	if response.Code != http.StatusPermanentRedirect ||
		response.Header().Get("Location") != "https://proctor.example.edu/health/ready" {
		t.Fatalf("forwarding response = %d %q", response.Code, response.Header().Get("Location"))
	}

	challengeHandler := manager.HTTPHandler(runtime.forwarding.Handler)
	untrustedChallenge := httptest.NewRequest(http.MethodGet, "http://other.example.edu/.well-known/acme-challenge/token", nil)
	untrustedResponse := httptest.NewRecorder()
	challengeHandler.ServeHTTP(untrustedResponse, untrustedChallenge)
	if untrustedResponse.Code != http.StatusForbidden {
		t.Fatalf("untrusted challenge status = %d, want %d", untrustedResponse.Code, http.StatusForbidden)
	}
	configuredChallenge := httptest.NewRequest(http.MethodGet, "http://proctor.example.edu:80/.well-known/acme-challenge/missing", nil)
	configuredResponse := httptest.NewRecorder()
	challengeHandler.ServeHTTP(configuredResponse, configuredChallenge)
	if configuredResponse.Code != http.StatusNotFound {
		t.Fatalf("configured challenge status = %d, want %d", configuredResponse.Code, http.StatusNotFound)
	}
}

func TestHTTPRuntimeUsesCanonicalACMEHostname(t *testing.T) {
	t.Parallel()

	settings := config.Default().Server
	settings.PublicURL = "https://PROCTOR.Example.EDU."
	settings.TLS.Mode = config.ServerTLSModeLetsEncrypt
	settings.TLS.LetsEncrypt.CacheDirectory = t.TempDir()
	settings.TLS.ForwardHTTPToHTTPS = true
	runtime := newStandardHTTPRuntimeForTest(t, settings)

	if runtime.tls.letsEncryptHostname != "proctor.example.edu" {
		t.Fatalf("runtime ACME hostname = %q", runtime.tls.letsEncryptHostname)
	}
	if runtime.tls.redirectAuthority != "proctor.example.edu" {
		t.Fatalf("runtime redirect authority = %q", runtime.tls.redirectAuthority)
	}
	if hostname := canonicalCertificateHostname("PROCTOR.Example.EDU.:443"); hostname != "proctor.example.edu" {
		t.Fatalf("canonical certificate hostname = %q", hostname)
	}
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
	if !errors.Is(err, bindErr) || !errors.Is(err, cleanupErr) {
		t.Fatalf("Serve() error = %v", err)
	}
	select {
	case <-manager.stopped:
	default:
		t.Fatal("automatic certificate manager was not stopped")
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
	if _, err := fmt.Fprint(clientConnection, "GET / HTTP/1.1\r\nHost: proctor.example.edu\r\nConnection: close\r\n\r\n"); err != nil {
		t.Fatal(err)
	}
	<-requestStarted

	shutdownResult := make(chan error, 1)
	shutdownContext, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	go func() { shutdownResult <- runtime.Shutdown(shutdownContext) }()
	select {
	case err := <-shutdownResult:
		t.Fatalf("Shutdown returned before forwarding request drained: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	close(releaseRequest)
	if err := <-shutdownResult; err != nil {
		t.Fatal(err)
	}
	if err := <-serveResult; err != nil {
		t.Fatalf("Serve() error = %v", err)
	}
	_ = clientConnection.Close()
}

func TestPrepareACMECacheDirectoryCreatesPrivateEmptyDirectory(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "nested", "acme")
	if err := prepareACMECacheDirectory(path); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if permission := info.Mode().Perm(); permission != 0o700 {
		t.Fatalf("cache permissions = %04o, want 0700", permission)
	}
	entries, err := os.ReadDir(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("cache write probe was not removed: %v", entries)
	}
}

func TestPrepareACMECacheDirectoryRejectsUnsafePaths(t *testing.T) {
	t.Parallel()

	t.Run("permissive directory", func(t *testing.T) {
		path := t.TempDir()
		if err := os.Chmod(path, 0o750); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.Chmod(path, 0o700) })
		if err := prepareACMECacheDirectory(path); err == nil || !strings.Contains(err.Error(), "require 0700") {
			t.Fatalf("prepareACMECacheDirectory() error = %v", err)
		}
	})

	t.Run("non-directory", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "acme-cache")
		if err := os.WriteFile(path, []byte("not a directory"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := prepareACMECacheDirectory(path); err == nil {
			t.Fatal("prepareACMECacheDirectory() succeeded")
		}
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
	if !errors.Is(err, bindErr) || !strings.Contains(err.Error(), "HTTP-to-HTTPS forwarding") {
		t.Fatalf("Serve() error = %v", err)
	}
	if accepted {
		t.Fatal("primary listener ownership transferred after forwarding bind failure")
	}
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
	if err == nil || !strings.Contains(err.Error(), "missing-certificate.pem") || !errors.Is(err, closeErr) {
		t.Fatalf("Serve() error = %v", err)
	}
	if accepted {
		t.Fatal("primary listener ownership transferred after certificate load failure")
	}
	select {
	case <-forwardingListener.closed:
	default:
		t.Fatal("forwarding listener remained open after TLS startup failure")
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
	if !ok {
		t.Fatal("newHTTPServer returned an unexpected implementation")
	}
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
	if err != nil {
		t.Fatal(err)
	}
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
	if err != nil {
		t.Fatal(err)
	}
	privateKeyDER, err := x509.MarshalPKCS8PrivateKey(privateKey)
	if err != nil {
		t.Fatal(err)
	}
	directory := t.TempDir()
	certificateFile := filepath.Join(directory, "certificate.pem")
	privateKeyFile := filepath.Join(directory, "private-key.pem")
	if err := os.WriteFile(certificateFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certificateDER}), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(privateKeyFile, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: privateKeyDER}), 0o600); err != nil {
		t.Fatal(err)
	}
	return certificateFile, privateKeyFile
}
