// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package config

import (
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/internal/log"
	"github.com/DataDog/dd-trace-go/v2/internal/telemetry"
	"github.com/DataDog/dd-trace-go/v2/internal/telemetry/telemetrytest"
)

func TestOTLPMetricsProtocolWarning(t *testing.T) {
	const unsupported = "Unsupported OTEL_EXPORTER_OTLP"
	for _, tc := range []struct {
		name         string
		env          map[string]string
		wantWarn     bool
		wantProtocol string
	}{
		{
			name: "generic grpc, OTLP metrics export disabled",
			env:  map[string]string{"OTEL_EXPORTER_OTLP_PROTOCOL": "grpc"},
		},
		{
			name: "metrics grpc, OTLP metrics export disabled",
			env:  map[string]string{"OTEL_EXPORTER_OTLP_METRICS_PROTOCOL": "grpc"},
		},
		{
			name: "generic grpc, OTLP traces without span metrics",
			env: map[string]string{
				"OTEL_TRACES_EXPORTER":        "otlp",
				"OTEL_EXPORTER_OTLP_PROTOCOL": "grpc",
			},
		},
		{
			name: "generic grpc, span metrics explicitly disabled",
			env: map[string]string{
				"OTEL_TRACES_EXPORTER":             "otlp",
				"DD_METRICS_OTEL_ENABLED":          "true",
				"OTEL_TRACES_SPAN_METRICS_ENABLED": "false",
				"OTEL_EXPORTER_OTLP_PROTOCOL":      "grpc",
			},
		},
		{
			name: "generic grpc, span metrics auto-enabled",
			env: map[string]string{
				"OTEL_TRACES_EXPORTER":        "otlp",
				"DD_METRICS_OTEL_ENABLED":     "true",
				"OTEL_EXPORTER_OTLP_PROTOCOL": "grpc",
			},
			wantWarn: true,
		},
		{
			name: "metrics grpc, span metrics explicitly enabled",
			env: map[string]string{
				"OTEL_TRACES_SPAN_METRICS_ENABLED":    "true",
				"OTEL_EXPORTER_OTLP_METRICS_PROTOCOL": "grpc",
			},
			wantWarn: true,
		},
		{
			name: "supported protocol, span metrics explicitly enabled",
			env: map[string]string{
				"OTEL_TRACES_SPAN_METRICS_ENABLED": "true",
				"OTEL_EXPORTER_OTLP_PROTOCOL":      "http/json",
			},
			wantProtocol: "http/json",
		},
		{
			name: "generic grpc shadowed by supported signal-specific, span metrics enabled",
			env: map[string]string{
				"OTEL_TRACES_SPAN_METRICS_ENABLED":    "true",
				"OTEL_EXPORTER_OTLP_PROTOCOL":        "grpc",
				"OTEL_EXPORTER_OTLP_METRICS_PROTOCOL": "http/json",
			},
			wantProtocol: "http/json",
		},
		{
			name: "generic grpc shadowed by grpc signal-specific, span metrics enabled",
			env: map[string]string{
				"OTEL_TRACES_SPAN_METRICS_ENABLED":    "true",
				"OTEL_EXPORTER_OTLP_PROTOCOL":        "grpc",
				"OTEL_EXPORTER_OTLP_METRICS_PROTOCOL": "grpc",
			},
			wantWarn: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resetGlobalState()
			defer resetGlobalState()

			for k, v := range tc.env {
				t.Setenv(k, v)
			}
			tp := new(log.RecordLogger)
			defer log.UseLogger(tp)()

			cfg := Get()
			require.NotNil(t, cfg)

			logs := strings.Join(tp.Logs(), "\n")
			if tc.wantWarn {
				assert.Contains(t, logs, unsupported)
			} else {
				assert.NotContains(t, logs, unsupported)
			}
			want := tc.wantProtocol
			if want == "" {
				want = "http/protobuf"
			}
			assert.Equal(t, want, cfg.OTLPMetricsProtocol())
		})
	}
}

func TestOTLPMetricsProtocolTelemetryUnchangedWhenExportDisabled(t *testing.T) {
	// The unsupported value is still reported as configured so that the
	// user's environment stays visible in telemetry.
	for _, name := range []string{"OTEL_EXPORTER_OTLP_PROTOCOL", "OTEL_EXPORTER_OTLP_METRICS_PROTOCOL"} {
		for _, enabled := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/enabled=%t", name, enabled), func(t *testing.T) {
				resetGlobalState()
				defer resetGlobalState()

				rec := new(telemetrytest.RecordClient)
				defer telemetry.MockClient(rec)()
				t.Setenv(name, "grpc")
				if enabled {
					t.Setenv("OTEL_TRACES_SPAN_METRICS_ENABLED", "true")
				}

				cfg := Get()
				require.NotNil(t, cfg)
				assert.Equal(t, "http/protobuf", cfg.OTLPMetricsProtocol())

				var got []telemetry.Configuration
				for _, c := range rec.Configuration {
					if c.Name == name && c.Value == "grpc" {
						got = append(got, c)
					}
				}
				require.NotEmpty(t, got, "expected %s=grpc in telemetry", name)
				assert.Equal(t, telemetry.OriginEnvVar, got[0].Origin)
			})
		}
	}
}
