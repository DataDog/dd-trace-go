// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package main

import (
	"net"
	"strconv"

	streamprocessingoffload "github.com/DataDog/dd-trace-go/contrib/haproxy/stream-processing-offload/v2"
	"github.com/DataDog/dd-trace-go/v2/instrumentation"
	"github.com/DataDog/dd-trace-go/v2/instrumentation/env"
)

// IntEnv returns the parsed int value of an environment variable, or
// def otherwise.
func intEnv(key string, def int) int {
	vv, ok := env.Lookup(key)
	if !ok {
		streamprocessingoffload.Instrumentation().TelemetryRegisterAppConfig(key, def, instrumentation.TelemetryOriginDefault)
		return def
	}
	v, err := strconv.Atoi(vv)
	if err != nil {
		log.Warn("Non-integer value for env var %s, defaulting to %d. Parse failed with error: %v", key, def, err)
		streamprocessingoffload.Instrumentation().TelemetryRegisterAppConfig(key, def, instrumentation.TelemetryOriginDefault)
		return def
	}
	streamprocessingoffload.Instrumentation().TelemetryRegisterAppConfig(key, def, instrumentation.TelemetryOriginEnvVar)
	return v
}

// IpEnv returns the valid IP value of an environment variable, or def otherwise.
func ipEnv(key string, def net.IP) net.IP {
	vv, ok := env.Lookup(key)
	if !ok {
		streamprocessingoffload.Instrumentation().TelemetryRegisterAppConfig(key, def.String(), instrumentation.TelemetryOriginDefault)
		return def
	}

	ip := net.ParseIP(vv)
	if ip == nil {
		log.Warn("Non-IP value for env var %s, defaulting to %s", key, def.String())
		streamprocessingoffload.Instrumentation().TelemetryRegisterAppConfig(key, def.String(), instrumentation.TelemetryOriginDefault)
		return def
	}
	streamprocessingoffload.Instrumentation().TelemetryRegisterAppConfig(key, vv, instrumentation.TelemetryOriginEnvVar)
	return ip
}
