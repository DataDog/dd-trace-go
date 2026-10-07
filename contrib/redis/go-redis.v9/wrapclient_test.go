// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package redis

import (
	"context"
	"reflect"
	"sync"
	"sync/atomic"
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

	if n := datadogHooks(client); n != 1 {
		t.Fatalf("expected exactly 1 datadog hook after 3 wraps, got %d", n)
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

	// Nest beyond the field-walk depth, so the wrap falls back to the
	// proxy's own identity.
	p := redis.UniversalClient(client)
	for range 10 {
		p = &redisDecorator{p}
	}
	WrapClient(p)
	WrapClient(p)

	if n := datadogHooks(client); n != 1 {
		t.Fatalf("expected exactly 1 datadog hook after 2 deep-proxy wraps, got %d", n)
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
	afterFirst := datadogHooks(client)
	WrapClient(router)
	WrapClient(router)
	afterRest := datadogHooks(client)
	if afterRest != afterFirst {
		t.Fatalf("expected the hook chain to stop growing after the first wrap, got %d then %d", afterFirst, afterRest)
	}
	if n := reflect.ValueOf(private).Elem().FieldByName("hooksMixin").FieldByName("slice").Len(); n != 0 {
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

	_ = a.Get(context.Background(), "foo").Err()
	_ = b.Get(context.Background(), "foo").Err()

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
	proxy := redis.UniversalClient(&reentrantLayer{client})
	for range 10 {
		proxy = &redisDecorator{proxy}
	}

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

// selfWrappingLayer re-enters WrapClient for the same proxy that its
// AddHook was called from.
type selfWrappingLayer struct {
	redis.UniversalClient
	self redis.UniversalClient
}

func (r *selfWrappingLayer) AddHook(hook redis.Hook) {
	WrapClient(r.self)
}

// A proxy whose AddHook re-enters WrapClient for that same proxy must not
// wait for its own in-flight install: the outer call closes the marker only
// once AddHook returns.
func TestWrapClientReentrantSameProxy(t *testing.T) {
	client := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1"})
	t.Cleanup(func() { client.Close() })

	layer := &selfWrappingLayer{UniversalClient: client}
	outer := redis.UniversalClient(layer)
	for range 10 {
		outer = &redisDecorator{outer}
	}
	layer.self = outer

	done := make(chan struct{})
	go func() {
		defer close(done)
		WrapClient(outer)
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("WrapClient deadlocked waiting for its own in-flight install")
	}
}

// datadogHooks counts the datadog hooks on a client, ignoring the no-op
// probe hooks that proxy observation leaves behind.
func datadogHooks(client *redis.Client) int {
	hooks := hookSlice(client)
	n := 0
	for i := 0; i < hooks.Len(); i++ {
		if _, ok := hooks.Index(i).Interface().(*datadogHook); ok {
			n++
		}
	}
	return n
}

// retainingProxy keeps the hooks it is given instead of applying them, and
// hands them to delegates it creates later.
type retainingProxy struct {
	redis.UniversalClient
	retained []redis.Hook
}

func (r *retainingProxy) AddHook(hook redis.Hook) {
	r.retained = append(r.retained, hook)
}

func (r *retainingProxy) applyTo(delegate redis.UniversalClient) {
	for _, hook := range r.retained {
		delegate.AddHook(hook)
	}
}

// A proxy that retains hooks for delegates it creates later must receive the
// real hook through its AddHook, not only the observation probe.
func TestWrapClientRetainingProxy(t *testing.T) {
	cfg := new(clientConfig)
	defaults(cfg)

	mt := mocktracer.Start()
	defer mt.Stop()

	current := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1"})
	t.Cleanup(func() { current.Close() })
	proxy := &retainingProxy{UniversalClient: current}
	WrapClient(proxy)

	later := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1"})
	t.Cleanup(func() { later.Close() })
	proxy.applyTo(later)

	_ = later.Get(context.Background(), "foo").Err()

	if spans := commandSpans(mt, cfg.spanName); len(spans) != 1 {
		t.Fatalf("expected the later delegate to be traced, got %d spans", len(spans))
	}
	if n := datadogHooks(later); n != 1 {
		t.Fatalf("expected the later delegate to carry 1 datadog hook, got %d", n)
	}
}

// A proxy may hold its own mutex while calling WrapClient; the field walk
// must not take that mutex while WrapClient holds the package lock.
func TestWrapClientProxyLockOrder(t *testing.T) {
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
				WrapClient(b)
				router.mu.Unlock()
			}
		}
	})
	done := make(chan struct{})
	go func() {
		defer close(done)
		WrapClient(router)
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("WrapClient deadlocked against a proxy holding its own mutex")
	}
	close(stop)
	wg.Wait()
}

// slowAddHookProxy delays its AddHook so another goroutine's wrap is in
// flight while this one runs.
type slowAddHookProxy struct {
	redis.UniversalClient
	delay time.Duration
}

func (r *slowAddHookProxy) AddHook(hook redis.Hook) {
	time.Sleep(r.delay)
	r.UniversalClient.AddHook(hook)
}

// nestedWrapLayer re-enters WrapClient for another proxy from its AddHook.
type nestedWrapLayer struct {
	redis.UniversalClient
	target redis.UniversalClient
}

func (r *nestedWrapLayer) AddHook(hook redis.Hook) {
	WrapClient(r.target)
}

// A nested wrap of a proxy whose install another goroutine started must
// still wait for that install: only the caller's own in-flight install is
// skipped, not every in-flight entry, or the nested wrap returns before the
// proxy is instrumented.
func TestWrapClientNestedWaitsForOtherInstalls(t *testing.T) {
	cfg := new(clientConfig)
	defaults(cfg)

	mt := mocktracer.Start()
	defer mt.Stop()

	b := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1"})
	t.Cleanup(func() { b.Close() })

	deep := redis.UniversalClient(&slowAddHookProxy{UniversalClient: b, delay: 400 * time.Millisecond})
	for range 10 {
		deep = &redisDecorator{deep}
	}
	x := redis.UniversalClient(&nestedWrapLayer{UniversalClient: b, target: deep})
	for range 10 {
		x = &redisDecorator{x}
	}

	installed := make(chan struct{})
	go func() {
		defer close(installed)
		WrapClient(deep)
	}()
	time.Sleep(100 * time.Millisecond)
	WrapClient(x)

	// x's wrap must not have returned before deep's install completed: the
	// command through b must be traced.
	_ = b.Get(context.Background(), "foo").Err()
	if spans := commandSpans(mt, cfg.spanName); len(spans) != 1 {
		t.Fatalf("expected the command to be traced once the wraps returned, got %d spans", len(spans))
	}
	<-installed
	if n := datadogHooks(b); n != 1 {
		t.Fatalf("expected exactly 1 datadog hook on the slow proxy's member, got %d", n)
	}
}

// panickingProxy's AddHook panics, as user code may.
type panickingProxy struct {
	redis.UniversalClient
}

func (r *panickingProxy) AddHook(hook redis.Hook) {
	panic("AddHook panicked")
}

// A panic in a proxy's AddHook — recovered by the application — must not
// leave an in-flight marker that later wraps wait on forever.
func TestWrapClientAddHookPanic(t *testing.T) {
	client := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1"})
	t.Cleanup(func() { client.Close() })
	proxy := &panickingProxy{UniversalClient: client}

	func() {
		defer func() { _ = recover() }()
		WrapClient(proxy)
	}()

	// The marker was dropped, so the later wrap retries instead of waiting
	// forever; the proxy panics on every AddHook, so the retry panics again
	// and the application recovers it too.
	done := make(chan struct{})
	go func() {
		defer close(done)
		defer func() { _ = recover() }()
		WrapClient(proxy)
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("a later wrap waited forever on the marker left by a panicking AddHook")
	}
}

// fanOutRetainProxy applies each hook to its current delegate and retains
// it for delegates it creates later.
type fanOutRetainProxy struct {
	redis.UniversalClient
	retained []redis.Hook
}

func (r *fanOutRetainProxy) AddHook(hook redis.Hook) {
	r.retained = append(r.retained, hook)
	r.UniversalClient.AddHook(hook)
}

func (r *fanOutRetainProxy) applyTo(delegate redis.UniversalClient) {
	for _, hook := range r.retained {
		delegate.AddHook(hook)
	}
}

// A proxy that fans hooks out to its current members and retains them for
// later delegates must receive the real hook through its AddHook: the
// current member is traced once, and a delegate created later is traced too.
func TestWrapClientFanOutRetainProxy(t *testing.T) {
	cfg := new(clientConfig)
	defaults(cfg)

	mt := mocktracer.Start()
	defer mt.Stop()

	current := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1"})
	t.Cleanup(func() { current.Close() })
	proxy := &fanOutRetainProxy{UniversalClient: current}
	WrapClient(proxy)

	if n := datadogHooks(current); n != 1 {
		t.Fatalf("expected the current member to carry exactly 1 datadog hook, got %d", n)
	}

	later := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1"})
	t.Cleanup(func() { later.Close() })
	proxy.applyTo(later)

	_ = later.Get(context.Background(), "foo").Err()
	if spans := commandSpans(mt, cfg.spanName); len(spans) != 1 {
		t.Fatalf("expected the later delegate to be traced exactly once, got %d spans", len(spans))
	}
}

// mutexValueDecorator is a decorator passed by value that carries a mutex.
type mutexValueDecorator struct {
	redis.UniversalClient
	mu sync.RWMutex
}

// A decorator passed by value with a mutex of its own must not make the
// field walk panic on the unaddressable copy.
func TestWrapClientMutexValueDecorator(t *testing.T) {
	client := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1"})
	t.Cleanup(func() { client.Close() })

	WrapClient(mutexValueDecorator{UniversalClient: client})
	WrapClient(mutexValueDecorator{UniversalClient: client})

	if n := datadogHooks(client); n != 1 {
		t.Fatalf("expected exactly 1 datadog hook after 2 wraps, got %d", n)
	}
}

// concreteFanOutProxy stores its delegates as concrete clients and fans every
// hook out to both.
type concreteFanOutProxy struct {
	*redis.Client
	write *redis.Client
}

func (p *concreteFanOutProxy) AddHook(hook redis.Hook) {
	p.Client.AddHook(hook)
	p.write.AddHook(hook)
}

// A proxy whose delegates are concrete clients must not be mistaken for one
// that retains hooks: the probe its AddHook fanned out to a delegate's hook
// slice is not proxy-owned storage. Per-member endpoint tags must survive.
func TestWrapClientConcreteFanOutProxy(t *testing.T) {
	cfg := new(clientConfig)
	defaults(cfg)

	mt := mocktracer.Start()
	defer mt.Stop()

	read := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1"})
	t.Cleanup(func() { read.Close() })
	write := redis.NewClient(&redis.Options{Addr: "127.0.0.1:2"})
	t.Cleanup(func() { write.Close() })

	WrapClient(&concreteFanOutProxy{Client: read, write: write})

	_ = read.Get(context.Background(), "foo").Err()
	_ = write.Get(context.Background(), "foo").Err()

	spans := commandSpans(mt, cfg.spanName)
	if len(spans) != 2 {
		t.Fatalf("expected 1 command span per member, got %d", len(spans))
	}
	ports := map[any]bool{}
	for _, s := range spans {
		ports[s.Tag(ext.TargetPort)] = true
	}
	if !ports["1"] || !ports["2"] {
		t.Fatalf("expected spans tagged with each member's port, got %v", ports)
	}
}

// A proxy may call WrapClient while holding its own mutex. The field walk
// must not block on that mutex — held by the caller's own goroutine, waiting
// would deadlock — and a wrap after the unlock instruments normally.
func TestWrapClientHeldProxyMutex(t *testing.T) {
	a := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1"})
	t.Cleanup(func() { a.Close() })
	b := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1"})
	t.Cleanup(func() { b.Close() })
	router := &hotSwapRouter{UniversalClient: a, write: b}

	router.mu.Lock()
	done := make(chan struct{})
	go func() {
		defer close(done)
		WrapClient(router)
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("WrapClient deadlocked on a proxy mutex held by the caller")
	}
	router.mu.Unlock()

	WrapClient(router)
	// The router's AddHook hooks its write member: that member is
	// instrumented once the mutex is no longer held.
	if n := datadogHooks(b); n != 1 {
		t.Fatalf("expected the write member to carry 1 datadog hook after the unlocked wrap, got %d", n)
	}
}

// A waiter for an install that failed — the installer's AddHook panicked and
// the marker was dropped — must retry the install instead of returning
// without a hook.
func TestWrapClientWaiterRetriesFailedInstall(t *testing.T) {
	cfg := new(clientConfig)
	defaults(cfg)

	mt := mocktracer.Start()
	defer mt.Stop()

	client := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1"})
	t.Cleanup(func() { client.Close() })
	// Nest beyond the field-walk depth, so both wraps go through the
	// proxy's own AddHook.
	inner := &slowPanickingProxy{UniversalClient: client, delay: 300 * time.Millisecond}
	proxy := redis.UniversalClient(inner)
	for range 10 {
		proxy = &redisDecorator{proxy}
	}
	// The first wrap starts the install and panics; a concurrent wrap waits
	// for it and must retry after the marker is dropped.
	var wg sync.WaitGroup
	wg.Go(func() {
		defer func() { _ = recover() }()
		WrapClient(proxy)
	})
	time.Sleep(100 * time.Millisecond)
	wg.Go(func() {
		defer func() { _ = recover() }()
		WrapClient(proxy)
	})
	wg.Wait()

	// Both wraps panicked through the proxy's AddHook — the install was
	// attempted twice, not zero times or once.
	if n := inner.count.Load(); n != 2 {
		t.Fatalf("expected the install to be attempted twice, got %d", n)
	}
}

// slowPanickingProxy's AddHook panics after a delay, so another goroutine's
// wrap waits on the in-flight install before it fails.
type slowPanickingProxy struct {
	redis.UniversalClient
	delay time.Duration
	count atomic.Int32
}

func (r *slowPanickingProxy) AddHook(hook redis.Hook) {
	r.count.Add(1)
	time.Sleep(r.delay)
	panic("AddHook panicked")
}
