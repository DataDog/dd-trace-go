// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package fixturea

// Simulate the env-reading helpers in the real repo without depending on it.

func envGet(key string) string          { return "" }
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
