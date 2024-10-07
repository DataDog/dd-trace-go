package sqs

import (
	"context"
	"github.com/aws/aws-sdk-go-v2/service/sqs/types"
	"github.com/aws/smithy-go/middleware"
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
	// TODO
}

func handleSendMessageBatch(ctx context.Context, in middleware.InitializeInput) {
	// TODO
}

func injectTraceContext(ctx context.Context, messageAttributes map[string]types.MessageAttributeValue) {
	// TODO
}
