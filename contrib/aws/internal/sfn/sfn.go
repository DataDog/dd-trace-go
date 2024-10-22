package sfn

import (
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
	carrier := tracer.TextMapCarrier{}
	tracer.Inject(span.Context(), carrier)
	fmt.Printf("============== test carrier: %+v\n", carrier)

	traceId := span.Context().TraceID()
	parentId := span.Context().SpanID()
	traceContext := fmt.Sprintf("{\"x-datadog-trace-id\":\"%d\",\"x-datadog-parent-id\":\"%d\"}", traceId, parentId)
	fmt.Printf("============= custom traceContext: %+v\n", traceContext)

	modifiedInput := (*input)[:len(*input)-1] // remove closing bracket
	input = &modifiedInput
	modifiedInput += fmt.Sprintf(",\"_datadog\": %s }", traceContext)
	input = &modifiedInput
}
