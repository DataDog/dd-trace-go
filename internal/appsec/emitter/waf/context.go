// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package waf

import (
	"context"
	"maps"
	"slices"
	"sync"
	"sync/atomic"

	"github.com/DataDog/appsec-internal-go/limiter"
	waf "github.com/DataDog/go-libddwaf/v3"

	"gopkg.in/DataDog/dd-trace-go.v1/internal/log"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/stacktrace"

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/dyngo"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/emitter/trace"
)

type (
	ContextOperation struct {
		dyngo.Operation
		*trace.ServiceEntrySpanOperation

		// context is an atomic pointer to the current WAF context.
		// Makes sure the calls to context.Run are safe.
		context     atomic.Pointer[waf.Context]
		limiter     limiter.Limiter
		events      []any
		stacks      []*stacktrace.Event
		derivatives map[string]any
		mu          sync.Mutex
	}

	ContextArgs struct {
	}

	ContextRes struct{}

	RunEvent struct {
		waf.RunAddressData
		dyngo.Operation
	}
)

func (ContextArgs) IsArgOf(*ContextOperation)   {}
func (ContextRes) IsResultOf(*ContextOperation) {}

func StartContextOperation(ctx context.Context) (*ContextOperation, context.Context) {
	entrySpanOp, ctx := trace.StartServiceEntrySpanOperation(ctx)
	op := &ContextOperation{
		Operation:                 dyngo.NewOperation(entrySpanOp),
		ServiceEntrySpanOperation: entrySpanOp,
	}
	return op, dyngo.StartAndRegisterOperation(ctx, op, ContextArgs{})
}

func (op *ContextOperation) Finish(span ddtrace.Span) {
	dyngo.FinishOperation(op, ContextRes{})
	op.ServiceEntrySpanOperation.Finish(span)
}

func (op *ContextOperation) SwapContext(ctx *waf.Context) *waf.Context {
	return op.context.Swap(ctx)
}

func (op *ContextOperation) SetLimiter(limiter limiter.Limiter) {
	op.limiter = limiter
}

func (op *ContextOperation) AddEvents(events ...any) {
	if len(events) == 0 {
		return
	}

	if !op.limiter.Allow() {
		log.Warn("appsec: too many Feature events, stopping further reporting")
		return
	}

	op.mu.Lock()
	defer op.mu.Unlock()
	op.events = append(op.events, events...)
}

func (op *ContextOperation) AddStackTraces(stacks ...*stacktrace.Event) {
	if len(stacks) == 0 {
		return
	}

	op.mu.Lock()
	defer op.mu.Unlock()
	op.stacks = append(op.stacks, stacks...)
}

func (op *ContextOperation) AbsorbDerivatives(derivatives map[string]any) {
	if len(derivatives) == 0 {
		return
	}

	op.mu.Lock()
	defer op.mu.Unlock()
	if op.derivatives == nil {
		op.derivatives = make(map[string]any)
	}

	for k, v := range derivatives {
		op.derivatives[k] = v
	}
}

func (op *ContextOperation) Derivatives() map[string]any {
	op.mu.Lock()
	defer op.mu.Unlock()
	return maps.Clone(op.derivatives)
}

func (op *ContextOperation) Events() []any {
	op.mu.Lock()
	defer op.mu.Unlock()
	return slices.Clone(op.events)
}

func (op *ContextOperation) StackTraces() []*stacktrace.Event {
	op.mu.Lock()
	defer op.mu.Unlock()
	return slices.Clone(op.stacks)
}

func (op *ContextOperation) OnEvent(event RunEvent) {
	op.Run(event.Operation, event.RunAddressData)
}
