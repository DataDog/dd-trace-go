// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2023 Datadog, Inc.

package aws

import (
	"fmt"

	"gopkg.in/DataDog/dd-trace-go.v1/internal/namingschema"
)

func newServiceNameSchema(userOverride, awsService string) namingschema.Schema {
	serviceNameV0 := namingschema.VersionOverrideFunc(func() string {
		if userOverride != "" {
			return userOverride
		}
		return defaultServiceName(awsService)
	})

	return namingschema.NewServiceNameSchema(
		userOverride,
		defaultServiceName(awsService),
		namingschema.WithVersionOverride(namingschema.SchemaV0, serviceNameV0),
	)
}

func defaultServiceName(awsService string) string {
	return fmt.Sprintf("aws.%s", awsService)
}

type outboundOperationNameSchema struct {
	awsService string
}

func newOutboundOperationNameSchema(awsService string) namingschema.Schema {
	return namingschema.New(&outboundOperationNameSchema{awsService: awsService})
}

func (o *outboundOperationNameSchema) V0() string {
	return fmt.Sprintf("%s.request", o.awsService)
}

func (o *outboundOperationNameSchema) V1() string {
	return fmt.Sprintf("aws.%s.request", o.awsService)
}
