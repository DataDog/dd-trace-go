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
	"unsafe"
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

// wrapEntry records the configuration of a client whose hook chain could not
// be inspected, keyed weakly by the client. It holds no reference to the
// client and no user callback, so it cannot pin the client.
type wrapEntry struct {
	cfg configKey
}

var (
	// wrapMu serializes WrapClient: hook inspection and installation must
	// happen as one step, or two concurrent first wraps would both see no
	// hook and install two.
	wrapMu sync.Mutex
	// wrapped deduplicates WrapClient calls for clients whose hook chain
	// cannot be read. Entries are keyed and cleaned up weakly, so the
	// registry never keeps a client alive.
	wrapped = map[weak.Pointer[byte]]*wrapEntry{}
)

// configKey is the part of a client configuration that determines the spans
// a client produces. It excludes the error-check function: it is not
// comparable, and keeping it out of the registry avoids retaining a user
// closure that may capture the client.
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

	wrapMu.Lock()
	defer wrapMu.Unlock()

	if prev, seen := datadogConfig(client); seen {
		if prev != nil {
			if !sameConfig(*prev, cfg.key()) {
				instr.Logger().Warn("contrib/go-redis/redis.v7: WrapClient called more than once on the same client; keeping the first configuration")
			}
			return
		}
	} else if key, ok := weakHandle(client); ok {
		// The hook chain cannot be read — a proxy nested deeper than
		// underlyingClient searches, for example, or an upstream layout
		// change. Deduplicate by the client's own weak identity instead, so
		// repeated wraps still install a single hook.
		if e, ok := wrapped[key]; ok {
			if !sameConfig(e.cfg, cfg.key()) {
				instr.Logger().Warn("contrib/go-redis/redis.v7: WrapClient called more than once on the same client; keeping the first configuration")
			}
			return
		}
		wrapped[key] = &wrapEntry{cfg: cfg.key()}
		runtime.AddCleanup(key.Value(), func(k weak.Pointer[byte]) {
			wrapMu.Lock()
			delete(wrapped, k)
			wrapMu.Unlock()
		}, key)
	}

	hookParams := &params{
		config: cfg,
	}
	hookParams.spanCfg = newSpanConfig(cfg, additionalTagOptions(client))
	client.AddHook(&datadogHook{params: hookParams})
}

// datadogConfig returns the configuration of the datadog hook the client
// already carries, and whether the hook chain could be read at all. A
// WithContext or WithTimeout clone of a wrapped client inherits the hook
// slice, so this detects clones; a decorator or proxy is resolved to the
// concrete client it delegates to. Reading the chain makes a second hook —
// and with it a duplicate span per command — impossible.
func datadogConfig(client redis.UniversalClient) (key *configKey, seen bool) {
	target := client
	if u := underlyingClient(client); u != nil {
		target = u
	}
	hooks := hookSlice(target)
	if !hooks.IsValid() {
		return nil, false
	}
	for i := 0; i < hooks.Len(); i++ {
		if ddh, ok := hooks.Index(i).Interface().(*datadogHook); ok {
			k := ddh.params.config.key()
			return &k, true
		}
	}
	return nil, true
}

var redisHookSliceType = reflect.TypeFor[[]redis.Hook]()

// hookSlice returns the client's hook slice, read through the unexported
// fields it lives in, or an invalid Value when the client has no readable
// hook slice.
func hookSlice(client redis.UniversalClient) reflect.Value {
	v := reflect.ValueOf(client)
	if v.Kind() != reflect.Pointer || v.IsNil() || v.Elem().Kind() != reflect.Struct {
		return reflect.Value{}
	}
	s := v.Elem()
	// The hooks live in an unexported embedded struct: view the whole client
	// through its address so its fields can be read.
	s = reflect.NewAt(s.Type(), unsafe.Pointer(s.UnsafeAddr())).Elem()
	return findHookSlice(s, 3)
}

// findHookSlice returns the first []redis.Hook field in s or in the structs
// embedded within it.
func findHookSlice(s reflect.Value, depth int) reflect.Value {
	if s.Kind() != reflect.Struct || depth == 0 {
		return reflect.Value{}
	}
	for i := 0; i < s.NumField(); i++ {
		f := s.Field(i)
		if f.Kind() == reflect.Slice && f.Type() == redisHookSliceType {
			if !f.CanInterface() {
				// Unexported field: read it through its address.
				f = reflect.NewAt(f.Type(), unsafe.Pointer(f.UnsafeAddr())).Elem()
			}
			return f
		}
		if f.Kind() == reflect.Struct {
			if !f.CanInterface() {
				// Unexported field: read it through its address.
				f = reflect.NewAt(f.Type(), unsafe.Pointer(f.UnsafeAddr())).Elem()
			}
			if h := findHookSlice(f, depth-1); h.IsValid() {
				return h
			}
		}
	}
	return reflect.Value{}
}

// weakHandle returns a weak identity for any pointer client, by referencing
// the start of the object it points to. Two handles compare equal exactly for
// the same object, and the handle never keeps the client alive.
func weakHandle(client any) (weak.Pointer[byte], bool) {
	v := reflect.ValueOf(client)
	if v.Kind() != reflect.Pointer || v.IsNil() {
		return weak.Pointer[byte]{}, false
	}
	return weak.Make((*byte)(v.UnsafePointer())), true
}

// underlyingClient returns the concrete go-redis client behind a decorator or
// proxy, if there is one within a few levels of fields, embedded or not,
// exported or not. It returns nil when it cannot see through the
// implementation, for example when the delegated client is not held in a
// field at all.
func underlyingClient(client redis.UniversalClient) redis.UniversalClient {
	var walk func(c redis.UniversalClient, depth int) redis.UniversalClient
	walk = func(c redis.UniversalClient, depth int) redis.UniversalClient {
		if depth == 0 || c == nil {
			return nil
		}
		if v := reflect.ValueOf(c); v.Kind() == reflect.Pointer && v.IsNil() {
			// A typed-nil concrete client is not a delegate; keep looking.
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
			f := s.Field(i)
			if !f.CanInterface() {
				// Unexported field: read it through its address.
				f = reflect.NewAt(f.Type(), unsafe.Pointer(f.UnsafeAddr())).Elem()
			}
			field, ok := f.Interface().(redis.UniversalClient)
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
	return ctx, nil
}

func (ddh *datadogHook) AfterProcess(ctx context.Context, cmd redis.Cmder) error {
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
	return ctx, nil
}

func (ddh *datadogHook) AfterProcessPipeline(ctx context.Context, cmds []redis.Cmder) error {
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
