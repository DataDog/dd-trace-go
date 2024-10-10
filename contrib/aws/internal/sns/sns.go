// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package sns

import (
	"context"
	"encoding/json"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/sns"
	"github.com/aws/aws-sdk-go-v2/service/sns/types"
	"github.com/aws/smithy-go/middleware"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/log"
)

const (
	datadogKey           = "_datadog"
	maxMessageAttributes = 10
)

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
		log.Debug("Unable to read PublishInput params")
		return
	}

	if params.MessageAttributes == nil {
		params.MessageAttributes = make(map[string]types.MessageAttributeValue)
	}

	injectTraceContext(ctx, params.MessageAttributes)
}

func handlePublishBatch(ctx context.Context, in middleware.InitializeInput) {
	params, ok := in.Parameters.(*sns.PublishBatchInput)
	if !ok {
		log.Debug("Unable to read PublishBatch params")
		return
	}

	for i := range params.PublishBatchRequestEntries {
		if params.PublishBatchRequestEntries[i].MessageAttributes == nil {
			params.PublishBatchRequestEntries[i].MessageAttributes = make(map[string]types.MessageAttributeValue)
		}
		injectTraceContext(ctx, params.PublishBatchRequestEntries[i].MessageAttributes)
	}
}

func injectTraceContext(ctx context.Context, messageAttributes map[string]types.MessageAttributeValue) {
	span, ok := tracer.SpanFromContext(ctx)
	if !ok || span == nil {
		log.Debug("Unable to find span from context")
		return
	}

	// SNS only allows a maximum of 10 message attributes.
	// https://docs.aws.amazon.com/sns/latest/dg/sns-message-attributes.html
	// Only inject if there's room.
	if len(messageAttributes) >= maxMessageAttributes {
		log.Info("Cannot inject trace context: message already has maximum allowed attributes")
		return
	}

	carrier := tracer.TextMapCarrier{}
	err := tracer.Inject(span.Context(), carrier)
	if err != nil {
		log.Debug("Unable to inject trace context: %s", err.Error())
		return
	}

	jsonBytes, err := json.Marshal(carrier)
	if err != nil {
		log.Debug("Unable to marshal trace context: %s", err.Error())
		return
	}

	// Use Binary since SNS subscription filter policies fail silently with JSON
	// strings. https://github.com/DataDog/datadog-lambda-js/pull/269
	messageAttributes[datadogKey] = types.MessageAttributeValue{
		DataType:    aws.String("Binary"),
		BinaryValue: jsonBytes,
	}
}
