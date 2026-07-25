// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package utils

import (
	"strings"

	"github.com/DataDog/dd-trace-go/v2/internal/env"
)

// ciProviderEnvKey is a non-configuration CI provider metadata key.
type ciProviderEnvKey string

// lookupCIProviderEnvironment is the sole dynamic provider-metadata boundary.
// Datadog and OpenTelemetry configuration namespaces are deliberately rejected.
func lookupCIProviderEnvironment(key ciProviderEnvKey) (string, bool) {
	raw := string(key)
	if strings.HasPrefix(raw, "DD_") || strings.HasPrefix(raw, "DD-") || strings.HasPrefix(raw, "OTEL_") {
		return "", false
	}
	return env.Lookup(raw)
}

func getCIProviderEnvironment(key ciProviderEnvKey) string {
	value, _ := lookupCIProviderEnvironment(key)
	return value
}
