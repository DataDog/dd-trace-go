// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package sdkgov2

import (
	"context"
	"fmt"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/instrumentation"
	"github.com/cloudevents/sdk-go/v2/client"
	"github.com/cloudevents/sdk-go/v2/event"
	"github.com/cloudevents/sdk-go/v2/protocol"
)

var _ client.Client = &Client{}

// Client wraps a CloudEvents client to add tracing capabilities.
type Client struct {
	client client.Client
	cfg    *config
}

// NewClient creates a new traced CloudEvents client that wraps the provided client.
// The client automatically injects trace context into outgoing events using W3C trace context format.
// Options can be provided to configure default behavior for all operations.
func NewClient(c client.Client, opts ...Option) *Client {
	cfg := defaultConfig()
	for _, opt := range opts {
		opt(cfg)
	}
	return &Client{
		client: c,
		cfg:    cfg,
	}
}

// Send wraps the Send method with tracing.
func (tc *Client) Send(ctx context.Context, e event.Event) protocol.Result {
	resourceName := tc.cfg.resourceName
	if resourceName == "" {
		resourceName = e.Type()
	}

	spanOpts := []tracer.StartSpanOption{
		tracer.ResourceName(resourceName),
		tracer.SpanType(ext.SpanTypeMessageProducer),
		tracer.Tag("event.id", e.ID()),
		tracer.Tag("event.type", e.Type()),
		tracer.Tag("event.source", e.Source()),
		tracer.Tag("message_size", len(e.Data())),
		tracer.Tag(ext.Component, "cloudevents"),
		tracer.Tag(ext.SpanKind, ext.SpanKindProducer),
	}

	if tc.cfg.messagingSystem != "" {
		spanOpts = append(spanOpts, tracer.Tag(ext.MessagingSystem, tc.cfg.messagingSystem))
	}

	if tc.cfg.includeSubject && e.Subject() != "" {
		spanOpts = append(spanOpts, tracer.Tag("event.subject", e.Subject()))
	}

	span, ctx := tracer.StartSpanFromContext(
		ctx,
		instr.OperationName(instrumentation.ComponentProducer, instrumentation.OperationContext{
			"cloudeventsOperation": "send",
		}),
		spanOpts...,
	)
	defer span.Finish()

	carrier := NewEventCarrier(&e)
	if err := tracer.Inject(span.Context(), carrier); err != nil {
		return fmt.Errorf("failed to inject trace context: %w", err)
	}

	return tc.client.Send(ctx, e)
}

// Request wraps the Request method with tracing.
func (tc *Client) Request(ctx context.Context, e event.Event) (*event.Event, protocol.Result) {
	return tc.client.Request(ctx, e)
}

// StartReceiver passes through to the underlying client's StartReceiver method.
// To add tracing to your receiver function, wrap it with WrapHandler before passing it to StartReceiver.
//
// The most common CloudEvents handler signature is:
//
//	handler := func(ctx context.Context, event cloudevents.Event) error {
//	    // your event handling logic
//	    return nil
//	}
//	tracedHandler := sdkgov2.WrapHandler(handler, sdkgov2.WithResourceName("my-subscription"))
//	err := client.StartReceiver(ctx, tracedHandler)
//
// CloudEvents supports multiple valid handler signatures:
//   - func()
//   - func() error
//   - func(context.Context)
//   - func(context.Context) error
//   - func(event.Event)
//   - func(event.Event) error
//   - func(context.Context, event.Event)
//   - func(context.Context, event.Event) error
//
// For other handler signatures, you can manually extract trace context using NewEventCarrier:
//
//	handler := func(ctx context.Context) error {
//	    // Manually extract trace context from the event
//	    carrier := sdkgov2.NewEventCarrier(&event)
//	    spanCtx, _ := tracer.Extract(carrier)
//	    span, ctx := tracer.StartSpanFromContext(ctx, "process.event", tracer.ChildOf(spanCtx))
//	    defer span.Finish()
//	    // your event handling logic
//	    return nil
//	}
//	err := client.StartReceiver(ctx, handler)
func (tc *Client) StartReceiver(ctx context.Context, fn interface{}) error {
	return tc.client.StartReceiver(ctx, fn)
}
