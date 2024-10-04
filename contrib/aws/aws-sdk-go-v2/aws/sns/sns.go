package sns

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/sns"
	"github.com/aws/aws-sdk-go-v2/service/sns/types"
	"github.com/aws/smithy-go/middleware"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
)

const (
	datadogKey           = "_datadog"
	maxMessageAttributes = 10
)

type messageCarrier map[string]string

func (carrier messageCarrier) Set(key, val string) {
	carrier[key] = val
}

func EnrichOperation(ctx context.Context, in middleware.InitializeInput, operation string) {
	switch operation {
	case "Publish":
		handlePublish(ctx, in)
	case "PublishBatch":
		handlePublishBatch(ctx, in)
	}
}

func handlePublish(ctx context.Context, in middleware.InitializeInput) {
	params, ok := in.Parameters.(*sns.PublishInput)
	if !ok {
		fmt.Println("Unable to read PublishInput params")
		return
	}

	injectTraceContext(ctx, &params.MessageAttributes)
}

func handlePublishBatch(ctx context.Context, in middleware.InitializeInput) {
	params, ok := in.Parameters.(*sns.PublishBatchInput)
	if !ok {
		fmt.Println("Unable to read PublishBatch params")
		return
	}

	for i := range params.PublishBatchRequestEntries {
		injectTraceContext(ctx, &params.PublishBatchRequestEntries[i].MessageAttributes)
	}
}

func injectTraceContext(ctx context.Context, ptrMessageAttributes *map[string]types.MessageAttributeValue) {
	span, _ := tracer.SpanFromContext(ctx)
	if span == nil {
		fmt.Println("Unable to find span from context")
		return
	}

	if *ptrMessageAttributes == nil {
		*ptrMessageAttributes = make(map[string]types.MessageAttributeValue)
	}

	// SNS only allow a maximum of 10 message attributes.
	// https://docs.aws.amazon.com/sns/latest/dg/sns-message-attributes.html
	// Only inject if there's room.
	if len(*ptrMessageAttributes) >= maxMessageAttributes {
		fmt.Println("Cannot inject trace context: message already has maximum allowed attributes")
		return
	}

	carrier := make(messageCarrier)
	err := tracer.Inject(span.Context(), carrier)
	if err != nil {
		fmt.Printf("Unable to inject trace context: %s\n", err.Error())
		return
	}

	jsonBytes, err := json.Marshal(carrier)
	if err != nil {
		fmt.Printf("Unable to marshal trace context: %s\n", err.Error())
		return
	}

	(*ptrMessageAttributes)[datadogKey] = types.MessageAttributeValue{
		DataType:    aws.String("String"),
		StringValue: aws.String(string(jsonBytes)),
	}
}
