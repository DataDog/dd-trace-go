// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package sdkgov2_test

import (
	"context"
	"log"

	cloudevents "github.com/cloudevents/sdk-go/v2"

	sdkgov2 "github.com/DataDog/dd-trace-go/contrib/cloudevents/sdk-go.v2"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
)

// To trace CloudEvents, wrap your handler function with TraceWrapCloudEventsHandler.
func Example_handler() {
	tracer.Start()
	defer tracer.Stop()

	// Define your CloudEvents handler
	handler := func(ctx context.Context, event cloudevents.Event) error {
		// Your event processing logic here
		log.Printf("Received event: %s", event.ID())
		return nil
	}

	// Wrap the handler with tracing
	tracedHandler := sdkgov2.WrapHandler(handler, sdkgov2.WithResourceName("my-subscription"))

	// Use the traced handler with CloudEvents client
	c, err := cloudevents.NewClientHTTP()
	if err != nil {
		log.Fatal(err)
	}

	if err := c.StartReceiver(context.Background(), tracedHandler); err != nil {
		log.Fatal(err)
	}
}

// To include event subject in traces (opt-in for security), use WithSubject option.
func Example_handlerWithSubject() {
	tracer.Start()
	defer tracer.Stop()

	handler := func(ctx context.Context, event cloudevents.Event) error {
		log.Printf("Received event: %s", event.ID())
		return nil
	}

	// Include subject field in span tags (be cautious of sensitive data)
	tracedHandler := sdkgov2.WrapHandler(
		handler,
		sdkgov2.WithResourceName("my-subscription"),
		sdkgov2.WithSubject(),
	)

	c, err := cloudevents.NewClientHTTP()
	if err != nil {
		log.Fatal(err)
	}

	if err := c.StartReceiver(context.Background(), tracedHandler); err != nil {
		log.Fatal(err)
	}
}

// To trace CloudEvents client for sending events, wrap the client with NewClient.
func Example_client() {
	tracer.Start()
	defer tracer.Stop()

	// Create a regular CloudEvents client
	c, err := cloudevents.NewClientHTTP()
	if err != nil {
		log.Fatal(err)
	}

	// Wrap it with tracing
	tc := sdkgov2.NewClient(c)

	// Create and send an event
	event := cloudevents.NewEvent()
	event.SetID("example-event")
	event.SetType("com.example.event")
	event.SetSource("example/source")
	event.SetData(cloudevents.ApplicationJSON, map[string]string{"key": "value"})

	// Send will automatically inject trace context and create a span
	if result := tc.Send(context.Background(), event); cloudevents.IsUndelivered(result) {
		log.Printf("Failed to send: %v", result)
	}
}

// To manually inject trace context into an event for publishing.
func Example_injectTraceContext() {
	tracer.Start()
	defer tracer.Stop()

	// Start a span
	span, ctx := tracer.StartSpanFromContext(context.Background(), "publish.event")
	defer span.Finish()

	// Create an event
	event := cloudevents.NewEvent()
	event.SetID("example-event")
	event.SetType("com.example.event")
	event.SetSource("example/source")

	carrier := sdkgov2.NewEventCarrier(&event)

	// Inject trace context
	if err := tracer.Inject(span.Context(), carrier); err != nil {
		log.Fatal(err)
	}

	// Now publish the event with trace context included
	c, err := cloudevents.NewClientHTTP()
	if err != nil {
		log.Fatal(err)
	}

	if result := c.Send(ctx, event); cloudevents.IsUndelivered(result) {
		log.Printf("Failed to send: %v", result)
	}
}
