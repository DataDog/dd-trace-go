// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

// Package redis provides functions to trace the redis/go-redis package (https://github.com/redis/go-redis).
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

	"github.com/redis/go-redis/v9"
)

const componentName = "redis/go-redis.v9"

func init() {
	telemetry.LoadIntegration(componentName)
	tracer.MarkIntegrationImported("github.com/redis/go-redis/v9")
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
func NewClient(opt *redis.Options, opts ...ClientOption) redis.UniversalClient {
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
	additionalTags = append(additionalTags,
		tracer.SpanType(ext.SpanTypeRedis),
		tracer.Tag(ext.Component, componentName),
		tracer.Tag(ext.SpanKind, ext.SpanKindClient),
		tracer.Tag(ext.DBSystem, ext.DBSystemRedis),
	)
	return additionalTags
}

func (ddh *datadogHook) DialHook(hook redis.DialHook) redis.DialHook {
	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		p := ddh.params
		startOpts := make([]ddtrace.StartSpanOption, 0, 1+len(ddh.additionalTags)+1) // serviceName + ddh.additionalTags + analyticsRate
		startOpts = append(startOpts, tracer.ServiceName(p.config.serviceName))
		startOpts = append(startOpts, ddh.additionalTags...)
		if !math.IsNaN(p.config.analyticsRate) {
			startOpts = append(startOpts, tracer.Tag(ext.EventSampleRate, p.config.analyticsRate))
		}
		span, ctx := tracer.StartSpanFromContext(ctx, "redis.dial", startOpts...)

		conn, err := hook(ctx, network, addr)

		var finishOpts []ddtrace.FinishOption
		if err != nil {
			finishOpts = append(finishOpts, tracer.WithError(err))
		}
		span.Finish(finishOpts...)
		return conn, err
	}
}

func (ddh *datadogHook) ProcessHook(hook redis.ProcessHook) redis.ProcessHook {
	return func(ctx context.Context, cmd redis.Cmder) error {
		raw := cmd.String()
		length := strings.Count(raw, " ")
		p := ddh.params
		startOpts := make([]ddtrace.StartSpanOption, 0, 3+1+len(ddh.additionalTags)+1) // 3 options below + redis.raw_command + ddh.additionalTags + analyticsRate
		startOpts = append(startOpts,
			tracer.ServiceName(p.config.serviceName),
			tracer.ResourceName(raw[:strings.IndexByte(raw, ' ')]),
			tracer.Tag("redis.args_length", strconv.Itoa(length)),
		)
		if !p.config.skipRaw {
			startOpts = append(startOpts, tracer.Tag("redis.raw_command", raw))
		}
		startOpts = append(startOpts, ddh.additionalTags...)
		if !math.IsNaN(p.config.analyticsRate) {
			startOpts = append(startOpts, tracer.Tag(ext.EventSampleRate, p.config.analyticsRate))
		}
		span, ctx := tracer.StartSpanFromContext(ctx, p.config.spanName, startOpts...)

		err := hook(ctx, cmd)

		var finishOpts []ddtrace.FinishOption
		if err != nil && err != redis.Nil && ddh.config.errCheck(err) {
			finishOpts = append(finishOpts, tracer.WithError(err))
		}
		span.Finish(finishOpts...)
		return err
	}
}

func (ddh *datadogHook) ProcessPipelineHook(hook redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return func(ctx context.Context, cmds []redis.Cmder) error {
		p := ddh.params
		startOpts := make([]ddtrace.StartSpanOption, 0, 3+1+len(ddh.additionalTags)+1) // 3 options below + redis.raw_command + ddh.additionalTags + analyticsRate
		startOpts = append(startOpts,
			tracer.ServiceName(p.config.serviceName),
			tracer.ResourceName("redis.pipeline"),
			tracer.Tag("redis.pipeline_length", strconv.Itoa(len(cmds))),
		)
		if !p.config.skipRaw {
			raw := commandsToString(cmds)
			startOpts = append(startOpts, tracer.Tag("redis.raw_command", raw))
		}
		startOpts = append(startOpts, ddh.additionalTags...)
		if !math.IsNaN(p.config.analyticsRate) {
			startOpts = append(startOpts, tracer.Tag(ext.EventSampleRate, p.config.analyticsRate))
		}
		span, ctx := tracer.StartSpanFromContext(ctx, p.config.spanName, startOpts...)

		err := hook(ctx, cmds)

		var finishOpts []ddtrace.FinishOption
		if err != nil && err != redis.Nil && ddh.config.errCheck(err) {
			finishOpts = append(finishOpts, tracer.WithError(err))
		}
		span.Finish(finishOpts...)
		return err
	}
}

// commandsToString returns a string representation of a slice of redis Commands, separated by newlines.
func commandsToString(cmds []redis.Cmder) string {
	var b bytes.Buffer
	for idx, cmd := range cmds {
		if idx > 0 {
			b.WriteString("\n")
		}
		b.WriteString(cmd.String())
	}
	return b.String()
}
