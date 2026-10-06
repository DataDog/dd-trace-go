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

	"github.com/go-redis/redis/v7"
)

// commandSpans returns the finished spans whose operation name is spanName,
// excluding pipeline spans.
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

		_ = client.Get("foo").Err()

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

		_ = client.Get("foo").Err()

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

		_ = client.Get("foo").Err()
		_ = other.Get("foo").Err()

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
	_ = client.Get("foo").Err()

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
		clone = orig.WithContext(context.Background())
		WrapClient(clone)
		return orig, clone
	}

	t.Run("WithContext clone", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()

		orig, clone := run(t)

		_ = orig.Get("foo").Err()
		_ = clone.Get("foo").Err()

		if spans := commandSpans(mt, cfg.spanName); len(spans) != 2 {
			t.Fatalf("expected 1 command span per handle, got %d", len(spans))
		}
		if open := mt.OpenSpans(); len(open) != 0 {
			t.Fatalf("expected no leaked command spans, got %d", len(open))
		}
	})

	t.Run("WithTimeout clone", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()

		orig, clone := run(t)
		clone = clone.WithTimeout(time.Second)

		_ = orig.Get("foo").Err()
		_ = clone.Get("foo").Err()

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

		_ = client.Get("foo").Err()

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

		_ = client.Get("foo").Err()

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

	hooks := reflect.ValueOf(client).Elem().FieldByName("hooks").FieldByName("hooks")
	if n := hooks.Len(); n != 1 {
		t.Fatalf("expected exactly 1 hook after 3 wraps, got %d", n)
	}

	_ = client.Get("foo").Err()

	if spans := commandSpans(mt, cfg.spanName); len(spans) != 1 {
		t.Fatalf("expected exactly 1 command span, got %d", len(spans))
	}
	if open := mt.OpenSpans(); len(open) != 0 {
		t.Fatalf("expected no leaked command spans, got %d", len(open))
	}
}

// nestedClientHook is a user hook that issues a command on another wrapped
// client with the context it receives from the first client's hook chain.
type nestedClientHook struct{ other *redis.Client }

func (h *nestedClientHook) BeforeProcess(ctx context.Context, cmd redis.Cmder) (context.Context, error) {
	_ = h.other.WithContext(ctx).Get("foo").Err()
	return ctx, nil
}

func (h *nestedClientHook) AfterProcess(ctx context.Context, cmd redis.Cmder) error { return nil }
func (h *nestedClientHook) BeforeProcessPipeline(ctx context.Context, cmds []redis.Cmder) (context.Context, error) {
	return ctx, nil
}
func (h *nestedClientHook) AfterProcessPipeline(ctx context.Context, cmds []redis.Cmder) error {
	return nil
}

// A command issued on another wrapped client with the first client's context
// in its hook chain is still traced.
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

	_ = a.Get("foo").Err()

	if spans := commandSpans(mt, cfg.spanName); len(spans) != 2 {
		t.Fatalf("expected 1 command span per client, got %d", len(spans))
	}
	if open := mt.OpenSpans(); len(open) != 0 {
		t.Fatalf("expected no leaked command spans, got %d", len(open))
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

	hooks := reflect.ValueOf(client).Elem().FieldByName("hooks").FieldByName("hooks")
	if n := hooks.Len(); n != 1 {
		t.Fatalf("expected exactly 1 hook after 2 deep-proxy wraps, got %d", n)
	}

	_ = client.Get("foo").Err()

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

	_ = read.Get("foo").Err()
	_ = write.Get("foo").Err()

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
	afterFirst := reflect.ValueOf(client).Elem().FieldByName("hooks").FieldByName("hooks").Len()
	WrapClient(router)
	WrapClient(router)
	afterRest := reflect.ValueOf(client).Elem().FieldByName("hooks").FieldByName("hooks").Len()
	if afterRest != afterFirst {
		t.Fatalf("expected the hook chain to stop growing after the first wrap, got %d then %d", afterFirst, afterRest)
	}
	if n := reflect.ValueOf(private).Elem().FieldByName("hooks").FieldByName("hooks").Len(); n != 0 {
		t.Fatalf("expected the private member to stay unhooked, got %d hooks", n)
	}
}

// writeOnlyRouter routes commands through its embedded client but hooks
// only its write member: a proxy whose AddHook targets differ from another
// proxy's over the same members.
type writeOnlyRouter struct {
	redis.UniversalClient
	write redis.UniversalClient
}

func (r *writeOnlyRouter) AddHook(hook redis.Hook) {
	r.write.AddHook(hook)
}

// The unhooked-member observation is recorded per proxy: a second proxy over
// the same members, whose AddHook targets a member the first proxy skipped,
// is still observed and that member is still instrumented.
func TestWrapClientDistinctProxiesDistinctTargets(t *testing.T) {
	cfg := new(clientConfig)
	defaults(cfg)

	mt := mocktracer.Start()
	defer mt.Stop()

	a := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1"})
	t.Cleanup(func() { a.Close() })
	b := redis.NewClient(&redis.Options{Addr: "127.0.0.1:2"})
	t.Cleanup(func() { b.Close() })

	// The read-only proxy hooks only a; the write-only proxy hooks only b.
	WrapClient(&selectiveRouter{UniversalClient: a, private: b})
	WrapClient(&writeOnlyRouter{UniversalClient: a, write: b})

	_ = a.Get("foo").Err()
	_ = b.Get("foo").Err()

	spans := commandSpans(mt, cfg.spanName)
	if len(spans) != 2 {
		t.Fatalf("expected 1 command span per member, got %d", len(spans))
	}
	if open := mt.OpenSpans(); len(open) != 0 {
		t.Fatalf("expected no leaked command spans, got %d", len(open))
	}
	ports := map[any]bool{}
	for _, s := range spans {
		ports[s.Tag(ext.TargetPort)] = true
	}
	if !ports["1"] || !ports["2"] {
		t.Fatalf("expected spans tagged with each member's port, got %v", ports)
	}
}

// reentrantLayer is a proxy whose AddHook re-enters WrapClient, for example
// to lazily instrument its delegate.
type reentrantLayer struct {
	redis.UniversalClient
}

func (r *reentrantLayer) AddHook(hook redis.Hook) {
	WrapClient(r.UniversalClient)
}

// A proxy's AddHook may call WrapClient again — from a proxy that lazily
// instruments a delegate, say — and must not deadlock on the package lock.
func TestWrapClientReentrantAddHook(t *testing.T) {
	client := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1"})
	t.Cleanup(func() { client.Close() })

	// Nest beyond the field-walk depth, so the wrap goes through the
	// proxy's own AddHook.
	proxy := &redisDecorator{&redisDecorator{&redisDecorator{&reentrantLayer{client}}}}

	done := make(chan struct{})
	go func() {
		defer close(done)
		WrapClient(proxy)
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("WrapClient deadlocked on a re-entrant AddHook")
	}
}

// hotSwapRouter guards a replaceable delegate with its own mutex.
type hotSwapRouter struct {
	redis.UniversalClient // the read path
	mu                    sync.RWMutex
	write                 redis.UniversalClient // the replaceable write path
}

func (r *hotSwapRouter) AddHook(hook redis.Hook) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.write.AddHook(hook)
}

// The field walk must read a proxy's delegate fields under the proxy's own
// mutex, so a proxy that replaces a delegate while serving traffic does not
// race with WrapClient.
func TestWrapClientSynchronizedProxyFields(t *testing.T) {
	a := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1"})
	t.Cleanup(func() { a.Close() })
	b := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1"})
	t.Cleanup(func() { b.Close() })

	router := &hotSwapRouter{UniversalClient: a, write: b}
	stop := make(chan struct{})
	var wg sync.WaitGroup
	wg.Go(func() {
		for {
			select {
			case <-stop:
				return
			default:
				router.mu.Lock()
				router.write = b
				router.mu.Unlock()
				router.mu.Lock()
				router.write = a
				router.mu.Unlock()
			}
		}
	})
	WrapClient(router)
	WrapClient(router)
	close(stop)
	wg.Wait()
}
