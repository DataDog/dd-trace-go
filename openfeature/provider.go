// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package openfeature

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/open-feature/go-sdk/openfeature"

	"github.com/DataDog/dd-trace-go/v2/internal"
	internalconfig "github.com/DataDog/dd-trace-go/v2/internal/config"
	"github.com/DataDog/dd-trace-go/v2/internal/log"
	internalffe "github.com/DataDog/dd-trace-go/v2/internal/openfeature"
)

var _ openfeature.FeatureProvider = (*DatadogProvider)(nil)
var _ openfeature.ContextAwareStateHandler = (*DatadogProvider)(nil)
var _ openfeature.StateHandler = (*DatadogProvider)(nil)

// Sentinel errors for error classification
var (
	errFlagNotFound        = errors.New("flag not found")
	errTypeMismatch        = errors.New("type mismatch")
	errParseError          = errors.New("parse error")
	errNoConfiguration     = errors.New("no configuration loaded")
	errTargetingKeyMissing = errors.New("targeting key missing")
)

const (
	spanEnrichmentEnvVar = "DD_EXPERIMENTAL_FLAGGING_PROVIDER_SPAN_ENRICHMENT_ENABLED"
	// flagEvalCountsEnabledEnvVar is the operator killswitch for the EVP flagevaluation emission path.
	// Default: true (EVP path is ON by default). Set to "false" to disable only the EVP path
	// while leaving the OTel feature_flag.evaluations path unaffected.
	flagEvalCountsEnabledEnvVar = "DD_FLAGGING_EVALUATION_COUNTS_ENABLED"
	// Default timeout for provider initialization
	defaultInitTimeout = 30 * time.Second
	// Default timeout for provider shutdown
	defaultShutdownTimeout = 30 * time.Second
)

// ProviderConfig contains configuration options for the Datadog OpenFeature provider
type ProviderConfig struct {
	// ExposureFlushInterval is the interval at which exposure events are flushed to the agent
	// Default: 1 second
	ExposureFlushInterval time.Duration

	// FlagEvaluationFlushInterval is the interval for flushing EVP flag evaluation events.
	// Default: 10 seconds. Leave zero to use the default.
	FlagEvaluationFlushInterval time.Duration
}

// DatadogProvider is an OpenFeature provider that evaluates feature flags
// using configuration received from Datadog Remote Config.
type DatadogProvider struct {
	mu            sync.RWMutex
	configuration *universalFlagsConfiguration
	metadata      openfeature.Metadata

	configChange sync.Cond

	hooks []openfeature.Hook

	// Exposure tracking
	exposureWriter *exposureWriter

	// Flag evaluation metrics hook (OTel counter via Finally hook)
	flagEvalMetricsHook *flagEvalMetricsHook

	// Flag evaluation EVP writer + hook (new Path B — EVP flagevaluation track).
	// Both fields are nil when DD_FLAGGING_EVALUATION_COUNTS_ENABLED=false (killswitch).
	// Named distinctly from flagEvalHook (OTel) to avoid collisions.
	flagEvalLoggingWriter *flagEvalLoggingWriter
	flagEvalLoggingHook   *flagEvalLoggingHook

	// source is fixed at construction. // +checklocks:mu
	source internalffe.Source
	// agentless is non-nil only once startWithAgentless has registered it. // +checklocks:mu
	agentless *agentlessSource
	// activated reports whether a delivery source has been registered. // +checklocks:mu
	activated bool
	// shutdownCalled reports whether ShutdownWithContext has already run. // +checklocks:mu
	shutdownCalled bool
	// deliveryErr is set when no delivery source could start; permanent for the process. // +checklocks:mu
	deliveryErr error
	// writersStarted makes Init idempotent. // +checklocks:mu
	writersStarted bool
}

// NewDatadogProvider creates a new Datadog OpenFeature provider with default configuration.
// Depending on DD_FEATURE_FLAGS_CONFIGURATION_SOURCE (default: agentless), it either polls
// Datadog directly over HTTPS or subscribes to Remote Config updates, and automatically updates
// the provider's configuration when new flag configurations are received.
//
// The provider will be ready to use immediately, but flag evaluations will return errors
// until the first configuration is received.
//
// Returns an error if the remote_config source is selected and the default configuration of the
// Remote Config client is NOT working. In this case, please call tracer.Start before creating
// the provider.
func NewDatadogProvider(config ProviderConfig) (openfeature.FeatureProvider, error) {
	settings := internalffe.ResolveSettings(internalconfig.Get())
	if settings.LegacyKeyDecided {
		warnLegacyFlaggingProviderOnce()
	}

	switch settings.Source {
	case internalffe.SourceRemoteConfig:
		return startWithRemoteConfig(config)
	case internalffe.SourceAgentless:
		return startWithAgentless(config, settings)
	default:
		return &openfeature.NoopProvider{}, nil
	}
}

var warnLegacyFlaggingProviderOnce = sync.OnceFunc(func() {
	log.Warn("openfeature: DD_EXPERIMENTAL_FLAGGING_PROVIDER_ENABLED is deprecated; use DD_FEATURE_FLAGS_CONFIGURATION_SOURCE instead")
})

// newDatadogProvider builds a provider defaulting to the Remote Config
// source. It exists so the ~60 existing tests exercising evaluation, hooks,
// and metrics — none of which care about the delivery source — don't need to
// be touched; production code paths call newDatadogProviderWithSource.
func newDatadogProvider(config ProviderConfig) *DatadogProvider {
	return newDatadogProviderWithSource(config, internalffe.SourceRemoteConfig)
}

func newDatadogProviderWithSource(config ProviderConfig, source internalffe.Source) *DatadogProvider {
	evp := newEVPClient()

	// Create exposure writer
	writer := newExposureWriterWithEVP(config, evp)

	// Create exposure hook
	exposureLoggingHook := newExposureHook(writer)

	// Create flag evaluation metrics (noop if DD_METRICS_OTEL_ENABLED != true)
	metrics, err := newFlagEvalMetrics()
	if err != nil {
		log.Error("openfeature: failed to create flag evaluation metrics: %v", err.Error())
	}
	evalMetricsHook := newFlagEvalMetricsHook(metrics)

	// Conditionally construct the EVP flagevaluation writer + hook.
	// Gated by DD_FLAGGING_EVALUATION_COUNTS_ENABLED (default true).
	// When false, both fields are left nil and the EVP path is disabled.
	// The OTel hook (flagEvalHook above) is registered unconditionally.
	var evalWriter *flagEvalLoggingWriter
	var evalLoggingHook *flagEvalLoggingHook
	if internal.BoolEnv(flagEvalCountsEnabledEnvVar, true) {
		evalWriter = newFlagEvalLoggingWriterWithEVP(config, evp)
		evalLoggingHook = newFlagEvalLoggingHook(evalWriter)
	}

	var spanEnrichmentHook *spanEnrichmentHook
	if internal.BoolEnv(spanEnrichmentEnvVar, false) {
		spanEnrichmentHook = newSpanEnrichmentHook()
		log.Debug("openfeature: span enrichment is enabled")
	} else {
		log.Debug("openfeature: span enrichment is disabled")
	}

	hooks := make([]openfeature.Hook, 0, 4)
	if exposureLoggingHook != nil {
		hooks = append(hooks, exposureLoggingHook)
	}
	if evalMetricsHook != nil {
		hooks = append(hooks, evalMetricsHook)
	}
	if evalLoggingHook != nil {
		hooks = append(hooks, evalLoggingHook)
	}
	if spanEnrichmentHook != nil {
		hooks = append(hooks, spanEnrichmentHook)
	}

	p := &DatadogProvider{
		metadata: openfeature.Metadata{
			Name: "Datadog Provider",
		},
		hooks:                 hooks,
		exposureWriter:        writer,
		flagEvalMetricsHook:   evalMetricsHook,
		flagEvalLoggingWriter: evalWriter,
		flagEvalLoggingHook:   evalLoggingHook,
		source:                source,
	}
	p.configChange.L = &p.mu

	return p
}

// updateConfiguration updates the provider's flag configuration.
// This is called by the Remote Config callback when new configuration is received.
// startWithAgentless registers an Agentless configuration source as the
// provider's activated delivery source. Claiming activation and registering
// the source happen under the same lock as the shutdownCalled check, so a
// poller can never be registered after Shutdown — that would otherwise leak
// a billable poller for the process lifetime. src.start() runs outside the
// lock since it issues the first request synchronously.
func startWithAgentless(config ProviderConfig, settings internalffe.Settings) (*DatadogProvider, error) {
	p := newDatadogProviderWithSource(config, internalffe.SourceAgentless)

	src, err := newAgentlessSource(settings, p.updateConfiguration)
	if err != nil {
		// err never contains the configured endpoint or credentials.
		log.Error("openfeature: failed to start agentless configuration source: %v", err.Error())
		p.mu.Lock()
		p.deliveryErr = err
		p.mu.Unlock()
		return p, nil
	}

	if !p.tryRegisterAgentless(src) {
		return p, nil
	}

	src.start()
	return p, nil
}

// tryRegisterAgentless registers src as the provider's active delivery
// source unless Shutdown has already run, in which case it registers
// nothing so a poller can never outlive Shutdown. Returns whether it
// registered src.
func (p *DatadogProvider) tryRegisterAgentless(src *agentlessSource) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.shutdownCalled {
		return false
	}
	p.agentless = src
	p.activated = true
	return true
}

// markActivated records that a delivery source has been registered, unless
// Shutdown already ran.
func (p *DatadogProvider) markActivated() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.shutdownCalled {
		return
	}
	p.activated = true
}

func (p *DatadogProvider) updateConfiguration(config *universalFlagsConfiguration) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.shutdownCalled {
		// A poll or RC callback already in flight must not resurrect
		// configuration after Shutdown.
		return
	}
	p.configuration = config
	p.configChange.Broadcast()
}

// getConfiguration returns the current configuration (for testing purposes).
func (p *DatadogProvider) getConfiguration() *universalFlagsConfiguration {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.configuration
}

// Metadata returns provider metadata including the provider name.
func (p *DatadogProvider) Metadata() openfeature.Metadata {
	return p.metadata
}

// Init initializes the provider. For the Datadog provider,
// this is waiting for the first configuration to be loaded.
func (p *DatadogProvider) Init(evaluationContext openfeature.EvaluationContext) error {
	// Use a background context with a reasonable timeout for backward compatibility
	ctx, cancel := context.WithTimeout(context.Background(), defaultInitTimeout)
	defer cancel()
	return p.InitWithContext(ctx, evaluationContext)
}

// waitForConfigurationUpdate waits for a configuration update or context cancellation.
// Assumes mutex is locked on entry, temporarily unlocks during wait, relocks on exit.
func (p *DatadogProvider) waitForConfigurationUpdate(ctx context.Context) error {
	defer p.mu.Lock() // Always relock when function exits

	// Check if context was cancelled before waiting
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}

	// Create channel to signal condition variable completion
	done := make(chan struct{})
	go func() {
		defer close(done)
		p.mu.Lock()
		defer p.mu.Unlock()
		p.configChange.Wait()
	}()

	// Temporarily unlock to allow configuration update and context handling
	p.mu.Unlock()

	// Wait for either context cancellation or configuration update
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-done:
		return nil // Configuration updated, defer will relock
	}
}

// InitWithContext initializes the provider with context support.
// This method respects context cancellation and timeouts, allowing users
// to cancel the initialization process if needed.
func (p *DatadogProvider) InitWithContext(ctx context.Context, _ openfeature.EvaluationContext) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.deliveryErr != nil {
		// Permanent: no delivery source could be started, so waiting out the
		// timeout would only delay startup for configuration that can never arrive.
		return &openfeature.ProviderInitError{
			ErrorCode: openfeature.ProviderNotReadyCode,
			Message:   "no feature-flag delivery source could be started",
		}
	}

	for p.configuration == nil {
		if p.shutdownCalled {
			// Shutdown ran while Init was waiting: configuration will never
			// arrive, so return instead of waiting out the full timeout.
			return nil
		}
		if err := p.waitForConfigurationUpdate(ctx); err != nil {
			return err
		}
	}

	if !p.writersStarted {
		// Start periodic flushing for exposure writer.
		p.exposureWriter.start()
		// Start periodic flushing for EVP flag evaluation writer (nil when killswitch disabled).
		if p.flagEvalLoggingWriter != nil {
			p.flagEvalLoggingWriter.start()
		}
		p.writersStarted = true
	}
	return nil
}

// Shutdown shuts down the provider and stops Remote Config updates.
func (p *DatadogProvider) Shutdown() {
	// Use a background context with a reasonable timeout for backward compatibility
	ctx, cancel := context.WithTimeout(context.Background(), defaultShutdownTimeout)
	defer cancel()
	_ = p.ShutdownWithContext(ctx)
}

// ShutdownWithContext shuts down the provider with context support.
// This method respects context cancellation and timeouts, allowing users
// to control how long the shutdown process should take.
func (p *DatadogProvider) ShutdownWithContext(ctx context.Context) error {
	// Claim shutdown and copy out the components to tear down while still
	// holding the lock, then Broadcast so a parked Init wakes up instead of
	// waiting out its timeout. The teardown itself must run without the
	// lock held: agentless.Stop joins the poll goroutine, which itself calls
	// updateConfiguration and takes p.mu, so holding the lock across it
	// would deadlock.
	p.mu.Lock()
	p.shutdownCalled = true
	source := p.source
	agentless := p.agentless
	p.configuration = nil
	p.configChange.Broadcast()
	p.mu.Unlock()

	done := make(chan error, 1)
	go func() {
		var err error
		// An agentless provider never registered an RC capability, so it
		// must not unregister one.
		if source == internalffe.SourceRemoteConfig {
			err = stopRemoteConfig()
		}
		if agentless != nil {
			agentless.Stop(ctx)
		}

		p.mu.Lock()
		defer p.mu.Unlock()
		// Stop the exposure writer
		if p.exposureWriter != nil {
			p.exposureWriter.flush()
			p.exposureWriter.stop()
		}
		// Stop the EVP flag evaluation writer (nil when killswitch disabled).
		if p.flagEvalLoggingWriter != nil {
			p.flagEvalLoggingWriter.stop()
		}
		// Shut down flag evaluation metrics
		if p.flagEvalMetricsHook != nil && p.flagEvalMetricsHook.metrics != nil {
			_ = p.flagEvalMetricsHook.metrics.shutdown(ctx)
		}
		done <- err
	}()

	// Wait for completion or context cancellation
	select {
	case <-ctx.Done():
		return ctx.Err()
	case err := <-done:
		return err
	}
}

// BooleanEvaluation evaluates a boolean feature flag.
func (p *DatadogProvider) BooleanEvaluation(
	ctx context.Context,
	flagKey string,
	defaultValue bool,
	flatCtx openfeature.FlattenedContext,
) openfeature.BoolResolutionDetail {
	result := p.evaluate(ctx, flagKey, defaultValue, flatCtx)

	// Convert result to boolean
	boolValue, ok := result.Value.(bool)
	if !ok && result.Error == nil {
		result.Error = fmt.Errorf("%w: flag %q returned non-boolean value: %T", errTypeMismatch, flagKey, result.Value)
		result.Reason = openfeature.ErrorReason
		boolValue = defaultValue
	}

	return openfeature.BoolResolutionDetail{
		Value: boolValue,
		ProviderResolutionDetail: openfeature.ProviderResolutionDetail{
			ResolutionError: toResolutionError(result.Error),
			Reason:          result.Reason,
			Variant:         result.VariantKey,
			FlagMetadata:    result.Metadata,
		},
	}
}

// StringEvaluation evaluates a string feature flag.
func (p *DatadogProvider) StringEvaluation(
	ctx context.Context,
	flagKey string,
	defaultValue string,
	flatCtx openfeature.FlattenedContext,
) openfeature.StringResolutionDetail {
	result := p.evaluate(ctx, flagKey, defaultValue, flatCtx)

	// Convert result to string
	strValue, ok := result.Value.(string)
	if !ok && result.Error == nil {
		result.Error = fmt.Errorf("%w: flag %q returned non-string value: %T", errTypeMismatch, flagKey, result.Value)
		result.Reason = openfeature.ErrorReason
		strValue = defaultValue
	}

	return openfeature.StringResolutionDetail{
		Value: strValue,
		ProviderResolutionDetail: openfeature.ProviderResolutionDetail{
			ResolutionError: toResolutionError(result.Error),
			Reason:          result.Reason,
			Variant:         result.VariantKey,
			FlagMetadata:    result.Metadata,
		},
	}
}

// FloatEvaluation evaluates a numeric (float) feature flag.
func (p *DatadogProvider) FloatEvaluation(
	ctx context.Context,
	flagKey string,
	defaultValue float64,
	flatCtx openfeature.FlattenedContext,
) openfeature.FloatResolutionDetail {
	result := p.evaluate(ctx, flagKey, defaultValue, flatCtx)

	// Convert result to float64
	var floatValue float64
	var conversionErr error

	switch v := result.Value.(type) {
	case float64:
		floatValue = v
	case float32:
		floatValue = float64(v)
	case int:
		floatValue = float64(v)
	case int64:
		floatValue = float64(v)
	case int32:
		floatValue = float64(v)
	default:
		if result.Error == nil {
			conversionErr = fmt.Errorf("%w: flag %q returned non-numeric value: %T", errTypeMismatch, flagKey, result.Value)
			result.Reason = openfeature.ErrorReason
		}
		floatValue = defaultValue
	}

	if conversionErr != nil {
		result.Error = conversionErr
	}

	return openfeature.FloatResolutionDetail{
		Value: floatValue,
		ProviderResolutionDetail: openfeature.ProviderResolutionDetail{
			ResolutionError: toResolutionError(result.Error),
			Reason:          result.Reason,
			Variant:         result.VariantKey,
			FlagMetadata:    result.Metadata,
		},
	}
}

// IntEvaluation evaluates an integer feature flag.
func (p *DatadogProvider) IntEvaluation(
	ctx context.Context,
	flagKey string,
	defaultValue int64,
	flatCtx openfeature.FlattenedContext,
) openfeature.IntResolutionDetail {
	result := p.evaluate(ctx, flagKey, defaultValue, flatCtx)

	// Convert result to int64
	var intValue int64
	var conversionErr error

	switch v := result.Value.(type) {
	case int64:
		intValue = v
	case int:
		intValue = int64(v)
	case int32:
		intValue = int64(v)
	case int16:
		intValue = int64(v)
	case int8:
		intValue = int64(v)
	case float64:
		// Accept float64 if it's a whole number (e.g., -5.0 → -5)
		if v == float64(int64(v)) {
			intValue = int64(v)
		} else {
			conversionErr = fmt.Errorf("%w: flag %q returned float with decimal part: %v", errTypeMismatch, flagKey, v)
		}
	default:
		if result.Error == nil {
			conversionErr = fmt.Errorf("%w: flag %q returned non-integer value: %T", errTypeMismatch, flagKey, result.Value)
			result.Reason = openfeature.ErrorReason
		}
		intValue = defaultValue
	}

	if conversionErr != nil {
		result.Error = conversionErr
	}

	return openfeature.IntResolutionDetail{
		Value: intValue,
		ProviderResolutionDetail: openfeature.ProviderResolutionDetail{
			ResolutionError: toResolutionError(result.Error),
			Reason:          result.Reason,
			Variant:         result.VariantKey,
			FlagMetadata:    result.Metadata,
		},
	}
}

// ObjectEvaluation evaluates a structured (JSON) feature flag.
func (p *DatadogProvider) ObjectEvaluation(
	ctx context.Context,
	flagKey string,
	defaultValue any,
	flatCtx openfeature.FlattenedContext,
) openfeature.InterfaceResolutionDetail {
	result := p.evaluate(ctx, flagKey, defaultValue, flatCtx)

	return openfeature.InterfaceResolutionDetail{
		Value: result.Value,
		ProviderResolutionDetail: openfeature.ProviderResolutionDetail{
			ResolutionError: toResolutionError(result.Error),
			Reason:          result.Reason,
			Variant:         result.VariantKey,
			FlagMetadata:    result.Metadata,
		},
	}
}

// Hooks returns the provider's hooks, built once during Init.
// Returns p.hooks directly to avoid per-evaluation allocations.
func (p *DatadogProvider) Hooks() []openfeature.Hook {
	return p.hooks
}

// evaluate is the core evaluation method that all type-specific methods use.
func (p *DatadogProvider) evaluate(
	ctx context.Context,
	flagKey string,
	defaultValue any,
	flatCtx openfeature.FlattenedContext,
) (res evaluationResult) {
	// Capture the evaluation time once, at evaluation entry. It is used for allocation
	// time-window checks and EVP first/last evaluation bounds.
	evalNow := time.Now()
	log.Debug("openfeature: evaluating flag %q", flagKey)

	// Consent for this evaluation, stamped onto res.Metadata by the defer below. Stays false
	// on paths with no configuration (cancelled context, provider not ready) — an evaluation
	// with no environment behind it withholds consent.
	var observeFullEvaluationData bool
	defer func() {
		if res.Metadata == nil {
			res.Metadata = make(map[string]any, 2)
		}
		res.Metadata[metadataEvalTimeKey] = evalNow.UnixMilli()
		res.Metadata[metadataObserveFullEvaluationDataKey] = observeFullEvaluationData
	}()

	// Check if context was cancelled before starting evaluation
	select {
	case <-ctx.Done():
		return evaluationResult{
			Value:  defaultValue,
			Reason: openfeature.ErrorReason,
			Error:  ctx.Err(),
		}
	default:
	}

	config := p.getConfiguration()

	// Check if configuration is loaded
	if config == nil {
		return evaluationResult{
			Value:  defaultValue,
			Reason: openfeature.ErrorReason,
			Error:  errNoConfiguration,
		}
	}

	// Snapshot consent before evaluating, so a Remote Config swap of p.configuration mid-eval
	// cannot change the value stamped on the result.
	observeFullEvaluationData = config.ObserveFullEvaluationData

	// Evaluate the flag, sharing the eval-time captured at entry.
	return evaluateConfiguredFlag(config, flagKey, defaultValue, flatCtx, evalNow)
}

// toResolutionError converts a Go error to an OpenFeature ResolutionError.
// It uses errors.Is to check for wrapped sentinel errors instead of string matching.
func toResolutionError(err error) openfeature.ResolutionError {
	if err == nil {
		return openfeature.ResolutionError{}
	}

	errMsg := err.Error()

	// Check for wrapped sentinel errors using errors.Is
	switch {
	case errors.Is(err, errFlagNotFound):
		return openfeature.NewFlagNotFoundResolutionError(errMsg)
	case errors.Is(err, errTypeMismatch):
		return openfeature.NewTypeMismatchResolutionError(errMsg)
	case errors.Is(err, errParseError):
		return openfeature.NewParseErrorResolutionError(errMsg)
	case errors.Is(err, errNoConfiguration):
		return openfeature.NewProviderNotReadyResolutionError(errMsg)
	case errors.Is(err, errTargetingKeyMissing):
		return openfeature.NewTargetingKeyMissingResolutionError(errMsg)
	default:
		return openfeature.NewGeneralResolutionError(errMsg)
	}
}
