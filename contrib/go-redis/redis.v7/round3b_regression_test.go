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
