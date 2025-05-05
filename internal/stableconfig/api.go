// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

// Package stableconfig provides utilities to load and manage APM configurations
// loaded from YAML configuration files
package stableconfig

import (
	"fmt"
	"os"
	"strconv"

	"github.com/DataDog/dd-trace-go/v2/internal/telemetry"
)

// BoolStableConfig returns a boolean config value from managed file-based config, environment variable,
// or local file-based config, in that order. If none provide a valid boolean, it returns the default.
// Also returns the value's origin and any parse error encountered.
func BoolStableConfig(env string, def bool) (value bool, origin telemetry.Origin, err error) {
	err = nil
	if v := ManagedConfig.Get(env); v != "" {
		if vv, parseErr := strconv.ParseBool(v); parseErr == nil {
			return vv, telemetry.OriginManagedStableConfig, nil
		}
		err = fmt.Errorf("non-boolean value for %s: '%s' in fleet-managed configuration file, dropping", env, v)
	}
	if v, ok := os.LookupEnv(env); ok {
		if vv, parseErr := strconv.ParseBool(v); parseErr == nil {
			return vv, telemetry.OriginEnvVar, nil
		}
		err = fmt.Errorf("could not parse %s value `%s` as a boolean value", env, v)
	}
	if v := LocalConfig.Get(env); v != "" {
		if vv, parseErr := strconv.ParseBool(v); parseErr == nil {
			return vv, telemetry.OriginLocalStableConfig, nil
		}
		err = fmt.Errorf("non-boolean value for %s: '%s' in local configuration file, dropping", env, v)
	}
	return def, telemetry.OriginDefault, err
}

// StringStableConfig returns a string config value from managed file-based config, environment variable,
// or local file-based config, in that order. If none are set, it returns the default value and origin.
func StringStableConfig(env string, def string) (string, telemetry.Origin) {
	if v := ManagedConfig.Get(env); v != "" {
		return v, telemetry.OriginManagedStableConfig
	}
	if v, ok := os.LookupEnv(env); ok {
		return v, telemetry.OriginEnvVar
	}
	if v := LocalConfig.Get(env); v != "" {
		return v, telemetry.OriginLocalStableConfig
	}
	return def, telemetry.OriginDefault
}
