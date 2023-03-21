// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2023 Datadog, Inc.

package aws

import (
	"fmt"

	"gopkg.in/DataDog/dd-trace-go.v1/internal/namingschema"

	"github.com/aws/aws-sdk-go/aws/request"
)

func newServiceNameSchema(userOverride string, req *request.Request) namingschema.Schema {
	serviceNameV0 := namingschema.VersionOverrideFunc(func() string {
		if userOverride != "" {
			return userOverride
		}
		return defaultServiceName(req)
	})

	return namingschema.NewServiceNameSchema(
		userOverride,
		defaultServiceName(req),
		namingschema.WithVersionOverride(namingschema.SchemaV0, serviceNameV0),
	)
}

func defaultServiceName(req *request.Request) string {
	return fmt.Sprintf("aws.%s", awsService(req))
}

type outboundOperationNameSchema struct {
	req *request.Request
}

func newOutboundOperationNameSchema(req *request.Request) namingschema.Schema {
	return namingschema.New(&outboundOperationNameSchema{req: req})
}

func (o *outboundOperationNameSchema) V0() string {
	return fmt.Sprintf("%s.command", awsService(o.req))
}

func (o *outboundOperationNameSchema) V1() string {
	return fmt.Sprintf("aws.%s.request", awsService(o.req))
}
