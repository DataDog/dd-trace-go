// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

// Package perfevent contains dyngo event definitions for the Performance domain.
package perfevent

import (
	"context"

	"github.com/datadog/dd-trace-go/dyngo/internal/opcontext"
	"github.com/datadog/dd-trace-go/dyngo/internal/operation"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace"
)

type (
	// MonitoredOperation represents an operation that is being monitored for
	// performance. This usually translates to a span being addded to the trace.
	MonitoredOperation struct {
		operation.Operation
	}

	// MonitoredOperationArgs represents the arguments for a MonitoredOperation.
	MonitoredOperationArgs struct {
		Context       context.Context
		Options       []ddtrace.StartSpanOption
		OperationName string
	}

	// MonitoredOperationRes represents the results of a MonitoredOperation.
	MonitoredOperationRes struct {
		Options []ddtrace.FinishOption
	}
)

// StartMonitoredOperation creates and start a new performance-monitored
// operation with the provided arguments and context. The newContext callback
// is called with the new context to use for correctly weaving trace data with
// the rest of the control flow.
func StartMonitoredOperation(
	ctx context.Context,
	operationName string,
	options ...ddtrace.StartSpanOption,
) (context.Context, *MonitoredOperation) {
	op := &MonitoredOperation{Operation: operation.New(opcontext.Operation(ctx))}
	operation.OnData(op, func(newCtx context.Context) {
		ctx = newCtx
	})
	operation.Start(op, MonitoredOperationArgs{Context: ctx, OperationName: operationName, Options: options})
	return opcontext.WithOperation(ctx, op), op
}

// Finish finishes the monitored operation with the provided results.
func (o *MonitoredOperation) Finish(options ...ddtrace.FinishOption) {
	operation.Finish(o, MonitoredOperationRes{options})
}

func (MonitoredOperationArgs) IsArgOf(*MonitoredOperation)   {}
func (MonitoredOperationRes) IsResultOf(*MonitoredOperation) {}
