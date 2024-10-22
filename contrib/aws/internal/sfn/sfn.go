package sfn

import (
	"encoding/json"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/service/sfn"
	"github.com/aws/smithy-go/middleware"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/log"
)

func EnrichOperation(span tracer.Span, in middleware.InitializeInput, operation string) {
	switch operation {
	case "StartExecution":
		handleStartExecution(span, in)
	case "StartSyncExecution":
		handleStartSyncExecution(span, in)
	}
}

func handleStartExecution(span tracer.Span, in middleware.InitializeInput) {
	params, ok := in.Parameters.(*sfn.StartExecutionInput)
	if !ok {
		log.Debug("Unable to read StartExecutionInput params")
		return
	}

	injectTraceContext(span, params.Input)
}

func handleStartSyncExecution(span tracer.Span, in middleware.InitializeInput) {
	params, ok := in.Parameters.(*sfn.StartSyncExecutionInput)
	if !ok {
		log.Debug("Unable to read StartSyncExecutionInput params")
		return
	}

	injectTraceContext(span, params.Input)
}

func injectTraceContext(span tracer.Span, input *string) {
	if input == nil || len(*input) == 0 || (*input)[len(*input)-1] != '}' {
		return
	}
	traceCtxCarrier := tracer.TextMapCarrier{}
	if err := tracer.Inject(span.Context(), traceCtxCarrier); err != nil {
		log.Debug("Unable to inject trace context: %s", err)
		return
	}

	traceCtxJSON, err := json.Marshal(traceCtxCarrier)
	if err != nil {
		log.Debug("Unable to marshal trace context: %s", err)
		return
	}

	modifiedInput := (*input)[:len(*input)-1] // remove closing bracket
	input = &modifiedInput
	modifiedInput += fmt.Sprintf(",\"_datadog\": %s }", string(traceCtxJSON))
	input = &modifiedInput
	fmt.Printf("==================== input: \n%s\n", *input)
}
