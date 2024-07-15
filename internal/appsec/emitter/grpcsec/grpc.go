// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

// Package grpcsec is the gRPC instrumentation API and contract for AppSec
// defining an abstract run-time representation of gRPC handlers.
// gRPC integrations must use this package to enable AppSec features for gRPC,
// which listens to this package's operation events.
package grpcsec

import (
	"context"

	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/dyngo"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/emitter/grpcsec/types"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/trace"
)

// StartHandlerOperation starts an gRPC server handler operation, along with the
// given arguments and parent operation, and emits a start event up in the
// operation stack. When parent is nil, the operation is linked to the global
// root operation.
func StartHandlerOperation(ctx context.Context, args types.HandlerOperationArgs, parent dyngo.Operation, setup ...func(*types.HandlerOperation)) (context.Context, *types.HandlerOperation) {
	op := &types.HandlerOperation{
		Operation:  dyngo.NewOperation(parent),
		TagsHolder: trace.NewTagsHolder(),
	}
	for _, cb := range setup {
		cb(op)
	}
	return dyngo.StartAndRegisterOperation(ctx, op, args), op
}

// StartReceiveOperation starts a receive operation of a gRPC handler, along
// with the given arguments and parent operation, and emits a start event up in
// the operation stack. When parent is nil, the operation is linked to the
// global root operation.
func StartReceiveOperation(args types.ReceiveOperationArgs, parent dyngo.Operation) types.ReceiveOperation {
	op := types.ReceiveOperation{Operation: dyngo.NewOperation(parent)}
	dyngo.StartOperation(op, args)
	return op
}
