// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.
package tracer

import (
	"gopkg.in/DataDog/dd-trace-go.v1/internal/log"
)

var otelDdEnvvars = map[string]string {
	"OTEL_SERVICE_NAME": "DD_SERVICE",
	// "OTEL_RESOURCE_ATTRIBUTES": "DD_TAGS" ?
	"OTEL_METRICS_EXPORTER": "DD_RUNTIME_METRICS_ENABLED",
	"OTEL_LOG_LEVEL": "DD_TRACE_DEBUG",
	// "OTEL_TRACES_EXPORTER": "DD_TRACE_ENABLED",
}

var otelRemapper = map[string]func(string) string {
	"OTEL_SERVICE_NAME": serviceName,
	"OTEL_METRICS_EXPORTER": metrics,
	"OTEL_LOG_LEVEL": logLevel,
}

// var configChanges = map[string]func(*config) {
// 	"DD_SERVICE": 
// }

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