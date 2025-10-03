// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package main

import (
	"net"
	"strconv"

	"github.com/DataDog/dd-trace-go/v2/instrumentation/env"
)

// IntEnv returns the parsed int value of an environment variable, or
// def otherwise.
func intEnv(key string, def int) int {
	vv, ok := env.Lookup(key)
	if !ok {
		return def
	}
	v, err := strconv.Atoi(vv)
	if err != nil {
		log.Warn("Non-integer value for env var %s, defaulting to %d. Parse failed with error: %v", key, def, err)
		return def
	}
	return v
}

// IpEnv returns the valid IP value of an environment variable, or def otherwise.
func ipEnv(key string, def net.IP) net.IP {
	vv, ok := env.Lookup(key)
	if !ok {
		return def
	}

	ip := net.ParseIP(vv)
	if ip == nil {
		log.Warn("Non-IP value for env var %s, defaulting to %s", key, def.String())
		return def
	}

	return ip
}

// BoolEnv returns the parsed boolean value of an environment variable, or
// def otherwise.
func boolEnv(key string, def bool) bool {
	vv, ok := env.Lookup(key)
	if !ok {
		return def
	}
	v, err := strconv.ParseBool(vv)
	if err != nil {
		log.Warn("Non-boolean value for env var %s, defaulting to %t. Parse failed with error: %v", key, def, err)
		return def
	}
	return v
}
