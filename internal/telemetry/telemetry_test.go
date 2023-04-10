// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2023 Datadog, Inc.

// Package telemetry implements a client for sending telemetry information to
// Datadog regarding usage of an APM library such as tracing or profiling.

package telemetry

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestProductChange(t *testing.T) {
	client := new(client)
	client.Start(nil)
	client.ProductChange(NamespaceProfilers, true,
		[]Configuration{BoolConfig("delta_profiles", true)})

	// should contain app-client-configuration-change and app-product-change
	assert.Len(t, client.requests, 2)

	firstBody := client.requests[0].Body
	assert.Equal(t, RequestTypeAppClientConfigurationChange, firstBody.RequestType)
	var configPayload *ConfigurationChange = client.requests[0].Body.Payload.(*ConfigurationChange)
	assert.Len(t, configPayload.Configuration, 1)

	Check(t, configPayload.Configuration, "delta_profiles", true)

	secondBody := client.requests[1].Body
	assert.Equal(t, RequestTypeAppProductChange, secondBody.RequestType)

	var productsPayload *Products = secondBody.Payload.(*Products)
	assert.Equal(t, productsPayload.Profiler.Enabled, true)
}
