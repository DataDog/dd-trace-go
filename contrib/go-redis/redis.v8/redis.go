// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-2020 Datadog, Inc.

// Package redis provides tracing functions for tracing the go-redis/redis package (https://github.com/go-redis/redis).
// This package supports versions up to go-redis 6.15.
package redis

import (
	"bytes"
	"context"
	"fmt"
	"math"
	"net"
	"strconv"
	"strings"

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"

	"github.com/go-redis/redis/v8"
)

type dataDogHook struct {
	*params
}

// params holds the tracer and a set of parameters which are recorded with every trace.
type params struct {
	host   string
	port   string
	db     string
	config *clientConfig
}

// NewClient returns a new Client that is traced with the default tracer under
// the service name "redis".
func NewClient(opt *redis.Options, opts ...ClientOption) redis.UniversalClient {
	cfg := new(clientConfig)
	defaults(cfg)
	for _, fn := range opts {
		fn(cfg)
	}
	host, port, err := net.SplitHostPort(opt.Addr)
	if err != nil {
		host = opt.Addr
		port = "6379"
	}
	params := &params{
		host:   host,
		port:   port,
		db:     strconv.Itoa(opt.DB),
		config: cfg,
	}
	client := redis.NewClient(opt)
	hook := &dataDogHook{params: params}
	client.AddHook(hook)
	return client
}

func (ddh *dataDogHook) BeforeProcess(ctx context.Context, cmd redis.Cmder) (context.Context, error) {
	raw := cmderToString(cmd)
	parts := strings.Split(raw, " ")
	length := len(parts) - 1
	p := ddh.params
	opts := []ddtrace.StartSpanOption{
		tracer.SpanType(ext.SpanTypeRedis),
		tracer.ServiceName(p.config.serviceName),
		tracer.ResourceName(parts[0]),
		tracer.Tag(ext.TargetHost, p.host),
		tracer.Tag(ext.TargetPort, p.port),
		tracer.Tag("out.db", p.db),
		tracer.Tag("redis.raw_command", raw),
		tracer.Tag("redis.args_length", strconv.Itoa(length)),
	}
	if !math.IsNaN(p.config.analyticsRate) {
		opts = append(opts, tracer.Tag(ext.EventSampleRate, p.config.analyticsRate))
	}
	_, ctx = tracer.StartSpanFromContext(ctx, "redis.command", opts...)
	return ctx, nil
}

func (ddh *dataDogHook) AfterProcess(ctx context.Context, cmd redis.Cmder) error {
	var span tracer.Span
	span, _ = tracer.SpanFromContext(ctx)
	var finishOpts []ddtrace.FinishOption
	errRedis := cmd.Err()
	if errRedis != redis.Nil {
		finishOpts = append(finishOpts, tracer.WithError(errRedis))
	}
	span.Finish(finishOpts...)
	return nil
}

func (ddh *dataDogHook) BeforeProcessPipeline(ctx context.Context, cmds []redis.Cmder) (context.Context, error) {
	raw := commandsToString(cmds)
	parts := strings.Split(raw, " ")
	length := len(parts) - 1
	p := ddh.params
	opts := []ddtrace.StartSpanOption{
		tracer.SpanType(ext.SpanTypeRedis),
		tracer.ServiceName(p.config.serviceName),
		tracer.ResourceName(parts[0]),
		tracer.Tag(ext.TargetHost, p.host),
		tracer.Tag(ext.TargetPort, p.port),
		tracer.Tag("out.db", p.db),
		tracer.Tag("redis.raw_command", raw),
		tracer.Tag("redis.args_length", strconv.Itoa(length)),
		tracer.Tag(ext.ResourceName, raw),
		tracer.Tag("redis.pipeline_length", strconv.Itoa(len(cmds))),
	}
	if !math.IsNaN(p.config.analyticsRate) {
		opts = append(opts, tracer.Tag(ext.EventSampleRate, p.config.analyticsRate))
	}
	_, ctx = tracer.StartSpanFromContext(ctx, "redis.command", opts...)
	return ctx, nil
}

func (ddh *dataDogHook) AfterProcessPipeline(ctx context.Context, cmds []redis.Cmder) error {
	var span tracer.Span
	span, _ = tracer.SpanFromContext(ctx)
	var finishOpts []ddtrace.FinishOption
	for _, cmd := range cmds {
		errCmd := cmd.Err()
		if errCmd != redis.Nil {
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
		b.WriteString(cmderToString(cmd))
		b.WriteString("\n")
	}
	return b.String()
}

func cmderToString(cmd redis.Cmder) string {
	// We want to support multiple versions of the go-redis library. In
	// older versions Cmder implements the Stringer interface, while in
	// newer versions that was removed, and this String method which
	// sometimes returns an error is used instead. By doing a type assertion
	// we can support both versions.
	if stringer, ok := cmd.(fmt.Stringer); ok {
		return stringer.String()
	}

	args := cmd.Args()
	if len(args) == 0 {
		return ""
	}
	if str, ok := args[0].(string); ok {
		return str
	}
	return ""
}
