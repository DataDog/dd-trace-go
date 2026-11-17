// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package sqs

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"maps"
	"slices"
	"strings"
	"unicode/utf8"

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
	maxMessageBytes      = 1 << 20
	maxBatchEntries      = 10
)

var instr = internal.Instr

// Enricher adds the Datadog trace context and the Data Streams checkpoints to SQS requests and responses.
// The zero value injects the trace context and records no Data Streams checkpoints.
type Enricher struct {
	noInject    bool
	dataStreams bool
}

// NewEnricher returns an enricher that writes the trace context into the request only if inject is true,
// and records Data Streams checkpoints only if dataStreams is true. The span tags and the produce
// checkpoint do not depend on inject.
func NewEnricher(inject, dataStreams bool) *Enricher {
	return &Enricher{noInject: !inject, dataStreams: dataStreams}
}

func (e *Enricher) EnrichOperation(ctx context.Context, span *tracer.Span, in middleware.InitializeInput, operation string) middleware.InitializeInput {
	switch operation {
	case "SendMessage":
		e.handleSendMessage(ctx, span, &in)
	case "SendMessageBatch":
		e.handleSendMessageBatch(ctx, span, &in)
	case "ReceiveMessage":
		e.handleReceiveMessage(&in)
	}
	span.SetTag(ext.MessagingSystem, ext.MessagingSystemSQS)
	return in
}

// EnrichResponse sets a Data Streams consume checkpoint for each message a ReceiveMessage call returned,
// and writes the resulting pathway back into the message's _datadog attribute.
func (e *Enricher) EnrichResponse(in middleware.InitializeInput, out middleware.InitializeOutput, operation string) {
	if !e.dataStreams || operation != "ReceiveMessage" {
		return
	}
	params, ok := in.Parameters.(*sqs.ReceiveMessageInput)
	if !ok {
		return
	}
	resp, ok := out.Result.(*sqs.ReceiveMessageOutput)
	if !ok || resp == nil {
		return
	}
	queue := queueName(params.QueueUrl)
	for i := range resp.Messages {
		setConsumeCheckpoint(&resp.Messages[i], queue)
	}
}

func (e *Enricher) handleSendMessage(ctx context.Context, span *tracer.Span, in *middleware.InitializeInput) {
	params, ok := in.Parameters.(*sqs.SendMessageInput)
	if !ok {
		instr.Logger().Debug("Unable to read SendMessage params")
		return
	}

	messageSize := sendMessageSize(params)
	traceContext, err := e.getTraceContext(ctx, span, queueName(params.QueueUrl), messageSize)
	if err != nil {
		instr.Logger().Debug("Unable to get trace context: %s", err.Error())
		return
	}
	if e.noInject {
		return
	}

	budget := newTraceBudget(messageSize, func() int64 { return messageTextExpansion(params.MessageBody, params.MessageAttributes) })
	if attrs := budget.inject(traceContext, params.MessageAttributes); attrs != nil {
		message := *params
		message.MessageAttributes = attrs
		in.Parameters = &message
	}
}

func (e *Enricher) handleSendMessageBatch(ctx context.Context, span *tracer.Span, in *middleware.InitializeInput) {
	params, ok := in.Parameters.(*sqs.SendMessageBatchInput)
	if !ok {
		instr.Logger().Debug("Unable to read SendMessageBatch params")
		return
	}

	var budget traceBudget
	canInject := !e.noInject
	if canInject && len(params.Entries) > maxBatchEntries {
		instr.Logger().Debug("Cannot inject trace context: SQS batch has %d entries (maximum %d)", len(params.Entries), maxBatchEntries)
		canInject = false
	}
	if canInject {
		var batchSize int64
		for i := range params.Entries {
			batchSize += sendMessageBatchEntrySize(&params.Entries[i])
		}
		budget = newTraceBudget(batchSize, func() int64 {
			var extra int64
			for i := range params.Entries {
				entry := &params.Entries[i]
				extra += messageTextExpansion(entry.MessageBody, entry.MessageAttributes)
			}
			return extra
		})
	}

	original := params
	for i := range params.Entries {
		traceContext, err := e.getTraceContext(ctx, span, queueName(params.QueueUrl), sendMessageBatchEntrySize(&params.Entries[i]))
		if err != nil {
			instr.Logger().Debug("Unable to get trace context: %s", err.Error())
			continue
		}
		if !canInject {
			continue
		}
		if attrs := budget.inject(traceContext, params.Entries[i].MessageAttributes); attrs != nil {
			if params == original {
				batch := *params
				batch.Entries = slices.Clone(params.Entries)
				params = &batch
				in.Parameters = params
			}
			params.Entries[i].MessageAttributes = attrs
		}
	}
}

func (e *Enricher) handleReceiveMessage(in *middleware.InitializeInput) {
	if !e.dataStreams {
		return
	}
	params, ok := in.Parameters.(*sqs.ReceiveMessageInput)
	if !ok {
		instr.Logger().Debug("Unable to read ReceiveMessage params")
		return
	}
	for _, name := range params.MessageAttributeNames {
		if name == "All" || name == ".*" || name == datadogKey {
			return
		}
	}
	receive := *params
	receive.MessageAttributeNames = append(slices.Clip(params.MessageAttributeNames), datadogKey)
	in.Parameters = &receive
}

func (e *Enricher) getTraceContext(ctx context.Context, span *tracer.Span, queue string, payloadSize int64) (types.MessageAttributeValue, error) {
	carrier := tracer.TextMapCarrier{}
	err := tracer.Inject(span.Context(), carrier)
	if err != nil {
		return types.MessageAttributeValue{}, err
	}

	if e.dataStreams {
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

func setConsumeCheckpoint(msg *types.Message, queue string) {
	carrier, binary := datadogCarrier(msg)
	parent := context.Background()
	if carrier != nil {
		parent = datastreams.ExtractFromBase64Carrier(parent, carrier)
	}
	checkpointCtx, ok := tracer.SetDataStreamsCheckpointWithParams(
		parent,
		options.CheckpointParams{PayloadSize: receivedMessageSize(msg)},
		"direction:in",
		"type:sqs",
		"topic:"+queue,
	)
	if !ok {
		return
	}
	if carrier == nil {
		carrier = tracer.TextMapCarrier{}
	}
	datastreams.InjectToBase64Carrier(checkpointCtx, carrier)
	jsonBytes, err := json.Marshal(carrier)
	if err != nil {
		instr.Logger().Debug("Unable to encode _datadog message attribute: %s", err.Error())
		return
	}
	attr := types.MessageAttributeValue{DataType: aws.String("String"), StringValue: aws.String(string(jsonBytes))}
	if binary {
		attr = types.MessageAttributeValue{DataType: aws.String("Binary"), BinaryValue: jsonBytes}
	}
	if _, exists := msg.MessageAttributes[datadogKey]; !exists && len(msg.MessageAttributes) >= maxMessageAttributes {
		return
	}
	if msg.MessageAttributes == nil {
		msg.MessageAttributes = make(map[string]types.MessageAttributeValue, 1)
	}
	msg.MessageAttributes[datadogKey] = attr
}

func datadogCarrier(msg *types.Message) (carrier tracer.TextMapCarrier, binaryAttribute bool) {
	if attr, ok := msg.MessageAttributes[datadogKey]; ok {
		switch {
		case attr.StringValue != nil:
			return decodeCarrier([]byte(*attr.StringValue)), false
		case attr.BinaryValue != nil:
			return decodeCarrier(attr.BinaryValue), true
		}
		return nil, false
	}
	return snsNotificationCarrier(aws.ToString(msg.Body)), false
}

type snsNotification struct {
	Type              string
	MessageAttributes map[string]struct {
		Type  string
		Value string
	}
}

func snsNotificationCarrier(body string) tracer.TextMapCarrier {
	if !strings.HasPrefix(strings.TrimSpace(body), "{") || !strings.Contains(body, datadogKey) {
		return nil
	}
	var notification snsNotification
	if err := json.Unmarshal([]byte(body), &notification); err != nil || notification.Type != "Notification" {
		return nil
	}
	attr, ok := notification.MessageAttributes[datadogKey]
	if !ok {
		return nil
	}
	switch attr.Type {
	case "Binary":
		raw, err := base64.StdEncoding.DecodeString(attr.Value)
		if err != nil {
			instr.Logger().Debug("Unable to decode SNS _datadog message attribute: %s", err.Error())
			return nil
		}
		return decodeCarrier(raw)
	case "String":
		return decodeCarrier([]byte(attr.Value))
	}
	return nil
}

func decodeCarrier(raw []byte) tracer.TextMapCarrier {
	var carrier tracer.TextMapCarrier
	if err := json.Unmarshal(raw, &carrier); err != nil {
		instr.Logger().Debug("Unable to decode _datadog message attribute: %s", err.Error())
		return nil
	}
	return carrier
}

func receivedMessageSize(msg *types.Message) int64 {
	return int64(len(aws.ToString(msg.Body))) + messageAttributesSize(msg.MessageAttributes)
}

// traceBudget starts with raw byte lengths. The SDK replaces each malformed UTF-8 byte
// with U+FFFD. Once that expansion could affect a write, the budget counts it once
// for the whole request and clears textExpansion.
type traceBudget struct {
	remaining     int64
	maxExpansion  int64
	textExpansion func() int64
}

func newTraceBudget(originalSize int64, textExpansion func() int64) traceBudget {
	return traceBudget{
		remaining: maxMessageBytes - originalSize,
		// The SDK replaces each invalid byte with the three-byte U+FFFD, adding at most two bytes per original byte.
		maxExpansion:  int64(len("\uFFFD")-1) * originalSize,
		textExpansion: textExpansion,
	}
}

func (budget *traceBudget) inject(traceContext types.MessageAttributeValue, messageAttributes map[string]types.MessageAttributeValue) map[string]types.MessageAttributeValue {
	// SQS only allows a maximum of 10 message attributes.
	// https://docs.aws.amazon.com/AWSSimpleQueueService/latest/SQSDeveloperGuide/sqs-message-metadata.html#sqs-message-attributes
	// Only inject if there's room.
	if len(messageAttributes) >= maxMessageAttributes {
		instr.Logger().Info("Cannot inject trace context: message already has maximum allowed attributes")
		return nil
	}

	added := attributeSize(datadogKey, traceContext)
	old, replacing := messageAttributes[datadogKey]
	if replacing {
		added -= attributeSize(datadogKey, old)
	}
	if budget.textExpansion != nil && added > 0 && added <= budget.remaining && budget.remaining-added < budget.maxExpansion {
		budget.remaining -= budget.textExpansion()
		budget.textExpansion = nil
	}
	if replacing && budget.textExpansion == nil {
		added -= attributeTextExpansion(old)
	}
	if added > budget.remaining {
		return nil
	}
	budget.remaining -= added
	attrs := make(map[string]types.MessageAttributeValue, len(messageAttributes)+1)
	maps.Copy(attrs, messageAttributes)
	attrs[datadogKey] = traceContext
	return attrs
}

func messageTextExpansion(body *string, attrs map[string]types.MessageAttributeValue) int64 {
	extra := sdkTextExpansion(aws.ToString(body))
	for name, attr := range attrs {
		extra += sdkTextExpansion(name) + attributeTextExpansion(attr)
	}
	return extra
}

func attributeTextExpansion(attr types.MessageAttributeValue) int64 {
	return sdkTextExpansion(aws.ToString(attr.DataType)) + sdkTextExpansion(aws.ToString(attr.StringValue))
}

func sdkTextExpansion(text string) int64 {
	if utf8.ValidString(text) {
		return 0
	}
	var size int64
	for _, char := range text {
		size += int64(utf8.RuneLen(char))
	}
	return size - int64(len(text))
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
		size += attributeSize(name, attr)
	}
	return size
}

func attributeSize(name string, attr types.MessageAttributeValue) int64 {
	metadata := len(name) + len(aws.ToString(attr.DataType))
	value := len(aws.ToString(attr.StringValue)) + len(attr.BinaryValue)
	return int64(metadata + value)
}
