// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package stableconfig

import (
	"os"
	"strconv"

	"github.com/DataDog/dd-trace-go/v2/internal/log"
	"github.com/DataDog/dd-trace-go/v2/internal/telemetry"
)

func BoolStableConfig(env string, def bool) (value bool, origin telemetry.Origin, configured bool) {
	if v := FleetConfig.Get(env); v != "" {
		if vv, err := strconv.ParseBool(v); err == nil {
			return vv, telemetry.OriginFleetStableConfig, true
		} else {
			log.Warn("Non-boolean value for %s in fleet-managed configuration file, dropping. Parse failed with error: %v", env, err)
		}
	}
	if v, ok := os.LookupEnv(env); ok {
		if vv, err := strconv.ParseBool(v); err == nil {
			return vv, telemetry.OriginEnvVar, true
		} else {
			log.Warn("Non-boolean value for env var %s, dropping. Parse failed with error: %v", env, err)
		}
	}
	if v := LocalConfig.Get(env); v != "" {
		if vv, err := strconv.ParseBool(v); err == nil {
			return vv, telemetry.OriginLocalStableConfig, true
		} else {
			log.Warn("Non-boolean value for %s in fleet-managed configuration file, dropping. Parse failed with error: %v", env, err)
		}
	}
	return def, telemetry.OriginDefault, false
}

func StringStableConfig(env string, def string) (string, telemetry.Origin) {
	if v := FleetConfig.Get(env); v != "" {
		return v, telemetry.OriginFleetStableConfig
	}
	if v, ok := os.LookupEnv(env); ok {
		return v, telemetry.OriginEnvVar
	}
	if v := LocalConfig.Get(env); v != "" {
		return v, telemetry.OriginLocalStableConfig
	}
	return def, telemetry.OriginDefault
}
