// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package redis

import (
	"context"
	"reflect"
	"sync"
	"testing"
	"time"
	"unsafe"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/mocktracer"

	"github.com/redis/go-redis/v9"
)

// twoMemberProxy fans every hook out to both members.
type twoMemberProxy struct {
	redis.UniversalClient // nil: both delegates live in a and b
	a, b                  redis.UniversalClient
}

func (r *twoMemberProxy) AddHook(hook redis.Hook) {
	r.a.AddHook(hook)
	r.b.AddHook(hook)
}

// hookChainMu returns the mutex go-redis guards a client's hook chain with.
func hookChainMu(client *redis.Client) *sync.RWMutex {
	v := reflect.ValueOf(client).Elem().FieldByName("hooksMixin").FieldByName("hooksMu")
	v = reflect.NewAt(v.Type(), unsafe.Pointer(v.UnsafeAddr())).Elem()
	return v.Interface().(*sync.RWMutex)
}

// A member whose hook chain is transiently unreadable at the pre-probe
// snapshot — its mutex held by a concurrent update — must not be abandoned
// in favor of the readable one: the wrap retries the read, so the busy
// member is instrumented too and commands through it trace once.
func TestWrapClientUnreadableMemberRetried(t *testing.T) {
	cfg := new(clientConfig)
	defaults(cfg)

	mt := mocktracer.Start()
	defer mt.Stop()

	member1 := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1"})
	t.Cleanup(func() { member1.Close() })
	hidden := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1"})
	t.Cleanup(func() { hidden.Close() })

	// Hold hidden's hook mutex past the pre-probe snapshot — longer than
	// the snapshot's own 100ms lock-retry window, so the member's chain
	// reads as busy there. The proxy's AddHook blocks on the same mutex,
	// so the probe reaches hidden only once the hold ends.
	mu := hookChainMu(hidden)
	locked := make(chan struct{})
	released := make(chan struct{})
	go func() {
		mu.Lock()
		close(locked)
		time.Sleep(150 * time.Millisecond)
		mu.Unlock()
		close(released)
	}()
	<-locked

	proxy := &twoMemberProxy{a: member1, b: hidden}
	WrapClient(proxy)
	<-released

	_ = member1.Get(context.Background(), "foo").Err()
	_ = hidden.Get(context.Background(), "foo").Err()
	if spans := commandSpans(mt, cfg.spanName); len(spans) != 2 {
		t.Fatalf("expected 1 command span per member, got %d", len(spans))
	}
}
