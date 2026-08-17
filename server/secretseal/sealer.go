// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package secretseal

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
)

const (
	// EnvelopeVersion1 is the first persisted secret-envelope format.
	EnvelopeVersion1 = 1
	// AlgorithmAES256GCM identifies AES-256-GCM without implying negotiability.
	AlgorithmAES256GCM = "AES-256-GCM"

	// MaximumPlaintextBytes is the absolute module bound. Callers may construct
	// a Sealer with a smaller domain-specific bound.
	MaximumPlaintextBytes = 4 << 20
	// MaximumFallbackKeys bounds operational key-ring size during rotation.
	MaximumFallbackKeys = 8

	maximumPurposeBytes = 64
	maximumOwnerBytes   = 256
	keyIDBytes          = 16
)

var (
	// ErrInvalidSettings reports an unusable key ring or plaintext bound.
	ErrInvalidSettings = errors.New("secret sealing settings are invalid")
	// ErrInvalidBinding reports an invalid purpose or owner binding.
	ErrInvalidBinding = errors.New("secret sealing binding is invalid")
	// ErrPlaintextTooLarge reports plaintext beyond the configured bound.
	ErrPlaintextTooLarge = errors.New("secret sealing plaintext exceeds its bound")
	// ErrInvalidEnvelope deliberately combines unsupported, unavailable,
	// malformed, and unauthenticated envelopes into one safe read error.
	ErrInvalidEnvelope = errors.New("sealed value is invalid or unavailable")
)

// Settings configures one immutable key ring. Keys use canonical standard
// base64 encoding of exactly 32 bytes. Its diagnostic representations redact
// all key material.
type Settings struct {
	EncryptionKey    string   `json:"-"`
	DecryptionKeys   []string `json:"-"`
	MaximumPlaintext int      `json:"maximum_plaintext"`
}

// String provides a safe diagnostic representation.
func (s Settings) String() string {
	return fmt.Sprintf(
		"secretseal.Settings{EncryptionKey:%s, DecryptionKeys:%d, MaximumPlaintext:%d}",
		redacted(s.EncryptionKey), len(s.DecryptionKeys), s.MaximumPlaintext,
	)
}

// GoString provides a safe %#v diagnostic representation.
func (s Settings) GoString() string { return s.String() }

// LogValue prevents structured logging from reflecting secret-bearing fields.
func (s Settings) LogValue() slog.Value { return slog.StringValue(s.String()) }

// MarshalJSON returns only the non-secret operational setting.
func (s Settings) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		MaximumPlaintext int `json:"maximum_plaintext"`
	}{MaximumPlaintext: s.MaximumPlaintext})
}

// Auditable returns deliberately safe configuration metadata. It satisfies
// the server's structural audit contract without importing the domain model.
func (s Settings) Auditable() map[string]any {
	return map[string]any{
		"configured":         s.EncryptionKey != "",
		"fallback_key_count": len(s.DecryptionKeys),
		"maximum_plaintext":  s.MaximumPlaintext,
	}
}

// Binding supplies authenticated domain separation. Purpose is a fixed
// application namespace; Owner is the stable identity of the owning record.
// Neither value is stored in the Envelope.
type Binding struct {
	Purpose string
	Owner   string
}

// Envelope is the complete persisted cryptographic value. JSON contains
// ciphertext metadata but never plaintext or the authenticated Binding.
type Envelope struct {
	Version    int    `json:"version"`
	Algorithm  string `json:"algorithm"`
	KeyID      string `json:"key_id"`
	Nonce      string `json:"nonce"`
	Ciphertext string `json:"ciphertext"`
}

// String excludes nonce and ciphertext from ordinary logs.
func (e Envelope) String() string {
	return fmt.Sprintf(
		"secretseal.Envelope{Version:%d, Algorithm:%q, KeyID:%q, Ciphertext:[redacted]}",
		e.Version, e.Algorithm, e.KeyID,
	)
}

// GoString excludes nonce and ciphertext from %#v formatting.
func (e Envelope) GoString() string { return e.String() }

// LogValue excludes nonce and ciphertext from structured logs.
func (e Envelope) LogValue() slog.Value { return slog.StringValue(e.String()) }

// Auditable excludes nonce, ciphertext, and the authenticated Binding while
// retaining only non-secret format and key-selection metadata.
func (e Envelope) Auditable() map[string]any {
	return map[string]any{
		"version": e.Version, "algorithm": e.Algorithm, "key_id": e.KeyID,
	}
}

// Sealer seals with the primary key and opens with any configured key.
type Sealer struct {
	keys             map[string][32]byte
	primaryKeyID     string
	maximumPlaintext int
}

// HasKey reports whether this immutable ring can open envelopes bearing the
// supplied non-secret key identifier. It does not validate ciphertext and is
// intended for startup reconciliation of persisted envelope metadata.
func (s *Sealer) HasKey(keyID string) bool {
	if s == nil || !validKeyID(keyID) {
		return false
	}
	_, ok := s.keys[keyID]
	return ok
}

// PrimaryKeyID returns the non-secret identity of the key used for new
// envelopes. It never exposes key material and is safe for fencing rotation.
func (s *Sealer) PrimaryKeyID() string {
	if s == nil {
		return ""
	}
	return s.primaryKeyID
}

// New constructs an immutable key ring.
func New(settings Settings) (*Sealer, error) {
	if settings.MaximumPlaintext < 1 || settings.MaximumPlaintext > MaximumPlaintextBytes ||
		len(settings.DecryptionKeys) > MaximumFallbackKeys {
		return nil, ErrInvalidSettings
	}
	encodedKeys := make([]string, 0, 1+len(settings.DecryptionKeys))
	encodedKeys = append(encodedKeys, settings.EncryptionKey)
	encodedKeys = append(encodedKeys, settings.DecryptionKeys...)
	keys := make(map[string][32]byte, len(encodedKeys))
	seenMaterial := make(map[[32]byte]struct{}, len(encodedKeys))
	primaryKeyID := ""
	for index, encoded := range encodedKeys {
		key, ok := decodeKey(encoded)
		if !ok {
			return nil, ErrInvalidSettings
		}
		if _, duplicate := seenMaterial[key]; duplicate {
			return nil, ErrInvalidSettings
		}
		seenMaterial[key] = struct{}{}
		keyID := keyIdentity(key)
		if existing, collision := keys[keyID]; collision &&
			subtle.ConstantTimeCompare(existing[:], key[:]) != 1 {
			return nil, ErrInvalidSettings
		}
		keys[keyID] = key
		if index == 0 {
			primaryKeyID = keyID
		}
	}
	return &Sealer{
		keys: keys, primaryKeyID: primaryKeyID,
		maximumPlaintext: settings.MaximumPlaintext,
	}, nil
}

// Seal encrypts plaintext under the primary key and authenticates its Binding.
func (s *Sealer) Seal(binding Binding, plaintext []byte) (Envelope, error) {
	if s == nil || !validBinding(binding) {
		return Envelope{}, ErrInvalidBinding
	}
	if len(plaintext) > s.maximumPlaintext {
		return Envelope{}, ErrPlaintextTooLarge
	}
	key, ok := s.keys[s.primaryKeyID]
	if !ok {
		return Envelope{}, ErrInvalidSettings
	}
	aead, err := newAEAD(key)
	if err != nil {
		return Envelope{}, ErrInvalidSettings
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return Envelope{}, errors.New("generate secret sealing nonce")
	}
	envelope := Envelope{
		Version: EnvelopeVersion1, Algorithm: AlgorithmAES256GCM,
		KeyID: s.primaryKeyID,
		Nonce: base64.RawURLEncoding.EncodeToString(nonce),
	}
	ciphertext := aead.Seal(nil, nonce, plaintext, authenticatedData(binding, envelope))
	envelope.Ciphertext = base64.RawURLEncoding.EncodeToString(ciphertext)
	return envelope, nil
}

// Open authenticates the envelope and Binding before returning a fresh
// plaintext slice. All caller-controlled envelope failures use one safe error.
func (s *Sealer) Open(binding Binding, envelope Envelope) ([]byte, error) {
	if s == nil || !validBinding(binding) || envelope.Version != EnvelopeVersion1 ||
		envelope.Algorithm != AlgorithmAES256GCM || !validKeyID(envelope.KeyID) {
		return nil, ErrInvalidEnvelope
	}
	key, ok := s.keys[envelope.KeyID]
	if !ok {
		return nil, ErrInvalidEnvelope
	}
	aead, err := newAEAD(key)
	if err != nil {
		return nil, ErrInvalidEnvelope
	}
	nonce, ok := decodeRawURL(envelope.Nonce, aead.NonceSize())
	if !ok {
		return nil, ErrInvalidEnvelope
	}
	maximumCiphertext := s.maximumPlaintext + aead.Overhead()
	ciphertext, ok := decodeRawURLBounded(
		envelope.Ciphertext, aead.Overhead(), maximumCiphertext,
	)
	if !ok {
		return nil, ErrInvalidEnvelope
	}
	plaintext, err := aead.Open(nil, nonce, ciphertext, authenticatedData(binding, envelope))
	if err != nil || len(plaintext) > s.maximumPlaintext {
		return nil, ErrInvalidEnvelope
	}
	return plaintext, nil
}

// MarshalJSON exposes no key material from a configured Sealer.
func (s *Sealer) MarshalJSON() ([]byte, error) {
	return []byte(`{"type":"secret_sealer"}`), nil
}

// String exposes no key material from a configured Sealer.
func (s *Sealer) String() string { return "secretseal.Sealer{keys:[redacted]}" }

// GoString exposes no key material from %#v formatting.
func (s *Sealer) GoString() string { return s.String() }

// LogValue exposes no key material to structured logging.
func (s *Sealer) LogValue() slog.Value { return slog.StringValue(s.String()) }

func newAEAD(key [32]byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

func decodeKey(encoded string) ([32]byte, bool) {
	var key [32]byte
	if len(encoded) != base64.StdEncoding.EncodedLen(len(key)) {
		return key, false
	}
	decoded, err := base64.StdEncoding.Strict().DecodeString(encoded)
	if err != nil || len(decoded) != len(key) ||
		base64.StdEncoding.EncodeToString(decoded) != encoded {
		return key, false
	}
	copy(key[:], decoded)
	return key, true
}

func keyIdentity(key [32]byte) string {
	digest := sha256.Sum256(key[:])
	return hex.EncodeToString(digest[:keyIDBytes])
}

func validKeyID(keyID string) bool {
	if len(keyID) != keyIDBytes*2 {
		return false
	}
	decoded, err := hex.DecodeString(keyID)
	return err == nil && hex.EncodeToString(decoded) == keyID
}

func validBinding(binding Binding) bool {
	if len(binding.Purpose) < 1 || len(binding.Purpose) > maximumPurposeBytes ||
		len(binding.Owner) < 1 || len(binding.Owner) > maximumOwnerBytes {
		return false
	}
	for index, character := range []byte(binding.Purpose) {
		if (character >= 'a' && character <= 'z') ||
			(index > 0 && character >= '0' && character <= '9') ||
			(index > 0 && (character == '.' || character == '_' || character == '-')) {
			continue
		}
		return false
	}
	for _, character := range []byte(binding.Owner) {
		if (character >= 'a' && character <= 'z') ||
			(character >= 'A' && character <= 'Z') ||
			(character >= '0' && character <= '9') ||
			character == '.' || character == '_' || character == ':' || character == '-' {
			continue
		}
		return false
	}
	return true
}

func authenticatedData(binding Binding, envelope Envelope) []byte {
	var aad bytes.Buffer
	aad.WriteString("proctor:secretseal")
	aad.WriteByte(0)
	_ = binary.Write(&aad, binary.BigEndian, uint16(envelope.Version))
	writeAADPart(&aad, envelope.Algorithm)
	writeAADPart(&aad, envelope.KeyID)
	writeAADPart(&aad, binding.Purpose)
	writeAADPart(&aad, binding.Owner)
	return aad.Bytes()
}

func writeAADPart(target *bytes.Buffer, value string) {
	_ = binary.Write(target, binary.BigEndian, uint32(len(value)))
	target.WriteString(value)
}

func decodeRawURL(encoded string, exactBytes int) ([]byte, bool) {
	decoded, ok := decodeRawURLBounded(encoded, exactBytes, exactBytes)
	return decoded, ok
}

func decodeRawURLBounded(encoded string, minimumBytes, maximumBytes int) ([]byte, bool) {
	if encoded == "" || len(encoded) > base64.RawURLEncoding.EncodedLen(maximumBytes) {
		return nil, false
	}
	decoded, err := base64.RawURLEncoding.Strict().DecodeString(encoded)
	if err != nil || len(decoded) < minimumBytes || len(decoded) > maximumBytes ||
		base64.RawURLEncoding.EncodeToString(decoded) != encoded {
		return nil, false
	}
	return decoded, true
}

func redacted(value string) string {
	if value == "" {
		return ""
	}
	return "[redacted]"
}
