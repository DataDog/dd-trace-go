// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package config

import (
	"fmt"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/internal/config/provider"
	"github.com/DataDog/dd-trace-go/v2/internal/log"
	"github.com/DataDog/dd-trace-go/v2/internal/telemetry"
	"github.com/DataDog/dd-trace-go/v2/internal/telemetry/telemetrytest"
)

func TestOTLPGenericProtocolSourceFallback(t *testing.T) {
	for _, tc := range []struct {
		name, lower, higher, logs, metrics string
	}{
		{"logs fallback", "grpc", "http/json", "grpc", "http/json"},
		{"metrics fallback", "http/json", "grpc", "grpc", "http/json"},
		{"shared higher priority", "http/json", "http/protobuf", "http/protobuf", "http/protobuf"},
		{"signal-specific higher priority", "http/protobuf", "http/json", "http/protobuf", "http/json"},
		{"invalid higher priority", "grpc", "typo", "grpc", "http/protobuf"},
		{"normalized fallback", " GRPC ", " HTTP/JSON ", "grpc", "http/json"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("OTEL_EXPORTER_OTLP_LOGS_PROTOCOL", "")
			t.Setenv("OTEL_EXPORTER_OTLP_METRICS_PROTOCOL", "")
			generic := []string{tc.lower, tc.higher}
			p := provider.NewEnvironment()
			assert.Equal(t, tc.logs, resolveOTLPProtocol(resolveOTLPSetting(p, "OTEL_EXPORTER_OTLP_LOGS_PROTOCOL", "OTEL_EXPORTER_OTLP_PROTOCOL", generic, supportsOTLPProtocol(otlpLogsProtocols), false)))
			assert.Equal(t, tc.metrics, resolveOTLPProtocol(resolveOTLPSetting(p, "OTEL_EXPORTER_OTLP_METRICS_PROTOCOL", "OTEL_EXPORTER_OTLP_PROTOCOL", generic, supportsOTLPProtocol(otlpMetricsProtocols), false)))
		})
	}
}

func TestOTLPInvalidGenericProtocolWarning(t *testing.T) {
	for _, tc := range []struct {
		name, enabled, specific, protocol string
	}{
		{"logs", "DD_LOGS_OTEL_ENABLED", "OTEL_EXPORTER_OTLP_LOGS_PROTOCOL", "grpc"},
		{"span metrics", "OTEL_TRACES_SPAN_METRICS_ENABLED", "OTEL_EXPORTER_OTLP_METRICS_PROTOCOL", "http/json"},
	} {
		for _, enabled := range []bool{false, true} {
			for _, overridden := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/enabled=%t/overridden=%t", tc.name, enabled, overridden), func(t *testing.T) {
					t.Setenv("OTEL_EXPORTER_OTLP_PROTOCOL", "typo")
					t.Setenv(tc.enabled, strconv.FormatBool(enabled))
					t.Setenv(tc.specific, "")
					if overridden {
						t.Setenv(tc.specific, tc.protocol)
					}
					tp := new(log.RecordLogger)
					defer log.UseLogger(tp)()

					cfg := loadConfig()
					protocol := cfg.OTLPLogsProtocol()
					if tc.name == "span metrics" {
						protocol = cfg.OTLPMetricsProtocol()
					}
					want := "http/protobuf"
					if overridden {
						want = tc.protocol
					}
					assert.Equal(t, want, protocol)
					// A value no signal supports warns even when unused.
					assert.Contains(t, strings.Join(tp.Logs(), "\n"), "Unsupported OTEL_EXPORTER_OTLP_PROTOCOL value")
				})
			}
		}
	}
}

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
			// The signal-specific value is supported and takes precedence, so
			// the generic value is never used and no warning fires.
			name: "supported metrics protocol overrides unsupported generic",
			env: map[string]string{
				"OTEL_TRACES_SPAN_METRICS_ENABLED":    "true",
				"OTEL_EXPORTER_OTLP_PROTOCOL":         "grpc",
				"OTEL_EXPORTER_OTLP_METRICS_PROTOCOL": "http/json",
			},
			wantProtocol: "http/json",
		},
		{
			name: "generic grpc shadowed by grpc signal-specific, span metrics enabled",
			env: map[string]string{
				"OTEL_TRACES_SPAN_METRICS_ENABLED":    "true",
				"OTEL_EXPORTER_OTLP_PROTOCOL":         "grpc",
				"OTEL_EXPORTER_OTLP_METRICS_PROTOCOL": "grpc",
			},
			wantWarn: true,
		},
		{
			// DD_TRACE_AGENT_PROTOCOL_VERSION forces otlpExportMode off, but the
			// explicit OTEL_TRACES_SPAN_METRICS_ENABLED pointer still enables the
			// exporter, so the warning must fire.
			name: "agent protocol override keeps explicit span metrics warning",
			env: map[string]string{
				"DD_TRACE_AGENT_PROTOCOL_VERSION":  "0.4",
				"OTEL_TRACES_SPAN_METRICS_ENABLED": "true",
				"OTEL_EXPORTER_OTLP_PROTOCOL":      "grpc",
			},
			wantWarn:     true,
			wantProtocol: "http/protobuf",
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

func TestOTLPGenericProtocolWarning(t *testing.T) {
	const warning = "Unsupported OTEL_EXPORTER_OTLP_PROTOCOL value; falling back to default."
	for _, tc := range []struct {
		name         string
		generic      string
		logsOverride string
		wantProtocol string
		wantWarning  bool
	}{
		{name: "invalid protocol warns and uses default", generic: "invalid", wantProtocol: "http/protobuf", wantWarning: true},
		{name: "valid protocol does not warn", generic: "grpc", wantProtocol: "grpc"},
		{name: "unset protocol uses default without warning", wantProtocol: "http/protobuf"},
		{name: "invalid generic still warns with a valid logs override", generic: "invalid", logsOverride: "grpc", wantProtocol: "grpc", wantWarning: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Invalid generic configuration is reported even with exporters disabled.
			t.Setenv("DD_LOGS_OTEL_ENABLED", "false")
			t.Setenv("OTEL_TRACES_SPAN_METRICS_ENABLED", "false")
			t.Setenv("OTEL_EXPORTER_OTLP_PROTOCOL", tc.generic)
			t.Setenv("OTEL_EXPORTER_OTLP_LOGS_PROTOCOL", tc.logsOverride)
			rec := new(log.RecordLogger)
			defer log.UseLogger(rec)()

			cfg := loadConfig()
			assert.Equal(t, tc.wantProtocol, cfg.OTLPLogsProtocol())
			logs := strings.Join(rec.Logs(), "\n")
			if tc.wantWarning {
				assert.Contains(t, logs, warning)
			} else {
				assert.NotContains(t, logs, warning)
			}
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
