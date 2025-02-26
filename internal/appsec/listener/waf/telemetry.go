// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package waf

import (
	"strconv"

	waf "github.com/DataDog/go-libddwaf/v3"

	"gopkg.in/DataDog/dd-trace-go.v1/internal/telemetry"
)

type TelemetryMetrics struct {
	TruncationCounts        map[waf.TruncationReason]telemetry.MetricHandle
	TruncationDistributions map[waf.TruncationReason]telemetry.MetricHandle
	WafRunMetrics           map[string]telemetry.MetricHandle
}

type Telemetry struct {
	BaseTelemetryTags []string
	Metrics           TelemetryMetrics
}

func NewTelemetryHandler(rulesVersion string) *Telemetry {
	tags := []string{
		"event_rules_version:" + rulesVersion,
		"waf_version:" + waf.Version(),
	}
	return &Telemetry{
		BaseTelemetryTags: tags,
		Metrics: TelemetryMetrics{
			TruncationCounts: map[waf.TruncationReason]telemetry.MetricHandle{
				waf.StringTooLong:     telemetry.Count(telemetry.NamespaceAppSec, "waf.input_truncated", []string{"truncation_reason+" + strconv.Itoa(int(waf.StringTooLong))}),
				waf.ContainerTooLarge: telemetry.Count(telemetry.NamespaceAppSec, "waf.input_truncated", []string{"truncation_reason+" + strconv.Itoa(int(waf.ContainerTooLarge))}),
				waf.ObjectTooDeep:     telemetry.Count(telemetry.NamespaceAppSec, "waf.input_truncated", []string{"truncation_reason+" + strconv.Itoa(int(waf.ObjectTooDeep))}),
			},
			TruncationDistributions: map[waf.TruncationReason]telemetry.MetricHandle{
				waf.StringTooLong:     telemetry.Count(telemetry.NamespaceAppSec, "waf.truncated_value_size", []string{"truncation_reason+" + strconv.Itoa(int(waf.StringTooLong))}),
				waf.ContainerTooLarge: telemetry.Count(telemetry.NamespaceAppSec, "waf.truncated_value_size", []string{"truncation_reason+" + strconv.Itoa(int(waf.ContainerTooLarge))}),
				waf.ObjectTooDeep:     telemetry.Count(telemetry.NamespaceAppSec, "waf.truncated_value_size", []string{"truncation_reason+" + strconv.Itoa(int(waf.ObjectTooDeep))}),
			},
			WafRunMetrics: map[string]telemetry.MetricHandle{
				"rasp.timeout":     telemetry.Count(telemetry.NamespaceAppSec, "rasp.timeout", tags),
				"waf.duration":     telemetry.Distribution(telemetry.NamespaceAppSec, "waf.duration", tags),
				"waf.duration_ext": telemetry.Distribution(telemetry.NamespaceAppSec, "waf.duration_ext", tags),
			},
		},
	}
}
