// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package config

import (
	"strings"
	"testing"
)

func TestBootstrapProtectionAcceptsStrongExplicitSecret(t *testing.T) {
	t.Parallel()

	cfg := Default()
	cfg.Authentication.Bootstrap.DevelopmentMode = false
	cfg.Server.ListenAddress = "0.0.0.0:8065"
	cfg.Server.PublicURL = "https://proctor.example.edu"
	cfg.Authentication.Bootstrap.Secret = "operator-provided-bootstrap-secret-32-bytes"
	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate() explicit production secret: %v", err)
	}
}

func TestBootstrapProtectionRejectsWeakOrNonLoopbackDevelopmentConfiguration(t *testing.T) {
	t.Parallel()

	cfg := Default()
	cfg.Authentication.Bootstrap.Secret = "too-short"
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "between 32 and 512 bytes") {
		t.Fatalf("Validate() weak secret error = %v", err)
	}

	cfg = Default()
	cfg.Authentication.Bootstrap.Secret = "operator-provided\tbootstrap-secret-32-bytes"
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "control characters") {
		t.Fatalf("Validate() control-character secret error = %v", err)
	}

	cfg = Default()
	cfg.Authentication.Bootstrap.DevelopmentMode = true
	cfg.Authentication.Bootstrap.Secret = "operator-provided-bootstrap-secret-32-bytes"
	cfg.Server.ListenAddress = "192.0.2.1:8065"
	cfg.Server.PublicURL = "https://proctor.example.edu"
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "development_mode") {
		t.Fatalf("Validate() non-loopback development error = %v", err)
	}
}

func TestBootstrapSecretIsRedacted(t *testing.T) {
	t.Parallel()

	cfg := Default()
	cfg.Authentication.Bootstrap.Secret = "operator-provided-bootstrap-secret-32-bytes"
	data, err := cfg.RedactedJSON()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), cfg.Authentication.Bootstrap.Secret) ||
		!strings.Contains(string(data), `"Secret": "[redacted]"`) {
		t.Fatalf("RedactedJSON() = %s", data)
	}
}
