// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package redis

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"

	rediswrap "github.com/DataDog/dd-trace-go/contrib/internal/rediswrap/v2"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/mocktracer"

	"github.com/go-redis/redis/v7"
)

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
	case <-time.After(5 * time.Second):
		t.Fatal("WrapClient deadlocked: a delegated nested wrap blocked on the outer walk guard")
	}

	_ = current.Get("foo").Err()
	if spans := commandSpans(mt, cfg.spanName); len(spans) != 1 {
		t.Fatalf("expected exactly 1 command span, got %d", len(spans))
	}
}

// panickingMember has no readable hook chain and an AddHook that panics.
type panickingMember struct {
	redis.UniversalClient
}

func (m *panickingMember) AddHook(hook redis.Hook) {
	panic("constructor boom")
}

// A panicking AddHook — recovered by the application — must not leave the
// weak registration behind: every later wrap would trust the entry and
// never install a hook.
func TestWrapMemberPanicRemovesWeakEntry(t *testing.T) {
	cfg := new(clientConfig)
	defaults(cfg)

	member := &panickingMember{}
	wrapMu.Lock()
	func() {
		defer func() { recover() }()
		wrapMember(member, cfg, func() {})
	}()
	wrapMu.Unlock()

	if k, ok := rediswrap.HandleOf(member); ok {
		wrapMu.Lock()
		_, still := wrapped[k]
		wrapMu.Unlock()
		if still {
			t.Fatal("the weak entry survived the panicking AddHook")
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
