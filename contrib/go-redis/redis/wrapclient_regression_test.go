// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package redis

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

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

// Two independently constructed clients built from the same *redis.Options
// are separate chains: a wrapper forwarding a command from one to the other
// synchronously must not suppress the second operation's span, even though
// the clients share their Options pointer.
func TestWrapClientSharedOptionsDistinctChains(t *testing.T) {
	cfg := new(clientConfig)
	defaults(cfg)

	mt := mocktracer.Start()
	defer mt.Stop()

	opt := &redis.Options{Addr: "127.0.0.1:1"}
	clientA := redis.NewClient(opt)
	t.Cleanup(func() { clientA.Close() })
	clientB := redis.NewClient(opt)
	t.Cleanup(func() { clientB.Close() })

	tcB := WrapClient(clientB)
	clientA.WrapProcess(func(old func(cmd redis.Cmder) error) func(cmd redis.Cmder) error {
		return func(cmd redis.Cmder) error {
			_ = tcB.Process(cmd)
			return old(cmd)
		}
	})
	tcA := WrapClient(clientA)

	_ = tcA.Process(redis.NewStringCmd("get", "foo"))

	if spans := commandSpans(mt, cfg.spanName); len(spans) != 2 {
		t.Fatalf("expected one span per client, got %d", len(spans))
	}
}

// Successive WithContext clones must keep the tracing chain flat: a handle
// made from a handle bases its wrapper on the predecessor's chain without
// the predecessor's wrapper, instead of nesting one wrapper per generation.
func TestWrapClientCloneChainStaysFlat(t *testing.T) {
	client := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1"})
	t.Cleanup(func() { client.Close() })
	tc := WrapClient(client)

	// Each tracing wrapper level resolves the goroutine id once per
	// command; a nested chain would resolve it once per generation.
	var calls atomic.Int64
	orig := goid
	goid = func() uint64 {
		calls.Add(1)
		return orig()
	}
	t.Cleanup(func() { goid = orig })

	chained := tc
	for range 5 {
		chained = chained.WithContext(context.Background())
	}
	calls.Store(0)
	_ = chained.Process(redis.NewStringCmd("get", "foo"))
	<-time.After(50 * time.Millisecond)

	if n := calls.Load(); n > 2 {
		t.Fatalf("expected at most 2 wrapper levels (clone wrapper and first-generation wrapper), got %d goid calls per command", n)
	}
}

// A user wrapper installed before Datadog that synchronously re-issues the
// command it is handling — a retry — runs a nested Process through the same
// client on the same goroutine. The nested operation is a separate Redis
// operation and traces, even though the command value is identical: only
// the chain's inherited wrapper skips a mark pushed by a different wrapper
// instance.
func TestWrapClientNestedReissueTraces(t *testing.T) {
	cfg := new(clientConfig)
	defaults(cfg)

	mt := mocktracer.Start()
	defer mt.Stop()

	cmd := redis.NewStringCmd("get", "foo")
	var reissued atomic.Bool
	client := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1"})
	t.Cleanup(func() { client.Close() })
	// The retry wrapper re-issues the same command object once, from
	// inside the handling of the outer command.
	client.WrapProcess(func(old func(cmd redis.Cmder) error) func(cmd redis.Cmder) error {
		return func(cmd redis.Cmder) error {
			if reissued.CompareAndSwap(false, true) {
				_ = client.Process(cmd)
			}
			return old(cmd)
		}
	})
	tc := WrapClient(client)

	_ = tc.Process(cmd)

	if spans := commandSpans(mt, cfg.spanName); len(spans) != 2 {
		t.Fatalf("expected the outer command and its nested re-issue to trace twice, got %d spans", len(spans))
	}
}

// A process wrapper installed on the raw client after the first WrapClient
// must run for commands through clones created later: the first-generation
// base is computed at WithContext time from the underlying client's current
// process chain, not frozen at install time.
func TestWrapClientKeepsLaterWrappersInClones(t *testing.T) {
	cfg := new(clientConfig)
	defaults(cfg)

	mt := mocktracer.Start()
	defer mt.Stop()

	raw := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1"})
	t.Cleanup(func() { raw.Close() })
	tc := WrapClient(raw)

	var laterCalls atomic.Int64
	raw.WrapProcess(func(old func(cmd redis.Cmder) error) func(cmd redis.Cmder) error {
		return func(cmd redis.Cmder) error {
			laterCalls.Add(1)
			return old(cmd)
		}
	})

	clone := tc.WithContext(context.Background())
	_ = clone.Get("foo").Err()

	if n := laterCalls.Load(); n != 1 {
		t.Fatalf("expected the later process wrapper to run exactly once per command, got %d", n)
	}
	if spans := commandSpans(mt, cfg.spanName); len(spans) != 1 {
		t.Fatalf("expected exactly 1 command span, got %d", len(spans))
	}
}
