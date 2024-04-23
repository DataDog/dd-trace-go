// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package dyngoapm

import (
	"math"

	"github.com/datadog/dd-trace-go/dyngo"
	"github.com/datadog/dd-trace-go/dyngo/domain"
	"github.com/datadog/dd-trace-go/dyngo/event/perfevent"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
)

type dyngoTracer struct{}

func Register() {
	domain.Performance.RegisterProduct(&dyngoTracer{}, math.MinInt /* before anything else */)
	domain.Performance.Activate()
}

func (d *dyngoTracer) Name() string {
	return "ddtrace"
}

func (d *dyngoTracer) Start(op dyngo.Operation) {
	dyngo.On(op, func(op *perfevent.MonitoredOperation, args perfevent.MonitoredOperationArgs) {
		span, ctx := tracer.StartSpanFromContext(args.Context, args.OperationName, args.Options...)
		dyngo.EmitData(op, ctx)

		dyngo.OnFinish(op, func(_ *perfevent.MonitoredOperation, res perfevent.MonitoredOperationRes) {
			span.Finish(res.Options...)
		})
	})
}
