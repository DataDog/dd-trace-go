// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package tracer

import (
	v2 "github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/internal"
)

// Propagator implementations should be able to inject and extract
// SpanContexts into an implementation specific carrier.
type Propagator interface {
	// Inject takes the SpanContext and injects it into the carrier.
	Inject(context ddtrace.SpanContext, carrier interface{}) error

	// Extract returns the SpanContext from the given carrier.
	Extract(carrier interface{}) (ddtrace.SpanContext, error)
}

type propagatorV1Adapter struct {
	propagator Propagator
}

// Extract implements tracer.Propagator.
func (pa *propagatorV1Adapter) Extract(carrier interface{}) (*v2.SpanContext, error) {
	ctx, err := pa.propagator.Extract(carrier)
	if err != nil {
		return nil, err
	}
	return ctx.(internal.SpanContextV2Adapter).Ctx, nil
}

// Inject implements tracer.Propagator.
func (pa *propagatorV1Adapter) Inject(context *v2.SpanContext, carrier interface{}) error {
	ctx := internal.SpanContextV2Adapter{Ctx: context}
	return pa.propagator.Inject(ctx, carrier)
}

type propagatorV2Adapter struct {
	propagator v2.Propagator
}

// Extract implements Propagator.
func (pa *propagatorV2Adapter) Extract(carrier interface{}) (ddtrace.SpanContext, error) {
	ctx, err := pa.propagator.Extract(carrier)
	if err != nil {
		return nil, err
	}
	return internal.SpanContextV2Adapter{Ctx: ctx}, nil
}

// Inject implements Propagator.
func (pa *propagatorV2Adapter) Inject(context ddtrace.SpanContext, carrier interface{}) error {
	sca, ok := context.(internal.SpanContextV2Adapter)
	if !ok {
		return internal.ErrInvalidSpanContext
	}
	return pa.propagator.Inject(sca.Ctx, carrier)
}

// TextMapWriter allows setting key/value pairs of strings on the underlying
// data structure. Carriers implementing TextMapWriter are compatible to be
// used with Datadog's TextMapPropagator.
type TextMapWriter interface {
	// Set sets the given key/value pair.
	Set(key, val string)
}

// TextMapReader allows iterating over sets of key/value pairs. Carriers implementing
// TextMapReader are compatible to be used with Datadog's TextMapPropagator.
type TextMapReader interface {
	// ForeachKey iterates over all keys that exist in the underlying
	// carrier. It takes a callback function which will be called
	// using all key/value pairs as arguments. ForeachKey will return
	// the first error returned by the handler.
	ForeachKey(handler func(key, val string) error) error
}

var (
	// ErrInvalidCarrier is returned when the carrier provided to the propagator
	// does not implement the correct interfaces.
	ErrInvalidCarrier = v2.ErrInvalidCarrier

	// ErrInvalidSpanContext is returned when the span context found in the
	// carrier is not of the expected type.
	ErrInvalidSpanContext = internal.ErrInvalidSpanContext

	// ErrSpanContextCorrupted is returned when there was a problem parsing
	// the information found in the carrier.
	ErrSpanContextCorrupted = v2.ErrSpanContextCorrupted

	// ErrSpanContextNotFound represents missing information in the given carrier.
	ErrSpanContextNotFound = v2.ErrSpanContextNotFound
)
