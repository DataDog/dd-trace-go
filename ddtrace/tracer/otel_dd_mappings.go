// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.
package tracer

// TODO: Move this into a separate package

import (
	"fmt"
	"os"
	"strings"

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
	remapper func(string) (string, error)
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
		dd:       "DD_TRACE_PROPAGATION_STYLE",
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
			v, err := config.remapper(otVal)
			if err != nil {
				log.Warn(err.Error())
				telemetry.GlobalClient.Count(telemetry.NamespaceTracers, "otel.env.invalid", 1.0, []string{config.dd, config.ot}, true)
			}
			val = v
		}
	}
	return val
}

var otelDdTags = map[string]string{
	"service.name":           "service",
	"deployment.environment": "env",
	"service.version":        "version",
}

func mapService(ot string) (string, error) {
	return ot, nil
}

func mapMetrics(ot string) (string, error) {
	if ot == "none" {
		return "false", nil
	}
	return "", fmt.Errorf("The following configuration is not supported: OTEL_METRICS_EXPORTER=%v", ot)
}

func mapLogLevel(ot string) (string, error) {
	if ot == "debug" {
		return "true", nil
	}
	return "", fmt.Errorf("The following configuration is not supported: OTEL_LOG_LEVEL=%v", ot)
}

func mapEnabled(ot string) (string, error) {
	if ot == "none" {
		return "false", nil
	}
	return "", fmt.Errorf("The following configuration is not supported: OTEL_METRICS_EXPORTER=%v", ot)
}

func otelTraceIDRatio() string {
	if v := os.Getenv("OTEL_TRACES_SAMPLER_ARG"); v != "" {
		return v
	}
	return "1.0"
}

func mapSampleRate(ot string) (string, error) {
	var unsupportedSamplerMapping = map[string]string{
		"always_on":    "parentbased_always_on",
		"always_off":   "parentbased_always_off",
		"traceidratio": "parentbased_traceidratio",
	}
	if v, ok := unsupportedSamplerMapping[ot]; ok {
		log.Warn("The following configuration is not supported: OTEL_TRACES_SAMPLER=%v. %v will be used", ot, v)
		ot = v
	}
	var samplerMapping = map[string]string{
		"parentbased_always_on":    "1.0",
		"parentbased_always_off":   "0.0",
		"parentbased_traceidratio": otelTraceIDRatio(),
	}
	if v, ok := samplerMapping[ot]; ok {
		return v, nil
	}

	return "", fmt.Errorf("unknown sampling configuration %v", ot)
}

func mapPropagationStyle(ot string) (string, error) {
	var propagationMapping = map[string]string{
		"tracecontext": "tracecontext",
		"b3":           "b3 single header",
		"b3multi":      "b3multi",
		"datadog":      "datadog",
		"none":         "none",
	}

	supportedStyles := make([]string, 0)
	for _, otStyle := range strings.Split(ot, ",") {
		otStyle = strings.TrimSpace(otStyle)
		if _, ok := propagationMapping[otStyle]; ok {
			supportedStyles = append(supportedStyles, propagationMapping[otStyle])
		} else {
			log.Warn("Invalid configuration: %v is not supported. This propagation style will be ignored.", otStyle)
		}
	}
	return strings.Join(supportedStyles, ","), nil
}
