// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

// Package sdkgov2 provides tracing for the cloudevents/sdk-go/v2 package (https://github.com/cloudevents/sdk-go).
//
// CloudEvents is a specification for describing event data in a common way. This integration
// enables distributed tracing across CloudEvents publishers and consumers by propagating trace
// context through CloudEvent extensions.
//
// # Handler Tracing
//
// To trace a CloudEvents handler, wrap your handler function with WrapHandler:
//
//	handler := func(ctx context.Context, event cloudevents.Event) error {
//	    // Your event processing logic
//	    return nil
//	}
//
//	tracedHandler := sdkgov2.WrapHandler(handler, sdkgov2.WithResourceName("my-subscription"))
//
//	c, _ := cloudevents.NewClient()
//	c.StartReceiver(context.Background(), tracedHandler)
//
// # Client Tracing
//
// To trace CloudEvents client operations, wrap your client with NewClient:
//
//	c, _ := cloudevents.NewClientHTTP()
//	tc := sdkgov2.NewClient(c)
//
//	event := cloudevents.NewEvent()
//	event.SetType("com.example.event")
//	tc.Send(context.Background(), event)
//
// You can also configure the client with default options that apply to all operations:
//
//	tc := sdkgov2.NewClient(c,
//	    sdkgov2.WithResourceName("my-publisher"),
//	    sdkgov2.WithMessagingSystem(ext.MessagingSystemKafka),
//	)
//
// # Options
//
// By default, the event subject field is not included in span tags as it may contain
// sensitive data. Use WithSubject() option to opt-in to including it:
//
//	tracedHandler := sdkgov2.WrapHandler(
//	    handler,
//	    sdkgov2.WithResourceName("my-subscription"),
//	    sdkgov2.WithSubject(),
//	)
//
// # Trace Propagation
//
// Trace context is automatically propagated through CloudEvent extensions using W3C trace
// context headers (traceparent, tracestate). This enables distributed tracing across
// different services and messaging systems.
//
// IMPORTANT: CloudEvents extension names cannot contain hyphens, which means Datadog-specific
// headers (x-datadog-trace-id, x-datadog-parent-id, etc.) cannot be used. You must configure
// the tracer to use W3C trace context format by setting the DD_TRACE_PROPAGATION_STYLE
// environment variable to include "tracecontext".
//
// For manual trace context injection and extraction, use NewEventCarrier to create a carrier
// that implements tracer.TextMapReader and tracer.TextMapWriter:
//
//	// Injecting trace context
//	carrier := sdkgov2.NewEventCarrier(&event)
//	tracer.Inject(span.Context(), carrier)
//
//	// Extracting trace context
//	carrier := sdkgov2.NewEventCarrier(&event)
//	spanCtx, err := tracer.Extract(carrier)
package sdkgov2
