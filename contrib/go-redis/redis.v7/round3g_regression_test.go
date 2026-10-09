// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package redis

import (
	"sync"
	"sync/atomic"
	"testing"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/mocktracer"

	"github.com/go-redis/redis/v7"
)

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
