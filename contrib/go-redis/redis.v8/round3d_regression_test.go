// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package redis

import (
	"context"
	"testing"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/mocktracer"

	goredis "github.com/go-redis/redis/v8"
)

// discardHook is a hook that wraps nothing.
type discardHook struct{}

func (discardHook) BeforeProcess(ctx context.Context, cmd goredis.Cmder) (context.Context, error) {
	return ctx, nil
}

func (discardHook) AfterProcess(ctx context.Context, cmd goredis.Cmder) error {
	return nil
}

func (discardHook) BeforeProcessPipeline(ctx context.Context, cmds []goredis.Cmder) (context.Context, error) {
	return ctx, nil
}

func (discardHook) AfterProcessPipeline(ctx context.Context, cmds []goredis.Cmder) error {
	return nil
}

// The hook slice a client exposes must be a snapshot: a hook added after the
// read must not change what the earlier value reports, or a caller inspecting
// it races with the concurrent append.
func TestHookSliceSnapshot(t *testing.T) {
	client := goredis.NewClient(&goredis.Options{Addr: "127.0.0.1:1"})
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
	goredis.UniversalClient
	Retained *[]goredis.Hook
}

func (r valueRetainProxy) AddHook(hook goredis.Hook) {
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

	member := goredis.NewClient(&goredis.Options{Addr: "127.0.0.1:1"})
	t.Cleanup(func() { member.Close() })
	store := []goredis.Hook{}
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
