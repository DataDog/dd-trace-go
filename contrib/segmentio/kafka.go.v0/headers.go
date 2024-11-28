// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package kafka

import (
	v2 "github.com/DataDog/dd-trace-go/contrib/segmentio/kafka-go/v2"
	"github.com/segmentio/kafka-go"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
)

// ExtractSpanContext retrieves the SpanContext from a kafka.Message
func ExtractSpanContext(msg kafka.Message) (ddtrace.SpanContext, error) {
	sc, err := v2.ExtractSpanContext(msg)
	if err != nil {
		return nil, err
	}
	return tracer.SpanContextV2Adapter{Ctx: sc}, nil
}
