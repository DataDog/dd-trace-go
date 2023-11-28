// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

// Package redis provides tracing functions for tracing the go-redis/redis package (https://github.com/go-redis/redis).
// This package supports versions up to go-redis 6.15.
package redis

import (
	"bytes"
	"context"
	"math"
	"net"
	"strconv"
	"strings"

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/telemetry"

	"github.com/go-redis/redis/v7"
)

const componentName = "go-redis/redis.v7"

func init() {
	telemetry.LoadIntegration(componentName)
	tracer.MarkIntegrationImported("github.com/go-redis/redis/v7")
}

type datadogHook struct {
	*params
}

// params holds the tracer and a set of parameters which are recorded with every trace.
type params struct {
	config         *clientConfig
	additionalTags []ddtrace.StartSpanOption
}

// NewClient returns a new Client that is traced with the default tracer under
// the service name "redis".
func NewClient(opt *redis.Options, opts ...ClientOption) *redis.Client {
	client := redis.NewClient(opt)
	WrapClient(client, opts...)
	return client
}

// WrapClient adds a hook to the given client that traces with the default tracer under
// the service name "redis".
func WrapClient(client redis.UniversalClient, opts ...ClientOption) {
	cfg := new(clientConfig)
	defaults(cfg)
	for _, fn := range opts {
		fn(cfg)
	}

	hookParams := &params{
		additionalTags: additionalTagOptions(client),
		config:         cfg,
	}

	client.AddHook(&datadogHook{params: hookParams})
}

type clientOptions interface {
	Options() *redis.Options
}

type clusterOptions interface {
	Options() *redis.ClusterOptions
}

func additionalTagOptions(client redis.UniversalClient) []ddtrace.StartSpanOption {
	additionalTags := []ddtrace.StartSpanOption{}
	if clientOptions, ok := client.(clientOptions); ok {
		opt := clientOptions.Options()
		if opt.Addr == "FailoverClient" {
			additionalTags = []ddtrace.StartSpanOption{
				tracer.Tag("out.db", strconv.Itoa(opt.DB)),
				tracer.Tag(ext.RedisDatabaseIndex, opt.DB),
			}
		} else {
			host, port, err := net.SplitHostPort(opt.Addr)
			if err != nil {
				host = opt.Addr
				port = "6379"
			}
			additionalTags = []ddtrace.StartSpanOption{
				tracer.Tag(ext.TargetHost, host),
				tracer.Tag(ext.TargetPort, port),
				tracer.Tag("out.db", strconv.Itoa(opt.DB)),
				tracer.Tag(ext.RedisDatabaseIndex, opt.DB),
			}
		}
	} else if clientOptions, ok := client.(clusterOptions); ok {
		addrs := []string{}
		for _, addr := range clientOptions.Options().Addrs {
			addrs = append(addrs, addr)
		}
		additionalTags = []ddtrace.StartSpanOption{
			tracer.Tag("addrs", strings.Join(addrs, ", ")),
		}
	}
	return additionalTags
}

func (ddh *datadogHook) BeforeProcess(ctx context.Context, cmd redis.Cmder) (context.Context, error) {
	raw := cmd.String()
	parts := strings.Split(raw, " ")
	length := len(parts) - 1
	p := ddh.params
	opts := []ddtrace.StartSpanOption{
		tracer.SpanType(ext.SpanTypeRedis),
		tracer.ServiceName(p.config.serviceName),
		tracer.ResourceName(parts[0]),
		tracer.Tag("redis.raw_command", raw),
		tracer.Tag("redis.args_length", strconv.Itoa(length)),
		tracer.Tag(ext.Component, componentName),
		tracer.Tag(ext.SpanKind, ext.SpanKindClient),
		tracer.Tag(ext.DBSystem, ext.DBSystemRedis),
	}
	opts = append(opts, ddh.additionalTags...)
	if !math.IsNaN(p.config.analyticsRate) {
		opts = append(opts, tracer.Tag(ext.EventSampleRate, p.config.analyticsRate))
	}
	_, ctx = tracer.StartSpanFromContext(ctx, p.config.spanName, opts...)
	return ctx, nil
}

func (ddh *datadogHook) AfterProcess(ctx context.Context, cmd redis.Cmder) error {
	var span tracer.Span
	span, _ = tracer.SpanFromContext(ctx)
	var finishOpts []ddtrace.FinishOption
	errRedis := cmd.Err()
	if errRedis != redis.Nil && ddh.config.errCheck(errRedis) {
		finishOpts = append(finishOpts, tracer.WithError(errRedis))
	}
	span.Finish(finishOpts...)
	return nil
}

func (ddh *datadogHook) BeforeProcessPipeline(ctx context.Context, cmds []redis.Cmder) (context.Context, error) {
	raw := commandsToString(cmds)
	parts := strings.Split(raw, " ")
	length := len(parts) - 1
	p := ddh.params
	opts := []ddtrace.StartSpanOption{
		tracer.SpanType(ext.SpanTypeRedis),
		tracer.ServiceName(p.config.serviceName),
		tracer.ResourceName(parts[0]),
		tracer.Tag("redis.raw_command", raw),
		tracer.Tag("redis.args_length", strconv.Itoa(length)),
		tracer.Tag(ext.ResourceName, raw),
		tracer.Tag("redis.pipeline_length", strconv.Itoa(len(cmds))),
		tracer.Tag(ext.Component, componentName),
		tracer.Tag(ext.SpanKind, ext.SpanKindClient),
		tracer.Tag(ext.DBSystem, ext.DBSystemRedis),
	}
	opts = append(opts, ddh.additionalTags...)
	if !math.IsNaN(p.config.analyticsRate) {
		opts = append(opts, tracer.Tag(ext.EventSampleRate, p.config.analyticsRate))
	}
	_, ctx = tracer.StartSpanFromContext(ctx, "redis.command", opts...)
	return ctx, nil
}

func (ddh *datadogHook) AfterProcessPipeline(ctx context.Context, cmds []redis.Cmder) error {
	var span tracer.Span
	span, _ = tracer.SpanFromContext(ctx)
	var finishOpts []ddtrace.FinishOption
	for _, cmd := range cmds {
		errCmd := cmd.Err()
		if errCmd != redis.Nil && ddh.config.errCheck(errCmd) {
			finishOpts = append(finishOpts, tracer.WithError(errCmd))
		}
	}
	span.Finish(finishOpts...)
	return nil
}

// commandsToString returns a string representation of a slice of redis Commands, separated by newlines.
func commandsToString(cmds []redis.Cmder) string {
	var b bytes.Buffer
	for _, cmd := range cmds {
		b.WriteString(cmd.String())
		b.WriteString("\n")
	}
	return b.String()
}
