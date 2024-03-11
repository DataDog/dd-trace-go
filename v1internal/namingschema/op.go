// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2023 Datadog, Inc.

// Note that this package is for dd-trace-go.v1 internal testing utilities only.
// This package is not intended for use by external consumers, no API stability is guaranteed.
package namingschema

import (
	"github.com/DataDog/dd-trace-go/v2/internal/namingschema"
)

type IntegrationType = namingschema.IntegrationType

// OpName returns the operation name for the given integration type.
// Note that this function is for dd-trace-go.v1 internal testing utilities only.
// This function is not intended for use by external consumers, no API stability is guaranteed.
func OpName(t namingschema.IntegrationType) string {
	return namingschema.OpName(t)
}

// OpNameOverrideV0 returns the operation name for the given integration type with a v0 override.
// Note that this function is for dd-trace-go.v1 internal testing utilities only.
// This function is not intended for use by external consumers, no API stability is guaranteed.
func OpNameOverrideV0(t namingschema.IntegrationType, overrideV0 string) string {
	return namingschema.OpNameOverrideV0(t, overrideV0)
}

// DBOpName returns the operation name for the given database system.
// Note that this function is for dd-trace-go.v1 internal testing utilities only.
// This function is not intended for use by external consumers, no API stability is guaranteed.
func DBOpName(system string, overrideV0 string) string {
	return namingschema.DBOpName(system, overrideV0)
}

// AWSOpName returns the operation name for the given AWS service and operation.
// Note that this function is for dd-trace-go.v1 internal testing utilities only.
// This function is not intended for use by external consumers, no API stability is guaranteed.
func AWSOpName(awsService, awsOp, overrideV0 string) string {
	return namingschema.AWSOpName(awsService, awsOp, overrideV0)
}
