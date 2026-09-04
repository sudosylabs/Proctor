// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package server

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/sudosylabs/proctor/server/config"
	"github.com/sudosylabs/proctor/server/internal/autocert"
)

type automaticCertificateManager interface {
	TLSConfig() *tls.Config
	HTTPHandler(http.Handler) http.Handler
	Close() error
}

// standardHTTPRuntime owns the complete client-facing HTTP lifecycle. The
// outer Server supplies the primary listener; this implementation additionally
// owns the optional ACME challenge and HTTP-to-HTTPS forwarding listener.
type standardHTTPRuntime struct {
	primary                        *http.Server
	forwarding                     *http.Server
	serve                          func(net.Listener) error
	listen                         func(string, string) (net.Listener, error)
	tls                            httpTLSSettings
	newAutomaticCertificateManager func() automaticCertificateManager
	stopping                       atomic.Bool
}

func newHTTPServer(settings httpServerSettings) httpRuntime {
	runtime := &standardHTTPRuntime{
		primary: &http.Server{
			Handler:           settings.handler,
			ErrorLog:          settings.errorLog,
			ReadHeaderTimeout: settings.readHeaderTimeout,
			ReadTimeout:       settings.readTimeout,
			WriteTimeout:      settings.writeTimeout,
			IdleTimeout:       settings.idleTimeout,
			MaxHeaderBytes:    settings.maxHeaderBytes,
		},
		listen: settings.listen,
		tls:    settings.tls,
	}
	runtime.serve = runtime.primary.Serve

	switch settings.tls.mode {
	case config.ServerTLSModeStatic:
		runtime.primary.TLSConfig = &tls.Config{MinVersion: tls.VersionTLS12}
		runtime.serve = func(listener net.Listener) error {
			return runtime.primary.ServeTLS(
				listener,
				settings.tls.certificateFile,
				settings.tls.privateKeyFile,
			)
		}
	case config.ServerTLSModeLetsEncrypt:
		runtime.newAutomaticCertificateManager = func() automaticCertificateManager {
			return newAutomaticCertificateManager(settings.tls)
		}
		runtime.serve = func(listener net.Listener) error {
			return runtime.primary.ServeTLS(listener, "", "")
		}
	}

	if settings.tls.forwardHTTPToHTTPS {
		forwardingHandler := newHTTPSRedirectHandler(settings.tls.redirectAuthority)
		runtime.forwarding = &http.Server{
			Handler:           forwardingHandler,
			ErrorLog:          settings.errorLog,
			ReadHeaderTimeout: settings.readHeaderTimeout,
			ReadTimeout:       settings.readTimeout,
			WriteTimeout:      settings.writeTimeout,
			IdleTimeout:       settings.idleTimeout,
			MaxHeaderBytes:    settings.maxHeaderBytes,
		}
	}
	return runtime
}

func newHTTPSRedirectHandler(authority string) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		target := url.URL{
			Scheme:   "https",
			Host:     authority,
			Path:     request.URL.Path,
			RawPath:  request.URL.RawPath,
			RawQuery: request.URL.RawQuery,
		}
		http.Redirect(writer, request, target.String(), http.StatusPermanentRedirect)
	})
}

func (r *standardHTTPRuntime) Serve(listener net.Listener, accept func() bool) (resultErr error) {
	if r.newAutomaticCertificateManager != nil {
		if err := prepareACMECacheDirectory(r.tls.letsEncryptCacheDirectory); err != nil {
			return fmt.Errorf("prepare Let's Encrypt cache directory: %w", err)
		}
		manager := r.newAutomaticCertificateManager()
		defer func() {
			resultErr = errors.Join(resultErr, manager.Close())
		}()
		r.primary.TLSConfig = manager.TLSConfig()
		if r.forwarding != nil {
			r.forwarding.Handler = manager.HTTPHandler(r.forwarding.Handler)
		}
	}
	ownedPrimary := &ownershipListener{Listener: listener, accept: accept}
	if r.forwarding == nil {
		return r.serve(ownedPrimary)
	}
	if r.listen == nil {
		return errors.New("HTTP forwarding listener factory is unavailable")
	}
	listener, err := r.listen("tcp", r.tls.httpListenAddress)
	if err != nil {
		return fmt.Errorf("listen for HTTP-to-HTTPS forwarding on %s: %w", r.tls.httpListenAddress, err)
	}
	forwardingListener := &closeOnceListener{Listener: listener}

	type serveResult struct {
		name string
		err  error
	}
	results := make(chan serveResult, 2)
	go func() {
		results <- serveResult{name: "HTTPS", err: r.serve(ownedPrimary)}
	}()
	go func() {
		results <- serveResult{name: "HTTP forwarding", err: r.forwarding.Serve(forwardingListener)}
	}()

	first := <-results
	var siblingCloseErr error
	forwardingListenerClosed := false
	if !r.stopping.Load() {
		if first.name == "HTTPS" {
			if err := r.forwarding.Close(); err != nil {
				siblingCloseErr = fmt.Errorf("close HTTP forwarding server after HTTPS stopped: %w", err)
			}
		} else {
			if err := r.primary.Close(); err != nil {
				siblingCloseErr = fmt.Errorf("close HTTPS server after HTTP forwarding stopped: %w", err)
			}
		}
		if err := forwardingListener.Close(); err != nil {
			siblingCloseErr = errors.Join(
				siblingCloseErr,
				fmt.Errorf("close HTTP forwarding listener: %w", err),
			)
		}
		forwardingListenerClosed = true
	}
	second := <-results
	if !forwardingListenerClosed {
		if err := forwardingListener.Close(); err != nil {
			siblingCloseErr = errors.Join(
				siblingCloseErr,
				fmt.Errorf("close HTTP forwarding listener: %w", err),
			)
		}
	}
	return errors.Join(
		normalizeHTTPServeResult(first.name, first.err),
		normalizeHTTPServeResult(second.name, second.err),
		siblingCloseErr,
	)
}

type closeOnceListener struct {
	net.Listener
	once sync.Once
	err  error
}

func (l *closeOnceListener) Close() error {
	l.once.Do(func() { l.err = l.Listener.Close() })
	return l.err
}

type ownedAutomaticCertificateManager struct {
	manager  *autocert.Manager
	hostname string
}

func newAutomaticCertificateManager(settings httpTLSSettings) *ownedAutomaticCertificateManager {
	return &ownedAutomaticCertificateManager{
		hostname: settings.letsEncryptHostname,
		manager: &autocert.Manager{
			Cache:      autocert.DirCache(settings.letsEncryptCacheDirectory),
			Prompt:     autocert.AcceptTOS,
			Email:      settings.letsEncryptEmail,
			HostPolicy: autocert.HostWhitelist(settings.letsEncryptHostname),
		},
	}
}

func (m *ownedAutomaticCertificateManager) TLSConfig() *tls.Config {
	configuration := m.manager.TLSConfig()
	configuration.MinVersion = tls.VersionTLS12
	getCertificate := configuration.GetCertificate
	configuration.GetCertificate = func(hello *tls.ClientHelloInfo) (*tls.Certificate, error) {
		if canonicalCertificateHostname(hello.ServerName) != m.hostname {
			return nil, fmt.Errorf("automatic certificate hostname %q is not configured", hello.ServerName)
		}
		canonicalHello := *hello
		canonicalHello.ServerName = m.hostname
		return getCertificate(&canonicalHello)
	}
	return configuration
}

func (m *ownedAutomaticCertificateManager) HTTPHandler(fallback http.Handler) http.Handler {
	challengeHandler := m.manager.HTTPHandler(fallback)
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if !strings.HasPrefix(request.URL.Path, "/.well-known/acme-challenge/") {
			challengeHandler.ServeHTTP(writer, request)
			return
		}
		if canonicalCertificateHostname(request.Host) != m.hostname {
			http.Error(writer, "ACME challenge hostname is not configured", http.StatusForbidden)
			return
		}
		canonicalRequest := request.Clone(request.Context())
		canonicalRequest.Host = m.hostname
		challengeHandler.ServeHTTP(writer, canonicalRequest)
	})
}

func (m *ownedAutomaticCertificateManager) Close() error {
	return m.manager.Close()
}

func canonicalCertificateHostname(authority string) string {
	hostname := authority
	if host, _, err := net.SplitHostPort(authority); err == nil {
		hostname = host
	}
	return strings.TrimSuffix(strings.ToLower(hostname), ".")
}

func prepareACMECacheDirectory(path string) (resultErr error) {
	if err := os.MkdirAll(path, 0o700); err != nil {
		return err
	}
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return errors.New("cache path is not a directory")
	}
	if info.Mode().Perm()&0o077 != 0 {
		return fmt.Errorf("cache directory permissions %04o expose private key material; require 0700", info.Mode().Perm())
	}
	probe, err := os.CreateTemp(path, ".proctor-acme-write-test-*")
	if err != nil {
		return err
	}
	probePath := probe.Name()
	defer func() {
		resultErr = errors.Join(resultErr, probe.Close(), os.Remove(probePath))
	}()
	return nil
}

func normalizeHTTPServeResult(name string, err error) error {
	if err == nil || errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return fmt.Errorf("serve %s: %w", name, err)
}

func (r *standardHTTPRuntime) Shutdown(ctx context.Context) error {
	r.stopping.Store(true)
	return r.stopServers(func(server *http.Server) error {
		return server.Shutdown(ctx)
	})
}

func (r *standardHTTPRuntime) Close() error {
	r.stopping.Store(true)
	return r.stopServers(func(server *http.Server) error {
		return server.Close()
	})
}

func (r *standardHTTPRuntime) stopServers(stop func(*http.Server) error) error {
	servers := []*http.Server{r.primary}
	if r.forwarding != nil {
		servers = append(servers, r.forwarding)
	}
	errorsByServer := make(chan error, len(servers))
	var wait sync.WaitGroup
	wait.Add(len(servers))
	for _, server := range servers {
		go func() {
			defer wait.Done()
			errorsByServer <- stop(server)
		}()
	}
	wait.Wait()
	close(errorsByServer)
	var result error
	for err := range errorsByServer {
		result = errors.Join(result, err)
	}
	return result
}
