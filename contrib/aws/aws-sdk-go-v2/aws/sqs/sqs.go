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

const (
	datadogKey = "_datadog"
)

type messageCarrier map[string]string

func (carrier messageCarrier) Set(key, val string) {
	carrier[key] = val
}

func EnrichOperation(ctx context.Context, in middleware.InitializeInput, operation string) error {
	switch operation {
	case "SendMessage":
		return handleSendMessage(ctx, in)
	case "SendMessageBatch":
		return handleSendMessageBatch(ctx, in)
	default:
		return fmt.Errorf("unsupported operation: %s", operation)
	}
}

func handleSendMessage(ctx context.Context, in middleware.InitializeInput) error {
	if params, ok := in.Parameters.(*sqs.SendMessageInput); ok {
		if params.MessageAttributes == nil {
			params.MessageAttributes = make(map[string]types.MessageAttributeValue)
		}
		return injectTraceContext(ctx, params.MessageAttributes)
	}
	return fmt.Errorf("unable to inject trace context into SendMessage request")
}

func handleSendMessageBatch(ctx context.Context, in middleware.InitializeInput) error {
	if params, ok := in.Parameters.(*sqs.SendMessageBatchInput); ok {
		for i := range params.Entries {
			if params.Entries[i].MessageAttributes == nil {
				params.Entries[i].MessageAttributes = make(map[string]types.MessageAttributeValue)
			}
			err := injectTraceContext(ctx, params.Entries[i].MessageAttributes)
			if err != nil {
				return fmt.Errorf("unable to inject trace context: %w", err)
			}
		}
	}
	return fmt.Errorf("unable to inject trace context into SendMessageBatch request")
}

func injectTraceContext(ctx context.Context, messageAttributes map[string]types.MessageAttributeValue) error {
	span, _ := tracer.SpanFromContext(ctx)
	if span == nil {
		return nil
	}

	// SQS only allows a maximum of 10 message attributes.
	// https://docs.aws.amazon.com/AWSSimpleQueueService/latest/SQSDeveloperGuide/sqs-message-metadata.html#sqs-message-attributes
	// Only inject if there's room.
	if len(messageAttributes) >= 10 {
		return fmt.Errorf("cannot inject trace context: message already has maximum allowed attributes")
	}

	carrier := make(messageCarrier)
	err := tracer.Inject(span.Context(), carrier)
	if err != nil {
		return err
	}

	jsonBytes, err := json.Marshal(carrier)
	if err != nil {
		return err
	}

	messageAttributes[datadogKey] = types.MessageAttributeValue{
		DataType:    aws.String("String"),
		StringValue: aws.String(string(jsonBytes)),
	}

	return nil
}
