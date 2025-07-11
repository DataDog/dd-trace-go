// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package waf

import (
	"context"
	"errors"
	"maps"

	"github.com/DataDog/dd-trace-go/v2/appsec/events"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/instrumentation/appsec/dyngo"
	"github.com/DataDog/dd-trace-go/v2/instrumentation/appsec/emitter/waf/actions"
	"github.com/DataDog/dd-trace-go/v2/internal/log"
	"github.com/DataDog/dd-trace-go/v2/internal/samplernames"
	"github.com/DataDog/go-libddwaf/v4"
	"github.com/DataDog/go-libddwaf/v4/waferrors"
)

// Run runs the WAF with the given address data and sends the results to the event receiver
// the event receiver can be the same os the method receiver but not always
// the event receiver is the one that will receive the actions events generated by the WAF
func (op *ContextOperation) Run(eventReceiver dyngo.Operation, addrs libddwaf.RunAddressData) {
	ctx := op.context.Load()
	if ctx == nil { // Context was closed concurrently
		return
	}

	// Remove unsupported addresses in case the listener was registered but some addresses are still unsupported
	// Technically the WAF does this step for us but doing this check before calling the WAF makes us skip encoding huge
	// values that may be discarded by the WAF afterward.
	// e.g. gRPC response body address that is not in the default ruleset but will still be sent to the WAF and may be huge
	for _, addrType := range []map[string]any{addrs.Persistent, addrs.Ephemeral} {
		maps.DeleteFunc(addrType, func(key string, _ any) bool {
			_, ok := op.supportedAddresses[key]
			return !ok
		})
	}

	result, err := ctx.Run(addrs)
	if errors.Is(err, waferrors.ErrTimeout) {
		log.Debug("appsec: WAF timeout value reached: %s", err.Error())
	}

	op.metrics.IncWafError(addrs, err)

	wafTimeout := errors.Is(err, waferrors.ErrTimeout)
	rateLimited := op.AddEvents(result.Events...)
	blocking := actions.SendActionEvents(eventReceiver, result.Actions)
	op.AbsorbDerivatives(result.Derivatives)

	// Set the trace to ManualKeep if the WAF instructed us to keep it.
	if result.Keep {
		op.SetTag(ext.ManualKeep, samplernames.AppSec)
	}

	if result.HasEvents() {
		dyngo.EmitData(op, &SecurityEvent{})
	}

	op.metrics.RegisterWafRun(addrs, result.TimerStats, RequestMilestones{
		requestBlocked: blocking,
		ruleTriggered:  result.HasEvents(),
		wafTimeout:     wafTimeout,
		rateLimited:    rateLimited,
		wafError:       err != nil && !wafTimeout,
	})
}

// RunSimple runs the WAF with the given address data and returns an error that should be forwarded to the caller
func RunSimple(ctx context.Context, addrs libddwaf.RunAddressData, errorLog string) error {
	parent, _ := dyngo.FromContext(ctx)
	if parent == nil {
		log.Error("%s", errorLog)
		return nil
	}

	var err error
	op := dyngo.NewOperation(parent)
	dyngo.OnData(op, func(e *events.BlockingSecurityEvent) {
		err = e
	})
	dyngo.EmitData(op, RunEvent{
		Operation:      op,
		RunAddressData: addrs,
	})
	return err
}
