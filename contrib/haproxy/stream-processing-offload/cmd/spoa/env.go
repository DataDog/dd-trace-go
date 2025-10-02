// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package main

import (
	"net"
	"os"
	"strconv"
)

// IntEnv returns the parsed int value of an environment variable, or
// def otherwise.
func intEnv(key string, def int) int {
	vv, ok := os.LookupEnv(key)
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
	vv, ok := os.LookupEnv(key)
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
