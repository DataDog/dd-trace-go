// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package valkey_test

import (
	"context"
	"log/slog"

	"github.com/valkey-io/valkey-go"
	valkeytrace "gopkg.in/DataDog/dd-trace-go.v1/contrib/valkey-go"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
)

// To start tracing Valkey, simply create a new client using the library and continue
// using as you normally would.
func Example() {
	tracer.Start()
	defer tracer.Stop()
	vk, err := valkeytrace.NewClient(valkey.ClientOption{
		InitAddress: []string{"localhost:6379"},
	})
	if err != nil {
		slog.Error(err.Error())
		return
	}

	span, ctx := tracer.StartSpanFromContext(context.Background(), "parent.request",
		tracer.SpanType(ext.SpanTypeValkey),
		tracer.ServiceName("web"),
		tracer.ResourceName("/home"),
	)

	if err := vk.Do(ctx, vk.B().Set().Key("key").Value("value").Build()).Error(); err != nil {
		slog.ErrorContext(ctx, "set a value", slog.Any("error", err))
	}

	span.Finish()
}
