// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only
//
// Password hashing follows the PHC string format and OWASP-recommended
// Argon2id parameters. Mattermost's current multi-hasher implementation was
// reviewed, but Proctor has no legacy bcrypt/PBKDF2 database to migrate and
// therefore uses one memory-hard format with bounded parsing.

package app

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"unicode/utf8"

	"golang.org/x/crypto/argon2"
)

var ErrPasswordMismatch = errors.New("password does not match")

var dummyHashes = struct {
	sync.Mutex
	values map[passwordParameters]string
}{values: make(map[passwordParameters]string)}

type passwordParameters struct {
	memoryKiB   uint32
	iterations  uint32
	parallelism uint8
	saltBytes   uint32
	keyBytes    uint32
}

type passwordHasher struct {
	minimumLength int
	maximumLength int
	parameters    passwordParameters
	dummyHash     string
}

func newPasswordHasher(settings PasswordPolicy) (*passwordHasher, error) {
	hasher := &passwordHasher{
		minimumLength: settings.MinimumLength,
		maximumLength: settings.MaximumLength,
		parameters: passwordParameters{
			memoryKiB:   uint32(settings.ArgonMemoryKiB),
			iterations:  uint32(settings.ArgonIterations),
			parallelism: uint8(settings.ArgonParallelism),
			saltBytes:   uint32(settings.ArgonSaltBytes),
			keyBytes:    uint32(settings.ArgonKeyBytes),
		},
	}
	dummyHashes.Lock()
	dummy := dummyHashes.values[hasher.parameters]
	if dummy == "" {
		var err error
		dummy, err = hasher.hashUnchecked("proctor-dummy-password-never-used")
		if err != nil {
			dummyHashes.Unlock()
			return nil, fmt.Errorf("create password timing hash: %w", err)
		}
		dummyHashes.values[hasher.parameters] = dummy
	}
	dummyHashes.Unlock()
	hasher.dummyHash = dummy
	return hasher, nil
}

func (h *passwordHasher) Hash(password string) (string, error) {
	if err := h.Validate(password); err != nil {
		return "", err
	}
	return h.hashUnchecked(password)
}

func (h *passwordHasher) hashUnchecked(password string) (string, error) {
	salt := make([]byte, h.parameters.saltBytes)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("read password salt: %w", err)
	}
	key := argon2.IDKey(
		[]byte(password),
		salt,
		h.parameters.iterations,
		h.parameters.memoryKiB,
		h.parameters.parallelism,
		h.parameters.keyBytes,
	)
	return fmt.Sprintf(
		"$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version,
		h.parameters.memoryKiB,
		h.parameters.iterations,
		h.parameters.parallelism,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(key),
	), nil
}

func (h *passwordHasher) Verify(encoded, password string) error {
	parameters, salt, expected, err := parseArgon2id(encoded)
	if err != nil {
		return ErrPasswordMismatch
	}
	actual := argon2.IDKey(
		[]byte(password),
		salt,
		parameters.iterations,
		parameters.memoryKiB,
		parameters.parallelism,
		uint32(len(expected)),
	)
	if subtle.ConstantTimeCompare(actual, expected) != 1 {
		return ErrPasswordMismatch
	}
	return nil
}

func (h *passwordHasher) VerifyDummy(password string) {
	_ = h.Verify(h.dummyHash, password)
}

func (h *passwordHasher) NeedsRehash(encoded string) bool {
	parameters, salt, key, err := parseArgon2id(encoded)
	if err != nil {
		return false
	}
	return parameters.memoryKiB != h.parameters.memoryKiB ||
		parameters.iterations != h.parameters.iterations ||
		parameters.parallelism != h.parameters.parallelism ||
		uint32(len(salt)) != h.parameters.saltBytes ||
		uint32(len(key)) != h.parameters.keyBytes
}

func (h *passwordHasher) Validate(password string) error {
	if !utf8.ValidString(password) {
		return errors.New("password must be valid UTF-8")
	}
	length := utf8.RuneCountInString(password)
	if length < h.minimumLength {
		return fmt.Errorf("password must contain at least %d characters", h.minimumLength)
	}
	if len(password) > h.maximumLength {
		return fmt.Errorf("password must contain at most %d bytes", h.maximumLength)
	}
	return nil
}

func parseArgon2id(encoded string) (passwordParameters, []byte, []byte, error) {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[0] != "" || parts[1] != "argon2id" {
		return passwordParameters{}, nil, nil, errors.New("invalid Argon2id hash")
	}
	if parts[2] != "v="+strconv.Itoa(argon2.Version) {
		return passwordParameters{}, nil, nil, errors.New("unsupported Argon2id version")
	}
	parameters, err := parseArgon2Parameters(parts[3])
	if err != nil {
		return passwordParameters{}, nil, nil, err
	}
	salt, err := base64.RawStdEncoding.Strict().DecodeString(parts[4])
	if err != nil || len(salt) < 16 || len(salt) > 64 {
		return passwordParameters{}, nil, nil, errors.New("invalid Argon2id salt")
	}
	key, err := base64.RawStdEncoding.Strict().DecodeString(parts[5])
	if err != nil || len(key) < 16 || len(key) > 64 {
		return passwordParameters{}, nil, nil, errors.New("invalid Argon2id key")
	}
	parameters.saltBytes = uint32(len(salt))
	parameters.keyBytes = uint32(len(key))
	return parameters, salt, key, nil
}

func parseArgon2Parameters(value string) (passwordParameters, error) {
	parts := strings.Split(value, ",")
	if len(parts) != 3 {
		return passwordParameters{}, errors.New("invalid Argon2id parameters")
	}
	memory, err := parseBoundedParameter(parts[0], "m=", 19*1024, 1024*1024)
	if err != nil {
		return passwordParameters{}, err
	}
	iterations, err := parseBoundedParameter(parts[1], "t=", 1, 20)
	if err != nil {
		return passwordParameters{}, err
	}
	parallelism, err := parseBoundedParameter(parts[2], "p=", 1, 64)
	if err != nil {
		return passwordParameters{}, err
	}
	return passwordParameters{
		memoryKiB:   uint32(memory),
		iterations:  uint32(iterations),
		parallelism: uint8(parallelism),
	}, nil
}

func parseBoundedParameter(value, prefix string, minimum, maximum uint64) (uint64, error) {
	if !strings.HasPrefix(value, prefix) {
		return 0, errors.New("invalid Argon2id parameter")
	}
	parsed, err := strconv.ParseUint(strings.TrimPrefix(value, prefix), 10, 32)
	if err != nil || parsed < minimum || parsed > maximum {
		return 0, errors.New("Argon2id parameter outside safe bounds")
	}
	return parsed, nil
}
