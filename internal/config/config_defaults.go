// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package config

import (
	"math"
	"time"

	"github.com/DataDog/dd-trace-go/v2/internal"
)

// Default values for generated configuration fields.
// These are referenced by loadConfigGenerated() in config_generated.go.
// The naming convention is <fieldName>Default for the default value
// and <fieldName>Delimiter for map delimiters.
var (
	ciVisibilityAgentlessDefault        = false
	ciVisibilityEnabledDefault          = false
	dataStreamsMonitoringEnabledDefault  = false
	debugDefault                        = false
	debugAbandonedSpansDefault          = false
	debugStackDefault                   = true
	dynamicInstrumentationEnabledDefault = false
	envDefault                          = ""
	globalSampleRateDefault             = math.NaN()
	logDirectoryDefault                 = ""
	logStartupDefault                   = true
	logsOTelEnabledDefault              = false
	partialFlushEnabledDefault          = false
	partialFlushMinSpansDefault         = 1000
	peerServiceDefaultsEnabledDefault   = false
	peerServiceMappingsDefault          map[string]string
	peerServiceMappingsDelimiter        = internal.DDTagsDelimiter
	profilerEndpointsDefault            = true
	profilerHotspotsDefault             = true
	retryIntervalDefault                = time.Millisecond
	runtimeMetricsDefault               = false
	runtimeMetricsV2Default             = true
	serviceMappingsDefault              map[string]string
	serviceMappingsDelimiter            = internal.DDTagsDelimiter
	serviceNameDefault                  = ""
	spanTimeoutDefault                  = 10 * time.Minute
	statsComputationEnabledDefault      = true
	traceID128BitEnabledDefault         = true
	traceRateLimitPerSecondDefault      = DefaultRateLimit
	versionDefault                      = ""
)
