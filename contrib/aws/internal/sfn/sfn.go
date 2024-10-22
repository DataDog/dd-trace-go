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

	if params.Input != nil && len(*params.Input) > 0 && (*params.Input)[len(*params.Input)-1] == '}' {
		traceId := span.Context().TraceID()
		parentId := span.Context().SpanID()
		traceContext := fmt.Sprintf("{\"x-datadog-trace-id\":\"%d\",\"x-datadog-parent-id\":\"%d\"}", traceId, parentId)

		modifiedInput := (*params.Input)[:len(*params.Input)-1] // remove closing bracket
		params.Input = &modifiedInput
		modifiedInput += fmt.Sprintf(",\"_datadog\":{ %s }", traceContext)
		params.Input = &modifiedInput
	}
}

func handleStartSyncExecution(span tracer.Span, in middleware.InitializeInput) {
	fmt.Println("================= handling start sync execution")
	params, ok := in.Parameters.(*sfn.StartSyncExecutionInput)
	if !ok {
		log.Debug("Unable to read StartSyncExecutionInput params")
		return
	}

	if params.Input == nil {
		fmt.Printf("================= params.Input is nil: %+v\n", params)
		return
	}
	executionInput := *params.Input

	if len(executionInput) > 0 && executionInput[len(executionInput)-1] == '}' {
		fmt.Println("================= injecting trace context")
		traceId := span.Context().TraceID()
		parentId := span.Context().SpanID()
		traceContext := fmt.Sprintf("{\"x-datadog-trace-id\":\"%d\",\"x-datadog-parent-id\":\"%d\"}", traceId, parentId)

		modifiedInput := (*params.Input)[:len(*params.Input)-1] // remove closing bracket
		params.Input = &modifiedInput
		modifiedInput += fmt.Sprintf(",\"_datadog\":{ %s }", traceContext)
		params.Input = &modifiedInput

		fmt.Printf("================= executionInput: \n%s\n", executionInput)
	}
}
