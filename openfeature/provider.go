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

	"github.com/DataDog/dd-trace-go/v2/internal"
	"github.com/DataDog/dd-trace-go/v2/internal/log"
	"github.com/open-feature/go-sdk/openfeature"
)

var _ openfeature.FeatureProvider = (*DatadogProvider)(nil)
var _ openfeature.ContextAwareStateHandler = (*DatadogProvider)(nil)
var _ openfeature.StateHandler = (*DatadogProvider)(nil)

// Sentinel errors for error classification
var (
	errFlagNotFound    = errors.New("flag not found")
	errTypeMismatch    = errors.New("type mismatch")
	errParseError      = errors.New("parse error")
	errNoConfiguration = errors.New("no configuration loaded")
)

const (
	// ffeProductEnvVar is the environment variable to enable the experimental flagging provider
	ffeProductEnvVar = "DD_EXPERIMENTAL_FLAGGING_PROVIDER_ENABLED"
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
}

// DatadogProvider is an OpenFeature provider that evaluates feature flags
// using configuration received from Datadog Remote Config.
type DatadogProvider struct {
	mu            sync.RWMutex
	configuration *universalFlagsConfiguration
	metadata      openfeature.Metadata

	configChange sync.Cond

	// Exposure tracking
	exposureWriter *exposureWriter
	exposureHook   *exposureHook
}

// NewDatadogProvider creates a new Datadog OpenFeature provider with default configuration.
// It subscribes to Remote Config updates and automatically updates the provider's configuration
// when new flag configurations are received.
//
// The provider will be ready to use immediately, but flag evaluations will return errors
// until the first configuration is received from Remote Config.
//
// Returns an error if the default configuration of the Remote Config client is NOT working
// In this case, please call tracer.Start before creating the provider.
func NewDatadogProvider(config ProviderConfig) (openfeature.FeatureProvider, error) {
	if !internal.BoolEnv(ffeProductEnvVar, false) {
		log.Error("openfeature: experimental flagging provider is not enabled, please set %s=true to enable it", ffeProductEnvVar)
		return &openfeature.NoopProvider{}, nil
	}

	return startWithRemoteConfig(config)
}

func newDatadogProvider(config ProviderConfig) *DatadogProvider {
	// Create exposure writer
	writer := newExposureWriter(config)

	// Create exposure hook
	hook := newExposureHook(writer)

	p := &DatadogProvider{
		metadata: openfeature.Metadata{
			Name: "Datadog Remote Config Provider",
		},
		exposureWriter: writer,
		exposureHook:   hook,
	}
	p.configChange.L = &p.mu
	return p
}

// updateConfiguration updates the provider's flag configuration.
// This is called by the Remote Config callback when new configuration is received.
func (p *DatadogProvider) updateConfiguration(config *universalFlagsConfiguration) {
	p.mu.Lock()
	defer p.mu.Unlock()
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

	for p.configuration == nil {
		if err := p.waitForConfigurationUpdate(ctx); err != nil {
			return err
		}
	}

	// Start periodic flushing
	p.exposureWriter.start()
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
	// Create a channel to signal completion
	done := make(chan error, 1)

	go func() {
		// Perform the shutdown operations
		err := stopRemoteConfig()
		// Stop the exposure writer
		if p.exposureWriter != nil {
			p.exposureWriter.flush()
			p.exposureWriter.stop()
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
		// Accept float64 if it's a whole number
		if v == float64(int64(v)) {
			intValue = int64(v)
		} else {
			conversionErr = fmt.Errorf("%w: flag %q returned float with decimal part: %v", errParseError, flagKey, v)
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

// Hooks returns the hooks for this provider.
// This includes the exposure tracking hook.
func (p *DatadogProvider) Hooks() []openfeature.Hook {
	if p.exposureHook != nil {
		return []openfeature.Hook{p.exposureHook}
	}
	return []openfeature.Hook{}
}

// evaluate is the core evaluation method that all type-specific methods use.
func (p *DatadogProvider) evaluate(
	ctx context.Context,
	flagKey string,
	defaultValue any,
	flatCtx openfeature.FlattenedContext,
) (res evaluationResult) {
	log.Debug("openfeature: evaluating flag %q", flagKey)
	defer func() {
		log.Debug("openfeature: evaluated flag %q: value=%v, reason=%s, error=%v", flagKey, res.Value, res.Reason, res.Error)
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

	// Find the flag
	flag, exists := config.Flags[flagKey]
	if !exists {
		return evaluationResult{
			Value:  defaultValue,
			Reason: openfeature.ErrorReason,
			Error:  fmt.Errorf("%w: %q", errFlagNotFound, flagKey),
		}
	}

	// Evaluate the flag (pass context for potential future use in evaluateFlag)
	return evaluateFlag(flag, defaultValue, flatCtx)
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
		return openfeature.NewGeneralResolutionError(errMsg)
	default:
		return openfeature.NewGeneralResolutionError(errMsg)
	}
}
