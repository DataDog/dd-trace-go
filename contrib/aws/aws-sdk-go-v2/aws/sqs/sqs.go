package sqs

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	"github.com/aws/aws-sdk-go-v2/service/sqs/types"
	"github.com/aws/smithy-go/middleware"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
	"strings"
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
	case "SendMessage":
		handleSendMessage(ctx, in)
	case "SendMessageBatch":
		handleSendMessageBatch(ctx, in)
	}
}

func handleSendMessage(ctx context.Context, in middleware.InitializeInput) {
	params, ok := in.Parameters.(*sqs.SendMessageInput)
	if !ok {
		fmt.Println("Unable to read SendMessage params")
		return
	}

	setQueueTags(ctx, params.QueueUrl)

	if params.MessageAttributes == nil {
		params.MessageAttributes = make(map[string]types.MessageAttributeValue)
	}

	injectTraceContext(ctx, params.MessageAttributes)
}

func handleSendMessageBatch(ctx context.Context, in middleware.InitializeInput) {
	params, ok := in.Parameters.(*sqs.SendMessageBatchInput)
	if !ok {
		fmt.Println("Unable to read SendMessageBatch params")
		return
	}

	setQueueTags(ctx, params.QueueUrl)

	for _, entry := range params.Entries {
		if entry.MessageAttributes == nil {
			entry.MessageAttributes = make(map[string]types.MessageAttributeValue)
		}
		injectTraceContext(ctx, entry.MessageAttributes)
	}
}

func setQueueTags(ctx context.Context, queueUrlPtr *string) {
	if queueUrlPtr == nil {
		return
	}

	queueUrl := *queueUrlPtr
	span, _ := tracer.SpanFromContext(ctx)

	if span != nil && queueUrl != "" {
		lastSeparationIndex := strings.LastIndex(queueUrl, "/") + 1
		queueName := queueUrl[lastSeparationIndex:]

		if queueName != "" {
			span.SetTag(ext.QueueName, queueName)
			span.SetTag(ext.QueueUrl, queueUrl)
		}
	}
}

func injectTraceContext(ctx context.Context, messageAttributes map[string]types.MessageAttributeValue) {
	span, _ := tracer.SpanFromContext(ctx)
	if span == nil {
		fmt.Println("Unable to find span from context")
		return
	}

	// SQS only allows a maximum of 10 message attributes.
	// https://docs.aws.amazon.com/AWSSimpleQueueService/latest/SQSDeveloperGuide/sqs-message-metadata.html#sqs-message-attributes
	// Only inject if there's room.
	if len(messageAttributes) >= maxMessageAttributes {
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
