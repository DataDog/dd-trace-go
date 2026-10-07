// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package redis

import (
	"context"
	"runtime"
	"sync"
	"testing"
	"time"
	"weak"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/mocktracer"

	"github.com/go-redis/redis"
)

// commandSpans returns the finished spans whose operation name is spanName.
func commandSpans(mt mocktracer.Tracer, spanName string) []*mocktracer.Span {
	var spans []*mocktracer.Span
	for _, s := range mt.FinishedSpans() {
		if s.OperationName() == spanName {
			spans = append(spans, s)
		}
	}
	return spans
}

// Repeated WrapClient calls on the same client must not stack process
// wrappers and duplicate spans (https://github.com/DataDog/dd-trace-go/issues/619).
// The unreachable address is enough: the wrappers still run for every command.
func TestWrapClientIdempotent(t *testing.T) {
	cfg := new(clientConfig)
	defaults(cfg)

	newClient := func(t *testing.T) *redis.Client {
		t.Helper()
		client := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1"})
		t.Cleanup(func() { client.Close() })
		return client
	}

	t.Run("single span after repeated wraps", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()

		client := newClient(t)
		WrapClient(client)
		WrapClient(client)
		WrapClient(client)

		_ = client.Get("foo").Err()

		spans := commandSpans(mt, cfg.spanName)
		if len(spans) != 1 {
			t.Fatalf("expected exactly 1 command span after 3 wraps, got %d", len(spans))
		}
	})

	t.Run("raw client stays single-span", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()

		client := newClient(t)
		WrapClient(client)
		WrapClient(client)
		// A command through the raw handle, not a traced wrapper.
		_ = client.Get("foo").Err()

		spans := commandSpans(mt, cfg.spanName)
		if len(spans) != 1 {
			t.Fatalf("expected exactly 1 command span through the raw client, got %d", len(spans))
		}
	})

	t.Run("first configuration is kept", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()

		client := newClient(t)
		WrapClient(client)
		WrapClient(client, WithService("redis-other"))

		_ = client.Get("foo").Err()

		spans := commandSpans(mt, cfg.spanName)
		if len(spans) != 1 {
			t.Fatalf("expected exactly 1 command span, got %d", len(spans))
		}
		if got := spans[0].Tag(ext.ServiceName); got != cfg.serviceName {
			t.Fatalf("expected the first configuration's service name %q, got %v", cfg.serviceName, got)
		}
	})

	t.Run("WithContext clone of a wrapped client", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()

		client := newClient(t)
		WrapClient(client)
		// A traced client's WithContext must trace each command once.
		tc := WrapClient(client).WithContext(context.Background())

		_ = tc.Get("foo").Err()

		spans := commandSpans(mt, cfg.spanName)
		if len(spans) != 1 {
			t.Fatalf("expected exactly 1 command span for the traced clone, got %d", len(spans))
		}
	})

	t.Run("other clients are wrapped independently", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()

		client := newClient(t)
		WrapClient(client)
		other := newClient(t)
		WrapClient(other)

		_ = client.Get("foo").Err()
		_ = other.Get("foo").Err()

		if spans := commandSpans(mt, cfg.spanName); len(spans) != 2 {
			t.Fatalf("expected 1 command span per client, got %d", len(spans))
		}
	})
}

// Concurrent first wraps must serialize: upstream WrapProcess assigns the
// client's process without synchronization, so two racing first wraps could
// strip the tracing wrapper from the client.
func TestWrapClientConcurrent(t *testing.T) {
	cfg := new(clientConfig)
	defaults(cfg)

	mt := mocktracer.Start()
	defer mt.Stop()

	client := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1"})
	t.Cleanup(func() { client.Close() })

	const n = 8
	var wg sync.WaitGroup
	for range n {
		wg.Go(func() {
			WrapClient(client)
		})
	}
	wg.Wait()
	_ = client.Get("foo").Err()

	// Concurrent wraps installed a single hook or wrapper, so the command is
	// traced exactly once. Commands run only after every wrap returned:
	// go-redis does not synchronize WrapProcess or AddHook with command
	// processing.
	if spans := commandSpans(mt, cfg.spanName); len(spans) != 1 {
		t.Fatalf("expected exactly 1 command span after %d concurrent wraps, got %d", n, len(spans))
	}
}

// A second WrapClient call with a different configuration must return a
// handle that, with its pipelines and WithContext clones, still traces with
// the first call's configuration.
func TestWrapClientKeepsFirstConfigOnRewrap(t *testing.T) {
	cfg := new(clientConfig)
	defaults(cfg)

	mt := mocktracer.Start()
	defer mt.Stop()

	client := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1"})
	t.Cleanup(func() { client.Close() })

	WrapClient(client)
	tc := WrapClient(client, WithService("redis-other"))

	_ = tc.Get("foo").Err()
	clone := tc.WithContext(context.Background())
	_ = clone.Get("foo").Err()
	pipe := tc.Pipeline()
	pipe.Get("foo")
	_, _ = pipe.Exec()

	spans := commandSpans(mt, cfg.spanName)
	if len(spans) != 3 {
		t.Fatalf("expected 1 command span per command, got %d", len(spans))
	}
	for _, s := range spans {
		if got := s.Tag(ext.ServiceName); got != cfg.serviceName {
			t.Fatalf("expected the first configuration's service name %q, got %v", cfg.serviceName, got)
		}
	}
}

// The registry keys clients weakly, so a retired client must be collected and
// its entry dropped: the registry holds at most one entry per live client.
func TestWrapClientRegistryDropsDeadClients(t *testing.T) {
	client := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1"})
	key := weak.Make(client)
	WrapClient(client)
	client = nil

	// Runtime cleanups run shortly after the object becomes unreachable, but
	// not necessarily after the very first GC.
	for range 1000 {
		runtime.GC()
		wrapMu.Lock()
		_, alive := wrapped[key]
		wrapMu.Unlock()
		if !alive {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("registry entry outlived its client")
}

// Third-party process wrappers installed between two Datadog wraps must keep
// running for commands through the second wrap's handle and its WithContext
// clones.
func TestWrapClientKeepsLaterProcessWrappers(t *testing.T) {
	cfg := new(clientConfig)
	defaults(cfg)

	mt := mocktracer.Start()
	defer mt.Stop()

	client := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1"})
	t.Cleanup(func() { client.Close() })

	WrapClient(client)
	var ran bool
	client.WrapProcess(func(oldProcess func(cmd redis.Cmder) error) func(cmd redis.Cmder) error {
		return func(cmd redis.Cmder) error {
			ran = true
			return oldProcess(cmd)
		}
	})

	tc := WrapClient(client)
	clone := tc.WithContext(context.Background())
	_ = clone.Get("foo").Err()

	if !ran {
		t.Fatal("the later process wrapper did not run for the traced clone")
	}
	spans := commandSpans(mt, cfg.spanName)
	if len(spans) != 1 {
		t.Fatalf("expected exactly 1 command span, got %d", len(spans))
	}
	if got := spans[0].Tag(ext.ServiceName); got != cfg.serviceName {
		t.Fatalf("expected the first configuration's service name %q, got %v", cfg.serviceName, got)
	}
}

// A re-wrap while the shared client is serving commands must not reassign
// the client's process chain: concurrent commands read it without locking,
// and upstream WrapProcess writes it even for a no-op callback.
func TestWrapClientRewrapDuringCommands(t *testing.T) {
	mt := mocktracer.Start()
	defer mt.Stop()

	client := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1"})
	t.Cleanup(func() { client.Close() })
	WrapClient(client)

	stop := make(chan struct{})
	var wg sync.WaitGroup
	for range 4 {
		wg.Go(func() {
			for {
				select {
				case <-stop:
					return
				default:
					_ = client.Get("foo").Err()
				}
			}
		})
	}
	WrapClient(client) // duplicate wrap while commands are in flight
	WrapClient(client)
	close(stop)
	wg.Wait()
}

// unhashableCmd wraps a command with a map field, so its value cannot be a
// map key.
type unhashableCmd struct {
	*redis.StringCmd
	extra map[string]string
}

func (c unhashableCmd) String() string { return c.StringCmd.String() }

// A command value that cannot be compared must not panic the deduplication
// map: it traces once per wrapper instead.
func TestWrapClientUnhashableCmd(t *testing.T) {
	cfg := new(clientConfig)
	defaults(cfg)

	mt := mocktracer.Start()
	defer mt.Stop()

	client := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1"})
	t.Cleanup(func() { client.Close() })
	WrapClient(client)

	cmd := unhashableCmd{StringCmd: redis.NewStringCmd("get", "foo"), extra: map[string]string{"a": "b"}}
	if err := client.Process(cmd); err == nil {
		t.Fatal("expected the unreachable address to error")
	}

	if spans := commandSpans(mt, cfg.spanName); len(spans) != 1 {
		t.Fatalf("expected exactly 1 command span, got %d", len(spans))
	}
}
