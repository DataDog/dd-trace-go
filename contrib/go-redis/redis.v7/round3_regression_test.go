// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package redis

import (
	"testing"

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
