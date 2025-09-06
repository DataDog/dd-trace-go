package ddlambda_test

import (
	"context"
	"encoding/json"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"strings"
	"testing"

	"github.com/aws/aws-lambda-go/events"

	ddlambda "github.com/DataDog/dd-trace-go/v2/contrib/aws/datadog-lambda-go"
)

var exampleSQSExtractor = func(ctx context.Context, ev json.RawMessage) map[string]string {
	eh := events.SQSEvent{}

	headers := map[string]string{}

	if err := json.Unmarshal(ev, &eh); err != nil {
		return headers
	}

	// Using SQS as a trigger with a batchSize=1 so its important we check for this as a single SQS message
	// will drive the execution of the handler.
	if len(eh.Records) != 1 {
		return headers
	}

	record := eh.Records[0]

	lowercaseHeaders := map[string]string{}
	for k, v := range record.MessageAttributes {
		if v.StringValue != nil {
			lowercaseHeaders[strings.ToLower(k)] = *v.StringValue
		}
	}

	return lowercaseHeaders
}

func TestCustomExtractorExample(t *testing.T) {
	handler := func(ctx context.Context, event events.SQSEvent) error {
		// Use the parent span retrieved from the SQS Message Attributes.
		span, _ := tracer.SpanFromContext(ctx)
		span.SetTag("key", "value")
		return nil
	}

	cfg := &ddlambda.Config{
		TraceContextExtractor: exampleSQSExtractor,
	}
	ddlambda.WrapFunction(handler, cfg)
}
