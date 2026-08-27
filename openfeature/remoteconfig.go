// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package openfeature

import (
	"encoding/json"
	"errors"
	"fmt"
	"maps"

	rc "github.com/DataDog/datadog-agent/pkg/remoteconfig/state"

	"github.com/DataDog/dd-trace-go/v2/internal"
	"github.com/DataDog/dd-trace-go/v2/internal/log"
	internalffe "github.com/DataDog/dd-trace-go/v2/internal/openfeature"
	"github.com/DataDog/dd-trace-go/v2/internal/remoteconfig"
)

var errInvalidSemverComparand = errors.New("invalid semantic version comparand")

func startWithRemoteConfig(config ProviderConfig) (*DatadogProvider, error) {
	provider := newDatadogProviderWithSource(config, internalffe.SourceRemoteConfig)

	// Subscribe via the internal package, which serializes with tracer subscription
	// and starts RC only if needed (slow path).
	tracerOwnsSubscription, err := internalffe.SubscribeProvider(provider.rcCallback)
	if err != nil {
		return nil, fmt.Errorf("failed to subscribe to Remote Config: %w", err)
	}

	if !tracerOwnsSubscription {
		log.Debug("openfeature: successfully subscribed to Remote Config updates")
		return provider, nil
	}
	if !attachProvider(provider) {
		// This shouldn't happen since SubscribeProvider just told us tracer subscribed.
		return nil, errors.New("failed to attach to tracer's RC subscription")
	}
	log.Debug("openfeature: attached to tracer's RC subscription")
	return provider, nil
}

func (p *DatadogProvider) rcCallback(update remoteconfig.ProductUpdate) map[string]rc.ApplyStatus {
	statuses := make(map[string]rc.ApplyStatus, len(update))

	// Process each configuration file in the update
	for path, data := range update {
		status := processConfigUpdate(p, path, data)
		statuses[path] = status
	}

	return statuses
}

// processConfigUpdate processes a single configuration update from Remote Config.
func processConfigUpdate(provider *DatadogProvider, path string, data []byte) rc.ApplyStatus {
	// Handle configuration deletion (nil data means the config was removed)
	if data == nil {
		log.Debug("openfeature: remote config: removing configuration %q", path)
		// For now, we treat deletion as clearing the configuration
		// In a multi-config scenario, we might track configs per path
		provider.updateConfiguration(nil)
		return rc.ApplyStatus{
			State: rc.ApplyStateAcknowledged,
		}
	}

	// Parse the configuration
	log.Debug("openfeature: remote config: processing configuration update %q", path)

	var config universalFlagsConfiguration
	if err := json.Unmarshal(data, &config); err != nil {
		log.Error("openfeature: remote config: failed to unmarshal configuration %q: %v", path, err.Error())
		return rc.ApplyStatus{
			State: rc.ApplyStateError,
			Error: fmt.Sprintf("failed to unmarshal configuration: %v", err),
		}
	}

	// Validate the configuration
	err := validateConfiguration(&config)
	if err != nil {
		log.Error("openfeature: remote config: invalid configuration %q: %v", path, err.Error())
		return rc.ApplyStatus{
			State: rc.ApplyStateError,
			Error: fmt.Sprintf("invalid configuration: %v", err),
		}
	}

	// Update the provider with the new configuration
	provider.updateConfiguration(&config)
	log.Debug("openfeature: remote config: successfully applied configuration %q with %d flags", path, len(config.Flags))

	return rc.ApplyStatus{
		State: rc.ApplyStateAcknowledged,
	}
}

// validateConfiguration performs basic validation on a serverConfiguration.
func validateConfiguration(config *universalFlagsConfiguration) error {
	if config == nil {
		return errors.New("configuration is nil")
	}

	if config.Format != "SERVER" {
		return fmt.Errorf("unsupported format %q, expected SERVER (Is the remote config payload the right format ?)", config.Format)
	}

	hasFlags := len(config.Flags) > 0

	// Validate each flag and delete invalid ones from the map
	// Collect errors for reporting
	errs := make([]error, 0, len(config.Flags))
	maps.DeleteFunc(config.Flags, func(flagKey string, flag *flag) bool {
		err := validateFlag(flagKey, flag)
		errs = append(errs, err)
		return err != nil
	})

	if hasFlags && len(config.Flags) == 0 {
		errs = append(errs, errors.New("all flags are invalid"))
	}

	return errors.Join(errs...)
}

func validateFlag(flagKey string, flag *flag) error {
	if flag == nil {
		return fmt.Errorf("flag %q is nil", flagKey)
	}

	if flag.Key != flagKey {
		return fmt.Errorf("flag key mismatch: map key %q != flag.Key %q", flagKey, flag.Key)
	}

	// Validate variation type
	switch flag.VariationType {
	case valueTypeBoolean, valueTypeString, valueTypeInteger, valueTypeNumeric, valueTypeJSON:
		// Valid types
	default:
		return fmt.Errorf("flag %q has invalid variation type %q", flagKey, flag.VariationType)
	}

	for i, allocation := range flag.Allocations {
		if allocation == nil {
			return fmt.Errorf("flag %q allocation %d is nil", flagKey, i)
		}

		for j, split := range allocation.Splits {
			if split == nil {
				return fmt.Errorf("flag %q allocation %d split %d is nil", flagKey, i, j)
			}

			for _, shard := range split.Shards {
				if shard.TotalShards <= 0 || uint64(shard.TotalShards) > uint64(^uint32(0)) {
					return fmt.Errorf("flag %q allocation %d split %d has shard with invalid TotalShards %d",
						flagKey, i, j, shard.TotalShards)
				}
				for _, shardRange := range shard.Ranges {
					if shardRange == nil {
						return fmt.Errorf("flag %q allocation %d split %d has nil shard range", flagKey, i, j)
					}
					if shardRange.Start < 0 || shardRange.End < 0 {
						return fmt.Errorf("flag %q allocation %d split %d has shard with negative range bounds",
							flagKey, i, j)
					}
				}
			}

			if split.Shards == nil {
				return fmt.Errorf("flag %q allocation %d split %d is missing shards", flagKey, i, j)
			}

			if _, exists := flag.Variations[split.VariationKey]; !exists {
				return fmt.Errorf("flag %q allocation %d split %d references non-existent variation %q",
					flagKey, i, j, split.VariationKey)
			}
		}

		for _, rule := range allocation.Rules {
			if rule == nil {
				return fmt.Errorf("flag %q allocation %d has nil rule", flagKey, i)
			}

			for _, condition := range rule.Conditions {
				if condition == nil {
					return fmt.Errorf("flag %q allocation %d rule has nil condition", flagKey, i)
				}

				switch condition.Operator {
				case operatorLT, operatorLTE, operatorGT, operatorGTE,
					operatorSemverEQ, operatorSemverNEQ, operatorSemverLT,
					operatorSemverLTE, operatorSemverGT, operatorSemverGTE,
					operatorMatches, operatorNotMatches,
					operatorOneOf, operatorNotOneOf, operatorIsNull:
				default:
					return fmt.Errorf("flag %q allocation %d rule has unknown operator %q",
						flagKey, i, condition.Operator)
				}

				switch condition.Operator {
				case operatorLT, operatorLTE, operatorGT, operatorGTE:
					if _, ok := internal.ToFloat64(condition.Value); !ok {
						return fmt.Errorf("flag %q allocation %d rule has condition with operator %q that requires numeric value",
							flagKey, i, condition.Operator)
					}
				case operatorMatches, operatorNotMatches:
					regex, ok := condition.Value.(string)
					if !ok {
						return fmt.Errorf("flag %q allocation %d rule has condition with operator %q that requires string value",
							flagKey, i, condition.Operator)
					}
					if _, err := loadRegex(regex); err != nil {
						return fmt.Errorf("flag %q allocation %d rule has condition with invalid regex %q: %v",
							flagKey, i, regex, err)
					}
				case operatorOneOf, operatorNotOneOf:
					if _, ok := condition.Value.([]any); !ok {
						if _, ok := condition.Value.([]string); !ok {
							return fmt.Errorf("flag %q allocation %d rule has condition with operator %q that requires array value",
								flagKey, i, condition.Operator)
						}
					}
				case operatorIsNull:
					if _, ok := condition.Value.(bool); !ok {
						return fmt.Errorf("flag %q allocation %d rule has condition with operator %q that requires boolean value",
							flagKey, i, condition.Operator)
					}
				case operatorSemverEQ, operatorSemverNEQ, operatorSemverLT,
					operatorSemverLTE, operatorSemverGT, operatorSemverGTE:
					comparand, ok := condition.Value.(string)
					if !ok {
						return fmt.Errorf("%w: flag %q allocation %d rule has condition with operator %q that requires string value",
							errInvalidSemverComparand, flagKey, i, condition.Operator)
					}
					parsedComparand, ok := parseSemver(comparand)
					if !ok {
						return fmt.Errorf("%w: flag %q allocation %d rule has condition with operator %q and invalid semantic version %q",
							errInvalidSemverComparand, flagKey, i, condition.Operator, comparand)
					}
					condition.semverComparand = &parsedComparand
				}
			}
		}
	}
	return nil
}

// stopRemoteConfig unsubscribes from Remote Config updates.
// This should be called when shutting down the application or when
// the OpenFeature provider is no longer needed.
//
// Note: In the slow path, this package discards the subscription token from
// Subscribe(), so we cannot call Unsubscribe(). Instead we unregister the
// capability which stops updates. In the fast path (tracer subscribed),
// the subscription is owned by the tracer.
func stopRemoteConfig() error {
	log.Debug("openfeature: unregistered from Remote Config")
	_ = remoteconfig.UnregisterCapability(remoteconfig.FFEFlagEvaluation)
	return nil
}
