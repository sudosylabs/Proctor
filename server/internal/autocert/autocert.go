// ---------------------------------------------------------------------------------------------
// Copyright 2016 The Go Authors. All rights reserved.
// Modifications Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.
// SPDX-License-Identifier: BSD-3-Clause
// ---------------------------------------------------------------------------------------------

// Package autocert provides automatic access to certificates from Let's Encrypt
// and any other ACME-based CA.
//
// This package is a work in progress and makes no API stability promises.
package autocert

import (
	"bytes"
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	mathrand "math/rand"
	"net"
	"net/http"
	"path"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/crypto/acme"
	"golang.org/x/net/idna"
)

// DefaultACMEDirectory is the default ACME Directory URL used when the Manager's Client is nil.
const DefaultACMEDirectory = "https://acme-v02.api.letsencrypt.org/directory"

const defaultCreateCertRetryAfter = time.Minute

// AcceptTOS is a Manager.Prompt function that always returns true to
// indicate acceptance of the CA's Terms of Service during account
// registration.
func AcceptTOS(tosURL string) bool { return true }

// HostPolicy specifies which host names the Manager is allowed to respond to.
// It returns a non-nil error if the host should be rejected.
// The returned error is accessible via tls.Conn.Handshake and its callers.
// See Manager's HostPolicy field and GetCertificate method docs for more details.
type HostPolicy func(ctx context.Context, host string) error

// HostWhitelist returns a policy where only the specified host names are allowed.
// Only exact matches are currently supported. Subdomains, regexp or wildcard
// will not match.
//
// Note that all hosts will be converted to Punycode via idna.Lookup.ToASCII so that
// Manager.GetCertificate can handle the Unicode IDN and mixedcase hosts correctly.
// Invalid hosts will be silently ignored.
func HostWhitelist(hosts ...string) HostPolicy {
	whitelist := make(map[string]bool, len(hosts))
	for _, h := range hosts {
		if h, err := idna.Lookup.ToASCII(h); err == nil {
			whitelist[h] = true
		}
	}
	return func(_ context.Context, host string) error {
		if !whitelist[host] {
			return fmt.Errorf("acme/autocert: host %q not configured in HostWhitelist", host)
		}
		return nil
	}
}

// defaultHostPolicy is used when Manager.HostPolicy is not set.
func defaultHostPolicy(context.Context, string) error {
	return nil
}

// Manager is a stateful certificate manager built on top of acme.Client.
// It obtains and refreshes certificates automatically using "tls-alpn-01"
// or "http-01" challenge types, as well as providing them to a TLS server
// via tls.Config.
//
// You must specify a cache implementation, such as DirCache,
// to reuse obtained certificates across program restarts.
// Otherwise your server is very likely to exceed the certificate
// issuer's request rate limits.
type Manager struct {
	// Prompt specifies a callback function to conditionally accept a CA's Terms of Service (TOS).
	// The registration may require the caller to agree to the CA's TOS.
	// If so, Manager calls Prompt with a TOS URL provided by the CA. Prompt should report
	// whether the caller agrees to the terms.
	//
	// To always accept the terms, the callers can use AcceptTOS.
	Prompt func(tosURL string) bool

	// Cache optionally stores and retrieves previously-obtained certificates
	// and other state. If nil, certs will only be cached for the lifetime of
	// the Manager. Multiple Managers can share the same Cache.
	//
	// Using a persistent Cache, such as DirCache, is strongly recommended.
	Cache Cache

	// HostPolicy controls which domains the Manager will attempt
	// to retrieve new certificates for. It does not affect cached certs.
	//
	// If non-nil, HostPolicy is called before requesting a new cert.
	// If nil, all hosts are currently allowed. This is not recommended,
	// as it opens a potential attack where clients connect to a server
	// by IP address and pretend to be asking for an incorrect host name.
	// Manager will attempt to obtain a certificate for that host, incorrectly,
	// eventually reaching the CA's rate limit for certificate requests
	// and making it impossible to obtain actual certificates.
	//
	// See GetCertificate for more details.
	HostPolicy HostPolicy

	// RenewBefore optionally specifies how early certificates should
	// be renewed before they expire.
	//
	// If zero, they're renewed at the lesser of 30 days or
	// 1/3 of the certificate lifetime.
	RenewBefore time.Duration

	// Client is used to perform low-level operations, such as account registration
	// and requesting new certificates.
	//
	// If Client is nil, a zero-value acme.Client is used with DefaultACMEDirectory
	// as the directory endpoint.
	// If the Client.Key is nil, a new ECDSA P-256 key is generated and,
	// if Cache is not nil, stored in cache.
	//
	// Mutating the field after the first call of GetCertificate method will have no effect.
	Client *acme.Client

	// Email optionally specifies a contact email address.
	// This is used by CAs, such as Let's Encrypt, to notify about problems
	// with issued certificates.
	//
	// If the Client's account key is already registered, Email is not used.
	Email string

	// ForceRSA used to make the Manager generate RSA certificates. It is now ignored.
	//
	// Deprecated: the Manager will request the correct type of certificate based
	// on what each client supports.
	ForceRSA bool

	// ExtraExtensions are used when generating a new CSR (Certificate Request),
	// thus allowing customization of the resulting certificate.
	// For instance, TLS Feature Extension (RFC 7633) can be used
	// to prevent an OCSP downgrade attack.
	//
	// The field value is passed to crypto/x509.CreateCertificateRequest
	// in the template's ExtraExtensions field as is.
	ExtraExtensions []pkix.Extension

	// ExternalAccountBinding optionally represents an arbitrary binding to an
	// account of the CA to which the ACME server is tied.
	// See RFC 8555, Section 7.3.4 for more details.
	ExternalAccountBinding *acme.ExternalAccountBinding

	clientMu sync.Mutex
	client   *acme.Client // initialized by acmeClient method

	stateMu sync.Mutex
	state   map[certKey]*certState

	// renewal tracks the set of domains currently running renewal timers.
	renewalMu sync.Mutex
	renewal   map[certKey]*domainRenewal

	// challengeMu guards tryHTTP01, certTokens and httpTokens.
	challengeMu sync.RWMutex
	// tryHTTP01 indicates whether the Manager should try "http-01" challenge type
	// during the authorization flow.
	tryHTTP01 bool
	// httpTokens contains response body values for http-01 challenges
	// and is keyed by the URL path at which a challenge response is expected
	// to be provisioned.
	// The entries are stored for the duration of the authorization flow.
	httpTokens map[string][]byte
	// certTokens contains temporary certificates for tls-alpn-01 challenges
	// and is keyed by the domain name which matches the ClientHello server name.
	// The entries are stored for the duration of the authorization flow.
	certTokens map[string]*tls.Certificate

	// nowFunc, if not nil, returns the current time. This may be set for
	// testing purposes.
	nowFunc func() time.Time

	lifecycleMu        sync.Mutex
	lifecycleCtx       context.Context
	lifecycleCancel    context.CancelFunc
	closed             atomic.Bool
	closeMu            sync.Mutex
	backgroundMu       sync.Mutex
	background         sync.WaitGroup
	operationMu        sync.Mutex
	operations         sync.WaitGroup
	errorMu            sync.Mutex
	retainedError      error
	randMu             sync.Mutex
	rand               *mathrand.Rand
	retryAfter         time.Duration
	didStartBackground func()
	didRemoveState     func(certKey)
	didRenewLoop       func(time.Duration, error)
}

// certKey is the key by which certificates are tracked in state, renewal and cache.
type certKey struct {
	domain  string // without trailing dot
	isRSA   bool   // RSA cert for legacy clients (as opposed to default ECDSA)
	isToken bool   // tls-based challenge token cert; key type is undefined regardless of isRSA
}

func (c certKey) String() string {
	if c.isToken {
		return c.domain + "+token"
	}
	if c.isRSA {
		return c.domain + "+rsa"
	}
	return c.domain
}

// TLSConfig creates a new TLS config suitable for net/http.Server servers,
// supporting HTTP/2 and the tls-alpn-01 ACME challenge type.
func (m *Manager) TLSConfig() *tls.Config {
	return &tls.Config{
		GetCertificate: m.GetCertificate,
		NextProtos: []string{
			"h2", "http/1.1", // enable HTTP/2
			acme.ALPNProto, // enable tls-alpn ACME challenges
		},
	}
}

// GetCertificate implements the tls.Config.GetCertificate hook.
// It provides a TLS certificate for hello.ServerName host, including answering
// tls-alpn-01 challenges.
// All other fields of hello are ignored.
//
// If m.HostPolicy is non-nil, GetCertificate calls the policy before requesting
// a new cert. A non-nil error returned from m.HostPolicy halts TLS negotiation.
// The error is propagated back to the caller of GetCertificate and is user-visible.
// This does not affect cached certs. See HostPolicy field description for more details.
//
// If GetCertificate is used directly, instead of via Manager.TLSConfig, package users will
// also have to add acme.ALPNProto to NextProtos for tls-alpn-01, or use HTTPHandler for http-01.
func (m *Manager) GetCertificate(hello *tls.ClientHelloInfo) (*tls.Certificate, error) {
	lifecycleCtx, operationDone, err := m.beginOperation()
	if err != nil {
		return nil, err
	}
	defer operationDone()
	name := hello.ServerName
	if name == "" {
		return nil, errors.New("acme/autocert: missing server name")
	}
	if !strings.Contains(strings.Trim(name, "."), ".") {
		return nil, errors.New("acme/autocert: server name component count invalid")
	}

	// Note that this conversion is necessary because some server names in the handshakes
	// started by some clients (such as cURL) are not converted to Punycode, which will
	// prevent us from obtaining certificates for them. In addition, we should also treat
	// example.com and EXAMPLE.COM as equivalent and return the same certificate for them.
	// Fortunately, this conversion also helped us deal with this kind of mixedcase problems.
	//
	// Due to the "σςΣ" problem (see https://unicode.org/faq/idn.html#22), we can't use
	// idna.Punycode.ToASCII (or just idna.ToASCII) here.
	name, err = idna.Lookup.ToASCII(name)
	if err != nil {
		return nil, errors.New("acme/autocert: server name contains invalid character")
	}

	// In the worst-case scenario, the timeout needs to account for caching, host policy,
	// domain ownership verification and certificate issuance.
	ctx, cancel := context.WithTimeout(hello.Context(), 5*time.Minute)
	stopLifecycleCancellation := context.AfterFunc(lifecycleCtx, cancel)
	defer func() {
		stopLifecycleCancellation()
		cancel()
	}()

	// Check whether this is a token cert requested for TLS-ALPN challenge.
	if wantsTokenCert(hello) {
		m.challengeMu.RLock()
		defer m.challengeMu.RUnlock()
		if cert := m.certTokens[name]; cert != nil {
			return cert, nil
		}
		if cert, err := m.cacheGet(ctx, certKey{domain: name, isToken: true}); err == nil {
			return cert, nil
		}
		// TODO: cache error results?
		return nil, fmt.Errorf("acme/autocert: no token cert for %q", name)
	}

	// regular domain
	if err := m.hostPolicy()(ctx, name); err != nil {
		return nil, err
	}

	ck := certKey{
		domain: strings.TrimSuffix(name, "."), // golang.org/issue/18114
		isRSA:  !supportsECDSA(hello),
	}
	cert, err := m.cert(ctx, ck)
	if err == nil {
		return cert, nil
	}
	if err != ErrCacheMiss {
		return nil, err
	}

	// first-time
	cert, err = m.createCert(ctx, ck)
	if err != nil {
		return nil, err
	}
	m.cachePut(ctx, ck, cert)
	return cert, nil
}

// wantsTokenCert reports whether a TLS request with SNI is made by a CA server
// for a challenge verification.
func wantsTokenCert(hello *tls.ClientHelloInfo) bool {
	// tls-alpn-01
	if len(hello.SupportedProtos) == 1 && hello.SupportedProtos[0] == acme.ALPNProto {
		return true
	}
	return false
}

func supportsECDSA(hello *tls.ClientHelloInfo) bool {
	// The "signature_algorithms" extension, if present, limits the key exchange
	// algorithms allowed by the cipher suites. See RFC 5246, section 7.4.1.4.1.
	if hello.SignatureSchemes != nil {
		ecdsaOK := false
	schemeLoop:
		for _, scheme := range hello.SignatureSchemes {
			const tlsECDSAWithSHA1 tls.SignatureScheme = 0x0203 // constant added in Go 1.10
			switch scheme {
			case tlsECDSAWithSHA1, tls.ECDSAWithP256AndSHA256,
				tls.ECDSAWithP384AndSHA384, tls.ECDSAWithP521AndSHA512:
				ecdsaOK = true
				break schemeLoop
			}
		}
		if !ecdsaOK {
			return false
		}
	}
	if hello.SupportedCurves != nil {
		ecdsaOK := false
		for _, curve := range hello.SupportedCurves {
			if curve == tls.CurveP256 {
				ecdsaOK = true
				break
			}
		}
		if !ecdsaOK {
			return false
		}
	}
	for _, suite := range hello.CipherSuites {
		switch suite {
		case tls.TLS_ECDHE_ECDSA_WITH_RC4_128_SHA,
			tls.TLS_ECDHE_ECDSA_WITH_AES_128_CBC_SHA,
			tls.TLS_ECDHE_ECDSA_WITH_AES_256_CBC_SHA,
			tls.TLS_ECDHE_ECDSA_WITH_AES_128_CBC_SHA256,
			tls.TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256,
			tls.TLS_ECDHE_ECDSA_WITH_AES_256_GCM_SHA384,
			tls.TLS_ECDHE_ECDSA_WITH_CHACHA20_POLY1305:
			return true
		}
	}
	return false
}

// HTTPHandler configures the Manager to provision ACME "http-01" challenge responses.
// It returns an http.Handler that responds to the challenges and must be
// running on port 80. If it receives a request that is not an ACME challenge,
// it delegates the request to the optional fallback handler.
//
// If fallback is nil, the returned handler redirects all GET and HEAD requests
// to the default TLS port 443 with 302 Found status code, preserving the original
// request path and query. It responds with 400 Bad Request to all other HTTP methods.
// The fallback is not protected by the optional HostPolicy.
//
// Because the fallback handler is run with unencrypted port 80 requests,
// the fallback should not serve TLS-only requests.
//
// If HTTPHandler is never called, the Manager will only use the "tls-alpn-01"
// challenge for domain verification.
func (m *Manager) HTTPHandler(fallback http.Handler) http.Handler {
	m.challengeMu.Lock()
	defer m.challengeMu.Unlock()
	m.tryHTTP01 = true

	if fallback == nil {
		fallback = http.HandlerFunc(handleHTTPRedirect)
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/.well-known/acme-challenge/") {
			fallback.ServeHTTP(w, r)
			return
		}
		// A reasonable context timeout for cache and host policy only,
		// because we don't wait for a new certificate issuance here.
		ctx, cancel := context.WithTimeout(r.Context(), time.Minute)
		defer cancel()
		if err := m.hostPolicy()(ctx, r.Host); err != nil {
			http.Error(w, err.Error(), http.StatusForbidden)
			return
		}
		data, err := m.httpToken(ctx, r.URL.Path)
		if err != nil {
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		}
		w.Write(data)
	})
}

func handleHTTPRedirect(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" && r.Method != "HEAD" {
		http.Error(w, "Use HTTPS", http.StatusBadRequest)
		return
	}
	target := "https://" + stripPort(r.Host) + r.URL.RequestURI()
	http.Redirect(w, r, target, http.StatusFound)
}

func stripPort(hostport string) string {
	host, _, err := net.SplitHostPort(hostport)
	if err != nil {
		return hostport
	}
	return net.JoinHostPort(host, "443")
}

// cert returns an existing certificate either from m.state or cache.
// If a certificate is found in cache but not in m.state, the latter will be filled
// with the cached value.
func (m *Manager) cert(ctx context.Context, ck certKey) (*tls.Certificate, error) {
	m.stateMu.Lock()
	if s, ok := m.state[ck]; ok {
		m.stateMu.Unlock()
		s.RLock()
		defer s.RUnlock()
		return s.tlscert()
	}
	defer m.stateMu.Unlock()
	cert, err := m.cacheGet(ctx, ck)
	if err != nil {
		return nil, err
	}
	signer, ok := cert.PrivateKey.(crypto.Signer)
	if !ok {
		return nil, errors.New("acme/autocert: private key cannot sign")
	}
	if m.state == nil {
		m.state = make(map[certKey]*certState)
	}
	s := &certState{
		key:  signer,
		cert: cert.Certificate,
		leaf: cert.Leaf,
	}
	m.state[ck] = s
	m.startRenew(ck, s.key, s.leaf.NotBefore, s.leaf.NotAfter)
	return cert, nil
}

// cacheGet always returns a valid certificate, or an error otherwise.
// If a cached certificate exists but is not valid, ErrCacheMiss is returned.
func (m *Manager) cacheGet(ctx context.Context, ck certKey) (*tls.Certificate, error) {
	if m.Cache == nil {
		return nil, ErrCacheMiss
	}
	data, err := m.Cache.Get(ctx, ck.String())
	if err != nil {
		return nil, err
	}

	// private
	priv, pub := pem.Decode(data)
	if priv == nil || !strings.Contains(priv.Type, "PRIVATE") {
		return nil, ErrCacheMiss
	}
	privKey, err := parsePrivateKey(priv.Bytes)
	if err != nil {
		return nil, err
	}

	// public
	var pubDER [][]byte
	for len(pub) > 0 {
		var b *pem.Block
		b, pub = pem.Decode(pub)
		if b == nil {
			break
		}
		pubDER = append(pubDER, b.Bytes)
	}
	if len(pub) > 0 {
		// Leftover content not consumed by pem.Decode. Corrupt. Ignore.
		return nil, ErrCacheMiss
	}

	// verify and create TLS cert
	leaf, err := validCert(ck, pubDER, privKey, m.now())
	if err != nil {
		return nil, ErrCacheMiss
	}
	tlscert := &tls.Certificate{
		Certificate: pubDER,
		PrivateKey:  privKey,
		Leaf:        leaf,
	}
	return tlscert, nil
}

func (m *Manager) cachePut(ctx context.Context, ck certKey, tlscert *tls.Certificate) error {
	if m.Cache == nil {
		return nil
	}

	// contains PEM-encoded data
	var buf bytes.Buffer

	// private
	switch key := tlscert.PrivateKey.(type) {
	case *ecdsa.PrivateKey:
		if err := encodeECDSAKey(&buf, key); err != nil {
			return err
		}
	case *rsa.PrivateKey:
		b := x509.MarshalPKCS1PrivateKey(key)
		pb := &pem.Block{Type: "RSA PRIVATE KEY", Bytes: b}
		if err := pem.Encode(&buf, pb); err != nil {
			return err
		}
	default:
		return errors.New("acme/autocert: unknown private key type")
	}

	// public
	for _, b := range tlscert.Certificate {
		pb := &pem.Block{Type: "CERTIFICATE", Bytes: b}
		if err := pem.Encode(&buf, pb); err != nil {
			return err
		}
	}

	return m.Cache.Put(ctx, ck.String(), buf.Bytes())
}

func encodeECDSAKey(w io.Writer, key *ecdsa.PrivateKey) error {
	b, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return err
	}
	pb := &pem.Block{Type: "EC PRIVATE KEY", Bytes: b}
	return pem.Encode(w, pb)
}

// createCert starts the domain ownership verification and returns a certificate
// for that domain upon success.
//
// If the domain is already being verified, it waits for the existing verification to complete.
// Either way, createCert blocks for the duration of the whole process.
func (m *Manager) createCert(ctx context.Context, ck certKey) (*tls.Certificate, error) {
	// TODO: maybe rewrite this whole piece using sync.Once
	state, err := m.certState(ck)
	if err != nil {
		return nil, err
	}
	// state may exist if another goroutine is already working on it
	// in which case just wait for it to finish
	if !state.locked {
		state.RLock()
		defer state.RUnlock()
		return state.tlscert()
	}

	// We are the first; state is locked.
	// Unblock the readers when domain ownership is verified
	// and we got the cert or the process failed.
	defer state.Unlock()
	state.locked = false

	der, leaf, err := m.authorizedCert(ctx, state.key, ck)
	if err != nil {
		// Remove the failed state after some time, making the manager call
		// createCert again on a later TLS hello. The retry is owned by the
		// manager so Close cancels and waits for it.
		didRemove := m.didRemoveState
		m.runBackground(func(ctx context.Context) {
			timer := time.NewTimer(m.failedIssuanceRetryAfter())
			defer timer.Stop()
			select {
			case <-ctx.Done():
				return
			case <-timer.C:
			}
			if didRemove != nil {
				defer didRemove(ck)
			}
			m.stateMu.Lock()
			defer m.stateMu.Unlock()
			// Verify the state hasn't changed and it's still invalid
			// before deleting.
			s, ok := m.state[ck]
			if !ok {
				return
			}
			if _, err := validCert(ck, s.cert, s.key, m.now()); err == nil {
				return
			}
			delete(m.state, ck)
		})
		return nil, err
	}
	state.cert = der
	state.leaf = leaf
	m.startRenew(ck, state.key, state.leaf.NotBefore, state.leaf.NotAfter)
	return state.tlscert()
}

// certState returns a new or existing certState.
// If a new certState is returned, state.exist is false and the state is locked.
// The returned error is non-nil only in the case where a new state could not be created.
func (m *Manager) certState(ck certKey) (*certState, error) {
	m.stateMu.Lock()
	defer m.stateMu.Unlock()
	if m.state == nil {
		m.state = make(map[certKey]*certState)
	}
	// existing state
	if state, ok := m.state[ck]; ok {
		return state, nil
	}

	// new locked state
	var (
		err error
		key crypto.Signer
	)
	if ck.isRSA {
		key, err = rsa.GenerateKey(rand.Reader, 2048)
	} else {
		key, err = ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	}
	if err != nil {
		return nil, err
	}

	state := &certState{
		key:    key,
		locked: true,
	}
	state.Lock() // will be unlocked by m.certState caller
	m.state[ck] = state
	return state, nil
}

// authorizedCert starts the domain ownership verification process and requests a new cert upon success.
// The key argument is the certificate private key.
func (m *Manager) authorizedCert(ctx context.Context, key crypto.Signer, ck certKey) (der [][]byte, leaf *x509.Certificate, err error) {
	csr, err := certRequest(key, ck.domain, m.ExtraExtensions)
	if err != nil {
		return nil, nil, err
	}

	client, err := m.acmeClient(ctx)
	if err != nil {
		return nil, nil, err
	}
	dir, err := client.Discover(ctx)
	if err != nil {
		return nil, nil, err
	}
	if dir.OrderURL == "" {
		return nil, nil, errors.New("autocert: ACME server doesn't support RFC 8555")
	}

	o, err := m.verifyRFC(ctx, client, ck.domain)
	if err != nil {
		return nil, nil, err
	}
	chain, _, err := client.CreateOrderCert(ctx, o.FinalizeURL, csr, true)
	if err != nil {
		return nil, nil, err
	}

	leaf, err = validCert(ck, chain, key, m.now())
	if err != nil {
		return nil, nil, err
	}
	return chain, leaf, nil
}

// verifyRFC runs the identifier (domain) order-based authorization flow for RFC compliant CAs
// using each applicable ACME challenge type.
func (m *Manager) verifyRFC(ctx context.Context, client *acme.Client, domain string) (order *acme.Order, resultErr error) {
	// Try each supported challenge type starting with a new order each time.
	// The nextTyp index of the next challenge type to try is shared across
	// all order authorizations: if we've tried a challenge type once and it didn't work,
	// it will most likely not work on another order's authorization either.
	challengeTypes := m.supportedChallengeTypes()
	nextTyp := 0 // challengeTypes index
AuthorizeOrderLoop:
	for {
		o, err := client.AuthorizeOrder(ctx, acme.DomainIDs(domain))
		if err != nil {
			return nil, err
		}
		// Remove all hanging authorizations to reduce rate limit quotas
		// after we're done.
		defer func(urls []string) {
			m.runBackground(func(ctx context.Context) {
				m.deactivatePendingAuthz(ctx, urls)
			})
		}(o.AuthzURLs)

		// Check if there's actually anything we need to do.
		switch o.Status {
		case acme.StatusReady:
			// Already authorized.
			return o, nil
		case acme.StatusPending:
			// Continue normal Order-based flow.
		default:
			return nil, fmt.Errorf("acme/autocert: invalid new order status %q; order URL: %q", o.Status, o.URI)
		}

		// Satisfy all pending authorizations.
		for _, zurl := range o.AuthzURLs {
			z, err := client.GetAuthorization(ctx, zurl)
			if err != nil {
				return nil, err
			}
			if z.Status != acme.StatusPending {
				// We are interested only in pending authorizations.
				continue
			}
			// Pick the next preferred challenge.
			var chal *acme.Challenge
			for chal == nil && nextTyp < len(challengeTypes) {
				chal = pickChallenge(challengeTypes[nextTyp], z.Challenges)
				nextTyp++
			}
			if chal == nil {
				return nil, fmt.Errorf("acme/autocert: unable to satisfy %q for domain %q: no viable challenge type found", z.URI, domain)
			}
			// Respond to the challenge and wait for validation result.
			cleanup, err := m.fulfill(ctx, client, chal, domain)
			if err != nil {
				continue AuthorizeOrderLoop
			}
			defer func() {
				resultErr = errors.Join(resultErr, cleanup())
			}()
			if _, err := client.Accept(ctx, chal); err != nil {
				continue AuthorizeOrderLoop
			}
			if _, err := client.WaitAuthorization(ctx, z.URI); err != nil {
				continue AuthorizeOrderLoop
			}
		}

		// All authorizations are satisfied.
		// Wait for the CA to update the order status.
		o, err = client.WaitOrder(ctx, o.URI)
		if err != nil {
			continue AuthorizeOrderLoop
		}
		return o, nil
	}
}

func pickChallenge(typ string, chal []*acme.Challenge) *acme.Challenge {
	for _, c := range chal {
		if c.Type == typ {
			return c
		}
	}
	return nil
}

func (m *Manager) supportedChallengeTypes() []string {
	m.challengeMu.RLock()
	defer m.challengeMu.RUnlock()
	typ := []string{"tls-alpn-01"}
	if m.tryHTTP01 {
		typ = append(typ, "http-01")
	}
	return typ
}

// deactivatePendingAuthz relinquishes all authorizations identified by the elements
// of the provided uri slice which are in "pending" state.
func (m *Manager) deactivatePendingAuthz(lifecycleCtx context.Context, uri []string) (resultErr error) {
	defer func() { m.retainCleanupError(resultErr) }()
	ctx, cancel := context.WithTimeout(lifecycleCtx, 5*time.Minute)
	defer cancel()
	client, err := m.acmeClient(ctx)
	if err != nil {
		return fmt.Errorf("initialize ACME authorization cleanup client: %w", err)
	}
	for _, u := range uri {
		z, err := client.GetAuthorization(ctx, u)
		if err != nil {
			resultErr = errors.Join(resultErr, fmt.Errorf("inspect pending ACME authorization: %w", err))
			if ctx.Err() != nil {
				break
			}
			continue
		}
		if z.Status == acme.StatusPending {
			if err := client.RevokeAuthorization(ctx, u); err != nil {
				resultErr = errors.Join(resultErr, fmt.Errorf("revoke pending ACME authorization: %w", err))
				if ctx.Err() != nil {
					break
				}
			}
		}
	}
	return resultErr
}

// fulfill provisions a response to the challenge chal.
// The cleanup is non-nil only if provisioning succeeded.
func (m *Manager) fulfill(ctx context.Context, client *acme.Client, chal *acme.Challenge, domain string) (cleanup func() error, err error) {
	switch chal.Type {
	case "tls-alpn-01":
		cert, err := client.TLSALPN01ChallengeCert(chal.Token, domain)
		if err != nil {
			return nil, err
		}
		m.putCertToken(ctx, domain, &cert)
		return func() error {
			cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Minute)
			defer cancel()
			err := m.deleteCertToken(cleanupCtx, domain)
			m.retainCleanupError(err)
			return err
		}, nil
	case "http-01":
		resp, err := client.HTTP01ChallengeResponse(chal.Token)
		if err != nil {
			return nil, err
		}
		p := client.HTTP01ChallengePath(chal.Token)
		m.putHTTPToken(ctx, p, resp)
		return func() error {
			cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Minute)
			defer cancel()
			err := m.deleteHTTPToken(cleanupCtx, p)
			m.retainCleanupError(err)
			return err
		}, nil
	}
	return nil, fmt.Errorf("acme/autocert: unknown challenge type %q", chal.Type)
}

// putCertToken stores the token certificate with the specified name
// in both m.certTokens map and m.Cache.
func (m *Manager) putCertToken(ctx context.Context, name string, cert *tls.Certificate) {
	m.challengeMu.Lock()
	defer m.challengeMu.Unlock()
	if m.certTokens == nil {
		m.certTokens = make(map[string]*tls.Certificate)
	}
	m.certTokens[name] = cert
	m.cachePut(ctx, certKey{domain: name, isToken: true}, cert)
}

// deleteCertToken removes the token certificate with the specified name
// from both m.certTokens map and m.Cache.
func (m *Manager) deleteCertToken(ctx context.Context, name string) error {
	m.challengeMu.Lock()
	defer m.challengeMu.Unlock()
	delete(m.certTokens, name)
	if m.Cache != nil {
		ck := certKey{domain: name, isToken: true}
		if err := m.Cache.Delete(ctx, ck.String()); err != nil {
			return fmt.Errorf("delete cached TLS-ALPN challenge certificate: %w", err)
		}
	}
	return nil
}

// httpToken retrieves an existing http-01 token value from an in-memory map
// or the optional cache.
func (m *Manager) httpToken(ctx context.Context, tokenPath string) ([]byte, error) {
	m.challengeMu.RLock()
	defer m.challengeMu.RUnlock()
	if v, ok := m.httpTokens[tokenPath]; ok {
		return v, nil
	}
	if m.Cache == nil {
		return nil, fmt.Errorf("acme/autocert: no token at %q", tokenPath)
	}
	return m.Cache.Get(ctx, httpTokenCacheKey(tokenPath))
}

// putHTTPToken stores an http-01 token value using tokenPath as key
// in both in-memory map and the optional Cache.
//
// It ignores any error returned from Cache.Put.
func (m *Manager) putHTTPToken(ctx context.Context, tokenPath, val string) {
	m.challengeMu.Lock()
	defer m.challengeMu.Unlock()
	if m.httpTokens == nil {
		m.httpTokens = make(map[string][]byte)
	}
	b := []byte(val)
	m.httpTokens[tokenPath] = b
	if m.Cache != nil {
		m.Cache.Put(ctx, httpTokenCacheKey(tokenPath), b)
	}
}

// deleteHTTPToken removes an http-01 token value from both the in-memory map
// and the optional Cache.
func (m *Manager) deleteHTTPToken(ctx context.Context, tokenPath string) error {
	m.challengeMu.Lock()
	defer m.challengeMu.Unlock()
	delete(m.httpTokens, tokenPath)
	if m.Cache != nil {
		if err := m.Cache.Delete(ctx, httpTokenCacheKey(tokenPath)); err != nil {
			return fmt.Errorf("delete cached HTTP-01 challenge token: %w", err)
		}
	}
	return nil
}

// httpTokenCacheKey returns a key at which an http-01 token value may be stored
// in the Manager's optional Cache.
func httpTokenCacheKey(tokenPath string) string {
	return path.Base(tokenPath) + "+http-01"
}

// startRenew starts a cert renewal timer loop, one per domain.
//
// The loop is scheduled in two cases:
// - a cert was fetched from cache for the first time (wasn't in m.state)
// - a new cert was created by m.createCert
//
// The key argument is a certificate private key.
// The exp argument is the cert expiration time (NotAfter).
func (m *Manager) startRenew(ck certKey, key crypto.Signer, notBefore, notAfter time.Time) {
	m.renewalMu.Lock()
	defer m.renewalMu.Unlock()
	if m.closed.Load() {
		return
	}
	if m.renewal[ck] != nil {
		// another goroutine is already on it
		return
	}
	if m.renewal == nil {
		m.renewal = make(map[certKey]*domainRenewal)
	}
	dr := &domainRenewal{m: m, ck: ck, key: key}
	m.renewal[ck] = dr
	dr.start(notBefore, notAfter)
}

func (m *Manager) lifecycleContext() (context.Context, error) {
	if m.closed.Load() {
		return nil, errors.New("acme/autocert: manager is closed")
	}
	m.lifecycleMu.Lock()
	defer m.lifecycleMu.Unlock()
	if m.closed.Load() {
		return nil, errors.New("acme/autocert: manager is closed")
	}
	if m.lifecycleCtx == nil {
		m.lifecycleCtx, m.lifecycleCancel = context.WithCancel(context.Background())
	}
	return m.lifecycleCtx, nil
}

func (m *Manager) runBackground(run func(context.Context)) {
	lifecycleCtx, err := m.lifecycleContext()
	if err != nil {
		return
	}
	m.backgroundMu.Lock()
	if m.closed.Load() {
		m.backgroundMu.Unlock()
		return
	}
	m.background.Add(1)
	m.backgroundMu.Unlock()
	if m.didStartBackground != nil {
		m.didStartBackground()
	}
	go func() {
		defer m.background.Done()
		run(lifecycleCtx)
	}()
}

func (m *Manager) beginOperation() (context.Context, func(), error) {
	lifecycleCtx, err := m.lifecycleContext()
	if err != nil {
		return nil, nil, err
	}
	m.operationMu.Lock()
	if m.closed.Load() {
		m.operationMu.Unlock()
		return nil, nil, errors.New("acme/autocert: manager is closed")
	}
	m.operations.Add(1)
	m.operationMu.Unlock()
	return lifecycleCtx, m.operations.Done, nil
}

// Close cancels certificate issuance and renewal, stops every renewal timer,
// and waits for an in-flight renewal callback to observe cancellation.
func (m *Manager) Close() error {
	m.closeMu.Lock()
	defer m.closeMu.Unlock()
	if m.closed.Swap(true) {
		return m.retainedCleanupError()
	}
	m.lifecycleMu.Lock()
	if m.lifecycleCancel != nil {
		m.lifecycleCancel()
	}
	m.lifecycleMu.Unlock()
	m.stopRenew()
	// Synchronize with a possible runBackground Add before waiting.
	m.backgroundMu.Lock()
	m.backgroundMu.Unlock()
	m.background.Wait()
	// Synchronize with a possible beginOperation Add before waiting.
	m.operationMu.Lock()
	m.operationMu.Unlock()
	m.operations.Wait()
	return m.retainedCleanupError()
}

func (m *Manager) retainCleanupError(err error) {
	if err == nil {
		return
	}
	m.errorMu.Lock()
	m.retainedError = errors.Join(m.retainedError, err)
	m.errorMu.Unlock()
}

func (m *Manager) retainedCleanupError() error {
	m.errorMu.Lock()
	defer m.errorMu.Unlock()
	return m.retainedError
}

func (m *Manager) failedIssuanceRetryAfter() time.Duration {
	if m.retryAfter > 0 {
		return m.retryAfter
	}
	return defaultCreateCertRetryAfter
}

func (m *Manager) randomInt63n(maximum int64) int64 {
	m.randMu.Lock()
	defer m.randMu.Unlock()
	if m.rand == nil {
		m.rand = mathrand.New(mathrand.NewSource(time.Now().UnixNano()))
	}
	return m.rand.Int63n(maximum)
}

// stopRenew stops all currently running cert renewal timers.
// The timers are not restarted during the lifetime of the Manager.
func (m *Manager) stopRenew() {
	m.renewalMu.Lock()
	renewals := make([]*domainRenewal, 0, len(m.renewal))
	for _, dr := range m.renewal {
		renewals = append(renewals, dr)
	}
	m.renewal = nil
	m.renewalMu.Unlock()
	for _, dr := range renewals {
		dr.stop()
	}
}

func (m *Manager) accountKey(ctx context.Context) (crypto.Signer, error) {
	const keyName = "acme_account+key"

	// Previous versions of autocert stored the value under a different key.
	const legacyKeyName = "acme_account.key"

	genKey := func() (*ecdsa.PrivateKey, error) {
		return ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	}

	if m.Cache == nil {
		return genKey()
	}

	data, err := m.Cache.Get(ctx, keyName)
	if err == ErrCacheMiss {
		data, err = m.Cache.Get(ctx, legacyKeyName)
	}
	if err == ErrCacheMiss {
		key, err := genKey()
		if err != nil {
			return nil, err
		}
		var buf bytes.Buffer
		if err := encodeECDSAKey(&buf, key); err != nil {
			return nil, err
		}
		if err := m.Cache.Put(ctx, keyName, buf.Bytes()); err != nil {
			return nil, err
		}
		return key, nil
	}
	if err != nil {
		return nil, err
	}

	priv, _ := pem.Decode(data)
	if priv == nil || !strings.Contains(priv.Type, "PRIVATE") {
		return nil, errors.New("acme/autocert: invalid account key found in cache")
	}
	return parsePrivateKey(priv.Bytes)
}

func (m *Manager) acmeClient(ctx context.Context) (*acme.Client, error) {
	m.clientMu.Lock()
	defer m.clientMu.Unlock()
	if m.client != nil {
		return m.client, nil
	}

	client := m.Client
	if client == nil {
		client = &acme.Client{DirectoryURL: DefaultACMEDirectory}
	}
	if client.Key == nil {
		var err error
		client.Key, err = m.accountKey(ctx)
		if err != nil {
			return nil, err
		}
	}
	if client.UserAgent == "" {
		client.UserAgent = "autocert"
	}
	var contact []string
	if m.Email != "" {
		contact = []string{"mailto:" + m.Email}
	}
	a := &acme.Account{Contact: contact, ExternalAccountBinding: m.ExternalAccountBinding}
	_, err := client.Register(ctx, a, m.Prompt)
	if err == nil || isAccountAlreadyExist(err) {
		m.client = client
		err = nil
	}
	return m.client, err
}

// isAccountAlreadyExist reports whether the err, as returned from acme.Client.Register,
// indicates the account has already been registered.
func isAccountAlreadyExist(err error) bool {
	if err == acme.ErrAccountAlreadyExists {
		return true
	}
	ae, ok := err.(*acme.Error)
	return ok && ae.StatusCode == http.StatusConflict
}

func (m *Manager) hostPolicy() HostPolicy {
	if m.HostPolicy != nil {
		return m.HostPolicy
	}
	return defaultHostPolicy
}

func (m *Manager) now() time.Time {
	if m.nowFunc != nil {
		return m.nowFunc()
	}
	return time.Now()
}

// certState is ready when its mutex is unlocked for reading.
type certState struct {
	sync.RWMutex
	locked bool              // locked for read/write
	key    crypto.Signer     // private key for cert
	cert   [][]byte          // DER encoding
	leaf   *x509.Certificate // parsed cert[0]; always non-nil if cert != nil
}

// tlscert creates a tls.Certificate from s.key and s.cert.
// Callers should wrap it in s.RLock() and s.RUnlock().
func (s *certState) tlscert() (*tls.Certificate, error) {
	if s.key == nil {
		return nil, errors.New("acme/autocert: missing signer")
	}
	if len(s.cert) == 0 {
		return nil, errors.New("acme/autocert: missing certificate")
	}
	return &tls.Certificate{
		PrivateKey:  s.key,
		Certificate: s.cert,
		Leaf:        s.leaf,
	}, nil
}

// certRequest generates a CSR for the given common name.
func certRequest(key crypto.Signer, name string, ext []pkix.Extension) ([]byte, error) {
	req := &x509.CertificateRequest{
		Subject:         pkix.Name{CommonName: name},
		DNSNames:        []string{name},
		ExtraExtensions: ext,
	}
	return x509.CreateCertificateRequest(rand.Reader, req, key)
}

// Attempt to parse the given private key DER block. OpenSSL 0.9.8 generates
// PKCS#1 private keys by default, while OpenSSL 1.0.0 generates PKCS#8 keys.
// OpenSSL ecparam generates SEC1 EC private keys for ECDSA. We try all three.
//
// Inspired by parsePrivateKey in crypto/tls/tls.go.
func parsePrivateKey(der []byte) (crypto.Signer, error) {
	if key, err := x509.ParsePKCS1PrivateKey(der); err == nil {
		return key, nil
	}
	if key, err := x509.ParsePKCS8PrivateKey(der); err == nil {
		switch key := key.(type) {
		case *rsa.PrivateKey:
			return key, nil
		case *ecdsa.PrivateKey:
			return key, nil
		default:
			return nil, errors.New("acme/autocert: unknown private key type in PKCS#8 wrapping")
		}
	}
	if key, err := x509.ParseECPrivateKey(der); err == nil {
		return key, nil
	}

	return nil, errors.New("acme/autocert: failed to parse private key")
}

// validCert parses a cert chain provided as der argument and verifies the leaf and der[0]
// correspond to the private key, the domain and key type match, and expiration dates
// are valid. It doesn't do any revocation checking.
//
// The returned value is the verified leaf cert.
func validCert(ck certKey, der [][]byte, key crypto.Signer, now time.Time) (leaf *x509.Certificate, err error) {
	// parse public part(s)
	var n int
	for _, b := range der {
		n += len(b)
	}
	pub := make([]byte, n)
	n = 0
	for _, b := range der {
		n += copy(pub[n:], b)
	}
	x509Cert, err := x509.ParseCertificates(pub)
	if err != nil || len(x509Cert) == 0 {
		return nil, errors.New("acme/autocert: no public key found")
	}
	// verify the leaf is not expired and matches the domain name
	leaf = x509Cert[0]
	if now.Before(leaf.NotBefore) {
		return nil, errors.New("acme/autocert: certificate is not valid yet")
	}
	if now.After(leaf.NotAfter) {
		return nil, errors.New("acme/autocert: expired certificate")
	}
	if err := leaf.VerifyHostname(ck.domain); err != nil {
		return nil, err
	}
	// renew certificates revoked by Let's Encrypt in January 2022
	if isRevokedLetsEncrypt(leaf) {
		return nil, errors.New("acme/autocert: certificate was probably revoked by Let's Encrypt")
	}
	// ensure the leaf corresponds to the private key and matches the certKey type
	switch pub := leaf.PublicKey.(type) {
	case *rsa.PublicKey:
		prv, ok := key.(*rsa.PrivateKey)
		if !ok {
			return nil, errors.New("acme/autocert: private key type does not match public key type")
		}
		if pub.N.Cmp(prv.N) != 0 {
			return nil, errors.New("acme/autocert: private key does not match public key")
		}
		if !ck.isRSA && !ck.isToken {
			return nil, errors.New("acme/autocert: key type does not match expected value")
		}
	case *ecdsa.PublicKey:
		prv, ok := key.(*ecdsa.PrivateKey)
		if !ok {
			return nil, errors.New("acme/autocert: private key type does not match public key type")
		}
		if pub.X.Cmp(prv.X) != 0 || pub.Y.Cmp(prv.Y) != 0 {
			return nil, errors.New("acme/autocert: private key does not match public key")
		}
		if ck.isRSA && !ck.isToken {
			return nil, errors.New("acme/autocert: key type does not match expected value")
		}
	default:
		return nil, errors.New("acme/autocert: unknown public key algorithm")
	}
	return leaf, nil
}

// isRevokedLetsEncrypt returns whether the certificate is likely to be part of
// a batch of certificates revoked by Let's Encrypt in January 2022. This check
// can be safely removed from May 2022.
func isRevokedLetsEncrypt(cert *x509.Certificate) bool {
	O := cert.Issuer.Organization
	return len(O) == 1 && O[0] == "Let's Encrypt" &&
		cert.NotBefore.Before(time.Date(2022, time.January, 26, 0, 48, 0, 0, time.UTC))
}
