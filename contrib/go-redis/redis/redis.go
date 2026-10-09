// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

// Package redis provides tracing functions for tracing the go-redis/redis package (https://github.com/go-redis/redis).
// This package supports versions up to go-redis 6.15.
package redis

import (
	"bytes"
	"context"
	"fmt"
	"math"
	"net"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"unsafe"
	"weak"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/instrumentation"

	"github.com/go-redis/redis"
)

const componentName = "go-redis/redis"

var instr *instrumentation.Instrumentation

func init() {
	instr = instrumentation.Load(instrumentation.PackageGoRedis)
}

var (
	// wrapMu serializes WrapClient. Upstream WrapProcess mutates the client's
	// process chain with an unsynchronized assignment, so two concurrent
	// first wraps could race, leave the client half-instrumented, or even
	// strip the tracing wrapper; registration and installation must happen
	// as one critical section.
	wrapMu sync.Mutex
	// wrapped records the configuration of the first WrapClient call for
	// each client, keyed weakly by the client. The configuration holds no
	// reference to the client, and once a wrapped client becomes
	// unreachable a runtime cleanup drops its entry, so the registry holds
	// at most one entry per live client. Without the registry, every
	// WrapClient call would stack another process wrapper on the client and
	// every Redis command would emit one duplicate span per extra wrapper.
	wrapped = map[weak.Pointer[redis.Client]]*clientConfig{}

	// tracedStacks marks, per goroutine, the commands a datadog process
	// wrapper further out on that goroutine is currently driving, so the
	// chain's own datadog wrapper does not start another span for them: a
	// command through a traced handle would otherwise be traced once by the
	// handle's wrapper — with the caller's context — and once more by the
	// chain's wrapper, with the client's context. A process chain is one
	// call chain, so the wrappers of one command run on one goroutine and
	// the mark is goroutine-scoped: concurrent commands on other
	// goroutines never collide with it, however equal their values — two
	// equal value commands are separate operations and each traces once.
	// The mark is also scoped to one client chain — identified by the
	// underlying client's Options pointer, inherited with the process
	// chain by every clone of it —
	// so a user wrapper that retries or fails over by forwarding the same
	// command to a second wrapped client, synchronously, still gets that
	// client's span: the second Process call is its own Redis operation.
	// The key is the command's dynamic type and the interface data word —
	// the command itself when it is pointer-shaped, a pointer to its
	// interface copy otherwise — so a command that cannot be compared,
	// holding a map or a slice, is deduplicated like any other, and
	// commands of different types never collide on a shared boxing
	// address. The hook-based integrations deduplicate by reading hook
	// chains instead, but v6 has no hook chain to read.
	tracedStacks sync.Map // goid -> *[]tracedCmd
)

// cmdKey identifies a command value: its dynamic type and the interface's
// data word, which holds the command itself when it is pointer-shaped and a
// pointer to its interface copy otherwise. The two wrappers of one command
// see the same pair, while commands of different types never collide on a
// shared boxing address — the zero base of zero-sized values included. Two
// commands of the same type share an address only when their values are
// equal, which no API can tell apart.
type cmdKey struct {
	typ  reflect.Type
	word unsafe.Pointer
}

func newCmdKey(cmd redis.Cmder) cmdKey {
	return cmdKey{typ: reflect.TypeOf(cmd), word: (*[2]unsafe.Pointer)(unsafe.Pointer(&cmd))[1]}
}

// tracedCmd identifies one in-flight command: the client chain it runs
// through — identified by the underlying client's Options pointer, which
// every handle of that client shares, a WithContext clone inherits, and a
// raw upstream clone keeps when it is wrapped separately — and the
// command's own identity.
type tracedCmd struct {
	chain *redis.Options
	key   cmdKey
}

// pushTraced records that this goroutine's outermost running datadog
// wrapper is driving the command through chain, and returns the function
// that retires it once the command returns. The stack is per goroutine:
// only the wrappers of this goroutine's own call chain see it.
// tracedStack returns this goroutine's stack of in-flight traced commands,
// creating it when absent, and reports the goroutine id for the retiring
// cleanup. The id is resolved once per wrapper invocation: formatting the
// current stack costs about a microsecond, and a Redis command would
// otherwise pay it three times — the outer check, the push, and the pop.
func tracedStack() (uint64, *[]tracedCmd) {
	id := goid()
	if v, ok := tracedStacks.Load(id); ok {
		return id, v.(*[]tracedCmd)
	}
	p := &[]tracedCmd{}
	tracedStacks.Store(id, p)
	return id, p
}

// pushTraced records that this goroutine's outermost running datadog
// wrapper is driving the command through chain, and returns the function
// that retires it once the command returns. The stack is per goroutine:
// only the wrappers of this goroutine's own call chain see it.
func pushTraced(id uint64, p *[]tracedCmd, chain *redis.Options, key cmdKey) func() {
	*p = append(*p, tracedCmd{chain: chain, key: key})
	return func() {
		// Retire the slot with the stack: goroutine ids are reused and a
		// retained entry would both leak its backing array — keeping the
		// last command reachable — and outlive the goroutine it belonged
		// to, so a service running short-lived goroutines would grow the
		// map without bound.
		last := len(*p) - 1
		(*p)[last] = tracedCmd{}
		*p = (*p)[:last]
		if last == 0 {
			*p = nil
			tracedStacks.Delete(id)
		}
	}
}

// tracedOuter reports whether a datadog wrapper further out on this
// goroutine is currently driving the command through the same client chain.
func tracedOuter(p *[]tracedCmd, chain *redis.Options, key cmdKey) bool {
	return len(*p) > 0 && (*p)[len(*p)-1] == tracedCmd{chain: chain, key: key}
}

// goid returns the current goroutine's id.
func goid() uint64 {
	b := make([]byte, 64)
	b = b[:runtime.Stack(b, false)]
	// The first line reads "goroutine 123 [running]:".
	if len(b) < 11 || string(b[:10]) != "goroutine " {
		return 0
	}
	var id uint64
	for _, c := range b[10:] {
		if c < '0' || c > '9' {
			break
		}
		id = id*10 + uint64(c-'0')
	}
	return id
}

// currentProcess returns the client's current process chain, read through
// the unexported field it lives in, without reassigning it the way upstream
// WrapProcess does: commands in flight read the field without locking, so a
// re-wrap must not write it.
func currentProcess(c *redis.Client) func(cmd redis.Cmder) error {
	v := reflect.ValueOf(c).Elem().FieldByName("baseClient").FieldByName("process")
	if !v.CanInterface() {
		// Unexported field: read it through its address.
		v = reflect.NewAt(v.Type(), unsafe.Pointer(v.UnsafeAddr())).Elem()
	}
	process, _ := reflect.TypeAssert[func(cmd redis.Cmder) error](v)
	return process
}

// sameConfig reports whether two configurations produce the same spans.
func sameConfig(a, b *clientConfig) bool {
	analytics := a.analyticsRate == b.analyticsRate ||
		(math.IsNaN(a.analyticsRate) && math.IsNaN(b.analyticsRate))
	return analytics &&
		a.serviceName == b.serviceName &&
		a.serviceSource == b.serviceSource &&
		a.spanName == b.spanName
}

// Client is used to trace requests to a redis server.
type Client struct {
	*redis.Client
	*params

	process func(cmd redis.Cmder) error
}

var _ redis.Cmdable = (*Client)(nil)

// Pipeliner is used to trace pipelines executed on a Redis server.
type Pipeliner struct {
	redis.Pipeliner
	*params

	ctx context.Context
}

var _ redis.Pipeliner = (*Pipeliner)(nil)

// params holds the tracer and a set of parameters which are recorded with every trace.
type params struct {
	host   string
	port   string
	db     int
	config *clientConfig
	// spanCfg holds the tags that are constant for every command issued
	// through this client (component, span kind, db system, target
	// host/port/db, analytics rate). It is built once in WrapClient and
	// merged into each request via WithStartSpanConfig, instead of
	// rebuilding a Tag() closure per tag on every call.
	spanCfg *tracer.StartSpanConfig
}

// newSpanConfig builds the base StartSpanConfig holding the tags that stay
// constant for every command issued through a client/pipeline with the given
// static attributes, so per-command calls don't need to rebuild them.
func newSpanConfig(host, port string, db int, cfg *clientConfig) *tracer.StartSpanConfig {
	opts := []tracer.StartSpanOption{
		tracer.SpanType(ext.SpanTypeRedis),
		instrumentation.ServiceNameWithSource(cfg.serviceName, cfg.serviceSource),
		tracer.Tag(ext.TargetHost, host),
		tracer.Tag(ext.TargetPort, port),
		tracer.Tag(ext.TargetDB, strconv.Itoa(db)),
		tracer.Tag(ext.Component, componentName),
		tracer.Tag(ext.SpanKind, ext.SpanKindClient),
		tracer.Tag(ext.DBSystem, ext.DBSystemRedis),
		tracer.Tag(ext.RedisDatabaseIndex, db),
	}
	if !math.IsNaN(cfg.analyticsRate) {
		opts = append(opts, tracer.Tag(ext.EventSampleRate, cfg.analyticsRate))
	}
	return tracer.NewStartSpanConfig(opts...)
}

// startSpan starts a span for a single Redis command using p's static span
// tags (spanCfg) plus the command-specific resource/raw-command/args-length
// tags, which vary on every call.
func (p *params) startSpan(ctx context.Context, resource, raw string, argsLength int) *tracer.Span {
	tags := map[string]any{
		ext.ResourceName:    resource,
		"redis.raw_command": raw,
		"redis.args_length": strconv.Itoa(argsLength),
	}
	span, _ := tracer.StartSpanFromContext(ctx, p.config.spanName,
		tracer.WithTags(tags),
		tracer.WithStartSpanConfig(p.spanCfg),
	)
	return span
}

// NewClient returns a new Client that is traced with the default tracer under
// the service name "redis".
func NewClient(opt *redis.Options, opts ...ClientOption) *Client {
	return WrapClient(redis.NewClient(opt), opts...)
}

// WrapClient wraps a given redis.Client with a tracer under the given service name.
// Calling it more than once on the same client is safe: the client is
// instrumented once and the configuration of the first call is kept.
func WrapClient(c *redis.Client, opts ...ClientOption) *Client {
	cfg := new(clientConfig)
	defaults(cfg)
	for _, fn := range opts {
		fn.apply(cfg)
	}
	instr.Logger().Debug("contrib/go-redis/redis: Wrapping Client: %#v", cfg)
	opt := c.Options()
	host, port, err := net.SplitHostPort(opt.Addr)
	if err != nil {
		host = opt.Addr
		port = "6379"
	}

	// Warnings are emitted after the lock is released: a custom logger is
	// user-controlled code and may call WrapClient again from its Log method.
	var warnDuplicate bool
	defer func() {
		if warnDuplicate {
			instr.Logger().Warn("contrib/go-redis/redis: WrapClient called more than once on the same client; keeping the first configuration")
		}
	}()

	wrapMu.Lock()
	defer wrapMu.Unlock()
	key := weak.Make(c)
	if first, ok := wrapped[key]; ok {
		// The client is already instrumented. Keep the current process chain
		// untouched — it may also hold wrappers added by others since the
		// first wrap — and reuse the first configuration, so commands
		// through the returned handle and its WithContext clones trace each
		// command exactly once with that configuration.
		warnDuplicate = !sameConfig(first, cfg)
		params := &params{
			host:   host,
			port:   port,
			db:     opt.DB,
			config: first,
		}
		params.spanCfg = newSpanConfig(host, port, opt.DB, first)
		tc := &Client{Client: c, params: params}
		// Capture the current process chain without touching it: upstream
		// WrapProcess reassigns the client's process even for a no-op
		// callback, which would race with commands in flight. The chain's
		// own datadog wrapper skips its span for commands driven by this
		// handle's clones (see tracedCmds).
		tc.process = currentProcess(c)
		return tc
	}

	params := &params{
		host:   host,
		port:   port,
		db:     opt.DB,
		config: cfg,
	}
	params.spanCfg = newSpanConfig(host, port, opt.DB, cfg)
	tc := &Client{Client: c, params: params}
	// createWrapperFromClient installs the tracing wrapper as the client's
	// process and records the original process on tc.
	c.WrapProcess(createWrapperFromClient(tc))
	// The cleanup is attached to the client: when it becomes unreachable the
	// entry goes with it, even though neither side keeps the other alive.
	wrapped[key] = cfg
	runtime.AddCleanup(c, func(k weak.Pointer[redis.Client]) {
		wrapMu.Lock()
		delete(wrapped, k)
		wrapMu.Unlock()
	}, key)
	return tc
}

// Pipeline creates a Pipeline from a Client
func (c *Client) Pipeline() redis.Pipeliner {
	return &Pipeliner{c.Client.Pipeline(), c.params, c.Client.Context()}
}

// Pipelined executes a function parameter to build a Pipeline and then immediately executes it.
func (c *Client) Pipelined(fn func(redis.Pipeliner) error) ([]redis.Cmder, error) {
	return c.Pipeline().Pipelined(fn)
}

// TxPipelined executes a function parameter to build a Transactional Pipeline and then immediately executes it.
func (c *Client) TxPipelined(fn func(redis.Pipeliner) error) ([]redis.Cmder, error) {
	return c.TxPipeline().Pipelined(fn)
}

// TxPipeline acts like Pipeline, but wraps queued commands with MULTI/EXEC.
func (c *Client) TxPipeline() redis.Pipeliner {
	return &Pipeliner{c.Client.TxPipeline(), c.params, c.Client.Context()}
}

// ExecWithContext calls Pipeline.Exec(). It ensures that the resulting Redis calls
// are traced, and that emitted spans are children of the given Context.
func (c *Pipeliner) ExecWithContext(ctx context.Context) ([]redis.Cmder, error) {
	return c.execWithContext(ctx)
}

// Exec calls Pipeline.Exec() ensuring that the resulting Redis calls are traced.
func (c *Pipeliner) Exec() ([]redis.Cmder, error) {
	return c.execWithContext(c.ctx)
}

func (c *Pipeliner) execWithContext(ctx context.Context) ([]redis.Cmder, error) {
	p := c.params
	tags := map[string]any{ext.ResourceName: "redis"}
	span, _ := tracer.StartSpanFromContext(ctx, p.config.spanName,
		tracer.WithTags(tags),
		tracer.WithStartSpanConfig(p.spanCfg),
	)
	cmds, err := c.Pipeliner.Exec()
	span.SetTag(ext.ResourceName, commandsToString(cmds))
	span.SetTag("redis.pipeline_length", strconv.Itoa(len(cmds)))
	var finishOpts []tracer.FinishOption
	if err != redis.Nil {
		finishOpts = append(finishOpts, tracer.WithError(err))
	}
	span.Finish(finishOpts...)

	return cmds, err
}

// commandsToString returns a string representation of a slice of redis Commands, separated by newlines.
func commandsToString(cmds []redis.Cmder) string {
	var b bytes.Buffer
	for _, cmd := range cmds {
		b.WriteString(cmderToString(cmd))
		b.WriteString("\n")
	}
	return b.String()
}

// Pipelined executes a function parameter to build a Pipeline and then immediately executes the built pipeline.
func (c *Pipeliner) Pipelined(fn func(redis.Pipeliner) error) ([]redis.Cmder, error) {
	if err := fn(c); err != nil {
		return nil, err
	}
	defer c.Close()
	return c.Exec()
}

// WithContext sets a context on a Client. Use it to ensure that emitted spans have the correct parent.
func (c *Client) WithContext(ctx context.Context) *Client {
	clone := &Client{
		Client: c.Client.WithContext(ctx),
		params: c.params,
		// process is left nil so that createWrapperFromClient captures the
		// raw clone's current process chain: wrappers added to the client
		// after this handle was created keep running. The chain's own
		// datadog wrapper skips its span for commands driven by the clone
		// (see tracedCmds), so each command still traces exactly once.
	}
	clone.Client.WrapProcess(createWrapperFromClient(clone))
	return clone
}

// createWrapperFromClient returns a new createWrapper function which wraps the processor with tracing
// information obtained from the provided Client. To understand this functionality better see the
// documentation for the github.com/go-redis/redis.(*baseClient).WrapProcess function.
func createWrapperFromClient(tc *Client) func(oldProcess func(cmd redis.Cmder) error) func(cmd redis.Cmder) error {
	return func(oldProcess func(cmd redis.Cmder) error) func(cmd redis.Cmder) error {
		if tc.process == nil {
			tc.process = oldProcess
		}
		return func(cmd redis.Cmder) error {
			key := newCmdKey(cmd)
			id, stack := tracedStack()
			if tracedOuter(stack, tc.Client.Options(), key) {
				// A datadog wrapper further out on this goroutine, on the
				// same client chain, is driving this command and already
				// started its span for it; see tracedStacks.
				return tc.process(cmd)
			}
			defer pushTraced(id, stack, tc.Client.Options(), key)()
			ctx := tc.Client.Context()
			raw := cmderToString(cmd)
			parts := strings.Split(raw, " ")
			length := len(parts) - 1
			span := tc.params.startSpan(ctx, parts[0], raw, length)
			err := tc.process(cmd)
			var finishOpts []tracer.FinishOption
			if err != redis.Nil {
				finishOpts = append(finishOpts, tracer.WithError(err))
			}
			span.Finish(finishOpts...)
			return err
		}
	}
}

func cmderToString(cmd redis.Cmder) string {
	// We want to support multiple versions of the go-redis library. In
	// older versions Cmder implements the Stringer interface, while in
	// newer versions that was removed, and this String method which
	// sometimes returns an error is used instead. By doing a type assertion
	// we can support both versions.
	switch v := cmd.(type) {
	case fmt.Stringer:
		return v.String()
	case interface{ String() (string, error) }:
		str, err := v.String()
		if err == nil {
			return str
		}
	}
	args := cmd.Args()
	if len(args) == 0 {
		return ""
	}
	if str, ok := args[0].(string); ok {
		return str
	}
	return ""
}
