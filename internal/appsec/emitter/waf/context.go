// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package waf

import (
	"context"
	"sync"
	"sync/atomic"

	"github.com/DataDog/appsec-internal-go/limiter"
	waf "github.com/DataDog/go-libddwaf/v3"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/log"

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/dyngo"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/emitter/trace"
)

type (
	ContextOperation struct {
		dyngo.Operation
		trace.ServiceEntrySpanOperation

		limiter     limiter.Limiter
		context     atomic.Pointer[waf.Context]
		events      []any
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

func (op *ContextOperation) Start(ctx context.Context) context.Context {
	return dyngo.StartAndRegisterOperation(op.ServiceEntrySpanOperation.Start(ctx), op, ContextArgs{})
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

func (op *ContextOperation) AddEvents(events []any) {
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

func (op *ContextOperation) AbsorbDerivatives(derivatives map[string]any) {
	if len(derivatives) == 0 {
		return
	}

	op.mu.Lock()
	defer op.mu.Unlock()
	for k, v := range derivatives {
		op.derivatives[k] = v
	}
}

func (op *ContextOperation) Derivatives() map[string]any {
	return op.derivatives
}

func (op *ContextOperation) Events() []any {
	return op.events
}

func (op *ContextOperation) OnEvent(event RunEvent) {
	op.Run(event.Operation, event.RunAddressData)
}
