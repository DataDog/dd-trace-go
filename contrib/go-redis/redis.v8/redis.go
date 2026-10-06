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
	"runtime"
	"strconv"
	"strings"
	"sync"
	"weak"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/instrumentation"

	"github.com/go-redis/redis/v8"
)

const componentName = "go-redis/redis.v8"

var instr *instrumentation.Instrumentation

func init() {
	instr = instrumentation.Load(instrumentation.PackageGoRedisV8)
}

// traceMarkerKey is a private context key under which the datadog hook that
// started a command's span records itself. A WithContext or WithTimeout
// clone of an already-wrapped client inherits the hook, and go-redis clones
// share the hook slice with the original, so wrapping the clone adds a
// second datadog hook to it. The outermost datadog hook owns the span: every
// hook that finds the marker in the context skips the command, so each
// command is traced exactly once no matter how many datadog hooks the client
// carries.
type traceMarkerKey struct{}

// wrapEntry is the registry record for one instrumented client. It holds no
// reference to the client itself, so a weakly keyed entry never keeps a
// retired client alive.
type wrapEntry struct {
	cfg  *clientConfig
	done chan struct{} // closed once the winning call has installed its hook
}

var (
	// wrapMu guards wrapped.
	wrapMu sync.Mutex
	// wrapped records every client WrapClient has instrumented, keyed
	// weakly by the client. Weak keys do not pin clients: once a wrapped
	// client becomes unreachable, a runtime cleanup drops its entry, so the
	// registry holds at most one entry per live client. Without the registry,
	// every WrapClient call would add another hook to the client and every
	// Redis command would emit one duplicate span per extra hook.
	wrapped = map[any]*wrapEntry{} // weak.Pointer[T] (client) -> *wrapEntry
)

// registerWrapped instruments the client identified by key at most once:
// cfg is the configuration to use when this call wins the registration race
// and installHook installs the tracing hook. Concurrent first calls elect a
// single installer and the others wait for it, so tracing is active by the
// time every call returns.
func registerWrapped[T any](key weak.Pointer[T], cfg *clientConfig, installHook func()) {
	wrapMu.Lock()
	if e, ok := wrapped[key]; ok {
		wrapMu.Unlock()
		if !sameConfig(e.cfg, cfg) {
			instr.Logger().Warn("contrib/go-redis/redis.v8: WrapClient called more than once on the same client; keeping the first configuration")
		}
		// Wait for the winning call to install its hook before returning.
		<-e.done
		return
	}
	e := &wrapEntry{cfg: cfg, done: make(chan struct{})}
	wrapped[key] = e
	wrapMu.Unlock()
	runtime.AddCleanup(key.Value(), func(k any) {
		wrapMu.Lock()
		delete(wrapped, k)
		wrapMu.Unlock()
	}, any(key))
	defer close(e.done)
	installHook()
}

// sameConfig reports whether two configurations produce the same spans. The
// error-check function is not comparable and is ignored: with differing
// functions the first configuration is kept without a warning.
func sameConfig(a, b *clientConfig) bool {
	analytics := a.analyticsRate == b.analyticsRate ||
		(math.IsNaN(a.analyticsRate) && math.IsNaN(b.analyticsRate))
	return analytics &&
		a.serviceName == b.serviceName &&
		a.serviceSource == b.serviceSource &&
		a.spanName == b.spanName &&
		a.skipRaw == b.skipRaw
}

type datadogHook struct {
	*params
}

// params holds the tracer and a set of parameters which are recorded with every trace.
type params struct {
	config *clientConfig
	// spanCfg holds the tags that are constant for every command/pipeline
	// traced through this client (component, span kind, db system, service
	// name, analytics rate, and the additional host/port/db or cluster addrs
	// tags). It is built once in WrapClient and merged into each request via
	// WithStartSpanConfig, instead of rebuilding a Tag() closure per tag and
	// re-appending additionalTags on every call.
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
// the service name "redis". Calling it more than once on the same client, or on a
// WithContext or WithTimeout clone of an already-wrapped client, is safe: each
// command is traced exactly once and the configuration of the first call is kept.
func WrapClient(client redis.UniversalClient, opts ...ClientOption) {
	cfg := new(clientConfig)
	defaults(cfg)
	for _, fn := range opts {
		fn.apply(cfg)
	}
	installHook := func() {
		hookParams := &params{
			config: cfg,
		}
		hookParams.spanCfg = newSpanConfig(cfg, additionalTagOptions(client))
		client.AddHook(&datadogHook{params: hookParams})
	}
	// The registry is keyed by the client itself, not by its Options()
	// pointer: go-redis stores the caller's options pointer, so two
	// independent clients built from one shared *redis.Options would collide
	// and the second client would silently go uninstrumented. Missing spans
	// are worse than duplicate spans.
	switch c := client.(type) {
	case *redis.Client:
		registerWrapped(weak.Make(c), cfg, installHook)
	case *redis.ClusterClient:
		registerWrapped(weak.Make(c), cfg, installHook)
	case *redis.Ring:
		registerWrapped(weak.Make(c), cfg, installHook)
	default:
		// Unknown UniversalClient implementation: it cannot be keyed in the
		// registry, so instrument it directly on every call. The context
		// marker still keeps every command single-span across such calls.
		installHook()
	}
}

// newSpanConfig builds the base StartSpanConfig holding the tags that stay
// constant for every command/pipeline traced through a client with the given
// config and additional (host/port/db, or cluster addrs) tags, so per-command
// calls don't need to rebuild them.
func newSpanConfig(cfg *clientConfig, additionalTags []tracer.StartSpanOption) *tracer.StartSpanConfig {
	opts := []tracer.StartSpanOption{
		tracer.SpanType(ext.SpanTypeRedis),
		instrumentation.ServiceNameWithSource(cfg.serviceName, cfg.serviceSource),
		tracer.Tag(ext.Component, componentName),
		tracer.Tag(ext.SpanKind, ext.SpanKindClient),
		tracer.Tag(ext.DBSystem, ext.DBSystemRedis),
	}
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
				tracer.Tag(ext.RedisDatabaseIndex, opt.DB),
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
				tracer.Tag(ext.RedisDatabaseIndex, opt.DB),
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
	return additionalTags
}

func (ddh *datadogHook) BeforeProcess(ctx context.Context, cmd redis.Cmder) (context.Context, error) {
	if _, ok := ctx.Value(traceMarkerKey{}).(*datadogHook); ok {
		// Another datadog hook already started this command's span; see traceMarkerKey.
		return ctx, nil
	}
	raw := strings.TrimSpace(cmd.String())
	first := strings.SplitN(raw, " ", 2)[0]
	length := strings.Count(raw, " ") + 1
	p := ddh.params
	tags := map[string]any{
		ext.ResourceName:    first,
		"redis.args_length": strconv.Itoa(length),
	}
	if !p.config.skipRaw {
		tags["redis.raw_command"] = raw
	}
	_, ctx = tracer.StartSpanFromContext(ctx, p.config.spanName,
		tracer.WithTags(tags),
		tracer.WithStartSpanConfig(p.spanCfg),
	)
	return context.WithValue(ctx, traceMarkerKey{}, ddh), nil
}

func (ddh *datadogHook) AfterProcess(ctx context.Context, cmd redis.Cmder) error {
	// go-redis hands the final context to every hook's AfterProcess, so only
	// the hook that started the span finishes it; see traceMarkerKey.
	if owner, ok := ctx.Value(traceMarkerKey{}).(*datadogHook); !ok || owner != ddh {
		return nil
	}
	var span *tracer.Span
	span, _ = tracer.SpanFromContext(ctx)
	var finishOpts []tracer.FinishOption
	errRedis := cmd.Err()
	if errRedis != redis.Nil && ddh.config.errCheck(errRedis) {
		finishOpts = append(finishOpts, tracer.WithError(errRedis))
	}
	span.Finish(finishOpts...)
	return nil
}

func (ddh *datadogHook) BeforeProcessPipeline(ctx context.Context, cmds []redis.Cmder) (context.Context, error) {
	if _, ok := ctx.Value(traceMarkerKey{}).(*datadogHook); ok {
		// Another datadog hook already started this pipeline's span; see traceMarkerKey.
		return ctx, nil
	}
	raw := strings.TrimSpace(commandsToString(cmds))
	first := strings.SplitN(raw, " ", 2)[0]
	length := strings.Count(raw, " ") + 1
	p := ddh.params
	tags := map[string]any{
		ext.ResourceName:        first,
		"redis.args_length":     strconv.Itoa(length),
		"redis.pipeline_length": strconv.Itoa(len(cmds)),
	}
	if !p.config.skipRaw {
		tags["redis.raw_command"] = raw
	}
	_, ctx = tracer.StartSpanFromContext(ctx, p.config.spanName,
		tracer.WithTags(tags),
		tracer.WithStartSpanConfig(p.spanCfg),
	)
	return context.WithValue(ctx, traceMarkerKey{}, ddh), nil
}

func (ddh *datadogHook) AfterProcessPipeline(ctx context.Context, cmds []redis.Cmder) error {
	// go-redis hands the final context to every hook's AfterProcessPipeline,
	// so only the hook that started the span finishes it; see traceMarkerKey.
	if owner, ok := ctx.Value(traceMarkerKey{}).(*datadogHook); !ok || owner != ddh {
		return nil
	}
	var span *tracer.Span
	span, _ = tracer.SpanFromContext(ctx)
	var finishOpts []tracer.FinishOption
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
