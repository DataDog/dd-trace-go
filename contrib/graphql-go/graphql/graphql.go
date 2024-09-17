// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package graphql // import "gopkg.in/DataDog/dd-trace-go.v1/contrib/graph-go/graphql"

import (
	v2 "github.com/DataDog/dd-trace-go/contrib/graphql-go/graphql/v2"

	"github.com/graphql-go/graphql"
)

const (
	spanServer              = "graphql.server"
	spanParse               = "graphql.parse"
	spanValidate            = "graphql.validate"
	spanExecute             = "graphql.execute"
	spanResolve             = "graphql.resolve"
	tagGraphqlField         = "graphql.field"
	tagGraphqlOperationName = "graphql.operation.name"
	tagGraphqlOperationType = "graphql.operation.type"
	tagGraphqlSource        = "graphql.source"
	tagGraphqlVariables     = "graphql.variables"
)

func NewSchema(config graphql.SchemaConfig, options ...Option) (graphql.Schema, error) {
	return v2.NewSchema(config, options...)
}
