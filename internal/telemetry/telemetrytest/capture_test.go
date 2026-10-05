// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package telemetrytest

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/internal/telemetry"
)

// TestNewCapturingClient_RegisterAppEndpoint proves CaptureRoundTripper can
// decode an "app-endpoints" request without aborting the test.
// transport.Body.UnmarshalJSON dispatches by RequestType through
// unmarshalPayload, which used to have no case for
// transport.RequestTypeAppEndpoints ("app-endpoints") — every other
// RequestType a Client can produce (app-started, logs, generate-metrics,
// etc.) had a matching case there except this one, so
// json.Unmarshal(payloadBytes, nil) ran with a nil destination and
// CaptureRoundTripper.RoundTrip's Fatalf aborted the test outright, even
// though this helper is documented as capturing the client's outbound
// bodies generically.
func TestNewCapturingClient_RegisterAppEndpoint(t *testing.T) {
	client, rt := NewCapturingClient(t)
	defer telemetry.MockClient(client)()

	client.RegisterAppEndpoint("GET /decision-maker", "GET /decision-maker", telemetry.AppEndpointAttributes{
		Kind:   "REST",
		Method: "GET",
		Path:   "/decision-maker",
	})
	client.Flush()

	bodies := rt.Bodies()
	require.NotEmpty(t, bodies, "expected at least one captured request body")

	var found bool
	for _, body := range bodies {
		if body.RequestType == "app-endpoints" {
			found = true
			break
		}
	}
	assert.True(t, found, "expected an app-endpoints request among the captured bodies: %+v", bodies)
}
