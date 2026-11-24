// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package sfn

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/DataDog/dd-trace-go/contrib/aws/aws-sdk-go-v2/v2/internal"
	"github.com/aws/aws-sdk-go-v2/service/sfn"
	"github.com/aws/smithy-go/middleware"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
)

var instr = internal.Instr

// enricher adds the Datadog trace context to the input of Step Functions executions.
// The zero value injects the trace context.
type enricher struct {
	noInject bool
}

// NewEnricher returns an enricher that writes the trace context into the request only if inject is true.
// The span tags are recorded either way.
func NewEnricher(inject bool) *enricher {
	return &enricher{noInject: !inject}
}

func (e *enricher) EnrichOperation(_ context.Context, span *tracer.Span, in middleware.InitializeInput, operation string) {
	switch operation {
	case "StartExecution":
		e.handleStartExecution(span, in)
	case "StartSyncExecution":
		e.handleStartSyncExecution(span, in)
	}
}

func (e *enricher) handleStartExecution(span *tracer.Span, in middleware.InitializeInput) {
	params, ok := in.Parameters.(*sfn.StartExecutionInput)
	if !ok {
		instr.Logger().Debug("Unable to read StartExecutionInput params")
		return
	}
	if e.noInject {
		return
	}

	modifiedInput := injectTraceContext(span, params.Input)
	params.Input = modifiedInput
}

func (e *enricher) handleStartSyncExecution(span *tracer.Span, in middleware.InitializeInput) {
	params, ok := in.Parameters.(*sfn.StartSyncExecutionInput)
	if !ok {
		instr.Logger().Debug("Unable to read StartSyncExecutionInput params")
		return
	}
	if e.noInject {
		return
	}

	modifiedInput := injectTraceContext(span, params.Input)
	params.Input = modifiedInput
}

func injectTraceContext(span *tracer.Span, input *string) *string {
	if input == nil || len(*input) == 0 || (*input)[len(*input)-1] != '}' {
		return input
	}
	traceCtxCarrier := tracer.TextMapCarrier{}
	if err := tracer.Inject(span.Context(), traceCtxCarrier); err != nil {
		instr.Logger().Debug("Unable to inject trace context: %s", err.Error())
		return input
	}

	traceCtxJSON, err := json.Marshal(traceCtxCarrier)
	if err != nil {
		instr.Logger().Debug("Unable to marshal trace context: %s", err.Error())
		return input
	}

	modifiedInput := (*input)[:len(*input)-1] // remove closing bracket
	modifiedInput += fmt.Sprintf(",\"_datadog\": %s }", string(traceCtxJSON))
	return &modifiedInput
}
