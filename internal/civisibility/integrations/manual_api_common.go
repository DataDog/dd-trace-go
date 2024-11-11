// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package integrations

import (
	"context"
	"sync"
	"time"

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/civisibility/constants"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/civisibility/utils"
)

// common
var _ ddTslvEvent = (*ciVisibilityCommon)(nil)

// ciVisibilityCommon is a struct that implements the ddTslvEvent interface and provides common functionality for CI visibility.
type ciVisibilityCommon struct {
	startTime time.Time

	tags   []tracer.StartSpanOption
	span   tracer.Span
	ctx    context.Context
	mutex  sync.Mutex
	closed bool
}

// Context returns the context of the event.
func (c *ciVisibilityCommon) Context() context.Context { return c.ctx }

// StartTime returns the start time of the event.
func (c *ciVisibilityCommon) StartTime() time.Time { return c.startTime }

// SetError sets an error on the event.
func (c *ciVisibilityCommon) SetError(options ...ErrorOption) {
	defaults := &tslvErrorOptions{}
	for _, o := range options {
		o(defaults)
	}

	// if there is an error, set the span with the error
	if defaults.err != nil {
		c.span.SetTag(ext.Error, defaults.err)
		return
	}

	// if there is no error, set the span with error the error info

	// set the span with error:1
	c.span.SetTag(ext.Error, true)

	// set the error type
	if defaults.errType != "" {
		c.span.SetTag(ext.ErrorType, defaults.errType)
	}

	// set the error message
	if defaults.message != "" {
		c.span.SetTag(ext.ErrorMsg, defaults.message)
	}

	// set the error stacktrace
	if defaults.callstack != "" {
		c.span.SetTag(ext.ErrorStack, defaults.callstack)
	}
}

// SetTag sets a tag on the event.
func (c *ciVisibilityCommon) SetTag(key string, value interface{}) { c.span.SetTag(key, value) }

// fillCommonTags adds common tags to the span options for CI visibility.
func fillCommonTags(opts []tracer.StartSpanOption) []tracer.StartSpanOption {
	opts = append(opts, []tracer.StartSpanOption{
		tracer.Tag(constants.Origin, constants.CIAppTestOrigin),
		tracer.Tag(ext.ManualKeep, true),
	}...)

	// Apply CI tags
	for k, v := range utils.GetCITags() {
		// Ignore the test session name (sent at the payload metadata level, see `civisibility_payload.go`)
		if k == constants.TestSessionName {
			continue
		}
		opts = append(opts, tracer.Tag(k, v))
	}

	// Apply CI metrics
	for k, v := range utils.GetCIMetrics() {
		opts = append(opts, tracer.Tag(k, v))
	}

	return opts
}
