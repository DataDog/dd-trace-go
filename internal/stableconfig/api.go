// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

// Package stableconfig provides utilities to load and manage APM configurations
// loaded from YAML configuration files
package stableconfig

import (
	"errors"
	"fmt"
	"iter"
	"os"
	"strconv"

	"github.com/DataDog/dd-trace-go/v2/internal/telemetry"
)

func reportTelemetryAndReturnWithErr(env string, value bool, origin telemetry.Origin, err error) (bool, telemetry.Origin, error) {
	if env == "DD_APPSEC_SCA_ENABLED" && origin == telemetry.OriginDefault {
		return value, origin, err
	}
	telemetry.RegisterAppConfig(telemetry.EnvToTelemetryName(env), value, origin)
	return value, origin, err
}

func reportTelemetryAndReturn(env string, value string, origin telemetry.Origin) (string, telemetry.Origin) {
	telemetry.RegisterAppConfig(telemetry.EnvToTelemetryName(env), value, origin)
	return value, origin
}

// Bool returns a boolean config value from managed file-based config, environment variable,
// or local file-based config, in that order. If none provide a valid boolean, it returns the default.
// Also returns the value's origin and any parse error encountered.
func Bool(env string, def bool) (value bool, origin telemetry.Origin, err error) {
	for o, v := range stableConfigByPriority(env) {
		if val, err := strconv.ParseBool(v); err == nil {
			return reportTelemetryAndReturnWithErr(env, val, o, nil)
		}
		err = errors.Join(err, fmt.Errorf("non-boolean value for %s: '%s' in %s configuration, dropping", env, v, o))
	}
	return reportTelemetryAndReturnWithErr(env, def, telemetry.OriginDefault, err)
}

// String returns a string config value from managed file-based config, environment variable,
// or local file-based config, in that order. If none are set, it returns the default value and origin.
func String(env string, def string) (string, telemetry.Origin) {
	for origin, value := range stableConfigByPriority(env) {
		return reportTelemetryAndReturn(env, value, origin)
	}
	return reportTelemetryAndReturn(env, def, telemetry.OriginDefault)
}

func stableConfigByPriority(env string) iter.Seq2[telemetry.Origin, string] {
	return func(yield func(telemetry.Origin, string) bool) {
		if v := ManagedConfig.Get(env); v != "" && !yield(telemetry.OriginManagedStableConfig, v) {
			return
		}
		if v, ok := os.LookupEnv(env); ok && !yield(telemetry.OriginEnvVar, v) {
			return
		}
		if v := LocalConfig.Get(env); v != "" && !yield(telemetry.OriginLocalStableConfig, v) {
			return
		}
	}
}
