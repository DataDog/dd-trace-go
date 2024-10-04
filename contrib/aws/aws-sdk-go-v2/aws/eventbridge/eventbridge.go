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

func EnrichOperation(ctx context.Context, in middleware.InitializeInput, operation string) {
	switch operation {
	case "PutEvents":
		handlePutEvents(ctx, in)
	}
}

func handlePutEvents(ctx context.Context, in middleware.InitializeInput) {
	params, ok := in.Parameters.(*eventbridge.PutEventsInput)
	if !ok {
		fmt.Println("Unable to read PutEvents params")
		return
	}

	// Entries could be sent through different EventBuses, so we
	// find the first valid bus name to set the tag.
	var eventBusName string
	for i := range params.Entries {
		if params.Entries[i].EventBusName != nil {
			eventBusName = *params.Entries[i].EventBusName
			break
		}
	}

	// Set tags
	span, _ := tracer.SpanFromContext(ctx)
	if span != nil && eventBusName != "" {
		// TODO tags
		span.SetTag("eventbridge.event_bus_name", eventBusName)
	}

	for i := range params.Entries {
		injectTraceContext(ctx, &params.Entries[i])
	}
}

// Injects trace context into the `detail` field of a PutEventsRequestEntry
func injectTraceContext(ctx context.Context, entry *types.PutEventsRequestEntry) {
	span, _ := tracer.SpanFromContext(ctx)
	if span == nil {
		fmt.Println("Unable to find span from context")
		return
	}

	carrier := make(messageCarrier)
	err := tracer.Inject(span.Context(), carrier)
	if err != nil {
		fmt.Printf("Unable to inject trace context: %s\n", err.Error())
		return
	}

	// Add start time and resource name
	startTimeMillis := time.Now().UnixMilli()
	carrier[startTimeKey] = fmt.Sprintf("%d", startTimeMillis)
	if entry.EventBusName != nil {
		carrier[resourceNameKey] = *entry.EventBusName
	}

	jsonBytes, err := json.Marshal(carrier)
	if err != nil {
		fmt.Printf("Unable to marshal trace context: %s\n", err.Error())
		return
	}

	var detail map[string]interface{}
	if entry.Detail != nil {
		err = json.Unmarshal([]byte(*entry.Detail), &detail)
		if err != nil {
			fmt.Printf("Unable to unmarshal event detail: %s\n", err.Error())
			return
		}
	} else {
		detail = make(map[string]interface{})
	}

	detail[datadogKey] = json.RawMessage(jsonBytes)

	updatedDetail, err := json.Marshal(detail)
	if err != nil {
		fmt.Printf("Unable to marshal modified event detail: %s\n", err.Error())
		return
	}

	entry.Detail = aws.String(string(updatedDetail))
}
