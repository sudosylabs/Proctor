// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package config

import (
	"strings"
	"testing"
)

func TestExecutionConfigurationCloneRedactionAndValidation(t *testing.T) {
	t.Parallel()

	cfg := Default()
	cfg.Execution.Enabled = true
	cfg.Execution.Hosts = []ExecutionHost{{
		ID: "runner-a", Address: "127.0.0.1:9443", Security: "insecure_local", Token: "host-secret",
	}}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate() = %v", err)
	}

	clone := cfg.Clone()
	clone.Execution.Hosts[0].Address = "127.0.0.1:9553"
	if cfg.Execution.Hosts[0].Address == clone.Execution.Hosts[0].Address {
		t.Fatal("Clone() exposed the execution host slice")
	}
	data, err := cfg.RedactedJSON()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "host-secret") || !strings.Contains(string(data), "[redacted]") {
		t.Fatalf("RedactedJSON() exposed the host token: %s", data)
	}
}

func TestExecutionConfigurationRejectsUnsafeHosts(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		hosts []ExecutionHost
		field string
	}{
		{name: "missing hosts", field: "execution.hosts"},
		{name: "duplicate ID", hosts: []ExecutionHost{
			{ID: "runner", Address: "127.0.0.1:9443", Security: "insecure_local", Token: "a"},
			{ID: "runner", Address: "127.0.0.1:9553", Security: "insecure_local", Token: "b"},
		}, field: "execution.hosts[1].id"},
		{name: "non-loopback insecure", hosts: []ExecutionHost{
			{ID: "runner", Address: "192.0.2.1:9443", Security: "insecure_local", Token: "a"},
		}, field: "execution.hosts[0].address"},
		{name: "TLS without identity", hosts: []ExecutionHost{
			{ID: "runner", Address: "runner.example:9443", Security: "tls", Token: "a"},
		}, field: "execution.hosts[0].server_name"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			cfg := Default()
			cfg.Execution.Enabled = true
			cfg.Execution.Hosts = test.hosts
			err := cfg.Validate()
			if err == nil || !strings.Contains(err.Error(), test.field) {
				t.Fatalf("Validate() = %v, want field %s", err, test.field)
			}
		})
	}
}
