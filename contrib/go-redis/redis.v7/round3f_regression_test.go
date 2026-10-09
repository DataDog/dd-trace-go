// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package redis

import (
	"testing"
	"time"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/mocktracer"

	"github.com/go-redis/redis/v7"
)

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
