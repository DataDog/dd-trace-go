// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package redis

import (
	"context"
	"testing"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/mocktracer"

	goredis "github.com/redis/go-redis/v9"
)

// cyclicProxy holds a self-referential pointer field.
type cyclicProxy struct {
	goredis.UniversalClient
	next *cyclicProxy
}

func (r *cyclicProxy) AddHook(hook goredis.Hook) {
	r.UniversalClient.AddHook(hook)
}

func TestWrapClientCyclicProxy(t *testing.T) {
	cfg := new(clientConfig)
	defaults(cfg)

	mt := mocktracer.Start()
	defer mt.Stop()

	current := goredis.NewClient(&goredis.Options{Addr: "127.0.0.1:1"})
	t.Cleanup(func() { current.Close() })
	p := &cyclicProxy{UniversalClient: current}
	p.next = p
	WrapClient(p)

	_ = current.Get(context.Background(), "foo").Err()
	if spans := commandSpans(mt, cfg.spanName); len(spans) != 1 {
		t.Fatalf("expected exactly 1 command span, got %d", len(spans))
	}
}

// refDistinctionProxy is a non-comparable proxy type — its map field makes
// == illegal — passed by value, with an empty member set. Its exported
// pointer fields are its only identity: the guard must compare them before
// equating two distinct proxies through their (empty) member sets.
type refDistinctionProxy struct {
	goredis.UniversalClient
	ID    *int
	Other *refDistinctionProxy
	Tags  map[string]string
	name  string
	trace *[]string
}

func (r refDistinctionProxy) AddHook(hook goredis.Hook) {
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
