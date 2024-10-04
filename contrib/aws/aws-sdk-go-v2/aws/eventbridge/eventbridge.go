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
	"gopkg.in/DataDog/dd-trace-go.v1/internal/log"
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

func EnrichOperation(ctx context.Context, in middleware.InitializeInput, operation string) error {
	switch operation {
	case "PutEvents":
		return handlePutEvents(ctx, in)
	default:
		return fmt.Errorf("unsupported operation: " + operation)
	}
}

func handlePutEvents(ctx context.Context, in middleware.InitializeInput) error {
	params, ok := in.Parameters.(*eventbridge.PutEventsInput)
	if !ok {
		return fmt.Errorf("unable to process PutEvents params")
	}

	// All entries will have the same EventBusName.
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
		err := injectTraceContext(ctx, &params.Entries[i])
		if err != nil {
			// Leave entry unmodified and log, but continue with other entries.
			log.Debug("Unable to parse detail JSON: %s", err)
		}
	}

	return nil
}

func injectTraceContext(ctx context.Context, entry *types.PutEventsRequestEntry) error {
	span, _ := tracer.SpanFromContext(ctx)
	if span == nil {
		return fmt.Errorf("unable to find span")
	}

	carrier := make(messageCarrier)
	err := tracer.Inject(span.Context(), carrier)
	if err != nil {
		return err
	}

	// Add start time and resource name
	startTimeMillis := time.Now().UnixMilli()
	carrier[startTimeKey] = fmt.Sprintf("%d", startTimeMillis)
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
			// `detail` is not in a valid JSON format. Leave entry unmodified.
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
