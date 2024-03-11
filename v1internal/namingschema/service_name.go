// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2023 Datadog, Inc.

package namingschema

import (
	"github.com/DataDog/dd-trace-go/v2/internal/globalconfig"
	"github.com/DataDog/dd-trace-go/v2/internal/namingschema"
)

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
