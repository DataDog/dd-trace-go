// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package config

import "github.com/DataDog/dd-trace-go/v2/internal/log"

const (
	// DefaultRateLimit specifies the default rate limit per second for traces.
	DefaultRateLimit = 100.0
)

func validateSampleRate(rate float64) bool {
	if rate < 0.0 || rate > 1.0 {
		log.Warn("ignoring DD_TRACE_SAMPLE_RATE: out of range %f", rate)
		return false
	}
	return true
}

func validateRateLimit(rate float64) bool {
	if rate < 0.0 {
		log.Warn("ignoring DD_TRACE_RATE_LIMIT: negative value %f", rate)
		return false
	}
	return true
}
