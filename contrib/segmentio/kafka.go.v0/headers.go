// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package kafka

import (
	"github.com/segmentio/kafka-go"

	"gopkg.in/DataDog/dd-trace-go.v1/contrib/segmentio/kafka.go.v0/internal/tracing"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
)

// ExtractSpanContext retrieves the SpanContext from a kafka.Message
func ExtractSpanContext(msg kafka.Message) (ddtrace.SpanContext, error) {
	return tracer.Extract(tracing.NewMessageCarrier(wrapMessage(&msg)))
}
