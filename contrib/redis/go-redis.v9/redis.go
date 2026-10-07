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
	"time"
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
	cfg      configKey
	done     chan struct{} // non-nil while the recorded install is in flight
	goid     uint64        // the goroutine that started the install, for reentry
	observed bool          // an observation completed for this client
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

	// Resolve the concrete clients before taking the package lock: the
	// field walk takes each proxy's own mutex when it has one, and that
	// mutex must not be nested inside wrapMu — a proxy may hold its mutex
	// while calling WrapClient (to replace and instrument a delegate, say),
	// and wrapMu-then-proxy-mutex would then deadlock against
	// proxy-mutex-then-wrapMu.
	targets, ok := concreteClients(client)
	if !ok {
		// The proxy's mutex stayed held — possibly by this very call chain,
		// which would deadlock on any further interaction with the proxy.
		// Do nothing; a wrap after the mutex is released works normally.
		return
	}

	// Warnings are emitted after the lock is released: a custom logger is
	// user-controlled code — like a proxy's AddHook — and may call WrapClient
	// again from its Log method.
	var warnings []string
	defer func() {
		for _, w := range warnings {
			instr.Logger().Warn("%s", w)
		}
	}()

	wrapMu.Lock()
	defer wrapMu.Unlock()

	warn := func() {
		warnings = append(warnings, "contrib/redis/go-redis.v9: WrapClient called more than once on the same client; keeping the first configuration")
	}

	if len(targets) == 1 && targets[0] == client {
		// The client itself is a concrete go-redis client — or a clone of
		// one: deduplicate and instrument it directly, against the hook it
		// already carries — inherited by a clone, installed through a
		// previous wrap of another decorator, or not at all. A client chain
		// never carries two datadog hooks, which makes duplicate spans
		// impossible.
		wrapMember(client, cfg, warn)
		return
	}
	// Any other implementation is a proxy, with one member or several or
	// none that can be found: what its AddHook instruments is its own
	// decision — it may fan out to its current members, retain hooks for
	// delegates it creates later, or apply them lazily — so it is observed
	// before instrumented.
	wrapProxyMembers(client, targets, cfg, warn)
}

// wrapMember instruments a single concrete client, deduplicated against the
// hook it already carries or, when its chain cannot be read, against its
// weak identity. The caller must hold wrapMu; a concrete client's AddHook is
// go-redis code, not user code, so it runs under the lock.
func wrapMember(member redis.UniversalClient, cfg *clientConfig, warn func()) {
	if prev, seen := datadogConfig(member); seen {
		if prev != nil {
			if !sameConfig(*prev, cfg.key()) {
				warn()
			}
			return
		}
		addHook(member, cfg)
		return
	}
	// The hook chain cannot be read: the weak identity is the only
	// deduplication this client has, so its entry is kept.
	if registerWeak(member, cfg, warn) {
		return
	}
	addHook(member, cfg)
}

// wrapProxyMembers instruments a proxy — one delegating to a single concrete
// client, a read/write router holding several, or a client that also keeps a
// private one around. Which members its AddHook instruments, and whether it
// retains hooks for delegates it creates later instead of applying them now,
// cannot be inferred from fields, so it is observed instead: a no-op probe
// hook is added, and the members whose chains gain it are the proxy's own
// choice. Each of those members is then instrumented with that member's
// endpoint tags, deduplicated against the hook it already carries, so a
// pre-wrapped member is not hooked twice and every member is tagged with its
// own host, port, and database. When no member gains the probe — the proxy
// retains the hook rather than applying it — the real hook is handed to the
// proxy's AddHook, so delegates it instruments later are traced too. The
// observation is recorded against the proxy itself — not against the
// members, whose hook state is each proxy's own decision — so a repeated
// wrap of the same proxy probes once and never again, while a different
// proxy over the same members is still observed separately. The probe stays
// in the chains it landed on as a no-op. The caller must hold wrapMu;
// AddHook runs with the lock released.
func wrapProxyMembers(proxy redis.UniversalClient, members []redis.UniversalClient, cfg *clientConfig, warn func()) {
	entry, proceed := begin(proxy, cfg.key(), warn)
	if !proceed {
		// This proxy was observed by an earlier wrap: hooks cannot be
		// removed, so its outcome stands.
		return
	}
	// Nothing can be learned and nothing can be added once this proxy has
	// been observed and every member already carries the hook: repeated
	// wraps of the same proxy cost nothing. A first wrap still probes —
	// the members carry no proof about this proxy, and a retaining one
	// must be detected and given the real hook, or delegates it creates
	// later are untraced. The entry is kept, so wraps of freshly created
	// but equivalent proxies each observe once and never again; the probes
	// they leave on already hooked members are no-ops.
	var hooked *configKey
	allHooked := len(members) > 0
	for _, member := range members {
		prev, seen := datadogConfig(member)
		if !seen || prev == nil {
			allHooked = false
			break
		}
		if hooked == nil {
			k := *prev
			hooked = &k
		}
	}
	if allHooked && entry != nil && entry.observed {
		if hooked != nil && !sameConfig(*hooked, cfg.key()) {
			warn()
		}
		return
	}
	if allHooked {
		if hooked != nil && !sameConfig(*hooked, cfg.key()) {
			warn()
		}
	}
	before := make([]int, len(members))
	readable := make([]bool, len(members))
	for i, member := range members {
		if h := hookSlice(member); h.IsValid() {
			before[i], readable[i] = h.Len(), true
		}
	}
	// A panic in the proxy's AddHook — recovered by the application — must
	// not leave an in-flight marker that later wraps wait on forever; the
	// entry is dropped so the next wrap retries.
	completed := false
	defer func() {
		if !completed {
			finish(proxy, entry, false)
		}
	}()
	unlocked(func() { proxy.AddHook(probeHook{}) })
	// A proxy that keeps the probe in its own fields retains hooks for
	// delegates it creates later; those delegates are traced only through a
	// real hook passed to its AddHook. The same call fans that hook out to
	// the current members, so they must not be instrumented per member as
	// well — every command would be traced twice. The retained hook carries
	// no endpoint tags: the delegate it eventually lands on may have
	// different host, port, and database options than the proxy's current
	// members, and a missing tag is better than a wrong one.
	// The scan takes the proxy's own mutex, so it runs with the package
	// lock released.
	var retained bool
	unlocked(func() { retained = retainsHook(proxy, probeHook{}) })
	if retained {
		unlocked(func() { addHookWithoutEndpoints(proxy, cfg) })
		completed = true
		finish(proxy, entry, true)
		return
	}
	var instrumented bool
	for i, member := range members {
		if !readable[i] {
			continue
		}
		if h := hookSlice(member); h.Len() > before[i] {
			wrapMember(member, cfg, warn)
			instrumented = true
		}
	}
	if !instrumented {
		// AddHook reached no concrete client we can see: instrument through
		// the proxy itself, deduplicated by its identity.
		unlocked(func() { addHook(proxy, cfg) })
	}
	completed = true
	finishObserved(proxy, entry, true, true)
}

// begin records an install in flight for client, so that a concurrent wrap
// of the same client waits for this one instead of installing a second
// hook. It reports a nil entry when the client cannot be keyed — the caller
// then installs without a marker — and reports proceed=false when an entry
// already exists: its configuration is kept and, if the recorded install is
// still in flight, this call waits for it to finish. The caller must hold
// wrapMu.
func begin(client redis.UniversalClient, key configKey, warn func()) (entry *wrapEntry, proceed bool) {
	k, ok := weakHandle(client)
	if !ok {
		return nil, true
	}
	var warned bool
	for {
		e, ok := wrapped[k]
		if !ok {
			break
		}
		if !warned {
			warned = true
			if !sameConfig(e.cfg, key) {
				warn()
			}
		}
		// Wait for the recorded install, unless this goroutine is the one
		// running it — a proxy whose AddHook re-enters WrapClient for that
		// same proxy would otherwise wait for a channel only that very call
		// can close. Installs started by other goroutines are still waited
		// on, so a nested wrap does not return before a concurrent install
		// has finished.
		if e.done != nil && e.goid != goid() {
			done := e.done
			unlocked(func() { <-done })
			// The install may have failed and dropped its marker; recheck
			// instead of returning without a hook.
			continue
		}
		return nil, false
	}
	e := &wrapEntry{cfg: key, done: make(chan struct{}), goid: goid()}
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

// goid returns the current goroutine's ID, from the header of its stack
// snapshot. It identifies the goroutine that started an in-flight install,
// so a call chain re-entering its own install does not wait for it while
// concurrent installs are still waited on.
func goid() uint64 {
	b := make([]byte, 64)
	b = b[:runtime.Stack(b, false)]
	// The first line reads "goroutine 123 [running]:".
	if len(b) < 11 || string(b[:10]) != "goroutine " {
		return 0
	}
	var id uint64
	for _, c := range b[10:] {
		if c < '0' || c > '9' {
			break
		}
		id = id*10 + uint64(c-'0')
	}
	return id
}

// registerWeak records cfg for the client under its weak identity and
// reports whether the client was already registered — an already-registered
// client keeps its first configuration and gets no second hook. The caller
// must hold wrapMu.
func registerWeak(client redis.UniversalClient, cfg *clientConfig, warn func()) bool {
	k, ok := weakHandle(client)
	if !ok {
		return false
	}
	if e, ok := wrapped[k]; ok {
		if !sameConfig(e.cfg, cfg.key()) {
			warn()
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
	finishObserved(client, entry, keep, false)
}

// finishObserved completes the install recorded by entry, marking the client
// observed so a later wrap of the same object knows its outcome stands.
func finishObserved(client redis.UniversalClient, entry *wrapEntry, keep, observed bool) {
	if entry == nil {
		return
	}
	close(entry.done)
	entry.done = nil
	entry.observed = entry.observed || observed
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

// retainsHook reports whether the proxy kept the given hook in its own
// fields: a proxy that retains hooks, to apply them to delegates it creates
// later, keeps a copy of everything its AddHook is handed.
func retainsHook(proxy redis.UniversalClient, hook redis.Hook) bool {
	v := reflect.ValueOf(proxy)
	if v.Kind() == reflect.Pointer {
		if v.IsNil() {
			return false
		}
		v = v.Elem()
	}
	if v.Kind() != reflect.Struct {
		return false
	}
	if v.CanAddr() {
		unlock, ok := lockStruct(v)
		defer unlock()
		if !ok {
			// The proxy's mutex stayed held; reading its retained-hook
			// fields without it would race with the update in progress.
			// Report unknown rather than guess.
			return false
		}
	}
	return containsHook(v, hook, 3)
}

// containsHook reports whether s, or a struct embedded within it, holds the
// hook in a field or in a hook slice. Hooks with non-comparable dynamic
// types cannot be compared and are treated as absent.
func containsHook(s reflect.Value, hook redis.Hook, depth int) bool {
	if s.Kind() != reflect.Struct || depth == 0 {
		return false
	}
	for i := 0; i < s.NumField(); i++ {
		f := s.Field(i)
		if !f.CanInterface() {
			if !f.CanAddr() {
				continue
			}
			f = reflect.NewAt(f.Type(), unsafe.Pointer(f.UnsafeAddr())).Elem()
		}
		switch f.Kind() {
		case reflect.Interface:
			if h, ok := f.Interface().(redis.Hook); ok && hookEqual(h, hook) {
				return true
			}
		case reflect.Slice:
			if f.Type() == redisHookSliceType {
				for j := 0; j < f.Len(); j++ {
					if h, ok := f.Index(j).Interface().(redis.Hook); ok && hookEqual(h, hook) {
						return true
					}
				}
			}
		case reflect.Struct:
			if containsHook(f, hook, depth-1) {
				return true
			}
		case reflect.Pointer:
			if f.IsNil() || !f.CanInterface() {
				continue
			}
			// A concrete client field is a delegate, not proxy-owned
			// storage: the probe the proxy's own AddHook fanned out to it
			// is not evidence of retention.
			if t := f.Type(); t == redisClientType || t == redisClusterClientType || t == redisRingType {
				continue
			}
			if f.Elem().Kind() == reflect.Struct && containsHook(f.Elem(), hook, depth-1) {
				return true
			}
		}
	}
	return false
}

// hookEqual compares two hooks, guarding against non-comparable dynamic
// types: comparing those would panic.
func hookEqual(a, b redis.Hook) bool {
	ta, tb := reflect.TypeOf(a), reflect.TypeOf(b)
	if ta == nil || tb == nil || ta != tb || !ta.Comparable() {
		return false
	}
	return a == b
}

// probeHook is the no-op hook used to observe which concrete clients a
// proxy's AddHook instruments; see wrapProxyMembers.
type probeHook struct{}

func (probeHook) DialHook(hook redis.DialHook) redis.DialHook {
	return hook
}

func (probeHook) ProcessHook(hook redis.ProcessHook) redis.ProcessHook {
	return hook
}

func (probeHook) ProcessPipelineHook(hook redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return hook
}

// addHookWithoutEndpoints installs a datadog hook carrying every mandatory
// tag but no endpoint tags on client: for a hook a proxy retains, the
// delegate it eventually instruments is not known at wrap time, and the
// proxy's own endpoints may not match it.
func addHookWithoutEndpoints(client redis.UniversalClient, cfg *clientConfig) {
	hookParams := &params{
		config: cfg,
	}
	hookParams.spanCfg = newSpanConfig(cfg, commonTagOptions())
	client.AddHook(&datadogHook{params: hookParams})
}

// commonTagOptions returns the tags every span of this integration must
// carry, independent of any endpoint.
func commonTagOptions() []tracer.StartSpanOption {
	return []tracer.StartSpanOption{
		tracer.SpanType(ext.SpanTypeRedis),
		tracer.Tag(ext.Component, string(instrumentation.PackageRedisGoRedisV9)),
		tracer.Tag(ext.SpanKind, ext.SpanKindClient),
		tracer.Tag(ext.DBSystem, ext.DBSystemRedis),
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
	// The hooks live in unexported embedded structs — v9.22 nests them
	// behind the base-client pointer and an atomic snapshot: view the whole
	// client through its address so its fields can be read.
	s = reflect.NewAt(s.Type(), unsafe.Pointer(s.UnsafeAddr())).Elem()
	return findHookSlice(s, 8)
}

var (
	mutexType              = reflect.TypeOf(sync.Mutex{})
	rwMutexType            = reflect.TypeOf(sync.RWMutex{})
	redisClientType        = reflect.TypeFor[*redis.Client]()
	redisClusterClientType = reflect.TypeFor[*redis.ClusterClient]()
	redisRingType          = reflect.TypeFor[*redis.Ring]()
)

// lockStruct tries to take the struct's own mutex, when it has one, and
// returns the unlock function: a proxy may replace its delegate fields while
// serving traffic, guarded by that mutex, and reading them — here or in the
// hook chain — must not race with it. It reports false when the mutex stays
// held: the holder may be the caller's own goroutine, and blocking on it
// would deadlock. A struct without a mutex does not synchronize those fields,
// and reading them is then no more racy than the struct's own readers.
func lockStruct(s reflect.Value) (unlock func(), ok bool) {
	// Lock every mutex the struct owns — the one guarding a delegate field
	// cannot be told apart from unrelated ones — and give up entirely when
	// any stays held: the holder may be the very call chain running
	// WrapClient, and blocking on it would deadlock the caller's own
	// goroutine. Brief contention from another goroutine is ridden out with
	// a few short retries.
	var unlocks []func()
	for i := 0; i < s.NumField(); i++ {
		t := s.Type().Field(i).Type
		if t != mutexType && t != rwMutexType {
			continue
		}
		f := s.Field(i)
		if !f.CanAddr() {
			// A copy of a struct — a decorator passed by value, say — cannot
			// have its mutex locked; the copy is unshared, so nothing can
			// race with reading it.
			continue
		}
		if !f.CanInterface() {
			// Unexported field: address it through its location.
			f = reflect.NewAt(f.Type(), unsafe.Pointer(f.UnsafeAddr())).Elem()
		}
		var locked bool
		for range 100 {
			if f.Addr().MethodByName("TryLock").Call(nil)[0].Bool() {
				locked = true
				break
			}
			time.Sleep(time.Millisecond)
		}
		if !locked {
			for _, u := range unlocks {
				u()
			}
			return func() {}, false
		}
		unlocks = append(unlocks, func() { f.Addr().MethodByName("Unlock").Call(nil) })
	}
	return func() {
		for _, u := range unlocks {
			u()
		}
	}, true
}

// findHookSlice returns the first []redis.Hook field in s or in the structs
// embedded within it, read under the struct's own mutex when it has one.
func findHookSlice(s reflect.Value, depth int) reflect.Value {
	if s.Kind() != reflect.Struct || depth == 0 {
		return reflect.Value{}
	}
	unlock, _ := lockStruct(s)
	defer unlock()
	for i := 0; i < s.NumField(); i++ {
		f := s.Field(i)
		switch f.Kind() {
		case reflect.Slice:
			if f.Type() != redisHookSliceType {
				continue
			}
			if !f.CanInterface() {
				// Unexported field: read it through its address.
				f = reflect.NewAt(f.Type(), unsafe.Pointer(f.UnsafeAddr())).Elem()
			}
			return f
		case reflect.Struct:
			if !f.CanInterface() {
				// Unexported field: read it through its address.
				f = reflect.NewAt(f.Type(), unsafe.Pointer(f.UnsafeAddr())).Elem()
			}
			// An atomic snapshot stored by value exposes its target the
			// same way: through its Load method.
			if m := f.Addr().MethodByName("Load"); m.IsValid() &&
				m.Type().NumIn() == 0 && m.Type().NumOut() == 1 && m.Type().Out(0).Kind() == reflect.Pointer {
				target := m.Call(nil)[0]
				if !target.IsNil() && target.Elem().Kind() == reflect.Struct {
					if h := findHookSlice(target.Elem(), depth-1); h.IsValid() {
						return h
					}
				}
			}
			if h := findHookSlice(f, depth-1); h.IsValid() {
				return h
			}
		case reflect.Pointer:
			if f.IsNil() {
				continue
			}
			if !f.CanInterface() {
				// Unexported field: read it through its address.
				if !f.CanAddr() {
					continue
				}
				f = reflect.NewAt(f.Type(), unsafe.Pointer(f.UnsafeAddr())).Elem()
			}
			// go-redis v9.22 and later keep the hook snapshot behind an
			// atomic pointer: reach it through its Load method. Other
			// pointers to structs are followed directly.
			if m := f.MethodByName("Load"); m.IsValid() &&
				m.Type().NumIn() == 0 && m.Type().NumOut() == 1 && m.Type().Out(0).Kind() == reflect.Pointer {
				target := m.Call(nil)[0]
				if target.IsNil() || target.Elem().Kind() != reflect.Struct {
					continue
				}
				if h := findHookSlice(target.Elem(), depth-1); h.IsValid() {
					return h
				}
				continue
			}
			if f.Elem().Kind() != reflect.Struct {
				continue
			}
			if h := findHookSlice(f.Elem(), depth-1); h.IsValid() {
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
// from client through several levels of fields, embedded or not, exported or
// not, by pointer or by value. A client passed directly yields itself; a
// decorator delegating to one client yields that client; a proxy holding
// several — a read/write router, say — yields them all. It yields nothing
// when it cannot see through the implementation, for example when the
// delegated clients are not held in fields at all or are nested beyond the
// search depth.
func concreteClients(client redis.UniversalClient) (targets []redis.UniversalClient, ok bool) {
	var found []redis.UniversalClient
	var aborted bool
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
		if v.Kind() == reflect.Pointer {
			if v.IsNil() {
				return
			}
			v = v.Elem()
		}
		// A decorator may be passed by value as well as by pointer; its
		// exported fields are readable either way.
		if v.Kind() != reflect.Struct {
			return
		}
		s := v
		// The struct's own mutex, when it has one, guards its delegate
		// fields; read them under it. A mutex that stays held may be held
		// by this very call chain, and blocking on it would deadlock: treat
		// the proxy as opaque instead, and let its AddHook see nothing.
		unlock, locked := lockStruct(s)
		defer unlock()
		if !locked {
			aborted = true
			return
		}
		for i := 0; i < s.NumField(); i++ {
			f := s.Field(i)
			if !f.CanInterface() {
				// Unexported field of an addressable struct: read it through
				// its address. A non-addressable value cannot give access to
				// its unexported fields; skip them.
				if !f.CanAddr() {
					continue
				}
				f = reflect.NewAt(f.Type(), unsafe.Pointer(f.UnsafeAddr())).Elem()
			}
			field, ok := f.Interface().(redis.UniversalClient)
			if !ok {
				continue
			}
			walk(field, depth-1)
		}
	}
	// A proxy may nest its delegates a few levels deep; only nesting beyond
	// this depth leaves the client undiscoverable, falling back to the
	// proxy's own identity.
	walk(client, 8)
	if aborted {
		return nil, false
	}
	return found, true
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
		tracer.Tag(ext.Component, string(instrumentation.PackageRedisGoRedisV9)),
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
