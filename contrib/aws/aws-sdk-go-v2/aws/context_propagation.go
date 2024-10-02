package aws

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/aws/aws-sdk-go-v2/aws"
	snstypes "github.com/aws/aws-sdk-go-v2/service/sns/types"
	sqstypes "github.com/aws/aws-sdk-go-v2/service/sqs/types"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
)

const (
	datadogKey = "_datadog"
)

type messageCarrier map[string]string

func (carrier messageCarrier) Set(key, val string) {
	carrier[key] = val
}

func injectTraceContext(ctx context.Context, messageAttributes interface{}) error {
	span, _ := tracer.SpanFromContext(ctx)
	if span == nil {
		return nil
	}

	var attrCount int
	switch attrs := messageAttributes.(type) {
	case map[string]sqstypes.MessageAttributeValue:
		attrCount = len(attrs)
	case map[string]snstypes.MessageAttributeValue:
		attrCount = len(attrs)
	default:
		return fmt.Errorf("unsupported message attributes type")
	}

	// SQS and SNS only allow a maximum of 10 message attributes.
	// https://docs.aws.amazon.com/AWSSimpleQueueService/latest/SQSDeveloperGuide/sqs-message-metadata.html#sqs-message-attributes
	// https://docs.aws.amazon.com/sns/latest/dg/sns-message-attributes.html
	// Only inject if there's room,
	if attrCount >= 10 {
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

	switch attrs := messageAttributes.(type) {
	case map[string]sqstypes.MessageAttributeValue:
		attrs[datadogKey] = sqstypes.MessageAttributeValue{
			DataType:    aws.String("String"),
			StringValue: aws.String(string(jsonBytes)),
		}
	case map[string]snstypes.MessageAttributeValue:
		attrs[datadogKey] = snstypes.MessageAttributeValue{
			DataType:    aws.String("String"),
			StringValue: aws.String(string(jsonBytes)),
		}
	}

	return nil
}

func injectTraceContextBatch(ctx context.Context, entries []snstypes.PublishBatchRequestEntry) error {
	for i := range entries {
		if entries[i].MessageAttributes == nil {
			entries[i].MessageAttributes = make(map[string]snstypes.MessageAttributeValue)
		}
		err := injectTraceContext(ctx, entries[i].MessageAttributes)
		if err != nil {
			return err
		}
	}
	return nil
}
