// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package profiler

import (
	"time"

	v2 "github.com/DataDog/dd-trace-go/v2/profiler"
)

// customProfileLabelLimit is the maximum number of pprof labels which can
// be used as custom attributes in the profiler UI
const customProfileLabelLimit = 10

// Start starts the profiler. If the profiler is already running, it will be
// stopped and restarted with the given options.
//
// It may return an error if an API key is not provided by means of the
// WithAPIKey option, or if a hostname is not found.
//
// If DD_PROFILING_ENABLED=false is set in the process environment, it will
// prevent the profiler from starting.
func Start(opts ...Option) error {
	// HACK: quick fix for removing any nil options without releasing a new v2 version
	var filteredOpts []Option
	for _, opt := range opts {
		if opt != nil {
			filteredOpts = append(filteredOpts, opt)
		}
	}
	return v2.Start(filteredOpts...)
}

// Stop cancels any ongoing profiling or upload operations and returns after
// everything has been stopped.
func Stop() {
	v2.Stop()
}

// StatsdClient implementations can count and time certain event occurrences that happen
// in the profiler.
type StatsdClient interface {
	// Count counts how many times an event happened, at the given rate using the given tags.
	Count(event string, times int64, tags []string, rate float64) error
	// Timing creates a histogram metric of the values registered as the duration of a certain event.
	Timing(event string, duration time.Duration, tags []string, rate float64) error
}
