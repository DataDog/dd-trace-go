// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package sqs

import (
	"context"
	"encoding/json"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	"github.com/aws/aws-sdk-go-v2/service/sqs/types"
	"github.com/aws/smithy-go/middleware"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/log"
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
		log.Debug("Unable to read SendMessage params")
		return
	}

	if params.MessageAttributes == nil {
		params.MessageAttributes = make(map[string]types.MessageAttributeValue)
	}

	injectTraceContext(ctx, params.MessageAttributes)
}

func handleSendMessageBatch(ctx context.Context, in middleware.InitializeInput) {
	params, ok := in.Parameters.(*sqs.SendMessageBatchInput)
	if !ok {
		log.Debug("Unable to read SendMessageBatch params")
		return
	}

	for i := range params.Entries {
		entryPtr := &params.Entries[i]
		if entryPtr.MessageAttributes == nil {
			entryPtr.MessageAttributes = make(map[string]types.MessageAttributeValue)
		}
		injectTraceContext(ctx, entryPtr.MessageAttributes)
	}
}

func injectTraceContext(ctx context.Context, messageAttributes map[string]types.MessageAttributeValue) {
	span, ok := tracer.SpanFromContext(ctx)
	if !ok || span == nil {
		log.Debug("Unable to find span from context")
		return
	}

	// SQS only allows a maximum of 10 message attributes.
	// https://docs.aws.amazon.com/AWSSimpleQueueService/latest/SQSDeveloperGuide/sqs-message-metadata.html#sqs-message-attributes
	// Only inject if there's room.
	if len(messageAttributes) >= maxMessageAttributes {
		log.Info("Cannot inject trace context: message already has maximum allowed attributes")
		return
	}

	carrier := make(messageCarrier)
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

	messageAttributes[datadogKey] = types.MessageAttributeValue{
		DataType:    aws.String("String"),
		StringValue: aws.String(string(jsonBytes)),
	}
}
