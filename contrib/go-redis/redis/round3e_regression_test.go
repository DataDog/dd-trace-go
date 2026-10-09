// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package redis

import (
	"context"
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
