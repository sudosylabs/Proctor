// Package vfs defines a portable, streaming virtual file storage contract.
//
// Paths are slash-separated, relative to a backend-defined root, and never
// contain "." or ".." segments. The package deliberately models files rather
// than application-owned metadata. Consumers should keep ownership,
// authorization, retention, and domain relationships in their own data store.
package vfs
