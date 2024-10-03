package eventbridge

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/eventbridge"
	"github.com/aws/aws-sdk-go-v2/service/eventbridge/types"
	"github.com/aws/smithy-go/middleware"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
	"time"
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

func HandleOperation(ctx context.Context, in middleware.InitializeInput, operation string) error {
	switch operation {
	case "PutEvents":
		return handlePutEvents(ctx, in)
	default:
		return nil
	}
}

func handlePutEvents(ctx context.Context, in middleware.InitializeInput) error {
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
			err := injectTraceContext(ctx, &params.Entries[i])
			if err != nil {
				return fmt.Errorf("unable to inject trace context for entry %d: %w", i, err)
			}
		}
	}
	return nil
}

func injectTraceContext(ctx context.Context, entry *types.PutEventsRequestEntry) error {
	span, _ := tracer.SpanFromContext(ctx)
	if span == nil {
		return nil
	}

	carrier := make(messageCarrier)
	err := tracer.Inject(span.Context(), carrier)
	if err != nil {
		return err
	}

	// Add start time and resource name
	carrier[startTimeKey] = fmt.Sprintf("%d", time.Now().UnixNano()/int64(time.Millisecond))
	if entry.EventBusName != nil {
		carrier[resourceNameKey] = *entry.EventBusName
	}

	jsonBytes, err := json.Marshal(carrier)
	if err != nil {
		return err
	}

	var detail map[string]interface{}
	if entry.Detail != nil {
		err = json.Unmarshal([]byte(*entry.Detail), &detail)
		if err != nil {
			return err
		}
	} else {
		detail = make(map[string]interface{})
	}

	detail[datadogKey] = json.RawMessage(jsonBytes)

	updatedDetail, err := json.Marshal(detail)
	if err != nil {
		return err
	}

	entry.Detail = aws.String(string(updatedDetail))
	return nil
}
