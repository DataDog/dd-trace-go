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

	"github.com/redis/go-redis/v9"
)

var instr *instrumentation.Instrumentation

func init() {
	instr = instrumentation.Load(instrumentation.PackageRedisGoRedisV9)
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

	wrapMu.Lock()
	defer wrapMu.Unlock()

	targets := concreteClients(client)
	if len(targets) == 0 {
		// No concrete client can be found to instrument: deduplicate by the
		// client's own weak identity and let its AddHook decide what it
		// instruments. Repeated wraps still install a single hook.
		key, ok := weakHandle(client)
		if !ok {
			addHook(client, cfg)
			return
		}
		if e, ok := wrapped[key]; ok {
			if !sameConfig(e.cfg, cfg.key()) {
				instr.Logger().Warn("contrib/redis/go-redis.v9: WrapClient called more than once on the same client; keeping the first configuration")
			}
			return
		}
		wrapped[key] = &wrapEntry{cfg: cfg.key()}
		runtime.AddCleanup(key.Value(), func(k weak.Pointer[byte]) {
			wrapMu.Lock()
			delete(wrapped, k)
			wrapMu.Unlock()
		}, key)
		addHook(client, cfg)
		return
	}
	// Instrument every concrete client the implementation delegates to, one
	// hook each, deduplicated against the hook the client already carries —
	// inherited by a clone, installed through a previous wrap of another
	// decorator, or not at all. A proxy with several clients is not left
	// untraced because one member happens to be wrapped already, and a
	// member wrapped through the proxy is not wrapped twice.
	for _, target := range targets {
		prev, seen := datadogConfig(target)
		if seen && prev != nil {
			if !sameConfig(*prev, cfg.key()) {
				instr.Logger().Warn("contrib/redis/go-redis.v9: WrapClient called more than once on the same client; keeping the first configuration")
			}
			continue
		}
		if !seen {
			// The hook chain cannot be read: deduplicate by the client's own
			// weak identity instead.
			key, ok := weakHandle(target)
			if ok {
				if e, ok := wrapped[key]; ok {
					if !sameConfig(e.cfg, cfg.key()) {
						instr.Logger().Warn("contrib/redis/go-redis.v9: WrapClient called more than once on the same client; keeping the first configuration")
					}
					continue
				}
				wrapped[key] = &wrapEntry{cfg: cfg.key()}
				runtime.AddCleanup(key.Value(), func(k weak.Pointer[byte]) {
					wrapMu.Lock()
					delete(wrapped, k)
					wrapMu.Unlock()
				}, key)
			}
		}
		addHook(target, cfg)
	}
}

// addHook installs a datadog hook with the given configuration on client.
func addHook(client redis.UniversalClient, cfg *clientConfig) {
	hookParams := &params{
		config: cfg,
	}
	hookParams.spanCfg = newSpanConfig(cfg, additionalTagOptions(client))
	client.AddHook(&datadogHook{params: hookParams})
}

// datadogConfig returns the configuration of the datadog hook the client
// already carries, and whether the hook chain could be read at all. A
// WithContext or WithTimeout clone of a wrapped client inherits the hook
// slice, so this detects clones. Reading the chain makes a second hook —
// and with it a duplicate span per command — impossible.
func datadogConfig(client redis.UniversalClient) (key *configKey, seen bool) {
	hooks := hookSlice(client)
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

// concreteClients returns the distinct concrete go-redis clients reachable
// from client within a few levels of fields, embedded or not, exported or
// not. A client passed directly yields itself; a decorator delegating to one
// client yields that client; a proxy holding several — a read/write router,
// say — yields them all. It yields nothing when it cannot see through the
// implementation, for example when the delegated clients are not held in
// fields at all.
func concreteClients(client redis.UniversalClient) []redis.UniversalClient {
	var found []redis.UniversalClient
	var walk func(c redis.UniversalClient, depth int)
	walk = func(c redis.UniversalClient, depth int) {
		if depth == 0 || c == nil {
			return
		}
		if v := reflect.ValueOf(c); v.Kind() == reflect.Pointer && v.IsNil() {
			// A typed-nil concrete client is not a delegate; keep looking.
			return
		}
		switch c.(type) {
		case *redis.Client, *redis.ClusterClient, *redis.Ring:
			for _, f := range found {
				if f == c {
					return
				}
			}
			found = append(found, c)
			return
		}
		v := reflect.ValueOf(c)
		if v.Kind() != reflect.Pointer || v.IsNil() || v.Elem().Kind() != reflect.Struct {
			return
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
			walk(field, depth-1)
		}
	}
	walk(client, 3)
	return found
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
	// No duplicate-hook deduplication is needed anywhere, including here:
	// WrapClient reads the client's hook chain and installs its hook only
	// when no datadog hook is present — inherited by a clone or not — so a
	// client's chain never carries two. Dials additionally flow through the
	// original client's hook chain only: a WithTimeout clone shares the
	// original's pool, whose dialer is bound to the original.
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
