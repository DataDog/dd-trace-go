// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.
package tracer

import (
	"os"

	"gopkg.in/DataDog/dd-trace-go.v1/internal/log"
)

var otelDdEnvvars = map[string]string {
	"OTEL_SERVICE_NAME": "DD_SERVICE",
	"OTEL_METRICS_EXPORTER": "DD_RUNTIME_METRICS_ENABLED",
	"OTEL_LOG_LEVEL": "DD_TRACE_DEBUG",
	"OTEL_TRACES_EXPORTER": "DD_TRACE_ENABLED",
	"OTEL_TRACES_SAMPLER": "DD_TRACE_SAMPLE_RATE",
	"OTEL_PROPAGATORS": headerPropagationStyle,
}

var otelRemapper = map[string]func(string) string {
	"OTEL_SERVICE_NAME": serviceName,
	"OTEL_METRICS_EXPORTER": metrics,
	"OTEL_LOG_LEVEL": logLevel,
	"OTEL_TRACES_EXPORTER": enabled,
	"OTEL_TRACES_SAMPLER": sampleRate,
	"OTEL_PROPAGATORS": propagationStyle,
}

var otelDdTags = map[string]string {
	"service.name": "service",
	"deployment.environment": "env",
	"service.version": "version",
}

func serviceName(ot string) string {
	return ot
}

func metrics(ot string) string {
	if ot == "none" {
		return "false"
	}
	log.Warn("Unrecognized setting: OTEL_METRICS_EXPORTER=%v", ot)
	return ""
}

func logLevel(ot string) string {
	if ot == "debug" {
		return "true"
	}
	log.Warn("Unrecognized setting: OTEL_LOG_LEVEL=%v", ot)
	return ""
}

func enabled(ot string) string {
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

func sampleRate(ot string) string{
	var otelDdSamplerMapping = map[string]string {
		"parentbased_always_on": "1.0",
		"parentbased_always_off": "0.0",
		"parentbased_traceidratio": otelTraceIdRatio(),
	}
	if v, ok :=  otelDdSamplerMapping[ot]; ok {
		return v
	}
	log.Warn("Unknown sampling configuration %v", ot)
	return ""
}

func propagationStyle(ot string) string {
	if ot == "b3" {
		ot = "b3 single header"
	}
	return ot
}
