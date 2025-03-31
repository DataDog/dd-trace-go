// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package sqs

import (
	"encoding/json"

	"github.com/DataDog/dd-trace-go/contrib/aws/aws-sdk-go-v2/v2/internal"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	"github.com/aws/aws-sdk-go-v2/service/sqs/types"
	"github.com/aws/smithy-go/middleware"
)

const (
	datadogKey           = "_datadog"
	maxMessageAttributes = 10
)

var instr = internal.Instr

func EnrichOperation(span *tracer.Span, in middleware.InitializeInput, operation string) {
	switch operation {
	case "SendMessage":
		handleSendMessage(span, in)
	case "SendMessageBatch":
		handleSendMessageBatch(span, in)
	}
}

func handleSendMessage(span *tracer.Span, in middleware.InitializeInput) {
	params, ok := in.Parameters.(*sqs.SendMessageInput)
	if !ok {
		instr.Logger().Debug("Unable to read SendMessage params")
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

func handleSendMessageBatch(span *tracer.Span, in middleware.InitializeInput) {
	params, ok := in.Parameters.(*sqs.SendMessageBatchInput)
	if !ok {
		instr.Logger().Debug("Unable to read SendMessageBatch params")
		return
	}

	traceContext, err := getTraceContext(span)
	if err != nil {
		instr.Logger().Debug("Unable to get trace context: %s", err.Error())
		return
	}

	for i := range params.Entries {
		if params.Entries[i].MessageAttributes == nil {
			params.Entries[i].MessageAttributes = make(map[string]types.MessageAttributeValue)
		}
		injectTraceContext(traceContext, params.Entries[i].MessageAttributes)
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
