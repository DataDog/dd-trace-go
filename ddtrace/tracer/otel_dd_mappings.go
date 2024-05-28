// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.
package tracer

// TODO: Move this into a separate package

import (
	"os"

	"gopkg.in/DataDog/dd-trace-go.v1/internal/log"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/telemetry"
)

// otelDDOpt represents tracer configuration that can be modified by both DD and OTEL env vars
type otelDDOpt int

const (
	service otelDDOpt = iota
	metrics
	debugMode
	enabled
	sampleRate
	propagationStyle
)

// otelDDEnv contains env vars from both dd (DD) and ot (OTEL) that map to the same tracer configuration
// remapper contains functionality to remap OTEL values to DD values
type otelDDEnv struct {
	dd       string
	ot       string
	remapper func(string) string
}

var otelDDConfigs = map[otelDDOpt]*otelDDEnv{
	service: {
		dd:       "DD_SERVICE",
		ot:       "OTEL_SERVICE_NAME",
		remapper: mapService,
	},
	metrics: {
		dd:       "DD_RUNTIME_METRICS_ENABLED",
		ot:       "OTEL_METRICS_EXPORTER",
		remapper: mapMetrics,
	},
	debugMode: {
		dd:       "DD_TRACE_DEBUG",
		ot:       "OTEL_LOG_LEVEL",
		remapper: mapLogLevel,
	},
	enabled: {
		dd:       "DD_TRACE_ENABLED",
		ot:       "OTEL_TRACES_EXPORTER",
		remapper: mapEnabled,
	},
	sampleRate: {
		dd:       "DD_TRACE_SAMPLE_RATE",
		ot:       "OTEL_TRACES_SAMPLER",
		remapper: mapSampleRate,
	},
	propagationStyle: {
		dd:       headerPropagationStyle,
		ot:       "OTEL_PROPAGATORS",
		remapper: mapPropagationStyle,
	},
}

// assessSource determines whether the provided otelDDOpt will be set via DD or OTEL env vars, and returns the value
func assessSource(cfgName otelDDOpt) string {
	config, ok := otelDDConfigs[cfgName]
	if !ok {
		return ""
	}
	val := os.Getenv(config.dd)
	if otVal := os.Getenv(config.ot); otVal != "" {
		if val != "" {
			log.Warn("Both %v and %v are set, using %v=%v", config.ot, config.dd, config.dd, val)
			telemetry.GlobalClient.Count(telemetry.NamespaceTracers, "otel.env.hiding", 1.0, []string{config.dd, config.ot}, true)
		} else {
			val = config.remapper(otVal)
			if val == "" {
				telemetry.GlobalClient.Count(telemetry.NamespaceTracers, "otel.env.invalid", 1.0, []string{config.dd, config.ot}, true)
			}
		}
	}
	return val
}

var otelDdTags = map[string]string{
	"service.name":           "service",
	"deployment.environment": "env",
	"service.version":        "version",
}

func mapService(ot string) string {
	return ot
}

func mapMetrics(ot string) string {
	if ot == "none" {
		return "false"
	}
	log.Warn("Unrecognized setting: OTEL_METRICS_EXPORTER=%v", ot)
	return ""
}

func mapLogLevel(ot string) string {
	if ot == "debug" {
		return "true"
	}
	log.Warn("Unrecognized setting: OTEL_LOG_LEVEL=%v", ot)
	return ""
}

func mapEnabled(ot string) string {
	if ot == "none" {
		return "false"
	}
	log.Warn("Unrecognized setting: OTEL_METRICS_EXPORTER=%v", ot)
	return ""
}

func otelTraceIdRatio() string {
	if v := os.Getenv("OTEL_TRACES_SAMPLER_ARG"); v != "" {
		return v
	}
	return "1.0"
}

func mapSampleRate(ot string) string {
	var otelDdSamplerMapping = map[string]string{
		"parentbased_always_on":    "1.0",
		"parentbased_always_off":   "0.0",
		"parentbased_traceidratio": otelTraceIdRatio(),
	}
	if v, ok := otelDdSamplerMapping[ot]; ok {
		return v
	}
	log.Warn("Unknown sampling configuration %v", ot)
	return ""
}

func mapPropagationStyle(ot string) string {
	if ot == "b3" {
		ot = "b3 single header"
	}
	return ot
}
