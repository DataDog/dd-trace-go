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

func EnrichOperation(ctx context.Context, span *tracer.Span, in middleware.InitializeInput, operation string, dsmEnabled bool) {
	switch operation {
	case "SendMessage":
		handleSendMessage(ctx, span, in, dsmEnabled)
	case "SendMessageBatch":
		handleSendMessageBatch(ctx, span, in, dsmEnabled)
	case "ReceiveMessage":
		if dsmEnabled {
			ensureDatadogAttributeRequested(in)
		}
	}
	span.SetTag(ext.MessagingSystem, ext.MessagingSystemSQS)
}

// EnrichOperationOutput processes the SQS response after the API call returns.
// For ReceiveMessage it sets a DSM consume checkpoint for each returned message
// and writes the updated pathway back into the message attributes.
func EnrichOperationOutput(out middleware.InitializeOutput, operation string, dsmEnabled bool, queueName string) {
	if !dsmEnabled || operation != "ReceiveMessage" {
		return
	}
	resp, ok := out.Result.(*sqs.ReceiveMessageOutput)
	if !ok || resp == nil {
		return
	}
	for i := range resp.Messages {
		setConsumeCheckpoint(&resp.Messages[i], queueName)
	}
}

func handleSendMessage(ctx context.Context, span *tracer.Span, in middleware.InitializeInput, dsmEnabled bool) {
	params, ok := in.Parameters.(*sqs.SendMessageInput)
	if !ok {
		instr.Logger().Debug("Unable to read SendMessage params")
		return
	}

	traceContext, err := getTraceContext(ctx, span, queueName(params.QueueUrl), sendMessageSize(params), dsmEnabled)
	if err != nil {
		instr.Logger().Debug("Unable to get trace context: %s", err.Error())
		return
	}

	if params.MessageAttributes == nil {
		params.MessageAttributes = make(map[string]types.MessageAttributeValue)
	}

	injectTraceContext(traceContext, params.MessageAttributes)
}

func handleSendMessageBatch(ctx context.Context, span *tracer.Span, in middleware.InitializeInput, dsmEnabled bool) {
	params, ok := in.Parameters.(*sqs.SendMessageBatchInput)
	if !ok {
		instr.Logger().Debug("Unable to read SendMessageBatch params")
		return
	}

	for i := range params.Entries {
		traceContext, err := getTraceContext(ctx, span, queueName(params.QueueUrl), sendMessageBatchEntrySize(&params.Entries[i]), dsmEnabled)
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

func getTraceContext(ctx context.Context, span *tracer.Span, queue string, payloadSize int64, dsmEnabled bool) (types.MessageAttributeValue, error) {
	carrier := tracer.TextMapCarrier{}
	err := tracer.Inject(span.Context(), carrier)
	if err != nil {
		return types.MessageAttributeValue{}, err
	}

	if dsmEnabled {
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

// setConsumeCheckpoint extracts the producer pathway from msg's _datadog
// attribute (if any), sets a consume DSM checkpoint chained from it, and
// writes the updated pathway back into the message attributes.
func setConsumeCheckpoint(msg *types.Message, queueName string) {
	if msg == nil {
		return
	}
	carrier := readDatadogCarrier(msg.MessageAttributes)
	parentCtx := datastreams.ExtractFromBase64Carrier(context.Background(), carrier)
	newCtx, ok := tracer.SetDataStreamsCheckpointWithParams(
		parentCtx,
		options.CheckpointParams{PayloadSize: messageSize(msg)},
		"direction:in", "topic:"+queueName, "type:sqs",
	)
	if !ok {
		return
	}
	if carrier == nil {
		carrier = tracer.TextMapCarrier{}
	}
	datastreams.InjectToBase64Carrier(newCtx, carrier)
	writeDatadogCarrier(msg, carrier)
}

// ensureDatadogAttributeRequested adds _datadog to MessageAttributeNames so
// SQS returns it with each message. SQS does not return message attributes by
// default; the caller must explicitly list them.
func ensureDatadogAttributeRequested(in middleware.InitializeInput) {
	params, ok := in.Parameters.(*sqs.ReceiveMessageInput)
	if !ok {
		return
	}
	for _, name := range params.MessageAttributeNames {
		if name == "All" || name == ".*" || name == datadogKey {
			return
		}
	}
	params.MessageAttributeNames = append(params.MessageAttributeNames, datadogKey)
}

func readDatadogCarrier(attrs map[string]types.MessageAttributeValue) tracer.TextMapCarrier {
	if attrs == nil {
		return nil
	}
	attr, ok := attrs[datadogKey]
	if !ok {
		return nil
	}
	var raw []byte
	if attr.StringValue != nil {
		raw = []byte(*attr.StringValue)
	} else if len(attr.BinaryValue) > 0 {
		raw = attr.BinaryValue
	}
	if len(raw) == 0 {
		return nil
	}
	carrier := tracer.TextMapCarrier{}
	if err := json.Unmarshal(raw, &carrier); err != nil {
		instr.Logger().Debug("Unable to decode _datadog message attribute: %s", err.Error())
		return nil
	}
	return carrier
}

func writeDatadogCarrier(msg *types.Message, carrier tracer.TextMapCarrier) {
	if msg == nil || len(carrier) == 0 {
		return
	}
	jsonBytes, err := json.Marshal(carrier)
	if err != nil {
		instr.Logger().Debug("Unable to encode _datadog message attribute: %s", err.Error())
		return
	}
	if msg.MessageAttributes == nil {
		msg.MessageAttributes = make(map[string]types.MessageAttributeValue)
	}
	msg.MessageAttributes[datadogKey] = types.MessageAttributeValue{
		DataType:    aws.String("String"),
		StringValue: aws.String(string(jsonBytes)),
	}
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

func messageSize(msg *types.Message) int64 {
	var size int64
	if msg.Body != nil {
		size += int64(len(*msg.Body))
	}
	for k, v := range msg.MessageAttributes {
		size += int64(len(k))
		if v.DataType != nil {
			size += int64(len(*v.DataType))
		}
		if v.StringValue != nil {
			size += int64(len(*v.StringValue))
		}
		size += int64(len(v.BinaryValue))
	}
	return size
}
