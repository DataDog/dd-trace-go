// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package main

import (
	"net"
	"strconv"
	"time"

	apimcallout "github.com/DataDog/dd-trace-go/contrib/azure/apim-callout/v2"

	"github.com/DataDog/dd-trace-go/v2/instrumentation"
	"github.com/DataDog/dd-trace-go/v2/instrumentation/env"
)

// intEnv returns the parsed int value of an environment variable, or def otherwise.
func intEnv(key string, def int) int {
	vv, ok := env.Lookup(key)
	if !ok {
		apimcallout.Instrumentation().TelemetryRegisterAppConfig(key, def, instrumentation.TelemetryOriginDefault)
		return def
	}
	v, err := strconv.Atoi(vv)
	if err != nil {
		log.Warn("Non-integer value for env var %s, defaulting to %d. Parse failed with error: %v", key, def, err.Error())
		apimcallout.Instrumentation().TelemetryRegisterAppConfig(key, def, instrumentation.TelemetryOriginDefault)
		return def
	}
	apimcallout.Instrumentation().TelemetryRegisterAppConfig(key, v, instrumentation.TelemetryOriginEnvVar)
	return v
}

// ipEnv returns the valid IP value of an environment variable, or def otherwise.
func ipEnv(key string, def net.IP) net.IP {
	vv, ok := env.Lookup(key)
	if !ok {
		apimcallout.Instrumentation().TelemetryRegisterAppConfig(key, def.String(), instrumentation.TelemetryOriginDefault)
		return def
	}

	ip := net.ParseIP(vv)
	if ip == nil {
		log.Warn("Non-IP value for env var %s, defaulting to %s", key, def.String())
		apimcallout.Instrumentation().TelemetryRegisterAppConfig(key, def.String(), instrumentation.TelemetryOriginDefault)
		return def
	}
	apimcallout.Instrumentation().TelemetryRegisterAppConfig(key, vv, instrumentation.TelemetryOriginEnvVar)
	return ip
}

// stringEnv returns the string value of an environment variable, or def otherwise.
func stringEnv(key, def string) string {
	v, ok := env.Lookup(key)
	if !ok {
		apimcallout.Instrumentation().TelemetryRegisterAppConfig(key, def, instrumentation.TelemetryOriginDefault)
		return def
	}
	apimcallout.Instrumentation().TelemetryRegisterAppConfig(key, v, instrumentation.TelemetryOriginEnvVar)
	return v
}

// durationEnv returns the parsed duration value of an environment variable, or def otherwise.
func durationEnv(key string, def time.Duration) time.Duration {
	vv, ok := env.Lookup(key)
	if !ok {
		apimcallout.Instrumentation().TelemetryRegisterAppConfig(key, def.String(), instrumentation.TelemetryOriginDefault)
		return def
	}
	v, err := time.ParseDuration(vv)
	if err != nil {
		log.Warn("Invalid duration for env var %s, defaulting to %s. Parse failed with error: %v", key, def.String(), err.Error())
		apimcallout.Instrumentation().TelemetryRegisterAppConfig(key, def.String(), instrumentation.TelemetryOriginDefault)
		return def
	}
	apimcallout.Instrumentation().TelemetryRegisterAppConfig(key, v.String(), instrumentation.TelemetryOriginEnvVar)
	return v
}

// boolEnv returns the parsed bool value of an environment variable, or def otherwise.
func boolEnv(key string, def bool) bool {
	vv, ok := env.Lookup(key)
	if !ok {
		apimcallout.Instrumentation().TelemetryRegisterAppConfig(key, def, instrumentation.TelemetryOriginDefault)
		return def
	}
	v, err := strconv.ParseBool(vv)
	if err != nil {
		log.Warn("Non-boolean value for env var %s, defaulting to %v. Parse failed with error: %v", key, def, err.Error())
		apimcallout.Instrumentation().TelemetryRegisterAppConfig(key, def, instrumentation.TelemetryOriginDefault)
		return def
	}
	apimcallout.Instrumentation().TelemetryRegisterAppConfig(key, v, instrumentation.TelemetryOriginEnvVar)
	return v
}
