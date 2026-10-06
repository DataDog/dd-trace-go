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
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"weak"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/instrumentation"

	"github.com/go-redis/redis/v7"
)

const componentName = "go-redis/redis.v7"

var instr *instrumentation.Instrumentation

func init() {
	instr = instrumentation.Load(instrumentation.PackageGoRedisV7)
}

// traceMarkerKey is a private context key under which the datadog hook
// that started a command's span records itself, along with the command it
// started it for. A WithContext or WithTimeout clone of an already-wrapped
// client inherits the hook, and go-redis clones share the hook slice with
// the original, so wrapping the clone adds a second datadog hook to it.
// The outermost datadog hook owns the span: every hook that finds its own
// command's marker in the context skips it, so each command is traced
// exactly once no matter how many datadog hooks the client carries. The
// command in the marker keeps the check scoped to one command chain: a
// context handed to another wrapped client — for example by a user hook
// that issues a nested command with the context it received — does not
// suppress that client's span.
type traceMarkerKey struct{}

// traceMarker identifies the datadog hook that owns the span for one
// command. For pipelines, cmd is the first command of the slice.
type traceMarker struct {
	hook *datadogHook
	cmd  redis.Cmder
}

// wrapEntry is the registry record for one instrumented client. It stores
// only the scalar fields that identify the client's configuration — never
// the full clientConfig, whose error-check callback is a user closure that
// may capture the client and would pin it through the registry — and holds
// no reference to the client itself, so a weakly keyed entry never keeps a
// retired client alive.
type wrapEntry struct {
	cfg  configKey
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
		if !sameConfig(e.cfg, cfg.key()) {
			instr.Logger().Warn("contrib/go-redis/redis.v7: WrapClient called more than once on the same client; keeping the first configuration")
		}
		// Wait for the winning call to install its hook before returning.
		<-e.done
		return
	}
	e := &wrapEntry{cfg: cfg.key(), done: make(chan struct{})}
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

// configKey is the part of a client configuration that determines the spans
// a client produces. It excludes the error-check function: it is not
// comparable, and storing it in the registry would retain a user closure
// that may capture the client.
type configKey struct {
	serviceName   string
	serviceSource string
	spanName      string
	analyticsRate float64
	skipRaw       bool
}

// sameConfig reports whether two configurations produce the same spans.
// Two NaN analytics rates are equal: NaN != NaN made identical default
// configurations compare as different and fired spurious warnings. With
// differing error-check functions the first configuration is kept without
// a warning.
func sameConfig(a, b configKey) bool {
	analytics := a.analyticsRate == b.analyticsRate ||
		(math.IsNaN(a.analyticsRate) && math.IsNaN(b.analyticsRate))
	return analytics &&
		a.serviceName == b.serviceName &&
		a.serviceSource == b.serviceSource &&
		a.spanName == b.spanName &&
		a.skipRaw == b.skipRaw
}

func (cfg *clientConfig) key() configKey {
	return configKey{
		serviceName:   cfg.serviceName,
		serviceSource: cfg.serviceSource,
		spanName:      cfg.spanName,
		analyticsRate: cfg.analyticsRate,
		skipRaw:       cfg.skipRaw,
	}
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
func NewClient(opt *redis.Options, opts ...ClientOption) *redis.Client {
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
	if !registerConcrete(client, cfg, installHook) {
		// A decorator or proxy implementation. Key the registry by the
		// concrete go-redis client it embeds, when there is one: repeated
		// wraps, direct or through other decorators, then install a single
		// hook. Implementations we cannot see through are instrumented
		// directly on every call, which preserves the pre-existing behavior
		// for them.
		if u := underlyingClient(client); u == nil || !registerConcrete(u, cfg, installHook) {
			installHook()
		}
	}
}

// registerConcrete instruments a concrete go-redis client, keyed weakly by
// the client itself, and reports whether client is one.
func registerConcrete(client redis.UniversalClient, cfg *clientConfig, installHook func()) bool {
	switch c := client.(type) {
	case *redis.Client:
		registerWrapped(weak.Make(c), cfg, installHook)
	case *redis.ClusterClient:
		registerWrapped(weak.Make(c), cfg, installHook)
	case *redis.Ring:
		registerWrapped(weak.Make(c), cfg, installHook)
	default:
		return false
	}
	return true
}

// underlyingClient returns the concrete go-redis client behind a decorator or
// proxy built by embedding redis.UniversalClient, if there is one within a
// few levels of exported fields. It returns nil when it cannot see through
// the implementation.
func underlyingClient(client redis.UniversalClient) redis.UniversalClient {
	var walk func(c redis.UniversalClient, depth int) redis.UniversalClient
	walk = func(c redis.UniversalClient, depth int) redis.UniversalClient {
		if depth == 0 || c == nil {
			return nil
		}
		switch concrete := c.(type) {
		case *redis.Client, *redis.ClusterClient, *redis.Ring:
			return concrete
		}
		v := reflect.ValueOf(c)
		if v.Kind() != reflect.Pointer || v.IsNil() || v.Elem().Kind() != reflect.Struct {
			return nil
		}
		s := v.Elem()
		for i := 0; i < s.NumField(); i++ {
			if !s.Type().Field(i).IsExported() {
				continue
			}
			field, ok := s.Field(i).Interface().(redis.UniversalClient)
			if !ok {
				continue
			}
			if u := walk(field, depth-1); u != nil {
				return u
			}
		}
		return nil
	}
	return walk(client, 3)
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
	if m, ok := ctx.Value(traceMarkerKey{}).(traceMarker); ok && m.cmd == cmd {
		// Another datadog hook on this client already started this command's span; see traceMarkerKey.
		return ctx, nil
	}
	raw := cmd.String()
	parts := strings.Split(raw, " ")
	length := len(parts) - 1
	p := ddh.params
	tags := map[string]any{
		ext.ResourceName:    parts[0],
		"redis.args_length": strconv.Itoa(length),
	}
	if !p.config.skipRaw {
		tags["redis.raw_command"] = raw
	}
	_, ctx = tracer.StartSpanFromContext(ctx, p.config.spanName,
		tracer.WithTags(tags),
		tracer.WithStartSpanConfig(p.spanCfg),
	)
	return context.WithValue(ctx, traceMarkerKey{}, traceMarker{hook: ddh, cmd: cmd}), nil
}

func (ddh *datadogHook) AfterProcess(ctx context.Context, cmd redis.Cmder) error {
	// go-redis hands the final context to every hook's AfterProcess, so only
	// the hook that started this command's span finishes it; see traceMarkerKey.
	if m, ok := ctx.Value(traceMarkerKey{}).(traceMarker); !ok || m.hook != ddh || m.cmd != cmd {
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
	var first redis.Cmder
	if len(cmds) > 0 {
		first = cmds[0]
	}
	if m, ok := ctx.Value(traceMarkerKey{}).(traceMarker); ok && m.cmd == first {
		// Another datadog hook on this client already started this pipeline's span; see traceMarkerKey.
		return ctx, nil
	}
	raw := commandsToString(cmds)
	parts := strings.Split(raw, " ")
	length := len(parts) - 1
	p := ddh.params
	// The final resource name is raw (the joined pipeline), not parts[0]:
	// the original code set tracer.ResourceName(parts[0]) and then
	// unconditionally overwrote it with tracer.Tag(ext.ResourceName, raw)
	// later in the same option list, so only the raw value ever reached the
	// span. Preserve that by assigning the tag its final value directly.
	tags := map[string]any{
		ext.ResourceName:        raw,
		"redis.args_length":     strconv.Itoa(length),
		"redis.pipeline_length": strconv.Itoa(len(cmds)),
	}
	if !p.config.skipRaw {
		tags["redis.raw_command"] = raw
	}
	_, ctx = tracer.StartSpanFromContext(ctx, "redis.command",
		tracer.WithTags(tags),
		tracer.WithStartSpanConfig(p.spanCfg),
	)
	return context.WithValue(ctx, traceMarkerKey{}, traceMarker{hook: ddh, cmd: first}), nil
}

func (ddh *datadogHook) AfterProcessPipeline(ctx context.Context, cmds []redis.Cmder) error {
	var first redis.Cmder
	if len(cmds) > 0 {
		first = cmds[0]
	}
	// go-redis hands the final context to every hook's AfterProcessPipeline,
	// so only the hook that started this pipeline's span finishes it; see traceMarkerKey.
	if m, ok := ctx.Value(traceMarkerKey{}).(traceMarker); !ok || m.hook != ddh || m.cmd != first {
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
