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

	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/mocktracer"

	"github.com/redis/go-redis/v9"
)

// commandSpans returns the finished spans whose operation name is spanName,
// excluding dial and pipeline spans.
func commandSpans(mt mocktracer.Tracer, spanName string) []*mocktracer.Span {
	var spans []*mocktracer.Span
	for _, s := range mt.FinishedSpans() {
		if s.OperationName() == spanName {
			spans = append(spans, s)
		}
	}
	return spans
}

// Repeated WrapClient calls on the same client must not stack hooks and
// duplicate spans (https://github.com/DataDog/dd-trace-go/issues/619). The
// unreachable address is enough: the hooks still run for every command.
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

		_ = client.Get(context.Background(), "foo").Err()

		spans := commandSpans(mt, cfg.spanName)
		if len(spans) != 1 {
			t.Fatalf("expected exactly 1 command span after 3 wraps, got %d", len(spans))
		}
	})

	t.Run("first configuration is kept", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()

		client := newClient(t)
		WrapClient(client)
		WrapClient(client, WithService("redis-other"))

		_ = client.Get(context.Background(), "foo").Err()

		spans := commandSpans(mt, cfg.spanName)
		if len(spans) != 1 {
			t.Fatalf("expected exactly 1 command span, got %d", len(spans))
		}
		if got := spans[0].Tag(ext.ServiceName); got != cfg.serviceName {
			t.Fatalf("expected the first configuration's service name %q, got %v", cfg.serviceName, got)
		}
	})

	t.Run("other clients are wrapped independently", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()

		client := newClient(t)
		WrapClient(client)
		other := newClient(t)
		WrapClient(other)

		_ = client.Get(context.Background(), "foo").Err()
		_ = other.Get(context.Background(), "foo").Err()

		if spans := commandSpans(mt, cfg.spanName); len(spans) != 2 {
			t.Fatalf("expected 1 command span per client, got %d", len(spans))
		}
	})
}

// Concurrent first wraps must instrument the client exactly once: the loser
// of the registration race must not return before the winner installed its
// hook, or a command issued right after the losing call would run untraced.
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
	_ = client.Get(context.Background(), "foo").Err()

	// Concurrent wraps installed a single hook, so the command is traced
	// exactly once. Commands run only after every wrap returned: go-redis
	// does not synchronize AddHook with command processing.
	if spans := commandSpans(mt, cfg.spanName); len(spans) != 1 {
		t.Fatalf("expected exactly 1 command span after %d concurrent wraps, got %d", n, len(spans))
	}
	if open := mt.OpenSpans(); len(open) != 0 {
		t.Fatalf("expected no leaked command spans, got %d", len(open))
	}
}

// A WithContext or WithTimeout clone of an already-wrapped client inherits
// the datadog hook, so wrapping the clone adds a second hook to it. Commands
// through either handle must still trace exactly once.
func TestWrapClientCloneSingleSpan(t *testing.T) {
	cfg := new(clientConfig)
	defaults(cfg)

	newClient := func(t *testing.T) *redis.Client {
		t.Helper()
		client := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1"})
		t.Cleanup(func() { client.Close() })
		return client
	}

	run := func(t *testing.T) (orig, clone *redis.Client) {
		t.Helper()
		orig = newClient(t)
		WrapClient(orig)
		clone = orig.WithTimeout(time.Second)
		WrapClient(clone)
		return orig, clone
	}

	// v9 passes the context per command, so WithTimeout clones are the only
	// handle type that inherits the hook while carrying a different pointer.
	t.Run("WithTimeout clone", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()

		orig, clone := run(t)

		_ = orig.Get(context.Background(), "foo").Err()
		_ = clone.Get(context.Background(), "foo").Err()

		if spans := commandSpans(mt, cfg.spanName); len(spans) != 2 {
			t.Fatalf("expected 1 command span per handle, got %d", len(spans))
		}
		if open := mt.OpenSpans(); len(open) != 0 {
			t.Fatalf("expected no leaked command spans, got %d", len(open))
		}
	})
}

// Inspectable clients are deduplicated by reading their hook chain, so the
// registry — which exists only for clients whose chain cannot be read —
// stays empty no matter how many clients are wrapped, and nothing at all
// retains them: not even an error-check closure capturing the client.
func TestWrapClientRegistryStaysEmpty(t *testing.T) {
	client := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1"})
	t.Cleanup(func() { client.Close() })
	captured := client
	WrapClient(client, WithErrorCheck(func(error) bool {
		_ = captured
		return true
	}))

	wrapMu.Lock()
	before := len(wrapped)
	wrapMu.Unlock()

	WrapClient(client)
	other := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1"})
	t.Cleanup(func() { other.Close() })
	WrapClient(other)

	wrapMu.Lock()
	after := len(wrapped)
	wrapMu.Unlock()
	if after != before {
		t.Fatalf("expected no registry entries for inspectable clients, got %d new", after-before)
	}
}

// nestedClientHook is a user hook that issues a command on another wrapped
// client with the context it receives from the first client's hook chain.
type nestedClientHook struct{ other *redis.Client }

func (h *nestedClientHook) DialHook(hook redis.DialHook) redis.DialHook { return hook }

func (h *nestedClientHook) ProcessHook(hook redis.ProcessHook) redis.ProcessHook {
	return func(ctx context.Context, cmd redis.Cmder) error {
		_ = h.other.Get(ctx, "foo").Err()
		return hook(ctx, cmd)
	}
}

func (h *nestedClientHook) ProcessPipelineHook(hook redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return hook
}

// The outer command's marker must stay scoped to it: a command issued on
// another wrapped client with the marker in its context is still traced.
func TestWrapClientNestedOtherClient(t *testing.T) {
	cfg := new(clientConfig)
	defaults(cfg)

	mt := mocktracer.Start()
	defer mt.Stop()

	a := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1"})
	t.Cleanup(func() { a.Close() })
	b := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1"})
	t.Cleanup(func() { b.Close() })

	WrapClient(a)
	WrapClient(b)
	a.AddHook(&nestedClientHook{other: b})

	_ = a.Get(context.Background(), "foo").Err()

	if spans := commandSpans(mt, cfg.spanName); len(spans) != 2 {
		t.Fatalf("expected 1 command span per client, got %d", len(spans))
	}
	if open := mt.OpenSpans(); len(open) != 0 {
		t.Fatalf("expected no leaked command spans, got %d", len(open))
	}
}

// redisDecorator is a client built by embedding redis.UniversalClient, the
// common decorator pattern: AddHook delegates to the embedded client.
type redisDecorator struct {
	redis.UniversalClient
}

// Wrapping a decorator must register against the client it embeds, so
// repeated wraps through any number of decorators install a single hook.
func TestWrapClientDecorator(t *testing.T) {
	cfg := new(clientConfig)
	defaults(cfg)

	newClient := func(t *testing.T) *redis.Client {
		t.Helper()
		client := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1"})
		t.Cleanup(func() { client.Close() })
		return client
	}

	t.Run("two decorators over one client", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()

		client := newClient(t)
		WrapClient(&redisDecorator{client})
		WrapClient(&redisDecorator{client})

		_ = client.Get(context.Background(), "foo").Err()

		if spans := commandSpans(mt, cfg.spanName); len(spans) != 1 {
			t.Fatalf("expected exactly 1 command span, got %d", len(spans))
		}
	})

	t.Run("decorator and direct wrap", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()

		client := newClient(t)
		WrapClient(client)
		WrapClient(&redisDecorator{client})

		_ = client.Get(context.Background(), "foo").Err()

		if spans := commandSpans(mt, cfg.spanName); len(spans) != 1 {
			t.Fatalf("expected exactly 1 command span, got %d", len(spans))
		}
	})
}

// redisProxy hides its underlying client in an unexported embedded field.
type redisProxy struct {
	hiddenClient
}

type hiddenClient = *redis.Client

// A proxy holding its client in an unexported field is still registered
// against that client: repeated wraps, and a wrap of the client directly,
// install a single hook.
func TestWrapClientUnexportedProxy(t *testing.T) {
	cfg := new(clientConfig)
	defaults(cfg)

	mt := mocktracer.Start()
	defer mt.Stop()

	client := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1"})
	t.Cleanup(func() { client.Close() })

	proxy := &redisProxy{hiddenClient: client}
	WrapClient(proxy)
	WrapClient(proxy)
	WrapClient(client)

	hooks := reflect.ValueOf(client).Elem().FieldByName("hooksMixin").FieldByName("slice")
	if n := hooks.Len(); n != 1 {
		t.Fatalf("expected exactly 1 hook after 3 wraps, got %d", n)
	}

	_ = client.Get(context.Background(), "foo").Err()

	if spans := commandSpans(mt, cfg.spanName); len(spans) != 1 {
		t.Fatalf("expected exactly 1 command span, got %d", len(spans))
	}
	if open := mt.OpenSpans(); len(open) != 0 {
		t.Fatalf("expected no leaked command spans, got %d", len(open))
	}
}

// dialSpans returns the finished dial spans, for the DialHook path.
func dialSpans(mt mocktracer.Tracer) []*mocktracer.Span {
	var spans []*mocktracer.Span
	for _, s := range mt.FinishedSpans() {
		if s.OperationName() == "redis.dial" {
			spans = append(spans, s)
		}
	}
	return spans
}

// A WithTimeout clone shares the original client's connection pool, whose
// dialer is bound to the original's hook chain, so wrapping the clone cannot
// multiply dial spans: each connection attempt is dialed through one chain.
func TestWrapClientCloneDoesNotMultiplyDialSpans(t *testing.T) {
	mt := mocktracer.Start()
	defer mt.Stop()

	wrapped := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1"})
	t.Cleanup(func() { wrapped.Close() })
	WrapClient(wrapped)
	clone := wrapped.WithTimeout(time.Second)
	WrapClient(clone)

	_ = wrapped.Get(context.Background(), "foo").Err()
	_ = clone.Get(context.Background(), "foo").Err()
	withClone := len(dialSpans(mt))
	mt.Reset()

	single := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1"})
	t.Cleanup(func() { single.Close() })
	WrapClient(single)

	_ = single.Get(context.Background(), "foo").Err()
	_ = single.Get(context.Background(), "foo").Err()
	withoutClone := len(dialSpans(mt))

	if withClone != withoutClone {
		t.Fatalf("expected %d dial spans with a wrapped clone, got %d", withoutClone, withClone)
	}
}

// A proxy nested deeper than underlyingClient searches cannot be seen
// through; it is deduplicated by its own weak identity instead, so repeated
// wraps still install a single hook.
func TestWrapClientDeepProxy(t *testing.T) {
	cfg := new(clientConfig)
	defaults(cfg)

	mt := mocktracer.Start()
	defer mt.Stop()

	client := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1"})
	t.Cleanup(func() { client.Close() })

	p3 := &redisDecorator{client}
	p2 := &redisDecorator{p3}
	p1 := &redisDecorator{p2}
	WrapClient(p1)
	WrapClient(p1)

	hooks := reflect.ValueOf(client).Elem().FieldByName("hooksMixin").FieldByName("slice")
	if n := hooks.Len(); n != 1 {
		t.Fatalf("expected exactly 1 hook after 2 deep-proxy wraps, got %d", n)
	}

	_ = client.Get(context.Background(), "foo").Err()

	if spans := commandSpans(mt, cfg.spanName); len(spans) != 1 {
		t.Fatalf("expected exactly 1 command span, got %d", len(spans))
	}
}

// redisRouter is a proxy holding two concrete clients: its commands go to
// the embedded one, its AddHook instruments both.
type redisRouter struct {
	redis.UniversalClient
	write redis.UniversalClient
}

func (r *redisRouter) AddHook(hook redis.Hook) {
	r.UniversalClient.AddHook(hook)
	r.write.AddHook(hook)
}

// A proxy with several concrete clients decides through its own AddHook what
// it instruments: WrapClient observes the decision instead of inferring it
// from fields, then instruments exactly the members the proxy hooked — one
// hook each, with that member's own endpoint tags, and never a second hook
// on a member that was already wrapped.
func TestWrapClientMultiClientProxy(t *testing.T) {
	cfg := new(clientConfig)
	defaults(cfg)

	mt := mocktracer.Start()
	defer mt.Stop()

	read := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1"})
	t.Cleanup(func() { read.Close() })
	write := redis.NewClient(&redis.Options{Addr: "127.0.0.1:2"})
	t.Cleanup(func() { write.Close() })

	WrapClient(read)
	router := &redisRouter{UniversalClient: read, write: write}
	WrapClient(router)
	WrapClient(router) // same proxy: every member is already hooked

	_ = read.Get(context.Background(), "foo").Err()
	_ = write.Get(context.Background(), "foo").Err()

	// One span per member: the pre-wrapped read member is not traced twice.
	spans := commandSpans(mt, cfg.spanName)
	if len(spans) != 2 {
		t.Fatalf("expected 1 command span per member, got %d", len(spans))
	}
	if open := mt.OpenSpans(); len(open) != 0 {
		t.Fatalf("expected no leaked command spans, got %d", len(open))
	}
	// Each member's span carries that member's endpoint, not the other's.
	ports := map[any]bool{}
	for _, s := range spans {
		ports[s.Tag(ext.TargetPort)] = true
	}
	if !ports["1"] || !ports["2"] {
		t.Fatalf("expected spans tagged with each member's port, got %v", ports)
	}
}

// selectiveRouter hooks only its embedded client; the other member is a
// private client it never hooks.
type selectiveRouter struct {
	redis.UniversalClient
	private redis.UniversalClient
}

func (r *selectiveRouter) AddHook(hook redis.Hook) {
	r.UniversalClient.AddHook(hook)
}

// Repeated wraps of a proxy that does not hook one of its members must not
// keep probing for it: the no-op probe is added once, not once per wrap.
func TestWrapClientProxyProbeOnce(t *testing.T) {
	client := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1"})
	t.Cleanup(func() { client.Close() })
	private := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1"})
	t.Cleanup(func() { private.Close() })

	router := &selectiveRouter{UniversalClient: client, private: private}
	WrapClient(router)
	afterFirst := reflect.ValueOf(client).Elem().FieldByName("hooksMixin").FieldByName("slice").Len()
	WrapClient(router)
	WrapClient(router)
	afterRest := reflect.ValueOf(client).Elem().FieldByName("hooksMixin").FieldByName("slice").Len()
	if afterRest != afterFirst {
		t.Fatalf("expected the hook chain to stop growing after the first wrap, got %d then %d", afterFirst, afterRest)
	}
	if n := reflect.ValueOf(private).Elem().FieldByName("hooksMixin").FieldByName("slice").Len(); n != 0 {
		t.Fatalf("expected the private member to stay unhooked, got %d hooks", n)
	}
}
