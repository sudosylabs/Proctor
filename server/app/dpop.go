// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"context"
	"crypto/ecdsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/sudosylabs/proctor/server/model"
)

const (
	dpopNonceCachePrefix  = "authentication/dpop/nonce/"
	dpopReplayCachePrefix = "authentication/dpop/replay/"
	dpopProofMaxBytes     = 8 * 1024
	dpopJTIMaxBytes       = 128
)

type dpopPolicy struct {
	Origin        string
	NonceLifetime time.Duration
	ProofLifetime time.Duration
	ClockSkew     time.Duration
	NewNonce      func() string
	Now           func() time.Time
}

type dpopBindingKind string

const (
	dpopBindingAuthorization dpopBindingKind = "authorization"
	dpopBindingSession       dpopBindingKind = "session"
)

type dpopBinding struct {
	Kind                  dpopBindingKind
	AuthorizationID       model.BrowserAuthenticationTransactionID
	SessionID             model.SessionID
	DesktopRegistrationID model.DesktopRegistrationID
	KeyThumbprint         string
	Origin                string
}

type dpopNonceRecord struct {
	Binding  dpopBinding
	IssuedAt int64
}

type verifiedDPoPProof struct {
	PublicJWK     model.DesktopPublicJWK
	KeyThumbprint string
	JTI           string
	IssuedAt      time.Time
}

// dpopChallengeError carries only the replacement nonce needed by the HTTP
// edge. The failed proof itself is never retained or logged.
type dpopChallengeError struct {
	nonce string
	cause error
}

func (e *dpopChallengeError) Error() string { return "DPoP nonce is required" }
func (e *dpopChallengeError) Unwrap() error { return e.cause }
func (e *dpopChallengeError) Nonce() string { return e.nonce }

type dpopSecurity struct {
	cache  authenticationCache
	policy dpopPolicy
}

func newDPoPSecurity(cache authenticationCache, policy dpopPolicy) (*dpopSecurity, error) {
	if cache == nil {
		return nil, errors.New("DPoP security cache is required")
	}
	canonicalOrigin, err := canonicalDPoPOrigin(policy.Origin)
	if err != nil {
		return nil, fmt.Errorf("DPoP origin: %w", err)
	}
	if policy.NonceLifetime <= 0 || policy.ProofLifetime < policy.NonceLifetime ||
		policy.ClockSkew < 0 || policy.NewNonce == nil || policy.Now == nil {
		return nil, errors.New("DPoP policy is invalid")
	}
	policy.Origin = canonicalOrigin
	return &dpopSecurity{cache: cache, policy: policy}, nil
}

func (s *dpopSecurity) IssueNonce(ctx context.Context, binding dpopBinding) (string, error) {
	if err := s.validateBinding(binding); err != nil {
		return "", err
	}
	nonce := s.policy.NewNonce()
	if !model.IsValidCredentialToken(nonce) {
		return "", errors.New("DPoP nonce generator returned an invalid value")
	}
	record := dpopNonceRecord{Binding: binding, IssuedAt: s.policy.Now().UTC().Unix()}
	encoded, err := json.Marshal(record)
	if err != nil {
		return "", err
	}
	if err = s.cache.SetAlways(ctx, dpopNonceCachePrefix+model.HashToken(nonce), encoded, s.policy.NonceLifetime); err != nil {
		return "", NewError("authentication.dpop.unavailable").Wrap(err)
	}
	return nonce, nil
}

func (s *dpopSecurity) Verify(
	ctx context.Context,
	proof string,
	method string,
	targetURI string,
	accessToken string,
	expectedJWK *model.DesktopPublicJWK,
	binding dpopBinding,
) (*verifiedDPoPProof, error) {
	if proof == "" {
		return nil, s.challenge(ctx, binding, errors.New("DPoP proof is missing"))
	}
	parsed, err := parseAndVerifyDPoPProof(proof)
	if err != nil {
		return nil, NewError("authentication.dpop.invalid").Wrap(err)
	}
	if expectedJWK != nil {
		expectedThumbprint, thumbErr := expectedJWK.Thumbprint()
		if thumbErr != nil || expectedThumbprint != parsed.KeyThumbprint {
			return nil, NewError("authentication.dpop.invalid")
		}
	}
	if binding.KeyThumbprint != parsed.KeyThumbprint || s.validateBinding(binding) != nil {
		return nil, NewError("authentication.dpop.invalid")
	}
	canonicalTarget, err := canonicalDPoPTarget(targetURI)
	if err != nil || parsed.HTM != method || parsed.HTU != canonicalTarget {
		return nil, NewError("authentication.dpop.invalid")
	}
	if accessToken != "" {
		if parsed.ATH != dpopAccessTokenHash(accessToken) {
			return nil, NewError("authentication.dpop.invalid")
		}
	} else if parsed.ATH != "" {
		return nil, NewError("authentication.dpop.invalid")
	}
	if parsed.Nonce == "" {
		return nil, s.challenge(ctx, binding, errors.New("DPoP nonce is missing"))
	}
	record, err := s.nonceRecord(ctx, parsed.Nonce)
	if err != nil || record.Binding != binding {
		return nil, s.challenge(ctx, binding, errors.New("DPoP nonce is unknown or expired"))
	}
	now := s.policy.Now().UTC()
	issuedAt := time.Unix(record.IssuedAt, 0).UTC()
	proofAt := time.Unix(parsed.IAT, 0).UTC()
	if proofAt.Before(issuedAt.Add(-s.policy.ClockSkew)) || proofAt.After(now.Add(s.policy.ClockSkew)) ||
		now.Sub(issuedAt) > s.policy.NonceLifetime+s.policy.ClockSkew {
		return nil, NewError("authentication.dpop.invalid")
	}
	replayKey := dpopReplayCachePrefix + model.HashToken(parsed.KeyThumbprint+"\x00"+parsed.JTI)
	if err = s.cache.SetIfAbsent(ctx, replayKey, []byte{1}, s.policy.ProofLifetime); err != nil {
		if errors.Is(err, ErrAuthenticationCacheNotStored) {
			return nil, NewError("authentication.dpop.replayed")
		}
		return nil, NewError("authentication.dpop.unavailable").Wrap(err)
	}
	return &verifiedDPoPProof{PublicJWK: parsed.PublicJWK, KeyThumbprint: parsed.KeyThumbprint,
		JTI: parsed.JTI, IssuedAt: proofAt}, nil
}

func (s *dpopSecurity) challenge(ctx context.Context, binding dpopBinding, cause error) error {
	nonce, err := s.IssueNonce(ctx, binding)
	if err != nil {
		return err
	}
	return &dpopChallengeError{
		nonce: nonce,
		cause: NewError("authentication.dpop.use_nonce").Wrap(cause),
	}
}

func (s *dpopSecurity) nonceRecord(ctx context.Context, nonce string) (dpopNonceRecord, error) {
	if !model.IsValidCredentialToken(nonce) {
		return dpopNonceRecord{}, errors.New("invalid DPoP nonce")
	}
	encoded, err := s.cache.Get(ctx, dpopNonceCachePrefix+model.HashToken(nonce))
	if err != nil {
		return dpopNonceRecord{}, err
	}
	var record dpopNonceRecord
	if err = json.Unmarshal(encoded, &record); err != nil || s.validateBinding(record.Binding) != nil || record.IssuedAt <= 0 {
		return dpopNonceRecord{}, errors.New("invalid cached DPoP nonce")
	}
	return record, nil
}

func (s *dpopSecurity) validateBinding(binding dpopBinding) error {
	if !model.IsValidDPoPKeyThumbprint(binding.KeyThumbprint) || binding.Origin != s.policy.Origin {
		return errors.New("invalid DPoP binding")
	}
	switch binding.Kind {
	case dpopBindingAuthorization:
		if !binding.AuthorizationID.IsValid() || !binding.SessionID.IsZero() || !binding.DesktopRegistrationID.IsZero() {
			return errors.New("invalid authorization DPoP binding")
		}
	case dpopBindingSession:
		if !binding.AuthorizationID.IsZero() || !binding.SessionID.IsValid() || !binding.DesktopRegistrationID.IsValid() {
			return errors.New("invalid session DPoP binding")
		}
	default:
		return errors.New("invalid DPoP binding kind")
	}
	return nil
}

type parsedDPoPProof struct {
	PublicJWK     model.DesktopPublicJWK
	KeyThumbprint string
	JTI           string
	HTM           string
	HTU           string
	IAT           int64
	ATH           string
	Nonce         string
}

func parseAndVerifyDPoPProof(compact string) (*parsedDPoPProof, error) {
	if compact == "" || len(compact) > dpopProofMaxBytes || !utf8.ValidString(compact) {
		return nil, errors.New("DPoP proof is invalid")
	}
	segments := strings.Split(compact, ".")
	if len(segments) != 3 || segments[0] == "" || segments[1] == "" || segments[2] == "" {
		return nil, errors.New("DPoP proof must be a compact JWS")
	}
	headerBytes, err := base64.RawURLEncoding.DecodeString(segments[0])
	if err != nil || base64.RawURLEncoding.EncodeToString(headerBytes) != segments[0] {
		return nil, errors.New("DPoP header encoding is invalid")
	}
	payloadBytes, err := base64.RawURLEncoding.DecodeString(segments[1])
	if err != nil || base64.RawURLEncoding.EncodeToString(payloadBytes) != segments[1] {
		return nil, errors.New("DPoP payload encoding is invalid")
	}
	signature, err := base64.RawURLEncoding.DecodeString(segments[2])
	if err != nil || len(signature) != 64 || base64.RawURLEncoding.EncodeToString(signature) != segments[2] {
		return nil, errors.New("DPoP signature encoding is invalid")
	}
	var header struct {
		Typ string
		Alg string
		JWK json.RawMessage
	}
	if err = json.Unmarshal(headerBytes, &header); err != nil || header.Typ != "dpop+jwt" || header.Alg != "ES256" {
		return nil, errors.New("DPoP JOSE header is invalid")
	}
	var jwkMembers map[string]json.RawMessage
	if err = json.Unmarshal(header.JWK, &jwkMembers); err != nil {
		return nil, errors.New("DPoP JWK is invalid")
	}
	for _, member := range []string{"kty", "crv", "x", "y"} {
		if _, exists := jwkMembers[member]; !exists {
			return nil, errors.New("DPoP JWK is incomplete")
		}
	}
	if _, containsPrivateKey := jwkMembers["d"]; containsPrivateKey {
		return nil, errors.New("DPoP JWK must contain only public key material")
	}
	var jwk model.DesktopPublicJWK
	if err = json.Unmarshal(header.JWK, &jwk); err != nil || jwk.Validate() != nil {
		return nil, errors.New("DPoP JWK is invalid")
	}
	publicKey, _ := jwk.PublicKey()
	digest := sha256.Sum256([]byte(segments[0] + "." + segments[1]))
	r := new(big.Int).SetBytes(signature[:32])
	signatureS := new(big.Int).SetBytes(signature[32:])
	if !ecdsa.Verify(publicKey, digest[:], r, signatureS) {
		return nil, errors.New("DPoP signature is invalid")
	}
	var claims struct {
		JTI   string
		HTM   string
		HTU   string
		IAT   json.Number
		ATH   string
		Nonce string
	}
	decoder := json.NewDecoder(strings.NewReader(string(payloadBytes)))
	decoder.UseNumber()
	if err = decoder.Decode(&claims); err != nil || claims.JTI == "" || len(claims.JTI) > dpopJTIMaxBytes ||
		!utf8.ValidString(claims.JTI) || strings.TrimSpace(claims.JTI) != claims.JTI || claims.HTM == "" {
		return nil, errors.New("DPoP claims are invalid")
	}
	if err = decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return nil, errors.New("DPoP claims contain trailing JSON values")
	}
	iat, err := strconv.ParseInt(string(claims.IAT), 10, 64)
	if err != nil || iat <= 0 {
		return nil, errors.New("DPoP iat is invalid")
	}
	htu, err := canonicalDPoPTarget(claims.HTU)
	if err != nil || htu != claims.HTU {
		return nil, errors.New("DPoP htu is invalid")
	}
	thumbprint, _ := jwk.Thumbprint()
	return &parsedDPoPProof{PublicJWK: jwk, KeyThumbprint: thumbprint, JTI: claims.JTI,
		HTM: claims.HTM, HTU: htu, IAT: iat, ATH: claims.ATH, Nonce: claims.Nonce}, nil
}

func dpopAccessTokenHash(token string) string {
	digest := sha256.Sum256([]byte(token))
	return base64.RawURLEncoding.EncodeToString(digest[:])
}

func canonicalDPoPOrigin(raw string) (string, error) {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || parsed.Path != "" && parsed.Path != "/" {
		return "", errors.New("origin is invalid")
	}
	parsed.Scheme = strings.ToLower(parsed.Scheme)
	parsed.Host = normalizedDPoPHost(parsed)
	if parsed.Scheme != "https" && !(parsed.Scheme == "http" && isLoopbackHost(parsed.Hostname())) {
		return "", errors.New("origin must use HTTPS")
	}
	if parsed.Host == "" {
		return "", errors.New("origin host is required")
	}
	return parsed.Scheme + "://" + parsed.Host, nil
}

func canonicalDPoPTarget(raw string) (string, error) {
	parsed, err := url.Parse(raw)
	if err != nil || !parsed.IsAbs() || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", errors.New("target URI is invalid")
	}
	parsed.Scheme = strings.ToLower(parsed.Scheme)
	parsed.Host = normalizedDPoPHost(parsed)
	if parsed.Scheme != "https" && !(parsed.Scheme == "http" && isLoopbackHost(parsed.Hostname())) {
		return "", errors.New("target URI must use HTTPS")
	}
	if parsed.Path == "" {
		parsed.Path = "/"
	}
	parsed.RawPath = ""
	return parsed.String(), nil
}

func normalizedDPoPHost(parsed *url.URL) string {
	hostname := strings.ToLower(parsed.Hostname())
	port := parsed.Port()
	if (parsed.Scheme == "https" && port == "443") || (parsed.Scheme == "http" && port == "80") {
		port = ""
	}
	if port != "" {
		return net.JoinHostPort(hostname, port)
	}
	if strings.Contains(hostname, ":") {
		return "[" + hostname + "]"
	}
	return hostname
}

func isLoopbackHost(host string) bool {
	address := net.ParseIP(host)
	return strings.EqualFold(host, "localhost") || address != nil && address.IsLoopback()
}
