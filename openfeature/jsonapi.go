// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package openfeature

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
)

// ufcResourceType is the only accepted value of data.type in the JSON:API
// envelope returned by the Agentless configuration endpoint.
const ufcResourceType = "universal-flag-configuration"

// jsonAPIEnvelope is the outer JSON:API document. data.attributes is kept as
// raw bytes: it is shape-checked separately before being handed to
// universalFlagsConfiguration's own unmarshaler.
type jsonAPIEnvelope struct {
	Data *jsonAPIResource `json:"data"`
}

type jsonAPIResource struct {
	Type       string          `json:"type"`
	Attributes json.RawMessage `json:"attributes"`
}

// ufcAttributesShape shape-checks data.attributes before the lenient
// universalFlagsConfiguration unmarshaler runs. Pointer fields make "must be
// a string" free: a number or bool in that position fails to unmarshal into
// *string, whereas unmarshaling directly into a string would silently accept
// only strings and never surface a caller mistake as a rejection.
type ufcAttributesShape struct {
	Format      *string         `json:"format"`
	CreatedAt   *string         `json:"createdAt"`
	Environment ufcEnvShape     `json:"environment"`
	Flags       json.RawMessage `json:"flags"`
}

type ufcEnvShape struct {
	Name *string `json:"name"`
}

// parseUFCEnvelope parses and validates a JSON:API envelope carrying a
// Universal Flags Configuration. Only data.attributes ever reaches the
// evaluator; raw, non-enveloped UFC (as accepted by the legacy Remote Config
// path) is always rejected here.
func parseUFCEnvelope(body []byte) (*universalFlagsConfiguration, error) {
	var envelope jsonAPIEnvelope
	if err := json.Unmarshal(body, &envelope); err != nil {
		return nil, fmt.Errorf("jsonapi: invalid envelope: %w", err)
	}
	if envelope.Data == nil {
		return nil, errors.New("jsonapi: missing data")
	}
	if envelope.Data.Type != ufcResourceType {
		return nil, fmt.Errorf("jsonapi: unexpected data.type %q, expected %q", envelope.Data.Type, ufcResourceType)
	}
	if len(envelope.Data.Attributes) == 0 {
		return nil, errors.New("jsonapi: missing data.attributes")
	}

	var shape ufcAttributesShape
	if err := json.Unmarshal(envelope.Data.Attributes, &shape); err != nil {
		return nil, fmt.Errorf("jsonapi: invalid data.attributes shape: %w", err)
	}
	if shape.Format == nil {
		return nil, errors.New("jsonapi: data.attributes.format missing or not a string")
	}
	if shape.CreatedAt == nil {
		return nil, errors.New("jsonapi: data.attributes.createdAt missing or not a string")
	}
	if shape.Environment.Name == nil {
		return nil, errors.New("jsonapi: data.attributes.environment.name missing or not a string")
	}
	if !isJSONObject(shape.Flags) {
		return nil, errors.New("jsonapi: data.attributes.flags missing or not an object")
	}

	var config universalFlagsConfiguration
	if err := json.Unmarshal(envelope.Data.Attributes, &config); err != nil {
		return nil, fmt.Errorf("jsonapi: failed to unmarshal configuration: %w", err)
	}
	if err := validateConfiguration(&config); err != nil {
		return nil, fmt.Errorf("jsonapi: invalid configuration: %w", err)
	}
	return &config, nil
}

// isJSONObject reports whether raw is present and its first non-whitespace
// byte opens a JSON object, rejecting an absent, null, array, or scalar value.
func isJSONObject(raw json.RawMessage) bool {
	trimmed := bytes.TrimSpace(raw)
	return len(trimmed) > 0 && trimmed[0] == '{'
}
