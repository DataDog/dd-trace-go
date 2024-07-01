// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

// Package graphql is the GraphQL instrumentation API and contract for AppSec
// defining an abstract run-time representation of AppSec middleware. GraphQL
// integrations must use this package to enable AppSec features for GraphQL,
// which listens to this package's operation events.
package graphqlsec

import (
	"context"

	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/dyngo"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/emitter/graphqlsec/types"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/trace"
)

// StartRequestOperation starts a new GraphQL request operation, along with the given arguments, and
// emits a start event up in the operation stack. The operation is usually linked to tge global root
// operation. The operation is tracked on the returned context, and can be extracted later on using
// FromContext.
func StartRequestOperation(ctx context.Context, span trace.TagSetter, args types.RequestOperationArgs) (context.Context, *types.RequestOperation) {
	if span == nil {
		// The span may be nil (e.g: in case of GraphQL subscriptions with certian contribs)
		span = trace.NoopTagSetter{}
	}

	op := &types.RequestOperation{
		Operation: dyngo.NewOperation(nil),
		TagSetter: span,
	}

	return dyngo.StartAndRegisterOperation(ctx, op, args), op
}
