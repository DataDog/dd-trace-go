// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2021 Datadog, Inc.

// Package nats provides functions to trace the github.com/nats-io/nats.go package.
package nats

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"

	"github.com/nats-io/nats.go"
)

type Conn struct {
	*nats.Conn
	cfg wrapConfig
}

type Subscription struct {
	*nats.Subscription
	cfg wrapConfig
}

type Msg struct {
	*nats.Msg
	cfg wrapConfig
}

// WrapConn wraps a *nats.Conn so that all requests are traced using the
// default tracer with the service name "nats".
func WrapConn(conn *nats.Conn, opts ...WrapOption) (c *Conn) {
	cfg := newWrapConfig()
	for _, opt := range opts {
		opt(&cfg)
	}

	return &Conn{
		Conn: conn,
		cfg:  cfg,
	}
}

// WrapSubscription wraps a *nats.Subscription so that all requests are traced using the
// default tracer with the service name "nats".
func (c *Conn) WrapSubscription(subscription *nats.Subscription) (s *Subscription) {
	return &Subscription{
		Subscription: subscription,
		cfg:          c.cfg,
	}
}

// startSpanFromContext starts a span from a context.
func startSpanFromContext(ctx context.Context, resourceName, serviceName string) ddtrace.Span {
	span, _ := tracer.StartSpanFromContext(ctx, operationName, []ddtrace.StartSpanOption{
		tracer.SpanType(ext.AppTypeRPC),
		tracer.ServiceName(serviceName),
		tracer.ResourceName(resourceName),
	}...)
	return span
}

// StartSpanFromMsg starts a span based on a Msg headers.
func StartSpanFromMsg(msg *Msg, resourceName string) ddtrace.Span {
	traceHeaders := map[string]string{}
	re := regexp.MustCompile(`^_dd_apm_`)
	for k, v := range msg.Header {
		fmt.Println(k, v)
		if re.MatchString(k) {
			traceHeaders[strings.Replace(k, "_dd_apm_", "", 1)] = strings.Join(v, ",")
		}
	}

	parentSpan, err := tracer.Extract(tracer.TextMapCarrier(traceHeaders))
	if err != nil {
		// Start a span from a new context instead
		span, _ := tracer.StartSpanFromContext(context.Background(), operationName, []ddtrace.StartSpanOption{
			tracer.SpanType(ext.AppTypeRPC),
			tracer.ServiceName(msg.cfg.serviceName),
			tracer.ResourceName(resourceName),
		}...)
		return span
	}

	return tracer.StartSpan(
		operationName,
		tracer.SpanType(ext.AppTypeRPC),
		tracer.ServiceName(msg.cfg.serviceName),
		tracer.ResourceName(resourceName),
		tracer.ChildOf(parentSpan),
	)
}

// injectTracingHeadersIntoNatsMsg adds span tracing context into a *nats.Msg as headers.
func injectTracingHeadersIntoNatsMsg(span ddtrace.Span, msg *nats.Msg) (err error) {
	traceHeaders := map[string]string{}
	if err = tracer.Inject(span.Context(), tracer.TextMapCarrier(traceHeaders)); err != nil {
		return
	}

	if msg.Header == nil {
		msg.Header = nats.Header{}
	}

	for k, v := range traceHeaders {
		msg.Header.Add(fmt.Sprintf("_dd_apm_%s", k), v)
	}
	return
}

// PublishMsg invokes and traces *nats.Conn.PublishMsg().
func (c *Conn) PublishMsg(ctx context.Context, msg *nats.Msg) (err error) {
	span := startSpanFromContext(ctx, "publish", c.cfg.serviceName)
	defer span.Finish(tracer.WithError(err))

	_ = injectTracingHeadersIntoNatsMsg(span, msg)
	err = c.Conn.PublishMsg(msg)
	return
}

// RequestMsg invokes and traces *nats.Conn.RequestMsg().
func (c *Conn) RequestMsg(ctx context.Context, msg *nats.Msg, timeout time.Duration) (resp *Msg, err error) {
	span := startSpanFromContext(ctx, "request", c.cfg.serviceName)
	defer span.Finish(tracer.WithError(err))

	_ = injectTracingHeadersIntoNatsMsg(span, msg)
	resp = &Msg{cfg: c.cfg}
	resp.Msg, err = c.Conn.RequestMsg(msg, timeout)
	return
}

// RequestMsgWithContext invokes and traces *nats.Conn.RequestMsgWithContext().
func (c *Conn) RequestMsgWithContext(ctx context.Context, msg *nats.Msg) (resp *Msg, err error) {
	span := startSpanFromContext(ctx, "request", c.cfg.serviceName)
	defer span.Finish(tracer.WithError(err))

	_ = injectTracingHeadersIntoNatsMsg(span, msg)
	resp = &Msg{cfg: c.cfg}
	resp.Msg, err = c.Conn.RequestMsgWithContext(ctx, msg)
	return
}

// SubscribeSync invokes *nats.Conn.SubscribeSync() and returns a traceable *Subscription.
func (c *Conn) SubscribeSync(subj string) (s *Subscription, err error) {
	var subscription *nats.Subscription
	subscription, err = c.Conn.SubscribeSync(subj)
	s = c.WrapSubscription(subscription)
	return
}

// RespondMsg invokes and traces *nats.Msg.RespondMsg().
func (m *Msg) RespondMsg(msg *nats.Msg) (err error) {
	span := StartSpanFromMsg(m, "msg.respond")
	defer span.Finish(tracer.WithError(err))

	_ = injectTracingHeadersIntoNatsMsg(span, msg)
	err = m.Msg.RespondMsg(msg)
	return
}

// Fetch invokes and traces *nats.Subscription.Fetch().
func (s *Subscription) Fetch(ctx context.Context, batch int, opts ...nats.PullOpt) (msgs []*Msg, err error) {
	span := startSpanFromContext(ctx, "subscription.fetch", s.cfg.serviceName)
	defer span.Finish(tracer.WithError(err))

	var fetchedMsgs []*nats.Msg
	fetchedMsgs, err = s.Subscription.Fetch(batch, opts...)
	for _, msg := range fetchedMsgs {
		msgs = append(msgs, &Msg{
			Msg: msg,
			cfg: s.cfg,
		})
	}

	return
}

// NextMsg invokes and traces *nats.Subscription.NextMsg().
func (s *Subscription) NextMsg(ctx context.Context, timeout time.Duration) (msg *Msg, err error) {
	span := startSpanFromContext(ctx, "subscription.nextmsg", s.cfg.serviceName)
	defer span.Finish(tracer.WithError(err))

	var nextMsg *nats.Msg
	nextMsg, err = s.Subscription.NextMsg(timeout)
	msg = &Msg{
		Msg: nextMsg,
		cfg: s.cfg,
	}
	return
}
