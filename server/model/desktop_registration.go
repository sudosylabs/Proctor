// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package model

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"math/big"
	"time"
	"unicode/utf8"
)

const (
	DesktopRegistrationDisplayNameMaxRunes = 128
	DPoPKeyThumbprintLength                = 43
)

var rawBase64URL = base64.RawURLEncoding

// DesktopPublicJWK is the single public-key shape accepted by the current
// Desktop protocol. Private JWK members are deliberately absent.
type DesktopPublicJWK struct {
	Kty string `json:"kty"`
	Crv string `json:"crv"`
	X   string `json:"x"`
	Y   string `json:"y"`
}

// Validate restricts Desktop proof keys to ES256 on the P-256 curve.
func (j DesktopPublicJWK) Validate() error {
	if j.Kty != "EC" || j.Crv != "P-256" {
		return errors.New("desktop public JWK must use EC P-256")
	}
	x, err := decodeP256Coordinate(j.X)
	if err != nil {
		return errors.New("desktop public JWK x coordinate is invalid")
	}
	y, err := decodeP256Coordinate(j.Y)
	if err != nil {
		return errors.New("desktop public JWK y coordinate is invalid")
	}
	if !elliptic.P256().IsOnCurve(x, y) {
		return errors.New("desktop public JWK point is not on P-256")
	}
	return nil
}

// PublicKey returns the validated ES256 verification key.
func (j DesktopPublicJWK) PublicKey() (*ecdsa.PublicKey, error) {
	if err := j.Validate(); err != nil {
		return nil, err
	}
	x, _ := decodeP256Coordinate(j.X)
	y, _ := decodeP256Coordinate(j.Y)
	return &ecdsa.PublicKey{Curve: elliptic.P256(), X: x, Y: y}, nil
}

// Thumbprint returns the RFC 7638 SHA-256 JWK thumbprint. The member order is
// the required lexicographic order for an EC key: crv, kty, x, y.
func (j DesktopPublicJWK) Thumbprint() (string, error) {
	if err := j.Validate(); err != nil {
		return "", err
	}
	canonical, err := json.Marshal(struct {
		Crv string `json:"crv"`
		Kty string `json:"kty"`
		X   string `json:"x"`
		Y   string `json:"y"`
	}{Crv: j.Crv, Kty: j.Kty, X: j.X, Y: j.Y})
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(canonical)
	return rawBase64URL.EncodeToString(digest[:]), nil
}

func decodeP256Coordinate(value string) (*big.Int, error) {
	decoded, err := rawBase64URL.DecodeString(value)
	if err != nil || len(decoded) != 32 || rawBase64URL.EncodeToString(decoded) != value {
		return nil, errors.New("invalid P-256 coordinate")
	}
	return new(big.Int).SetBytes(decoded), nil
}

func IsValidDPoPKeyThumbprint(value string) bool {
	if len(value) != DPoPKeyThumbprintLength {
		return false
	}
	decoded, err := rawBase64URL.DecodeString(value)
	return err == nil && len(decoded) == sha256.Size && rawBase64URL.EncodeToString(decoded) == value
}

// DesktopRegistration is one User's public Desktop proof key for the current
// Institution. It is irreversible security history once revoked.
type DesktopRegistration struct {
	ID               DesktopRegistrationID
	CreatedAt        time.Time
	UpdatedAt        time.Time
	UserID           UserID
	InstitutionID    InstitutionID
	PublicJWK        DesktopPublicJWK
	KeyThumbprint    string
	DisplayName      string
	DesktopRelease   string
	DesktopBuildID   string
	Platform         DesktopPlatform
	Architecture     DesktopArchitecture
	RealtimeProtocol int
	LastUsedAt       time.Time
	RevokedAt        OptionalTime
}

func (r *DesktopRegistration) PrepareCreate(id DesktopRegistrationID, at time.Time) {
	if r == nil {
		return
	}
	at = TimeUTC(at)
	r.ID = id
	r.CreatedAt = at
	r.UpdatedAt = at
	r.LastUsedAt = at
	r.DisplayName = SanitizeUnicode(r.DisplayName)
	if r.KeyThumbprint == "" {
		r.KeyThumbprint, _ = r.PublicJWK.Thumbprint()
	}
}

func (r *DesktopRegistration) Validate() error {
	const where = "DesktopRegistration.Validate"
	if r == nil {
		return invalidModelError(where, "desktop_registration", "value", "is required", "")
	}
	if !r.ID.IsValid() || !r.UserID.IsValid() || !r.InstitutionID.IsValid() {
		return invalidModelError(where, "desktop_registration", "identity", "must be valid", "")
	}
	if r.CreatedAt.IsZero() || r.UpdatedAt.Before(r.CreatedAt) || r.LastUsedAt.Before(r.CreatedAt) ||
		r.LastUsedAt.After(r.UpdatedAt) {
		return invalidModelError(where, "desktop_registration", "timestamps", "must be ordered", "")
	}
	if err := r.PublicJWK.Validate(); err != nil {
		return invalidModelError(where, "desktop_registration", "public_jwk", err.Error(), "")
	}
	thumbprint, err := r.PublicJWK.Thumbprint()
	if err != nil || r.KeyThumbprint != thumbprint {
		return invalidModelError(where, "desktop_registration", "key_thumbprint", "must match the public JWK", "")
	}
	if utf8.RuneCountInString(r.DisplayName) > DesktopRegistrationDisplayNameMaxRunes ||
		!IsValidDesktopRelease(r.DesktopRelease) || !IsValidDesktopBuildID(r.DesktopBuildID) ||
		!r.Platform.IsValid() || !r.Architecture.IsValid() || r.RealtimeProtocol < 1 {
		return invalidModelError(where, "desktop_registration", "metadata", "is invalid", "")
	}
	if r.RevokedAt.Valid && (r.RevokedAt.Time.Before(r.CreatedAt) || r.RevokedAt.Time.After(r.UpdatedAt)) {
		return invalidModelError(where, "desktop_registration", "revoked_at", "must be within the lifecycle", "")
	}
	return nil
}

func (r *DesktopRegistration) IsActive() bool {
	return r != nil && !r.RevokedAt.Valid
}

// Auditable deliberately excludes the public key and thumbprint.
func (r *DesktopRegistration) Auditable() map[string]any {
	if r == nil {
		return nil
	}
	return map[string]any{
		"id": r.ID.String(), "user_id": r.UserID.String(),
		"institution_id": r.InstitutionID.String(), "revoked": r.RevokedAt.Valid,
	}
}
