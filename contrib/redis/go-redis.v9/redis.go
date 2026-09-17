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

	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/instrumentation"

	"github.com/redis/go-redis/v9"
)

var instr *instrumentation.Instrumentation

func init() {
	instr = instrumentation.Load(instrumentation.PackageRedisGoRedisV9)
}

type datadogHook struct {
	*params
}

// params holds the tracer and a set of parameters which are recorded with every trace.
type params struct {
	config *clientConfig
	// spanCfg holds the tags that are constant for every dial/command/
	// pipeline traced through this client (service name, analytics rate, and
	// the additionalTagOptions tags: component, span kind, db system, and
	// the host/port/db or cluster addrs tags). It is built once in
	// WrapClient and merged into each request via WithStartSpanConfig,
	// instead of rebuilding ServiceNameWithSource and re-appending
	// additionalTags on every call.
	spanCfg *tracer.StartSpanConfig
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
		fn.apply(cfg)
	}

	hookParams := &params{
		config: cfg,
	}
	hookParams.spanCfg = newSpanConfig(cfg, additionalTagOptions(client))

	client.AddHook(&datadogHook{params: hookParams})
}

// newSpanConfig builds the base StartSpanConfig holding the tags that stay
// constant for every dial/command/pipeline traced through a client with the
// given config and additional (component/span kind/db system plus host/
// port/db or cluster addrs) tags, so per-call hooks don't need to rebuild
// them.
func newSpanConfig(cfg *clientConfig, additionalTags []tracer.StartSpanOption) *tracer.StartSpanConfig {
	opts := []tracer.StartSpanOption{instrumentation.ServiceNameWithSource(cfg.serviceName, cfg.serviceSource)}
	opts = append(opts, additionalTags...)
	if !math.IsNaN(cfg.analyticsRate) {
		opts = append(opts, tracer.Tag(ext.EventSampleRate, cfg.analyticsRate))
	}
	return tracer.NewStartSpanConfig(opts...)
}

type clientOptions interface {
	Options() *redis.Options
}

type clusterOptions interface {
	Options() *redis.ClusterOptions
}

func additionalTagOptions(client redis.UniversalClient) []tracer.StartSpanOption {
	additionalTags := []tracer.StartSpanOption{}
	if clientOptions, ok := client.(clientOptions); ok {
		opt := clientOptions.Options()
		if opt.Addr == "FailoverClient" {
			additionalTags = []tracer.StartSpanOption{
				tracer.Tag(ext.TargetDB, strconv.Itoa(opt.DB)),
			}
		} else {
			host, port, err := net.SplitHostPort(opt.Addr)
			if err != nil {
				host = opt.Addr
				port = "6379"
			}
			additionalTags = []tracer.StartSpanOption{
				tracer.Tag(ext.TargetHost, host),
				tracer.Tag(ext.TargetPort, port),
				tracer.Tag(ext.TargetDB, strconv.Itoa(opt.DB)),
			}
		}
	} else if clientOptions, ok := client.(clusterOptions); ok {
		addrs := []string{}
		for _, addr := range clientOptions.Options().Addrs {
			addrs = append(addrs, addr)
		}
		additionalTags = []tracer.StartSpanOption{
			tracer.Tag("addrs", strings.Join(addrs, ", ")),
		}
	}
	additionalTags = append(additionalTags,
		tracer.SpanType(ext.SpanTypeRedis),
		tracer.Tag(ext.Component, instrumentation.PackageRedisGoRedisV9),
		tracer.Tag(ext.SpanKind, ext.SpanKindClient),
		tracer.Tag(ext.DBSystem, ext.DBSystemRedis),
	)
	return additionalTags
}

func (ddh *datadogHook) DialHook(hook redis.DialHook) redis.DialHook {
	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		// Every tag DialHook sets is static (constant for the client's
		// lifetime), so the span can start from spanCfg alone, with no
		// per-call tag map.
		span, ctx := tracer.StartSpanFromContext(ctx, "redis.dial", tracer.WithStartSpanConfig(ddh.spanCfg))

		conn, err := hook(ctx, network, addr)

		var finishOpts []tracer.FinishOption
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
		tags := map[string]any{
			ext.ResourceName:    raw[:strings.IndexByte(raw, ' ')],
			"redis.args_length": strconv.Itoa(length),
		}
		if !p.config.skipRaw {
			tags["redis.raw_command"] = raw
		}
		span, ctx := tracer.StartSpanFromContext(ctx, p.config.spanName,
			tracer.WithTags(tags),
			tracer.WithStartSpanConfig(p.spanCfg),
		)

		err := hook(ctx, cmd)

		var finishOpts []tracer.FinishOption
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
		tags := map[string]any{
			ext.ResourceName:        "redis.pipeline",
			"redis.pipeline_length": strconv.Itoa(len(cmds)),
		}
		if !p.config.skipRaw {
			tags["redis.raw_command"] = commandsToString(cmds)
		}
		span, ctx := tracer.StartSpanFromContext(ctx, p.config.spanName,
			tracer.WithTags(tags),
			tracer.WithStartSpanConfig(p.spanCfg),
		)

		err := hook(ctx, cmds)

		var finishOpts []tracer.FinishOption
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
