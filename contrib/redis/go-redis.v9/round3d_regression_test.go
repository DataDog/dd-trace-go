// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package redis

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/mocktracer"

	"github.com/redis/go-redis/v9"
)

// reentrantWrapHook re-enters WrapClient from the chain rebuild that adding
// another hook to its host client triggers. It stays quiet while it is
// installed — its own installation rebuilds the chain too — and re-enters
// from the next rebuild on.
type reentrantWrapHook struct {
	client redis.UniversalClient
	armed  atomic.Bool
}

func (r *reentrantWrapHook) DialHook(next redis.DialHook) redis.DialHook { return next }

func (r *reentrantWrapHook) ProcessHook(next redis.ProcessHook) redis.ProcessHook {
	if r.armed.Load() {
		WrapClient(r.client)
	}
	return next
}

func (r *reentrantWrapHook) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return next
}

// A concrete client's AddHook rebuilds its hook chain by calling every
// hook's constructors, so a custom hook may re-enter WrapClient from there:
// the installation must run with the package lock released, or the nested
// call deadlocks on it.
func TestWrapClientHookReenters(t *testing.T) {
	cfg := new(clientConfig)
	defaults(cfg)

	mt := mocktracer.Start()
	defer mt.Stop()

	client := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1"})
	t.Cleanup(func() { client.Close() })
	hook := &reentrantWrapHook{client: client}
	client.AddHook(hook)
	hook.armed.Store(true)

	done := make(chan struct{})
	go func() {
		WrapClient(client)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("WrapClient deadlocked: the client's AddHook rebuilt its hook chain and re-entered WrapClient while the package lock was held")
	}

	if n := datadogHooks(client); n != 1 {
		t.Fatalf("expected exactly 1 datadog hook after the re-entry, got %d", n)
	}
	_ = client.Get(context.Background(), "foo").Err()
	if spans := commandSpans(mt, cfg.spanName); len(spans) != 1 {
		t.Fatalf("expected exactly 1 command span, got %d", len(spans))
	}
}

// discardHook is a hook that wraps nothing.
type discardHook struct{}

func (discardHook) DialHook(next redis.DialHook) redis.DialHook { return next }

func (discardHook) ProcessHook(next redis.ProcessHook) redis.ProcessHook { return next }

func (discardHook) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return next
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
	_ = member.Get(context.Background(), "foo").Err()
	if spans := commandSpans(mt, cfg.spanName); len(spans) != 1 {
		t.Fatalf("expected exactly 1 command span, got %d", len(spans))
	}
}
