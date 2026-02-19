// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2022 Datadog, Inc.

package remoteconfig

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestConfigStatesPreservedDuringError verifies that config_states are preserved
// in requests even when lastError is set, as required by the Remote Config RFC.
func TestConfigStatesPreservedDuringError(t *testing.T) {
	client, err := newClient(DefaultClientConfig())
	require.NoError(t, err)

	// Simulate having some config states from a previous successful update
	client.lastConfigStates = []*configState{
		{
			ID:      "test-config-1",
			Version: 1,
			Product: "ASM_DD",
		},
		{
			ID:      "test-config-2",
			Version: 2,
			Product: "ASM_FEATURES",
		},
	}

	// Simulate an error state
	client.lastError = assert.AnError

	// Build a new update request
	buf, err := client.newUpdateRequest()
	require.NoError(t, err)

	// Parse the request
	var req clientGetConfigsRequest
	err = json.NewDecoder(&buf).Decode(&req)
	require.NoError(t, err)

	// Verify that config_states are present
	assert.NotNil(t, req.Client.State.ConfigStates)
	assert.Len(t, req.Client.State.ConfigStates, 2)

	// Verify the config states match what we saved
	assert.Equal(t, "test-config-1", req.Client.State.ConfigStates[0].ID)
	assert.Equal(t, uint64(1), req.Client.State.ConfigStates[0].Version)
	assert.Equal(t, "ASM_DD", req.Client.State.ConfigStates[0].Product)

	assert.Equal(t, "test-config-2", req.Client.State.ConfigStates[1].ID)
	assert.Equal(t, uint64(2), req.Client.State.ConfigStates[1].Version)
	assert.Equal(t, "ASM_FEATURES", req.Client.State.ConfigStates[1].Product)

	// Verify has_error is true
	assert.True(t, req.Client.State.HasError)
	assert.NotEmpty(t, req.Client.State.Error)
}

// TestConfigStatesNotOmittedWithError verifies that config_states field is not
// omitted from JSON when has_error is true, ensuring it doesn't get dropped by
// the omitempty tag.
func TestConfigStatesNotOmittedWithError(t *testing.T) {
	client, err := newClient(DefaultClientConfig())
	require.NoError(t, err)

	client.lastConfigStates = []*configState{
		{
			ID:      "test-config",
			Version: 1,
			Product: "ASM_DD",
		},
	}
	client.lastError = assert.AnError

	buf, err := client.newUpdateRequest()
	require.NoError(t, err)

	// Parse as raw JSON to verify the field is present
	var rawReq map[string]any
	err = json.NewDecoder(bytes.NewReader(buf.Bytes())).Decode(&rawReq)
	require.NoError(t, err)

	clientData := rawReq["client"].(map[string]any)
	state := clientData["state"].(map[string]any)

	// Verify config_states field exists in the JSON
	configStates, exists := state["config_states"]
	assert.True(t, exists, "config_states should be present in JSON even with error")
	assert.NotNil(t, configStates)

	// Verify has_error is true
	hasError, exists := state["has_error"]
	assert.True(t, exists)
	assert.True(t, hasError.(bool))
}
