// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product contains software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package rediswrap

import (
	"reflect"
	"runtime"
	"sync"
	"testing"
	"time"
)

type fakeClient struct{ id int }

type valueProxy struct {
	client *fakeClient
	other  *fakeClient
}

type nonComparableProxy struct {
	client *fakeClient
	tags   map[string]string
}

type guarded struct {
	mu     *sync.Mutex
	client *fakeClient
}

func (g *guarded) replace(c *fakeClient) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.client = c
}

func TestHandleOf(t *testing.T) {
	c := &fakeClient{}
	h1, ok := HandleOf(c)
	if !ok {
		t.Fatal("expected a pointer client to be keyable")
	}
	h2, _ := HandleOf(c)
	if h1 != h2 {
		t.Fatal("expected equal handles for the same object")
	}
	other := &fakeClient{}
	if h3, _ := HandleOf(other); h3 == h1 {
		t.Fatal("expected distinct handles for distinct objects")
	}
	if _, ok := HandleOf(42); ok {
		t.Fatal("expected a non-pointer to be unkeyable")
	}
	if _, ok := HandleOf(nil); ok {
		t.Fatal("expected nil to be unkeyable")
	}
}

func TestGoid(t *testing.T) {
	ch := make(chan uint64, 1)
	go func() { ch <- Goid() }()
	if Goid() == <-ch {
		t.Fatal("expected distinct goroutine ids")
	}
}

func TestMemberKeysAndSameMembers(t *testing.T) {
	a, b := &fakeClient{}, &fakeClient{}
	keys := MemberKeys([]any{a, b})
	if !SameMembers(keys, []any{a, b}) {
		t.Fatal("expected the same members to match")
	}
	if SameMembers(keys, []any{b, a}) {
		t.Fatal("expected order to matter")
	}
	if SameMembers(keys, []any{a}) {
		t.Fatal("expected a length mismatch to differ")
	}
	if SameMembers(MemberKeys([]any{a, b}), []any{a, (*fakeClient)(nil)}) {
		t.Fatal("expected an unkeyable member to differ")
	}
}

func TestContainsKey(t *testing.T) {
	a, b := &fakeClient{}, &fakeClient{}
	ka, ok := HandleOf(a)
	if !ok {
		t.Fatal("expected a keyable member")
	}
	kb, ok2 := HandleOf(b)
	if !ok2 {
		t.Fatal("expected a keyable member")
	}
	if !ContainsKey([]Handle{ka, kb}, kb) || ContainsKey([]Handle{ka}, kb) {
		t.Fatal("containsKey contract violated")
	}
}

func TestInstallMarks(t *testing.T) {
	a, b := &fakeClient{}, &fakeClient{}
	p := valueProxy{client: a, other: b}
	ma := []any{a, b}
	if IsInstalling(p, ma) {
		t.Fatal("nothing is installing")
	}
	unmark := MarkInstalling(p, ma)
	if !IsInstalling(p, ma) {
		t.Fatal("expected the mark to be visible")
	}
	// A comparable proxy is matched by value: an equal value is the same
	// proxy regardless of its member set, and a different value is
	// different.
	if !IsInstalling(valueProxy{client: a, other: b}, []any{a}) {
		t.Fatal("expected an equal value proxy to match regardless of members")
	}
	if IsInstalling(valueProxy{client: b, other: a}, []any{a, b}) {
		t.Fatal("expected a different value proxy not to match")
	}
	unmark()
	if IsInstalling(p, ma) {
		t.Fatal("expected the mark to be cleared")
	}

	// A non-comparable proxy is matched through its members.
	q := nonComparableProxy{client: a, tags: map[string]string{}}
	mq := []any{a}
	unmark2 := MarkInstalling(q, mq)
	if !IsInstalling(nonComparableProxy{client: a, tags: map[string]string{}}, mq) {
		t.Fatal("expected member matching for a non-comparable proxy")
	}
	if IsInstalling(nonComparableProxy{client: b, tags: map[string]string{}}, []any{b}) {
		t.Fatal("expected a different member set to differ")
	}
	unmark2()
	if IsInstalling(q, mq) {
		t.Fatal("expected the mark to be cleared")
	}
}

func TestMarkInstallingPanicsClear(t *testing.T) {
	a := &fakeClient{}
	p := &guarded{mu: &sync.Mutex{}, client: a}
	func() {
		defer func() { _ = recover() }()
		defer MarkInstalling(p, nil)()
		panic("user AddHook panicked")
	}()
	if IsInstalling(p, nil) {
		t.Fatal("expected the deferred unmark to survive the panic")
	}
}

func TestRegisterCleanupOnce(t *testing.T) {
	var mu sync.Mutex
	calls := 0
	c := &fakeClient{}
	k, _ := HandleOf(c)
	RegisterCleanup(c, func(Handle) { mu.Lock(); calls++; mu.Unlock() }, k)
	RegisterCleanup(c, func(Handle) { mu.Lock(); calls++; mu.Unlock() }, k)
	// Wait for the object to die and the single cleanup to run.
	c = nil
	for i := 0; i < 1000; i++ {
		runtime.GC()
		mu.Lock()
		n := calls
		mu.Unlock()
		if n == 1 {
			return
		}
		if n > 1 {
			t.Fatalf("expected one cleanup, got %d", n)
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("cleanup never ran")
}

func TestLockStruct(t *testing.T) {
	g := &guarded{mu: &sync.Mutex{}, client: &fakeClient{}}
	unlock, ok := LockStruct(reflect.ValueOf(g).Elem())
	if !ok {
		t.Fatal("expected the free mutex to lock")
	}
	unlock()

	// A held mutex aborts rather than blocking.
	g.mu.Lock()
	unlock, ok = LockStruct(reflect.ValueOf(g).Elem())
	if ok {
		unlock()
		t.Fatal("expected a held mutex to abort")
	}
	g.mu.Unlock()

	// Concurrent replacement under the lock does not race with the walk.
	stop := make(chan struct{})
	var wg sync.WaitGroup
	wg.Go(func() {
		for {
			select {
			case <-stop:
				return
			default:
				g.replace(&fakeClient{})
			}
		}
	})
	for range 10 {
		unlock, ok := LockStruct(reflect.ValueOf(g).Elem())
		if ok {
			unlock()
		}
	}
	close(stop)
	wg.Wait()
}

type aliased struct {
	a *sync.Mutex
	b *sync.Mutex
	c *sync.Mutex
}

// Two pointer fields referencing the same mutex must not read as
// contention: the walker locks each underlying lock once.
func TestLockStructAliasedPointers(t *testing.T) {
	m := &sync.Mutex{}
	g := &aliased{a: m, b: m, c: &sync.Mutex{}}

	unlock, ok := LockStruct(reflect.ValueOf(g).Elem())
	if !ok {
		t.Fatal("expected aliased mutexes to deduplicate, not abort")
	}
	unlock()

	// The underlying shared mutex really is unlocked after.
	if !g.a.TryLock() {
		t.Fatal("expected the aliased mutex to be released")
	}
	g.a.Unlock()
}
