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

// Bool returns a boolean config value from managed file-based config, environment variable,
// or local file-based config, in that order. If none provide a valid boolean, it returns the default.
// Also returns the value's origin and any parse error encountered.
func Bool(env string, def bool) (value bool, origin telemetry.Origin, err error) {
	for o, v := range stableConfigByPriority(env) {
		if val, err := strconv.ParseBool(v); err == nil {
			return val, o, nil
		}
		err = errors.Join(err, fmt.Errorf("non-boolean value for %s: '%s' in %s configuration, dropping", env, v, o))
	}
	return def, telemetry.OriginDefault, err
}

// String returns a string config value from managed file-based config, environment variable,
// or local file-based config, in that order. If none are set, it returns the default value and origin.
func String(env string, def string) (string, telemetry.Origin) {
	for origin, value := range stableConfigByPriority(env) {
		return value, origin
	}
	return def, telemetry.OriginDefault
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
