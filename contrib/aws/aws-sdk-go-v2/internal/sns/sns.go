// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package sns

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/DataDog/dd-trace-go/contrib/aws/aws-sdk-go-v2/v2/internal"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/sns"
	"github.com/aws/aws-sdk-go-v2/service/sns/types"
	"github.com/aws/smithy-go/middleware"

	"github.com/DataDog/dd-trace-go/v2/datastreams"
	"github.com/DataDog/dd-trace-go/v2/datastreams/options"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
)

const (
	datadogKey           = "_datadog"
	maxMessageAttributes = 10
	// maxMessageSizeBytes is the SNS maximum payload size for both Publish and
	// PublishBatch (262 144 bytes), minus a 256-byte safety margin for potential
	// undocumented framing overhead in SNS's internal size accounting.
	// https://docs.aws.amazon.com/sns/latest/dg/sns-message-attributes.html
	maxMessageSizeBytes = 262144 - 256
)

var instr = internal.Instr

func EnrichOperation(ctx context.Context, span *tracer.Span, in middleware.InitializeInput, operation string) {
	switch operation {
	case "Publish":
		handlePublish(ctx, span, in)
	case "PublishBatch":
		handlePublishBatch(ctx, span, in)
	}
}

func handlePublish(ctx context.Context, span *tracer.Span, in middleware.InitializeInput) {
	params, ok := in.Parameters.(*sns.PublishInput)
	if !ok {
		instr.Logger().Debug("Unable to read PublishInput params")
		return
	}

	traceContext, err := getTraceContext(ctx, span, destinationName(params.TopicArn, params.TargetArn), int64(publishInputSize(params)))
	if err != nil {
		instr.Logger().Debug("Unable to get trace context: %s", err.Error())
		return
	}

	msgSize := publishInputSize(params)
	if msgSize+attributeSize(datadogKey, traceContext) > maxMessageSizeBytes {
		instr.Logger().Debug("Cannot inject trace context: message size limit would be exceeded")
		return
	}

	if params.MessageAttributes == nil {
		params.MessageAttributes = make(map[string]types.MessageAttributeValue)
	}
	injectTraceContext(traceContext, params.MessageAttributes)
}

func handlePublishBatch(ctx context.Context, span *tracer.Span, in middleware.InitializeInput) {
	params, ok := in.Parameters.(*sns.PublishBatchInput)
	if !ok {
		instr.Logger().Debug("Unable to read PublishBatch params")
		return
	}

	runningSize := batchTotalSize(params.PublishBatchRequestEntries)

	for i := range params.PublishBatchRequestEntries {
		traceContext, err := getTraceContext(
			ctx,
			span,
			destinationName(params.TopicArn, nil),
			int64(publishBatchEntrySize(&params.PublishBatchRequestEntries[i])),
		)
		if err != nil {
			instr.Logger().Debug("Unable to get trace context: %s", err.Error())
			continue
		}
		ctxSize := attributeSize(datadogKey, traceContext)
		if runningSize+ctxSize > maxMessageSizeBytes {
			instr.Logger().Debug("Cannot inject trace context: batch size limit would be exceeded")
			break
		}
		if params.PublishBatchRequestEntries[i].MessageAttributes == nil {
			params.PublishBatchRequestEntries[i].MessageAttributes = make(map[string]types.MessageAttributeValue)
		}
		if injectTraceContext(traceContext, params.PublishBatchRequestEntries[i].MessageAttributes) {
			runningSize += ctxSize
		}
	}
}

func getTraceContext(ctx context.Context, span *tracer.Span, destination string, payloadSize int64) (types.MessageAttributeValue, error) {
	carrier := tracer.TextMapCarrier{}
	err := tracer.Inject(span.Context(), carrier)
	if err != nil {
		return types.MessageAttributeValue{}, err
	}

	checkpointCtx, ok := tracer.SetDataStreamsCheckpointWithParams(
		ctx,
		options.CheckpointParams{PayloadSize: payloadSize},
		"direction:out",
		"type:sns",
		"topic:"+destination,
	)
	if ok {
		datastreams.InjectToBase64Carrier(checkpointCtx, carrier)
	}

	jsonBytes, err := json.Marshal(carrier)
	if err != nil {
		return types.MessageAttributeValue{}, err
	}

	// Use Binary since SNS subscription filter policies fail silently with JSON
	// strings. https://github.com/DataDog/datadog-lambda-js/pull/269
	attribute := types.MessageAttributeValue{
		DataType:    aws.String("Binary"),
		BinaryValue: jsonBytes,
	}

	return attribute, nil
}

// attributeSize returns the byte size SNS counts for a single message attribute:
// name + data type + value length.
func attributeSize(name string, attr types.MessageAttributeValue) int {
	size := len(name)
	if attr.DataType != nil {
		size += len(*attr.DataType)
	}
	if attr.StringValue != nil {
		size += len(*attr.StringValue)
	}
	size += len(attr.BinaryValue)
	return size
}

func sizeAttributes(attrs map[string]types.MessageAttributeValue) int {
	size := 0
	for name, attr := range attrs {
		size += attributeSize(name, attr)
	}
	return size
}

func publishInputSize(params *sns.PublishInput) int {
	size := 0
	if params.Message != nil {
		size += len(*params.Message)
	}
	return size + sizeAttributes(params.MessageAttributes)
}

func batchTotalSize(entries []types.PublishBatchRequestEntry) int {
	total := 0
	for _, entry := range entries {
		total += publishBatchEntrySize(&entry)
	}
	return total
}

func publishBatchEntrySize(entry *types.PublishBatchRequestEntry) int {
	if entry == nil {
		return 0
	}

	size := 0
	if entry.Message != nil {
		size += len(*entry.Message)
	}
	return size + sizeAttributes(entry.MessageAttributes)
}

func injectTraceContext(traceContext types.MessageAttributeValue, messageAttributes map[string]types.MessageAttributeValue) bool {
	// https://docs.aws.amazon.com/sns/latest/dg/sns-message-attributes.html
	if len(messageAttributes) >= maxMessageAttributes {
		instr.Logger().Info("Cannot inject trace context: message already has maximum allowed attributes")
		return false
	}

	messageAttributes[datadogKey] = traceContext
	return true
}

func destinationName(topicArn *string, targetArn *string) string {
	switch {
	case topicArn != nil && *topicArn != "":
		return arnResourceName(*topicArn)
	case targetArn != nil && *targetArn != "":
		return arnResourceName(*targetArn)
	default:
		return ""
	}
}

func arnResourceName(arn string) string {
	parts := strings.Split(arn, ":")
	return parts[len(parts)-1]
}
