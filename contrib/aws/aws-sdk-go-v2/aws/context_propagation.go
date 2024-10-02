package aws

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/eventbridge/types"
	snstypes "github.com/aws/aws-sdk-go-v2/service/sns/types"
	sqstypes "github.com/aws/aws-sdk-go-v2/service/sqs/types"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
	"time"
)

const (
	datadogKey      = "_datadog"
	startTimeKey    = "x-datadog-start-time"
	resourceNameKey = "x-datadog-resource-name"
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

func injectTraceContextEventBridge(ctx context.Context, entry *types.PutEventsRequestEntry) error {
	span, _ := tracer.SpanFromContext(ctx)
	if span == nil {
		return nil
	}

	carrier := make(messageCarrier)
	err := tracer.Inject(span.Context(), carrier)
	if err != nil {
		return err
	}

	// Add start time and resource name
	carrier[startTimeKey] = fmt.Sprintf("%d", time.Now().UnixNano()/int64(time.Millisecond))
	if entry.EventBusName != nil {
		carrier[resourceNameKey] = *entry.EventBusName
	}

	jsonBytes, err := json.Marshal(carrier)
	if err != nil {
		return err
	}

	var detail map[string]interface{}
	if entry.Detail != nil {
		err = json.Unmarshal([]byte(*entry.Detail), &detail)
		if err != nil {
			return err
		}
	} else {
		detail = make(map[string]interface{})
	}

	detail[datadogKey] = json.RawMessage(jsonBytes)

	updatedDetail, err := json.Marshal(detail)
	if err != nil {
		return err
	}

	entry.Detail = aws.String(string(updatedDetail))
	return nil
}
