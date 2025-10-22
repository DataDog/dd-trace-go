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
	"github.com/DataDog/dd-trace-go/v2/internal/remoteconfig"
	"github.com/stretchr/testify/require"
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

func TestProcessConfigUpdate(t *testing.T) {
	t.Run("valid configuration update", func(t *testing.T) {
		provider := newDatadogProvider()

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

		data, err := json.Marshal(serverConfiguration{config})
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

	t.Run("configuration deletion", func(t *testing.T) {
		provider := newDatadogProvider()

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
		provider := newDatadogProvider()

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
		provider := newDatadogProvider()

		config := universalFlagsConfiguration{
			Format: "INVALID",
			Flags:  map[string]*flag{},
		}

		data, _ := json.Marshal(serverConfiguration{config})
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
	provider := newDatadogProvider()
	callback := createRemoteConfigCallback(provider)

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

	data, _ := json.Marshal(serverConfiguration{config})

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
		provider := newDatadogProvider()
		callback := createRemoteConfigCallback(provider)

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

		data1, _ := json.Marshal(serverConfiguration{config1})
		data2, _ := json.Marshal(serverConfiguration{config2})

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
		provider := newDatadogProvider()
		callback := createRemoteConfigCallback(provider)

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

		validData, _ := json.Marshal(serverConfiguration{validConfig})
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
	provider := newDatadogProvider()

	// Simulate multiple Remote Config updates
	callback := createRemoteConfigCallback(provider)

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
		data, _ := json.Marshal(serverConfiguration{config})
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
