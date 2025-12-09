// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package sns

import (
	"encoding/json"

	"github.com/DataDog/dd-trace-go/contrib/aws/aws-sdk-go-v2/v2/internal"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/sns"
	"github.com/aws/aws-sdk-go-v2/service/sns/types"
	"github.com/aws/smithy-go/middleware"
)

const (
	datadogKey           = "_datadog"
	maxMessageAttributes = 10
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

	for i := range params.PublishBatchRequestEntries {
		if params.PublishBatchRequestEntries[i].MessageAttributes == nil {
			params.PublishBatchRequestEntries[i].MessageAttributes = make(map[string]types.MessageAttributeValue)
		}
		injectTraceContext(traceContext, params.PublishBatchRequestEntries[i].MessageAttributes)
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

func injectTraceContext(traceContext types.MessageAttributeValue, messageAttributes map[string]types.MessageAttributeValue) {
	// SNS only allows a maximum of 10 message attributes.
	// https://docs.aws.amazon.com/sns/latest/dg/sns-message-attributes.html
	// Only inject if there's room.
	if len(messageAttributes) >= maxMessageAttributes {
		instr.Logger().Info("Cannot inject trace context: message already has maximum allowed attributes")
		return
	}

	messageAttributes[datadogKey] = traceContext
}
