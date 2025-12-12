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

	"github.com/DataDog/dd-trace-go/v2/internal"
	"github.com/DataDog/dd-trace-go/v2/internal/log"
	"github.com/open-feature/go-sdk/openfeature"
)

var _ openfeature.FeatureProvider = (*DatadogProvider)(nil)

// Sentinel errors for error classification
var (
	errFlagNotFound    = errors.New("flag not found")
	errTypeMismatch    = errors.New("type mismatch")
	errParseError      = errors.New("parse error")
	errNoConfiguration = errors.New("no configuration loaded")
)

const ffeProductEnvVar = "DD_EXPERIMENTAL_FLAGGING_PROVIDER_ENABLED"

// DataSource represents the configuration data source for the OpenFeature provider.
type DataSource int

const (
	// DataSourceRemoteConfig uses Datadog Remote Config as the data source (default).
	DataSourceRemoteConfig DataSource = iota
	// DataSourceFlagRules uses an HTTP endpoint as the data source for flag rules.
	DataSourceFlagRules
)

// DatadogProvider is an OpenFeature provider that evaluates feature flags
// using configuration received from Datadog Remote Config or a flag rules backend.
type DatadogProvider struct {
	mu            sync.RWMutex
	configuration *universalFlagsConfiguration
	metadata      openfeature.Metadata

	configChange sync.Cond

	// flagRulesBackend is set when using the flag rules data source
	flagRulesBackend *flagRulesBackend
}

// ProviderConfig holds configuration for the Datadog OpenFeature provider.
type ProviderConfig struct {
	// DataSource specifies where to fetch flag configurations from.
	// Default: DataSourceRemoteConfig
	DataSource DataSource

	// FlagRules contains flag rules backend configuration.
	// Only used when DataSource is DataSourceFlagRules.
	FlagRules FlagRulesConfig
}

// NewDatadogProvider creates a new Datadog OpenFeature provider.
// It subscribes to Remote Config updates (default) or polls an HTTP endpoint
// for flag configurations based on the ProviderConfig.DataSource setting.
//
// The provider will be ready to use immediately, but flag evaluations will return errors
// until the first configuration is received.
//
// For Remote Config (default): Returns an error if the Remote Config client cannot be initialized.
// In this case, please call tracer.Start before creating the provider.
//
// For FlagRules: Returns an error if no URL is configured via ProviderConfig.FlagRules.URL
// or DD_FFE_FLAG_RULES_URL environment variable.
func NewDatadogProvider(config ProviderConfig) (openfeature.FeatureProvider, error) {
	if !internal.BoolEnv(ffeProductEnvVar, false) {
		log.Error("openfeature: experimental flagging provider is not enabled, please set %s=true to enable it", ffeProductEnvVar)
		return &openfeature.NoopProvider{}, nil
	}

	switch config.DataSource {
	case DataSourceFlagRules:
		return startWithFlagRules(config.FlagRules)
	case DataSourceRemoteConfig:
		fallthrough
	default:
		return startWithRemoteConfig()
	}
}

func newDatadogProvider() *DatadogProvider {
	p := &DatadogProvider{
		metadata: openfeature.Metadata{
			Name: "Datadog Remote Config Provider",
		},
	}
	p.configChange.L = &p.mu
	return p
}

// startWithFlagRules creates a provider that fetches configuration from a flag rules backend.
func startWithFlagRules(flagRulesConfig FlagRulesConfig) (*DatadogProvider, error) {
	provider := newDatadogProvider()
	provider.metadata = openfeature.Metadata{
		Name: "Datadog Flag Rules Provider",
	}

	backend, err := newFlagRulesBackend(flagRulesConfig, provider)
	if err != nil {
		return nil, fmt.Errorf("failed to create flag rules backend: %w", err)
	}

	provider.flagRulesBackend = backend
	backend.Start()

	log.Debug("openfeature: successfully started flag rules backend")
	return provider, nil
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
func (p *DatadogProvider) Init(openfeature.EvaluationContext) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	for p.configuration == nil {
		p.configChange.Wait()
	}

	return nil
}

// Shutdown shuts down the provider and stops configuration updates.
func (p *DatadogProvider) Shutdown() {
	// Stop flag rules backend if using flag rules data source
	if p.flagRulesBackend != nil {
		_ = p.flagRulesBackend.Stop()
		return
	}
	// Otherwise, stop Remote Config
	_ = stopRemoteConfig()
}

// BooleanEvaluation evaluates a boolean feature flag.
func (p *DatadogProvider) BooleanEvaluation(
	_ context.Context,
	flagKey string,
	defaultValue bool,
	flatCtx openfeature.FlattenedContext,
) openfeature.BoolResolutionDetail {
	result := p.evaluate(flagKey, defaultValue, flatCtx)

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
		},
	}
}

// StringEvaluation evaluates a string feature flag.
func (p *DatadogProvider) StringEvaluation(
	_ context.Context,
	flagKey string,
	defaultValue string,
	flatCtx openfeature.FlattenedContext,
) openfeature.StringResolutionDetail {
	result := p.evaluate(flagKey, defaultValue, flatCtx)

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
		},
	}
}

// FloatEvaluation evaluates a numeric (float) feature flag.
func (p *DatadogProvider) FloatEvaluation(
	_ context.Context,
	flagKey string,
	defaultValue float64,
	flatCtx openfeature.FlattenedContext,
) openfeature.FloatResolutionDetail {
	result := p.evaluate(flagKey, defaultValue, flatCtx)

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
		},
	}
}

// IntEvaluation evaluates an integer feature flag.
func (p *DatadogProvider) IntEvaluation(
	_ context.Context,
	flagKey string,
	defaultValue int64,
	flatCtx openfeature.FlattenedContext,
) openfeature.IntResolutionDetail {
	result := p.evaluate(flagKey, defaultValue, flatCtx)

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
		},
	}
}

// ObjectEvaluation evaluates a structured (JSON) feature flag.
func (p *DatadogProvider) ObjectEvaluation(
	_ context.Context,
	flagKey string,
	defaultValue any,
	flatCtx openfeature.FlattenedContext,
) openfeature.InterfaceResolutionDetail {
	result := p.evaluate(flagKey, defaultValue, flatCtx)

	return openfeature.InterfaceResolutionDetail{
		Value: result.Value,
		ProviderResolutionDetail: openfeature.ProviderResolutionDetail{
			ResolutionError: toResolutionError(result.Error),
			Reason:          result.Reason,
			Variant:         result.VariantKey,
		},
	}
}

// Hooks returns the hooks for this provider.
// Currently returns an empty slice as we don't have provider-level hooks.
func (p *DatadogProvider) Hooks() []openfeature.Hook {
	return []openfeature.Hook{}
}

// evaluate is the core evaluation method that all type-specific methods use.
func (p *DatadogProvider) evaluate(
	flagKey string,
	defaultValue any,
	flatCtx openfeature.FlattenedContext,
) (res evaluationResult) {
	log.Debug("openfeature: evaluating flag %q", flagKey)
	defer func() {
		log.Debug("openfeature: evaluated flag %q: value=%v, reason=%s, error=%v", flagKey, res.Value, res.Reason, res.Error)
	}()
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

	// Evaluate the flag
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
