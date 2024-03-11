// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2023 Datadog, Inc.

// Package namingschema allows to use the naming schema from the integrations to set different
// service and span/operation names based on the value of the DD_TRACE_SPAN_ATTRIBUTE_SCHEMA environment variable.
package namingschema

import (
	"github.com/DataDog/dd-trace-go/v2/v1internal/namingschema"
)

// Version represents the available naming schema versions.
type Version = namingschema.Version

const (
	// SchemaV0 represents naming schema v0.
	SchemaV0 Version = namingschema.SchemaV0
	// SchemaV1 represents naming schema v1.
	SchemaV1 = namingschema.SchemaV1
)

// GetVersion returns the global naming schema version used for this application.
func GetVersion() Version {
	return namingschema.GetVersion()
}

// SetVersion sets the global naming schema version used for this application.
func SetVersion(v Version) {
	namingschema.SetVersion(v)
}

// UseGlobalServiceName returns the value of the useGlobalServiceName setting for this application.
func UseGlobalServiceName() bool {
	return namingschema.UseGlobalServiceName()
}

// SetUseGlobalServiceName sets the value of the useGlobalServiceName setting used for this application.
func SetUseGlobalServiceName(v bool) {
	namingschema.SetUseGlobalServiceName(v)
}
