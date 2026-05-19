// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package sns

import (
	"encoding/json"

	"github.com/DataDog/dd-trace-go/contrib/aws/aws-sdk-go-v2/v2/internal"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/sns"
	"github.com/aws/aws-sdk-go-v2/service/sns/types"
	"github.com/aws/smithy-go/middleware"

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

func EnrichOperation(span *tracer.Span, in middleware.InitializeInput, operation string) {
	switch operation {
	case "Publish":
		handlePublish(span, in)
	case "PublishBatch":
		handlePublishBatch(span, in)
	}
}

func handlePublish(span *tracer.Span, in middleware.InitializeInput) {
	params, ok := in.Parameters.(*sns.PublishInput)
	if !ok {
		instr.Logger().Debug("Unable to read PublishInput params")
		return
	}

	traceContext, err := getTraceContext(span)
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

func handlePublishBatch(span *tracer.Span, in middleware.InitializeInput) {
	params, ok := in.Parameters.(*sns.PublishBatchInput)
	if !ok {
		instr.Logger().Debug("Unable to read PublishBatch params")
		return
	}

	traceContext, err := getTraceContext(span)
	if err != nil {
		instr.Logger().Debug("Unable to get trace context: %s", err.Error())
		return
	}

	ctxSize := attributeSize(datadogKey, traceContext)
	runningSize := batchTotalSize(params.PublishBatchRequestEntries)

	for i := range params.PublishBatchRequestEntries {
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

func getTraceContext(span *tracer.Span) (types.MessageAttributeValue, error) {
	carrier := tracer.TextMapCarrier{}
	err := tracer.Inject(span.Context(), carrier)
	if err != nil {
		return types.MessageAttributeValue{}, err
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
		if entry.Message != nil {
			total += len(*entry.Message)
		}
		total += sizeAttributes(entry.MessageAttributes)
	}
	return total
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
