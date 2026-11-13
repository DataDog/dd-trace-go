// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package sqs

import (
	"context"
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

// Enricher adds the Datadog trace context and the Data Streams checkpoint to SQS requests.
// The zero value injects the trace context.
type Enricher struct {
	noInject bool
}

// NewEnricher returns an enricher that writes the trace context into the request only if inject is true.
// The span tags and the Data Streams checkpoint are recorded either way.
func NewEnricher(inject bool) *Enricher {
	return &Enricher{noInject: !inject}
}

func (e *Enricher) EnrichOperation(ctx context.Context, span *tracer.Span, in middleware.InitializeInput, operation string) middleware.InitializeInput {
	switch operation {
	case "SendMessage":
		e.handleSendMessage(ctx, span, &in)
	case "SendMessageBatch":
		e.handleSendMessageBatch(ctx, span, &in)
	}
	span.SetTag(ext.MessagingSystem, ext.MessagingSystemSQS)
	return in
}

func (e *Enricher) handleSendMessage(ctx context.Context, span *tracer.Span, in *middleware.InitializeInput) {
	params, ok := in.Parameters.(*sqs.SendMessageInput)
	if !ok {
		instr.Logger().Debug("Unable to read SendMessage params")
		return
	}

	messageSize := sendMessageSize(params)
	traceContext, err := getTraceContext(ctx, span, queueName(params.QueueUrl), messageSize)
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
		traceContext, err := getTraceContext(ctx, span, queueName(params.QueueUrl), sendMessageBatchEntrySize(&params.Entries[i]))
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
