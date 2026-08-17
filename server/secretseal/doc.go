// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

// Package secretseal encrypts bounded recoverable application secrets with a
// versioned, purpose-bound envelope. It owns cryptographic mechanics only;
// callers retain ownership of persistence, rotation workflow, and retention.
package secretseal
