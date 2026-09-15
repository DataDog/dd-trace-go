// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package openfeature

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"

	rc "github.com/DataDog/datadog-agent/pkg/remoteconfig/state"
	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/internal/remoteconfig"
)

func TestValidateConfiguration(t *testing.T) {
	t.Run("valid configuration", func(t *testing.T) {
		config := &universalFlagsConfiguration{
			CreatedAt: time.Now(),
			Format:    "SERVER",
			Environment: environment{
				Name: "test",
			},
			Flags: map[string]*flag{
				"test-flag": {
					Key:           "test-flag",
					Enabled:       true,
					VariationType: valueTypeBoolean,
					Variations: map[string]*variant{
						"on": {Key: "on", Value: true},
					},
					Allocations: []*allocation{
						{
							Key:   "allocation1",
							Rules: []*rule{},
							Splits: []*split{
								{
									Shards: []*shard{
										{
											Salt: "test",
											Ranges: []*shardRange{
												{Start: 0, End: 8192},
											},
											TotalShards: 8192,
										},
									},
									VariationKey: "on",
								},
							},
						},
					},
				},
			},
		}

		err := validateConfiguration(config)
		if err != nil {
			t.Errorf("expected valid configuration, got error: %v", err)
		}
	})

	t.Run("nil configuration", func(t *testing.T) {
		err := validateConfiguration(nil)
		if err == nil {
			t.Error("expected error for nil configuration")
		}
	})

	t.Run("invalid format", func(t *testing.T) {
		config := &universalFlagsConfiguration{
			Format: "CLIENT",
			Flags:  map[string]*flag{},
		}

		err := validateConfiguration(config)
		if err == nil {
			t.Error("expected error for invalid format")
		}
	})

	t.Run("nil flag", func(t *testing.T) {
		config := &universalFlagsConfiguration{
			Format: "SERVER",
			Flags: map[string]*flag{
				"test-flag": nil,
			},
		}

		err := validateConfiguration(config)
		if err == nil {
			t.Error("expected error for nil flag")
		}
	})

	t.Run("flag key mismatch", func(t *testing.T) {
		config := &universalFlagsConfiguration{
			Format: "SERVER",
			Flags: map[string]*flag{
				"test-flag": {
					Key:           "wrong-key",
					VariationType: valueTypeBoolean,
					Variations:    map[string]*variant{},
				},
			},
		}

		err := validateConfiguration(config)
		if err == nil {
			t.Error("expected error for flag key mismatch")
		}
	})

	t.Run("invalid variation type", func(t *testing.T) {
		config := &universalFlagsConfiguration{
			Format: "SERVER",
			Flags: map[string]*flag{
				"test-flag": {
					Key:           "test-flag",
					VariationType: valueType("INVALID"),
					Variations:    map[string]*variant{},
				},
			},
		}

		err := validateConfiguration(config)
		if err == nil {
			t.Error("expected error for invalid variation type")
		}
	})

	t.Run("no variations", func(t *testing.T) {
		config := &universalFlagsConfiguration{
			Format: "SERVER",
			Flags: map[string]*flag{
				"test-flag": {
					Key:           "test-flag",
					VariationType: valueTypeBoolean,
					Variations:    map[string]*variant{},
				},
			},
		}

		// Flags with no variations are valid (though they won't match any allocations)
		err := validateConfiguration(config)
		require.NoError(t, err)
	})

	t.Run("split references non-existent variation", func(t *testing.T) {
		config := &universalFlagsConfiguration{
			Format: "SERVER",
			Flags: map[string]*flag{
				"test-flag": {
					Key:           "test-flag",
					VariationType: valueTypeBoolean,
					Variations: map[string]*variant{
						"on": {Key: "on", Value: true},
					},
					Allocations: []*allocation{
						{
							Key:   "allocation1",
							Rules: []*rule{},
							Splits: []*split{
								{
									Shards: []*shard{
										{
											Salt: "test",
											Ranges: []*shardRange{
												{Start: 0, End: 8192},
											},
											TotalShards: 8192,
										},
									},
									VariationKey: "non-existent",
								},
							},
						},
					},
				},
			},
		}

		err := validateConfiguration(config)
		if err == nil {
			t.Error("expected error for split referencing non-existent variation")
		}
	})

	t.Run("invalid flags are deleted from config", func(t *testing.T) {
		config := &universalFlagsConfiguration{
			Format: "SERVER",
			Flags: map[string]*flag{
				"valid-flag": {
					Key:           "valid-flag",
					VariationType: valueTypeBoolean,
					Variations: map[string]*variant{
						"on": {Key: "on", Value: true},
					},
					Allocations: []*allocation{},
				},
				"invalid-flag-nil": nil,
			},
		}

		// Should return errors but also delete invalid flags
		err := validateConfiguration(config)
		if err == nil {
			t.Error("expected error for invalid flags")
		}

		// Check that invalid flags were deleted
		if _, exists := config.Flags["invalid-flag-nil"]; exists {
			t.Error("expected nil flag to be deleted from config")
		}
		if _, exists := config.Flags["valid-flag"]; !exists {
			t.Error("expected valid flag to remain in config")
		}
	})

	t.Run("multiple invalid flags produce joined errors", func(t *testing.T) {
		config := &universalFlagsConfiguration{
			Format: "SERVER",
			Flags: map[string]*flag{
				"flag1-nil": nil,
				"flag2-invalid-type": {
					Key:           "flag2-invalid-type",
					VariationType: valueType("INVALID"),
					Variations: map[string]*variant{
						"v1": {Key: "v1", Value: "val"},
					},
				},
			},
		}

		err := validateConfiguration(config)
		if err == nil {
			t.Error("expected error for multiple invalid flags")
		}

		// Both invalid flags should be deleted
		if len(config.Flags) != 0 {
			t.Errorf("expected all invalid flags to be deleted, got %d flags", len(config.Flags))
		}
	})
}

func TestValidateFlag(t *testing.T) {
	t.Run("valid flag", func(t *testing.T) {
		flag := &flag{
			Key:           "test-flag",
			VariationType: valueTypeBoolean,
			Variations: map[string]*variant{
				"on": {Key: "on", Value: true},
			},
			Allocations: []*allocation{},
		}

		err := validateFlag("test-flag", flag)
		if err != nil {
			t.Errorf("expected valid flag, got error: %v", err)
		}
	})

	t.Run("nil flag", func(t *testing.T) {
		err := validateFlag("test-flag", nil)
		if err == nil {
			t.Error("expected error for nil flag")
		}
	})

	t.Run("flag key mismatch", func(t *testing.T) {
		flag := &flag{
			Key:           "wrong-key",
			VariationType: valueTypeBoolean,
			Variations: map[string]*variant{
				"on": {Key: "on", Value: true},
			},
		}

		err := validateFlag("test-flag", flag)
		if err == nil {
			t.Error("expected error for flag key mismatch")
		}
	})

	t.Run("invalid variation type", func(t *testing.T) {
		flag := &flag{
			Key:           "test-flag",
			VariationType: valueType("INVALID_TYPE"),
			Variations: map[string]*variant{
				"v1": {Key: "v1", Value: "test"},
			},
		}

		err := validateFlag("test-flag", flag)
		if err == nil {
			t.Error("expected error for invalid variation type")
		}
	})

	t.Run("no variations", func(t *testing.T) {
		flag := &flag{
			Key:           "test-flag",
			VariationType: valueTypeBoolean,
			Variations:    map[string]*variant{},
		}

		// Flags with no variations are valid (though they won't match any allocations)
		err := validateFlag("test-flag", flag)
		require.NoError(t, err)
	})

	t.Run("nil allocation", func(t *testing.T) {
		flag := &flag{
			Key:           "test-flag",
			VariationType: valueTypeBoolean,
			Variations: map[string]*variant{
				"on": {Key: "on", Value: true},
			},
			Allocations: []*allocation{nil},
		}

		err := validateFlag("test-flag", flag)
		if err == nil {
			t.Error("expected error for nil allocation")
		}
	})

	t.Run("nil split in allocation", func(t *testing.T) {
		flag := &flag{
			Key:           "test-flag",
			VariationType: valueTypeBoolean,
			Variations: map[string]*variant{
				"on": {Key: "on", Value: true},
			},
			Allocations: []*allocation{
				{
					Key:    "allocation1",
					Rules:  []*rule{},
					Splits: []*split{nil},
				},
			},
		}

		err := validateFlag("test-flag", flag)
		if err == nil {
			t.Error("expected error for nil split")
		}
	})

	t.Run("split references non-existent variation", func(t *testing.T) {
		flag := &flag{
			Key:           "test-flag",
			VariationType: valueTypeBoolean,
			Variations: map[string]*variant{
				"on": {Key: "on", Value: true},
			},
			Allocations: []*allocation{
				{
					Key:   "allocation1",
					Rules: []*rule{},
					Splits: []*split{
						{
							Shards: []*shard{
								{
									Salt: "test",
									Ranges: []*shardRange{
										{Start: 0, End: 8192},
									},
									TotalShards: 8192,
								},
							},
							VariationKey: "non-existent",
						},
					},
				},
			},
		}

		err := validateFlag("test-flag", flag)
		if err == nil {
			t.Error("expected error for split referencing non-existent variation")
		}
	})

	t.Run("all variation types are valid", func(t *testing.T) {
		validTypes := []valueType{
			valueTypeBoolean,
			valueTypeString,
			valueTypeInteger,
			valueTypeNumeric,
			valueTypeJSON,
		}

		for _, vType := range validTypes {
			flag := &flag{
				Key:           "test-flag",
				VariationType: vType,
				Variations: map[string]*variant{
					"v1": {Key: "v1", Value: "test-value"},
				},
			}

			err := validateFlag("test-flag", flag)
			if err != nil {
				t.Errorf("expected %s to be valid, got error: %v", vType, err)
			}
		}
	})
}

func TestValidateFlagConditionOperands(t *testing.T) {
	newFlag := func(operator conditionOperator, value any) *flag {
		return &flag{
			Key:           "test-flag",
			VariationType: valueTypeBoolean,
			Variations: map[string]*variant{
				"on": {Key: "on", Value: true},
			},
			Allocations: []*allocation{
				{
					Rules: []*rule{
						{Conditions: []*condition{{Operator: operator, Attribute: "attribute", Value: value}}},
					},
				},
			},
		}
	}

	tests := []struct {
		name     string
		operator conditionOperator
		value    any
		valid    bool
	}{
		{name: "numeric", operator: operatorGT, value: 1.5, valid: true},
		{name: "numeric requires number", operator: operatorGT, value: "1.5"},
		{name: "regex", operator: operatorMatches, value: "^value$", valid: true},
		{name: "regex requires string", operator: operatorMatches, value: true},
		{name: "list", operator: operatorOneOf, value: []any{"value"}, valid: true},
		{name: "list requires array", operator: operatorOneOf, value: "value"},
		{name: "null", operator: operatorIsNull, value: true, valid: true},
		{name: "null requires boolean", operator: operatorIsNull, value: "true"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateFlag("test-flag", newFlag(tt.operator, tt.value))
			if tt.valid {
				require.NoError(t, err)
				return
			}
			require.Error(t, err)
		})
	}
}

func TestValidateFlagSemverConditions(t *testing.T) {
	newFlag := func(operator conditionOperator, value any) *flag {
		return &flag{
			Key:           "test-flag",
			VariationType: valueTypeBoolean,
			Variations: map[string]*variant{
				"on": {Key: "on", Value: true},
			},
			Allocations: []*allocation{
				{
					Rules: []*rule{
						{
							Conditions: []*condition{
								{Operator: operator, Attribute: "version", Value: value},
							},
						},
					},
				},
			},
		}
	}

	operators := []conditionOperator{
		operatorSemverEQ,
		operatorSemverNEQ,
		operatorSemverLT,
		operatorSemverLTE,
		operatorSemverGT,
		operatorSemverGTE,
	}
	for _, operator := range operators {
		t.Run(string(operator), func(t *testing.T) {
			flag := newFlag(operator, "1.2.3-alpha.1+build.5")
			require.NoError(t, validateFlag("test-flag", flag))
			require.Equal(t, &parsedSemver{major: 1, minor: 2, patch: 3, prerelease: "alpha.1"},
				flag.Allocations[0].Rules[0].Conditions[0].semverComparand)
		})
	}

	invalidValues := []struct {
		name  string
		value any
	}{
		{name: "non-string", value: 1.2},
		{name: "invalid", value: "not-a-version"},
		{name: "v prefix", value: "v1.2.3"},
		{name: "leading zero", value: "01.2.3"},
		{name: "overflow", value: "18446744073709551616.0.0"},
	}
	for _, tt := range invalidValues {
		t.Run(tt.name, func(t *testing.T) {
			require.ErrorIs(t, validateFlag("test-flag", newFlag(operatorSemverEQ, tt.value)), errInvalidSemverComparand)
		})
	}
}

func TestInvalidSemverComparandReturnsParseError(t *testing.T) {
	data := []byte(`{
		"format": "SERVER",
		"flags": {
			"invalid-semver": {
				"key": "invalid-semver",
				"enabled": true,
				"variationType": "BOOLEAN",
				"variations": {"on": {"key": "on", "value": true}},
				"allocations": [{
					"key": "targeted",
					"rules": [{"conditions": [{
						"attribute": "version",
						"operator": "SEMVER_EQ",
						"value": "not-a-version"
					}]}],
					"splits": [{"shards": [], "variationKey": "on"}]
				}]
			}
		}
	}`)

	var config universalFlagsConfiguration
	require.NoError(t, json.Unmarshal(data, &config))
	require.NotContains(t, config.Flags, "invalid-semver")
	require.ErrorIs(t, config.invalidFlags["invalid-semver"], errInvalidSemverComparand)

	result := evaluateConfiguredFlag(&config, "invalid-semver", false, map[string]any{"version": "1.2.3"}, time.Now())
	require.Equal(t, false, result.Value)
	require.Equal(t, "ERROR", string(result.Reason))
	require.ErrorIs(t, result.Error, errParseError)
	require.ErrorIs(t, result.Error, errInvalidSemverComparand)
}

func TestProcessConfigUpdate(t *testing.T) {
	t.Run("valid configuration update", func(t *testing.T) {
		provider := newDatadogProvider(ProviderConfig{})

		config := universalFlagsConfiguration{
			CreatedAt: time.Now(),
			Format:    "SERVER",
			Environment: environment{
				Name: "test",
			},
			Flags: map[string]*flag{
				"test-flag": {
					Key:           "test-flag",
					Enabled:       true,
					VariationType: valueTypeBoolean,
					Variations: map[string]*variant{
						"on": {Key: "on", Value: true},
					},
					Allocations: []*allocation{},
				},
			},
		}

		data, err := json.Marshal(config)
		if err != nil {
			t.Fatalf("failed to marshal config: %v", err)
		}

		status := processConfigUpdate(provider, "test-path", data)
		if status.State != rc.ApplyStateAcknowledged {
			t.Errorf("expected ApplyStateAcknowledged, got %v", status.State)
		}

		// Verify configuration was updated
		updatedConfig := provider.getConfiguration()
		if updatedConfig == nil {
			t.Fatal("expected configuration to be set")
		}
		if len(updatedConfig.Flags) != 1 {
			t.Errorf("expected 1 flag, got %d", len(updatedConfig.Flags))
		}
	})

	t.Run("nil shard range does not reject valid flags", func(t *testing.T) {
		provider := newDatadogProvider(ProviderConfig{})
		data := []byte(`{
			"format": "SERVER",
			"flags": {
				"valid-flag": {
					"key": "valid-flag",
					"enabled": true,
					"variationType": "BOOLEAN",
					"variations": {"on": {"key": "on", "value": true}},
					"allocations": [{
						"key": "static",
						"rules": [],
						"splits": [{"shards": [], "variationKey": "on"}]
					}]
				},
				"invalid-flag": {
					"key": "invalid-flag",
					"enabled": true,
					"variationType": "BOOLEAN",
					"variations": {"on": {"key": "on", "value": true}},
					"allocations": [{
						"key": "invalid",
						"rules": [],
						"splits": [{
							"shards": [{"totalShards": 8192, "ranges": [null]}],
							"variationKey": "on"
						}]
					}]
				}
			}
		}`)

		status := processConfigUpdate(provider, "test-path", data)
		require.Equal(t, rc.ApplyStateAcknowledged, status.State)

		updatedConfig := provider.getConfiguration()
		require.NotNil(t, updatedConfig)
		require.Contains(t, updatedConfig.Flags, "valid-flag")
		require.NotContains(t, updatedConfig.Flags, "invalid-flag")
		require.Contains(t, updatedConfig.invalidFlags, "invalid-flag")

		validResult := evaluateConfiguredFlag(updatedConfig, "valid-flag", false, nil, time.Now())
		require.Equal(t, true, validResult.Value)
		require.Equal(t, "STATIC", string(validResult.Reason))

		invalidResult := evaluateConfiguredFlag(updatedConfig, "invalid-flag", false, nil, time.Now())
		require.Equal(t, false, invalidResult.Value)
		require.Equal(t, "ERROR", string(invalidResult.Reason))
		require.ErrorIs(t, invalidResult.Error, errParseError)
	})

	t.Run("configuration deletion", func(t *testing.T) {
		provider := newDatadogProvider(ProviderConfig{})

		// First set a configuration
		config := &universalFlagsConfiguration{
			Format: "SERVER",
			Flags: map[string]*flag{
				"test-flag": {
					Key:           "test-flag",
					VariationType: valueTypeBoolean,
					Variations: map[string]*variant{
						"on": {Key: "on", Value: true},
					},
					Allocations: []*allocation{},
				},
			},
		}
		provider.updateConfiguration(config)

		// Now send a deletion (nil data)
		status := processConfigUpdate(provider, "test-path", nil)
		if status.State != rc.ApplyStateAcknowledged {
			t.Errorf("expected ApplyStateAcknowledged, got %v", status.State)
		}

		// Verify configuration was cleared
		updatedConfig := provider.getConfiguration()
		if updatedConfig != nil {
			t.Error("expected configuration to be cleared")
		}
	})

	t.Run("invalid JSON", func(t *testing.T) {
		provider := newDatadogProvider(ProviderConfig{})

		invalidJSON := []byte("{invalid json")
		status := processConfigUpdate(provider, "test-path", invalidJSON)

		if status.State != rc.ApplyStateError {
			t.Errorf("expected ApplyStateError, got %v", status.State)
		}
		if status.Error == "" {
			t.Error("expected error message")
		}
	})

	t.Run("invalid configuration", func(t *testing.T) {
		provider := newDatadogProvider(ProviderConfig{})

		config := universalFlagsConfiguration{
			Format: "INVALID",
			Flags:  map[string]*flag{},
		}

		data, _ := json.Marshal(config)
		status := processConfigUpdate(provider, "test-path", data)

		if status.State != rc.ApplyStateError {
			t.Errorf("expected ApplyStateError, got %v", status.State)
		}
		if status.Error == "" {
			t.Error("expected error message")
		}
	})
}

func TestCreateRemoteConfigCallback(t *testing.T) {
	provider := newDatadogProvider(ProviderConfig{})
	callback := provider.rcCallback

	// Create a valid configuration
	config := universalFlagsConfiguration{
		CreatedAt: time.Now(),
		Format:    "SERVER",
		Environment: environment{
			Name: "test",
		},
		Flags: map[string]*flag{
			"flag1": {
				Key:           "flag1",
				Enabled:       true,
				VariationType: valueTypeBoolean,
				Variations: map[string]*variant{
					"on": {Key: "on", Value: true},
				},
				Allocations: []*allocation{},
			},
			"flag2": {
				Key:           "flag2",
				Enabled:       true,
				VariationType: valueTypeString,
				Variations: map[string]*variant{
					"v1": {Key: "v1", Value: "version-1"},
				},
				Allocations: []*allocation{},
			},
		},
	}

	data, _ := json.Marshal(config)

	// Simulate Remote Config update with multiple paths
	update := remoteconfig.ProductUpdate{
		"path1": data,
		"path2": data,
	}

	statuses := callback(update)

	if len(statuses) != 2 {
		t.Errorf("expected 2 statuses, got %d", len(statuses))
	}

	for path, status := range statuses {
		if status.State != rc.ApplyStateAcknowledged {
			t.Errorf("expected ApplyStateAcknowledged for %s, got %v", path, status.State)
		}
	}
}

func TestRemoteConfigIntegration(t *testing.T) {
	// This test verifies the integration flow but doesn't actually
	// connect to Remote Config (would require a running agent)

	t.Run("callback handles multiple updates", func(t *testing.T) {
		provider := newDatadogProvider(ProviderConfig{})
		callback := provider.rcCallback

		// Create two different configurations
		config1 := universalFlagsConfiguration{
			Format: "SERVER",
			Flags: map[string]*flag{
				"flag1": {
					Key:           "flag1",
					Enabled:       true,
					VariationType: valueTypeBoolean,
					Variations: map[string]*variant{
						"on": {Key: "on", Value: true},
					},
					Allocations: []*allocation{},
				},
			},
		}

		config2 := universalFlagsConfiguration{
			Format: "SERVER",
			Flags: map[string]*flag{
				"flag2": {
					Key:           "flag2",
					Enabled:       true,
					VariationType: valueTypeString,
					Variations: map[string]*variant{
						"v1": {Key: "v1", Value: "value1"},
					},
					Allocations: []*allocation{},
				},
			},
		}

		data1, _ := json.Marshal(config1)
		data2, _ := json.Marshal(config2)

		// First update
		update1 := remoteconfig.ProductUpdate{
			"config1": data1,
		}
		statuses1 := callback(update1)
		if statuses1["config1"].State != rc.ApplyStateAcknowledged {
			t.Error("expected first update to be acknowledged")
		}

		// Second update (replaces first)
		update2 := remoteconfig.ProductUpdate{
			"config2": data2,
		}
		statuses2 := callback(update2)
		if statuses2["config2"].State != rc.ApplyStateAcknowledged {
			t.Error("expected second update to be acknowledged")
		}

		// Verify the provider has the latest configuration
		finalConfig := provider.getConfiguration()
		if finalConfig == nil {
			t.Fatal("expected configuration to be set")
		}
		if _, exists := finalConfig.Flags["flag2"]; !exists {
			t.Error("expected flag2 to be present in final configuration")
		}
	})

	t.Run("callback handles mixed success and failure", func(t *testing.T) {
		provider := newDatadogProvider(ProviderConfig{})
		callback := provider.rcCallback

		validConfig := universalFlagsConfiguration{
			Format: "SERVER",
			Flags: map[string]*flag{
				"valid-flag": {
					Key:           "valid-flag",
					Enabled:       true,
					VariationType: valueTypeBoolean,
					Variations: map[string]*variant{
						"on": {Key: "on", Value: true},
					},
					Allocations: []*allocation{},
				},
			},
		}

		validData, _ := json.Marshal(validConfig)
		invalidData := []byte("{invalid")

		update := remoteconfig.ProductUpdate{
			"valid":   validData,
			"invalid": invalidData,
		}

		statuses := callback(update)

		if statuses["valid"].State != rc.ApplyStateAcknowledged {
			t.Error("expected valid config to be acknowledged")
		}
		if statuses["invalid"].State != rc.ApplyStateError {
			t.Error("expected invalid config to be error")
		}
	})
}

func TestConfigurationPersistence(t *testing.T) {
	provider := newDatadogProvider(ProviderConfig{})

	// Simulate multiple Remote Config updates
	callback := provider.rcCallback

	configs := []universalFlagsConfiguration{
		{
			Format: "SERVER",
			Flags: map[string]*flag{
				"flag-v1": {
					Key:           "flag-v1",
					Enabled:       true,
					VariationType: valueTypeString,
					Variations: map[string]*variant{
						"v1": {Key: "v1", Value: "version-1"},
					},
					Allocations: []*allocation{},
				},
			},
		},
		{
			Format: "SERVER",
			Flags: map[string]*flag{
				"flag-v2": {
					Key:           "flag-v2",
					Enabled:       true,
					VariationType: valueTypeString,
					Variations: map[string]*variant{
						"v2": {Key: "v2", Value: "version-2"},
					},
					Allocations: []*allocation{},
				},
			},
		},
	}

	// Apply configurations sequentially
	for i, config := range configs {
		data, _ := json.Marshal(config)
		update := remoteconfig.ProductUpdate{
			"config": data,
		}
		callback(update)

		// Verify the provider has the latest config
		currentConfig := provider.getConfiguration()
		if currentConfig == nil {
			t.Fatalf("expected configuration to be set after update %d", i)
		}

		expectedFlagKey := fmt.Sprintf("flag-v%d", i+1)
		if _, exists := currentConfig.Flags[expectedFlagKey]; !exists {
			t.Errorf("expected flag %s after update %d", expectedFlagKey, i)
		}
	}
}
