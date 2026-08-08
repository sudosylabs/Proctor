// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package server

import (
	"context"
	"testing"
	"time"

	vfspkg "github.com/sudosylabs/proctor/packages/vfs"
	"github.com/sudosylabs/proctor/server/config"
	"github.com/sudosylabs/proctor/server/mlog"
)

func TestRootSelectsLocalDevelopmentInfrastructure(t *testing.T) {
	t.Parallel()

	settings := config.Default()
	settings.VFS.Local.Root = t.TempDir()
	logger, err := mlog.New()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = logger.Shutdown() })

	cache, err := newCache(settings.Cache)
	if err != nil {
		t.Fatalf("construct memory cache: %v", err)
	}
	t.Cleanup(func() { _ = cache.Close() })
	if err := cache.Ping(context.Background()); err != nil {
		t.Fatalf("memory cache health: %v", err)
	}

	mailer, err := newMailer(settings.Mail)
	if err != nil {
		t.Fatalf("construct disabled mailer: %v", err)
	}
	t.Cleanup(func() { _ = mailer.Close() })
	if mailer.Enabled() {
		t.Fatal("disabled mail configuration selected an enabled mailer")
	}

	filesystem, err := newVFS(settings.VFS)
	if err != nil {
		t.Fatalf("construct local VFS: %v", err)
	}
	if _, err := filesystem.List(context.Background(), vfspkg.ListOptions{Limit: 1}); err != nil {
		t.Fatalf("local VFS health: %v", err)
	}

	cluster, err := newCluster(settings.Cluster, logger, nil, "test")
	if err != nil {
		t.Fatalf("construct local cluster: %v", err)
	}
	t.Cleanup(func() { _ = cluster.Stop(context.Background()) })
	if cluster.NodeID() != settings.Cluster.NodeID {
		t.Fatalf("cluster node ID = %q, want %q", cluster.NodeID(), settings.Cluster.NodeID)
	}

	providers, err := newExternalAuthenticationRegistry()
	if err != nil {
		t.Fatalf("construct external authentication registry: %v", err)
	}
	if err := providers.Configure(settings.Authentication.External); err != nil {
		t.Fatalf("configure external authentication registry: %v", err)
	}
}

func TestRootSelectsConfiguredSMTPVFSAndIdentityAdapters(t *testing.T) {
	t.Parallel()

	settings := config.Default()

	settings.Mail.Enabled = true
	settings.Mail.Backend = "smtp"
	mailer, err := newMailer(settings.Mail)
	if err != nil {
		t.Fatalf("construct SMTP mailer: %v", err)
	}
	if !mailer.Enabled() {
		t.Fatal("enabled SMTP configuration selected a disabled mailer")
	}

	settings.VFS.Backend = "s3"
	settings.VFS.S3.Endpoint = "s3.example.test"
	settings.VFS.S3.Bucket = "proctor"
	if _, err := newVFS(settings.VFS); err != nil {
		t.Fatalf("construct S3 VFS: %v", err)
	}

	providers, err := newExternalAuthenticationRegistry()
	if err != nil {
		t.Fatal(err)
	}
	external := settings.Authentication.External
	external.Providers = []config.ExternalAuthenticationProvider{
		{
			ID: "campus-cas", Type: config.ExternalAuthenticationTypeCAS,
			DisplayName: "Campus CAS", Enabled: true,
			CAS: &config.CASProvider{
				BaseURL: "https://cas.example.test/cas", ValidationPath: "/p3/serviceValidate",
				Timeout: config.Duration{Duration: 5 * time.Second}, MaxResponseBytes: 64 << 10,
			},
			Claims: config.ExternalClaimMapping{Subject: "user"},
		},
		{
			ID: "campus-oidc", Type: config.ExternalAuthenticationTypeOIDC,
			DisplayName: "Campus OIDC", Enabled: true,
			OIDC: &config.OIDCProvider{
				Issuer: "https://oidc.example.test", ClientID: "proctor",
				Scopes: []string{"openid"}, Timeout: config.Duration{Duration: 5 * time.Second},
				MaxResponseBytes: 64 << 10,
			},
			Claims: config.ExternalClaimMapping{Subject: "sub"},
		},
	}
	if err := providers.Configure(external); err != nil {
		t.Fatalf("configure CAS and OIDC adapters: %v", err)
	}
	if descriptors := providers.Descriptors(); len(descriptors) != 2 {
		t.Fatalf("external authentication descriptors = %#v", descriptors)
	}
}
