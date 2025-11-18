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

		status := provider.processConfigUpdate("test-path", data)
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
		status := provider.processConfigUpdate("test-path", nil)
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
		status := provider.processConfigUpdate("test-path", invalidJSON)

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
		status := provider.processConfigUpdate("test-path", data)

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

// TestMultipleRCFiles tests handling of multiple Remote Config files
func TestMultipleRCFiles(t *testing.T) {
	t.Run("multiple files sent and tracked", func(t *testing.T) {
		provider := newDatadogProvider(ProviderConfig{})

		// Send first RC file
		config1 := universalFlagsConfiguration{
			Format: "SERVER",
			Flags: map[string]*flag{
				"flag-from-file1": {
					Key:           "flag-from-file1",
					VariationType: valueTypeBoolean,
					Enabled:       true,
					Variations: map[string]*variant{
						"on": {Key: "on", Value: true},
					},
					Allocations: []*allocation{},
				},
			},
		}
		data1, err := json.Marshal(config1)
		if err != nil {
			t.Fatalf("failed to marshal config1: %v", err)
		}

		status := provider.processConfigUpdate("config/file1", data1)
		if status.State != rc.ApplyStateAcknowledged {
			t.Errorf("expected ApplyStateAcknowledged for file1, got %v", status.State)
		}

		// Verify file1 is tracked
		provider.receivedRCFIlesMu.Lock()
		if _, exists := provider.receivedRCFiles["config/file1"]; !exists {
			t.Error("expected file1 to be tracked in receivedRCFiles")
		}
		file1Count := len(provider.receivedRCFiles)
		provider.receivedRCFIlesMu.Unlock()

		if file1Count != 1 {
			t.Errorf("expected 1 file tracked, got %d", file1Count)
		}

		// Send second RC file
		config2 := universalFlagsConfiguration{
			Format: "SERVER",
			Flags: map[string]*flag{
				"flag-from-file2": {
					Key:           "flag-from-file2",
					VariationType: valueTypeString,
					Enabled:       true,
					Variations: map[string]*variant{
						"v1": {Key: "v1", Value: "value1"},
					},
					Allocations: []*allocation{},
				},
			},
		}
		data2, err := json.Marshal(config2)
		if err != nil {
			t.Fatalf("failed to marshal config2: %v", err)
		}

		status = provider.processConfigUpdate("config/file2", data2)
		if status.State != rc.ApplyStateAcknowledged {
			t.Errorf("expected ApplyStateAcknowledged for file2, got %v", status.State)
		}

		// Verify both files are tracked
		provider.receivedRCFIlesMu.Lock()
		if _, exists := provider.receivedRCFiles["config/file2"]; !exists {
			t.Error("expected file2 to be tracked in receivedRCFiles")
		}
		totalFiles := len(provider.receivedRCFiles)
		provider.receivedRCFIlesMu.Unlock()

		if totalFiles != 2 {
			t.Errorf("expected 2 files tracked, got %d", totalFiles)
		}

		// Configuration should be set (the last one wins since we don't merge)
		currentConfig := provider.getConfiguration()
		if currentConfig == nil {
			t.Fatal("expected configuration to be set")
		}
	})

	t.Run("partial file deletion does not clear configuration", func(t *testing.T) {
		provider := newDatadogProvider(ProviderConfig{})

		// Send two RC files
		config1 := universalFlagsConfiguration{
			Format: "SERVER",
			Flags: map[string]*flag{
				"flag1": {
					Key:           "flag1",
					VariationType: valueTypeBoolean,
					Enabled:       true,
					Variations: map[string]*variant{
						"on": {Key: "on", Value: true},
					},
					Allocations: []*allocation{},
				},
			},
		}
		data1, _ := json.Marshal(config1)
		provider.processConfigUpdate("config/file1", data1)

		config2 := universalFlagsConfiguration{
			Format: "SERVER",
			Flags: map[string]*flag{
				"flag2": {
					Key:           "flag2",
					VariationType: valueTypeBoolean,
					Enabled:       true,
					Variations: map[string]*variant{
						"on": {Key: "on", Value: true},
					},
					Allocations: []*allocation{},
				},
			},
		}
		data2, _ := json.Marshal(config2)
		provider.processConfigUpdate("config/file2", data2)

		// Set the configuration manually (simulating that file2 was the last to set it)
		provider.updateConfiguration(&config2)

		// Verify we have 2 files tracked
		provider.receivedRCFIlesMu.Lock()
		initialCount := len(provider.receivedRCFiles)
		provider.receivedRCFIlesMu.Unlock()
		if initialCount != 2 {
			t.Fatalf("expected 2 files tracked initially, got %d", initialCount)
		}

		// Delete file1 (send nil data)
		status := provider.processConfigUpdate("config/file1", nil)
		if status.State != rc.ApplyStateAcknowledged {
			t.Errorf("expected ApplyStateAcknowledged for deletion, got %v", status.State)
		}

		// Verify file1 is removed from tracking
		provider.receivedRCFIlesMu.Lock()
		if _, exists := provider.receivedRCFiles["config/file1"]; exists {
			t.Error("expected file1 to be removed from receivedRCFiles")
		}
		remainingFiles := len(provider.receivedRCFiles)
		provider.receivedRCFIlesMu.Unlock()

		if remainingFiles != 1 {
			t.Errorf("expected 1 file remaining, got %d", remainingFiles)
		}

		// Configuration should NOT be cleared (file2 still exists)
		currentConfig := provider.getConfiguration()
		if currentConfig == nil {
			t.Error("expected configuration to remain set after partial deletion")
		}
	})

	t.Run("all files deletion clears configuration", func(t *testing.T) {
		provider := newDatadogProvider(ProviderConfig{})

		// Send two RC files
		config1 := universalFlagsConfiguration{
			Format: "SERVER",
			Flags: map[string]*flag{
				"flag1": {
					Key:           "flag1",
					VariationType: valueTypeBoolean,
					Enabled:       true,
					Variations: map[string]*variant{
						"on": {Key: "on", Value: true},
					},
					Allocations: []*allocation{},
				},
			},
		}
		data1, _ := json.Marshal(config1)
		provider.processConfigUpdate("config/file1", data1)

		config2 := universalFlagsConfiguration{
			Format: "SERVER",
			Flags: map[string]*flag{
				"flag2": {
					Key:           "flag2",
					VariationType: valueTypeBoolean,
					Enabled:       true,
					Variations: map[string]*variant{
						"on": {Key: "on", Value: true},
					},
					Allocations: []*allocation{},
				},
			},
		}
		data2, _ := json.Marshal(config2)
		provider.processConfigUpdate("config/file2", data2)

		// Verify configuration is set
		if provider.getConfiguration() == nil {
			t.Fatal("expected configuration to be set before deletions")
		}

		// Delete file1
		provider.processConfigUpdate("config/file1", nil)

		// Configuration should still be set
		if provider.getConfiguration() == nil {
			t.Error("expected configuration to remain after deleting file1")
		}

		// Delete file2 (last file)
		status := provider.processConfigUpdate("config/file2", nil)
		if status.State != rc.ApplyStateAcknowledged {
			t.Errorf("expected ApplyStateAcknowledged for final deletion, got %v", status.State)
		}

		// Verify all files are removed
		provider.receivedRCFIlesMu.Lock()
		remainingFiles := len(provider.receivedRCFiles)
		provider.receivedRCFIlesMu.Unlock()

		if remainingFiles != 0 {
			t.Errorf("expected 0 files remaining, got %d", remainingFiles)
		}

		// Configuration should NOW be cleared
		currentConfig := provider.getConfiguration()
		if currentConfig != nil {
			t.Error("expected configuration to be cleared after all files deleted")
		}
	})

	t.Run("callback processes multiple files in single update", func(t *testing.T) {
		provider := newDatadogProvider(ProviderConfig{})

		// Create a product update with multiple files
		config1 := universalFlagsConfiguration{
			Format: "SERVER",
			Flags: map[string]*flag{
				"flag1": {
					Key:           "flag1",
					VariationType: valueTypeBoolean,
					Enabled:       true,
					Variations: map[string]*variant{
						"on": {Key: "on", Value: true},
					},
					Allocations: []*allocation{},
				},
			},
		}
		data1, _ := json.Marshal(config1)

		config2 := universalFlagsConfiguration{
			Format: "SERVER",
			Flags: map[string]*flag{
				"flag2": {
					Key:           "flag2",
					VariationType: valueTypeString,
					Enabled:       true,
					Variations: map[string]*variant{
						"v1": {Key: "v1", Value: "test"},
					},
					Allocations: []*allocation{},
				},
			},
		}
		data2, _ := json.Marshal(config2)

		// Simulate a batch update with multiple files
		update := remoteconfig.ProductUpdate{
			"config/file1": data1,
			"config/file2": data2,
		}

		statuses := provider.rcCallback(update)

		// Verify both files processed successfully
		if len(statuses) != 2 {
			t.Errorf("expected 2 statuses, got %d", len(statuses))
		}

		for path, status := range statuses {
			if status.State != rc.ApplyStateAcknowledged {
				t.Errorf("expected ApplyStateAcknowledged for %s, got %v", path, status.State)
			}
		}

		// Verify both files are tracked
		provider.receivedRCFIlesMu.Lock()
		trackedFiles := len(provider.receivedRCFiles)
		provider.receivedRCFIlesMu.Unlock()

		if trackedFiles != 2 {
			t.Errorf("expected 2 files tracked, got %d", trackedFiles)
		}
	})

	t.Run("mixed valid and invalid files in batch update", func(t *testing.T) {
		provider := newDatadogProvider(ProviderConfig{})

		// Valid config
		validConfig := universalFlagsConfiguration{
			Format: "SERVER",
			Flags: map[string]*flag{
				"valid-flag": {
					Key:           "valid-flag",
					VariationType: valueTypeBoolean,
					Enabled:       true,
					Variations: map[string]*variant{
						"on": {Key: "on", Value: true},
					},
					Allocations: []*allocation{},
				},
			},
		}
		validData, _ := json.Marshal(validConfig)

		// Invalid config (missing variationType)
		invalidConfig := map[string]interface{}{
			"format": "SERVER",
			"flags": map[string]interface{}{
				"invalid-flag": map[string]interface{}{
					"key":     "invalid-flag",
					"enabled": true,
					// Missing variationType
					"variations": map[string]interface{}{
						"v1": map[string]interface{}{
							"key":   "v1",
							"value": "test",
						},
					},
				},
			},
		}
		invalidData, _ := json.Marshal(invalidConfig)

		// Process both files
		update := remoteconfig.ProductUpdate{
			"config/valid":   validData,
			"config/invalid": invalidData,
		}

		statuses := provider.rcCallback(update)

		// Valid file should succeed
		if statuses["config/valid"].State != rc.ApplyStateAcknowledged {
			t.Errorf("expected valid file to be acknowledged, got %v", statuses["config/valid"].State)
		}

		// Invalid file should error
		if statuses["config/invalid"].State != rc.ApplyStateError {
			t.Errorf("expected invalid file to error, got %v", statuses["config/invalid"].State)
		}

		// Only valid file should be tracked
		provider.receivedRCFIlesMu.Lock()
		trackedFiles := len(provider.receivedRCFiles)
		_, validTracked := provider.receivedRCFiles["config/valid"]
		_, invalidTracked := provider.receivedRCFiles["config/invalid"]
		provider.receivedRCFIlesMu.Unlock()

		if trackedFiles != 1 {
			t.Errorf("expected 1 file tracked, got %d", trackedFiles)
		}
		if !validTracked {
			t.Error("expected valid file to be tracked")
		}
		if invalidTracked {
			t.Error("expected invalid file NOT to be tracked")
		}
	})
}
