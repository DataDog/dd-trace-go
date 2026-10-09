// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package redis

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	rediswrap "github.com/DataDog/dd-trace-go/contrib/internal/rediswrap/v2"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/mocktracer"

	goredis "github.com/go-redis/redis/v8"
)

// mapKeyedSetProxy keeps one delegate directly and the rest in a client-keyed
// set: the members are the map's keys.
type mapKeyedSetProxy struct {
	goredis.UniversalClient
	set map[goredis.UniversalClient]struct{}
}

func (r *mapKeyedSetProxy) AddHook(hook goredis.Hook) {
	r.UniversalClient.AddHook(hook)
	for member := range r.set {
		member.AddHook(hook)
	}
}

// Delegates held as map keys — map[goredis.UniversalClient]struct{} — are
// members like any other: the walk must find them, so every backend is
// instrumented and traced.
func TestWrapClientMapKeyedMemberSet(t *testing.T) {
	cfg := new(clientConfig)
	defaults(cfg)

	mt := mocktracer.Start()
	defer mt.Stop()

	current := goredis.NewClient(&goredis.Options{Addr: "127.0.0.1:1"})
	t.Cleanup(func() { current.Close() })
	keyed := goredis.NewClient(&goredis.Options{Addr: "127.0.0.1:1"})
	t.Cleanup(func() { keyed.Close() })
	proxy := &mapKeyedSetProxy{
		UniversalClient: current,
		set:             map[goredis.UniversalClient]struct{}{keyed: {}},
	}
	WrapClient(proxy)

	_ = keyed.Get(context.Background(), "foo").Err()
	if spans := commandSpans(mt, cfg.spanName); len(spans) != 1 {
		t.Fatalf("expected the key-held member to be traced exactly once, got %d spans", len(spans))
	}
}

// storeIdentityProxy is a non-comparable value proxy — its map field makes ==
// illegal — whose only distinguishing references are the stores it holds:
// the other proxy sits behind a closure, which the identity scan cannot see.
type storeIdentityProxy struct {
	goredis.UniversalClient
	other func() storeIdentityProxy
	Store map[string]int
	name  string
	trace *[]string
}

func (r storeIdentityProxy) AddHook(hook goredis.Hook) {
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
	goredis.UniversalClient
	delay   time.Duration
	started chan struct{}
}

func (r *slowInFlightProxy) AddHook(hook goredis.Hook) {
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

	member := goredis.NewClient(&goredis.Options{Addr: "127.0.0.1:1"})
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
	_ = member.Get(context.Background(), "foo").Err()
	if spans := commandSpans(mt, cfg.spanName); len(spans) != 1 {
		t.Fatalf("expected the command to be traced once the wrap returned, got %d spans", len(spans))
	}
	<-done
}

// readerHeldProxy guards its fields with a value RWMutex, read-locked by
// the caller while it wraps.
type readerHeldProxy struct {
	goredis.UniversalClient
	mu sync.RWMutex
}

func (r *readerHeldProxy) AddHook(hook goredis.Hook) {
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

	member := goredis.NewClient(&goredis.Options{Addr: "127.0.0.1:1"})
	t.Cleanup(func() { member.Close() })
	proxy := &readerHeldProxy{UniversalClient: member}

	proxy.mu.RLock()
	defer proxy.mu.RUnlock()
	WrapClient(proxy)

	if n := datadogHooks(member); n != 1 {
		t.Fatalf("expected the member to be instrumented under the held read lock, got %d hooks", n)
	}
	_ = member.Get(context.Background(), "foo").Err()
	if spans := commandSpans(mt, cfg.spanName); len(spans) != 1 {
		t.Fatalf("expected exactly 1 command span, got %d", len(spans))
	}
}

// ptrReaderHeldProxy guards its fields with a pointer RWMutex, read-locked
// by the caller while it wraps.
type ptrReaderHeldProxy struct {
	goredis.UniversalClient
	mu *sync.RWMutex
}

func (r *ptrReaderHeldProxy) AddHook(hook goredis.Hook) {
	r.UniversalClient.AddHook(hook)
}

func TestWrapClientUnderHeldPtrReadLock(t *testing.T) {
	cfg := new(clientConfig)
	defaults(cfg)

	mt := mocktracer.Start()
	defer mt.Stop()

	member := goredis.NewClient(&goredis.Options{Addr: "127.0.0.1:1"})
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
	goredis.UniversalClient
	started chan struct{}
	once    sync.Once
	boom    atomic.Bool
}

func (r *slowPanickingAfterBoundProxy) AddHook(hook goredis.Hook) {
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

	member := goredis.NewClient(&goredis.Options{Addr: "127.0.0.1:1"})
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
	goredis.UniversalClient
	Box any
}

func (r *cyclicAnyProxy) AddHook(hook goredis.Hook) {
	r.UniversalClient.AddHook(hook)
}

// A self-referential any — x = &x — must not drive the retention scan into
// unbounded recursion: every indirection consumes the depth limit.
func TestWrapClientCyclicAnyScan(t *testing.T) {
	cfg := new(clientConfig)
	defaults(cfg)

	mt := mocktracer.Start()
	defer mt.Stop()

	member := goredis.NewClient(&goredis.Options{Addr: "127.0.0.1:1"})
	t.Cleanup(func() { member.Close() })
	var x any
	x = &x
	proxy := &cyclicAnyProxy{UniversalClient: member, Box: x}
	WrapClient(proxy)

	_ = member.Get(context.Background(), "foo").Err()
	if spans := commandSpans(mt, cfg.spanName); len(spans) != 1 {
		t.Fatalf("expected exactly 1 command span, got %d", len(spans))
	}
}

// hookBox holds a hook inside a holder, so a store of boxes is a hook
// collection one level down.
type hookBox struct {
	Hook goredis.Hook
}

// boxedRetainProxy fans hooks out to its current member and retains them
// in a slice of holders.
type boxedRetainProxy struct {
	goredis.UniversalClient
	retained []hookBox
}

func (r *boxedRetainProxy) AddHook(hook goredis.Hook) {
	r.retained = append(r.retained, hookBox{Hook: hook})
	r.UniversalClient.AddHook(hook)
}

func (r *boxedRetainProxy) applyTo(delegate goredis.UniversalClient) {
	for _, box := range r.retained {
		delegate.AddHook(box.Hook)
	}
}

// A proxy that keeps hooks inside holder values — []struct{ Hook
// goredis.Hook } — retains them like any other store: the scan recurses into
// the collection, the real hook is handed to the proxy, and delegates it
// instruments later are traced.
func TestWrapClientBoxedHookStore(t *testing.T) {
	cfg := new(clientConfig)
	defaults(cfg)

	mt := mocktracer.Start()
	defer mt.Stop()

	current := goredis.NewClient(&goredis.Options{Addr: "127.0.0.1:1"})
	t.Cleanup(func() { current.Close() })
	proxy := &boxedRetainProxy{UniversalClient: current}
	WrapClient(proxy)

	later := goredis.NewClient(&goredis.Options{Addr: "127.0.0.1:1"})
	t.Cleanup(func() { later.Close() })
	proxy.applyTo(later)

	_ = later.Get(context.Background(), "foo").Err()
	if spans := commandSpans(mt, cfg.spanName); len(spans) != 1 {
		t.Fatalf("expected the later delegate to be traced exactly once, got %d spans", len(spans))
	}
}

// memberHolder keeps a delegate behind its own mutex; contention on it
// hides the member from a first walk.
type memberHolder struct {
	mu     sync.Mutex
	member goredis.UniversalClient
}

// contendedHolderProxy has one delegate directly and one inside a holder,
// and its AddHook is slow enough that the holder's contention window passes
// while the probe runs.
type contendedHolderProxy struct {
	goredis.UniversalClient
	holder *memberHolder
}

func (r *contendedHolderProxy) AddHook(hook goredis.Hook) {
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

	visible := goredis.NewClient(&goredis.Options{Addr: "127.0.0.1:1"})
	t.Cleanup(func() { visible.Close() })
	hidden := goredis.NewClient(&goredis.Options{Addr: "127.0.0.1:1"})
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

	_ = hidden.Get(context.Background(), "foo").Err()
	if spans := commandSpans(mt, cfg.spanName); len(spans) != 1 {
		t.Fatalf("expected the holder member to be traced exactly once, got %d spans", len(spans))
	}
}
