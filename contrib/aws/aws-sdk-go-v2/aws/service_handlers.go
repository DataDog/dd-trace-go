package aws

import (
	"context"
	"fmt"
	"github.com/aws/aws-sdk-go-v2/service/eventbridge"
	"github.com/aws/aws-sdk-go-v2/service/sns"
	snstypes "github.com/aws/aws-sdk-go-v2/service/sns/types"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	sqstypes "github.com/aws/aws-sdk-go-v2/service/sqs/types"
	"github.com/aws/smithy-go/middleware"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
)

// TODO tags?
func (mw *traceMiddleware) handleSQSOperation(ctx context.Context, in middleware.InitializeInput, operation string) {
	fmt.Println("[nhulston tracer] handleSQSOperation()")

	switch operation {
	case "SendMessage":
		fmt.Println("[nhulston tracer] Operation SendMessage")
		if params, ok := in.Parameters.(*sqs.SendMessageInput); ok {
			if params.MessageAttributes == nil {
				params.MessageAttributes = make(map[string]sqstypes.MessageAttributeValue)
			}
			err := injectTraceContext(ctx, params.MessageAttributes)
			if err != nil {
				fmt.Printf("[nhulston tracer] Error: %s", err.Error())
			}
		}
	case "SendMessageBatch":
		fmt.Println("[nhulston tracer] Operation SendMessageBatch")
		if params, ok := in.Parameters.(*sqs.SendMessageBatchInput); ok {
			for i := range params.Entries {
				if params.Entries[i].MessageAttributes == nil {
					params.Entries[i].MessageAttributes = make(map[string]sqstypes.MessageAttributeValue)
				}
				err := injectTraceContext(ctx, params.Entries[i].MessageAttributes)
				if err != nil {
					fmt.Printf("[nhulston tracer] Error: %s", err.Error())
				}
			}
		}
	}
}

func (mw *traceMiddleware) handleSNSOperation(ctx context.Context, in middleware.InitializeInput, operation string) {
	fmt.Println("[nhulston tracer] handleSNSOperation()")

	switch operation {
	case "Publish":
		fmt.Println("[nhulston tracer] Operation Publish")
		if params, ok := in.Parameters.(*sns.PublishInput); ok {
			if params.MessageAttributes == nil {
				params.MessageAttributes = make(map[string]snstypes.MessageAttributeValue)
			}
			err := injectTraceContext(ctx, params.MessageAttributes)
			if err != nil {
				fmt.Printf("[nhulston tracer] Error: %s", err.Error())
			}
		}
	case "PublishBatch":
		fmt.Println("[nhulston tracer] Operation PublishBatch")
		if params, ok := in.Parameters.(*sns.PublishBatchInput); ok {
			err := injectTraceContextBatch(ctx, params.PublishBatchRequestEntries)
			if err != nil {
				fmt.Printf("[nhulston tracer] Error: %s", err.Error())
			}
		}
	}
}

func (mw *traceMiddleware) handleEventBridgeOperation(ctx context.Context, in middleware.InitializeInput, operation string) {
	fmt.Println("[nhulston tracer] handleEventBridgeOperation()")

	switch operation {
	case "PutEvents":
		fmt.Println("[nhulston tracer] Operation PutEvents")
		if params, ok := in.Parameters.(*eventbridge.PutEventsInput); ok {
			var eventBusName string
			for i := range params.Entries {
				if params.Entries[i].EventBusName != nil {
					eventBusName = *params.Entries[i].EventBusName
					break
				}
			}

			span, _ := tracer.SpanFromContext(ctx)
			if span != nil && eventBusName != "" {
				span.SetTag("eventbridge.event_bus_name", eventBusName)
			}

			for i := range params.Entries {
				err := injectTraceContextEventBridge(ctx, &params.Entries[i])
				if err != nil {
					fmt.Printf("[nhulston tracer] Error: %s", err.Error())
				}
			}
		}
	}
}
