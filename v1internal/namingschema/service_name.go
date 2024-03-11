// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2023 Datadog, Inc.

// Note that this package is for dd-trace-go.v1 internal testing utilities only.
// This package is not intended for use by external consumers, no API stability is guaranteed.
package namingschema

import (
	"github.com/DataDog/dd-trace-go/v2/internal/globalconfig"
	"github.com/DataDog/dd-trace-go/v2/internal/namingschema"
)

// ServiceName returns the service name, falling back to the provided value if not set.
// Note that this function is for dd-trace-go.v1 internal testing utilities only.
// This function is not intended for use by external consumers, no API stability is guaranteed.
func ServiceName(fallback string) string {
	switch namingschema.GetVersion() {
	case namingschema.SchemaV1:
		if svc := globalconfig.ServiceName(); svc != "" {
			return svc
		}
		return fallback
	default:
		if svc := globalconfig.ServiceName(); svc != "" {
			return svc
		}
		return fallback
	}
}

// ServiceNameOverrideV0 returns the service name with a v0 override.
// Note that this function is for dd-trace-go.v1 internal testing utilities only.
// This function is not intended for use by external consumers, no API stability is guaranteed.
func ServiceNameOverrideV0(fallback, overrideV0 string) string {
	switch namingschema.GetVersion() {
	case namingschema.SchemaV1:
		if svc := globalconfig.ServiceName(); svc != "" {
			return svc
		}
		return fallback
	default:
		if namingschema.UseGlobalServiceName() {
			if svc := globalconfig.ServiceName(); svc != "" {
				return svc
			}
		}
		return overrideV0
	}
}
