// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package sqs

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/DataDog/dd-trace-go/contrib/aws/aws-sdk-go-v2/v2/internal"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	"github.com/aws/aws-sdk-go-v2/service/sqs/types"
	"github.com/aws/smithy-go/middleware"

	"github.com/DataDog/dd-trace-go/v2/datastreams"
	"github.com/DataDog/dd-trace-go/v2/datastreams/options"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
)

const (
	datadogKey           = "_datadog"
	maxMessageAttributes = 10
)

var instr = internal.Instr

func EnrichOperation(ctx context.Context, span *tracer.Span, in middleware.InitializeInput, operation string) {
	switch operation {
	case "SendMessage":
		handleSendMessage(ctx, span, in)
	case "SendMessageBatch":
		handleSendMessageBatch(ctx, span, in)
	}
	span.SetTag(ext.MessagingSystem, ext.MessagingSystemSQS)
}

func handleSendMessage(ctx context.Context, span *tracer.Span, in middleware.InitializeInput) {
	params, ok := in.Parameters.(*sqs.SendMessageInput)
	if !ok {
		instr.Logger().Debug("Unable to read SendMessage params")
		return
	}

	traceContext, err := getTraceContext(ctx, span, queueName(params.QueueUrl), sendMessageSize(params))
	if err != nil {
		instr.Logger().Debug("Unable to get trace context: %s", err.Error())
		return
	}

	if params.MessageAttributes == nil {
		params.MessageAttributes = make(map[string]types.MessageAttributeValue)
	}

	injectTraceContext(traceContext, params.MessageAttributes)
}

func handleSendMessageBatch(ctx context.Context, span *tracer.Span, in middleware.InitializeInput) {
	params, ok := in.Parameters.(*sqs.SendMessageBatchInput)
	if !ok {
		instr.Logger().Debug("Unable to read SendMessageBatch params")
		return
	}

	for i := range params.Entries {
		traceContext, err := getTraceContext(ctx, span, queueName(params.QueueUrl), sendMessageBatchEntrySize(&params.Entries[i]))
		if err != nil {
			instr.Logger().Debug("Unable to get trace context: %s", err.Error())
			continue
		}
		if params.Entries[i].MessageAttributes == nil {
			params.Entries[i].MessageAttributes = make(map[string]types.MessageAttributeValue)
		}
		injectTraceContext(traceContext, params.Entries[i].MessageAttributes)
	}
}

func getTraceContext(ctx context.Context, span *tracer.Span, queue string, payloadSize int64) (types.MessageAttributeValue, error) {
	carrier := tracer.TextMapCarrier{}
	err := tracer.Inject(span.Context(), carrier)
	if err != nil {
		return types.MessageAttributeValue{}, err
	}

	checkpointCtx, ok := tracer.SetDataStreamsCheckpointWithParams(
		ctx,
		options.CheckpointParams{PayloadSize: payloadSize},
		"direction:out",
		"type:sqs",
		"topic:"+queue,
	)
	if ok {
		datastreams.InjectToBase64Carrier(checkpointCtx, carrier)
	}

	jsonBytes, err := json.Marshal(carrier)
	if err != nil {
		return types.MessageAttributeValue{}, err
	}

	attribute := types.MessageAttributeValue{
		DataType:    aws.String("String"),
		StringValue: aws.String(string(jsonBytes)),
	}

	return attribute, nil
}

func injectTraceContext(traceContext types.MessageAttributeValue, messageAttributes map[string]types.MessageAttributeValue) {
	// SQS only allows a maximum of 10 message attributes.
	// https://docs.aws.amazon.com/AWSSimpleQueueService/latest/SQSDeveloperGuide/sqs-message-metadata.html#sqs-message-attributes
	// Only inject if there's room.
	if len(messageAttributes) >= maxMessageAttributes {
		instr.Logger().Info("Cannot inject trace context: message already has maximum allowed attributes")
		return
	}

	messageAttributes[datadogKey] = traceContext
}

func queueName(queueURL *string) string {
	if queueURL == nil || *queueURL == "" {
		return ""
	}
	parts := strings.Split(strings.TrimRight(*queueURL, "/"), "/")
	return parts[len(parts)-1]
}

func sendMessageSize(params *sqs.SendMessageInput) int64 {
	if params == nil {
		return 0
	}

	var size int64
	if params.MessageBody != nil {
		size += int64(len(*params.MessageBody))
	}
	return size + messageAttributesSize(params.MessageAttributes)
}

func sendMessageBatchEntrySize(entry *types.SendMessageBatchRequestEntry) int64 {
	if entry == nil {
		return 0
	}

	var size int64
	if entry.MessageBody != nil {
		size += int64(len(*entry.MessageBody))
	}
	return size + messageAttributesSize(entry.MessageAttributes)
}

func messageAttributesSize(attrs map[string]types.MessageAttributeValue) int64 {
	var size int64
	for name, attr := range attrs {
		size += int64(len(name))
		if attr.DataType != nil {
			size += int64(len(*attr.DataType))
		}
		if attr.StringValue != nil {
			size += int64(len(*attr.StringValue))
		}
		size += int64(len(attr.BinaryValue))
	}
	return size
}
