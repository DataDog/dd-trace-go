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

// wrapEntry records one client in the weak registry: either an install in
// flight — done is open until the hook is added — or the durable record of
// a client whose hook chain cannot be read or of a proxy observation. It
// holds no reference to the client and no user callback, so it cannot pin
// the client.
type wrapEntry struct {
	cfg  configKey
	done chan struct{} // non-nil while the recorded install is in flight
}

var (
	// wrapMu serializes WrapClient. Decisions — hook-chain inspection and
	// registry updates — hold it; AddHook does not, because it runs
	// user-controlled code that may call WrapClient again and would deadlock
	// on the lock.
	wrapMu sync.Mutex
	// wrapped deduplicates WrapClient calls for clients whose hook chain
	// cannot be read and for proxies, keyed weakly. Entries are cleaned up
	// when the client is retired, so the registry never keeps a client
	// alive.
	wrapped = map[weak.Pointer[byte]]*wrapEntry{}
)

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

	targets := concreteClients(client)
	switch len(targets) {
	case 0:
		// No concrete client can be found to instrument: deduplicate by the
		// client's own weak identity and let its AddHook decide what it
		// instruments. Repeated wraps still install a single hook.
		wrapThrough(client, cfg)
	case 1:
		// A decorator delegating to a single concrete client: deduplicate
		// and instrument that client, against the hook it already carries —
		// inherited by a clone, installed through a previous wrap of
		// another decorator, or not at all. A client chain never carries two
		// datadog hooks, which makes duplicate spans impossible.
		wrapMember(targets[0], cfg)
	default:
		wrapProxyMembers(client, targets, cfg)
	}
}

// wrapThrough instruments client through its own AddHook, deduplicated by
// its weak identity. The caller must hold wrapMu; AddHook runs with the
// lock released.
func wrapThrough(client redis.UniversalClient, cfg *clientConfig) {
	entry, proceed := begin(client, cfg.key())
	if !proceed {
		return
	}
	unlocked(func() { addHook(client, cfg) })
	finish(client, entry, true)
}

// wrapMember instruments a single concrete client, deduplicated against the
// hook it already carries or, when its chain cannot be read, against its
// weak identity. The caller must hold wrapMu; a concrete client's AddHook is
// go-redis code, not user code, so it runs under the lock.
func wrapMember(member redis.UniversalClient, cfg *clientConfig) {
	if prev, seen := datadogConfig(member); seen {
		if prev != nil {
			if !sameConfig(*prev, cfg.key()) {
				instr.Logger().Warn("contrib/go-redis/redis.v7: WrapClient called more than once on the same client; keeping the first configuration")
			}
			return
		}
		addHook(member, cfg)
		return
	}
	// The hook chain cannot be read: the weak identity is the only
	// deduplication this client has, so its entry is kept.
	if registerWeak(member, cfg) {
		return
	}
	addHook(member, cfg)
}

// wrapProxyMembers instruments a proxy holding several concrete clients — a
// read/write router, or a client that also keeps a private one around.
// Which members its AddHook instruments cannot be inferred from fields, so
// it is observed instead: a no-op probe hook is added, and the members whose
// chains gain it are the proxy's own choice. Each of those members is then
// instrumented with that member's endpoint tags, deduplicated against the
// hook it already carries, so a pre-wrapped member is not hooked twice and
// every member is tagged with its own host, port, and database. The
// observation is recorded against the proxy itself — not against the
// members, whose hook state is each proxy's own decision — so a repeated
// wrap of the same proxy probes once and never again, while a different
// proxy over the same members is still observed separately. The probe stays
// in the chains it landed on as a no-op. The caller must hold wrapMu;
// AddHook runs with the lock released.
func wrapProxyMembers(proxy redis.UniversalClient, members []redis.UniversalClient, cfg *clientConfig) {
	entry, proceed := begin(proxy, cfg.key())
	if !proceed {
		return
	}
	before := make([]int, len(members))
	readable := make([]bool, len(members))
	for i, member := range members {
		if h := hookSlice(member); h.IsValid() {
			before[i], readable[i] = h.Len(), true
		}
	}
	unlocked(func() { proxy.AddHook(probeHook{}) })
	var instrumented bool
	for i, member := range members {
		if !readable[i] {
			continue
		}
		if h := hookSlice(member); h.Len() > before[i] {
			wrapMember(member, cfg)
			instrumented = true
		}
	}
	if !instrumented {
		// AddHook reached no concrete client we can see: instrument through
		// the proxy itself, deduplicated by its identity.
		unlocked(func() { addHook(proxy, cfg) })
	}
	finish(proxy, entry, true)
}

// begin records an install in flight for client, so that a concurrent wrap
// of the same client waits for this one instead of installing a second
// hook. It reports a nil entry when the client cannot be keyed — the caller
// then installs without a marker — and reports proceed=false when an entry
// already exists: its configuration is kept and, if the recorded install is
// still in flight, this call waits for it to finish. The caller must hold
// wrapMu.
func begin(client redis.UniversalClient, key configKey) (entry *wrapEntry, proceed bool) {
	k, ok := weakHandle(client)
	if !ok {
		return nil, true
	}
	if e, ok := wrapped[k]; ok {
		if !sameConfig(e.cfg, key) {
			instr.Logger().Warn("contrib/go-redis/redis.v7: WrapClient called more than once on the same client; keeping the first configuration")
		}
		if e.done != nil {
			done := e.done
			unlocked(func() { <-done })
		}
		return nil, false
	}
	e := &wrapEntry{cfg: key, done: make(chan struct{})}
	wrapped[k] = e
	runtime.AddCleanup(k.Value(), func(kk weak.Pointer[byte]) {
		wrapMu.Lock()
		delete(wrapped, kk)
		wrapMu.Unlock()
	}, k)
	// The cleanup is attached to the client: when it becomes unreachable the
	// entry goes with it, even though neither side keeps the other alive.
	// KeepAlive closes the window in which a GC could collect a client whose
	// last mention was the weak handle above.
	runtime.KeepAlive(client)
	return e, true
}

// registerWeak records cfg for the client under its weak identity and
// reports whether the client was already registered — an already-registered
// client keeps its first configuration and gets no second hook. The caller
// must hold wrapMu.
func registerWeak(client redis.UniversalClient, cfg *clientConfig) bool {
	k, ok := weakHandle(client)
	if !ok {
		return false
	}
	if e, ok := wrapped[k]; ok {
		if !sameConfig(e.cfg, cfg.key()) {
			instr.Logger().Warn("contrib/go-redis/redis.v7: WrapClient called more than once on the same client; keeping the first configuration")
		}
		return true
	}
	wrapped[k] = &wrapEntry{cfg: cfg.key()}
	runtime.AddCleanup(k.Value(), func(kk weak.Pointer[byte]) {
		wrapMu.Lock()
		delete(wrapped, kk)
		wrapMu.Unlock()
	}, k)
	// KeepAlive closes the window in which a GC could collect a client
	// whose last mention was the weak handle above.
	runtime.KeepAlive(client)
	return false
}

// finish completes the install recorded by entry: waiters are released and,
// unless keep is set, the marker is removed — a client with a readable hook
// chain is deduplicated by the hook itself, so the registry stays empty for
// it. The caller must hold wrapMu.
func finish(client redis.UniversalClient, entry *wrapEntry, keep bool) {
	if entry == nil {
		return
	}
	close(entry.done)
	entry.done = nil
	if !keep {
		if k, ok := weakHandle(client); ok {
			delete(wrapped, k)
		}
	}
}

// unlocked runs f without holding wrapMu, and holds it again once f returns.
// AddHook runs user-controlled code, which may call WrapClient again — from
// a proxy that lazily instruments a delegate, say — and must not deadlock on
// the lock. The caller must hold wrapMu.
func unlocked(f func()) {
	wrapMu.Unlock()
	defer wrapMu.Lock()
	f()
}

// probeHook is the no-op hook used to observe which concrete clients a
// proxy's AddHook instruments; see wrapProxyMembers.
type probeHook struct{}

func (probeHook) BeforeProcess(ctx context.Context, cmd redis.Cmder) (context.Context, error) {
	return ctx, nil
}

func (probeHook) AfterProcess(ctx context.Context, cmd redis.Cmder) error { return nil }

func (probeHook) BeforeProcessPipeline(ctx context.Context, cmds []redis.Cmder) (context.Context, error) {
	return ctx, nil
}

func (probeHook) AfterProcessPipeline(ctx context.Context, cmds []redis.Cmder) error {
	return nil
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

var (
	mutexType   = reflect.TypeOf(sync.Mutex{})
	rwMutexType = reflect.TypeOf(sync.RWMutex{})
)

// lockStruct read-locks the struct's own mutex, when it has one, and returns
// the unlock function: a proxy may replace its delegate fields while serving
// traffic, guarded by that mutex, and reading them — here or in the hook
// chain — must not race with it. A struct without a mutex does not
// synchronize those fields, and reading them is then no more racy than the
// struct's own readers.
func lockStruct(s reflect.Value) func() {
	for i := 0; i < s.NumField(); i++ {
		t := s.Type().Field(i).Type
		if t != mutexType && t != rwMutexType {
			continue
		}
		f := s.Field(i)
		if !f.CanInterface() {
			// Unexported field: address it through its location.
			f = reflect.NewAt(f.Type(), unsafe.Pointer(f.UnsafeAddr())).Elem()
		}
		if t == rwMutexType {
			f.Addr().MethodByName("RLock").Call(nil)
			return func() { f.Addr().MethodByName("RUnlock").Call(nil) }
		}
		f.Addr().MethodByName("Lock").Call(nil)
		return func() { f.Addr().MethodByName("Unlock").Call(nil) }
	}
	return func() {}
}

// findHookSlice returns the first []redis.Hook field in s or in the structs
// embedded within it, read under the struct's own mutex when it has one.
func findHookSlice(s reflect.Value, depth int) reflect.Value {
	if s.Kind() != reflect.Struct || depth == 0 {
		return reflect.Value{}
	}
	unlock := lockStruct(s)
	defer unlock()
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
		// The struct's own mutex, when it has one, guards its delegate
		// fields; read them under it.
		unlock := lockStruct(s)
		defer unlock()
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
