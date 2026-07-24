// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package fixturea

// Simulate the env-reading helpers in the real repo without depending on it.

import "os"

func envGet(key string) string          { return os.Getenv(key) }
func boolEnv(key string, def bool) bool { return def }
func intEnv(key string, def int) int    { return def }

const ddSiteKey = "DD_SITE"

func ReadAll() {
	_ = envGet("DD_HOSTNAME")
	_ = envGet(ddSiteKey)
	_ = boolEnv("DD_PROFILING_ENABLED", false)
	_ = intEnv("DD_TRACE_AGENT_PORT", 8126)
	_ = envGet("DD_ENV") //nolint:configaudit — intentional direct read, not a migration candidate
}

var readEnv = os.Getenv

func exerciseDynamic(prefix string) {
	_ = os.Getenv("DD_DIRECT_OS")
	_ = readEnv("DD_ALIASED_OS")
	_ = envGet("DD_TRACE_" + prefix)
	_ = envGet("DD_SUPPRESSED") //nolint:configaudit
}

func wrappedEnv(key string) string { return os.Getenv(key) }

func exerciseWrapper() {
	_ = wrappedEnv("DD_WRAPPED")
}
