// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package server

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"image"
	"io"
	"net/http"
	"time"

	"github.com/HugoSmits86/nativewebp"
	"github.com/disintegration/imaging"
	_ "golang.org/x/image/webp"

	mailpkg "github.com/sudosylabs/proctor/packages/mail"
	vfspkg "github.com/sudosylabs/proctor/packages/vfs"
	"github.com/sudosylabs/proctor/server/app"
	"github.com/sudosylabs/proctor/server/app/api"
	"github.com/sudosylabs/proctor/server/mlog"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/platform"
	"github.com/sudosylabs/proctor/server/platform/externalauth"
)

// applicationDependencies projects platform capabilities and deployment
// configuration into the explicit app.Dependencies bundle so package app never
// imports platform.
func applicationDependencies(
	applicationPlatform *platform.Service,
) (app.Dependencies, error) {
	if applicationPlatform == nil {
		return app.Dependencies{}, errors.New("platform service is nil")
	}
	cfg := applicationPlatform.Config()
	auth := cfg.Authentication
	cache := platformAuthenticationCache{cache: applicationPlatform.Cache()}
	log := applicationPlatform.Log()
	return app.Dependencies{
		Store:       applicationPlatform.Store(),
		Cache:       cache,
		Mailer:      accountMailerAdapter{mailer: applicationPlatform.Mailer()},
		Registry:    externalProviderRegistryAdapter{registry: applicationPlatform},
		FileContent: fileContentAdapter{filesystem: applicationPlatform.VFS()},
		NodeID:      applicationPlatform.Cluster().NodeID(),
		PublicURL:   cfg.Server.PublicURL,
		Password: app.PasswordPolicy{
			MinimumLength:    auth.Password.MinimumLength,
			MaximumLength:    auth.Password.MaximumLength,
			ArgonMemoryKiB:   auth.Password.ArgonMemoryKiB,
			ArgonIterations:  auth.Password.ArgonIterations,
			ArgonParallelism: auth.Password.ArgonParallelism,
			ArgonSaltBytes:   auth.Password.ArgonSaltBytes,
			ArgonKeyBytes:    auth.Password.ArgonKeyBytes,
		},
		Sessions: app.SessionPolicy{
			AccessTTL:              auth.Sessions.AccessTTL.Duration,
			RefreshTTL:             auth.Sessions.RefreshTTL.Duration,
			IdleTTL:                auth.Sessions.IdleTTL.Duration,
			AbsoluteTTL:            auth.Sessions.AbsoluteTTL.Duration,
			ActivityUpdateInterval: auth.Sessions.ActivityUpdateInterval.Duration,
			MaximumPerUser:         auth.Sessions.MaximumPerUser,
		},
		LoginRateLimit: app.LoginRateLimitPolicy{
			Window:                auth.LoginRateLimit.Window.Duration,
			MaximumAttempts:       auth.LoginRateLimit.MaximumAttempts,
			MaximumSourceAttempts: auth.LoginRateLimit.MaximumSourceAttempts,
		},
		PersonalAccessToken: app.PersonalAccessTokenPolicy{
			MinimumLifetime:        auth.PersonalAccessTokens.MinimumLifetime.Duration,
			MaximumLifetime:        auth.PersonalAccessTokens.MaximumLifetime.Duration,
			LastUsedUpdateInterval: auth.PersonalAccessTokens.LastUsedUpdateInterval.Duration,
			MaximumPerUser:         auth.PersonalAccessTokens.MaximumPerUser,
		},
		AccountRecovery: app.AccountRecoveryPolicy{
			EmailVerificationTTL: auth.AccountRecovery.EmailVerificationTTL.Duration,
			PasswordResetTTL:     auth.AccountRecovery.PasswordResetTTL.Duration,
			RateLimit: app.LoginRateLimitPolicy{
				Window:                auth.AccountRecovery.RateLimit.Window.Duration,
				MaximumAttempts:       auth.AccountRecovery.RateLimit.MaximumAttempts,
				MaximumSourceAttempts: auth.AccountRecovery.RateLimit.MaximumSourceAttempts,
			},
		},
		MFA: app.MFAPolicy{
			Enabled:           auth.MFA.Enabled,
			Issuer:            auth.MFA.Issuer,
			EncryptionKey:     auth.MFA.EncryptionKey,
			DecryptionKeys:    append([]string(nil), auth.MFA.DecryptionKeys...),
			SetupTTL:          auth.MFA.SetupTTL.Duration,
			RecoveryCodeCount: auth.MFA.RecoveryCodeCount,
		},
		ExternalAuth: app.ExternalAuthenticationPolicy{
			PublicURL:     cfg.Server.PublicURL,
			LoginStateTTL: auth.External.LoginStateTTL.Duration,
			LoginRateLimit: app.LoginRateLimitPolicy{
				Window:                auth.LoginRateLimit.Window.Duration,
				MaximumAttempts:       auth.LoginRateLimit.MaximumAttempts,
				MaximumSourceAttempts: auth.LoginRateLimit.MaximumSourceAttempts,
			},
			NodeID: applicationPlatform.Cluster().NodeID(),
		},
		RecentAuthenticationTTL:   auth.RecentAuthenticationTTL.Duration,
		AuthenticationDiagnostics: mlogAuthenticationDiagnostics{log: log},
		RealtimeDiagnostics:       mlogRealtimeDiagnostics{log: log},
		RecoveryDiagnostics:       mlogRecoveryDiagnostics{log: log},
	}, nil
}

type fileContentAdapter struct{ filesystem vfspkg.FileSystem }

func (a fileContentAdapter) NormalizeAndStoreProfilePicture(ctx context.Context, revisionID model.FileRevisionID, body io.Reader, size int64, at time.Time) ([]model.FileRendition, error) {
	const maximumPixels = 25_000_000
	const maximumBytes int64 = 5 << 20
	raw, err := io.ReadAll(io.LimitReader(body, maximumBytes+1))
	if err != nil || int64(len(raw)) > maximumBytes || (size >= 0 && int64(len(raw)) != size) {
		return nil, app.ErrInvalidProfilePicture
	}
	mediaType := http.DetectContentType(raw)
	if mediaType != "image/png" && mediaType != "image/jpeg" && mediaType != "image/webp" {
		return nil, app.ErrInvalidProfilePicture
	}
	configuration, _, err := image.DecodeConfig(bytes.NewReader(raw))
	if err != nil || configuration.Width <= 0 || configuration.Height <= 0 || configuration.Width > 4096 || configuration.Height > 4096 {
		return nil, app.ErrInvalidProfilePicture
	}
	imageValue, err := imaging.Decode(bytes.NewReader(raw), imaging.AutoOrientation(true))
	if err != nil || imageValue.Bounds().Dx() <= 0 || imageValue.Bounds().Dy() <= 0 || imageValue.Bounds().Dx() > maximumPixels/imageValue.Bounds().Dy() {
		return nil, app.ErrInvalidProfilePicture
	}
	squareSize := min(imageValue.Bounds().Dx(), imageValue.Bounds().Dy())
	square := imaging.CropCenter(imageValue, squareSize, squareSize)
	renditions := make([]model.FileRendition, 0, 3)
	for _, target := range []int{128, 256, 512} {
		dimension := min(target, squareSize)
		normalized := square
		if dimension < squareSize {
			normalized = imaging.Resize(square, dimension, dimension, imaging.Lanczos)
		}
		var encoded bytes.Buffer
		if err = nativewebp.Encode(&encoded, normalized, &nativewebp.Options{CompressionLevel: nativewebp.DefaultCompression}); err != nil {
			_ = a.RemoveProfilePictureRenditions(ctx, revisionID, renditions)
			return nil, err
		}
		checksum := fmt.Sprintf("%x", sha256.Sum256(encoded.Bytes()))
		rendition, modelErr := model.NewFileRendition(model.NewFileRenditionID(), revisionID, fmt.Sprintf("profile_%d", target), "image/webp", int64(encoded.Len()), dimension, dimension, checksum, at)
		if modelErr != nil {
			_ = a.RemoveProfilePictureRenditions(ctx, revisionID, renditions)
			return nil, modelErr
		}
		path := profilePictureRenditionPath(revisionID, rendition.ID)
		encodedSize := int64(encoded.Len())
		if _, err = a.filesystem.Write(ctx, path, bytes.NewReader(encoded.Bytes()), vfspkg.WriteOptions{Size: &encodedSize, NoOverwrite: true}); err != nil {
			_ = a.RemoveProfilePictureRenditions(ctx, revisionID, renditions)
			return nil, err
		}
		renditions = append(renditions, *rendition)
	}
	return renditions, nil
}

func (a fileContentAdapter) OpenProfilePictureRendition(ctx context.Context, revisionID model.FileRevisionID, renditionID model.FileRenditionID) (io.ReadCloser, error) {
	file, err := a.filesystem.Open(ctx, profilePictureRenditionPath(revisionID, renditionID), vfspkg.OpenOptions{})
	if err != nil {
		return nil, err
	}
	return file.Body, nil
}

func (a fileContentAdapter) RemoveProfilePictureRenditions(ctx context.Context, revisionID model.FileRevisionID, renditions []model.FileRendition) error {
	var joined error
	for _, rendition := range renditions {
		joined = errors.Join(joined, a.filesystem.Remove(ctx, profilePictureRenditionPath(revisionID, rendition.ID), vfspkg.RemoveOptions{}))
	}
	return joined
}

func profilePictureRenditionPath(revisionID model.FileRevisionID, renditionID model.FileRenditionID) string {
	id := renditionID.String()
	return fmt.Sprintf("files/%s/%s/revisions/%s/renditions/%s.webp", id[:2], id[2:4], revisionID.String(), id)
}

// platformAuthenticationCache adapts platform.Cache to app.authenticationCache.
type platformAuthenticationCache struct {
	cache platform.Cache
}

func (c platformAuthenticationCache) Get(ctx context.Context, key string) ([]byte, error) {
	data, err := c.cache.Get(ctx, key)
	if errors.Is(err, platform.ErrCacheMiss) {
		return nil, app.ErrAuthenticationCacheMiss
	}
	return data, err
}

func (c platformAuthenticationCache) SetAlways(
	ctx context.Context,
	key string,
	value []byte,
	ttl time.Duration,
) error {
	return c.cache.Set(ctx, key, value, ttl, platform.CacheSetAlways)
}

func (c platformAuthenticationCache) SetIfAbsent(
	ctx context.Context,
	key string,
	value []byte,
	ttl time.Duration,
) error {
	err := c.cache.Set(ctx, key, value, ttl, platform.CacheSetIfAbsent)
	if errors.Is(err, platform.ErrCacheNotStored) {
		return app.ErrAuthenticationCacheNotStored
	}
	return err
}

func (c platformAuthenticationCache) Delete(ctx context.Context, key string) error {
	return c.cache.Delete(ctx, key)
}

func (c platformAuthenticationCache) Add(
	ctx context.Context,
	key string,
	delta int64,
	ttl time.Duration,
) (int64, error) {
	return c.cache.Add(ctx, key, delta, ttl)
}

type accountMailerAdapter struct {
	mailer platform.Mailer
}

func (a accountMailerAdapter) Enabled() bool {
	return a.mailer.Enabled()
}

func (a accountMailerAdapter) SendCredentialMail(
	ctx context.Context,
	displayName string,
	email string,
	subject string,
	textBody string,
	htmlBody string,
	at time.Time,
) error {
	_, err := a.mailer.Send(ctx, mailpkg.Message{
		To: []mailpkg.Address{{
			Name: displayName, Address: email,
		}},
		Subject: subject,
		Text:    textBody,
		HTML:    htmlBody,
		Date:    at,
	})
	return err
}

// externalProviderRegistryAdapter exposes the platform registry through the
// app-owned ExternalIdentityProvider port.
type externalProviderRegistryAdapter struct {
	registry interface {
		ExternalAuthenticationProviders() []model.ExternalAuthenticationProvider
		ExternalAuthenticationProvider(string) (externalauth.Provider, bool)
	}
}

func (a externalProviderRegistryAdapter) Descriptors() []model.ExternalAuthenticationProvider {
	return a.registry.ExternalAuthenticationProviders()
}

func (a externalProviderRegistryAdapter) Provider(
	id string,
) (app.ExternalIdentityProvider, bool) {
	provider, ok := a.registry.ExternalAuthenticationProvider(id)
	if !ok {
		return nil, false
	}
	return externalIdentityProviderAdapter{provider: provider}, true
}

type externalIdentityProviderAdapter struct {
	provider externalauth.Provider
}

func (a externalIdentityProviderAdapter) Descriptor() model.ExternalAuthenticationProvider {
	return a.provider.Descriptor()
}

func (a externalIdentityProviderAdapter) AutoProvision() bool {
	return a.provider.AutoProvision()
}

func (a externalIdentityProviderAdapter) Begin(
	ctx context.Context,
	request app.ExternalProviderBeginRequest,
) (*app.ExternalProviderBeginResponse, error) {
	response, err := a.provider.Begin(ctx, externalauth.BeginRequest{
		CallbackURL: request.CallbackURL,
		State:       request.State,
		Proof:       request.Proof,
	})
	if err != nil {
		return nil, mapExternalProviderError(err)
	}
	if response == nil {
		return nil, nil
	}
	return &app.ExternalProviderBeginResponse{RedirectURL: response.RedirectURL}, nil
}

func (a externalIdentityProviderAdapter) State(
	callback model.ExternalAuthenticationCallback,
) (string, error) {
	state, err := a.provider.State(callback)
	if err != nil {
		return "", mapExternalProviderError(err)
	}
	return state, nil
}

func (a externalIdentityProviderAdapter) Complete(
	ctx context.Context,
	request app.ExternalProviderCompleteRequest,
) (*model.ExternalAuthenticationAssertion, error) {
	assertion, err := a.provider.Complete(ctx, externalauth.CompleteRequest{
		CallbackURL: request.CallbackURL,
		State:       request.State,
		Proof:       request.Proof,
		Callback:    request.Callback,
	})
	if err != nil {
		return nil, mapExternalProviderError(err)
	}
	return assertion, nil
}

func mapExternalProviderError(err error) error {
	switch {
	case errors.Is(err, externalauth.ErrAuthenticationRejected):
		return app.ErrExternalAuthenticationRejected
	case errors.Is(err, externalauth.ErrInvalidResponse):
		return app.ErrExternalAuthenticationInvalid
	case errors.Is(err, externalauth.ErrProviderUnavailable):
		return app.ErrExternalAuthenticationUnavailable
	default:
		return err
	}
}

type mlogAuthenticationDiagnostics struct {
	log *mlog.Logger
}

func (d mlogAuthenticationDiagnostics) WarnContext(ctx context.Context, message string, err error) {
	if d.log == nil {
		return
	}
	fields := []mlog.Field{}
	if err != nil {
		fields = append(fields, mlog.Err(err))
	}
	d.log.WarnContext(ctx, message, fields...)
}

type mlogRealtimeDiagnostics struct {
	log *mlog.Logger
}

func (d mlogRealtimeDiagnostics) ErrorContext(ctx context.Context, message string, err error) {
	if d.log == nil {
		return
	}
	fields := []mlog.Field{}
	if err != nil {
		fields = append(fields, mlog.Err(err))
	}
	d.log.ErrorContext(ctx, message, fields...)
}

func (d mlogRealtimeDiagnostics) ErrorContextWithEvent(
	ctx context.Context,
	message, event string,
	err error,
) {
	if d.log == nil {
		return
	}
	fields := []mlog.Field{mlog.String("event", event)}
	if err != nil {
		fields = append(fields, mlog.Err(err))
	}
	d.log.ErrorContext(ctx, message, fields...)
}

type mlogRecoveryDiagnostics struct {
	log *mlog.Logger
}

func (d mlogRecoveryDiagnostics) ErrorContext(ctx context.Context, message string, err error) {
	if d.log == nil {
		return
	}
	fields := []mlog.Field{}
	if err != nil {
		fields = append(fields, mlog.Err(err))
	}
	d.log.ErrorContext(ctx, message, fields...)
}

// websocketLogger adapts mlog to the narrow websocket.Logger port so the
// sibling transport package never imports mlog.
type websocketLogger struct {
	log *mlog.Logger
}

func (l websocketLogger) WarnContext(ctx context.Context, message string, err error) {
	if l.log == nil {
		return
	}
	fields := []mlog.Field{}
	if err != nil {
		fields = append(fields, mlog.Err(err))
	}
	l.log.WarnContext(ctx, message, fields...)
}

// apiLogger adapts mlog to the narrow api.Logger port so the HTTP transport
// package never imports mlog.
type apiLogger struct {
	log *mlog.Logger
}

func (l apiLogger) InfoContext(ctx context.Context, message string, fields ...api.LogField) {
	if l.log == nil {
		return
	}
	l.log.InfoContext(ctx, message, apiLogFields(fields)...)
}

func (l apiLogger) ErrorContext(ctx context.Context, message string, fields ...api.LogField) {
	if l.log == nil {
		return
	}
	l.log.ErrorContext(ctx, message, apiLogFields(fields)...)
}

func apiLogFields(fields []api.LogField) []mlog.Field {
	out := make([]mlog.Field, 0, len(fields))
	for _, field := range fields {
		out = append(out, mlog.Any(field.Key, field.Value))
	}
	return out
}
