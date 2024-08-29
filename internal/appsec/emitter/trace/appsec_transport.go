// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package trace

import (
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/dyngo"
)

type (
	SpanTag struct {
		Key   string
		Value any
	}

	ServiceEntrySpanTag struct {
		Key   string
		Value any
	}
)

func RegisterServiceEntrySpan(op dyngo.Operation, span ddtrace.Span) {
	dyngo.OnData(op, func(args ServiceEntrySpanTag) {
		span.SetTag(args.Key, args.Value)
	})
}

func RegisterSpanTag(op dyngo.Operation, span ddtrace.Span) {
	dyngo.OnData(op, func(args SpanTag) {
		span.SetTag(args.Key, args.Value)
	})
}
