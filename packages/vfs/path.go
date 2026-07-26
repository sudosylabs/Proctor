package vfs

import (
	"fmt"
	"path"
	"strings"
)

// NormalizePath validates a portable VFS path and returns its canonical form.
// Paths use forward slashes and are always relative to the backend root.
func NormalizePath(name string) (string, error) {
	return normalize(name, false, false)
}

// NormalizePrefix validates a list prefix. Unlike NormalizePath, the empty
// prefix is valid and a trailing slash is preserved.
func NormalizePrefix(prefix string) (string, error) {
	return normalize(prefix, true, true)
}

func normalize(name string, allowEmpty, preserveTrailingSlash bool) (string, error) {
	if strings.ContainsRune(name, '\x00') || strings.ContainsRune(name, '\\') {
		return "", fmt.Errorf("%w: paths must use forward slashes", ErrInvalidPath)
	}
	if name == "" {
		if allowEmpty {
			return "", nil
		}
		return "", fmt.Errorf("%w: path is empty", ErrInvalidPath)
	}
	if strings.HasPrefix(name, "/") {
		return "", fmt.Errorf("%w: path is absolute", ErrInvalidPath)
	}

	for _, segment := range strings.Split(name, "/") {
		if segment == ".." {
			return "", fmt.Errorf("%w: parent traversal is not allowed", ErrInvalidPath)
		}
	}

	trailingSlash := preserveTrailingSlash && strings.HasSuffix(name, "/")
	cleaned := path.Clean(name)
	if cleaned == "." || cleaned == "" || strings.HasPrefix(cleaned, "../") {
		if allowEmpty && cleaned == "." {
			return "", nil
		}
		return "", fmt.Errorf("%w: path has no file name", ErrInvalidPath)
	}
	if trailingSlash {
		cleaned += "/"
	}
	return cleaned, nil
}
