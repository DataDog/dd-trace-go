// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

// Package mocktracer provides a mock implementation of the tracer used in testing. It
// allows querying spans generated at runtime, without having them actually be sent to
// an agent. It provides a simple way to test that instrumentation is running correctly
// in your application.
//
// Simply call "Start" at the beginning of your tests to start and obtain an instance
// of the mock tracer.
package mocktracer

import (
	"sync/atomic"

	v2 "github.com/DataDog/dd-trace-go/v2/ddtrace/mocktracer"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/datastreams"
)

var _ Tracer = (*mocktracerV2Adapter)(nil)

var (
	active atomic.Value
)

// Tracer exposes an interface for querying the currently running mock tracer.
type Tracer interface {
	// OpenSpans returns the set of started spans that have not been finished yet.
	OpenSpans() []Span

	// FinishedSpans returns the set of finished spans.
	FinishedSpans() []Span
	SentDSMBacklogs() []datastreams.Backlog

	// Reset resets the spans and services recorded in the tracer. This is
	// especially useful when running tests in a loop, where a clean start
	// is desired for FinishedSpans calls.
	Reset()

	// Stop deactivates the mock tracer and allows a normal tracer to take over.
	// It should always be called when testing has finished.
	Stop()
}

type mocktracerV2Adapter struct {
	tracer v2.Tracer
}

// FinishedSpans implements Tracer.
func (mta *mocktracerV2Adapter) FinishedSpans() []Span {
	spans := mta.tracer.FinishedSpans()
	return convertSpans(spans)
}

// OpenSpans implements Tracer.
func (mta *mocktracerV2Adapter) OpenSpans() []Span {
	spans := mta.tracer.OpenSpans()
	return convertSpans(spans)
}

func convertSpans(spans []*v2.Span) []Span {
	ss := make([]Span, len(spans))
	for i, s := range spans {
		ss[i] = MockspanV2Adapter{
			Span: s,
		}
	}
	return ss
}

// Reset implements Tracer.
func (mta *mocktracerV2Adapter) Reset() {
	mta.tracer.Reset()
}

// SentDSMBacklogs implements Tracer.
func (mta *mocktracerV2Adapter) SentDSMBacklogs() []datastreams.Backlog {
	sdb := mta.tracer.SentDSMBacklogs()
	db := make([]datastreams.Backlog, len(sdb))
	for i, b := range sdb {
		db[i] = datastreams.Backlog{
			Tags:  b.Tags,
			Value: b.Value,
		}
	}
	return db
}

// Stop implements Tracer.
func (mta *mocktracerV2Adapter) Stop() {
	mta.tracer.Stop()
	setActive(false)
}

// Start sets the internal tracer to a mock and returns an interface
// which allows querying it. Call Start at the beginning of your tests
// to activate the mock tracer. When your test runs, use the returned
// interface to query the tracer's state.
func Start() Tracer {
	t := v2.Start()
	setActive(true)
	return &mocktracerV2Adapter{tracer: t}
}

func IsActive() bool {
	v := active.Load()
	if v == nil {
		return false
	}
	return v.(bool)
}

func setActive(b bool) {
	active.Store(b)
}
