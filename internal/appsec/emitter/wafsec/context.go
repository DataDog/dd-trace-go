// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package wafsec

import (
	"sync"
	"sync/atomic"

	waf "github.com/DataDog/go-libddwaf/v3"

	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/dyngo"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/emitter/trace"
)

type (
	WAFContextOperation struct {
		dyngo.Operation
		trace.ServiceEntrySpanOperation

		Context atomic.Pointer[waf.Context]
		events  []any
		mu      sync.Mutex
	}

	WAFContextArgs struct {
	}

	WAFContextRes struct{}
)

func (WAFContextArgs) IsArgOf(*WAFContextOperation)   {}
func (WAFContextRes) IsResultOf(*WAFContextOperation) {}

func (op *WAFContextOperation) AddEvents(events []any) {
	op.mu.Lock()
	defer op.mu.Unlock()
	op.events = append(op.events, events...)
}

func (op *WAFContextOperation) Events() []any {
	op.mu.Lock()
	defer op.mu.Unlock()
	eventsCopy := make([]any, len(op.events))
	copy(eventsCopy[:], op.events)
	return eventsCopy
}
