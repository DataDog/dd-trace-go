// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2023 Datadog, Inc.

package opentelemetry

import (
	"fmt"
	"testing"

	"github.com/DataDog/dd-trace-go/v2/internal/telemetry"
	"github.com/DataDog/dd-trace-go/v2/internal/telemetry/telemetrytest"

	"github.com/stretchr/testify/assert"
)

func TestTelemetry(t *testing.T) {
	tests := []struct {
		env             map[string]string
		expectedInject  string
		expectedExtract string
	}{
		{
			// if nothing is set, DD_TRACE_PROPAGATION_STYLE will be set to datadog,tracecontext,baggage
			expectedInject:  "datadog,tracecontext,baggage",
			expectedExtract: "datadog,tracecontext,baggage",
		},
		{
			env: map[string]string{
				"DD_TRACE_PROPAGATION_STYLE_EXTRACT": "datadog",
			},
			expectedInject:  "datadog,tracecontext,baggage",
			expectedExtract: "datadog",
		},
		{
			env: map[string]string{
				"DD_TRACE_PROPAGATION_STYLE_EXTRACT": "none",
			},
			expectedInject:  "datadog,tracecontext,baggage",
			expectedExtract: "",
		},
		{
			env: map[string]string{
				"DD_TRACE_PROPAGATION_STYLE":         "datadog,tracecontext",
				"DD_TRACE_PROPAGATION_STYLE_EXTRACT": "none",
			},
			expectedInject:  "datadog,tracecontext",
			expectedExtract: "",
		},
		{
			env: map[string]string{
				// deprecated environment variable
				"DD_PROPAGATION_STYLE_INJECT":        "tracecontext",
				"DD_TRACE_PROPAGATION_STYLE_EXTRACT": "",
			},
			expectedInject:  "datadog,tracecontext,baggage", // default value
			expectedExtract: "datadog,tracecontext,baggage",
		},
		{
			env: map[string]string{
				// deprecated environment variable
				"DD_PROPAGATION_STYLE_INJECT":        "datadog,tracecontext",
				"DD_TRACE_PROPAGATION_STYLE_EXTRACT": "b3",
			},
			expectedInject:  "datadog,tracecontext,baggage", // default value
			expectedExtract: "b3",
		},
	}

	for i, test := range tests {
		t.Run(fmt.Sprintf("test #%v with env: %s", i, test.env), func(t *testing.T) {
			for k, v := range test.env {
				t.Setenv(k, v)
			}
			telemetryClient := new(telemetrytest.RecordClient)
			defer telemetry.MockClient(telemetryClient)()

			p := NewTracerProvider()
			p.Tracer("")
			defer p.Shutdown()

			assert.Contains(t, telemetryClient.Configuration, telemetry.Configuration{Name: "trace_propagation_style_inject", Value: test.expectedInject})
			assert.Contains(t, telemetryClient.Configuration, telemetry.Configuration{Name: "trace_propagation_style_extract", Value: test.expectedExtract})
		})
	}

}
