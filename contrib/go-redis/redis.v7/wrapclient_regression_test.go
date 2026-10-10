// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package redis

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	rediswrap "github.com/DataDog/dd-trace-go/contrib/internal/rediswrap/v2"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/mocktracer"

	"github.com/go-redis/redis/v7"
)

// cyclicProxy holds a self-referential pointer field.
type cyclicProxy struct {
	redis.UniversalClient
	next *cyclicProxy
}

func (r *cyclicProxy) AddHook(hook redis.Hook) {
	r.UniversalClient.AddHook(hook)
}

func TestWrapClientCyclicProxy(t *testing.T) {
	cfg := new(clientConfig)
	defaults(cfg)

	mt := mocktracer.Start()
	defer mt.Stop()

	current := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1"})
	t.Cleanup(func() { current.Close() })
	p := &cyclicProxy{UniversalClient: current}
	p.next = p
	WrapClient(p)

	_ = current.Get("foo").Err()
	if spans := commandSpans(mt, cfg.spanName); len(spans) != 1 {
		t.Fatalf("expected exactly 1 command span, got %d", len(spans))
	}
}

// Two lazy proxies with distinct delegates must not be mistaken for each other
// by the reentry guard: the guard's fallback matches on the reference-bearing
// fields too, so a nested wrap of the second still installs.
type delegateLazyProxy struct {
	redis.UniversalClient
	retains []redis.Hook
}

func (r *delegateLazyProxy) AddHook(hook redis.Hook) {
	WrapClient(r)
	r.retains = append(r.retains, hook)
}

func TestWrapClientDistinctLazyProxies(t *testing.T) {
	cfg := new(clientConfig)
	defaults(cfg)

	mt := mocktracer.Start()
	defer mt.Stop()

	a := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1"})
	t.Cleanup(func() { a.Close() })
	b := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1"})
	t.Cleanup(func() { b.Close() })

	pa := &delegateLazyProxy{UniversalClient: a}
	WrapClient(pa)
	// The first proxy's AddHook re-entered for the SECOND proxy —
	// the guard must not block it.
	pb := &delegateLazyProxy{UniversalClient: b}
	WrapClient(pb)

	for _, h := range pa.retains {
		a.AddHook(h)
	}
	for _, h := range pb.retains {
		b.AddHook(h)
	}
	_ = a.Get("foo").Err()
	_ = b.Get("foo").Err()
	if spans := commandSpans(mt, cfg.spanName); len(spans) != 2 {
		t.Fatalf("expected 1 command span per proxy, got %d", len(spans))
	}
}

// refDistinctionProxy is a non-comparable proxy type — its map field makes
// == illegal — passed by value, with an empty member set. Its exported
// pointer fields are its only identity: the guard must compare them before
// equating two distinct proxies through their (empty) member sets.
type refDistinctionProxy struct {
	redis.UniversalClient
	ID    *int
	Other *refDistinctionProxy
	Tags  map[string]string
	name  string
	trace *[]string
}

func (r refDistinctionProxy) AddHook(hook redis.Hook) {
	if r.trace != nil {
		*r.trace = append(*r.trace, r.name+".addhook")
	}
	// Re-enter by value: two value copies of distinct proxies are both
	// non-comparable, so the guard can only tell them apart through their
	// reference-bearing fields.
	WrapClient(*r.Other)
}

// Two distinct non-comparable value proxies with the same member set (empty)
// must not be mistaken for each other by the reentry guard: a nested wrap of
// the second proxy still reaches its AddHook.
func TestWrapClientRefFieldDistinguishesProxies(t *testing.T) {
	cfg := new(clientConfig)
	defaults(cfg)

	var order []string
	pa := refDistinctionProxy{ID: new(int), Tags: map[string]string{}, name: "pa", trace: &order}
	pb := refDistinctionProxy{ID: new(int), Tags: map[string]string{}, name: "pb", trace: &order}
	pa.Other = &pb
	pb.Other = &pa

	WrapClient(pa)
	// pb's AddHook must have run during pa's install — the guard must not
	// have mistaken pb for pa's own re-entry.
	found := false
	for _, e := range order {
		if e == "pb.addhook" {
			found = true
		}
	}
	if !found {
		t.Fatal("the second proxy's AddHook was never invoked: the reentry guard equated two distinct proxies")
	}
	_ = cfg
}

// ptrContainerProxy keeps one delegate directly and a second behind a pointer
// to a slice of delegates.
type ptrContainerProxy struct {
	redis.UniversalClient
	extra *[]redis.UniversalClient
}

func (r *ptrContainerProxy) AddHook(hook redis.Hook) {
	r.UniversalClient.AddHook(hook)
	for _, c := range *r.extra {
		c.AddHook(hook)
	}
}

// A delegate held behind a pointer to a member container must be discovered
// like a directly held one: the walk finds it, and the wrap instruments it,
// so commands through it are traced.
func TestWrapClientPtrSliceMemberContainer(t *testing.T) {
	cfg := new(clientConfig)
	defaults(cfg)

	mt := mocktracer.Start()
	defer mt.Stop()

	current := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1"})
	t.Cleanup(func() { current.Close() })
	hidden := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1"})
	t.Cleanup(func() { hidden.Close() })
	extra := []redis.UniversalClient{hidden}
	proxy := &ptrContainerProxy{UniversalClient: current, extra: &extra}
	WrapClient(proxy)

	_ = hidden.Get("foo").Err()
	if spans := commandSpans(mt, cfg.spanName); len(spans) != 1 {
		t.Fatalf("expected the hidden delegate to be traced exactly once, got %d spans", len(spans))
	}
}

// ptrMemberProxy keeps one delegate directly and a second behind a pointer
// to the client interface itself.
type ptrMemberProxy struct {
	redis.UniversalClient
	extra *redis.UniversalClient
}

func (r *ptrMemberProxy) AddHook(hook redis.Hook) {
	r.UniversalClient.AddHook(hook)
	(*r.extra).AddHook(hook)
}

// A delegate held behind a pointer to the client interface must be
// discovered: commands through it are traced.
func TestWrapClientPtrInterfaceMember(t *testing.T) {
	cfg := new(clientConfig)
	defaults(cfg)

	mt := mocktracer.Start()
	defer mt.Stop()

	current := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1"})
	t.Cleanup(func() { current.Close() })
	hidden := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1"})
	t.Cleanup(func() { hidden.Close() })
	member := redis.UniversalClient(hidden)
	proxy := &ptrMemberProxy{UniversalClient: current, extra: &member}
	WrapClient(proxy)

	_ = hidden.Get("foo").Err()
	if spans := commandSpans(mt, cfg.spanName); len(spans) != 1 {
		t.Fatalf("expected the pointer-held delegate to be traced exactly once, got %d spans", len(spans))
	}
}

// mapValueHolderProxy keeps one delegate directly and a second in a map of
// value holders with an unexported client field.
type mapValueHolderProxy struct {
	redis.UniversalClient
	holders map[string]struct{ client redis.UniversalClient }
}

func (r *mapValueHolderProxy) AddHook(hook redis.Hook) {
	r.UniversalClient.AddHook(hook)
	for _, h := range r.holders {
		h.client.AddHook(hook)
	}
}

// A delegate held in the unexported field of a value struct stored in a map
// must be discovered — map values are not addressable, so the holder is read
// through an addressable copy — and traced like a directly held one.
func TestWrapClientMapValueHolderMember(t *testing.T) {
	cfg := new(clientConfig)
	defaults(cfg)

	mt := mocktracer.Start()
	defer mt.Stop()

	current := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1"})
	t.Cleanup(func() { current.Close() })
	hidden := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1"})
	t.Cleanup(func() { hidden.Close() })
	proxy := &mapValueHolderProxy{
		UniversalClient: current,
		holders: map[string]struct{ client redis.UniversalClient }{
			"a": {client: hidden},
		},
	}
	WrapClient(proxy)

	_ = hidden.Get("foo").Err()
	if spans := commandSpans(mt, cfg.spanName); len(spans) != 1 {
		t.Fatalf("expected the map-held delegate to be traced exactly once, got %d spans", len(spans))
	}
}

// discardHook is a hook that wraps nothing.
type discardHook struct{}

func (discardHook) BeforeProcess(ctx context.Context, cmd redis.Cmder) (context.Context, error) {
	return ctx, nil
}

func (discardHook) AfterProcess(ctx context.Context, cmd redis.Cmder) error {
	return nil
}

func (discardHook) BeforeProcessPipeline(ctx context.Context, cmds []redis.Cmder) (context.Context, error) {
	return ctx, nil
}

func (discardHook) AfterProcessPipeline(ctx context.Context, cmds []redis.Cmder) error {
	return nil
}

// The hook slice a client exposes must be a snapshot: a hook added after the
// read must not change what the earlier value reports, or a caller inspecting
// it races with the concurrent append.
func TestHookSliceSnapshot(t *testing.T) {
	client := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1"})
	t.Cleanup(func() { client.Close() })

	hooks := hookSlice(client)
	before := hooks.Len()
	client.AddHook(discardHook{})
	if after := hooks.Len(); after != before {
		t.Fatalf("the returned hook slice changed after AddHook: %d became %d", before, after)
	}
}

// valueRetainProxy is passed by value and shares its retained-hook store
// through a pointer field; every hook is also fanned out to its member.
type valueRetainProxy struct {
	redis.UniversalClient
	Retained *[]redis.Hook
}

func (r valueRetainProxy) AddHook(hook redis.Hook) {
	*r.Retained = append(*r.Retained, hook)
	r.UniversalClient.AddHook(hook)
}

// A value proxy has no registry entry — no weak key can track it — so a
// repeated wrap must not hand it another real hook: its members would carry
// one datadog hook per wrap and trace every command once per hook.
func TestWrapClientValueProxyRepeat(t *testing.T) {
	cfg := new(clientConfig)
	defaults(cfg)

	mt := mocktracer.Start()
	defer mt.Stop()

	member := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1"})
	t.Cleanup(func() { member.Close() })
	store := []redis.Hook{}
	proxy := valueRetainProxy{UniversalClient: member, Retained: &store}

	WrapClient(proxy)
	WrapClient(proxy)
	WrapClient(proxy)

	if n := datadogHooks(member); n != 1 {
		t.Fatalf("expected exactly 1 datadog hook after 3 wraps, got %d", n)
	}
	_ = member.Get("foo").Err()
	if spans := commandSpans(mt, cfg.spanName); len(spans) != 1 {
		t.Fatalf("expected exactly 1 command span, got %d", len(spans))
	}
}

// delegatingProxy's AddHook delegates the wrap to another goroutine and
// synchronously waits for it — a common delegation pattern.
type delegatingProxy struct {
	redis.UniversalClient
}

func (r *delegatingProxy) AddHook(hook redis.Hook) {
	done := make(chan struct{})
	go func() {
		WrapClient(r)
		close(done)
	}()
	<-done
	r.UniversalClient.AddHook(hook)
}

// A user callback that delegates a WrapClient call to another goroutine and
// waits for it must not deadlock against the walk guard: the nested call
// gives up its wait — the in-flight wrap instruments the client — and both
// return.
func TestWrapClientDelegatingAddHook(t *testing.T) {
	cfg := new(clientConfig)
	defaults(cfg)

	mt := mocktracer.Start()
	defer mt.Stop()

	current := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1"})
	t.Cleanup(func() { current.Close() })
	proxy := &delegatingProxy{UniversalClient: current}

	done := make(chan struct{})
	go func() {
		WrapClient(proxy)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(75 * time.Second):
		t.Fatal("WrapClient deadlocked: a delegated nested wrap blocked on the outer walk guard")
	}

	_ = current.Get("foo").Err()
	if spans := commandSpans(mt, cfg.spanName); len(spans) != 1 {
		t.Fatalf("expected exactly 1 command span, got %d", len(spans))
	}
}

// deferredMember has no readable hook chain and an AddHook that must never
// be called against an unreadable chain: a constructor re-entering WrapClient
// while the upstream AddHook holds the hook mutex would deadlock against it.
type deferredMember struct {
	redis.UniversalClient
	addHooked atomic.Bool
}

func (m *deferredMember) AddHook(hook redis.Hook) {
	m.addHooked.Store(true)
}

// An unreadable chain defers the installation: the weak identity is
// registered (a later wrap once the chain is readable installs) and the
// member's AddHook is never invoked against a chain whose mutex may be held
// by the very constructor this call re-entered from.
func TestWrapMemberDefersUnreadableChain(t *testing.T) {
	cfg := new(clientConfig)
	defaults(cfg)

	member := &deferredMember{}
	wrapMu.Lock()
	wrapMember(member, cfg, func() {})
	wrapMu.Unlock()

	if member.addHooked.Load() {
		t.Fatal("AddHook was invoked against an unreadable chain")
	}
	if k, ok := rediswrap.HandleOf(member); ok {
		wrapMu.Lock()
		_, registered := wrapped[k]
		wrapMu.Unlock()
		if !registered {
			t.Fatal("the weak identity was not registered for the deferred installation")
		}
	}
}

// mapKeyRetainProxy fans hooks out to its current member and keeps them in
// a hook set: the hooks are the map's keys.
type mapKeyRetainProxy struct {
	redis.UniversalClient
	hooks map[redis.Hook]struct{}
}

func (r *mapKeyRetainProxy) AddHook(hook redis.Hook) {
	if r.hooks == nil {
		r.hooks = map[redis.Hook]struct{}{}
	}
	r.hooks[hook] = struct{}{}
	r.UniversalClient.AddHook(hook)
}

func (r *mapKeyRetainProxy) applyTo(delegate redis.UniversalClient) {
	for hook := range r.hooks {
		delegate.AddHook(hook)
	}
}

// A proxy that keeps hooks as map keys retains them like any other store:
// the scan must look at the keys, so the real hook is handed to the proxy
// and delegates it instruments later are traced.
func TestWrapClientMapKeyRetain(t *testing.T) {
	cfg := new(clientConfig)
	defaults(cfg)

	mt := mocktracer.Start()
	defer mt.Stop()

	current := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1"})
	t.Cleanup(func() { current.Close() })
	proxy := &mapKeyRetainProxy{UniversalClient: current}
	WrapClient(proxy)

	later := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1"})
	t.Cleanup(func() { later.Close() })
	proxy.applyTo(later)

	_ = later.Get("foo").Err()
	if spans := commandSpans(mt, cfg.spanName); len(spans) != 1 {
		t.Fatalf("expected the later delegate to be traced exactly once, got %d spans", len(spans))
	}
}

// unguardedValueProxy is passed by value; its mutex is a copy that guards
// nothing, and its holder reaches the original's shared state. AddHook is
// promoted from the embedded interface: hooks fan out to the current
// delegate.
type unguardedValueProxy struct {
	redis.UniversalClient
	Mu     sync.Mutex
	Holder *unguardedHolder
}

// unguardedHolder has no mutex of its own: the original proxy's mutex
// guards its delegate field.
type unguardedHolder struct {
	delegate redis.UniversalClient
}

// The field walk over a value copy with a mutex it cannot take must not
// descend into the copy's shared containers: the original's mutex guards
// them, and reading them would race the concurrent update. The copy's own
// header fields are snapshots and stay readable.
func TestWrapClientUnguardedValueCopy(t *testing.T) {
	cfg := new(clientConfig)
	defaults(cfg)

	mt := mocktracer.Start()
	defer mt.Stop()

	current := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1"})
	t.Cleanup(func() { current.Close() })
	extraClient := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1"})
	t.Cleanup(func() { extraClient.Close() })
	other := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1"})
	t.Cleanup(func() { other.Close() })
	holder := &unguardedHolder{delegate: extraClient}
	original := struct {
		mu sync.Mutex
	}{}

	// The mutator swaps the shared holder's delegate under the original's
	// mutex in a tight loop: the copy cannot take that mutex, so a walk
	// that descends into the copy's holder reads the field while the
	// writes land.
	var wg sync.WaitGroup
	var writes atomic.Int64
	wg.Go(func() {
		for range 200000 {
			original.mu.Lock()
			holder.delegate = other
			holder.delegate = extraClient
			original.mu.Unlock()
			writes.Add(1)
		}
	})
	for writes.Load() < 1000 {
		// The mutator is mid-write: the wrap below overlaps it.
	}

	// The copy is made before the mutator has written, and the literal's
	// mutex is a fresh zero value: nothing in the call itself races — only
	// the walk's descent into the shared holder would.
	WrapClient(unguardedValueProxy{
		UniversalClient: current,
		Mu:              sync.Mutex{},
		Holder:          holder,
	})
	wg.Wait()

	if n := datadogHooks(current); n != 1 {
		t.Fatalf("expected the header member to be instrumented exactly once, got %d", n)
	}
	_ = current.Get("foo").Err()
	if spans := commandSpans(mt, cfg.spanName); len(spans) != 1 {
		t.Fatalf("expected exactly 1 command span, got %d", len(spans))
	}
}

// mapKeyedSetProxy keeps one delegate directly and the rest in a client-keyed
// set: the members are the map's keys.
type mapKeyedSetProxy struct {
	redis.UniversalClient
	set map[redis.UniversalClient]struct{}
}

func (r *mapKeyedSetProxy) AddHook(hook redis.Hook) {
	r.UniversalClient.AddHook(hook)
	for member := range r.set {
		member.AddHook(hook)
	}
}

// Delegates held as map keys — map[redis.UniversalClient]struct{} — are
// members like any other: the walk must find them, so every backend is
// instrumented and traced.
func TestWrapClientMapKeyedMemberSet(t *testing.T) {
	cfg := new(clientConfig)
	defaults(cfg)

	mt := mocktracer.Start()
	defer mt.Stop()

	current := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1"})
	t.Cleanup(func() { current.Close() })
	keyed := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1"})
	t.Cleanup(func() { keyed.Close() })
	proxy := &mapKeyedSetProxy{
		UniversalClient: current,
		set:             map[redis.UniversalClient]struct{}{keyed: {}},
	}
	WrapClient(proxy)

	_ = keyed.Get("foo").Err()
	if spans := commandSpans(mt, cfg.spanName); len(spans) != 1 {
		t.Fatalf("expected the key-held member to be traced exactly once, got %d spans", len(spans))
	}
}

// storeIdentityProxy is a non-comparable value proxy — its map field makes ==
// illegal — whose only distinguishing references are the stores it holds:
// the other proxy sits behind a closure, which the identity scan cannot see.
type storeIdentityProxy struct {
	redis.UniversalClient
	other func() storeIdentityProxy
	Store map[string]int
	name  string
	trace *[]string
}

func (r storeIdentityProxy) AddHook(hook redis.Hook) {
	if r.trace != nil {
		*r.trace = append(*r.trace, r.name+".addhook")
	}
	WrapClient(r.other())
}

// Two distinct non-comparable value proxies with the same members that
// differ only in their map or slice backing storage must not be equated by
// the reentry guard: a nested wrap of the second still reaches its AddHook.
func TestWrapClientStoreIdentityDistinguishesProxies(t *testing.T) {
	cfg := new(clientConfig)
	defaults(cfg)

	var order []string
	pa := &storeIdentityProxy{Store: map[string]int{"a": 1}, name: "pa", trace: &order}
	pb := &storeIdentityProxy{Store: map[string]int{"b": 2}, name: "pb", trace: &order}
	pa.other = func() storeIdentityProxy { return *pb }
	pb.other = func() storeIdentityProxy { return *pa }

	WrapClient(*pa)
	found := false
	for _, e := range order {
		if e == "pb.addhook" {
			found = true
		}
	}
	if !found {
		t.Fatal("the second proxy's AddHook was never invoked: the reentry guard equated two distinct proxies")
	}
	_ = cfg
}

// slowInFlightProxy's AddHook takes longer than the walk guard's bound, so a
// concurrent wrap of the same client proceeds degraded and waits for the
// install. It signals once its wrap has reached the AddHook — the guard and
// the in-flight install are held from there.
type slowInFlightProxy struct {
	redis.UniversalClient
	delay   time.Duration
	started chan struct{}
}

func (r *slowInFlightProxy) AddHook(hook redis.Hook) {
	if r.started != nil {
		close(r.started)
	}
	time.Sleep(r.delay)
	r.UniversalClient.AddHook(hook)
}

// A wrap that waits out a concurrent install must not report completion
// before that install has finished: the command right after WrapClient
// returns is traced.
func TestWrapClientWaitsOutSlowInstall(t *testing.T) {
	cfg := new(clientConfig)
	defaults(cfg)

	mt := mocktracer.Start()
	defer mt.Stop()

	member := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1"})
	t.Cleanup(func() { member.Close() })
	started := make(chan struct{})
	proxy := &slowInFlightProxy{UniversalClient: member, delay: 3 * time.Second, started: started}

	done := make(chan struct{})
	go func() {
		defer close(done)
		WrapClient(proxy)
	}()
	<-started // the concurrent wrap holds the guard; its install is in flight
	// The concurrent wrap holds the guard while its slow AddHook runs; this
	// wrap waits it out and must return only once the hook is active — the
	// command right after it returns is traced.
	WrapClient(proxy)
	_ = member.Get("foo").Err()
	if spans := commandSpans(mt, cfg.spanName); len(spans) != 1 {
		t.Fatalf("expected the command to be traced once the wrap returned, got %d spans", len(spans))
	}
	<-done
}

// readerHeldProxy guards its fields with a value RWMutex, read-locked by
// the caller while it wraps.
type readerHeldProxy struct {
	redis.UniversalClient
	mu sync.RWMutex
}

func (r *readerHeldProxy) AddHook(hook redis.Hook) {
	r.UniversalClient.AddHook(hook)
}

// A read lock held by the calling goroutine — a proxy method wrapping from
// inside its own reader — must not read as contention: the walk takes a
// read lock alongside it and the member is still instrumented.
func TestWrapClientUnderHeldReadLock(t *testing.T) {
	cfg := new(clientConfig)
	defaults(cfg)

	mt := mocktracer.Start()
	defer mt.Stop()

	member := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1"})
	t.Cleanup(func() { member.Close() })
	proxy := &readerHeldProxy{UniversalClient: member}

	proxy.mu.RLock()
	defer proxy.mu.RUnlock()
	WrapClient(proxy)

	if n := datadogHooks(member); n != 1 {
		t.Fatalf("expected the member to be instrumented under the held read lock, got %d hooks", n)
	}
	_ = member.Get("foo").Err()
	if spans := commandSpans(mt, cfg.spanName); len(spans) != 1 {
		t.Fatalf("expected exactly 1 command span, got %d", len(spans))
	}
}

// ptrReaderHeldProxy guards its fields with a pointer RWMutex, read-locked
// by the caller while it wraps.
type ptrReaderHeldProxy struct {
	redis.UniversalClient
	mu *sync.RWMutex
}

func (r *ptrReaderHeldProxy) AddHook(hook redis.Hook) {
	r.UniversalClient.AddHook(hook)
}

func TestWrapClientUnderHeldPtrReadLock(t *testing.T) {
	cfg := new(clientConfig)
	defaults(cfg)

	mt := mocktracer.Start()
	defer mt.Stop()

	member := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1"})
	t.Cleanup(func() { member.Close() })
	mu := &sync.RWMutex{}
	proxy := &ptrReaderHeldProxy{UniversalClient: member, mu: mu}

	mu.RLock()
	defer mu.RUnlock()
	WrapClient(proxy)

	if n := datadogHooks(member); n != 1 {
		t.Fatalf("expected the member to be instrumented under the held read lock, got %d hooks", n)
	}
}

// slowPanickingAfterBoundProxy's first AddHook outlasts the install wait
// and then panics, so a concurrent wrap's wait times out before it can see
// the failure: the background watcher retries the installation, and the
// retry's AddHook succeeds.
type slowPanickingAfterBoundProxy struct {
	redis.UniversalClient
	started chan struct{}
	once    sync.Once
	boom    atomic.Bool
}

func (r *slowPanickingAfterBoundProxy) AddHook(hook redis.Hook) {
	r.once.Do(func() { close(r.started) })
	if r.boom.CompareAndSwap(true, false) {
		time.Sleep(33 * time.Second)
		panic("constructor boom")
	}
	r.UniversalClient.AddHook(hook)
}

func TestWrapClientWatcherRetriesFailedInstall(t *testing.T) {
	cfg := new(clientConfig)
	defaults(cfg)

	mt := mocktracer.Start()
	defer mt.Stop()

	member := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1"})
	t.Cleanup(func() { member.Close() })
	started := make(chan struct{})
	proxy := &slowPanickingAfterBoundProxy{UniversalClient: member, started: started}
	proxy.boom.Store(true)

	go func() {
		defer func() { _ = recover() }()
		WrapClient(proxy)
	}()
	<-started // the slow install is in flight, guard and entry held

	// The waiter outlasts the install wait and returns; the watcher retries
	// once the original install panics and drops its entry. The retry
	// settles an entry for the proxy — wait for it before reading the
	// member's chain, so the read does not race the retry's writes.
	WrapClient(proxy)

	k, _ := rediswrap.HandleOf(proxy)
	deadline := time.After(60 * time.Second)
	for {
		wrapMu.Lock()
		e, ok := wrapped[k]
		settled := ok && e.done == nil
		wrapMu.Unlock()
		if settled {
			break
		}
		select {
		case <-deadline:
			t.Fatal("the watcher never retried the failed install")
		case <-time.After(100 * time.Millisecond):
		}
	}
	if n := datadogHooks(member); n != 1 {
		t.Fatalf("expected exactly 1 datadog hook after the retry, got %d", n)
	}
}

// cyclicAnyProxy holds a self-referential value behind an any field: the
// retention scan crosses an interface and a pointer per turn.
type cyclicAnyProxy struct {
	redis.UniversalClient
	Box any
}

func (r *cyclicAnyProxy) AddHook(hook redis.Hook) {
	r.UniversalClient.AddHook(hook)
}

// A self-referential any — x = &x — must not drive the retention scan into
// unbounded recursion: every indirection consumes the depth limit.
func TestWrapClientCyclicAnyScan(t *testing.T) {
	cfg := new(clientConfig)
	defaults(cfg)

	mt := mocktracer.Start()
	defer mt.Stop()

	member := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1"})
	t.Cleanup(func() { member.Close() })
	var x any
	x = &x
	proxy := &cyclicAnyProxy{UniversalClient: member, Box: x}
	WrapClient(proxy)

	_ = member.Get("foo").Err()
	if spans := commandSpans(mt, cfg.spanName); len(spans) != 1 {
		t.Fatalf("expected exactly 1 command span, got %d", len(spans))
	}
}

// hookBox holds a hook inside a holder, so a store of boxes is a hook
// collection one level down.
type hookBox struct {
	Hook redis.Hook
}

// boxedRetainProxy fans hooks out to its current member and retains them
// in a slice of holders.
type boxedRetainProxy struct {
	redis.UniversalClient
	retained []hookBox
}

func (r *boxedRetainProxy) AddHook(hook redis.Hook) {
	r.retained = append(r.retained, hookBox{Hook: hook})
	r.UniversalClient.AddHook(hook)
}

func (r *boxedRetainProxy) applyTo(delegate redis.UniversalClient) {
	for _, box := range r.retained {
		delegate.AddHook(box.Hook)
	}
}

// A proxy that keeps hooks inside holder values — []struct{ Hook
// redis.Hook } — retains them like any other store: the scan recurses into
// the collection, the real hook is handed to the proxy, and delegates it
// instruments later are traced.
func TestWrapClientBoxedHookStore(t *testing.T) {
	cfg := new(clientConfig)
	defaults(cfg)

	mt := mocktracer.Start()
	defer mt.Stop()

	current := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1"})
	t.Cleanup(func() { current.Close() })
	proxy := &boxedRetainProxy{UniversalClient: current}
	WrapClient(proxy)

	later := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1"})
	t.Cleanup(func() { later.Close() })
	proxy.applyTo(later)

	_ = later.Get("foo").Err()
	if spans := commandSpans(mt, cfg.spanName); len(spans) != 1 {
		t.Fatalf("expected the later delegate to be traced exactly once, got %d spans", len(spans))
	}
}

// memberHolder keeps a delegate behind its own mutex; contention on it
// hides the member from a first walk.
type memberHolder struct {
	mu     sync.Mutex
	member redis.UniversalClient
}

// contendedHolderProxy has one delegate directly and one inside a holder,
// and its AddHook is slow enough that the holder's contention window passes
// while the probe runs.
type contendedHolderProxy struct {
	redis.UniversalClient
	holder *memberHolder
}

func (r *contendedHolderProxy) AddHook(hook redis.Hook) {
	time.Sleep(200 * time.Millisecond)
	r.UniversalClient.AddHook(hook)
	r.holder.member.AddHook(hook)
}

// A member inside a holder whose mutex outlasts the walk's own retry window
// must not be left with the no-op probe: the probe still reaches it through
// the proxy's fan-out, and the wrap re-walks once the contention has passed
// and instruments what the first walk could not see.
func TestWrapClientContendedHolderMember(t *testing.T) {
	cfg := new(clientConfig)
	defaults(cfg)

	mt := mocktracer.Start()
	defer mt.Stop()

	visible := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1"})
	t.Cleanup(func() { visible.Close() })
	hidden := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1"})
	t.Cleanup(func() { hidden.Close() })
	holder := &memberHolder{member: hidden}
	proxy := &contendedHolderProxy{UniversalClient: visible, holder: holder}

	// Hold the holder's mutex past the walk's lock-retry window.
	go func() {
		holder.mu.Lock()
		time.Sleep(150 * time.Millisecond)
		holder.mu.Unlock()
	}()
	time.Sleep(5 * time.Millisecond) // the hold is in place before the wrap

	WrapClient(proxy)

	_ = hidden.Get("foo").Err()
	if spans := commandSpans(mt, cfg.spanName); len(spans) != 1 {
		t.Fatalf("expected the holder member to be traced exactly once, got %d spans", len(spans))
	}
}

// mapHolderRetainProxy keeps hooks in a map of holders with an unexported
// hook field, and fans them out to its current member.
type mapHolderRetainProxy struct {
	redis.UniversalClient
	store map[string]struct{ hook redis.Hook }
}

func (r *mapHolderRetainProxy) AddHook(hook redis.Hook) {
	if r.store == nil {
		r.store = map[string]struct{ hook redis.Hook }{}
	}
	r.store[fmt.Sprintf("hook-%d", len(r.store))] = struct{ hook redis.Hook }{hook: hook}
	r.UniversalClient.AddHook(hook)
}

func (r *mapHolderRetainProxy) applyTo(delegate redis.UniversalClient) {
	for _, h := range r.store {
		delegate.AddHook(h.hook)
	}
}

// Hooks held in the unexported field of a struct stored in a map must be
// found: the scan copies the map value into an addressable snapshot before
// reading the field, so a retaining proxy keeps its real hook and delegates
// it instruments later are traced.
func TestWrapClientMapHolderUnexported(t *testing.T) {
	cfg := new(clientConfig)
	defaults(cfg)

	mt := mocktracer.Start()
	defer mt.Stop()

	current := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1"})
	t.Cleanup(func() { current.Close() })
	proxy := &mapHolderRetainProxy{UniversalClient: current}
	WrapClient(proxy)

	later := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1"})
	t.Cleanup(func() { later.Close() })
	proxy.applyTo(later)

	_ = later.Get("foo").Err()
	if spans := commandSpans(mt, cfg.spanName); len(spans) != 1 {
		t.Fatalf("expected the later delegate to be traced exactly once, got %d spans", len(spans))
	}
}

// barrierValueRetainProxy's AddHook waits for another wrap's AddHook to
// arrive before installing, so two concurrent wraps would both observe
// before either installs.
type barrierValueRetainProxy struct {
	redis.UniversalClient
	Retained *[]redis.Hook
	arrived  chan struct{}
	release  chan struct{}
}

func (r barrierValueRetainProxy) AddHook(hook redis.Hook) {
	select {
	case r.arrived <- struct{}{}:
	case <-time.After(50 * time.Millisecond):
	}
	select {
	case <-r.release:
	case <-time.After(500 * time.Millisecond):
	}
	*r.Retained = append(*r.Retained, hook)
	r.UniversalClient.AddHook(hook)
}

// Two concurrent wraps of the same value-based fan-out-and-retain proxy
// must not each hand it a real hook: the installation is serialized on the
// proxy's reference identity, and the second wrap re-decides once the first
// has finished.
func TestWrapClientConcurrentValueProxy(t *testing.T) {
	cfg := new(clientConfig)
	defaults(cfg)

	mt := mocktracer.Start()
	defer mt.Stop()

	member := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1"})
	t.Cleanup(func() { member.Close() })
	store := []redis.Hook{}
	arrived := make(chan struct{}, 4)
	release := make(chan struct{})
	proxy := barrierValueRetainProxy{UniversalClient: member, Retained: &store, arrived: arrived, release: release}
	go func() {
		for range 2 {
			<-arrived
		}
		close(release)
	}()

	var wg sync.WaitGroup
	wg.Go(func() { WrapClient(proxy) })
	wg.Go(func() { WrapClient(proxy) })
	wg.Wait()

	if n := datadogHooks(member); n != 1 {
		t.Fatalf("expected exactly 1 datadog hook after the concurrent wraps, got %d", n)
	}
	_ = member.Get("foo").Err()
	if spans := commandSpans(mt, cfg.spanName); len(spans) != 1 {
		t.Fatalf("expected exactly 1 command span, got %d", len(spans))
	}
}

// anyKeyRetainProxy keeps hooks as the keys of a generic set.
type anyKeyRetainProxy struct {
	redis.UniversalClient
	set map[any]struct{}
}

func (r *anyKeyRetainProxy) AddHook(hook redis.Hook) {
	if r.set == nil {
		r.set = map[any]struct{}{}
	}
	r.set[hook] = struct{}{}
	r.UniversalClient.AddHook(hook)
}

func (r *anyKeyRetainProxy) applyTo(delegate redis.UniversalClient) {
	for hook := range r.set {
		delegate.AddHook(hook.(redis.Hook))
	}
}

// Hooks held behind the interface keys of a generic set — map[any]struct{} —
// are retained like any other store: the scan walks the keys, so the real
// hook is handed to the proxy and delegates it instruments later are
// traced.
func TestWrapClientAnyKeyHookSet(t *testing.T) {
	cfg := new(clientConfig)
	defaults(cfg)

	mt := mocktracer.Start()
	defer mt.Stop()

	current := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1"})
	t.Cleanup(func() { current.Close() })
	proxy := &anyKeyRetainProxy{UniversalClient: current}
	WrapClient(proxy)

	later := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1"})
	t.Cleanup(func() { later.Close() })
	proxy.applyTo(later)

	_ = later.Get("foo").Err()
	if spans := commandSpans(mt, cfg.spanName); len(spans) != 1 {
		t.Fatalf("expected the later delegate to be traced exactly once, got %d spans", len(spans))
	}
}

// ptrMutexUnguardedProxy is passed by value; its mutex sits behind an
// unexported pointer field a copy cannot read, and its holder reaches the
// original's shared state.
type ptrMutexUnguardedProxy struct {
	redis.UniversalClient
	mu     *sync.Mutex // unexported: a copy cannot read it to take it
	Holder *unguardedHolder
}

// A value copy with an unexported pointer mutex must be treated as
// unguarded: the mutex guards the shared holder the copy reaches, and the
// copy can neither take it nor read the pointer to try. The walk stays at
// the headers and does not race the concurrent update.
func TestWrapClientUnexportedPtrMutexValueCopy(t *testing.T) {
	cfg := new(clientConfig)
	defaults(cfg)

	mt := mocktracer.Start()
	defer mt.Stop()

	current := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1"})
	t.Cleanup(func() { current.Close() })
	extraClient := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1"})
	t.Cleanup(func() { extraClient.Close() })
	holder := &unguardedHolder{delegate: extraClient}
	original := struct {
		mu     *sync.Mutex
		holder *unguardedHolder
	}{mu: &sync.Mutex{}, holder: holder}

	var wg sync.WaitGroup
	var writes atomic.Int64
	wg.Go(func() {
		for range 200000 {
			original.mu.Lock()
			holder.delegate = extraClient
			holder.delegate = extraClient
			original.mu.Unlock()
			writes.Add(1)
		}
	})
	for writes.Load() < 1000 {
		// The mutator is mid-write: the wrap below overlaps it.
	}

	// The copy is made before the mutator has settled, and the literal's
	// mutex is a fresh value whose pointer the copy shares: nothing in the
	// call itself races — only the walk's descent into the shared holder
	// would.
	WrapClient(ptrMutexUnguardedProxy{
		UniversalClient: current,
		mu:              &sync.Mutex{},
		Holder:          holder,
	})
	wg.Wait()

	if n := datadogHooks(current); n != 1 {
		t.Fatalf("expected the header member to be instrumented exactly once, got %d", n)
	}
	_ = current.Get("foo").Err()
	if spans := commandSpans(mt, cfg.spanName); len(spans) != 1 {
		t.Fatalf("expected exactly 1 command span, got %d", len(spans))
	}
}

// retainOnlySwapProxy retains every hook for delegates it creates later and
// never hooks its current member; its member can be swapped.
type retainOnlySwapProxy struct {
	redis.UniversalClient
	retained []redis.Hook
}

func (r *retainOnlySwapProxy) AddHook(hook redis.Hook) {
	r.retained = append(r.retained, hook)
}

func (r *retainOnlySwapProxy) applyTo(delegate redis.UniversalClient) {
	for _, hook := range r.retained {
		delegate.AddHook(hook)
	}
}

// A retain-only proxy whose current member is swapped for another unhooked
// client must not be re-observed: its members stay unhooked by design, and
// re-observation would hand it a second real hook whose application to a
// later delegate traces every command twice.
func TestWrapClientRetainOnlySwapNoRehand(t *testing.T) {
	cfg := new(clientConfig)
	defaults(cfg)

	mt := mocktracer.Start()
	defer mt.Stop()

	member1 := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1"})
	t.Cleanup(func() { member1.Close() })
	member2 := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1"})
	t.Cleanup(func() { member2.Close() })
	proxy := &retainOnlySwapProxy{UniversalClient: member1}

	WrapClient(proxy)
	proxy.UniversalClient = member2 // swap the current member
	WrapClient(proxy)

	later := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1"})
	t.Cleanup(func() { later.Close() })
	proxy.applyTo(later)

	_ = later.Get("foo").Err()
	if spans := commandSpans(mt, cfg.spanName); len(spans) != 1 {
		t.Fatalf("expected the later delegate to be traced exactly once, got %d spans", len(spans))
	}
}

// boxedKeyRetainProxy keeps hooks in a generic set whose keys are holder
// structs behind interfaces, with the hook in an unexported field.
type boxedKeyRetainProxy struct {
	redis.UniversalClient
	set map[any]struct{}
}

type boxedHookHolder struct {
	hook redis.Hook
}

func (r *boxedKeyRetainProxy) AddHook(hook redis.Hook) {
	if r.set == nil {
		r.set = map[any]struct{}{}
	}
	r.set[boxedHookHolder{hook: hook}] = struct{}{}
	r.UniversalClient.AddHook(hook)
}

func (r *boxedKeyRetainProxy) applyTo(delegate redis.UniversalClient) {
	for k := range r.set {
		delegate.AddHook(k.(boxedHookHolder).hook)
	}
}

// Hooks held behind interface-boxed holder keys — map[any]struct{} whose
// keys are holder structs with unexported hook fields — are retained like
// any other store: the scan unwraps the key's interface, snapshots the
// holder, and reads the field, so the real hook is handed to the proxy and
// delegates it instruments later are traced.
func TestWrapClientBoxedKeyHookSet(t *testing.T) {
	cfg := new(clientConfig)
	defaults(cfg)

	mt := mocktracer.Start()
	defer mt.Stop()

	current := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1"})
	t.Cleanup(func() { current.Close() })
	proxy := &boxedKeyRetainProxy{UniversalClient: current}
	WrapClient(proxy)

	later := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1"})
	t.Cleanup(func() { later.Close() })
	proxy.applyTo(later)

	_ = later.Get("foo").Err()
	if spans := commandSpans(mt, cfg.spanName); len(spans) != 1 {
		t.Fatalf("expected the later delegate to be traced exactly once, got %d spans", len(spans))
	}
}
