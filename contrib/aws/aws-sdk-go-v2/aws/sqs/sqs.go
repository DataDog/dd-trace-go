package sqs

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	"github.com/aws/aws-sdk-go-v2/service/sqs/types"
	"github.com/aws/smithy-go/middleware"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
)

const datadogKey = "_datadog"

type messageCarrier map[string]string

func (carrier messageCarrier) Set(key, val string) {
	carrier[key] = val
}

func EnrichOperation(ctx context.Context, in middleware.InitializeInput, operation string) {
	println("[nhulston tracer] EnrichOperation()")
	switch operation {
	case "SendMessage":
		handleSendMessage(ctx, in)
	case "SendMessageBatch":
		handleSendMessageBatch(ctx, in)
	}
}

func handleSendMessage(ctx context.Context, in middleware.InitializeInput) {
	println("[nhulston tracer] handleSendMessage()")
	params, ok := in.Parameters.(*sqs.SendMessageInput)
	if !ok {
		fmt.Println("Unable to read SendMessage params")
		return
	}

	if params.MessageAttributes == nil {
		println("[nhulston tracer] attributes nil")
	} else {
		println("[nhulston tracer] attributes not nil")
	}
	injectTraceContext(ctx, params.MessageAttributes)
	println("[nhulston tracer] done with injectTraceContext()")
}

func handleSendMessageBatch(ctx context.Context, in middleware.InitializeInput) {
	println("[nhulston tracer] handleSendMessageBatch()")
	params, ok := in.Parameters.(*sqs.SendMessageBatchInput)
	if !ok {
		fmt.Println("Unable to read SendMessageBatch params")
		return
	}

	for i := range params.Entries {
		injectTraceContext(ctx, params.Entries[i].MessageAttributes)
	}
}

func injectTraceContext(ctx context.Context, messageAttributes map[string]types.MessageAttributeValue) {
	println("[nhulston tracer] injectTraceContext()")
	span, _ := tracer.SpanFromContext(ctx)
	if span == nil {
		fmt.Println("Unable to find span from context")
		return
	}

	if messageAttributes == nil {
		messageAttributes = make(map[string]types.MessageAttributeValue)
	}

	// SQS only allows a maximum of 10 message attributes.
	// https://docs.aws.amazon.com/AWSSimpleQueueService/latest/SQSDeveloperGuide/sqs-message-metadata.html#sqs-message-attributes
	// Only inject if there's room.
	if len(messageAttributes) >= 10 {
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

	messageAttributes[datadogKey] = types.MessageAttributeValue{
		DataType:    aws.String("String"),
		StringValue: aws.String(string(jsonBytes)),
	}
}
