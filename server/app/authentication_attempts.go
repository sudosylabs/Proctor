// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"net"
	"strings"
	"time"
)

type authenticationAttemptCache interface {
	Add(context.Context, string, int64, time.Duration) (int64, error)
	Delete(context.Context, string) error
}

const (
	// Attempt inputs are hashed rather than retained, but still need an
	// application-owned work bound because accounting deliberately precedes
	// credential validation in enumeration-resistant flows.
	maxAuthenticationAttemptIdentityBytes  = 4 << 10
	maxAuthenticationAttemptSourceBytes    = 4 << 10
	maxAuthenticationAttemptQualifierBytes = 256
)

// authenticationAttemptAccounting owns the disposable mechanics shared by
// authentication attempt policies. It is deliberately stateless: callers own
// which dimensions and limits apply to each use case.
type authenticationAttemptAccounting struct {
	cache authenticationAttemptCache
}

type authenticationAttemptPurpose uint8

const (
	authenticationAttemptPurposeLocalLogin authenticationAttemptPurpose = iota + 1
	authenticationAttemptPurposeAccountRecovery
	authenticationAttemptPurposeExternalAuthentication
	authenticationAttemptPurposeInstallationBootstrap
)

func (p authenticationAttemptPurpose) keySegment() (string, bool) {
	switch p {
	case authenticationAttemptPurposeLocalLogin:
		return "local-login", true
	case authenticationAttemptPurposeAccountRecovery:
		return "account-recovery", true
	case authenticationAttemptPurposeExternalAuthentication:
		return "external-authentication", true
	case authenticationAttemptPurposeInstallationBootstrap:
		return "installation-bootstrap", true
	default:
		return "", false
	}
}

type authenticationAttemptDimension uint8

const (
	authenticationAttemptDimensionIdentitySource authenticationAttemptDimension = iota + 1
	authenticationAttemptDimensionIdentity
	authenticationAttemptDimensionSource
)

func (d authenticationAttemptDimension) keySegment() (string, bool) {
	switch d {
	case authenticationAttemptDimensionIdentitySource:
		return "identity-source", true
	case authenticationAttemptDimensionIdentity:
		return "identity", true
	case authenticationAttemptDimensionSource:
		return "source", true
	default:
		return "", false
	}
}

type authenticationAttemptLimit struct {
	dimension authenticationAttemptDimension
	maximum   int
	identity  string
	source    string
}

type authenticationAttemptIntent struct {
	purpose   authenticationAttemptPurpose
	qualifier string
	window    time.Duration
	limits    []authenticationAttemptLimit
}

// authenticationAttemptReceipt intentionally exposes no cache key. A caller
// may use it only to ask the accounting module to reset a dimension selected
// by that caller's successful-action policy.
type authenticationAttemptReceipt struct {
	keys map[authenticationAttemptDimension]string
}

func newAuthenticationAttemptAccounting(
	cache authenticationAttemptCache,
) (*authenticationAttemptAccounting, error) {
	if cache == nil {
		return nil, errors.New("authentication attempt cache is required")
	}
	return &authenticationAttemptAccounting{cache: cache}, nil
}

func (a *authenticationAttemptAccounting) account(
	ctx context.Context,
	intent authenticationAttemptIntent,
) (authenticationAttemptReceipt, bool, error) {
	purpose, limits, err := validateAuthenticationAttemptIntent(intent)
	if err != nil {
		return authenticationAttemptReceipt{}, false, err
	}

	receipt := authenticationAttemptReceipt{
		keys: make(map[authenticationAttemptDimension]string, len(limits)),
	}
	counts := make([]int64, 0, len(limits))
	for _, limit := range limits {
		dimension, _ := limit.dimension.keySegment()
		key := "authentication/attempts/" + purpose + "/" + dimension + "/" +
			digestAuthenticationAttempt(intent, limit)
		count, addErr := a.cache.Add(ctx, key, 1, intent.window)
		if addErr != nil {
			return authenticationAttemptReceipt{}, false, addErr
		}
		receipt.keys[limit.dimension] = key
		counts = append(counts, count)
	}

	for index, count := range counts {
		if count > int64(limits[index].maximum) {
			return receipt, true, nil
		}
	}
	return receipt, false, nil
}

func (a *authenticationAttemptAccounting) reset(
	ctx context.Context,
	receipt authenticationAttemptReceipt,
	dimension authenticationAttemptDimension,
) error {
	if a == nil || a.cache == nil {
		return errors.New("authentication attempt accounting is unavailable")
	}
	key, ok := receipt.keys[dimension]
	if !ok || key == "" {
		return errors.New("authentication attempt receipt is invalid")
	}
	return a.cache.Delete(ctx, key)
}

func validateAuthenticationAttemptIntent(
	intent authenticationAttemptIntent,
) (string, []authenticationAttemptLimit, error) {
	purpose, valid := intent.purpose.keySegment()
	if !valid || intent.window <= 0 || len(intent.limits) == 0 ||
		len(intent.qualifier) > maxAuthenticationAttemptQualifierBytes {
		return "", nil, errors.New("authentication attempt intent is invalid")
	}
	seen := make(map[authenticationAttemptDimension]struct{}, len(intent.limits))
	for _, limit := range intent.limits {
		if _, valid = limit.dimension.keySegment(); !valid || limit.maximum <= 0 {
			return "", nil, errors.New("authentication attempt intent is invalid")
		}
		if _, duplicate := seen[limit.dimension]; duplicate {
			return "", nil, errors.New("authentication attempt intent is invalid")
		}
		if len(limit.identity) > maxAuthenticationAttemptIdentityBytes ||
			len(limit.source) > maxAuthenticationAttemptSourceBytes {
			return "", nil, errors.New("authentication attempt intent is invalid")
		}
		seen[limit.dimension] = struct{}{}
	}
	return purpose, intent.limits, nil
}

func digestAuthenticationAttempt(
	intent authenticationAttemptIntent,
	limit authenticationAttemptLimit,
) string {
	hash := sha256.New()
	writeAuthenticationAttemptDigestPart(hash, []byte{byte(intent.purpose)})
	writeAuthenticationAttemptDigestPart(hash, []byte{byte(limit.dimension)})
	writeAuthenticationAttemptDigestPart(hash, []byte(intent.qualifier))
	switch limit.dimension {
	case authenticationAttemptDimensionIdentitySource:
		writeAuthenticationAttemptDigestPart(hash, []byte(normalizeLoginIdentity(limit.identity)))
		writeAuthenticationAttemptDigestPart(hash, []byte(normalizeLoginSource(limit.source)))
	case authenticationAttemptDimensionIdentity:
		writeAuthenticationAttemptDigestPart(hash, []byte(normalizeLoginIdentity(limit.identity)))
	case authenticationAttemptDimensionSource:
		writeAuthenticationAttemptDigestPart(hash, []byte(normalizeLoginSource(limit.source)))
	}
	return hex.EncodeToString(hash.Sum(nil))
}

type authenticationAttemptDigestWriter interface {
	Write([]byte) (int, error)
}

func writeAuthenticationAttemptDigestPart(writer authenticationAttemptDigestWriter, value []byte) {
	var size [4]byte
	binary.BigEndian.PutUint32(size[:], uint32(len(value)))
	_, _ = writer.Write(size[:])
	_, _ = writer.Write(value)
}

func normalizeLoginIdentity(identity string) string {
	return strings.ToLower(strings.TrimSpace(identity))
}

func normalizeLoginSource(source string) string {
	source = strings.TrimSpace(source)
	if host, _, err := net.SplitHostPort(source); err == nil {
		return strings.ToLower(host)
	}
	if source == "" {
		return "unknown"
	}
	return strings.ToLower(source)
}
