// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package openfeature

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// wrapUFCEnvelope wraps raw UFC attribute bytes in a JSON:API envelope, as the
// Agentless configuration endpoint does.
func wrapUFCEnvelope(t *testing.T, resourceType string, attributes json.RawMessage) []byte {
	t.Helper()
	envelope := map[string]any{
		"data": map[string]any{
			"id":         "1",
			"type":       resourceType,
			"attributes": attributes,
		},
	}
	body, err := json.Marshal(envelope)
	require.NoError(t, err)
	return body
}

func TestParseUFCEnvelope_Valid(t *testing.T) {
	fixtureDir := "ffe-system-test-data"
	configData, err := os.ReadFile(filepath.Join(fixtureDir, "ufc-config.json"))
	if err != nil {
		t.Fatalf("read canonical FFE fixtures: %v (initialize them with `git submodule update --init --recursive`)", err)
	}

	body := wrapUFCEnvelope(t, ufcResourceType, configData)

	config, err := parseUFCEnvelope(body)
	require.NoError(t, err)
	require.NotNil(t, config)
	assert.Equal(t, "SERVER", config.Format)
	assert.NotEmpty(t, config.Flags)
}

func TestParseUFCEnvelope_Rejections(t *testing.T) {
	validAttrs := json.RawMessage(`{
		"createdAt": "2024-04-17T19:40:53.716Z",
		"format": "SERVER",
		"environment": {"name": "Test"},
		"flags": {}
	}`)

	for name, tt := range map[string]struct {
		body []byte
	}{
		"wrong data.type": {
			body: wrapUFCEnvelope(t, "something-else", validAttrs),
		},
		"missing data.type": {
			body: wrapUFCEnvelope(t, "", validAttrs),
		},
		"data is null": {
			body: []byte(`{"data": null}`),
		},
		"data is missing": {
			body: []byte(`{}`),
		},
		"attributes absent": {
			body: []byte(`{"data": {"type": "` + ufcResourceType + `"}}`),
		},
		"format is numeric": {
			body: wrapUFCEnvelope(t, ufcResourceType, json.RawMessage(`{
				"createdAt": "2024-04-17T19:40:53.716Z",
				"format": 1,
				"environment": {"name": "Test"},
				"flags": {}
			}`)),
		},
		"format is absent": {
			body: wrapUFCEnvelope(t, ufcResourceType, json.RawMessage(`{
				"createdAt": "2024-04-17T19:40:53.716Z",
				"environment": {"name": "Test"},
				"flags": {}
			}`)),
		},
		"createdAt is numeric": {
			body: wrapUFCEnvelope(t, ufcResourceType, json.RawMessage(`{
				"createdAt": 1713383453,
				"format": "SERVER",
				"environment": {"name": "Test"},
				"flags": {}
			}`)),
		},
		"createdAt is absent": {
			body: wrapUFCEnvelope(t, ufcResourceType, json.RawMessage(`{
				"format": "SERVER",
				"environment": {"name": "Test"},
				"flags": {}
			}`)),
		},
		"createdAt is not RFC 3339": {
			body: wrapUFCEnvelope(t, ufcResourceType, json.RawMessage(`{
				"createdAt": "not-a-date",
				"format": "SERVER",
				"environment": {"name": "Test"},
				"flags": {}
			}`)),
		},
		"environment.name is absent": {
			body: wrapUFCEnvelope(t, ufcResourceType, json.RawMessage(`{
				"createdAt": "2024-04-17T19:40:53.716Z",
				"format": "SERVER",
				"environment": {},
				"flags": {}
			}`)),
		},
		"flags is absent": {
			body: wrapUFCEnvelope(t, ufcResourceType, json.RawMessage(`{
				"createdAt": "2024-04-17T19:40:53.716Z",
				"format": "SERVER",
				"environment": {"name": "Test"}
			}`)),
		},
		"flags is null": {
			body: wrapUFCEnvelope(t, ufcResourceType, json.RawMessage(`{
				"createdAt": "2024-04-17T19:40:53.716Z",
				"format": "SERVER",
				"environment": {"name": "Test"},
				"flags": null
			}`)),
		},
		"flags is an array": {
			body: wrapUFCEnvelope(t, ufcResourceType, json.RawMessage(`{
				"createdAt": "2024-04-17T19:40:53.716Z",
				"format": "SERVER",
				"environment": {"name": "Test"},
				"flags": []
			}`)),
		},
		"flags is a scalar": {
			body: wrapUFCEnvelope(t, ufcResourceType, json.RawMessage(`{
				"createdAt": "2024-04-17T19:40:53.716Z",
				"format": "SERVER",
				"environment": {"name": "Test"},
				"flags": "x"
			}`)),
		},
		"raw non-envelope UFC": {
			body: []byte(`{
				"createdAt": "2024-04-17T19:40:53.716Z",
				"format": "SERVER",
				"environment": {"name": "Test"},
				"flags": {}
			}`),
		},
		"mock's exact malformed bytes": {
			body: []byte(`{"flags": [`),
		},
	} {
		t.Run(name, func(t *testing.T) {
			config, err := parseUFCEnvelope(tt.body)
			require.Error(t, err)
			assert.Nil(t, config)
		})
	}
}

func TestParseUFCEnvelope_EmptyFlagsAccepted(t *testing.T) {
	body := wrapUFCEnvelope(t, ufcResourceType, json.RawMessage(`{
		"createdAt": "2024-04-17T19:40:53.716Z",
		"format": "SERVER",
		"environment": {"name": "Test"},
		"flags": {}
	}`))

	config, err := parseUFCEnvelope(body)
	require.NoError(t, err)
	require.NotNil(t, config)
	assert.Empty(t, config.Flags)
}
