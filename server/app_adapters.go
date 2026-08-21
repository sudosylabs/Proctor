// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package server

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"sort"
	"sync"
	"time"

	mailpkg "github.com/sudosylabs/proctor/packages/mail"
	"github.com/sudosylabs/proctor/server/app"
	appmail "github.com/sudosylabs/proctor/server/app/mail"
	"github.com/sudosylabs/proctor/server/config"
	"github.com/sudosylabs/proctor/server/httpapi"
	"github.com/sudosylabs/proctor/server/logging"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/platform"
	"github.com/sudosylabs/proctor/server/platform/externalauth"
)

// applicationDependencies projects platform capabilities and deployment
// configuration into the explicit app.Dependencies bundle so package app never
// imports platform.
func applicationDependencies(
	capabilities constructionCapabilities,
	cfg config.Config,
	content app.FileContent,
	mailRenderer appmail.Renderer,
) (app.Dependencies, error) {
	auth := cfg.Authentication
	cache := platformAuthenticationCache{cache: capabilities.cache}
	log := capabilities.logger
	if content == nil {
		return app.Dependencies{}, errors.New("file content is nil")
	}
	mailSecretSealer, err := newMailSecretSealer(cfg.Mail)
	if err != nil {
		return app.Dependencies{}, err
	}
	if mailRenderer == nil {
		return app.Dependencies{}, errors.New("mail template renderer is nil")
	}
	mailer := accountMailerAdapter{mailer: capabilities.mailer}
	return app.Dependencies{
		Store:                   capabilities.persistence,
		Cache:                   cache,
		MailDeliverySender:      mailer,
		MailTemplateRenderer:    mailRenderer,
		MailDeliveryRecorder:    newMailDeliveryRecorder(log, nil),
		MailSecretSealer:        mailSecretSealer,
		Registry:                externalProviderRegistryAdapter{registry: capabilities.externalAuthentication},
		FileContent:             content,
		ExecutionHosts:          capabilities.executionHosts,
		NodeID:                  capabilities.nodeID,
		PublicURL:               cfg.Server.PublicURL,
		LoopbackHTTPDevelopment: explicitLoopbackHTTPDevelopment(cfg.Server.PublicURL),
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
		BootstrapProtection: app.BootstrapProtectionPolicy{Secret: cfg.Authentication.Bootstrap.Secret},
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
			NodeID: capabilities.nodeID,
		},
		RecentAuthenticationTTL:   auth.RecentAuthenticationTTL.Duration,
		AuthenticationDiagnostics: loggingAuthenticationDiagnostics{log: log},
		RealtimeDiagnostics:       loggingRealtimeDiagnostics{log: log},
		RecoveryDiagnostics:       loggingRecoveryDiagnostics{log: log},
	}, nil
}

// explicitLoopbackHTTPDevelopment projects the deliberately local HTTP
// origin into the application without coupling app/model to deployment
// configuration. Non-loopback HTTP never receives this capability.
func explicitLoopbackHTTPDevelopment(raw string) bool {
	parsed, err := url.Parse(raw)
	return err == nil && parsed.Scheme == "http" &&
		model.ValidateDesktopAuthorizationIssuer(raw, true) == nil
}

// platformAuthenticationCache adapts platform.Cache to app.authenticationCache.
type platformAuthenticationCache struct {
	cache borrowedCache
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
	mailer borrowedMailer
}

func (a accountMailerAdapter) Enabled() bool {
	return a.mailer.Enabled()
}

func (a accountMailerAdapter) From() appmail.Address {
	from := a.mailer.From()
	return appmail.Address{Name: from.Name, Address: from.Address}
}

func (a accountMailerAdapter) Send(ctx context.Context, message appmail.Outbound) (appmail.TransportOutcome, error) {
	_, err := a.mailer.Send(ctx, mailpkg.Message{
		From: mailpkg.Address{Name: message.From.Name, Address: message.From.Address}, EnvelopeFrom: message.EnvelopeFrom,
		To: []mailpkg.Address{{Name: message.To.Name, Address: message.To.Address}}, Subject: message.Subject,
		Text: message.Text, HTML: message.HTML, Headers: message.Headers, MessageID: message.MessageID, Date: message.Date,
	})
	if err == nil {
		return appmail.TransportUnknown, nil
	}
	return classifyMailTransportError(err), err
}

type portableMailOutcome interface {
	MailOutcome() string
}

func classifyMailTransportError(err error) appmail.TransportOutcome {
	var portable portableMailOutcome
	if errors.As(err, &portable) {
		switch portable.MailOutcome() {
		case "temporary":
			return appmail.TransportTemporary
		case "permanent":
			return appmail.TransportPermanent
		case "acceptance_uncertain":
			return appmail.TransportAcceptanceUncertain
		}
	}
	switch {
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded), errors.Is(err, mailpkg.ErrConnection):
		return appmail.TransportTemporary
	case errors.Is(err, mailpkg.ErrInvalidMessage), errors.Is(err, mailpkg.ErrInvalidAddress),
		errors.Is(err, mailpkg.ErrInvalidHeader), errors.Is(err, mailpkg.ErrMessageTooLarge),
		errors.Is(err, mailpkg.ErrTLS), errors.Is(err, mailpkg.ErrAuthentication),
		errors.Is(err, mailpkg.ErrRejected), errors.Is(err, mailpkg.ErrUnsupported):
		return appmail.TransportPermanent
	default:
		return appmail.TransportUnknown
	}
}

func (a accountMailerAdapter) Probe(ctx context.Context) error {
	return a.mailer.Test(ctx)
}

// externalProviderRegistryAdapter exposes the platform registry through the
// app-owned ExternalIdentityProvider port.
type externalProviderRegistryAdapter struct {
	registry *externalauth.Registry
}

func (a externalProviderRegistryAdapter) Descriptors() []model.ExternalAuthenticationProvider {
	return a.registry.Descriptors()
}

func (a externalProviderRegistryAdapter) Provider(
	id string,
) (app.ExternalIdentityProvider, bool) {
	provider, ok := a.registry.Provider(id)
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

type loggingAuthenticationDiagnostics struct {
	log runtimeLogger
}

func (d loggingAuthenticationDiagnostics) WarnContext(ctx context.Context, message string, err error) {
	if d.log == nil {
		return
	}
	fields := []logging.Field{}
	if err != nil {
		fields = append(fields, logging.Err(err))
	}
	d.log.WarnContext(ctx, message, fields...)
}

type loggingRealtimeDiagnostics struct {
	log runtimeLogger
}

func (d loggingRealtimeDiagnostics) ErrorContext(ctx context.Context, message string, err error) {
	if d.log == nil {
		return
	}
	fields := []logging.Field{}
	if err != nil {
		fields = append(fields, logging.Err(err))
	}
	d.log.ErrorContext(ctx, message, fields...)
}

func (d loggingRealtimeDiagnostics) ErrorContextWithEvent(
	ctx context.Context,
	message, event string,
	err error,
) {
	if d.log == nil {
		return
	}
	fields := []logging.Field{logging.String("event", event)}
	if err != nil {
		fields = append(fields, logging.Err(err))
	}
	d.log.ErrorContext(ctx, message, fields...)
}

type loggingRecoveryDiagnostics struct {
	log runtimeLogger
}

type mailDeliveryMetricKey struct {
	template model.MailTemplateKey
	state    model.MailDeliveryState
	code     string
}

type mailDeliveryMetricAggregate struct {
	count             uint64
	attempts          uint64
	processingLatency time.Duration
	maximumLatency    time.Duration
}

// operationalMailTelemetry is the production bounded metrics collector and
// structured-log adapter. Its dimensions come only from closed template,
// state, and public outcome vocabularies; it never retains identifiers,
// recipients, or message content.
type operationalMailTelemetry struct {
	log runtimeLogger

	mu           sync.Mutex
	deliveries   map[mailDeliveryMetricKey]mailDeliveryMetricAggregate
	queues       map[mailDeliveryMetricKey]app.MailQueueMetric
	queueBuckets map[mailDeliveryMetricKey]string
	health       string
}

type combinedMailDeliveryRecorder struct {
	metrics app.MailDeliveryRecorder
	logs    app.MailDeliveryRecorder
}

func newMailDeliveryRecorder(log runtimeLogger, metrics app.MailDeliveryRecorder) app.MailDeliveryRecorder {
	operational := &operationalMailTelemetry{
		log: log, deliveries: make(map[mailDeliveryMetricKey]mailDeliveryMetricAggregate),
		queues:       make(map[mailDeliveryMetricKey]app.MailQueueMetric),
		queueBuckets: make(map[mailDeliveryMetricKey]string),
	}
	if metrics == nil {
		return operational
	}
	return combinedMailDeliveryRecorder{metrics: metrics, logs: operational}
}

func (r combinedMailDeliveryRecorder) RecordMailDelivery(ctx context.Context, metric app.MailDeliveryMetric) {
	r.metrics.RecordMailDelivery(ctx, metric)
	r.logs.RecordMailDelivery(ctx, metric)
}

func (r combinedMailDeliveryRecorder) RecordMailQueueSnapshot(ctx context.Context, metrics []app.MailQueueMetric) {
	r.metrics.RecordMailQueueSnapshot(ctx, metrics)
	r.logs.RecordMailQueueSnapshot(ctx, metrics)
}

func (r combinedMailDeliveryRecorder) RecordMailHealth(ctx context.Context, metric app.MailHealthMetric) {
	r.metrics.RecordMailHealth(ctx, metric)
	r.logs.RecordMailHealth(ctx, metric)
}

func (r combinedMailDeliveryRecorder) Snapshot() app.MailMetricsSnapshot {
	return r.logs.Snapshot()
}

func (r *operationalMailTelemetry) RecordMailDelivery(ctx context.Context, metric app.MailDeliveryMetric) {
	key := mailDeliveryMetricKey{template: metric.TemplateKey, state: metric.State, code: metric.OutcomeCode}
	r.mu.Lock()
	aggregate := r.deliveries[key]
	aggregate.count++
	aggregate.attempts += uint64(metric.AttemptCount)
	aggregate.processingLatency += metric.ProcessingLatency
	if metric.ProcessingLatency > aggregate.maximumLatency {
		aggregate.maximumLatency = metric.ProcessingLatency
	}
	r.deliveries[key] = aggregate
	r.mu.Unlock()
	if r.log != nil {
		r.log.InfoContext(ctx, "mail delivery outcome",
			logging.String("template_key", string(metric.TemplateKey)),
			logging.String("state", string(metric.State)),
			logging.String("outcome_code", metric.OutcomeCode),
			logging.Int("attempt_count", metric.AttemptCount),
			logging.Duration("processing_latency", metric.ProcessingLatency),
		)
	}
}

func (r *operationalMailTelemetry) RecordMailQueueSnapshot(ctx context.Context, metrics []app.MailQueueMetric) {
	next := make(map[mailDeliveryMetricKey]app.MailQueueMetric, len(metrics))
	for _, metric := range metrics {
		key := mailDeliveryMetricKey{template: metric.TemplateKey, state: metric.State, code: metric.OutcomeCode}
		next[key] = metric
	}
	r.mu.Lock()
	previous := r.queues
	r.queues = next
	observations := make([]app.MailQueueMetric, 0, len(next)+len(previous))
	for key, metric := range next {
		bucket := mailQueueAgeBucket(metric.OldestAge)
		prior, exists := previous[key]
		priorBucket := r.queueBuckets[key]
		r.queueBuckets[key] = bucket
		if !exists || prior.Count != metric.Count || prior.HealthCode != metric.HealthCode ||
			prior.Truncated != metric.Truncated || priorBucket != bucket {
			observations = append(observations, metric)
		}
	}
	for key, metric := range previous {
		if _, exists := next[key]; exists {
			continue
		}
		metric.Count = 0
		metric.OldestAge = 0
		observations = append(observations, metric)
		delete(r.queueBuckets, key)
	}
	r.mu.Unlock()
	if r.log == nil {
		return
	}
	for _, metric := range observations {
		bucket := mailQueueAgeBucket(metric.OldestAge)
		r.log.InfoContext(ctx, "mail queue observation",
			logging.String("template_key", string(metric.TemplateKey)),
			logging.String("state", string(metric.State)),
			logging.String("outcome_code", metric.OutcomeCode),
			logging.Int64("count", metric.Count),
			logging.String("oldest_age_bucket", bucket),
			logging.String("health_code", metric.HealthCode),
			logging.Bool("truncated", metric.Truncated),
		)
	}
}

func (r *operationalMailTelemetry) RecordMailHealth(ctx context.Context, metric app.MailHealthMetric) {
	r.mu.Lock()
	changed := r.health != metric.Code
	r.health = metric.Code
	r.mu.Unlock()
	if changed && r.log != nil {
		r.log.InfoContext(ctx, "mail subsystem health", logging.String("health_code", metric.Code))
	}
}

func (r *operationalMailTelemetry) Snapshot() app.MailMetricsSnapshot {
	r.mu.Lock()
	defer r.mu.Unlock()
	snapshot := app.MailMetricsSnapshot{
		Deliveries: make([]app.MailDeliveryMetricAggregate, 0, len(r.deliveries)),
		Queues:     make([]app.MailQueueMetric, 0, len(r.queues)),
		HealthCode: r.health,
	}
	for key, aggregate := range r.deliveries {
		snapshot.Deliveries = append(snapshot.Deliveries, app.MailDeliveryMetricAggregate{
			TemplateKey: key.template, State: key.state, OutcomeCode: key.code,
			Count: aggregate.count, AttemptCount: aggregate.attempts,
			ProcessingLatency: aggregate.processingLatency, MaximumProcessingLatency: aggregate.maximumLatency,
		})
	}
	for _, metric := range r.queues {
		snapshot.Queues = append(snapshot.Queues, metric)
	}
	sort.Slice(snapshot.Deliveries, func(i, j int) bool {
		left, right := snapshot.Deliveries[i], snapshot.Deliveries[j]
		if left.TemplateKey != right.TemplateKey {
			return left.TemplateKey < right.TemplateKey
		}
		if left.State != right.State {
			return left.State < right.State
		}
		return left.OutcomeCode < right.OutcomeCode
	})
	sort.Slice(snapshot.Queues, func(i, j int) bool {
		left, right := snapshot.Queues[i], snapshot.Queues[j]
		if left.TemplateKey != right.TemplateKey {
			return left.TemplateKey < right.TemplateKey
		}
		if left.State != right.State {
			return left.State < right.State
		}
		return left.OutcomeCode < right.OutcomeCode
	})
	return snapshot
}

func mailQueueAgeBucket(age time.Duration) string {
	for _, boundary := range []time.Duration{5 * time.Minute, 15 * time.Minute, 30 * time.Minute, time.Hour, 3 * time.Hour, 12 * time.Hour, 24 * time.Hour} {
		if age < boundary {
			return fmt.Sprintf("lt_%s", boundary)
		}
	}
	return "gte_24h"
}

func (d loggingRecoveryDiagnostics) ErrorContext(ctx context.Context, message string, err error) {
	if d.log == nil {
		return
	}
	fields := []logging.Field{}
	if err != nil {
		fields = append(fields, logging.Err(err))
	}
	d.log.ErrorContext(ctx, message, fields...)
}

// websocketLogger adapts logging to the narrow websocket.Logger port so the
// sibling transport package never imports logging.
type websocketLogger struct {
	log runtimeLogger
}

func (l websocketLogger) WarnContext(ctx context.Context, message string, err error) {
	if l.log == nil {
		return
	}
	fields := []logging.Field{}
	if err != nil {
		fields = append(fields, logging.Err(err))
	}
	l.log.WarnContext(ctx, message, fields...)
}

// apiLogger adapts logging to the narrow httpapi.Logger port so the HTTP transport
// package never imports logging.
type apiLogger struct {
	log runtimeLogger
}

func (l apiLogger) InfoContext(ctx context.Context, message string, fields ...httpapi.LogField) {
	if l.log == nil {
		return
	}
	l.log.InfoContext(ctx, message, apiLogFields(fields)...)
}

func (l apiLogger) ErrorContext(ctx context.Context, message string, fields ...httpapi.LogField) {
	if l.log == nil {
		return
	}
	l.log.ErrorContext(ctx, message, apiLogFields(fields)...)
}

func apiLogFields(fields []httpapi.LogField) []logging.Field {
	out := make([]logging.Field, 0, len(fields))
	for _, field := range fields {
		out = append(out, logging.Any(field.Key, field.Value))
	}
	return out
}
