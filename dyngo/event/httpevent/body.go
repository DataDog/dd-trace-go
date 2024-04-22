// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package httpevent

import (
	"context"
	"errors"

	"gopkg.in/DataDog/dd-trace-go.v1/dyngo/internal/opcontext"
	"gopkg.in/DataDog/dd-trace-go.v1/dyngo/internal/operation"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/log"
)

type (
	// SDKBodyOperation represents an SDK Operation's body.
	SDKBodyOperation struct {
		operation.Operation
	}

	// SDKBodyOperationArgs is the SDK body operation arguments.
	SDKBodyOperationArgs struct {
		// Body corresponds to the address `server.request.body`.
		Body interface{}
	}

	// SDKBodyOperationRes is the SDK body operation results.
	SDKBodyOperationRes struct{}
)

// FireSDKBodyOperation starts and finishes an SDK body operation immediately,
// retrieveing any error data emitted by an operation listener.
func FireSDKBodyOperation(parent operation.Operation, args SDKBodyOperationArgs) (err error) {
	op := &SDKBodyOperation{Operation: operation.New(parent)}
	operation.OnData(op, func(dataErr error) {
		err = errors.Join(err, dataErr)
	})
	operation.Start(op, args)
	operation.Finish(op, SDKBodyOperationRes{})
	return
}

// MonitorParsedBody propagates a parsed request body operation to the
// operations stack from the provided context, using the supplied value. If
// operation listeners determine the request should be blocked, an error
// is returned which can be used to determine how the blocking should be done.
func MonitorParsedBody(ctx context.Context, body any) error {
	op := opcontext.Operation(ctx)
	if op == nil {
		log.Error("dyngo: parsed body monitoring ignored, no Operation was found in context. Either the current request is not being monitored, or the provided context is not correct.")
		return nil
	}
	return FireSDKBodyOperation(op, SDKBodyOperationArgs{Body: body})
}

func (SDKBodyOperationArgs) IsArgOf(*SDKBodyOperation)   {}
func (SDKBodyOperationRes) IsResultOf(*SDKBodyOperation) {}
