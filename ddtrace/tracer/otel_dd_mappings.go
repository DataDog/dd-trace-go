// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package tracer

import internalconfig "github.com/DataDog/dd-trace-go/v2/internal/config"

// getDDorOtelConfig returns the provider-resolved Datadog value for the named
// OpenTelemetry compatibility mapping.
func getDDorOtelConfig(configName string) string {
	return internalconfig.TracerOTelDDValue(configName)
}
