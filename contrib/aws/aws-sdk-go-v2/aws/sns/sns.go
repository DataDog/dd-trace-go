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
	datadogKey = "_datadog"
)

type messageCarrier map[string]string

func (carrier messageCarrier) Set(key, val string) {
	carrier[key] = val
}

func EnrichOperation(ctx context.Context, in middleware.InitializeInput, operation string) error {
	switch operation {
	case "Publish":
		return handlePublish(ctx, in)
	case "PublishBatch":
		return handlePublishBatch(ctx, in)
	default:
		return nil
	}
}

func handlePublish(ctx context.Context, in middleware.InitializeInput) error {
	if params, ok := in.Parameters.(*sns.PublishInput); ok {
		if params.MessageAttributes == nil {
			params.MessageAttributes = make(map[string]types.MessageAttributeValue)
		}
		return injectTraceContext(ctx, params.MessageAttributes)
	}
	return fmt.Errorf("unable to inject trace context into Publish request")
}

func handlePublishBatch(ctx context.Context, in middleware.InitializeInput) error {
	if params, ok := in.Parameters.(*sns.PublishBatchInput); ok {
		return injectTraceContextBatch(ctx, params.PublishBatchRequestEntries)
	}
	return fmt.Errorf("unable to inject trace context into PublishBatch request")
}

func injectTraceContext(ctx context.Context, messageAttributes map[string]types.MessageAttributeValue) error {
	span, _ := tracer.SpanFromContext(ctx)
	if span == nil {
		return nil
	}

	// SNS only allow a maximum of 10 message attributes.
	// https://docs.aws.amazon.com/sns/latest/dg/sns-message-attributes.html
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

func injectTraceContextBatch(ctx context.Context, entries []types.PublishBatchRequestEntry) error {
	for i := range entries {
		if entries[i].MessageAttributes == nil {
			entries[i].MessageAttributes = make(map[string]types.MessageAttributeValue)
		}
		err := injectTraceContext(ctx, entries[i].MessageAttributes)
		if err != nil {
			return fmt.Errorf("unable to inject trace context for entry %d: %w", i, err)
		}
	}
	return nil
}
