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
	"github.com/DataDog/dd-trace-go/v2/internal/log"
	"github.com/DataDog/dd-trace-go/v2/internal/remoteconfig"
)

const (
	ffeProductName = "FFE_FLAGS"
	ffeCapability  = 46
)

func startWithRemoteConfig(config ProviderConfig) (*DatadogProvider, error) {
	provider := newDatadogProvider(config)

	if err := remoteconfig.Start(remoteconfig.DefaultClientConfig()); err != nil {
		return nil, fmt.Errorf("failed to start Remote Config: %w", err)
	}

	// Create the callback that will handle Remote Config updates
	callback := createRemoteConfigCallback(provider)

	// Subscribe to Remote Config updates for the OpenFeature product
	if err := remoteconfig.Subscribe(ffeProductName, callback, ffeCapability); err != nil {
		return nil, fmt.Errorf("failed to subscribe to Remote Config: %w (did you already create a provider ?)", err)
	}

	log.Debug("openfeature: successfully subscribed to Remote Config updates")
	return provider, nil
}

// createRemoteConfigCallback creates a callback function for Remote Config updates.
// This callback parses incoming configurations and updates the provider.
func createRemoteConfigCallback(provider *DatadogProvider) remoteconfig.ProductCallback {
	return func(update remoteconfig.ProductUpdate) map[string]rc.ApplyStatus {
		statuses := make(map[string]rc.ApplyStatus, len(update))

		// Process each configuration file in the update
		for path, data := range update {
			status := processConfigUpdate(provider, path, data)
			statuses[path] = status
		}

		return statuses
	}
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
		return fmt.Errorf("configuration is nil")
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
				if shard.TotalShards < 0 {
					return fmt.Errorf("flag %q allocation %d split %d has shard with non-positive TotalShards %d",
						flagKey, i, j, shard.TotalShards)
				}
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

				if condition.Operator == operatorMatches || condition.Operator == operatorNotMatches {
					regex, ok := condition.Value.(string)
					if !ok {
						return fmt.Errorf("flag %q allocation %d rule has condition with operator %q that requires string value",
							flagKey, i, condition.Operator)
					}

					if _, err := loadRegex(regex); err != nil {
						return fmt.Errorf("flag %q allocation %d rule has condition with invalid regex %q: %v",
							flagKey, i, regex, err)
					}
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
// Note: This function is currently not fully implemented as Remote Config
// doesn't provide an Unsubscribe method yet. The provider will continue
// to receive updates until the Remote Config client is stopped.
func stopRemoteConfig() error {
	// TODO: Implement unsubscribe when available in remoteconfig package
	// For now, we can unregister the product and the callback
	if err := remoteconfig.UnregisterProduct(ffeProductName); err != nil {
		return fmt.Errorf("failed to unregister OpenFeature product: %w", err)
	}

	log.Debug("openfeature: unregistered from Remote Config")
	return nil
}
