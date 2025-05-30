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
	"net"
	"os"
	"strconv"
	"time"

	"github.com/DataDog/dd-trace-go/v2/internal/telemetry"
)

const (
	originManagedStableConfig = "fleet application_monitoring.yaml"
	originLocalStableConfig   = "local application_monitoring.yaml"
	originEnvVar              = "environment variable"
)

// Bool returns a boolean config value from managed file-based config, environment variable,
// or local file-based config, in that order. If none provide a valid boolean, it returns the default.
// Also returns the value's origin and any parse error encountered.
func Bool(env string, def bool) (value bool, origin telemetry.Origin, err error) {
	return parseConfigValue(env, def, strconv.ParseBool)
}

// String returns a string config value from managed file-based config, environment variable,
// or local file-based config, in that order. If none are set, it returns the default value and origin.
func String(env string, def string) (string, telemetry.Origin) {
	val, origin, _ := parseConfigValue(env, def, func(s string) (string, error) { return s, nil })
	return val, origin
}

func Int(env string, def int) (v int, o telemetry.Origin) {
	val, origin, _ := parseConfigValue(env, def, func(s string) (int, error) { return strconv.Atoi(s) })
	return val, origin
}

func Float(env string, def float64) (v float64, o telemetry.Origin) {
	val, origin, _ := parseConfigValue(env, def, func(s string) (float64, error) { return strconv.ParseFloat(s, 64) })
	return val, origin
}

func Duration(env string, def time.Duration) (v time.Duration, o telemetry.Origin) {
	val, origin, _ := parseConfigValue(env, def, func(s string) (time.Duration, error) { return time.ParseDuration(s) })
	return val, origin
}

func IP(env string, def net.IP) (v net.IP, o telemetry.Origin) {
	val, origin, _ := parseConfigValue(env, def, func(s string) (net.IP, error) { return net.ParseIP(s), nil })
	return val, origin
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

// parseConfigValue is a helper function that takes a string value and attempts to parse it into type T.
// It returns the parsed value, origin, and any error encountered.
func parseConfigValue[T any](env string, def T, parse func(string) (T, error)) (T, telemetry.Origin, error) {
	var errs []error
	for o, v := range stableConfigByPriority(env) {
		if val, err := parse(v); err == nil {
			return val, o, nil
		} else {
			printOrigin := o
			switch o {
			case telemetry.OriginManagedStableConfig:
				printOrigin = originManagedStableConfig
			case telemetry.OriginLocalStableConfig:
				printOrigin = originLocalStableConfig
			case telemetry.OriginEnvVar:
				printOrigin = originEnvVar
			}
			errs = append(errs, fmt.Errorf("invalid value for %s: '%s' in %s configuration, dropping", env, v, printOrigin))
		}
	}
	return def, telemetry.OriginDefault, errors.Join(errs...)
}
