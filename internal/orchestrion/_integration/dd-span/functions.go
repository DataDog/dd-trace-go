// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package ddspan

import (
	"context"
	"errors"
	"net/http"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
)

// startOnGoroutine starts a span from a context with no span, so the parent
// comes from goroutine-local storage, and the new span becomes the active span
// there. An instrumented function must not pick it as its parent when it has a
// context or request argument.
func startOnGoroutine(name string) *tracer.Span {
	span, _ := tracer.StartSpanFromContext(context.Background(), name)
	return span
}

//dd:span arg:context
func spanWithContext(ctx context.Context) {
	inner := startOnGoroutine("test.inner")
	defer inner.Finish()
	spanWithRenamedContext(ctx)
}

//dd:span arg:renamed
func spanWithRenamedContext(c context.Context) {}

//dd:span arg:unnamed
func spanWithUnnamedContext(context.Context) {}

//dd:span arg:nil
func spanWithNilContext(ctx context.Context) {}

//dd:span arg:implements
func spanWithImplementingContext(ctx *customCtx) {}

//dd:span arg:request
func spanWithRequest(_ http.ResponseWriter, r *http.Request) {
	inner := startOnGoroutine("test.inner")
	defer inner.Finish()
	spanWithRenamedContext(r.Context())
}

//dd:span arg:both
func spanWithContextAndRequest(ctx context.Context, r *http.Request) {
	spanWithRenamedContext(r.Context())
}

//dd:span result:named
func errorNamed() (err error) {
	return errors.New("named error")
}

//dd:span result:unnamed
func errorUnnamed() (int, error) {
	return 0, errors.New("unnamed error")
}

//dd:span result:deferred
func errorSetInDefer() (err error) {
	defer func() { err = errors.New("deferred error") }()
	return nil
}

//dd:span result:nil
func errorNil() error {
	return nil
}

//dd:span
func spanWithoutArgs() {}

//dd:span a:1 span.name:custom.op b:"two words" span.name:ignored a:override empty:
func spanWithArgs() {}

type receiver struct{}

//dd:span kind:method
func (*receiver) Method(ctx context.Context) {}

//dd:span kind:generic
func spanGeneric[T any](ctx context.Context, v T) T {
	return v
}

// spanWithLocals declares variables with the names a hand-written span would
// use, which the instrumentation must not clash with.
//
//dd:span
func spanWithLocals() string {
	span := "span"
	ctx := "ctx"
	return span + ctx
}

// The functions below each run one function literal annotated in a different
// place. A literal has no function name, so its span is named after the first
// directive argument and has no function-name tag.

func literalDefine() {
	//dd:span literal:define
	op := func() {}
	op()
}

func literalAssign() {
	var op func()
	//dd:span literal:assign
	op = func() {}
	op()
}

func literalReturn() {
	returnLiteral()()
}

func returnLiteral() func() {
	//dd:span literal:return
	return func() {}
}

func literalGo(ctx context.Context) {
	done := make(chan struct{})
	//dd:span literal:go
	go func(ctx context.Context) {
		// Finish the span before the caller goes on and the test stops the
		// tracer. The deferred Finish of the instrumentation then does nothing.
		if span, ok := tracer.SpanFromContext(ctx); ok {
			span.Finish()
		}
		close(done)
	}(ctx)
	<-done
}

func literalDefer() {
	//dd:span literal:defer
	defer func() {}()
}

func literalCall() {
	//dd:span literal:call
	func() {}()
}

func literalSend() {
	ch := make(chan func(), 1)
	//dd:span literal:send
	ch <- func() {}
	(<-ch)()
}

func literalArgument() {
	call(
		//dd:span literal:argument
		func() {},
	)
}

func call(f func()) { f() }

// literalOuter has a span, and so has the literal it annotates. The literal
// that is not annotated has none.
//
//dd:span
func literalOuter() {
	//dd:span literal:inner
	inner := func() {
		func() {}()
	}
	inner()
}

func literalWithContext(ctx context.Context) {
	//dd:span literal:context
	op := func(ctx context.Context) {
		inner := startOnGoroutine("test.inner")
		defer inner.Finish()
		spanWithRenamedContext(ctx)
	}
	op(ctx)
}

func literalWithError() error {
	//dd:span literal:error
	op := func() error {
		return errors.New("literal error")
	}
	return op()
}

// otelc also expands a directive on a var declaration or on a key:value
// element. Orchestrion does not.

//dd:span literal:package
var packageLiteral = func() {}

func literalVar() {
	//dd:span literal:var
	var op = func() {}
	op()
}

func literalKeyValue() {
	ops := map[string]func(){
		//dd:span literal:keyvalue
		"op": func() {},
	}
	ops["op"]()
}
