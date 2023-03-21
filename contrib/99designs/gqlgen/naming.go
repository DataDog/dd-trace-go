// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2023 Datadog, Inc.

package gqlgen

import (
	"fmt"

	"gopkg.in/DataDog/dd-trace-go.v1/internal/namingschema"
)

type inboundOperationNameSchema struct {
	graphqlOp string
}

func newInboundOperationNameSchema(graphqlOp string) namingschema.Schema {
	return namingschema.New(&inboundOperationNameSchema{graphqlOp: graphqlOp})
}

func (i *inboundOperationNameSchema) V0() string {
	return fmt.Sprintf("graphql.%s", i.graphqlOp)
}

func (i *inboundOperationNameSchema) V1() string {
	return fmt.Sprintf("graphql.server.%s", i.graphqlOp)
}
