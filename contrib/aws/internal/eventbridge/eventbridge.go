package eventbridge

import (
	"context"
	"github.com/aws/aws-sdk-go-v2/service/eventbridge/types"
	"github.com/aws/smithy-go/middleware"
)

const (
	datadogKey      = "_datadog"
	startTimeKey    = "x-datadog-start-time"
	resourceNameKey = "x-datadog-resource-name"
)

type messageCarrier map[string]string

func (carrier messageCarrier) Set(key, val string) {
	carrier[key] = val
}

func EnrichOperation(ctx context.Context, in middleware.InitializeInput, operation string) {
	switch operation {
	case "PutEvents":
		handlePutEvents(ctx, in)
	}
}

func handlePutEvents(ctx context.Context, in middleware.InitializeInput) {
	// TODO
}

func injectTraceContext(ctx context.Context, entry *types.PutEventsRequestEntry) {
	// TODO
}
