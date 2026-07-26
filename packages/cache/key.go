package cache

import (
	"fmt"
	"unicode"
	"unicode/utf8"
)

const (
	// MaximumKeyBytes keeps keys reasonably portable across cache backends.
	MaximumKeyBytes = 1024
	// MaximumNamespaceBytes bounds backend prefixes.
	MaximumNamespaceBytes = 128
)

// ValidateKey applies the portable cache key rules.
func ValidateKey(key string) error {
	return validateIdentifier("key", key, MaximumKeyBytes, ErrInvalidKey)
}

// ValidateNamespace applies the portable backend namespace rules.
func ValidateNamespace(namespace string) error {
	return validateIdentifier("namespace", namespace, MaximumNamespaceBytes, ErrInvalidNamespace)
}

func validateIdentifier(kind, value string, maximum int, sentinel error) error {
	if value == "" {
		return fmt.Errorf("%w: %s must not be empty", sentinel, kind)
	}
	if len(value) > maximum {
		return fmt.Errorf("%w: %s exceeds %d bytes", sentinel, kind, maximum)
	}
	if !utf8.ValidString(value) {
		return fmt.Errorf("%w: %s is not valid UTF-8", sentinel, kind)
	}
	for _, r := range value {
		if unicode.IsControl(r) {
			return fmt.Errorf("%w: %s contains a control character", sentinel, kind)
		}
	}
	return nil
}
