// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

// Package pubsubtrace contains tracing logic for the cloud.google.com/go/pubsub instrumentation.
//
// WARNING: this package SHOULD NOT import cloud.google.com/go/pubsub or cloud.google.com/go/pubsub/v2.
//
// The motivation of this package is to support orchestrion, which cannot use the main package because it imports
// the package, and since orchestrion modifies the library code itself,
// this would cause an import cycle.
package pubsubtrace

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/instrumentation"
)

type Message struct {
	ID              string
	Data            []byte
	OrderingKey     string
	Attributes      map[string]string
	DeliveryAttempt *int
	PublishTime     time.Time
}

type Topic interface {
	String() string
}

type Subscription interface {
	String() string
}

type Tracer struct {
	instr     *instrumentation.Instrumentation
	component instrumentation.Package
}

func NewTracer(instr *instrumentation.Instrumentation, componentName instrumentation.Package) *Tracer {
	return &Tracer{
		instr:     instr,
		component: componentName,
	}
}

func (tr *Tracer) TracePublish(ctx context.Context, topic Topic, msg *Message, opts ...Option) (context.Context, func(serverID string, err error)) {
	cfg := tr.defaultConfig()
	for _, opt := range opts {
		opt.apply(cfg)
	}
	spanOpts := []tracer.StartSpanOption{
		tracer.ResourceName(topic.String()),
		tracer.SpanType(ext.SpanTypeMessageProducer),
		tracer.Tag(ext.PubsubMessageSize, len(msg.Data)),
		tracer.Tag(ext.PubsubOrderingKey, msg.OrderingKey),
		tracer.Tag(ext.Component, tr.component),
		tracer.Tag(ext.SpanKind, ext.SpanKindProducer),
		tracer.Tag(ext.MessagingSystem, ext.MessagingSystemGCPPubsub),
		tracer.Tag(ext.MessagingOperationName, "send"),
		tracer.Tag(ext.MessagingDestinationName, topic.String()),
	}
	if projectID := projectIDFromResourceName(topic.String()); projectID != "" {
		spanOpts = append(spanOpts, tracer.Tag(ext.GCPProjectID, projectID))
	}
	if cfg.serviceName != "" {
		spanOpts = append(spanOpts, instrumentation.ServiceNameWithSource(cfg.serviceName, cfg.serviceSource))
	}
	if cfg.measured {
		spanOpts = append(spanOpts, tracer.Measured())
	}
	span, ctx := tracer.StartSpanFromContext(
		ctx,
		cfg.publishSpanName,
		spanOpts...,
	)
	if msg.Attributes == nil {
		msg.Attributes = make(map[string]string)
	}
	if err := tracer.Inject(span.Context(), tracer.TextMapCarrier(msg.Attributes)); err != nil {
		tr.instr.Logger().Debug("contrib/cloud.google.com/go/pubsubtrace: failed injecting tracing attributes: %s", err.Error())
	}
	span.SetTag(ext.PubsubNumAttributes, len(msg.Attributes))

	var once sync.Once
	closeSpan := func(serverID string, err error) {
		once.Do(func() {
			span.SetTag(ext.PubsubServerID, serverID)
			span.SetTag(ext.MessagingMessageID, serverID)
			span.Finish(tracer.WithError(err))
		})
	}
	return ctx, closeSpan
}

func (tr *Tracer) TraceReceiveFunc(s Subscription, opts ...Option) func(ctx context.Context, msg *Message) (context.Context, func()) {
	cfg := tr.defaultConfig()
	for _, opt := range opts {
		opt.apply(cfg)
	}
	tr.instr.Logger().Debug("contrib/cloud.google.com/go/pubsubtrace: Wrapping Receive Handler: %#v", cfg)
	return func(ctx context.Context, msg *Message) (context.Context, func()) {
		parentSpanCtx, _ := tracer.Extract(tracer.TextMapCarrier(msg.Attributes))
		opts := []tracer.StartSpanOption{
			tracer.ResourceName(s.String()),
			tracer.SpanType(ext.SpanTypeMessageConsumer),
			tracer.Tag(ext.PubsubMessageSize, len(msg.Data)),
			tracer.Tag(ext.PubsubNumAttributes, len(msg.Attributes)),
			tracer.Tag(ext.PubsubOrderingKey, msg.OrderingKey),
			tracer.Tag(ext.PubsubMessageID, msg.ID),
			tracer.Tag(ext.MessagingMessageID, msg.ID),
			tracer.Tag(ext.PubsubPublishTime, msg.PublishTime.String()),
			tracer.Tag(ext.Component, tr.component),
			tracer.Tag(ext.SpanKind, ext.SpanKindConsumer),
			tracer.Tag(ext.MessagingSystem, ext.MessagingSystemGCPPubsub),
			tracer.Tag(ext.MessagingOperationName, "receive"),
			tracer.Tag(ext.MessagingDestinationName, s.String()),
		}
		var baggage map[string]string
		if cfg.propagationAsSpanLinks && parentSpanCtx != nil {
			var links []tracer.SpanLink

			// Distinguish a normal extracted context from an stub formed in the case of DD_TRACE_PROPAGATION_BEHAVIOR_EXTRACT=restart,
			// which zeroes trace/span IDs and stores the producer link in SpanLinks.
			if parentSpanCtx.TraceIDLower() != 0 || parentSpanCtx.TraceIDUpper() != 0 || parentSpanCtx.SpanID() != 0 {
				// Link to the producer without reparenting the consumer trace.
				links = []tracer.SpanLink{{
					TraceID:     parentSpanCtx.TraceIDLower(),
					TraceIDHigh: parentSpanCtx.TraceIDUpper(),
					SpanID:      parentSpanCtx.SpanID(),
				}}
			} else {
				// extract=restart zeroes IDs; reuse links already on the stub context.
				links = parentSpanCtx.SpanLinks()
			}
			if len(links) > 0 {
				opts = append(opts, tracer.WithSpanLinks(links))
			}

			// copy any baggage into the new span without reparenting
			baggage = make(map[string]string)
			parentSpanCtx.ForeachBaggageItem(func(k, v string) bool {
				baggage[k] = v
				return true
			})
		} else {
			opts = append(opts, tracer.ChildOf(parentSpanCtx))
			// If there are span links as a result of context extraction, add them as a StartSpanOption
			if parentSpanCtx != nil && parentSpanCtx.SpanLinks() != nil {
				opts = append(opts, tracer.WithSpanLinks(parentSpanCtx.SpanLinks()))
			}
		}
		if projectID := projectIDFromResourceName(s.String()); projectID != "" {
			opts = append(opts, tracer.Tag(ext.GCPProjectID, projectID))
		}
		if cfg.serviceName != "" {
			opts = append(opts, instrumentation.ServiceNameWithSource(cfg.serviceName, cfg.serviceSource))
		}
		if cfg.measured {
			opts = append(opts, tracer.Measured())
		}
		span, ctx := tracer.StartSpanFromContext(ctx, cfg.receiveSpanName, opts...)
		for k, v := range baggage {
			span.SetBaggageItem(k, v)
		}
		if msg.DeliveryAttempt != nil {
			span.SetTag(ext.PubsubDeliveryAttempt, *msg.DeliveryAttempt)
		}
		return ctx, func() { span.Finish() }
	}
}

// TraceAdmin starts a span for a Pub/Sub admin operation (e.g. CreateTopic, ListSubscriptions, DeleteSchema).
// It is driven by the unary client interceptor in admin.go / admin_v1.go, which is the single source of
// truth for the (method, resourcePath) mapping across both the manual and orchestrion instrumentation.
func (tr *Tracer) TraceAdmin(ctx context.Context, method string, resourcePath string, opts ...Option) (context.Context, func(err error)) {
	cfg := tr.defaultConfig()
	for _, opt := range opts {
		opt.apply(cfg)
	}
	resource := method
	if resourcePath != "" {
		resource = method + " " + resourcePath
	}
	spanOpts := []tracer.StartSpanOption{
		tracer.ResourceName(resource),
		tracer.SpanType(ext.SpanTypeWorker),
		tracer.Tag(ext.Component, tr.component),
		tracer.Tag(ext.SpanKind, ext.SpanKindClient),
		tracer.Tag(ext.MessagingSystem, ext.MessagingSystemGCPPubsub),
		tracer.Tag("pubsub.method", method),
		tracer.Measured(),
	}
	if projectID := projectIDFromResourceName(resourcePath); projectID != "" {
		spanOpts = append(spanOpts, tracer.Tag(ext.GCPProjectID, projectID))
	}
	if cfg.serviceName != "" {
		spanOpts = append(spanOpts, instrumentation.ServiceNameWithSource(cfg.serviceName, cfg.serviceSource))
	}

	span, ctx := tracer.StartSpanFromContext(ctx, cfg.requestSpanName, spanOpts...)

	var once sync.Once
	finish := func(err error) {
		once.Do(func() {
			span.Finish(tracer.WithError(err))
		})
	}
	return ctx, finish
}

// extracts the GCP project ID from a Pubsub resource name starting with
// "projects/{project}. e.g. schemas, snapshots, topics and subscriptions
func projectIDFromResourceName(name string) string {
	const prefix = "projects/"
	if !strings.HasPrefix(name, prefix) {
		return ""
	}
	rest := name[len(prefix):]
	project, _, _ := strings.Cut(rest, "/")
	return project
}
