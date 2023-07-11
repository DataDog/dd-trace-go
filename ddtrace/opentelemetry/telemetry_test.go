// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2023 Datadog, Inc.

package opentelemetry

import (
	"fmt"
	"github.com/stretchr/testify/assert"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/telemetry"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/telemetry/telemetrytest"
	"testing"
)

func TestTelemetry(t *testing.T) {
	tests := []struct {
		env             map[string]string
		expectedInject  string
		expectedExtract string
	}{
		{
			// if nothing is set, DD_TRACE_PROPAGATION_STYLE will be set to tracecontext
			expectedInject:  "tracecontext",
			expectedExtract: "tracecontext",
		},
		{
			env: map[string]string{
				"DD_TRACE_PROPAGATION_STYLE_EXTRACT": "datadog",
			},
			expectedInject:  "tracecontext",
			expectedExtract: "datadog,tracecontext",
		},
		{
			env: map[string]string{
				"DD_TRACE_PROPAGATION_STYLE_EXTRACT": "none",
			},
			expectedInject:  "tracecontext",
			expectedExtract: "tracecontext",
		},
		{
			env: map[string]string{
				"DD_TRACE_PROPAGATION_STYLE_EXTRACT": "tracecontext,datadog",
			},
			expectedInject:  "tracecontext",
			expectedExtract: "tracecontext,datadog",
		},
		{
			env: map[string]string{
				"DD_TRACE_PROPAGATION_STYLE":         "tracecontext,datadog",
				"DD_TRACE_PROPAGATION_STYLE_EXTRACT": "none",
			},
			expectedInject:  "tracecontext,datadog",
			expectedExtract: "tracecontext",
		},
		{
			env: map[string]string{
				// deprecated environment variable
				"DD_PROPAGATION_STYLE_INJECT":        "tracecontext,datadog",
				"DD_TRACE_PROPAGATION_STYLE_EXTRACT": "",
			},
			expectedInject:  "tracecontext,datadog",
			expectedExtract: "tracecontext",
		},
		{
			env: map[string]string{
				// deprecated environment variable
				"DD_PROPAGATION_STYLE_INJECT":        "tracecontext,datadog",
				"DD_TRACE_PROPAGATION_STYLE_EXTRACT": "b3",
			},
			expectedInject:  "tracecontext,datadog",
			expectedExtract: "b3,tracecontext",
		},
	}

	for i, test := range tests {
		t.Run(fmt.Sprintf("test #%v with env: %s", i, test.env), func(t *testing.T) {
			for k, v := range test.env {
				t.Setenv(k, v)
			}
			telemetryClient := new(telemetrytest.MockClient)
			defer telemetry.MockGlobalClient(telemetryClient)()

			p := NewTracerProvider()
			p.Tracer("")
			defer p.Shutdown()

			assert.True(t, telemetryClient.Started)
			telemetry.Check(t, telemetryClient.Configuration, "trace_propagation_style_inject", test.expectedInject)
			telemetry.Check(t, telemetryClient.Configuration, "trace_propagation_style_extract", test.expectedExtract)
		})
	}

}
