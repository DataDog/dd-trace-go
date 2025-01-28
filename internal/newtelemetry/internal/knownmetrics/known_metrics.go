// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

//go:generate go run gopkg.in/DataDog/dd-trace-go.v1/internal/newtelemetry/internal/metrics/generator

package knownmetrics

import (
	_ "embed"
	"encoding/json"
	"slices"

	"gopkg.in/DataDog/dd-trace-go.v1/internal/log"
)

//go:embed common_metrics.json
var commonMetricsJSON []byte

//go:embed golang_metrics.json
var golangMetricsJSON []byte

var (
	commonMetrics = parseMetricNames(commonMetricsJSON)
	golangMetrics = parseMetricNames(golangMetricsJSON)
)

func parseMetricNames(bytes []byte) []string {
	var names []string
	if err := json.Unmarshal(bytes, &names); err != nil {
		log.Error("telemetry: failed to parse metric names: %v", err)
	}
	return names
}

// IsKnownMetricName returns true if the given metric name is a known metric by the backend
// This is linked to generated common_metrics.json file and golang_metrics.json file. If you added new metrics to the backend, you should rerun the generator.
func IsKnownMetricName(name string) bool {
	return slices.Contains(commonMetrics, name) || slices.Contains(golangMetrics, name)
}

// IsCommonMetricName returns true if the given metric name is a known common (cross-language) metric by the backend
// This is linked to the generated common_metrics.json file. If you added new metrics to the backend, you should rerun the generator.
func IsCommonMetricName(name string) bool {
	return slices.Contains(commonMetrics, name)
}

// IsLanguageMetricName returns true if the given metric name is a known Go language metric by the backend
// This is linked to the generated golang_metrics.json file. If you added new metrics to the backend, you should rerun the generator.
func IsLanguageMetricName(name string) bool {
	return slices.Contains(golangMetrics, name)
}
