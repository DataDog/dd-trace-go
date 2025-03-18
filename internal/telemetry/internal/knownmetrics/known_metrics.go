// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

//go:generate go run github.com/DataDog/dd-trace-go/v2/internal/telemetry/internal/knownmetrics/generator

package knownmetrics

import (
	_ "embed"
	"encoding/json"
	"slices"

	"github.com/DataDog/dd-trace-go/v2/internal/log"
	"github.com/DataDog/dd-trace-go/v2/internal/telemetry/internal/transport"
)

//go:embed common_metrics.json
var commonMetricsJSON []byte

//go:embed golang_metrics.json
var golangMetricsJSON []byte

type Declaration struct {
	Namespace transport.Namespace  `json:"namespace"`
	Type      transport.MetricType `json:"type"`
	Name      string               `json:"name"`
}

var (
	commonMetrics = parseMetricNames(commonMetricsJSON)
	golangMetrics = parseMetricNames(golangMetricsJSON)
)

func parseMetricNames(bytes []byte) []Declaration {
	var names []Declaration
	if err := json.Unmarshal(bytes, &names); err != nil {
		log.Error("telemetry: failed to parse metric names: %v", err)
	}
	return names
}

// IsKnownMetric returns true if the given metric name is a known metric by the backend
// This is linked to generated common_metrics.json file and golang_metrics.json file. If you added new metrics to the backend, you should rerun the generator.
func IsKnownMetric(namespace transport.Namespace, typ transport.MetricType, name string) bool {
	decl := Declaration{Namespace: namespace, Type: typ, Name: name}
	return slices.Contains(commonMetrics, decl) || slices.Contains(golangMetrics, decl)
}

// IsCommonMetric returns true if the given metric name is a known common (cross-language) metric by the backend
// This is linked to the generated common_metrics.json file. If you added new metrics to the backend, you should rerun the generator.
func IsCommonMetric(namespace transport.Namespace, typ transport.MetricType, name string) bool {
	decl := Declaration{Namespace: namespace, Type: typ, Name: name}
	return slices.Contains(commonMetrics, decl)
}

// Size returns the total number of known metrics, including common and golang metrics
func Size() int {
	return len(commonMetrics) + len(golangMetrics)
}

// SizeWithFilter returns the total number of known metrics, including common and golang metrics, that pass the given filter
func SizeWithFilter(filter func(Declaration) bool) int {
	size := 0
	for _, decl := range commonMetrics {
		if filter(decl) {
			size++
		}
	}

	for _, decl := range golangMetrics {
		if filter(decl) {
			size++
		}
	}

	return size
}

// IsLanguageMetric returns true if the given metric name is a known Go language metric by the backend
// This is linked to the generated golang_metrics.json file. If you added new metrics to the backend, you should rerun the generator.
func IsLanguageMetric(typ transport.MetricType, name string) bool {
	decl := Declaration{Type: typ, Name: name}
	return slices.Contains(golangMetrics, decl)
}
