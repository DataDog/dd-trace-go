// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package redis

import (
	"context"
	"sync"
	"testing"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/mocktracer"

	"github.com/go-redis/redis"
)

// boxCmdA and boxCmdB are two distinct command types over the same
// underlying command object.
type boxCmdA struct {
	*redis.StringCmd
}

type boxCmdB struct {
	*redis.StringCmd
}

// Two distinct commands whose interface data words coincide — the same
// underlying command under two different wrapper types — must not be
// mistaken for one another by the deduplication mark: each traces once.
func TestWrapClientDistinctPointerShapedCmds(t *testing.T) {
	cfg := new(clientConfig)
	defaults(cfg)

	mt := mocktracer.Start()
	defer mt.Stop()

	clientA := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1"})
	t.Cleanup(func() { clientA.Close() })
	clientB := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1"})
	t.Cleanup(func() { clientB.Close() })

	underlying := redis.NewStringCmd("get", "foo")
	cmdA := boxCmdA{StringCmd: underlying}
	cmdB := boxCmdB{StringCmd: underlying}

	// A barrier keeps both commands in flight at once, so one mark is live
	// while the other wrapper looks; the releases are sequenced so the two
	// processes do not write the shared command's error field concurrently.
	arrived := make(chan struct{}, 2)
	releaseA := make(chan struct{})
	releaseB := make(chan struct{})
	wrapA := func(old func(cmd redis.Cmder) error) func(cmd redis.Cmder) error {
		return func(cmd redis.Cmder) error {
			arrived <- struct{}{}
			<-releaseA
			return old(cmd)
		}
	}
	wrapB := func(old func(cmd redis.Cmder) error) func(cmd redis.Cmder) error {
		return func(cmd redis.Cmder) error {
			arrived <- struct{}{}
			<-releaseB
			return old(cmd)
		}
	}
	clientA.WrapProcess(wrapA)
	clientB.WrapProcess(wrapB)

	cloneA := WrapClient(clientA).WithContext(context.Background())
	cloneB := WrapClient(clientB).WithContext(context.Background())

	doneA := make(chan struct{})
	doneB := make(chan struct{})
	go func() {
		_ = cloneA.Process(cmdA)
		close(doneA)
	}()
	<-arrived // cmdA is in flight; its mark is set
	go func() {
		_ = cloneB.Process(cmdB)
		close(doneB)
	}()
	<-arrived // cmdB's wrapper has looked: a shared identity would see cmdA's mark
	close(releaseA)
	<-doneA
	close(releaseB)
	<-doneB

	if spans := commandSpans(mt, cfg.spanName); len(spans) != 2 {
		t.Fatalf("expected one span per command, got %d", len(spans))
	}
}

// Two distinct commands whose interface values are bit-identical — equal
// values of the same type, processed concurrently on different goroutines —
// are separate operations and must each trace once: the deduplication mark
// is scoped to the goroutine of the call chain that drives one command.
func TestWrapClientConcurrentEqualCmds(t *testing.T) {
	cfg := new(clientConfig)
	defaults(cfg)

	mt := mocktracer.Start()
	defer mt.Stop()

	clientA := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1"})
	t.Cleanup(func() { clientA.Close() })
	clientB := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1"})
	t.Cleanup(func() { clientB.Close() })

	underlying := redis.NewStringCmd("get", "foo")
	cmdA := boxCmdA{StringCmd: underlying}
	cmdB := boxCmdA{StringCmd: underlying}

	arrived := make(chan struct{}, 2)
	releaseA := make(chan struct{})
	releaseB := make(chan struct{})
	wrapA := func(old func(cmd redis.Cmder) error) func(cmd redis.Cmder) error {
		return func(cmd redis.Cmder) error {
			arrived <- struct{}{}
			<-releaseA
			return old(cmd)
		}
	}
	wrapB := func(old func(cmd redis.Cmder) error) func(cmd redis.Cmder) error {
		return func(cmd redis.Cmder) error {
			arrived <- struct{}{}
			<-releaseB
			return old(cmd)
		}
	}
	clientA.WrapProcess(wrapA)
	clientB.WrapProcess(wrapB)

	cloneA := WrapClient(clientA).WithContext(context.Background())
	cloneB := WrapClient(clientB).WithContext(context.Background())

	doneA := make(chan struct{})
	doneB := make(chan struct{})
	go func() {
		_ = cloneA.Process(cmdA)
		close(doneA)
	}()
	<-arrived
	go func() {
		_ = cloneB.Process(cmdB)
		close(doneB)
	}()
	<-arrived
	close(releaseA)
	<-doneA
	close(releaseB)
	<-doneB

	if spans := commandSpans(mt, cfg.spanName); len(spans) != 2 {
		t.Fatalf("expected one span per command, got %d", len(spans))
	}
}

// Every goroutine's traced-command stack is retired with it: a service
// running short-lived goroutines must not grow the registry without bound.
func TestTracedStacksRetired(t *testing.T) {
	client := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1"})
	t.Cleanup(func() { client.Close() })
	tc := WrapClient(client)

	var wg sync.WaitGroup
	for range 50 {
		wg.Go(func() {
			_ = tc.Process(redis.NewStringCmd("get", "foo"))
		})
	}
	wg.Wait()

	remaining := 0
	tracedStacks.Range(func(_, _ any) bool {
		remaining++
		return true
	})
	if remaining != 0 {
		t.Fatalf("expected every traced stack to be retired, got %d remaining", remaining)
	}
}

// A user wrapper that retries or fails over by forwarding the same command
// to a second wrapped client, synchronously: the second Process call is a
// separate Redis operation and must emit its own span.
func TestWrapClientForwardedCommandSecondClient(t *testing.T) {
	cfg := new(clientConfig)
	defaults(cfg)

	mt := mocktracer.Start()
	defer mt.Stop()

	clientA := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1"})
	t.Cleanup(func() { clientA.Close() })
	clientB := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1"})
	t.Cleanup(func() { clientB.Close() })

	tcB := WrapClient(clientB)

	cmd := redis.NewStringCmd("get", "foo")
	// Fail over from A to B within one user wrapper, on one goroutine,
	// inside A's traced chain.
	clientA.WrapProcess(func(old func(cmd redis.Cmder) error) func(cmd redis.Cmder) error {
		return func(cmd redis.Cmder) error {
			_ = tcB.Process(cmd)
			return old(cmd)
		}
	})
	tcA := WrapClient(clientA)

	_ = tcA.Process(cmd)

	if spans := commandSpans(mt, cfg.spanName); len(spans) != 2 {
		t.Fatalf("expected one span per client, got %d", len(spans))
	}
}

// Wrapping a raw upstream clone of an already-wrapped client adds a second
// wrapper around the inherited one: the two share the chain identity — the
// underlying client's Options pointer, inherited by upstream clones — so
// each command still traces exactly once.
func TestWrapClientRawCloneChainIdentity(t *testing.T) {
	cfg := new(clientConfig)
	defaults(cfg)

	mt := mocktracer.Start()
	defer mt.Stop()

	raw := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1"})
	t.Cleanup(func() { raw.Close() })
	WrapClient(raw)

	clone := raw.WithContext(context.Background())
	t.Cleanup(func() { clone.Close() })
	WrapClient(clone)

	_ = clone.Get("foo").Err()

	if spans := commandSpans(mt, cfg.spanName); len(spans) != 1 {
		t.Fatalf("expected exactly 1 command span through the wrapped raw clone, got %d", len(spans))
	}
}
